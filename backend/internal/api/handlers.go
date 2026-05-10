package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/sandbox"
)

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	s.agentCommandsMu.RLock()
	defer s.agentCommandsMu.RUnlock()

	agents := make([]map[string]any, 0, len(s.config.Agents))
	for _, a := range s.config.Agents {
		backend := agentmode.DetectBackend(a.ID, a.Command, a.Args)
		agentData := map[string]any{
			"id":             a.ID,
			"name":           a.Name,
			"backend":        string(backend),
			"permissionMode": a.PermissionMode,
			"sessionMode":    agentmode.ResolveSessionMode(backend, a.SessionMode, a.PermissionMode),
			"command":        a.Command,
			"args":           a.Args,
			"env":            a.Env,
			"availableModes": agentmode.AvailableModes(backend),
		}
		// Include cached commands if available
		if cmds, ok := s.agentCommands[a.ID]; ok {
			agentData["commands"] = cmds
		}
		agents = append(agents, agentData)
	}

	writeJSON(w, map[string]any{
		"agents":  agents,
		"default": s.config.DefaultAgent,
	})
}

func (s *Server) handleAgentUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		AgentID        string            `json:"agentId"`
		PermissionMode string            `json:"permissionMode,omitempty"`
		SessionMode    string            `json:"sessionMode,omitempty"`
		Env            map[string]string `json:"env,omitempty"`
		UpdateEnv      bool              `json:"updateEnv,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	agent := s.config.FindAgent(data.AgentID)
	if agent == nil {
		writeError(w, "Agent not found", http.StatusNotFound)
		return
	}

	backend := agentmode.DetectBackend(agent.ID, agent.Command, agent.Args)
	nextSessionMode := strings.TrimSpace(data.SessionMode)
	if nextSessionMode == "" && data.PermissionMode != "" {
		nextSessionMode = agentmode.ResolveSessionMode(backend, "", data.PermissionMode)
	}
	if nextSessionMode != "" && !agentmode.SupportsMode(backend, nextSessionMode) {
		writeError(w, "Unsupported mode for agent", http.StatusBadRequest)
		return
	}

	previous := *agent

	if nextSessionMode != "" {
		agent.SessionMode = nextSessionMode
		agent.PermissionMode = agentmode.LegacyPermissionMode(backend, nextSessionMode)
	}

	if data.UpdateEnv {
		agent.Env = data.Env
	}

	if err := agentmode.PrepareSessionMode(backend, agentmode.ResolveSessionMode(backend, agent.SessionMode, agent.PermissionMode)); err != nil {
		*agent = previous
		writeError(w, "Failed to apply agent mode: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.config.Save(""); err != nil {
		*agent = previous
		writeError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	// Stop the agent process so it will be recreated with new config on next request
	_ = s.agents.Stop(data.AgentID)

	// Clear agent initialization state so it will re-initialize
	delete(s.initialized, data.AgentID)

	// Clear all session mappings for this agent
	for convID, sessions := range s.agentSessions {
		if _, ok := sessions[data.AgentID]; ok {
			delete(s.agentSessions[convID], data.AgentID)
		}
	}
	_ = s.wechatChat.StopAgent(data.AgentID)
	_ = s.wecomChat.StopAgent(data.AgentID)

	writeJSON(w, map[string]any{"success": true, "agent": agent})
}

func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.listWorkspaces(w, r)
	case "POST":
		s.createWorkspace(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces := make([]map[string]any, 0, len(s.config.Workspaces))
	for _, ws := range s.config.Workspaces {
		entry := map[string]any{
			"id":   ws.ID,
			"name": ws.Name,
			"path": ws.Path,
		}
		if ws.Kind != "" {
			entry["kind"] = ws.Kind
		}
		if ws.DeviceID != "" {
			entry["deviceId"] = ws.DeviceID
		}
		if ws.DeviceName != "" {
			entry["deviceName"] = ws.DeviceName
		}
		if ws.RemotePath != "" {
			entry["remotePath"] = ws.RemotePath
		}
		if ws.Image != "" {
			entry["image"] = ws.Image
		}
		if ws.IdleTimeoutSec > 0 {
			entry["idleTimeoutSec"] = ws.IdleTimeoutSec
		}
		if len(ws.Agents) > 0 {
			entry["agents"] = append([]string(nil), ws.Agents...)
		}
		if isRemoteWorkspaceConfig(ws) {
			entry["deviceStatus"] = device.StatusOffline
			entry["setupReady"] = false
			if dev, ok := s.devices.GetDevice(ws.DeviceID); ok {
				entry["deviceStatus"] = dev.Status
				entry["setupReady"] = dev.SetupReady
			}
		}
		if isSandboxWorkspaceConfig(ws) {
			state := s.sandbox.Status(ws)
			entry["sandboxStatus"] = state.Status
			entry["sandboxReady"] = state.Status == sandbox.StatusRunning
			entry["sandboxExpiresAt"] = state.ExpiresAt
			if state.Stage != "" {
				entry["sandboxStage"] = state.Stage
			}
			if state.ErrorCode != "" {
				entry["sandboxError"] = state.ErrorCode
			}
		}
		workspaces = append(workspaces, entry)
	}

	writeJSON(w, map[string]any{
		"workspaces": workspaces,
		"default":    s.defaultWorkspaceID(),
	})
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Name           string   `json:"name"`
		Path           string   `json:"path"`
		Kind           string   `json:"kind,omitempty"`
		DeviceID       string   `json:"deviceId,omitempty"`
		DeviceName     string   `json:"deviceName,omitempty"`
		RemotePath     string   `json:"remotePath,omitempty"`
		Image          string   `json:"image,omitempty"`
		IdleTimeoutSec int      `json:"idleTimeoutSec,omitempty"`
		Agents         []string `json:"agents,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	data.Name = strings.TrimSpace(data.Name)
	data.Path = strings.TrimSpace(data.Path)
	data.Kind = strings.TrimSpace(data.Kind)
	data.DeviceID = strings.TrimSpace(data.DeviceID)
	data.DeviceName = strings.TrimSpace(data.DeviceName)
	data.RemotePath = strings.TrimSpace(data.RemotePath)
	data.Image = strings.TrimSpace(data.Image)

	if data.Name == "" || data.Path == "" {
		writeError(w, "name and path are required", http.StatusBadRequest)
		return
	}

	if data.Kind == "" {
		data.Kind = "local"
	}
	switch data.Kind {
	case "local", "remote", "sandbox":
	default:
		writeError(w, "Unsupported workspace kind", http.StatusBadRequest)
		return
	}

	path := data.Path
	if data.Kind == "remote" {
		if data.RemotePath == "" {
			data.RemotePath = data.Path
		}
		if data.DeviceID == "" || data.RemotePath == "" {
			writeError(w, "deviceId and remotePath are required for remote workspaces", http.StatusBadRequest)
			return
		}
		if !isAbsoluteWorkspacePath(data.RemotePath) {
			writeError(w, "remotePath must be an absolute path", http.StatusBadRequest)
			return
		}
		if _, ok := s.devices.GetDevice(data.DeviceID); !ok {
			writeError(w, "Device not found", http.StatusNotFound)
			return
		}
		path = data.RemotePath
	} else {
		if err := validateWorkspacePath(path); err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if data.Kind == "sandbox" {
		for _, agentID := range data.Agents {
			if s.config.FindAgent(agentID) == nil {
				writeError(w, "Unknown agent: "+agentID, http.StatusBadRequest)
				return
			}
		}
	}

	// Generate ID from name
	id := strings.ToLower(data.Name)
	id = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")

	// Check duplicate
	for _, ws := range s.config.Workspaces {
		if ws.ID == id {
			writeError(w, "Workspace with this name already exists", http.StatusBadRequest)
			return
		}
	}

	ws := config.WorkspaceConfig{
		ID:             id,
		Name:           data.Name,
		Path:           path,
		Kind:           data.Kind,
		DeviceID:       data.DeviceID,
		DeviceName:     data.DeviceName,
		RemotePath:     data.RemotePath,
		Image:          data.Image,
		IdleTimeoutSec: data.IdleTimeoutSec,
		Agents:         append([]string(nil), data.Agents...),
	}
	if data.Kind == "sandbox" {
		ws.Image = sandbox.ResolveImage(ws)
		ws.IdleTimeoutSec = sandbox.ResolveIdleTimeoutSec(ws)
	}
	s.config.Workspaces = append(s.config.Workspaces, ws)
	if err := s.workspaceStore.Add(ws); err != nil {
		writeError(w, "Failed to persist workspace", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"workspace": ws})
}

