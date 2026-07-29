package wecom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	lumicron "github.com/pengmide/lumi/internal/cron"
)

func TestRunCronJobRejectsBeforeRunnerWhenRequesterPolicyEnabled(t *testing.T) {
	runner := &requesterPolicyCountingRunner{}
	service := newTestService(t, runner)
	policyPath := writeRequesterPolicyFile(t, enabledRequesterPolicyJSON(
		`["qdm.cmr.query"]`,
		`["CN18"]`,
		`["12"]`,
	))
	if err := service.configStore.Save(requesterPolicyServiceConfig("default", policyPath)); err != nil {
		t.Fatalf("Save(config) error = %v", err)
	}
	// Make all post-policy preconditions satisfiable so a missing policy guard
	// would reach the runner rather than failing because the socket is absent.
	service.runtime = &wsRuntime{}

	conversationID, err := service.RunCronJob(context.Background(), lumicron.Job{
		ConversationID: "conversation-1",
		WorkspaceID:    "default",
		AgentID:        "claude",
		Prompt:         "run scheduled query",
		Target: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ChatID:   "chat-1",
			ChatType: "group",
			UserID:   "u1",
		}},
	})
	if conversationID != "conversation-1" {
		t.Fatalf("conversationID = %q, want conversation-1", conversationID)
	}
	if err == nil || !strings.Contains(err.Error(), "cron jobs are disabled while requester permissions are enabled") {
		t.Fatalf("RunCronJob() error = %v, want requester policy cron rejection", err)
	}
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("runner calls = %d, want 0", got)
	}
}

func TestRunCronJobKeepsRejectingWhenRunningSnapshotIsStrictAfterConfigClear(t *testing.T) {
	runner := &requesterPolicyCountingRunner{}
	service := newTestService(t, runner)
	policyPath := writeRequesterPolicyFile(t, enabledRequesterPolicyJSON(
		`["qdm.cmr.query"]`,
		`["CN18"]`,
		`["12"]`,
	))
	policy, err := LoadRequesterPolicy(policyPath, "bot-1")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy() error = %v", err)
	}
	if err := service.configStore.Save(requesterPolicyServiceConfig("default", "")); err != nil {
		t.Fatalf("Save(cleared config) error = %v", err)
	}
	service.runtime = &wsRuntime{requesterPolicy: policy}

	_, err = service.RunCronJob(context.Background(), lumicron.Job{
		ConversationID: "conversation-1",
		WorkspaceID:    "default",
		AgentID:        "claude",
		Prompt:         "run scheduled query",
		Target: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ChatID: "chat-1",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "cron jobs are disabled while requester permissions are enabled") {
		t.Fatalf("RunCronJob() error = %v, want running snapshot rejection", err)
	}
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("runner calls = %d, want 0", got)
	}
}

func TestServiceRejectsSandboxRequesterConfigInsideWorkspace(t *testing.T) {
	t.Run("direct path", func(t *testing.T) {
		service, workspace := newSandboxRequesterPolicyService(t)
		policyPath := filepath.Join(workspace, "requesters.json")
		writeRequesterPolicyAt(t, policyPath, requesterPolicyJSONForUser("u1"))

		_, err := service.SaveConfig(context.Background(), requesterPolicyServiceConfig("sandbox", policyPath))
		if err == nil || !strings.Contains(err.Error(), "requester config must be outside the sandbox workspace") {
			t.Fatalf("SaveConfig() error = %v, want sandbox requester config location rejection", err)
		}
	})

	t.Run("symlink outside workspace targets file inside", func(t *testing.T) {
		service, workspace := newSandboxRequesterPolicyService(t)
		target := filepath.Join(workspace, "requesters.json")
		writeRequesterPolicyAt(t, target, requesterPolicyJSONForUser("u1"))
		link := filepath.Join(t.TempDir(), "requesters-link.json")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("Symlink() is unavailable: %v", err)
		}

		_, err := service.SaveConfig(context.Background(), requesterPolicyServiceConfig("sandbox", link))
		if err == nil || !strings.Contains(err.Error(), "requester config must be outside the sandbox workspace") {
			t.Fatalf("SaveConfig() error = %v, want resolved symlink location rejection", err)
		}
	})
}

