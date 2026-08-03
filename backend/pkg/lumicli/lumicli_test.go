package lumicli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/wechat"
	"github.com/pengmide/lumi/internal/wecom"
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
	if !strings.Contains(text, `"id": "claude"`) || !strings.Contains(text, `"id": "codex"`) || !strings.Contains(text, `"id": "qwen"`) || !strings.Contains(text, `"id": "pi"`) {
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

func TestResolveConfigStatePersistsExactLegacyPiACPMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".lumi", "lumi.config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	original := `{
  "customTopLevel": {"keep": true},
  "agents": [
    {"id":"claude","name":"Claude Code","command":"npx"},
    {"id":"pi","name":"Custom PI Name","command":"npx","args":["-y","` + config.LegacyPiACPPackageSpec + `"],"env":{"KEEP":"1"},"sessionMode":"bypass","customAgentField":"keep"}
  ],
  "defaultAgent":"claude",
  "routing":{"keywords":{"@qwen":"qwen","@pi":"pi"}}
}
`
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	state, err := ResolveConfigState(configPath)
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	pi := state.Config.FindAgent("pi")
	if pi == nil || strings.Join(pi.Args, " ") != "-y "+config.PiACPPackageSpec {
		t.Fatalf("PI config = %+v, want migrated %s", pi, config.PiACPPackageSpec)
	}
	if pi.Name != "Custom PI Name" || pi.Env["KEEP"] != "1" || pi.SessionMode != "bypass" {
		t.Fatalf("migration overwrote supported custom fields: %+v", pi)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	if strings.Contains(text, config.LegacyPiACPPackageSpec) || !strings.Contains(text, config.PiACPPackageSpec) {
		t.Fatalf("PI ACP migration was not persisted:\n%s", data)
	}
	for _, want := range []string{`"customTopLevel": {`, `"keep": true`, `"customAgentField": "keep"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("migration dropped custom field %s:\n%s", want, data)
		}
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
    {"id": "qwen", "name": "Qwen Code", "command": "npx"},
    {"id": "pi", "name": "PI", "command": "npx"}
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
	if got != "claude,codex,qwen,pi" {
		t.Fatalf("AgentIDs() = %q, want %q", got, "claude,codex,qwen,pi")
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
		{ID: "pi", Name: "PI", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, resolved, err := PrepareRun(state, RunOptions{
		Workspace:   workspace,
		AgentID:     "claude",
		BotID:       "bot-123",
		BotSecret:   "secret-456",
		WeComStream: true,
		Port:        "3344",
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
	if got := strings.Join(ws.Agents, ","); got != "claude,codex,qwen,pi" {
		t.Fatalf("workspace agents = %q, want claude,codex,qwen,pi", got)
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
	if !strings.Contains(text, `"stream": true`) {
		t.Fatalf("wecom config missing stream=true: %s", text)
	}
}

func TestPrepareRunValidatesAndSavesAbsoluteRequesterConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{{ID: "claude", Name: "Claude", Command: "npx"}}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	policyPath := filepath.Join(t.TempDir(), "requesters.json")
	policy := `{"version":2,"users":[{"userId":"u1","displayName":"U1","enabled":true,"authorization":{"capabilities":["com.example.reports.read"],"claims":{"com.example.reports":{"tenantIds":["tenant-a"]}}}}]}`
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatalf("WriteFile(policy) error = %v", err)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	relativePolicyPath, err := filepath.Rel(workingDir, policyPath)
	if err != nil {
		t.Fatalf("Rel(policy) error = %v", err)
	}

	if _, _, err := PrepareRun(state, RunOptions{
		Workspace:           workspace,
		AgentID:             "claude",
		BotID:               "bot-123",
		BotSecret:           "secret-456",
		RequesterConfigPath: relativePolicyPath,
	}); err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	saved, err := wecom.NewConfigStore().Load()
	if err != nil {
		t.Fatalf("Load(wecom config) error = %v", err)
	}
	wantPath, err := filepath.Abs(relativePolicyPath)
	if err != nil {
		t.Fatalf("Abs(policy) error = %v", err)
	}
	if saved.RequesterConfigPath != filepath.Clean(wantPath) || !filepath.IsAbs(saved.RequesterConfigPath) {
		t.Fatalf("RequesterConfigPath = %q, want absolute %q", saved.RequesterConfigPath, filepath.Clean(wantPath))
	}
}

func TestPrepareRunRejectsRequesterConfigBeforeSavingWeComConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{{ID: "claude", Name: "Claude", Command: "npx"}}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	policyPath := filepath.Join(t.TempDir(), "requesters.json")
	if err := os.WriteFile(policyPath, []byte(`{"version":2,"botId":"different-bot","users":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile(policy) error = %v", err)
	}
	_, _, err = PrepareRun(state, RunOptions{
		Workspace:           workspace,
		AgentID:             "claude",
		BotID:               "bot-123",
		BotSecret:           "secret-456",
		RequesterConfigPath: policyPath,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("PrepareRun() error = %v, want bot mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".lumi", "wecom", "config.json")); !os.IsNotExist(statErr) {
		t.Fatalf("wecom config stat error = %v, want no saved config", statErr)
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
		Workspace:   workspace,
		Kind:        "sandbox",
		AgentID:     "claude",
		BotID:       "bot-123",
		BotSecret:   "secret-456",
		WeComStream: true,
	})
	if err != nil {
		t.Fatalf("PrepareRun(sandbox) error = %v", err)
	}
	wantWorkspaceID, err := resolveSandboxWorkspaceID("wecom", "bot-123", workspace, "")
	if err != nil {
		t.Fatalf("resolveSandboxWorkspaceID() error = %v", err)
	}
	ws := cfg.FindWorkspace(wantWorkspaceID)
	if ws == nil {
		t.Fatalf("workspace %s not found", wantWorkspaceID)
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
	if cfg.DefaultWorkspace != wantWorkspaceID {
		t.Fatalf("default workspace = %q, want %q", cfg.DefaultWorkspace, wantWorkspaceID)
	}

	wecomData, err := os.ReadFile(filepath.Join(home, ".lumi", "wecom", "instances", wantWorkspaceID, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wecom) error = %v", err)
	}
	if !strings.Contains(string(wecomData), fmt.Sprintf(`"workspaceId": "%s"`, wantWorkspaceID)) {
		t.Fatalf("wecom config missing sandbox workspace: %s", string(wecomData))
	}
	if !strings.Contains(string(wecomData), `"stream": true`) {
		t.Fatalf("wecom config missing stream=true: %s", string(wecomData))
	}
	if _, err := os.Stat(filepath.Join(home, ".lumi", "wecom", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("global wecom config exists for sandbox run, err=%v", err)
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
	wantWorkspaceID, err := resolveSandboxWorkspaceID("wecom", "bot-123", workspace, "")
	if err != nil {
		t.Fatalf("resolveSandboxWorkspaceID() error = %v", err)
	}
	ws := cfg.FindWorkspace(wantWorkspaceID)
	if ws == nil {
		t.Fatalf("workspace %s not found", wantWorkspaceID)
	}
	if ws.IdleTimeoutSec != 7200 {
		t.Fatalf("sandbox idle timeout = %d, want 7200", ws.IdleTimeoutSec)
	}
}

func TestPrepareRunSandboxIDDerivationAndOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspaceA := filepath.Join(home, "workspace-a")
	workspaceB := filepath.Join(home, "workspace-b")
	if err := os.MkdirAll(workspaceA, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceA) error = %v", err)
	}
	if err := os.MkdirAll(workspaceB, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceB) error = %v", err)
	}

	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{{ID: "claude", Name: "Claude Code", Command: "npx"}}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true

	cfg, _, err := PrepareRun(state, RunOptions{Workspace: workspaceA, Kind: "sandbox", AgentID: "claude", BotID: "bot-a", BotSecret: "secret"})
	if err != nil {
		t.Fatalf("PrepareRun(bot-a/workspace-a) error = %v", err)
	}
	firstID := cfg.DefaultWorkspace
	cfg, _, err = PrepareRun(state, RunOptions{Workspace: workspaceA, Kind: "sandbox", AgentID: "claude", BotID: "bot-a", BotSecret: "secret"})
	if err != nil {
		t.Fatalf("PrepareRun(repeat) error = %v", err)
	}
	if cfg.DefaultWorkspace != firstID {
		t.Fatalf("repeat workspace ID = %q, want %q", cfg.DefaultWorkspace, firstID)
	}
	cfg, _, err = PrepareRun(state, RunOptions{Workspace: workspaceA, Kind: "sandbox", AgentID: "claude", BotID: "bot-b", BotSecret: "secret"})
	if err != nil {
		t.Fatalf("PrepareRun(bot-b) error = %v", err)
	}
	if cfg.DefaultWorkspace == firstID {
		t.Fatalf("different bot ID reused workspace ID %q", firstID)
	}
	cfg, _, err = PrepareRun(state, RunOptions{Workspace: workspaceB, Kind: "sandbox", AgentID: "claude", BotID: "bot-a", BotSecret: "secret"})
	if err != nil {
		t.Fatalf("PrepareRun(workspace-b) error = %v", err)
	}
	if cfg.DefaultWorkspace == firstID {
		t.Fatalf("different workspace path reused workspace ID %q", firstID)
	}
	cfg, _, err = PrepareRun(state, RunOptions{Workspace: workspaceB, Kind: "sandbox", AgentID: "claude", BotID: "bot-b", BotSecret: "secret", SandboxID: "manual-a"})
	if err != nil {
		t.Fatalf("PrepareRun(manual) error = %v", err)
	}
	if cfg.DefaultWorkspace != "cli-sandbox-manual-a" {
		t.Fatalf("manual workspace ID = %q, want cli-sandbox-manual-a", cfg.DefaultWorkspace)
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
	wantWorkspaceID, err := resolveSandboxWorkspaceID("wechat", "wx-bot", workspace, "")
	if err != nil {
		t.Fatalf("resolveSandboxWorkspaceID() error = %v", err)
	}
	if cfg.DefaultWorkspace != wantWorkspaceID {
		t.Fatalf("default workspace = %q, want %q", cfg.DefaultWorkspace, wantWorkspaceID)
	}
	ws := cfg.FindWorkspace(wantWorkspaceID)
	if ws == nil || ws.Kind != "sandbox" || ws.Agents[0] != "claude" {
		t.Fatalf("sandbox workspace = %+v, want claude sandbox", ws)
	}
	if cfg.PublicServerURL != "http://127.0.0.1:4455" {
		t.Fatalf("public server URL = %q, want http://127.0.0.1:4455", cfg.PublicServerURL)
	}

	data, err := os.ReadFile(filepath.Join(home, ".lumi", "wechat", "instances", wantWorkspaceID, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wechat) error = %v", err)
	}
	var saved wechat.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Unmarshal(wechat) error = %v", err)
	}
	if !saved.Enabled || saved.LoginMode != "qr" || saved.AccountID != "wx-bot" || saved.BotToken != "bot-token" ||
		saved.BaseURL != "https://wechat.test" || saved.WorkspaceID != wantWorkspaceID || saved.AgentID != "claude" {
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
