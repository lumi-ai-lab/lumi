package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/sandbox"
	workspacepreview "github.com/pengmide/lumi/internal/workspace"
)

type ResolvedRuntime struct {
	Mode          string
	Workspace     config.WorkspaceConfig
	WorkspaceID   string
	WorkspacePath string
	DeviceID      string
	Ready         bool
	Status        string
	Stage         string
	ExpiresAt     int64
	ErrorCode     string
}

func isSandboxWorkspaceConfig(ws config.WorkspaceConfig) bool {
	return ws.Kind == "sandbox"
}

func (s *Server) resolveWorkspaceRuntime(ctx context.Context, workspaceID string, req *http.Request) (ResolvedRuntime, error) {
	workspaceConfig, ok := s.resolveWorkspace(workspaceID)
	if !ok {
		return ResolvedRuntime{}, errors.New("workspace not found")
	}

	if isSandboxWorkspaceConfig(*workspaceConfig) {
		return s.resolveSandboxRuntime(ctx, *workspaceConfig, req)
	}
	if isRemoteWorkspaceConfig(*workspaceConfig) {
		workspacePath := workspaceConfig.RemotePath
		if workspacePath == "" {
			workspacePath = workspaceConfig.Path
		}
		return ResolvedRuntime{
			Mode:          "remote",
			Workspace:     *workspaceConfig,
			WorkspaceID:   workspaceConfig.ID,
			WorkspacePath: workspacePath,
			DeviceID:      workspaceConfig.DeviceID,
			Ready:         true,
			Status:        "running",
		}, nil
	}

	return ResolvedRuntime{
		Mode:          "local",
		Workspace:     *workspaceConfig,
		WorkspaceID:   workspaceConfig.ID,
		WorkspacePath: workspaceConfig.Path,
		Ready:         true,
		Status:        "running",
	}, nil
}

func (s *Server) resolveSandboxRuntime(ctx context.Context, workspace config.WorkspaceConfig, req *http.Request) (ResolvedRuntime, error) {
	runtimeState, runtimeErr := s.sandbox.Ensure(ctx, sandbox.EnsureOptions{
		Workspace:  workspace,
		BackendURL: s.backendURLForSandbox(req),
	})
	if runtimeErr != nil {
		return ResolvedRuntime{
			Mode:          "sandbox",
			Workspace:     workspace,
			WorkspaceID:   workspace.ID,
			WorkspacePath: sandbox.WorkspacePath,
			DeviceID:      runtimeState.DeviceID,
			Ready:         false,
			Status:        runtimeState.Status,
			Stage:         runtimeState.Stage,
			ExpiresAt:     runtimeState.ExpiresAt,
			ErrorCode:     runtimeState.ErrorCode,
		}, runtimeErr
	}

	return ResolvedRuntime{
		Mode:          "sandbox",
		Workspace:     workspace,
		WorkspaceID:   workspace.ID,
		WorkspacePath: sandbox.WorkspacePath,
		DeviceID:      runtimeState.DeviceID,
		Ready:         runtimeState.Status == sandbox.StatusRunning,
		Status:        runtimeState.Status,
		Stage:         runtimeState.Stage,
		ExpiresAt:     runtimeState.ExpiresAt,
		ErrorCode:     runtimeState.ErrorCode,
	}, nil
}

func (s *Server) backendURLForSandbox(req *http.Request) string {
	if req != nil {
		return inferServerURL(req)
	}
	if s != nil && s.config != nil && strings.TrimSpace(s.config.PublicServerURL) != "" {
		return strings.TrimRight(strings.TrimSpace(s.config.PublicServerURL), "/")
	}
	return inferServerURL(nil)
}

