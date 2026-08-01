package lumicli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/api"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/lumipaths"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/setupcheck"
	"github.com/pengmide/lumi/internal/wechat"
	"github.com/pengmide/lumi/internal/wecom"
)

const WorkspaceID = "cli-local"
const SandboxWorkspaceID = "cli-sandbox"
const IMSandboxIdleTimeoutSec = 10 * 365 * 24 * 60 * 60

type RunOptions struct {
	ConfigPath          string
	Workspace           string
	Kind                string
	AgentID             string
	AgentIDs            []string
	BotID               string
	BotSecret           string
	RequesterConfigPath string
	WeComStream         bool
	Port                string
	IdleTimeoutSec      int
	SandboxID           string
}

type WeChatRunOptions struct {
	ConfigPath     string
	Workspace      string
	Kind           string
	AgentID        string
	AgentIDs       []string
	AccountID      string
	BotToken       string
	BaseURL        string
	Port           string
	IdleTimeoutSec int
	SandboxID      string
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

type sandboxPruner interface {
	PruneAll(context.Context) ([]sandbox.RuntimeRecord, error)
	ShutdownPreserveContainers() error
}

type SandboxPruneResult struct {
	Containers []SandboxPrunedContainer
}

type SandboxPrunedContainer struct {
	WorkspaceID    string
	ContainerName  string
	Status         string
	CreatedAt      int64
	StartedAt      int64
	LastActivityAt int64
	ExpiresAt      int64
}

var newSandboxPruner = func(cfg *config.Config) (sandboxPruner, error) {
	deviceSecret, err := device.EnsureSecret("")
	if err != nil {
		return nil, err
	}
	devices, err := device.NewRegistry(device.NewStore(""), deviceSecret)
	if err != nil {
		return nil, err
	}
	return sandbox.NewManager(cfg, devices)
}

type SetupDependencyItem = setupcheck.DependencyItem
type SetupStatus = setupcheck.SetupStatus
type SetupInstallEvent = setupcheck.InstallEvent
type SetupInstallResult = setupcheck.InstallResult

type SandboxWarmupState = sandbox.RuntimeState

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
	if cfg.LegacyPiACPConfigMigrated() {
		if err := cfg.Save(targetPath); err != nil {
			return nil, err
		}
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
	botID := strings.TrimSpace(opts.BotID)
	if botID == "" {
		return nil, "", errors.New("bot id is required")
	}
	botSecret := strings.TrimSpace(opts.BotSecret)
	if botSecret == "" {
		return nil, "", errors.New("bot secret is required")
	}
	requesterConfigPath, err := resolveRequesterConfigPath(opts.RequesterConfigPath)
	if err != nil {
		return nil, "", err
	}
	if requesterConfigPath != "" {
		if _, err := wecom.LoadRequesterPolicy(requesterConfigPath, botID); err != nil {
			return nil, "", err
		}
	}

	cfg, workspacePath, workspaceID, agentID, err := prepareIMRunWorkspace(state, imRunWorkspaceOptions{
		Workspace:      opts.Workspace,
		Kind:           opts.Kind,
		AgentID:        opts.AgentID,
		AgentIDs:       opts.AgentIDs,
		Port:           opts.Port,
		IdleTimeoutSec: opts.IdleTimeoutSec,
		Channel:        "wecom",
		Identity:       botID,
		SandboxID:      opts.SandboxID,
	})
	if err != nil {
		return nil, "", err
	}
	if workspaceKind(opts.Kind) == "sandbox" {
		if err := wecom.ValidateRequesterConfigOutsideWorkspace(requesterConfigPath, workspacePath); err != nil {
			return nil, "", err
		}
	}

	wecomCfg := wecom.Config{
		Enabled:             true,
		Mode:                "websocket",
		BotID:               botID,
		BotSecret:           botSecret,
		WorkspaceID:         workspaceID,
		AgentID:             agentID,
		RequesterConfigPath: requesterConfigPath,
		Stream:              opts.WeComStream,
		ConnectTimeoutMs:    15000,
		HeartbeatIntervalMs: 30000,
		MessageAckTimeoutMs: 5000,
	}
	store := wecom.NewConfigStore()
	if workspaceKind(opts.Kind) == "sandbox" {
		store = wecom.NewConfigStoreForInstance(workspaceID)
	}
	if err := store.Save(wecomCfg); err != nil {
		return nil, "", err
	}

	return cfg, workspacePath, nil
}

func resolveRequesterConfigPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve requester config path: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func PrepareWeChatRun(state *ConfigState, opts WeChatRunOptions) (*config.Config, string, error) {
	cfg, workspacePath, workspaceID, agentID, err := prepareIMRunWorkspace(state, imRunWorkspaceOptions{
		Workspace:      opts.Workspace,
		Kind:           opts.Kind,
		AgentID:        opts.AgentID,
		AgentIDs:       opts.AgentIDs,
		Port:           opts.Port,
		IdleTimeoutSec: opts.IdleTimeoutSec,
		Channel:        "wechat",
		Identity:       opts.AccountID,
		SandboxID:      opts.SandboxID,
	})
	if err != nil {
		return nil, "", err
	}

	wechatCfg := wechat.Config{
		Enabled:     true,
		LoginMode:   "qr",
		AccountID:   strings.TrimSpace(opts.AccountID),
		BotToken:    strings.TrimSpace(opts.BotToken),
		BaseURL:     strings.TrimSpace(opts.BaseURL),
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	}
	if strings.TrimSpace(wechatCfg.AccountID) == "" {
		return nil, "", errors.New("account id is required")
	}
	if strings.TrimSpace(wechatCfg.BotToken) == "" {
		return nil, "", errors.New("bot token is required")
	}
	store := wechat.NewConfigStore()
	if workspaceKind(opts.Kind) == "sandbox" {
		store = wechat.NewConfigStoreForInstance(workspaceID)
	}
	if err := store.Save(wechatCfg); err != nil {
		return nil, "", err
	}

	return cfg, workspacePath, nil
}

type imRunWorkspaceOptions struct {
	Workspace      string
	Kind           string
	AgentID        string
	AgentIDs       []string
	Port           string
	IdleTimeoutSec int
	Channel        string
	Identity       string
	SandboxID      string
}

func prepareIMRunWorkspace(state *ConfigState, opts imRunWorkspaceOptions) (*config.Config, string, string, string, error) {
	if state == nil || state.Config == nil {
		return nil, "", "", "", errors.New("config state is required")
	}
	cfg := state.Config
	if len(cfg.Agents) == 0 {
		return nil, "", "", "", errors.New("no agents configured; run `lumi setup` first and prepare agents in lumi.config.json")
	}

	workspacePath, err := filepath.Abs(strings.TrimSpace(opts.Workspace))
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("workspace not found: %w", err)
	}
	if !info.IsDir() {
		return nil, "", "", "", errors.New("workspace must be a directory")
	}
	kind := workspaceKind(opts.Kind)
	if kind != "local" && kind != "sandbox" {
		return nil, "", "", "", errors.New("kind must be local or sandbox")
	}
	if opts.IdleTimeoutSec < 0 {
		return nil, "", "", "", errors.New("idle timeout sec must be non-negative")
	}

	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		return nil, "", "", "", errors.New("agent is required")
	}
	if cfg.FindAgent(agentID) == nil {
		return nil, "", "", "", fmt.Errorf("agent not found: %s; run `lumi setup` first and configure it in lumi.config.json", agentID)
	}
	workspaceAgents, err := resolveIMWorkspaceAgents(cfg, opts.AgentIDs, agentID)
	if err != nil {
		return nil, "", "", "", err
	}

	workspaceName := filepath.Base(workspacePath)
	if workspaceName == "." || workspaceName == string(filepath.Separator) || workspaceName == "" {
		workspaceName = "CLI Local Workspace"
	}
	workspaceID := WorkspaceID
	workspaceKind := "local"
	if kind == "sandbox" {
		workspaceID, err = resolveSandboxWorkspaceID(opts.Channel, opts.Identity, workspacePath, opts.SandboxID)
		if err != nil {
			return nil, "", "", "", err
		}
		workspaceKind = "sandbox"
	}
	workspace := config.WorkspaceConfig{
		ID:     workspaceID,
		Name:   workspaceName,
		Path:   workspacePath,
		Kind:   workspaceKind,
		Agents: workspaceAgents,
	}
	if kind == "sandbox" {
		workspace.Image = sandbox.ResolveImage(workspace)
		workspace.IdleTimeoutSec = IMSandboxIdleTimeoutSec
		if opts.IdleTimeoutSec > 0 {
			workspace.IdleTimeoutSec = opts.IdleTimeoutSec
		}
	}
	upsertWorkspace(cfg, workspace)
	cfg.DefaultWorkspace = workspaceID

	if err := cfg.Validate(); err != nil {
		return nil, "", "", "", err
	}
	if strings.TrimSpace(cfg.PublicServerURL) == "" {
		port := strings.TrimSpace(opts.Port)
		if port == "" {
			port = "3000"
		}
		cfg.PublicServerURL = "http://127.0.0.1:" + strings.TrimPrefix(port, ":")
	}
	if err := saveConfig(cfg, state.Path); err != nil {
		return nil, "", "", "", err
	}

	return cfg, workspacePath, workspaceID, agentID, nil
}

