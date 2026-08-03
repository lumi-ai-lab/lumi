package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/requestercontext"
)

func TestLoadRequesterPolicyBuildsImmutableContexts(t *testing.T) {
	raw := `{
  "version": 2,
  "botId": " bot-demo-001 ",
  "users": [
    {
      "userId": " user-demo-001 ",
      "displayName": " 张三 ",
      "enabled": true,
      "authorization": {
        "capabilities": [" com.example.reports.read ", "com.example.reports.export"],
        "claims": {
          "com.example.reports": {
            "schemaVersion": 1,
            "tenantIds": ["tenant-a", "tenant-b"],
            "domainOwnedField": true
          }
        }
      }
    },
    {
      "userId": "disabled-user",
      "displayName": "Disabled",
      "enabled": false,
      "authorization": {"capabilities": [], "claims": {}}
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
	if ctx.Version != requestercontext.CurrentContextVersion || ctx.RequestID != "msg-1" || ctx.PolicyRevision != wantRevision {
		t.Fatalf("context header = %+v", ctx)
	}
	if ctx.Principal.Channel != "wecom" || ctx.Principal.BotID != "bot-demo-001" ||
		ctx.Principal.CanonicalUserID != "user-demo-001" || ctx.Principal.DisplayName != "张三" {
		t.Fatalf("context principal = %+v", ctx.Principal)
	}
	if ctx.Audience.ChatID != "chat-1" || ctx.Audience.ChatType != "group" {
		t.Fatalf("context audience = %+v", ctx.Audience)
	}
	if strings.Join(ctx.Authorization.Capabilities, ",") != "com.example.reports.read,com.example.reports.export" {
		t.Fatalf("context capabilities = %+v", ctx.Authorization.Capabilities)
	}
	claim := decodeClaimObject(t, ctx.Authorization.Claims["com.example.reports"])
	if claim["schemaVersion"] != float64(1) || claim["domainOwnedField"] != true {
		t.Fatalf("context claim = %#v", claim)
	}

	ctx.Authorization.Capabilities[0] = "com.example.changed.read"
	ctx.Authorization.Claims["com.example.reports"][0] = 'X'
	again, ok := policy.BuildContext("user-demo-001", "msg-2", "chat-1", "group")
	if !ok || again.Authorization.Capabilities[0] != "com.example.reports.read" || again.Authorization.Claims["com.example.reports"][0] != '{' {
		t.Fatalf("policy snapshot was mutated: %+v", again)
	}
	if _, ok := policy.BuildContext("USER-DEMO-001", "", "", ""); ok {
		t.Fatal("BuildContext(case changed) ok = true, want exact case-sensitive match")
	}
	if _, ok := policy.BuildContext("disabled-user", "", "", ""); ok {
		t.Fatal("BuildContext(disabled) ok = true")
	}
}

func TestLoadRequesterPolicyNormalizesMissingClaimsToObject(t *testing.T) {
	raw := `{
      "version": 2,
      "botId": "bot-1",
      "users": [{
        "userId": "u1",
        "displayName": "No Claims",
        "enabled": true,
        "authorization": {"capabilities": ["com.example.status.read"]}
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
	data, err := json.Marshal(ctx.Authorization)
	if err != nil {
		t.Fatalf("json.Marshal(Authorization) error = %v", err)
	}
	if string(data) != `{"capabilities":["com.example.status.read"],"claims":{}}` {
		t.Fatalf("authorization JSON = %s", data)
	}
}

func TestLoadRequesterPolicyRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		bot  string
		want string
	}{
		{name: "unknown top-level field", raw: `{"version":2,"botId":"bot-1","users":[],"extra":true}`, bot: "bot-1", want: "unknown field"},
		{name: "unknown user field", raw: `{"version":2,"botId":"bot-1","users":[{"userId":"u1","enabled":false,"authorization":{"capabilities":[],"claims":{}},"extra":true}]}`, bot: "bot-1", want: "unknown field"},
		{name: "unknown authorization field", raw: `{"version":2,"botId":"bot-1","users":[{"userId":"u1","enabled":false,"authorization":{"capabilities":[],"claims":{},"scope":{}}}]}`, bot: "bot-1", want: "unknown field"},
		{name: "multiple values", raw: `{"version":2,"botId":"bot-1","users":[]} {}`, bot: "bot-1", want: "multiple JSON values"},
		{name: "legacy version", raw: `{"version":1,"botId":"bot-1","users":[]}`, bot: "bot-1", want: "version must be 2"},
		{name: "empty bot", raw: `{"version":2,"botId":" ","users":[]}`, want: "botId is required"},
		{name: "bot mismatch", raw: `{"version":2,"botId":"bot-2","users":[]}`, bot: "bot-1", want: "does not match"},
		{name: "empty user", raw: `{"version":2,"botId":"bot-1","users":[{"userId":" ","enabled":false,"authorization":{"capabilities":[],"claims":{}}}]}`, bot: "bot-1", want: "userId is required"},
		{name: "duplicate user after trim", raw: `{"version":2,"botId":"bot-1","users":[{"userId":"u1","enabled":false,"authorization":{"capabilities":[],"claims":{}}},{"userId":" u1 ","enabled":false,"authorization":{"capabilities":[],"claims":{}}}]}`, bot: "bot-1", want: "duplicate userId"},
		{name: "capability without namespace", raw: enabledRequesterPolicyJSON(`["reports-read"]`, `{}`), bot: "bot-1", want: "invalid namespaced capability"},
		{name: "uppercase capability", raw: enabledRequesterPolicyJSON(`["Com.Example.Read"]`, `{}`), bot: "bot-1", want: "invalid namespaced capability"},
		{name: "duplicate capability after trim", raw: enabledRequesterPolicyJSON(`["com.example.read"," com.example.read "]`, `{}`), bot: "bot-1", want: "duplicate capability"},
		{name: "enabled without capabilities", raw: enabledRequesterPolicyJSON(`[]`, `{"com.example.reports":{}}`), bot: "bot-1", want: "at least one capability"},
		{name: "claim namespace without dot", raw: enabledRequesterPolicyJSON(`["com.example.read"]`, `{"reports":{}}`), bot: "bot-1", want: "invalid namespace"},
		{name: "claim namespace with whitespace", raw: enabledRequesterPolicyJSON(`["com.example.read"]`, `{" com.example.reports ":{}}`), bot: "bot-1", want: "invalid namespace"},
		{name: "array claim", raw: enabledRequesterPolicyJSON(`["com.example.read"]`, `{"com.example.reports":[]}`), bot: "bot-1", want: "must be a JSON object"},
		{name: "null claim", raw: enabledRequesterPolicyJSON(`["com.example.read"]`, `{"com.example.reports":null}`), bot: "bot-1", want: "must be a JSON object"},
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
	validPath := writeRequesterPolicyFile(t, enabledRequesterPolicyJSON(`["com.example.reports.read"]`, `{"com.example.reports":{"tenantIds":["tenant-a"]}}`))
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

func enabledRequesterPolicyJSON(capabilities, claims string) string {
	return `{"version":2,"botId":"bot-1","users":[{"userId":"u1","displayName":"U1","enabled":true,"authorization":{"capabilities":` + capabilities + `,"claims":` + claims + `}}]}`
}

func decodeClaimObject(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal(claim) error = %v, raw = %s", err, raw)
	}
	return result
}

func claimStringValues(t *testing.T, claims requestercontext.Claims, namespace, field string) []string {
	t.Helper()
	claim := decodeClaimObject(t, claims[namespace])
	values, ok := claim[field].([]any)
	if !ok {
		t.Fatalf("claim %q field %q = %#v, want array", namespace, field, claim[field])
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("claim %q field %q value[%d] = %#v, want string", namespace, field, i, value)
		}
		result[i] = text
	}
	return result
}

func writeRequesterPolicyFile(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "requesters.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(requester policy) error = %v", err)
	}
	return path
}