func (s *Server) deviceWorkspacePayload(ctx context.Context, deviceID string, workspacePath string, typ device.MessageType, payload device.WorkspaceRequestPayload, out any) error {
	if deviceID == "" {
		return device.WorkspaceError{Code: "invalid_path", Message: "Remote workspace device is missing"}
	}
	payload.WorkspacePath = workspacePath

	raw, err := s.devices.SendWorkspaceRequest(ctx, deviceID, typ, payload)
	if err != nil {
		return remoteWorkspaceError(err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}

func (s *Server) deviceWorkspaceBuffer(ctx context.Context, deviceID string, workspacePath string, relativePath string) (workspacepreview.FileMeta, []byte, error) {
	var response remoteWorkspaceBuffer
	err := s.deviceWorkspacePayload(ctx, deviceID, workspacePath, device.MsgWorkspaceBuffer, device.WorkspaceRequestPayload{
		Path: relativePath,
	}, &response)
	if err != nil {
		return workspacepreview.FileMeta{}, nil, err
	}
	data, err := base64.StdEncoding.DecodeString(response.Content)
	if err != nil {
		return workspacepreview.FileMeta{}, nil, err
	}
	return response.Meta, data, nil
}

func writeSandboxRuntimeError(w http.ResponseWriter, err error) {
	var runtimeErr *sandbox.RuntimeError
	if errors.As(err, &runtimeErr) {
		writeErrorDetails(
			w,
			sandboxRuntimeErrorCode(runtimeErr),
			runtimeErr.Message,
			runtimeErr.Details,
			sandboxRuntimeHTTPStatus(runtimeErr),
		)
		return
	}
	writeErrorDetails(w, sandbox.CodeUnknown, "Sandbox runtime failed", "", http.StatusServiceUnavailable)
}

func writeResolvedRuntimeError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var runtimeErr *sandbox.RuntimeError
	if errors.As(err, &runtimeErr) {
		writeSandboxRuntimeError(w, runtimeErr)
		return
	}
	if err.Error() == "workspace not found" {
		writeError(w, "Workspace not found", http.StatusNotFound)
		return
	}
	writeError(w, err.Error(), http.StatusInternalServerError)
}

func writeRuntimeWorkspaceError(w http.ResponseWriter, runtime ResolvedRuntime, err error) {
	if runtime.Mode == "local" {
		writeWorkspacePreviewError(w, err)
		return
	}
	if runtime.Mode == "sandbox" && isSandboxRuntimeAccessError(err) {
		writeErrorDetails(
			w,
			sandbox.CodeSandboxUnavailable,
			"Sandbox runtime is unavailable",
			"",
			http.StatusServiceUnavailable,
		)
		return
	}
	writeRemoteWorkspaceError(w, err)
}

func isSandboxRuntimeAccessError(err error) bool {
	return errors.Is(err, device.ErrDeviceNotFound) ||
		errors.Is(err, device.ErrDeviceOffline) ||
		errors.Is(err, device.ErrSetupNotReady) ||
		errors.Is(err, workspacepreview.ErrWorkspaceUnavailable)
}

func (s *Server) touchResolvedRuntime(runtime ResolvedRuntime) {
	if runtime.Mode == "sandbox" {
		s.sandbox.KeepAlive(runtime.Workspace)
	}
}

func sandboxRuntimeHTTPStatus(runtimeErr *sandbox.RuntimeError) int {
	if runtimeErr != nil && runtimeErr.Code == sandbox.CodePathInvalid {
		return http.StatusBadRequest
	}
	return http.StatusServiceUnavailable
}

func sandboxRuntimeErrorCode(runtimeErr *sandbox.RuntimeError) string {
	if runtimeErr == nil || strings.TrimSpace(runtimeErr.Code) == "" {
		return sandbox.CodeUnknown
	}
	return runtimeErr.Code
}

func runtimeErrorEventPayload(err error) map[string]string {
	var runtimeErr *sandbox.RuntimeError
	if errors.As(err, &runtimeErr) {
		code := sandboxRuntimeErrorCode(runtimeErr)
		payload := map[string]string{
			"message": code,
			"code":    code,
		}
		if strings.TrimSpace(runtimeErr.Message) != "" {
			payload["description"] = runtimeErr.Message
		}
		return payload
	}
	return map[string]string{"message": err.Error()}
}

func inferServerURL(req *http.Request) string {
	if req == nil {
		return "http://localhost:3000"
	}
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(req.Host)
	if host == "" {
		host = "localhost:3000"
	}
	return scheme + "://" + host
}
