package config

import (
	"os"
	"path/filepath"
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

func TestLoadAddsBuiltInQwenDefaultsToExistingConfig(t *testing.T) {
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
	if !cfg.BuiltInDefaultsChanged() {
		t.Fatal("BuiltInDefaultsChanged() = false, want true")
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
		`"@qwen": "qwen"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %s:\n%s", want, text)
		}
	}
}