func workspaceKind(value string) string {
	kind := strings.TrimSpace(value)
	if kind == "" {
		return "local"
	}
	return kind
}

func resolveSandboxWorkspaceID(channel, identity, workspacePath, sandboxID string) (string, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID != "" {
		if err := validateSandboxID(sandboxID); err != nil {
			return "", err
		}
		return SandboxWorkspaceID + "-" + sandboxID, nil
	}

	channel = strings.ToLower(strings.TrimSpace(channel))
	identity = strings.TrimSpace(identity)
	if channel != "wechat" && channel != "wecom" {
		return "", errors.New("sandbox channel must be wechat or wecom")
	}
	if identity == "" {
		return "", errors.New("sandbox identity is required")
	}
	seed := channel + "\x00" + identity + "\x00" + workspacePath
	sum := sha256.Sum256([]byte(seed))
	return SandboxWorkspaceID + "-" + channel + "-" + hex.EncodeToString(sum[:])[:8], nil
}

func validateSandboxID(value string) error {
	if value == "." || value == ".." {
		return errors.New("sandbox id may not be . or ..")
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return errors.New("sandbox id may only contain letters, numbers, dots, underscores, and hyphens")
	}
	return nil
}

func resolveIMWorkspaceAgents(cfg *config.Config, requested []string, defaultAgent string) ([]string, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	seen := make(map[string]struct{})
	result := make([]string, 0, len(requested))
	add := func(id string) error {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil
		}
		if cfg.FindAgent(id) == nil {
			return fmt.Errorf("agent not found: %s; run `lumi setup` first and configure it in lumi.config.json", id)
		}
		if _, ok := seen[id]; ok {
			return nil
		}
		seen[id] = struct{}{}
		result = append(result, id)
		return nil
	}

	if len(requested) == 0 {
		for _, agent := range cfg.Agents {
			if err := add(agent.ID); err != nil {
				return nil, err
			}
		}
	} else {
		for _, id := range requested {
			if err := add(id); err != nil {
				return nil, err
			}
		}
	}
	if _, ok := seen[defaultAgent]; !ok {
		return nil, fmt.Errorf("default agent %s must be included in --agents", defaultAgent)
	}
	return result, nil
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

