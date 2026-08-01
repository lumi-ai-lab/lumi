package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadAndSavePublicServerURL(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
  "publicServerURL": "https://chat.example.com/lumi",
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
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PublicServerURL != "https://chat.example.com/lumi" {
		t.Fatalf("cfg.PublicServerURL = %q, want saved value", cfg.PublicServerURL)
	}

	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `"publicServerURL": "https://chat.example.com/lumi"`) {
		t.Fatalf("saved config missing publicServerURL: %s", data)
	}
}

func TestLoadAddsBuiltInAgentDefaultsToExistingConfig(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
  "agents": [
    {"id": "claude", "name": "Claude Code", "command": "npx"},
    {"id": "codex", "name": "Codex CLI", "command": "npx"}
  ],
  "defaultAgent": "claude",
  "routing": {
    "keywords": {
      "@claude": "claude",
      "@codex": "codex"
    },
    "meta": true
  }
}
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	qwen := cfg.FindAgent("qwen")
	if qwen == nil {
		t.Fatal("FindAgent(qwen) = nil, want built-in Qwen")
	}
	if qwen.Command != "npx" || strings.Join(qwen.Args, " ") != "-y @qwen-code/qwen-code --acp" {
		t.Fatalf("qwen config = %+v, want npx @qwen-code/qwen-code --acp", qwen)
	}
	if cfg.DefaultAgent != "claude" {
		t.Fatalf("DefaultAgent = %q, want claude", cfg.DefaultAgent)
	}
	if cfg.Routing == nil || cfg.Routing.Keywords["@qwen"] != "qwen" {
		t.Fatalf("routing keywords = %+v, want @qwen route", cfg.Routing)
	}
	pi := cfg.FindAgent("pi")
	if pi == nil {
		t.Fatal("FindAgent(pi) = nil, want built-in PI")
	}
	if pi.Command != "npx" || strings.Join(pi.Args, " ") != "-y "+PiACPPackageSpec {
		t.Fatalf("pi config = %+v, want npx %s", pi, PiACPPackageSpec)
	}
	if cfg.Routing == nil || cfg.Routing.Keywords["@pi"] != "pi" {
		t.Fatalf("routing keywords = %+v, want @pi route", cfg.Routing)
	}
	if !cfg.BuiltInDefaultsChanged() {
		t.Fatal("BuiltInDefaultsChanged() = false, want true")
	}
	if cfg.LegacyPiACPConfigMigrated() {
		t.Fatal("LegacyPiACPConfigMigrated() = true, want false")
	}
}

func TestLoadMigratesOnlyLegacyBuiltInPiAgent(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
  "agents": [
    {"id":"claude","name":"Claude Code","command":"echo"},
    {"id":"pi","name":"PI","command":"npx","args":["-y","` + LegacyPiACPPackageSpec + `"],"env":{"KEEP":"1"},"sessionMode":"bypass"}
  ],
  "defaultAgent":"claude",
  "routing":{"keywords":{"@qwen":"qwen","@pi":"pi"},"meta":true}
}`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	pi := cfg.FindAgent("pi")
	if pi == nil || !slices.Equal(pi.Args, []string{"-y", PiACPPackageSpec}) {
		t.Fatalf("migrated PI config = %+v, want %s", pi, PiACPPackageSpec)
	}
	if pi.Env["KEEP"] != "1" || pi.SessionMode != "bypass" {
		t.Fatalf("migration overwrote custom PI fields: %+v", pi)
	}
	if !cfg.BuiltInDefaultsChanged() {
		t.Fatal("BuiltInDefaultsChanged() = false, want true")
	}
	if !cfg.LegacyPiACPConfigMigrated() {
		t.Fatal("LegacyPiACPConfigMigrated() = false, want true")
	}

	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), LegacyPiACPPackageSpec) || !strings.Contains(string(data), PiACPPackageSpec) {
		t.Fatalf("saved config did not persist migration:\n%s", data)
	}
}

func TestMigrateLegacyBuiltInPiAgentPreservesCustomCommandsAndArgs(t *testing.T) {
	t.Parallel()

	tests := []AgentConfig{
		{ID: "pi", Command: "pi-acp", Args: []string{"--trace"}},
		{ID: "pi", Command: "npx", Args: []string{"--registry=https://registry.example", "-y", LegacyPiACPPackageSpec}},
		{ID: "pi", Command: "npx", Args: []string{"-y", LegacyPiACPPackageSpec, "--trace"}},
		{ID: "pi", Command: "npx", Args: []string{"-y", "pi-acp@0.0.32"}},
		{ID: "custom-pi", Command: "npx", Args: []string{"-y", LegacyPiACPPackageSpec}},
	}
	for _, original := range tests {
		agent := original
		agent.Args = append([]string(nil), original.Args...)
		if MigrateLegacyBuiltInPiAgent(&agent) {
			t.Fatalf("custom config was migrated: before=%+v after=%+v", original, agent)
		}
		if !slices.Equal(agent.Args, original.Args) || agent.Command != original.Command {
			t.Fatalf("custom config changed: before=%+v after=%+v", original, agent)
		}
	}
}

func TestLoadPreservesCustomQwenConfig(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
  "agents": [
    {"id": "claude", "name": "Claude Code", "command": "npx"},
    {"id": "qwen", "name": "Custom Qwen", "command": "qwen", "args": ["--acp"], "env": {"QWEN_TOKEN": "test"}}
  ],
  "defaultAgent": "claude"
}
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	qwen := cfg.FindAgent("qwen")
	if qwen == nil {
		t.Fatal("FindAgent(qwen) = nil")
	}
	if qwen.Name != "Custom Qwen" || qwen.Command != "qwen" || strings.Join(qwen.Args, " ") != "--acp" {
		t.Fatalf("custom qwen was overwritten: %+v", qwen)
	}
	if qwen.Env["QWEN_TOKEN"] != "test" {
		t.Fatalf("custom qwen env = %+v, want QWEN_TOKEN", qwen.Env)
	}
	if !cfg.BuiltInDefaultsChanged() {
		t.Fatal("BuiltInDefaultsChanged() = false, want true because @qwen route was added")
	}
	if cfg.LegacyPiACPConfigMigrated() {
		t.Fatal("LegacyPiACPConfigMigrated() = true, want false")
	}
}

func TestSavePersistsBuiltInQwenDefaultsAndPreservesExistingFields(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
  "customTopLevel": {"keep": true},
  "publicServerURL": "https://chat.example.com/lumi",
  "agents": [
    {"id": "claude", "name": "Claude Code", "command": "npx", "custom": "keep"}
  ],
  "defaultAgent": "claude"
}
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"customTopLevel": {`,
		`"keep": true`,
		`"publicServerURL": "https://chat.example.com/lumi"`,
		`"custom": "keep"`,
		`"id": "qwen"`,
		`"id": "pi"`,
		`"@qwen": "qwen"`,
		`"@pi": "pi"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %s:\n%s", want, text)
		}
	}
}
