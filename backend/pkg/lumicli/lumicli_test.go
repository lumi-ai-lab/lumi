package lumicli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
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
	if !strings.Contains(text, `"id": "claude"`) || !strings.Contains(text, `"id": "codex"`) {
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
    {"id": "codex", "name": "Codex CLI", "command": "npx"}
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
	if got != "claude,codex" {
		t.Fatalf("AgentIDs() = %q, want %q", got, "claude,codex")
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

	wecomData, err := os.ReadFile(filepath.Join(home, ".lumi", "wecom", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wecom) error = %v", err)
	}
	text := string(wecomData)
	if !strings.Contains(text, `"enabled": true`) || !strings.Contains(text, `"agentId": "claude"`) {
		t.Fatalf("wecom config missing expected fields: %s", text)
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
