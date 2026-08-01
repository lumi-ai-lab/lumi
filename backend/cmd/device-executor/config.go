package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/config"
)

type ExecutorConfig struct {
	DeviceID     string               `json:"deviceId"`
	Name         string               `json:"name"`
	Workspace    string               `json:"workspace"`
	WorkspaceID  string               `json:"workspaceId,omitempty"`
	Hidden       bool                 `json:"hidden,omitempty"`
	Agents       []config.AgentConfig `json:"agents"`
	DefaultAgent string               `json:"defaultAgent"`
}

func LoadOrCreateConfig(path string) (*ExecutorConfig, error) {
	configPath, err := resolveConfigPath(path)
	if err != nil {
		return nil, err
	}

	cfg := &ExecutorConfig{}
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config %s: %w", configPath, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to read config %s: %w", configPath, readErr)
	}

	changed, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if changed || !fileExists(configPath) {
		if err := writeConfig(configPath, cfg); err != nil {
			return nil, err
		}
	}

	if len(cfg.Agents) == 0 {
		return nil, fmt.Errorf("config %s is missing agents; add at least one agent entry", configPath)
	}

	model := &config.Config{
		Agents:       cfg.Agents,
		DefaultAgent: cfg.DefaultAgent,
	}
	if err := model.Validate(); err != nil {
		return nil, fmt.Errorf("invalid executor config %s: %w", configPath, err)
	}

	return cfg, nil
}

func resolveConfigPath(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve home directory: %w", err)
		}
		path = filepath.Join(home, ".device-executor", "config.json")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to resolve home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve config path: %w", err)
	}
	return absPath, nil
}

func normalizeConfig(cfg *ExecutorConfig) (bool, error) {
	changed := false

	if cfg.Agents == nil {
		cfg.Agents = []config.AgentConfig{}
		changed = true
	}

	if cfg.DeviceID == "" {
		cfg.DeviceID = generateDeviceID()
		changed = true
	}
	if cfg.Name == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return false, fmt.Errorf("failed to resolve hostname: %w", err)
		}
		cfg.Name = hostname
		changed = true
	}
	if cfg.Workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return false, fmt.Errorf("failed to resolve current directory: %w", err)
		}
		cfg.Workspace = cwd
		changed = true
	}
	if cfg.DefaultAgent == "" && len(cfg.Agents) > 0 {
		cfg.DefaultAgent = cfg.Agents[0].ID
		changed = true
	}

	for i := range cfg.Agents {
		if config.MigrateLegacyBuiltInPiAgent(&cfg.Agents[i]) {
			changed = true
		}
		if cfg.Agents[i].Name == "" {
			cfg.Agents[i].Name = cfg.Agents[i].ID
			changed = true
		}
	}

	return changed, nil
}

func writeConfig(path string, cfg *ExecutorConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config %s: %w", path, err)
	}
	return nil
}

func generateDeviceID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return "dev_" + hex.EncodeToString(buf)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
