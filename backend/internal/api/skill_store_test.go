package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/skillstore"
)

func TestSkillStoreCRUD(t *testing.T) {
	server := newTestAPIServer(t)

	// Empty list
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/skills/store", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var listEmpty struct {
		Skills []skillstore.Record `json:"skills"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listEmpty); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listEmpty.Skills) != 0 {
		t.Fatalf("empty list: %+v", listEmpty.Skills)
	}

	// Create with local source pointing at the workspace tempdir
	ws := server.config.Workspaces[0].Path
	skillDir := filepath.Join(ws, "fakeskill")
	writeAPITestSkill(t, skillDir, "FakeSkill", "desc", "body")
	body, _ := json.Marshal(map[string]any{
		"name":   "fakeskill",
		"source": map[string]any{"type": "local", "path": skillDir},
		"apps":   map[string]bool{"claude": true, "codex": false, "qwen": false, "pi": false},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/skills/store", bytes.NewReader(body))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Skill skillstore.Record `json:"skill"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Skill.ID == "" || !created.Skill.Apps.Claude {
		t.Fatalf("created = %+v", created.Skill)
	}

	// Patch apps
	patch, _ := json.Marshal(map[string]any{
		"apps": map[string]bool{"claude": true, "codex": true, "qwen": true, "pi": true},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/skills/store/"+created.Skill.ID, bytes.NewReader(patch))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patched struct {
		Skill skillstore.Record `json:"skill"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if !patched.Skill.Apps.Codex || !patched.Skill.Apps.Qwen || !patched.Skill.Apps.Pi {
		t.Fatalf("patched = %+v", patched.Skill)
	}

	// Delete
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/skills/store/"+created.Skill.ID, nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Sync (P3 stub responds OK)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/skills/store/sync", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d", rec.Code)
	}
}

func TestSkillStoreUpsertValidationError(t *testing.T) {
	server := newTestAPIServer(t)

	body := []byte(`{"name":"","source":{"type":"local","path":""}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/skills/store", bytes.NewReader(body))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
