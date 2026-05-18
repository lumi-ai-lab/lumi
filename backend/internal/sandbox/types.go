package sandbox

import "github.com/pengmide/lumi/internal/config"

const (
	DefaultImage          = "lumi/sandbox:latest"
	DefaultIdleTimeoutSec = 1800
	WorkspacePath         = "/workspace"
	ConfigPath            = "/lumi/device-executor/config.json"
	RuntimePath           = "/lumi/runtime"
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
