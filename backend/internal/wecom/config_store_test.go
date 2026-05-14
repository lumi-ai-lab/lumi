package wecom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestConfigStoreDefaultsAndPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store := NewConfigStore()
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Mode != defaultMode {
		t.Fatalf("Mode = %q, want %q", cfg.Mode, defaultMode)
	}
	if cfg.ConnectTimeoutMs != defaultConnectTimeoutMs {
		t.Fatalf("ConnectTimeoutMs = %d, want %d", cfg.ConnectTimeoutMs, defaultConnectTimeoutMs)
	}

	cfg.BotID = "bot-123"
	cfg.BotSecret = "secret1234wxyz"
	cfg.WorkspaceID = "default"
	cfg.AgentID = "claude"
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load(saved) error = %v", err)
	}
	if loaded.BotSecret != cfg.BotSecret {
		t.Fatalf("BotSecret = %q, want %q", loaded.BotSecret, cfg.BotSecret)
	}
	sanitized := SanitizeConfig(loaded)
	if !sanitized.HasSecret {
		t.Fatal("HasSecret = false, want true")
	}
	if sanitized.MaskedSecret != "secr********wxyz" {
		t.Fatalf("MaskedSecret = %q", sanitized.MaskedSecret)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.path)
		if err != nil {
			t.Fatalf("Stat(config) error = %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("config mode = %#o, want %#o", info.Mode().Perm(), os.FileMode(0o600))
		}
	}
}

func TestHandleSaveConfigKeepsAndClearsSecret(t *testing.T) {
	service := newTestService(t, dummyRunner{})
	if err := service.configStore.Save(Config{
		Mode:                "websocket",
		BotID:               "bot-1",
		BotSecret:           "persisted-secret",
		WorkspaceID:         "default",
		AgentID:             "claude",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/wecom/config", strings.NewReader(`{
	  "enabled": false,
	  "mode": "websocket",
	  "botId": "bot-1",
	  "workspaceId": "default",
	  "agentId": "claude",
	  "allowFrom": "",
	  "connectTimeoutMs": 15000,
	  "heartbeatIntervalMs": 30000,
	  "messageAckTimeoutMs": 5000
	}`))
	req.Header.Set("Content-Type", "application/json")
	service.HandleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status omit secret = %d, body=%s", rec.Code, rec.Body.String())
	}
	cfg, err := service.configStore.Load()
	if err != nil {
		t.Fatalf("Load(after omit) error = %v", err)
	}
	if cfg.BotSecret != "persisted-secret" {
		t.Fatalf("BotSecret after omit = %q", cfg.BotSecret)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/wecom/config", strings.NewReader(`{
	  "enabled": false,
	  "mode": "websocket",
	  "botId": "bot-1",
	  "botSecret": "",
	  "workspaceId": "default",
	  "agentId": "claude",
	  "allowFrom": "",
	  "connectTimeoutMs": 15000,
	  "heartbeatIntervalMs": 30000,
	  "messageAckTimeoutMs": 5000
	}`))
	req.Header.Set("Content-Type", "application/json")
	service.HandleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status clear secret = %d, body=%s", rec.Code, rec.Body.String())
	}
	cfg, err = service.configStore.Load()
	if err != nil {
		t.Fatalf("Load(after clear) error = %v", err)
	}
	if cfg.BotSecret != "" {
		t.Fatalf("BotSecret after clear = %q, want empty", cfg.BotSecret)
	}
}

type dummyRunner struct{}

func (dummyRunner) RunWeComChat(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
	return nil
}

func newTestService(t *testing.T, runner ChatRunner) *Service {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := &config.Config{
		Agents: []config.AgentConfig{
			{ID: "claude", Name: "Claude", Command: "echo"},
			{ID: "codex", Name: "Codex", Command: "echo"},
		},
		DefaultAgent: "claude",
		Workspaces: []config.WorkspaceConfig{
			{ID: "default", Name: "Default", Path: home},
			{ID: "sandbox", Name: "Sandbox", Path: home, Kind: "sandbox"},
			{ID: "remote", Name: "Remote", Path: home, Kind: "remote"},
		},
		DefaultWorkspace: "default",
	}
	return NewService(cfg, runner)
}

func TestSaveConfigAllowsSandboxWorkspace(t *testing.T) {
	service := newTestService(t, dummyRunner{})

	_, err := service.SaveConfig(context.Background(), Config{
		Mode:                "websocket",
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "sandbox",
		AgentID:             "claude",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	})
	if err != nil {
		t.Fatalf("SaveConfig(sandbox) error = %v", err)
	}
}

func TestSaveConfigRejectsRemoteWorkspace(t *testing.T) {
	service := newTestService(t, dummyRunner{})

	_, err := service.SaveConfig(context.Background(), Config{
		Mode:                "websocket",
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "remote",
		AgentID:             "claude",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	})
	if err == nil || !strings.Contains(err.Error(), "workspace must be local or sandbox") {
		t.Fatalf("SaveConfig(remote) error = %v, want workspace kind error", err)
	}
}
