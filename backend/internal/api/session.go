package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/storage"
)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.sessionStore.List()
	writeJSON(w, map[string]any{"sessions": sessions})
}

func (s *Server) handleSessionNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		WorkspaceID string `json:"workspaceId"`
	}
	json.NewDecoder(r.Body).Decode(&data)

	id := generateUUID()
	workspaceID := data.WorkspaceID
	if workspaceID == "" {
		workspaceID = s.defaultWorkspaceID()
	}

	session := storage.CreateSession(id, s.config.DefaultAgent, workspaceID)
	s.sessionStore.Save(session)
	s.conversations.Create(id, s.config.DefaultAgent, workspaceID)
	s.agentSessions[id] = make(map[string]string)

	writeJSON(w, map[string]any{
		"session": map[string]any{
			"id":           session.ID,
			"title":        session.Title,
			"activeAgent":  session.ActiveAgent,
			"workspaceId":  session.WorkspaceID,
			"messageCount": 0,
			"createdAt":    session.CreatedAt,
			"updatedAt":    session.UpdatedAt,
		},
	})
}

func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" {
		writeError(w, "Session ID required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		session, err := s.sessionStore.Load(id)
		if err != nil {
			writeError(w, "Session not found", http.StatusNotFound)
			return
		}
		s.restoreConversation(session)
		if pending, ok := s.getPendingPermission(id); ok {
			session.PendingPermission = &pending
		}
		writeJSON(w, map[string]any{"session": session})

	case "DELETE":
		if s.cron != nil {
			_, _ = s.cron.DeleteByScope(lumicron.ChannelWeb, id)
		}
		s.sessionStore.Delete(id)
		_ = s.shareStore.RemoveByConversation(id)
		s.conversations.Delete(id)
		delete(s.agentSessions, id)
		s.remoteSessionsMu.Lock()
		delete(s.remoteAgentSessions, id)
		s.remoteSessionsMu.Unlock()
		writeJSON(w, map[string]any{"success": true})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) restoreConversation(session *storage.StoredSession) {
	s.conversations.Create(session.ID, session.ActiveAgent, session.WorkspaceID)
	for _, msg := range session.Messages {
		s.conversations.AddMessage(session.ID, msg)
	}
	if sessionID := session.AgentSessions[session.ActiveAgent]; sessionID != "" {
		s.conversations.SetSessionID(session.ID, sessionID)
	}
	s.agentSessions[session.ID] = cloneAgentSessions(session.AgentSessions)
	s.remoteSessionsMu.Lock()
	s.remoteAgentSessions[session.ID] = cloneRemoteAgentSessions(session.RemoteAgentSessions)
	s.remoteSessionsMu.Unlock()
}

func (s *Server) persistConversation(convID string) {
	conv := s.conversations.Get(convID)
	if conv == nil {
		return
	}

	session := &storage.StoredSession{
		ID:                  convID,
		Title:               storage.GenerateTitle(conv.Messages),
		Messages:            conv.Messages,
		ActiveAgent:         conv.ActiveAgent,
		WorkspaceID:         conv.WorkspaceID,
		AgentSessions:       s.snapshotAgentSessions(convID),
		RemoteAgentSessions: s.snapshotRemoteAgentSessions(convID),
		CreatedAt:           conv.CreatedAt,
		UpdatedAt:           time.Now().UnixMilli(),
	}

	s.sessionStore.Save(session)
}

func (s *Server) snapshotAgentSessions(convID string) map[string]string {
	if s == nil || s.agentSessions == nil {
		return nil
	}
	return cloneAgentSessions(s.agentSessions[convID])
}

func (s *Server) snapshotRemoteAgentSessions(convID string) map[string]map[string]string {
	if s == nil || s.remoteAgentSessions == nil {
		return nil
	}
	s.remoteSessionsMu.RLock()
	defer s.remoteSessionsMu.RUnlock()
	return cloneRemoteAgentSessions(s.remoteAgentSessions[convID])
}

func cloneAgentSessions(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]string, len(source))
	for agentID, sessionID := range source {
		if sessionID != "" {
			out[agentID] = sessionID
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneRemoteAgentSessions(source map[string]map[string]string) map[string]map[string]string {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(source))
	for deviceID, byAgent := range source {
		cloned := cloneAgentSessions(byAgent)
		if len(cloned) > 0 {
			out[deviceID] = cloned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
