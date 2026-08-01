package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestLoadOrCreateConfigMigratesLegacyBuiltInPiAgent(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	original := ExecutorConfig{
		DeviceID:     "device-1",
		Name:         "Device",
		Workspace:    t.TempDir(),
		DefaultAgent: "pi",
		Agents: []config.AgentConfig{{
			ID:      "pi",
			Name:    "PI",
			Command: "npx",
			Args:    []string{"-y", config.LegacyPiACPPackageSpec},
			Env:     map[string]string{"KEEP": "1"},
		}},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	loaded, err := LoadOrCreateConfig(configPath)
	if err != nil {
		t.Fatalf("LoadOrCreateConfig() error = %v", err)
	}
	if !slices.Equal(loaded.Agents[0].Args, []string{"-y", config.PiACPPackageSpec}) {
		t.Fatalf("PI args = %#v, want %s", loaded.Agents[0].Args, config.PiACPPackageSpec)
	}
	if loaded.Agents[0].Env["KEEP"] != "1" {
		t.Fatalf("PI env was not preserved: %#v", loaded.Agents[0].Env)
	}

	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(saved) == string(data) {
		t.Fatal("migrated executor config was not persisted")
	}
}

func TestNormalizeConfigPreservesCustomPiAgent(t *testing.T) {
	cfg := &ExecutorConfig{
		DeviceID:     "device-1",
		Name:         "Device",
		Workspace:    t.TempDir(),
		DefaultAgent: "pi",
		Agents: []config.AgentConfig{{
			ID:      "pi",
			Name:    "Custom PI",
			Command: "npx",
			Args:    []string{"--registry=https://registry.example", "-y", config.LegacyPiACPPackageSpec},
		}},
	}

	changed, err := normalizeConfig(cfg)
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if changed {
		t.Fatal("normalizeConfig() changed a custom PI command")
	}
	want := []string{"--registry=https://registry.example", "-y", config.LegacyPiACPPackageSpec}
	if !slices.Equal(cfg.Agents[0].Args, want) {
		t.Fatalf("custom PI args = %#v, want %#v", cfg.Agents[0].Args, want)
	}
}
