package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/pengmide/lumi/internal/sandbox"
)

func (s *Server) handleSandboxPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		Path           string `json:"path"`
		Image          string `json:"image"`
		CheckImagePull bool   `json:"checkImagePull"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	result := s.sandbox.Preflight(r.Context(), sandbox.PreflightRequest{
		Path:           strings.TrimSpace(data.Path),
		Image:          strings.TrimSpace(data.Image),
		CheckImagePull: data.CheckImagePull,
	})
	writeJSON(w, result)
}

func (s *Server) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	if !isLocalSandboxRequest(r) {
		writeError(w, "Not found", http.StatusNotFound)
		return
	}

	switch {
	case r.URL.Path == "/sandboxes/ensure" && r.Method == http.MethodPost:
		s.handleSandboxEnsure(w, r)
	case r.URL.Path == "/sandboxes/warmup" && r.Method == http.MethodPost:
		s.handleSandboxWarmup(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodGet:
		s.handleSandboxStatus(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && strings.HasSuffix(r.URL.Path, "/keepalive") && r.Method == http.MethodPost:
		s.handleSandboxKeepalive(w, r)
	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodDelete:
		s.handleSandboxDelete(w, r)
	default:
		writeError(w, "Not found", http.StatusNotFound)
	}
}

func (s *Server) handleSandboxEnsure(w http.ResponseWriter, r *http.Request) {
	var data struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	workspace, ok := s.resolveWorkspace(strings.TrimSpace(data.WorkspaceID))
	if !ok {
		writeError(w, "Workspace not found", http.StatusNotFound)
		return
	}

	runtimeState, runtimeErr := s.sandbox.Ensure(r.Context(), sandbox.EnsureOptions{
		Workspace:  *workspace,
		BackendURL: inferServerURL(r),
	})
	if runtimeErr != nil {
		writeSandboxRuntimeError(w, runtimeErr)
		return
	}
	writeJSON(w, runtimeState)
}

func (s *Server) handleSandboxWarmup(w http.ResponseWriter, r *http.Request) {
	var data struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	workspace, ok := s.resolveWorkspace(strings.TrimSpace(data.WorkspaceID))
	if !ok {
		writeError(w, "Workspace not found", http.StatusNotFound)
		return
	}
	if !isSandboxWorkspaceConfig(*workspace) {
		writeError(w, "Workspace is not a sandbox", http.StatusBadRequest)
		return
	}

	runtimeState := s.sandbox.Warmup(r.Context(), sandbox.EnsureOptions{
		Workspace:  *workspace,
		BackendURL: inferServerURL(r),
	})
	writeJSON(w, runtimeState)
}

func (s *Server) handleSandboxStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseWorkspaceIDFromSandboxPath(r.URL.Path)
	if workspaceID == "" {
		writeError(w, "Workspace ID required", http.StatusBadRequest)
		return
	}

	workspace, ok := s.resolveWorkspace(workspaceID)
	if !ok {
		writeError(w, "Workspace not found", http.StatusNotFound)
		return
	}

	writeJSON(w, s.sandbox.Status(*workspace))
}

func (s *Server) handleSandboxKeepalive(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseWorkspaceIDFromSandboxPath(r.URL.Path)
	if workspaceID == "" {
		writeError(w, "Workspace ID required", http.StatusBadRequest)
		return
	}

	workspace, ok := s.resolveWorkspace(workspaceID)
	if !ok {
		writeError(w, "Workspace not found", http.StatusNotFound)
		return
	}

	s.sandbox.KeepAlive(*workspace)
	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) handleSandboxDelete(w http.ResponseWriter, r *http.Request) {
	workspaceID := parseWorkspaceIDFromSandboxPath(r.URL.Path)
	if workspaceID == "" {
		writeError(w, "Workspace ID required", http.StatusBadRequest)
		return
	}

	if err := s.sandbox.Terminate(context.Background(), workspaceID); err != nil {
		writeError(w, "Failed to terminate sandbox", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func parseWorkspaceIDFromSandboxPath(path string) string {
	trimmed := strings.TrimPrefix(path, "/sandboxes/")
	trimmed = strings.TrimSuffix(trimmed, "/keepalive")
	trimmed = strings.Trim(trimmed, "/")
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

func isLocalSandboxRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
