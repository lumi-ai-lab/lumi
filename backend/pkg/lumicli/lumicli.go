package lumicli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/api"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/setupcheck"
	"github.com/pengmide/lumi/internal/wecom"
)

const WorkspaceID = "cli-local"

type RunOptions struct {
	ConfigPath string
	Workspace  string
	AgentID    string
	BotID      string
	BotSecret  string
	Port       string
}

type ConfigState struct {
	Config    *config.Config
	Path      string
	Exists    bool
	HasAgents bool
}

type ServerRuntime struct {
	server *api.Server
	port   string
}

type SetupDependencyItem = setupcheck.DependencyItem
type SetupStatus = setupcheck.SetupStatus
type SetupInstallEvent = setupcheck.InstallEvent
type SetupInstallResult = setupcheck.InstallResult

func ResolveConfigState(configPath string) (*ConfigState, error) {
	targetPath, exists, err := resolveConfigPath(configPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &ConfigState{
			Config:    &config.Config{},
			Path:      targetPath,
			Exists:    false,
			HasAgents: false,
		}, nil
	}

	cfg, err := config.Load(targetPath)
	if err != nil {
		return nil, err
	}
	return &ConfigState{
		Config:    cfg,
		Path:      targetPath,
		Exists:    true,
		HasAgents: len(cfg.Agents) > 0,
	}, nil
}

func EnsureConfigFile(state *ConfigState) error {
	if state == nil {
		return errors.New("config state is required")
	}
	if state.Exists {
		return nil
	}

	if err := config.EnsureConfigExists(); err != nil {
		return err
	}

	reloaded, err := ResolveConfigState(state.Path)
	if err != nil {
		return err
	}
	state.Config = reloaded.Config
	state.Path = reloaded.Path
	state.Exists = reloaded.Exists
	state.HasAgents = reloaded.HasAgents
	return nil
}

func AgentIDs(state *ConfigState) []string {
	if state == nil || state.Config == nil {
		return nil
	}
	ids := make([]string, 0, len(state.Config.Agents))
	for _, agent := range state.Config.Agents {
		if strings.TrimSpace(agent.ID) != "" {
			ids = append(ids, agent.ID)
		}
	}
	return ids
}

func HasAgent(state *ConfigState, agentID string) bool {
	if state == nil || state.Config == nil {
		return false
	}
	return state.Config.FindAgent(strings.TrimSpace(agentID)) != nil
}

func CheckSetup(state *ConfigState) SetupStatus {
	if state == nil || state.Config == nil {
		return setupcheck.Check(nil)
	}
	return setupcheck.Check(state.Config.Agents)
}

func InstallSetup(status SetupStatus, progress func(SetupInstallEvent), logFn func(string)) SetupInstallResult {
	return setupcheck.InstallMissing(status, progress, logFn)
}

func PrepareRun(state *ConfigState, opts RunOptions) (*config.Config, string, error) {
	if state == nil || state.Config == nil {
		return nil, "", errors.New("config state is required")
	}
	cfg := state.Config
	if len(cfg.Agents) == 0 {
		return nil, "", errors.New("no agents configured; run `lumi-cli setup` first and prepare agents in lumi.config.json")
	}

	workspacePath, err := filepath.Abs(strings.TrimSpace(opts.Workspace))
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return nil, "", fmt.Errorf("workspace not found: %w", err)
	}
	if !info.IsDir() {
		return nil, "", errors.New("workspace must be a directory")
	}

	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		return nil, "", errors.New("agent is required")
	}
	if cfg.FindAgent(agentID) == nil {
		return nil, "", fmt.Errorf("agent not found: %s; run `lumi-cli setup` first and configure it in lumi.config.json", agentID)
	}

	workspaceName := filepath.Base(workspacePath)
	if workspaceName == "." || workspaceName == string(filepath.Separator) || workspaceName == "" {
		workspaceName = "CLI Local Workspace"
	}
	upsertWorkspace(cfg, config.WorkspaceConfig{
		ID:     WorkspaceID,
		Name:   workspaceName,
		Path:   workspacePath,
		Kind:   "local",
		Agents: []string{agentID},
	})
	cfg.DefaultWorkspace = WorkspaceID

	if err := cfg.Validate(); err != nil {
		return nil, "", err
	}
	if err := saveConfig(cfg, state.Path); err != nil {
		return nil, "", err
	}

	wecomCfg := wecom.Config{
		Enabled:             true,
		Mode:                "websocket",
		BotID:               strings.TrimSpace(opts.BotID),
		BotSecret:           strings.TrimSpace(opts.BotSecret),
		WorkspaceID:         WorkspaceID,
		AgentID:             agentID,
		ConnectTimeoutMs:    15000,
		HeartbeatIntervalMs: 30000,
		MessageAckTimeoutMs: 5000,
	}
	if strings.TrimSpace(wecomCfg.BotID) == "" {
		return nil, "", errors.New("bot id is required")
	}
	if strings.TrimSpace(wecomCfg.BotSecret) == "" {
		return nil, "", errors.New("bot secret is required")
	}
	if err := wecom.NewConfigStore().Save(wecomCfg); err != nil {
		return nil, "", err
	}

	return cfg, workspacePath, nil
}

func StartServer(cfg *config.Config, staticFS fs.FS, port string) (*ServerRuntime, error) {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "3000"
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &ServerRuntime{
		server: api.NewServer(cfg, staticFS),
		port:   port,
	}, nil
}

func (r *ServerRuntime) ListenAndServe() error {
	return r.server.ListenAndServe(":" + r.port)
}

func (r *ServerRuntime) Shutdown(_ context.Context) error {
	return r.ShutdownWithContext(context.Background())
}

func (r *ServerRuntime) ShutdownWithContext(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		done <- r.server.Shutdown()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *ServerRuntime) Port() string {
	return r.port
}

func DefaultConfigPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return filepath.Join(home, ".lumi", "lumi.config.json")
}

func upsertWorkspace(cfg *config.Config, ws config.WorkspaceConfig) {
	for i := range cfg.Workspaces {
		if cfg.Workspaces[i].ID == ws.ID {
			cfg.Workspaces[i] = ws
			return
		}
	}
	cfg.Workspaces = append(cfg.Workspaces, ws)
}

func resolveConfigPath(configPath string) (string, bool, error) {
	if strings.TrimSpace(configPath) != "" {
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return "", false, err
		}
		_, err = os.Stat(absPath)
		if err == nil {
			return absPath, true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return absPath, false, nil
		}
		return "", false, err
	}

	found := config.FindConfigPath()
	if found != "" {
		return found, true, nil
	}
	return DefaultConfigPath(), false, nil
}

func saveConfig(cfg *config.Config, targetPath string) error {
	if targetPath == "" {
		return errors.New("config path is required")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(targetPath, []byte("{\n}\n"), 0o644); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return cfg.Save(targetPath)
}
