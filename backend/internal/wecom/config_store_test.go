package wecom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	if cfg.Stream {
		t.Fatal("Stream = true, want false by default")
	}

	cfg.BotID = "bot-123"
	cfg.BotSecret = "secret1234wxyz"
	cfg.WorkspaceID = "default"
	cfg.AgentID = "claude"
	cfg.Stream = true
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
	if !loaded.Stream {
		t.Fatal("Stream = false, want saved true")
	}
	sanitized := SanitizeConfig(loaded)
	if !sanitized.Stream {
		t.Fatal("Sanitized Stream = false, want true")
	}
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

func TestWeComRuntimeConfigEnvOverrides(t *testing.T) {
	t.Setenv("LUMI_WECOM_STREAM_MAX_AGE", "3s")
	t.Setenv("LUMI_WECOM_STREAM_MAX_BYTES", "1234")
	t.Setenv("LUMI_WECOM_STREAM_MAX_UPDATES", "7")
	t.Setenv("LUMI_WECOM_STREAM_MIN_UPDATE_GAP", "250ms")
	t.Setenv("LUMI_WECOM_STREAM_COALESCE_GAP", "500")
	t.Setenv("LUMI_WECOM_MARKDOWN_TABLE_MODE", "BULLETS")
	t.Setenv("LUMI_WECOM_IR_RENDERER", "off")

	cfg := loadWeComRuntimeConfigFromEnv()
	if cfg.StreamPolicy.MaxAge != 3*time.Second {
		t.Fatalf("MaxAge = %s, want 3s", cfg.StreamPolicy.MaxAge)
	}
	if cfg.StreamPolicy.MaxBytes != 1234 {
		t.Fatalf("MaxBytes = %d, want 1234", cfg.StreamPolicy.MaxBytes)
	}
	if cfg.StreamPolicy.MaxUpdates != 7 {
		t.Fatalf("MaxUpdates = %d, want 7", cfg.StreamPolicy.MaxUpdates)
	}
	if cfg.StreamPolicy.MinUpdateGap != 250*time.Millisecond {
		t.Fatalf("MinUpdateGap = %s, want 250ms", cfg.StreamPolicy.MinUpdateGap)
	}
	if cfg.StreamPolicy.CoalesceGap != 500*time.Millisecond {
		t.Fatalf("CoalesceGap = %s, want 500ms", cfg.StreamPolicy.CoalesceGap)
	}
	if cfg.MarkdownTableMode != "bullets" {
		t.Fatalf("MarkdownTableMode = %q, want bullets", cfg.MarkdownTableMode)
	}
	if cfg.IRRendererEnabled {
		t.Fatal("IRRendererEnabled = true, want false")
	}
}

func TestStoresUseConfiguredInstanceRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LUMI_WECOM_INSTANCE_ID", "cli-sandbox-wecom-test")

	wantRoot := filepath.Join(home, ".lumi", "wecom", "instances", "cli-sandbox-wecom-test")
	if got := NewConfigStore().path; got != filepath.Join(wantRoot, "config.json") {
		t.Fatalf("config path = %q, want instance path", got)
	}
	if got := NewRuntimeStore().path; got != filepath.Join(wantRoot, "runtime.json") {
		t.Fatalf("runtime path = %q, want instance path", got)
	}
	if got := NewConversationStore().baseDir; got != filepath.Join(wantRoot, "sessions") {
		t.Fatalf("conversation dir = %q, want instance path", got)
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
	  "stream": true,
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
	if !cfg.Stream {
		t.Fatal("Stream after save = false, want true")
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
