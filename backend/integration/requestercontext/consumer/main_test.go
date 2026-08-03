package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	requesterctx "github.com/pengmide/lumi/internal/requestercontext"
)

func TestConsumeAllowsMatchingQDMContext(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	cfg := validConsumerConfig(t)
	writeEnvelope(t, cfg.ContextDir, validEnvelope(now))

	got, err := consume(cfg, now)
	if err != nil {
		t.Fatalf("consume() error = %v", err)
	}
	if !got.Allowed || got.ContextVersion != requesterctx.CurrentContextVersion || got.Capability != defaultCapability || got.PrincipalFingerprint == "" {
		t.Fatalf("decision = %#v", got)
	}
}

func TestConsumeFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		edit func(*requesterctx.Envelope, *consumerConfig)
		want string
	}{
		{
			name: "missing capability",
			edit: func(envelope *requesterctx.Envelope, _ *consumerConfig) {
				envelope.RequesterContext.Authorization.Capabilities = []string{"qdm.indicators.query"}
			},
			want: "required capability",
		},
		{
			name: "workspace mismatch",
			edit: func(envelope *requesterctx.Envelope, _ *consumerConfig) {
				envelope.WorkspaceID = "another-workspace"
			},
			want: "workspace binding mismatch",
		},
		{
			name: "expired envelope",
			edit: func(envelope *requesterctx.Envelope, _ *consumerConfig) {
				envelope.ExpiresAt = now.Add(-time.Second)
			},
			want: "expired",
		},
		{
			name: "out of scope manage area",
			edit: func(_ *requesterctx.Envelope, cfg *consumerConfig) {
				cfg.ManageAreaID = "area-denied"
			},
			want: "outside the authorized scope",
		},
		{
			name: "unknown domain field",
			edit: func(envelope *requesterctx.Envelope, _ *consumerConfig) {
				envelope.RequesterContext.Authorization.Claims[defaultClaimNamespace] = json.RawMessage(`{"schemaVersion":1,"manageAreaIds":["area-demo"],"dcManageAreaIds":[],"categoryLevel1Ids":["category-demo"],"unknown":true}`)
			},
			want: "unknown field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConsumerConfig(t)
			envelope := validEnvelope(now)
			tt.edit(&envelope, &cfg)
			writeEnvelope(t, cfg.ContextDir, envelope)
			_, err := consume(cfg, now)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("consume() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestConsumeReadsOnlyExactSessionFile(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	cfg := validConsumerConfig(t)
	first := validEnvelope(now)
	second := validEnvelope(now)
	second.SessionID = "session-demo-002"
	writeEnvelope(t, cfg.ContextDir, first)
	writeEnvelope(t, cfg.ContextDir, second)
	if err := os.WriteFile(filepath.Join(cfg.ContextDir, "malformed.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := consume(cfg, now); err != nil {
		t.Fatalf("consume() exact session error = %v", err)
	}
}

func TestConsumeRejectsExactSessionBindingMismatch(t *testing.T) {
	now := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	cfg := validConsumerConfig(t)
	envelope := validEnvelope(now)
	envelope.SessionID = "different-on-wire-session"
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := requesterctx.SessionFileName(cfg.SessionID)
	if err := os.WriteFile(filepath.Join(cfg.ContextDir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := consume(cfg, now); err == nil || !strings.Contains(err.Error(), "session binding mismatch") {
		t.Fatalf("consume() error = %v, want exact session mismatch", err)
	}
}

func TestParseConfigUsesSandboxEnvironment(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		requesterctx.EnvRequesterContextDir: dir,
		"LUMI_WORKSPACE_ID":                 "sandbox-workspace-demo",
	}
	cfg, err := parseConfig([]string{"--session-id", " raw-session ", "--manage-area-id", "area-demo", "--category-level1-id", "category-demo"}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if cfg.ContextDir != dir || cfg.SessionID != " raw-session " || cfg.WorkspaceID != "sandbox-workspace-demo" || cfg.AgentID != "pi" {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestParseConfigRequiresSessionID(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{requesterctx.EnvRequesterContextDir: dir, "LUMI_WORKSPACE_ID": "workspace"}
	_, err := parseConfig([]string{"--manage-area-id", "area-demo", "--category-level1-id", "category-demo"}, func(key string) string { return env[key] })
	if err == nil || !strings.Contains(err.Error(), "session ID") {
		t.Fatalf("parseConfig() error = %v, want required session ID", err)
	}
}

func validConsumerConfig(t *testing.T) consumerConfig {
	t.Helper()
	return consumerConfig{
		ContextDir:       t.TempDir(),
		SessionID:        "session-demo-001",
		WorkspaceID:      "sandbox-workspace-demo",
		AgentID:          "pi",
		Capability:       defaultCapability,
		ClaimNamespace:   defaultClaimNamespace,
		ManageAreaID:     "area-demo",
		CategoryLevel1ID: "category-demo",
	}
}

func validEnvelope(now time.Time) requesterctx.Envelope {
	return requesterctx.Envelope{
		Version:     requesterctx.CurrentEnvelopeVersion,
		WorkspaceID: "sandbox-workspace-demo",
		AgentID:     "pi",
		SessionID:   "session-demo-001",
		IssuedAt:    now.Add(-time.Minute),
		ExpiresAt:   now.Add(29 * time.Minute),
		RequesterContext: requesterctx.Context{
			Version:        requesterctx.CurrentContextVersion,
			RequestID:      "message-demo-001",
			PolicyRevision: "sha256:" + strings.Repeat("a", 64),
			Principal: requesterctx.Principal{
				Channel:         "wecom",
				BotID:           "bot-demo-001",
				CanonicalUserID: "user-demo-allowed",
				DisplayName:     "Allowed Demo User",
			},
			Audience: requesterctx.Audience{ChatID: "chat-demo-001", ChatType: "group"},
			Authorization: requesterctx.Authorization{
				Capabilities: []string{defaultCapability, "qdm.indicators.query"},
				Claims: requesterctx.Claims{
					defaultClaimNamespace: json.RawMessage(`{"schemaVersion":1,"manageAreaIds":["area-demo"],"dcManageAreaIds":["dc-area-demo"],"categoryLevel1Ids":["category-demo"]}`),
				},
			},
		},
	}
}

func writeEnvelope(t *testing.T, dir string, envelope requesterctx.Envelope) {
	t.Helper()
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal(envelope) error = %v", err)
	}
	name, err := requesterctx.SessionFileName(envelope.SessionID)
	if err != nil {
		t.Fatalf("SessionFileName() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("os.WriteFile(envelope) error = %v", err)
	}
}
