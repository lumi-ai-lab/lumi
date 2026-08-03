package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/requestercontext"
	"github.com/pengmide/lumi/internal/sessioninstruction"
)

func TestSandboxRequesterContextBridgeDirMatchesAgentEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP process uses a POSIX shell")
	}

	testRoot := t.TempDir()
	npmPrefix := filepath.Join(testRoot, "npm", "prefix")
	capturePath := filepath.Join(testRoot, "agent-context-dir")
	t.Setenv("NPM_CONFIG_PREFIX", npmPrefix)

	cfg := &ExecutorConfig{
		Workspace:   testRoot,
		WorkspaceID: "sandbox-workspace-1",
		Agents: []config.AgentConfig{{
			ID:      "fake-agent",
			Name:    "Fake ACP Agent",
			Command: "sh",
			Args: []string{
				"-c",
				`printf '%s' "$LUMI_REQUESTER_CONTEXT_DIR" > "$CAPTURE_PATH"; IFS= read -r line; printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{}}'`,
			},
			Env: map[string]string{
				"CAPTURE_PATH":                          capturePath,
				requestercontext.EnvRequesterContextDir: filepath.Join(testRoot, "stale"),
			},
		}},
		DefaultAgent: "fake-agent",
	}
	client := NewClient("http://example.test", "token", cfg)
	runner := client.runner

	proc, err := runner.getOrStartAgent("fake-agent", testRoot)
	if err != nil {
		t.Fatalf("getOrStartAgent() error = %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop() })

	gotBytes, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(agent env capture) error = %v", err)
	}
	bridge, err := runner.requesterContextBridge("fake-agent")
	if err != nil {
		t.Fatalf("requesterContextBridge() error = %v", err)
	}
	want := filepath.Join(filepath.Dir(npmPrefix), "requester-context", "sandbox-workspace-1", "fake-agent")
	if got := filepath.Clean(string(gotBytes)); got != bridge.Dir() {
		t.Fatalf("agent %s = %q, bridge.Dir() = %q", requestercontext.EnvRequesterContextDir, got, bridge.Dir())
	}
	if bridge.Dir() != want {
		t.Fatalf("bridge.Dir() = %q, want %q", bridge.Dir(), want)
	}
}

