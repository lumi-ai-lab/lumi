package wecom

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func (s *Service) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/wecom/config" && r.Method == http.MethodGet:
		s.handleGetConfig(w, r)
	case r.URL.Path == "/api/wecom/config" && r.Method == http.MethodPost:
		s.handleSaveConfig(w, r)
	case r.URL.Path == "/api/wecom/status" && r.Method == http.MethodGet:
		s.handleStatus(w, r)
	case r.URL.Path == "/api/wecom/test" && r.Method == http.MethodPost:
		s.handleTest(w, r)
	case r.URL.Path == "/api/wecom/enable" && r.Method == http.MethodPost:
		s.handleEnable(w, r)
	case r.URL.Path == "/api/wecom/disable" && r.Method == http.MethodPost:
		s.handleDisable(w, r)
	default:
		writeError(w, "Not found", http.StatusNotFound)
	}
}

func (s *Service) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg, err := s.configStore.Load()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, SanitizeConfig(cfg))
}

func (s *Service) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var data struct {
		Enabled             bool    `json:"enabled"`
		Mode                string  `json:"mode"`
		BotID               string  `json:"botId"`
		BotSecret           *string `json:"botSecret"`
		WorkspaceID         string  `json:"workspaceId"`
		AgentID             string  `json:"agentId"`
		AllowFrom           string  `json:"allowFrom"`
		Stream              bool    `json:"stream"`
		ConnectTimeoutMs    int     `json:"connectTimeoutMs"`
		HeartbeatIntervalMs int     `json:"heartbeatIntervalMs"`
		MessageAckTimeoutMs int     `json:"messageAckTimeoutMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	current, err := s.configStore.Load()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	next := current
	next.Enabled = data.Enabled
	next.Mode = data.Mode
	next.BotID = data.BotID
	next.WorkspaceID = data.WorkspaceID
	next.AgentID = data.AgentID
	next.AllowFrom = data.AllowFrom
	next.Stream = data.Stream
	next.ConnectTimeoutMs = data.ConnectTimeoutMs
	next.HeartbeatIntervalMs = data.HeartbeatIntervalMs
	next.MessageAckTimeoutMs = data.MessageAckTimeoutMs
	if data.BotSecret != nil {
		next.BotSecret = *data.BotSecret
	}

	sanitized, err := s.SaveConfig(r.Context(), next)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"success": true,
		"config":  sanitized,
	})
}

func (s *Service) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.GetStatus()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, status)
}

func (s *Service) handleTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.TestConnection(ctx); err != nil {
		writeJSON(w, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	writeJSON(w, map[string]any{
		"success": true,
		"message": "connection ok",
	})
}

func (s *Service) handleEnable(w http.ResponseWriter, _ *http.Request) {
	if err := s.Enable(); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func (s *Service) handleDisable(w http.ResponseWriter, _ *http.Request) {
	if err := s.Disable(); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
