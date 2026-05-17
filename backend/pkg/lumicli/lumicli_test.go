package lumicli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/wechat"
)

func TestEnsureConfigFileCreatesExampleConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}

	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}

	data, err := os.ReadFile(state.Path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"id": "claude"`) || !strings.Contains(text, `"id": "codex"`) || !strings.Contains(text, `"id": "qwen"`) {
		t.Fatalf("saved config missing example agents: %s", text)
	}
	if !state.Exists {
		t.Fatal("state.Exists = false, want true")
	}
	if !state.HasAgents {
		t.Fatal("state.HasAgents = false, want true")
	}
}

func TestEnsureConfigFileDoesNotRewriteExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".lumi", "lumi.config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	original := `{
  "customTopLevel": "keep-me",
  "agents": [
    {
      "id": "claude",
      "name": "Claude Code",
      "command": "npx"
    }
  ],
  "defaultAgent": "claude"
}
`
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	state, err := ResolveConfigState(configPath)
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != original {
		t.Fatalf("config was rewritten:\n%s", data)
	}
}

func TestAgentIDsReturnsExistingAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".lumi", "lumi.config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	original := `{
  "agents": [
    {"id": "claude", "name": "Claude Code", "command": "npx"},
    {"id": "codex", "name": "Codex CLI", "command": "npx"},
    {"id": "qwen", "name": "Qwen Code", "command": "npx"}
  ],
  "defaultAgent": "claude"
}
`
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	state, err := ResolveConfigState(configPath)
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}

	got := strings.Join(AgentIDs(state), ",")
	if got != "claude,codex,qwen" {
		t.Fatalf("AgentIDs() = %q, want %q", got, "claude,codex,qwen")
	}
	if !HasAgent(state, "claude") {
		t.Fatal("HasAgent(claude) = false, want true")
	}
	if HasAgent(state, "missing") {
		t.Fatal("HasAgent(missing) = true, want false")
	}
}

func TestPrepareRunUpsertsWorkspaceAndWecomConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
		{ID: "codex", Name: "Codex CLI", Command: "npx"},
		{ID: "qwen", Name: "Qwen Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, resolved, err := PrepareRun(state, RunOptions{
		Workspace: workspace,
		AgentID:   "claude",
		BotID:     "bot-123",
		BotSecret: "secret-456",
		Port:      "3344",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	if resolved != workspace {
		t.Fatalf("resolved workspace = %q, want %q", resolved, workspace)
	}
	ws := cfg.FindWorkspace(WorkspaceID)
	if ws == nil {
		t.Fatal("workspace cli-local not found")
	}
	if ws.Path != workspace {
		t.Fatalf("workspace path = %q, want %q", ws.Path, workspace)
	}
	if cfg.DefaultWorkspace != WorkspaceID {
		t.Fatalf("default workspace = %q, want %q", cfg.DefaultWorkspace, WorkspaceID)
	}
	if got := strings.Join(ws.Agents, ","); got != "claude,codex,qwen" {
		t.Fatalf("workspace agents = %q, want claude,codex,qwen", got)
	}
	if cfg.PublicServerURL != "http://127.0.0.1:3344" {
		t.Fatalf("public server URL = %q, want http://127.0.0.1:3344", cfg.PublicServerURL)
	}

	wecomData, err := os.ReadFile(filepath.Join(home, ".lumi", "wecom", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wecom) error = %v", err)
	}
	text := string(wecomData)
	if !strings.Contains(text, `"enabled": true`) || !strings.Contains(text, `"agentId": "claude"`) {
		t.Fatalf("wecom config missing expected fields: %s", text)
	}
}

func TestPrepareRunUsesExplicitWorkspaceAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
		{ID: "codex", Name: "Codex CLI", Command: "npx"},
		{ID: "qwen", Name: "Qwen Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, _, err := PrepareRun(state, RunOptions{
		Workspace: workspace,
		AgentID:   "codex",
		AgentIDs:  []string{"claude", "codex", "codex"},
		BotID:     "bot-123",
		BotSecret: "secret-456",
	})
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	ws := cfg.FindWorkspace(WorkspaceID)
	if got := strings.Join(ws.Agents, ","); got != "claude,codex" {
		t.Fatalf("workspace agents = %q, want claude,codex", got)
	}

	_, _, err = PrepareRun(state, RunOptions{
		Workspace: workspace,
		AgentID:   "qwen",
		AgentIDs:  []string{"claude", "codex"},
		BotID:     "bot-123",
		BotSecret: "secret-456",
	})
	if err == nil || !strings.Contains(err.Error(), "default agent qwen must be included in --agents") {
		t.Fatalf("PrepareRun(missing default) error = %v, want default inclusion error", err)
	}
}

func TestPrepareRunUpsertsSandboxWorkspaceAndWecomConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, _, err := PrepareRun(state, RunOptions{
		Workspace: workspace,
		Kind:      "sandbox",
		AgentID:   "claude",
		BotID:     "bot-123",
		BotSecret: "secret-456",
	})
	if err != nil {
		t.Fatalf("PrepareRun(sandbox) error = %v", err)
	}
	ws := cfg.FindWorkspace(SandboxWorkspaceID)
	if ws == nil {
		t.Fatal("workspace cli-sandbox not found")
	}
	if ws.Kind != "sandbox" {
		t.Fatalf("workspace kind = %q, want sandbox", ws.Kind)
	}
	if ws.Image == "" {
		t.Fatalf("sandbox defaults not set: image=%q idle=%d", ws.Image, ws.IdleTimeoutSec)
	}
	if ws.IdleTimeoutSec != IMSandboxIdleTimeoutSec {
		t.Fatalf("sandbox idle timeout = %d, want %d", ws.IdleTimeoutSec, IMSandboxIdleTimeoutSec)
	}
	if cfg.DefaultWorkspace != SandboxWorkspaceID {
		t.Fatalf("default workspace = %q, want %q", cfg.DefaultWorkspace, SandboxWorkspaceID)
	}

	wecomData, err := os.ReadFile(filepath.Join(home, ".lumi", "wecom", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wecom) error = %v", err)
	}
	if !strings.Contains(string(wecomData), `"workspaceId": "cli-sandbox"`) {
		t.Fatalf("wecom config missing sandbox workspace: %s", string(wecomData))
	}
}

func TestPrepareRunUsesSandboxIdleTimeoutOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, _, err := PrepareRun(state, RunOptions{
		Workspace:      workspace,
		Kind:           "sandbox",
		AgentID:        "claude",
		BotID:          "bot-123",
		BotSecret:      "secret-456",
		IdleTimeoutSec: 7200,
	})
	if err != nil {
		t.Fatalf("PrepareRun(sandbox) error = %v", err)
	}
	ws := cfg.FindWorkspace(SandboxWorkspaceID)
	if ws == nil {
		t.Fatal("workspace cli-sandbox not found")
	}
	if ws.IdleTimeoutSec != 7200 {
		t.Fatalf("sandbox idle timeout = %d, want 7200", ws.IdleTimeoutSec)
	}
}

func TestPrepareRunIgnoresIdleTimeoutForLocalWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, _, err := PrepareRun(state, RunOptions{
		Workspace:      workspace,
		Kind:           "local",
		AgentID:        "claude",
		BotID:          "bot-123",
		BotSecret:      "secret-456",
		IdleTimeoutSec: 7200,
	})
	if err != nil {
		t.Fatalf("PrepareRun(local) error = %v", err)
	}
	ws := cfg.FindWorkspace(WorkspaceID)
	if ws == nil {
		t.Fatal("workspace cli-local not found")
	}
	if ws.IdleTimeoutSec != 0 {
		t.Fatalf("local idle timeout = %d, want 0", ws.IdleTimeoutSec)
	}
}

func TestPrepareWeChatRunSavesConfigAndWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, resolved, err := PrepareWeChatRun(state, WeChatRunOptions{
		Workspace: workspace,
		Kind:      "sandbox",
		AgentID:   "claude",
		AccountID: "wx-bot",
		BotToken:  "bot-token",
		BaseURL:   "https://wechat.test/",
		Port:      "4455",
	})
	if err != nil {
		t.Fatalf("PrepareWeChatRun() error = %v", err)
	}
	if resolved != workspace {
		t.Fatalf("resolved workspace = %q, want %q", resolved, workspace)
	}
	if cfg.DefaultWorkspace != SandboxWorkspaceID {
		t.Fatalf("default workspace = %q, want %q", cfg.DefaultWorkspace, SandboxWorkspaceID)
	}
	ws := cfg.FindWorkspace(SandboxWorkspaceID)
	if ws == nil || ws.Kind != "sandbox" || ws.Agents[0] != "claude" {
		t.Fatalf("sandbox workspace = %+v, want claude sandbox", ws)
	}
	if cfg.PublicServerURL != "http://127.0.0.1:4455" {
		t.Fatalf("public server URL = %q, want http://127.0.0.1:4455", cfg.PublicServerURL)
	}

	data, err := os.ReadFile(filepath.Join(home, ".lumi", "wechat", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wechat) error = %v", err)
	}
	var saved wechat.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal(wechat) error = %v", err)
	}
	if !saved.Enabled || saved.LoginMode != "qr" || saved.AccountID != "wx-bot" || saved.BotToken != "bot-token" ||
		saved.BaseURL != "https://wechat.test" || saved.WorkspaceID != SandboxWorkspaceID || saved.AgentID != "claude" {
		t.Fatalf("wechat config = %+v, want saved QR credentials", saved)
	}
}

func TestPrepareRunFailsWhenAgentMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	_, _, err = PrepareRun(state, RunOptions{
		Workspace: workspace,
		AgentID:   "missing",
		BotID:     "bot-123",
		BotSecret: "secret-456",
	})
	if err == nil || !strings.Contains(err.Error(), "agent not found") {
		t.Fatalf("PrepareRun() error = %v, want agent not found", err)
	}
}

func TestPrepareRunFailsWithoutAgents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}

	_, _, err = PrepareRun(state, RunOptions{
		Workspace: t.TempDir(),
		AgentID:   "claude",
		BotID:     "bot-123",
		BotSecret: "secret-456",
	})
	if err == nil || !strings.Contains(err.Error(), "no agents configured") {
		t.Fatalf("PrepareRun() error = %v, want no agents configured", err)
	}
}

func TestPruneSandboxesUsesEmptyConfigWhenConfigMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	original := newSandboxPruner
	defer func() { newSandboxPruner = original }()

	fake := &fakeSandboxPruner{}
	newSandboxPruner = func(cfg *config.Config) (sandboxPruner, error) {
		if cfg == nil {
			t.Fatal("cfg = nil, want empty config")
		}
		if len(cfg.Workspaces) != 0 {
			t.Fatalf("cfg.Workspaces = %d, want 0", len(cfg.Workspaces))
		}
		return fake, nil
	}

	result, err := PruneSandboxes(context.Background(), "")
	if err != nil {
		t.Fatalf("PruneSandboxes() error = %v", err)
	}
	if len(result.Containers) != 1 || result.Containers[0].WorkspaceID != "cli-sandbox" {
		t.Fatalf("PruneSandboxes() result = %+v, want cli-sandbox", result)
	}
	if !fake.pruned {
		t.Fatal("PruneAll was not called")
	}
	if !fake.shutdown {
		t.Fatal("ShutdownPreserveContainers was not called")
	}
}

type fakeSandboxPruner struct {
	pruned   bool
	shutdown bool
}

func (p *fakeSandboxPruner) PruneAll(context.Context) ([]sandbox.RuntimeRecord, error) {
	p.pruned = true
	return []sandbox.RuntimeRecord{{
		WorkspaceID:   "cli-sandbox",
		ContainerName: "lumi-sandbox-cli",
		Status:        sandbox.StatusRunning,
	}}, nil
}

func (p *fakeSandboxPruner) ShutdownPreserveContainers() error {
	p.shutdown = true
	return nil
}