func TestSandboxPromptPublishesMetaAndSessionFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP process uses a POSIX shell")
	}

	testRoot := t.TempDir()
	t.Setenv("NPM_CONFIG_PREFIX", filepath.Join(testRoot, "npm", "prefix"))

	runner := NewClient("http://example.test", "token", &ExecutorConfig{
		WorkspaceID: "sandbox-workspace-1",
	}).runner
	bridge, err := runner.requesterContextBridge("fake-agent")
	if err != nil {
		t.Fatalf("requesterContextBridge() error = %v", err)
	}
	sessionID := "acp/session/1"
	filename, err := requestercontext.SessionFileName(sessionID)
	if err != nil {
		t.Fatalf("SessionFileName() error = %v", err)
	}
	contextPath := filepath.Join(bridge.Dir(), filename)
	requestCapturePath := filepath.Join(testRoot, "prompt-request.json")
	contextPresentPath := filepath.Join(testRoot, "context-present")

	proc := agent.NewProcess(&config.AgentConfig{
		ID:      "fake-agent",
		Name:    "Fake ACP Agent",
		Command: "sh",
		Args: []string{
			"-c",
			`IFS= read -r initialize; printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"_meta":{"lumi":{"sessionInstructions":{"transportVersion":1,"systemPromptAppend":true,"rehydrateOnRestore":true,"turnContext":true}}}}}'; IFS= read -r line; printf '%s\n' "$line" > "$REQUEST_CAPTURE_PATH"; if [ -f "$EXPECTED_CONTEXT_PATH" ]; then printf present > "$CONTEXT_PRESENT_PATH"; fi; printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}'`,
		},
		Env: map[string]string{
			"REQUEST_CAPTURE_PATH":  requestCapturePath,
			"EXPECTED_CONTEXT_PATH": contextPath,
			"CONTEXT_PRESENT_PATH":  contextPresentPath,
		},
	})
	t.Cleanup(func() { _ = proc.Stop() })
	if _, err := proc.Request("initialize", map[string]any{"protocolVersion": 1}); err != nil {
		t.Fatalf("initialize fake ACP process: %v", err)
	}

	requester := bridgeTestRequesterContext()
	profile := sessioninstruction.NewProfile("stable protocol", "Session routing context")
	_, err = runner.promptWithRequesterContext(proc, sessionID, TaskExecutePayload{
		AgentID:            "fake-agent",
		Prompt:             "show revenue",
		InstructionProfile: &profile,
		TurnContext:        "quoted prior history",
		RequesterContext:   &requester,
	})
	if err != nil {
		t.Fatalf("promptWithRequesterContext() error = %v", err)
	}

	if marker, err := os.ReadFile(contextPresentPath); err != nil || string(marker) != "present" {
		t.Fatalf("requester context was not visible while prompt was in flight: marker=%q err=%v", marker, err)
	}
	if _, err := os.Stat(contextPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(context after prompt) error = %v, want not exist", err)
	}

	requestData, err := os.ReadFile(requestCapturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(prompt request) error = %v", err)
	}
	var request struct {
		Method string `json:"method"`
		Params struct {
			SessionID string           `json:"sessionId"`
			Prompt    []map[string]any `json:"prompt"`
			Meta      map[string]any   `json:"_meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal(requestData, &request); err != nil {
		t.Fatalf("json.Unmarshal(prompt request) error = %v\nrequest=%s", err, requestData)
	}
	if request.Method != "session/prompt" || request.Params.SessionID != sessionID {
		t.Fatalf("prompt identity = method %q session %q", request.Method, request.Params.SessionID)
	}
	if len(request.Params.Prompt) != 1 || request.Params.Prompt[0]["text"] != "show revenue" {
		t.Fatalf("prompt content = %#v", request.Params.Prompt)
	}

	wantParams := map[string]any{"_meta": requestercontext.PromptMeta(requester)}
	support := sessioninstruction.Support{
		Transport: sessioninstruction.TransportLumiV1,
		Capability: sessioninstruction.Capability{
			TransportVersion:   sessioninstruction.TransportVersion,
			SystemPromptAppend: true,
			RehydrateOnRestore: true,
			TurnContext:        true,
		},
	}
	if err := sessioninstruction.ApplyProfile(wantParams, support, profile, sessioninstruction.PhasePrompt); err != nil {
		t.Fatal(err)
	}
	if !sessioninstruction.ApplyTurnContext(wantParams, support, "quoted prior history") {
		t.Fatal("expected turn context support")
	}
	wantMeta := wantParams["_meta"]
	wantMetaData, err := json.Marshal(wantMeta)
	if err != nil {
		t.Fatalf("json.Marshal(want meta) error = %v", err)
	}
	var wireWantMeta map[string]any
	if err := json.Unmarshal(wantMetaData, &wireWantMeta); err != nil {
		t.Fatalf("json.Unmarshal(want meta) error = %v", err)
	}
	if !reflect.DeepEqual(request.Params.Meta, wireWantMeta) {
		t.Fatalf("prompt _meta = %#v, want %#v", request.Params.Meta, wireWantMeta)
	}
}

func TestExecutorRequesterContextBridgeUsesDefaultWorkspace(t *testing.T) {
	t.Setenv("NPM_CONFIG_PREFIX", filepath.Join(t.TempDir(), "npm", "prefix"))

	bridge, err := (&Runner{}).requesterContextBridge("claude")
	if err != nil {
		t.Fatalf("requesterContextBridge() error = %v", err)
	}
	if got := filepath.Base(filepath.Dir(bridge.Dir())); got != defaultWorkspace {
		t.Fatalf("bridge workspace segment = %q, want %q", got, defaultWorkspace)
	}
}

func bridgeTestRequesterContext() requestercontext.Context {
	return requestercontext.Context{
		Version:        requestercontext.CurrentContextVersion,
		RequestID:      "wecom-message-1",
		PolicyRevision: "sha256:test-policy",
		Principal: requestercontext.Principal{
			Channel:         "wecom",
			BotID:           "bot-1",
			CanonicalUserID: "user-1",
			DisplayName:     "Test User",
		},
		Audience: requestercontext.Audience{ChatID: "chat-1", ChatType: "group"},
		Authorization: requestercontext.Authorization{
			Capabilities: []string{"com.example.reports.read"},
			Claims: requestercontext.Claims{
				"com.example.reports": json.RawMessage(`{"tenantIds":["tenant-a"]}`),
			},
		},
	}
}

// Compile-time guard against accidentally dropping the runtime variable from
// the set that strips stale host values before the sandbox agent is launched.
func TestRequesterContextEnvIsRuntimeOwned(t *testing.T) {
	if !isLumiRuntimeEnvKey(requestercontext.EnvRequesterContextDir) {
		t.Fatalf("%s must be a Lumi-owned runtime env key", requestercontext.EnvRequesterContextDir)
	}
	if strings.TrimSpace(requestercontext.EnvRequesterContextDir) == "" {
		t.Fatal("requester context env key must not be empty")
	}
}