// validateWorkspacePath checks if the path exists and has valid format for the current OS
func validateWorkspacePath(path string) error {
	// On Windows, check for Git Bash style paths (e.g., /c/Users/...)
	if runtime.GOOS == "windows" {
		// Git Bash paths start with /drive/ (e.g., /c/, /d/)
		if len(path) >= 3 && path[0] == '/' && path[2] == '/' {
			return &pathError{
				msg: "Invalid path format for Windows. Use Windows-style path (e.g., C:\\Users\\... or C:/Users/...) instead of Git Bash path (" + path + ")",
			}
		}
		// Also check for simple Unix paths that won't work on Windows
		if len(path) > 0 && path[0] == '/' && (len(path) < 2 || path[1] != '/') {
			return &pathError{
				msg: "Invalid path format for Windows. Path appears to be Unix-style: " + path,
			}
		}
	}

	// Check if path exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &pathError{msg: "Path does not exist: " + path}
		}
		return &pathError{msg: "Cannot access path: " + path + " (" + err.Error() + ")"}
	}

	// Check if it's a directory
	if !info.IsDir() {
		return &pathError{msg: "Path is not a directory: " + path}
	}

	return nil
}

type pathError struct {
	msg string
}

func (e *pathError) Error() string {
	return e.msg
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/devices" && r.Method == "GET":
		s.devices.HandleListDevices(w, r)
	case r.URL.Path == "/api/devices/pairing-command" && r.Method == "GET":
		s.devices.HandlePairingCommand(w, inferPairingServerURL(s.config.PublicServerURL, r))
	case strings.HasSuffix(r.URL.Path, "/alias") && r.Method == "PUT":
		s.devices.HandleUpdateAlias(w, r, parseDeviceID(r.URL.Path))
	case strings.HasPrefix(r.URL.Path, "/api/devices/") && !strings.HasSuffix(r.URL.Path, "/setup/check") && r.Method == "DELETE":
		s.devices.HandleDeleteDevice(w, r, parseDeviceID(r.URL.Path))
	case strings.HasSuffix(r.URL.Path, "/setup/check") && r.Method == "POST":
		s.devices.HandleRequestSetupCheck(w, r, parseDeviceID(r.URL.Path))
	default:
		writeError(w, "Not found", http.StatusNotFound)
	}
}

