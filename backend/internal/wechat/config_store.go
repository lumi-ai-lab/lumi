package wechat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/pengmide/lumi/internal/lumipaths"
)

const defaultBaseURL = "https://ilinkai.weixin.qq.com"

type Config struct {
	Enabled     bool   `json:"enabled"`
	LoginMode   string `json:"loginMode"`
	AccountID   string `json:"accountId"`
	BotToken    string `json:"botToken"`
	BaseURL     string `json:"baseUrl"`
	WorkspaceID string `json:"workspaceId"`
	AgentID     string `json:"agentId"`
}

type SanitizedConfig struct {
	Enabled     bool   `json:"enabled"`
	LoginMode   string `json:"loginMode"`
	AccountID   string `json:"accountId"`
	BaseURL     string `json:"baseUrl"`
	WorkspaceID string `json:"workspaceId"`
	AgentID     string `json:"agentId"`
	HasToken    bool   `json:"hasToken"`
	MaskedToken string `json:"maskedToken,omitempty"`
}

type ConfigStore struct {
	path string
	mu   sync.Mutex
}

func NewConfigStore() *ConfigStore {
	return &ConfigStore{
		path: filepath.Join(wechatActiveRootDir(), "config.json"),
	}
}

func NewConfigStoreForInstance(instanceID string) *ConfigStore {
	return &ConfigStore{
		path: filepath.Join(wechatInstanceRootDir(instanceID), "config.json"),
	}
}

func DefaultConfig() Config {
	return Config{
		Enabled:   false,
		LoginMode: "qr",
		BaseURL:   defaultBaseURL,
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
	if cfg.LoginMode == "" {
		cfg.LoginMode = "qr"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg
}

func SanitizeConfig(cfg Config) SanitizedConfig {
	cfg = normalizeConfig(cfg)
	sanitized := SanitizedConfig{
		Enabled:     cfg.Enabled,
		LoginMode:   cfg.LoginMode,
		AccountID:   cfg.AccountID,
		BaseURL:     cfg.BaseURL,
		WorkspaceID: cfg.WorkspaceID,
		AgentID:     cfg.AgentID,
		HasToken:    strings.TrimSpace(cfg.BotToken) != "",
	}
	if sanitized.HasToken {
		sanitized.MaskedToken = MaskToken(cfg.BotToken)
	}
	return sanitized
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) < 8 {
		return "********"
	}
	return token[:4] + "********" + token[len(token)-4:]
}

func wechatRootDir() string {
	return lumipaths.Path("wechat")
}

func wechatActiveRootDir() string {
	if instanceID := strings.TrimSpace(os.Getenv("LUMI_WECHAT_INSTANCE_ID")); instanceID != "" {
		return wechatInstanceRootDir(instanceID)
	}
	return wechatRootDir()
}

func wechatInstanceRootDir(instanceID string) string {
	return filepath.Join(wechatRootDir(), "instances", strings.TrimSpace(instanceID))
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