func (r *ServerRuntime) WarmupSandbox(ctx context.Context, workspaceID string) (SandboxWarmupState, error) {
	return r.server.WarmupSandboxByID(ctx, workspaceID)
}

func (r *ServerRuntime) EnsureSandbox(ctx context.Context, workspaceID string) (SandboxWarmupState, error) {
	return r.server.EnsureSandboxByID(ctx, workspaceID)
}

func (r *ServerRuntime) SandboxStatus(workspaceID string) (SandboxWarmupState, error) {
	return r.server.SandboxStatusByID(workspaceID)
}

func PruneSandboxes(ctx context.Context, configPath string) (SandboxPruneResult, error) {
	state, err := ResolveConfigState(configPath)
	if err != nil {
		return SandboxPruneResult{}, err
	}
	cfg := state.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	pruner, err := newSandboxPruner(cfg)
	if err != nil {
		return SandboxPruneResult{}, err
	}
	defer pruner.ShutdownPreserveContainers()
	records, err := pruner.PruneAll(ctx)
	if err != nil {
		return SandboxPruneResult{}, err
	}
	result := SandboxPruneResult{Containers: make([]SandboxPrunedContainer, 0, len(records))}
	for _, record := range records {
		result.Containers = append(result.Containers, SandboxPrunedContainer{
			WorkspaceID:    record.WorkspaceID,
			ContainerName:  record.ContainerName,
			Status:         record.Status,
			CreatedAt:      record.CreatedAt,
			StartedAt:      record.StartedAt,
			LastActivityAt: record.LastActivityAt,
			ExpiresAt:      record.ExpiresAt,
		})
	}
	return result, nil
}

func DefaultConfigPath() string {
	return lumipaths.Path("lumi.config.json")
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
