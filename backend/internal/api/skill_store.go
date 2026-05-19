package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pengmide/lumi/internal/skillstore"
)

// handleSkillStore dispatches /api/skills/store and /api/skills/store/{id}
// requests. The trailing-slash form supports nested operations like
// /api/skills/store/{id}/sync if added later.
func (s *Server) handleSkillStore(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/skills/store")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		s.listSkillStore(w, r)
	case path == "" && r.Method == http.MethodPost:
		s.upsertSkillStore(w, r)
	case path == "sync" && r.Method == http.MethodPost:
		s.syncSkillStore(w, r)
	case path != "" && r.Method == http.MethodPatch:
		s.patchSkillStore(w, r, path)
	case path != "" && r.Method == http.MethodDelete:
		s.deleteSkillStore(w, r, path)
	default:
		writeError(w, "Not found", http.StatusNotFound)
	}
}

func (s *Server) listSkillStore(w http.ResponseWriter, _ *http.Request) {
	if s.skillStore == nil {
		writeJSON(w, map[string]any{"skills": []any{}})
		return
	}
	writeJSON(w, map[string]any{"skills": s.skillStore.List()})
}

type skillStoreUpsertRequest struct {
	ID          string             `json:"id,omitempty"`
	Name        string             `json:"name"`
	DisplayName string             `json:"displayName,omitempty"`
	Description string             `json:"description,omitempty"`
	Source      skillstore.Source  `json:"source"`
	Apps        *skillstore.Apps   `json:"apps,omitempty"`
	Scopes      *skillstore.Scopes `json:"scopes,omitempty"`
}

func (s *Server) upsertSkillStore(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		writeError(w, "skill store unavailable", http.StatusServiceUnavailable)
		return
	}
	var req skillStoreUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	rec := skillstore.Record{
		ID:          req.ID,
		Name:        strings.TrimSpace(req.Name),
		DisplayName: req.DisplayName,
		Description: req.Description,
		Source:      req.Source,
	}
	if req.Apps != nil {
		rec.Apps = *req.Apps
	}
	if req.Scopes != nil {
		rec.Scopes = *req.Scopes
	} else {
		rec.Scopes = skillstore.DefaultScopes()
	}
	saved, err := s.skillStore.Upsert(rec)
	if err != nil {
		status := http.StatusInternalServerError
		if skillstore.IsValidationError(err) {
			status = http.StatusBadRequest
		}
		writeError(w, err.Error(), status)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"skill": saved, "sync": summary})
}

type skillStorePatchRequest struct {
	Name        *string            `json:"name,omitempty"`
	DisplayName *string            `json:"displayName,omitempty"`
	Description *string            `json:"description,omitempty"`
	Apps        *skillstore.Apps   `json:"apps,omitempty"`
	Scopes      *skillstore.Scopes `json:"scopes,omitempty"`
}

func (s *Server) patchSkillStore(w http.ResponseWriter, r *http.Request, id string) {
	if s.skillStore == nil {
		writeError(w, "skill store unavailable", http.StatusServiceUnavailable)
		return
	}
	existing, ok := s.skillStore.Get(id)
	if !ok {
		writeError(w, "skill not found", http.StatusNotFound)
		return
	}
	var req skillStorePatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name != nil {
		existing.Name = strings.TrimSpace(*req.Name)
	}
	if req.DisplayName != nil {
		existing.DisplayName = *req.DisplayName
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Apps != nil {
		existing.Apps = *req.Apps
	}
	if req.Scopes != nil {
		existing.Scopes = *req.Scopes
	}
	saved, err := s.skillStore.Upsert(existing)
	if err != nil {
		status := http.StatusInternalServerError
		if skillstore.IsValidationError(err) {
			status = http.StatusBadRequest
		}
		writeError(w, err.Error(), status)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"skill": saved, "sync": summary})
}

func (s *Server) deleteSkillStore(w http.ResponseWriter, r *http.Request, id string) {
	if s.skillStore == nil {
		writeError(w, "skill store unavailable", http.StatusServiceUnavailable)
		return
	}
	removed, err := s.skillStore.Delete(id)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !removed {
		writeError(w, "skill not found", http.StatusNotFound)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"success": true, "sync": summary})
}

// syncSkillStore re-applies the SSOT to all enabled targets on demand.
func (s *Server) syncSkillStore(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		writeError(w, "skill store unavailable", http.StatusServiceUnavailable)
		return
	}
	summary := s.localSyncAll(r.Context())
	writeJSON(w, map[string]any{"success": true, "sync": summary})
}

var errSkillStoreUnavailable = errors.New("skill store unavailable")

// SkillStoreUnavailable lets future packages detect the absence of a store.
func SkillStoreUnavailable() error { return errSkillStoreUnavailable }