func (s *Server) handlePermissionConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		AgentID    string `json:"agentId"`
		ToolCallID string `json:"toolCallId"`
		OptionID   string `json:"optionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if task, ok := s.devices.TaskByToolCall(data.ToolCallID); ok {
		err := s.devices.SendToDevice(r.Context(), task.DeviceID, device.MsgPermissionConfirm, task.ID, device.PermissionConfirmPayload{
			ToolCallID: data.ToolCallID,
			OptionID:   data.OptionID,
		})
		if err != nil {
			writeError(w, "Failed to confirm permission: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.clearPendingPermission(task.ConversationID, data.ToolCallID)
		writeJSON(w, map[string]any{"success": true})
		return
	}

	agent, err := s.agents.Get(data.AgentID)
	if err != nil {
		writeError(w, "Agent not found", http.StatusNotFound)
		return
	}

	agent.ConfirmPermission(data.ToolCallID, data.OptionID)
	s.clearPendingPermissionByToolCall(data.AgentID, data.ToolCallID)
	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) handleChatCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		AgentID   string `json:"agentId"`
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if task, ok := s.devices.TaskBySession(data.SessionID); ok {
		err := s.devices.SendToDevice(r.Context(), task.DeviceID, device.MsgTaskCancel, task.ID, device.TaskCancelPayload{
			SessionID: data.SessionID,
			Reason:    "client_cancelled",
		})
		if err != nil && err != device.ErrDeviceOffline {
			writeError(w, "Failed to cancel: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err == device.ErrDeviceOffline {
			s.devices.FinishTask(task.ID)
		}
		writeJSON(w, map[string]any{"success": true})
		return
	}

	agent, err := s.agents.Get(data.AgentID)
	if err != nil {
		writeError(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Send session/cancel notification to agent
	err = agent.Notify("session/cancel", map[string]string{
		"sessionId": data.SessionID,
	})
	if err != nil {
		writeError(w, "Failed to cancel: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) resolveWorkspace(workspaceID string) (*config.WorkspaceConfig, bool) {
	if workspaceID != "" {
		ws := s.config.FindWorkspace(workspaceID)
		return ws, ws != nil
	}

	if defaultID := s.defaultWorkspaceID(); defaultID != "" {
		ws := s.config.FindWorkspace(defaultID)
		return ws, ws != nil
	}

	return nil, false
}

func (s *Server) resolveWorkspacePath(workspaceID string) string {
	if ws, ok := s.resolveWorkspace(workspaceID); ok {
		if ws.RemotePath != "" {
			return ws.RemotePath
		}
		return ws.Path
	}

	return "."
}

func isRemoteWorkspaceConfig(ws config.WorkspaceConfig) bool {
	return ws.Kind == "remote" || ws.DeviceID != "" || ws.RemotePath != ""
}

func (s *Server) defaultWorkspaceID() string {
	if s.config.DefaultWorkspace != "" {
		if ws := s.config.FindWorkspace(s.config.DefaultWorkspace); ws != nil && !isSandboxWorkspaceConfig(*ws) {
			return ws.ID
		}
	}
	for i := range s.config.Workspaces {
		if !isSandboxWorkspaceConfig(s.config.Workspaces[i]) {
			return s.config.Workspaces[i].ID
		}
	}
	return ""
}

func isAbsoluteWorkspacePath(path string) bool {
	return filepath.IsAbs(path) || regexp.MustCompile(`^[A-Za-z]:[\\/].+`).MatchString(path)
}

func parseDeviceID(path string) string {
	trimmed := strings.TrimPrefix(path, "/api/devices/")
	trimmed = strings.TrimSuffix(trimmed, "/setup/check")
	trimmed = strings.TrimSuffix(trimmed, "/alias")
	trimmed = strings.TrimSuffix(trimmed, "/")
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}
