package wechat

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
	if cfg.LoginMode != "qr" {
		t.Fatalf("LoginMode = %q, want qr", cfg.LoginMode)
	}
	if cfg.BaseURL != defaultBaseURL {
		t.Fatalf("BaseURL = %q, want %q", cfg.BaseURL, defaultBaseURL)
	}

	cfg.AccountID = "wx-account"
	cfg.BotToken = "abcd1234wxyz"
	cfg.WorkspaceID = "default"
	cfg.AgentID = "claude"
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load(saved) error = %v", err)
	}
	if loaded.BotToken != cfg.BotToken {
		t.Fatalf("BotToken = %q, want %q", loaded.BotToken, cfg.BotToken)
	}
	sanitized := SanitizeConfig(loaded)
	if !sanitized.HasToken {
		t.Fatal("HasToken = false, want true")
	}
	if sanitized.MaskedToken != "abcd********wxyz" {
		t.Fatalf("MaskedToken = %q", sanitized.MaskedToken)
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

func TestHandleSaveConfigKeepsAndClearsToken(t *testing.T) {
	service := newTestService(t, dummyRunner{})
	if err := service.configStore.Save(Config{
		LoginMode:   "manual",
		AccountID:   "wx-account",
		BotToken:    "persisted-token",
		BaseURL:     defaultBaseURL,
		WorkspaceID: "default",
		AgentID:     "claude",
	}); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/wechat/config", strings.NewReader(`{
	  "enabled": false,
	  "loginMode": "manual",
	  "accountId": "wx-account",
	  "baseUrl": "https://ilinkai.weixin.qq.com",
	  "workspaceId": "default",
	  "agentId": "claude"
	}`))
	req.Header.Set("Content-Type", "application/json")
	service.HandleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status omit token = %d, body=%s", rec.Code, rec.Body.String())
	}
	cfg, err := service.configStore.Load()
	if err != nil {
		t.Fatalf("Load(after omit) error = %v", err)
	}
	if cfg.BotToken != "persisted-token" {
		t.Fatalf("BotToken after omit = %q", cfg.BotToken)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/wechat/config", strings.NewReader(`{
	  "enabled": false,
	  "loginMode": "manual",
	  "accountId": "wx-account",
	  "botToken": "",
	  "baseUrl": "https://ilinkai.weixin.qq.com",
	  "workspaceId": "default",
	  "agentId": "claude"
	}`))
	req.Header.Set("Content-Type", "application/json")
	service.HandleHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status clear token = %d, body=%s", rec.Code, rec.Body.String())
	}
	cfg, err = service.configStore.Load()
	if err != nil {
		t.Fatalf("Load(after clear) error = %v", err)
	}
	if cfg.BotToken != "" {
		t.Fatalf("BotToken after clear = %q, want empty", cfg.BotToken)
	}
}

type dummyRunner struct{}

func (dummyRunner) RunWeChatChat(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
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
		LoginMode:   "manual",
		AccountID:   "wx-bot",
		BotToken:    "bot-token",
		BaseURL:     "https://wechat.test",
		WorkspaceID: "sandbox",
		AgentID:     "claude",
	})
	if err != nil {
		t.Fatalf("SaveConfig(sandbox) error = %v", err)
	}
}

func TestSaveConfigRejectsRemoteWorkspace(t *testing.T) {
	service := newTestService(t, dummyRunner{})

	_, err := service.SaveConfig(context.Background(), Config{
		LoginMode:   "manual",
		AccountID:   "wx-bot",
		BotToken:    "bot-token",
		BaseURL:     "https://wechat.test",
		WorkspaceID: "remote",
		AgentID:     "claude",
	})
	if err == nil || !strings.Contains(err.Error(), "workspace must be local or sandbox") {
		t.Fatalf("SaveConfig(remote) error = %v, want workspace kind error", err)
	}
}
