package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/requestercontext"
)

func TestLoadRequesterPolicyBuildsImmutableContexts(t *testing.T) {
	raw := `{
  "version": 1,
  "botId": " bot-demo-001 ",
  "users": [
    {
      "userId": " user-demo-001 ",
      "displayName": " 张三 ",
      "enabled": true,
      "capabilities": [" qdm.cmr.query ", "qdm.metric.query", "qdm.sql.select"],
      "scope": {
        "manageAreaIds": [" CN18 "],
        "dcManageAreaIds": [" CN18 ", "CN20"],
        "categoryLevel1Ids": [" 12 ", "13"]
      }
    },
    {
      "userId": "disabled-user",
      "displayName": "Disabled",
      "enabled": false,
      "capabilities": [],
      "scope": {"manageAreaIds": [], "dcManageAreaIds": [], "categoryLevel1Ids": []}
    }
  ]
}`
	path := writeRequesterPolicyFile(t, raw)
	policy, err := LoadRequesterPolicy(path, "bot-demo-001")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy() error = %v", err)
	}
	if policy.BotID() != "bot-demo-001" {
		t.Fatalf("BotID() = %q", policy.BotID())
	}
	if policy.EnabledUserCount() != 1 {
		t.Fatalf("EnabledUserCount() = %d, want 1", policy.EnabledUserCount())
	}
	hash := sha256.Sum256([]byte(raw))
	wantRevision := "sha256:" + hex.EncodeToString(hash[:])
	if policy.Revision() != wantRevision {
		t.Fatalf("Revision() = %q, want %q", policy.Revision(), wantRevision)
	}

	ctx, ok := policy.BuildContext(" user-demo-001 ", " msg-1 ", " chat-1 ", " group ")
	if !ok {
		t.Fatal("BuildContext(enabled) ok = false")
	}
	if ctx.Version != requestercontext.CurrentVersion || ctx.RequestID != "msg-1" || ctx.PolicyRevision != wantRevision {
		t.Fatalf("context header = %+v", ctx)
	}
	if ctx.Principal.Channel != "wecom" || ctx.Principal.BotID != "bot-demo-001" ||
		ctx.Principal.CanonicalUserID != "user-demo-001" || ctx.Principal.DisplayName != "张三" {
		t.Fatalf("context principal = %+v", ctx.Principal)
	}
	if ctx.Audience.ChatID != "chat-1" || ctx.Audience.ChatType != "group" {
		t.Fatalf("context audience = %+v", ctx.Audience)
	}
	if strings.Join(ctx.Authorization.Capabilities, ",") != "qdm.cmr.query,qdm.metric.query,qdm.sql.select" ||
		strings.Join(ctx.Authorization.Scope.ManageAreaIDs, ",") != "CN18" ||
		strings.Join(ctx.Authorization.Scope.DCManageAreaIDs, ",") != "CN18,CN20" ||
		strings.Join(ctx.Authorization.Scope.CategoryLevel1IDs, ",") != "12,13" {
		t.Fatalf("context authorization = %+v", ctx.Authorization)
	}

	ctx.Authorization.Capabilities[0] = "mutated"
	ctx.Authorization.Scope.DCManageAreaIDs[0] = "mutated"
	again, ok := policy.BuildContext("user-demo-001", "msg-2", "chat-1", "group")
	if !ok || again.Authorization.Capabilities[0] != "qdm.cmr.query" || again.Authorization.Scope.DCManageAreaIDs[0] != "CN18" {
		t.Fatalf("policy snapshot was mutated: %+v", again)
	}
	if _, ok := policy.BuildContext("USER-DEMO-001", "", "", ""); ok {
		t.Fatal("BuildContext(case changed) ok = true, want exact case-sensitive match")
	}
	if _, ok := policy.BuildContext("disabled-user", "", "", ""); ok {
		t.Fatal("BuildContext(disabled) ok = true")
	}
}

func TestLoadRequesterPolicyAcceptsScopeWithoutDCManageAreaIDs(t *testing.T) {
	raw := `{
	  "version": 1,
	  "botId": "bot-1",
	  "users": [{
	    "userId": "u1",
	    "displayName": "Legacy User",
	    "enabled": true,
	    "capabilities": ["qdm.cmr.query"],
	    "scope": {
	      "manageAreaIds": ["CN18"],
	      "categoryLevel1Ids": ["12"]
	    }
	  }]
	}`
	policy, err := LoadRequesterPolicy(writeRequesterPolicyFile(t, raw), "bot-1")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy() error = %v", err)
	}
	ctx, ok := policy.BuildContext("u1", "msg-1", "chat-1", "group")
	if !ok {
		t.Fatal("BuildContext() ok = false")
	}
	if strings.Join(ctx.Authorization.Scope.ManageAreaIDs, ",") != "CN18" {
		t.Fatalf("manageAreaIds = %v", ctx.Authorization.Scope.ManageAreaIDs)
	}
	if len(ctx.Authorization.Scope.DCManageAreaIDs) != 0 {
		t.Fatalf("dcManageAreaIds = %v, want empty", ctx.Authorization.Scope.DCManageAreaIDs)
	}
}

func TestLoadRequesterPolicyRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		bot  string
		want string
	}{
		{name: "unknown top-level field", raw: `{"version":1,"botId":"bot-1","users":[],"extra":true}`, bot: "bot-1", want: "unknown field"},
		{name: "unknown nested field", raw: `{"version":1,"botId":"bot-1","users":[{"userId":"u1","displayName":"U","enabled":false,"capabilities":[],"scope":{"manageAreaIds":[],"dcManageAreaIds":[],"categoryLevel1Ids":[],"extra":true}}]}`, bot: "bot-1", want: "unknown field"},
		{name: "multiple values", raw: `{"version":1,"botId":"bot-1","users":[]} {}`, bot: "bot-1", want: "multiple JSON values"},
		{name: "wrong version", raw: `{"version":2,"botId":"bot-1","users":[]}`, bot: "bot-1", want: "version must be 1"},
		{name: "empty bot", raw: `{"version":1,"botId":" ","users":[]}`, want: "botId is required"},
		{name: "bot mismatch", raw: `{"version":1,"botId":"bot-2","users":[]}`, bot: "bot-1", want: "does not match"},
		{name: "empty user", raw: `{"version":1,"botId":"bot-1","users":[{"userId":" ","enabled":false,"capabilities":[],"scope":{"manageAreaIds":[],"dcManageAreaIds":[],"categoryLevel1Ids":[]}}]}`, bot: "bot-1", want: "userId is required"},
		{name: "duplicate user after trim", raw: `{"version":1,"botId":"bot-1","users":[{"userId":"u1","enabled":false,"capabilities":[],"scope":{"manageAreaIds":[],"dcManageAreaIds":[],"categoryLevel1Ids":[]}},{"userId":" u1 ","enabled":false,"capabilities":[],"scope":{"manageAreaIds":[],"dcManageAreaIds":[],"categoryLevel1Ids":[]}}]}`, bot: "bot-1", want: "duplicate userId"},
		{name: "unknown capability", raw: enabledRequesterPolicyJSON(`["qdm.unknown"]`, `["CN18"]`, `[]`, `["12"]`), bot: "bot-1", want: "unknown capability"},
		{name: "duplicate capability after trim", raw: enabledRequesterPolicyJSON(`["qdm.cmr.query"," qdm.cmr.query "]`, `["CN18"]`, `[]`, `["12"]`), bot: "bot-1", want: "duplicate capability"},
		{name: "enabled without capabilities", raw: enabledRequesterPolicyJSON(`[]`, `["CN18"]`, `[]`, `["12"]`), bot: "bot-1", want: "at least one capability"},
		{name: "enabled without manage areas", raw: enabledRequesterPolicyJSON(`["qdm.cmr.query"]`, `[]`, `[]`, `["12"]`), bot: "bot-1", want: "at least one manageAreaId or dcManageAreaId"},
		{name: "enabled without categories", raw: enabledRequesterPolicyJSON(`["qdm.cmr.query"]`, `["CN18"]`, `[]`, `[]`), bot: "bot-1", want: "at least one categoryLevel1Id"},
		{name: "empty scope value", raw: enabledRequesterPolicyJSON(`["qdm.cmr.query"]`, `[" "]`, `[]`, `["12"]`), bot: "bot-1", want: "must not be empty"},
		{name: "duplicate scope value after trim", raw: enabledRequesterPolicyJSON(`["qdm.cmr.query"]`, `["CN18"," CN18 "]`, `[]`, `["12"]`), bot: "bot-1", want: "duplicate value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadRequesterPolicy(writeRequesterPolicyFile(t, tt.raw), tt.bot)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadRequesterPolicy() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestServiceValidatesRequesterPolicyForSaveAndRuntime(t *testing.T) {
	service := newTestService(t, dummyRunner{})
	validPath := writeRequesterPolicyFile(t, enabledRequesterPolicyJSON(`["qdm.cmr.query"]`, `["CN18"]`, `[]`, `["12"]`))
	cfg := Config{
		Mode:                "websocket",
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "default",
		AgentID:             "claude",
		RequesterConfigPath: validPath,
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}
	if _, err := service.SaveConfig(context.Background(), cfg); err != nil {
		t.Fatalf("SaveConfig(valid policy) error = %v", err)
	}
	if err := service.validateConfigForRuntime(cfg); err != nil {
		t.Fatalf("validateConfigForRuntime(valid policy) error = %v", err)
	}

	cfg.BotID = "another-bot"
	if _, err := service.SaveConfig(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("SaveConfig(bot mismatch) error = %v", err)
	}
	cfg.BotID = "bot-1"
	cfg.RequesterConfigPath = filepath.Join(t.TempDir(), "missing.json")
	if err := service.validateConfigForRuntime(cfg); err == nil || !strings.Contains(err.Error(), "load requester config") {
		t.Fatalf("validateConfigForRuntime(missing policy) error = %v", err)
	}
}

func enabledRequesterPolicyJSON(capabilities, manageAreas, dcManageAreas, categories string) string {
	return `{"version":1,"botId":"bot-1","users":[{"userId":"u1","displayName":"U1","enabled":true,"capabilities":` + capabilities + `,"scope":{"manageAreaIds":` + manageAreas + `,"dcManageAreaIds":` + dcManageAreas + `,"categoryLevel1Ids":` + categories + `}}]}`
}

func writeRequesterPolicyFile(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "requesters.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(requester policy) error = %v", err)
	}
	return path
}
