package wecom

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	defaultMode                = "websocket"
	defaultConnectTimeoutMs    = 15000
	defaultHeartbeatMs         = 30000
	defaultMessageAckTimeoutMs = 5000
)

type Config struct {
	Enabled             bool   `json:"enabled"`
	Mode                string `json:"mode"`
	BotID               string `json:"botId"`
	BotSecret           string `json:"botSecret"`
	WorkspaceID         string `json:"workspaceId"`
	AgentID             string `json:"agentId"`
	AllowFrom           string `json:"allowFrom"`
	Stream              bool   `json:"stream"`
	ConnectTimeoutMs    int    `json:"connectTimeoutMs"`
	HeartbeatIntervalMs int    `json:"heartbeatIntervalMs"`
	MessageAckTimeoutMs int    `json:"messageAckTimeoutMs"`
}

type SanitizedConfig struct {
	Enabled             bool   `json:"enabled"`
	Mode                string `json:"mode"`
	BotID               string `json:"botId"`
	WorkspaceID         string `json:"workspaceId"`
	AgentID             string `json:"agentId"`
	AllowFrom           string `json:"allowFrom"`
	Stream              bool   `json:"stream"`
	ConnectTimeoutMs    int    `json:"connectTimeoutMs"`
	HeartbeatIntervalMs int    `json:"heartbeatIntervalMs"`
	MessageAckTimeoutMs int    `json:"messageAckTimeoutMs"`
	HasSecret           bool   `json:"hasSecret"`
	MaskedSecret        string `json:"maskedSecret,omitempty"`
}

type ConfigStore struct {
	path string
	mu   sync.Mutex
}

func NewConfigStore() *ConfigStore {
	return &ConfigStore{
		path: filepath.Join(wecomActiveRootDir(), "config.json"),
	}
}

func NewConfigStoreForInstance(instanceID string) *ConfigStore {
	return &ConfigStore{
		path: filepath.Join(wecomInstanceRootDir(instanceID), "config.json"),
	}
}

func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		Mode:                defaultMode,
		AllowFrom:           "",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}
}

func (s *ConfigStore) Load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := DefaultConfig()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	return normalizeConfig(cfg), nil
}

func (s *ConfigStore) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return writePrivateJSON(s.path, normalizeConfig(cfg))
}

func normalizeConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = defaultMode
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.ConnectTimeoutMs <= 0 {
		cfg.ConnectTimeoutMs = defaultConnectTimeoutMs
	}
	if cfg.HeartbeatIntervalMs <= 0 {
		cfg.HeartbeatIntervalMs = defaultHeartbeatMs
	}
	if cfg.MessageAckTimeoutMs <= 0 {
		cfg.MessageAckTimeoutMs = defaultMessageAckTimeoutMs
	}
	cfg.AllowFrom = strings.TrimSpace(cfg.AllowFrom)
	return cfg
}

func SanitizeConfig(cfg Config) SanitizedConfig {
	cfg = normalizeConfig(cfg)
	sanitized := SanitizedConfig{
		Enabled:             cfg.Enabled,
		Mode:                cfg.Mode,
		BotID:               cfg.BotID,
		WorkspaceID:         cfg.WorkspaceID,
		AgentID:             cfg.AgentID,
		AllowFrom:           cfg.AllowFrom,
		Stream:              cfg.Stream,
		ConnectTimeoutMs:    cfg.ConnectTimeoutMs,
		HeartbeatIntervalMs: cfg.HeartbeatIntervalMs,
		MessageAckTimeoutMs: cfg.MessageAckTimeoutMs,
		HasSecret:           strings.TrimSpace(cfg.BotSecret) != "",
	}
	if sanitized.HasSecret {
		sanitized.MaskedSecret = maskSecret(cfg.BotSecret)
	}
	return sanitized
}

func maskSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) < 8 {
		return "********"
	}
	return secret[:4] + "********" + secret[len(secret)-4:]
}

func wecomRootDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".lumi", "wecom")
}

func wecomActiveRootDir() string {
	if instanceID := strings.TrimSpace(os.Getenv("LUMI_WECOM_INSTANCE_ID")); instanceID != "" {
		return wecomInstanceRootDir(instanceID)
	}
	return wecomRootDir()
}

func wecomInstanceRootDir(instanceID string) string {
	return filepath.Join(wecomRootDir(), "instances", strings.TrimSpace(instanceID))
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, 0o700)
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, 0o600)
	}
	return nil
}
