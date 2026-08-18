package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExampleConfigUsesManagedPiACPVersion(t *testing.T) {
	var raw rawConfig
	if err := json.Unmarshal(exampleConfigData, &raw); err != nil {
		t.Fatalf("Unmarshal(exampleConfigData) error = %v", err)
	}
	cfg := raw.normalize()
	pi := cfg.FindAgent("pi")
	if pi == nil || pi.Command != "npx" || strings.Join(pi.Args, " ") != "-y pi-acp@0.0.33" {
		t.Fatalf("example PI config = %+v, want npx -y pi-acp@0.0.33", pi)
	}
}

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
	if pi.Command != "npx" || strings.Join(pi.Args, " ") != "-y pi-acp@0.0.33" {
		t.Fatalf("pi config = %+v, want npx pi-acp@0.0.33", pi)
	}
	if cfg.Routing == nil || cfg.Routing.Keywords["@pi"] != "pi" {
		t.Fatalf("routing keywords = %+v, want @pi route", cfg.Routing)
	}
	if !cfg.BuiltInDefaultsChanged() {
		t.Fatal("BuiltInDefaultsChanged() = false, want true")
	}
}

func TestLoadUpgradesOnlyLegacyBuiltInPiAgent(t *testing.T) {
	tests := []struct {
		name        string
		pi          string
		wantCommand string
		wantArgs    string
	}{
		{
			name:        "legacy built-in",
			pi:          `{"id":"pi","name":"PI","command":"npx","args":["-y","pi-acp@0.0.27"],"sessionMode":"default"}`,
			wantCommand: "npx",
			wantArgs:    "-y pi-acp@0.0.33",
		},
		{
			name:        "custom PI",
			pi:          `{"id":"pi","name":"Custom PI","command":"pi-wrapper","args":["--acp"],"env":{"KEEP":"1"}}`,
			wantCommand: "pi-wrapper",
			wantArgs:    "--acp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "lumi.config.json")
			data := `{"agents":[{"id":"claude","name":"Claude","command":"npx"},` + tt.pi + `],"defaultAgent":"claude"}`
			if err := os.WriteFile(configPath, []byte(data), 0644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			pi := cfg.FindAgent("pi")
			if pi == nil || pi.Command != tt.wantCommand || strings.Join(pi.Args, " ") != tt.wantArgs {
				t.Fatalf("PI config = %+v, want command=%q args=%q", pi, tt.wantCommand, tt.wantArgs)
			}
		})
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
}

func TestSavePersistsBuiltInQwenDefaultsAndPreservesExistingFields(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
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
