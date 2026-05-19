package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pengmide/lumi/internal/mcpstore"
)

// handleMCPStore dispatches /api/mcp/store and /api/mcp/store/{id}.
func (s *Server) handleMCPStore(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/mcp/store")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		s.listMCPStore(w, r)
	case path == "" && r.Method == http.MethodPost:
		s.upsertMCPStore(w, r)
	case path == "sync" && r.Method == http.MethodPost:
		s.syncMCPStore(w, r)
	case path != "" && r.Method == http.MethodPatch:
		s.patchMCPStore(w, r, path)
	case path != "" && r.Method == http.MethodDelete:
		s.deleteMCPStore(w, r, path)
	default:
		writeError(w, "Not found", http.StatusNotFound)
	}
}

func (s *Server) listMCPStore(w http.ResponseWriter, _ *http.Request) {
	if s.mcpStore == nil {
		writeJSON(w, map[string]any{"servers": []any{}})
		return
	}
	writeJSON(w, map[string]any{"servers": s.mcpStore.List()})
}

type mcpStoreUpsertRequest struct {
	ID        string             `json:"id,omitempty"`
	Name      string             `json:"name"`
	Transport mcpstore.Transport `json:"transport,omitempty"`
	Command   string             `json:"command,omitempty"`
	Args      []string           `json:"args,omitempty"`
	Env       map[string]string  `json:"env,omitempty"`
	URL       string             `json:"url,omitempty"`
	Headers   map[string]string  `json:"headers,omitempty"`
	Apps      *mcpstore.Apps     `json:"apps,omitempty"`
	Scopes    *mcpstore.Scopes   `json:"scopes,omitempty"`
}

func (s *Server) upsertMCPStore(w http.ResponseWriter, r *http.Request) {
	if s.mcpStore == nil {
		writeError(w, "mcp store unavailable", http.StatusServiceUnavailable)
		return
	}
	var req mcpStoreUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	rec := mcpstore.Record{
		ID:        req.ID,
		Name:      strings.TrimSpace(req.Name),
		Transport: req.Transport,
		Command:   req.Command,
		Args:      req.Args,
		Env:       req.Env,
		URL:       req.URL,
		Headers:   req.Headers,
	}
	if req.Apps != nil {
		rec.Apps = *req.Apps
	}
	if req.Scopes != nil {
		rec.Scopes = *req.Scopes
	} else {
		rec.Scopes = mcpstore.DefaultScopes()
	}
	saved, err := s.mcpStore.Upsert(rec)
	if err != nil {
		status := http.StatusInternalServerError
		if mcpstore.IsValidationError(err) {
			status = http.StatusBadRequest
		}
		writeError(w, err.Error(), status)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"server": saved, "sync": summary})
}

type mcpStorePatchRequest struct {
	Name      *string            `json:"name,omitempty"`
	Transport *mcpstore.Transport `json:"transport,omitempty"`
	Command   *string            `json:"command,omitempty"`
	Args      *[]string          `json:"args,omitempty"`
	Env       *map[string]string `json:"env,omitempty"`
	URL       *string            `json:"url,omitempty"`
	Headers   *map[string]string `json:"headers,omitempty"`
	Apps      *mcpstore.Apps     `json:"apps,omitempty"`
	Scopes    *mcpstore.Scopes   `json:"scopes,omitempty"`
}

func (s *Server) patchMCPStore(w http.ResponseWriter, r *http.Request, id string) {
	if s.mcpStore == nil {
		writeError(w, "mcp store unavailable", http.StatusServiceUnavailable)
		return
	}
	existing, ok := s.mcpStore.Get(id)
	if !ok {
		writeError(w, "mcp not found", http.StatusNotFound)
		return
	}
	var req mcpStorePatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name != nil {
		existing.Name = strings.TrimSpace(*req.Name)
	}
	if req.Transport != nil {
		existing.Transport = *req.Transport
	}
	if req.Command != nil {
		existing.Command = *req.Command
	}
	if req.Args != nil {
		existing.Args = *req.Args
	}
	if req.Env != nil {
		existing.Env = *req.Env
	}
	if req.URL != nil {
		existing.URL = *req.URL
	}
	if req.Headers != nil {
		existing.Headers = *req.Headers
	}
	if req.Apps != nil {
		existing.Apps = *req.Apps
	}
	if req.Scopes != nil {
		existing.Scopes = *req.Scopes
	}
	saved, err := s.mcpStore.Upsert(existing)
	if err != nil {
		status := http.StatusInternalServerError
		if mcpstore.IsValidationError(err) {
			status = http.StatusBadRequest
		}
		writeError(w, err.Error(), status)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"server": saved, "sync": summary})
}

func (s *Server) deleteMCPStore(w http.ResponseWriter, r *http.Request, id string) {
	if s.mcpStore == nil {
		writeError(w, "mcp store unavailable", http.StatusServiceUnavailable)
		return
	}
	removed, err := s.mcpStore.Delete(id)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !removed {
		writeError(w, "mcp not found", http.StatusNotFound)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"success": true, "sync": summary})
}

func (s *Server) syncMCPStore(w http.ResponseWriter, r *http.Request) {
	if s.mcpStore == nil {
		writeError(w, "mcp store unavailable", http.StatusServiceUnavailable)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"success": true, "sync": summary})
}
