package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/fssecure"
	"github.com/pengmide/lumi/internal/requestercontext"
)

const (
	DefaultImage          = "ghcr.io/lumi-ai-lab/lumi-sandbox:latest"
	DefaultIdleTimeoutSec = 1800
	WorkspacePath         = "/workspace"
	ConfigPath            = "/lumi/device-executor/config.json"
	RuntimePath           = "/lumi/runtime"
	RequesterContextPath  = "/run/lumi/requester-context"
)

const (
	StatusPending     = "pending"
	StatusRunning     = "running"
	StatusFailed      = "failed"
	StatusTerminating = "terminating"
	StatusTerminated  = "terminated"
)

const (
	StageCheckingDocker    = "checking_docker"
	StagePreparingImage    = "preparing_image"
	StageStartingContainer = "starting_container"
	StageConnectingExec    = "connecting_executor"
	StageBootstrapping     = "bootstrapping_runtime"
)

const (
	CodeReady                       = "ready"
	CodePathInvalid                 = "path_invalid"
	CodeDockerUnavailable           = "docker_unavailable"
	CodeDockerPermissionDenied      = "docker_permission_denied"
	CodeImageMissing                = "image_missing"
	CodeImagePullFailed             = "image_pull_failed"
	CodeHostConnectUnresolved       = "host_connect_unresolved"
	CodeExecutorRegistrationTimeout = "executor_registration_timeout"
	CodeSandboxUnavailable          = "sandbox_unavailable"
	CodeUnknown                     = "unknown"
)

type PreflightRequest struct {
	Path           string
	Image          string
	CheckImagePull bool
}

type PreflightResponse struct {
	OK          bool   `json:"ok"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
	Details     string `json:"details,omitempty"`
}

type RuntimeRecord struct {
	WorkspaceID    string `json:"workspaceId"`
	DeviceID       string `json:"deviceId"`
	ContainerName  string `json:"containerName"`
	Image          string `json:"image"`
	HostPath       string `json:"hostPath"`
	WorkspacePath  string `json:"workspacePath"`
	Status         string `json:"status"`
	Stage          string `json:"stage,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	ErrorDetails   string `json:"errorDetails,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	StartedAt      int64  `json:"startedAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
	ExpiresAt      int64  `json:"expiresAt"`
}

type RuntimeState = RuntimeRecord

type EnsureOptions struct {
	Workspace  config.WorkspaceConfig
	BackendURL string
}

type requesterContextContainerSettings struct {
	Root      string
	ReaderGID *uint32
}

func (settings requesterContextContainerSettings) Secure() bool {
	return settings.Root != "" && settings.ReaderGID != nil
}

func resolveRequesterContextContainerSettings(cfg *config.Config, workspace config.WorkspaceConfig) (requesterContextContainerSettings, error) {
	settings, err := requestercontext.RuntimeSettingsFromEnv("")
	if err != nil {
		return requesterContextContainerSettings{}, err
	}
	if !settings.Secure() {
		return requesterContextContainerSettings{}, nil
	}
	if cfg == nil {
		return requesterContextContainerSettings{}, fmt.Errorf("Lumi config is required for secured Sandbox requester context")
	}
	for _, agentCfg := range filterAgents(cfg, workspace.Agents) {
		if agentCfg.ID != "pi" {
			continue
		}
		if err := agentCfg.ValidateRunAsIdentity(); err != nil || agentCfg.RunAsUID == nil {
			return requesterContextContainerSettings{}, fmt.Errorf("secured Sandbox pi agent requires a complete non-root run-as identity")
		}
		if !agentCfg.HasRunAsGroup(*settings.ReaderGID) {
			return requesterContextContainerSettings{}, fmt.Errorf("secured Sandbox pi agent does not receive requester context reader GID %d", *settings.ReaderGID)
		}
	}
	gid := *settings.ReaderGID
	return requesterContextContainerSettings{Root: RequesterContextPath, ReaderGID: &gid}, nil
}

// prepareRequesterContextMount creates a publisher-controlled, per-workspace
// bind source for secured Sandbox requester context. It intentionally lives
// outside the shared /lumi/runtime mount, whose owner may be the run-as Agent.
func (m *Manager) prepareRequesterContextMount(workspaceID string, settings requesterContextContainerSettings) (string, error) {
	if !settings.Secure() {
		return "", nil
	}
	if settings.Root != RequesterContextPath {
		return "", fmt.Errorf("secured Sandbox requester-context root must be %s", RequesterContextPath)
	}

	sandboxesRoot := filepath.Join(m.runtimeDir, "sandboxes")
	source, err := requestercontext.SessionDir(sandboxesRoot, workspaceID, "requester-context")
	if err != nil {
		return "", fmt.Errorf("resolve Sandbox requester-context mount source: %w", err)
	}
	workspaceRoot := filepath.Dir(source)
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return "", fmt.Errorf("create Sandbox workspace runtime directory: %w", err)
	}
	workspaceInfo, err := os.Lstat(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("inspect Sandbox workspace runtime directory: %w", err)
	}
	if workspaceInfo.Mode()&os.ModeSymlink != 0 || !workspaceInfo.IsDir() {
		return "", fmt.Errorf("Sandbox workspace runtime directory %q must be a real directory", workspaceRoot)
	}
	if err := fssecure.EnsureDirectory(source, 0o710, settings.ReaderGID); err != nil {
		return "", fmt.Errorf("prepare Sandbox requester-context mount source: %w", err)
	}
	return source, nil
}

func ResolveImage(ws config.WorkspaceConfig) string {
	if ws.Image != "" {
		return ws.Image
	}
	return DefaultImage
}

func ResolveIdleTimeoutSec(ws config.WorkspaceConfig) int {
	if ws.IdleTimeoutSec > 0 {
		return ws.IdleTimeoutSec
	}
	return DefaultIdleTimeoutSec
}