func TestServiceAllowsSandboxRequesterConfigOutsideWorkspace(t *testing.T) {
	service, _ := newSandboxRequesterPolicyService(t)
	policyPath := filepath.Join(t.TempDir(), "requesters.json")
	writeRequesterPolicyAt(t, policyPath, requesterPolicyJSONForUser("u1"))

	saved, err := service.SaveConfig(context.Background(), requesterPolicyServiceConfig("sandbox", policyPath))
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if saved.RequesterConfigPath != policyPath {
		t.Fatalf("RequesterConfigPath = %q, want %q", saved.RequesterConfigPath, policyPath)
	}
}

func TestStartKeepsRequesterPolicySnapshotUntilRestart(t *testing.T) {
	upgrader := websocket.Upgrader{}
	subscribed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("ReadJSON(subscribe) error = %v", err)
			return
		}
		response, err := json.Marshal(wsFrame{ErrCode: intPtr(0)})
		if err != nil {
			t.Errorf("Marshal(subscribe response) error = %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
			t.Errorf("WriteMessage(subscribe response) error = %v", err)
			return
		}
		close(subscribed)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	originalEndpoint := wsEndpoint
	originalDialer := wsDialer
	wsEndpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	wsDialer = websocket.DefaultDialer
	t.Cleanup(func() {
		wsEndpoint = originalEndpoint
		wsDialer = originalDialer
	})

	service := newTestService(t, &noopChatRunner{})
	policyPath := filepath.Join(t.TempDir(), "requesters.json")
	writeRequesterPolicyAt(t, policyPath, requesterPolicyJSONForUser("u1"))
	cfg := requesterPolicyServiceConfig("default", policyPath)
	if err := service.configStore.Save(cfg); err != nil {
		t.Fatalf("Save(config) error = %v", err)
	}
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Stop() })

	select {
	case <-subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not subscribe")
	}
	runtime := service.currentRuntime()
	if runtime == nil || runtime.requesterPolicy == nil {
		t.Fatal("running service requester policy snapshot = nil")
	}
	startedRevision := runtime.requesterPolicy.Revision()

	writeRequesterPolicyAt(t, policyPath, requesterPolicyJSONForUser("u2"))
	reloaded, err := LoadRequesterPolicy(policyPath, "bot-1")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy(updated file) error = %v", err)
	}
	if reloaded.Revision() == startedRevision {
		t.Fatal("updated policy revision did not change")
	}
	if _, ok := reloaded.BuildContext("u2", "msg-new", "chat-1", "group"); !ok {
		t.Fatal("updated file does not authorize u2")
	}

	if runtime.requesterPolicy.Revision() != startedRevision {
		t.Fatalf("running revision = %q, want startup revision %q", runtime.requesterPolicy.Revision(), startedRevision)
	}
	if _, ok := runtime.requesterPolicy.BuildContext("u1", "msg-old", "chat-1", "group"); !ok {
		t.Fatal("running snapshot stopped authorizing startup user u1")
	}
	if _, ok := runtime.requesterPolicy.BuildContext("u2", "msg-new", "chat-1", "group"); ok {
		t.Fatal("running snapshot authorized user u2 added after startup")
	}

	if err := service.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

type requesterPolicyCountingRunner struct {
	calls atomic.Int32
}

func (r *requesterPolicyCountingRunner) RunWeComChat(context.Context, ChatRunInput, ChatEventSink) error {
	r.calls.Add(1)
	return nil
}

func newSandboxRequesterPolicyService(t *testing.T) (*Service, string) {
	t.Helper()
	service := newTestService(t, dummyRunner{})
	workspace := filepath.Join(t.TempDir(), "harness-data")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	for i := range service.config.Workspaces {
		if service.config.Workspaces[i].ID == "sandbox" {
			service.config.Workspaces[i].Path = workspace
		}
	}
	return service, workspace
}

func requesterPolicyServiceConfig(workspaceID, policyPath string) Config {
	return Config{
		Mode:                defaultMode,
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         workspaceID,
		AgentID:             "claude",
		RequesterConfigPath: policyPath,
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}
}

func requesterPolicyJSONForUser(userID string) string {
	return `{"version":1,"botId":"bot-1","users":[{"userId":"` + userID + `","displayName":"` + userID + `","enabled":true,"capabilities":["qdm.cmr.query"],"scope":{"manageAreaIds":["CN18"],"categoryLevel1Ids":["12"]}}]}`
}

func writeRequesterPolicyAt(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(policy parent) error = %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(policy) error = %v", err)
	}
}
