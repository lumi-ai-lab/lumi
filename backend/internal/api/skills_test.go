package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/skills"
)

func TestHandleSkillsReturnsProjectsDirsAndSkills(t *testing.T) {
	server := newTestAPIServer(t)
	workspace := server.config.Workspaces[0].Path
	writeAPITestSkill(t, filepath.Join(workspace, ".claude", "skills", "pdf-helper"), "PDF Helper", "Use PDFs", "# PDF")
	writeAPITestSkill(t, filepath.Join(workspace, ".agents", "skills", "codex-helper"), "Codex Helper", "Use Codex", "# Codex")
	writeAPITestSkill(t, filepath.Join(workspace, ".qwen", "skills", "qwen-helper"), "Qwen Helper", "Use Qwen", "# Qwen")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Projects []projectSkills `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Projects) != 3 {
		t.Fatalf("len(projects) = %d, want 3 (%+v)", len(payload.Projects), payload.Projects)
	}
	byAgent := make(map[string]projectSkills)
	for _, project := range payload.Projects {
		byAgent[project.AgentType] = project
	}
	if len(byAgent["claude"].Dirs) == 0 || len(byAgent["codex"].Dirs) == 0 || len(byAgent["qwen"].Dirs) == 0 {
		t.Fatalf("missing dirs in response: %+v", payload.Projects)
	}
	if byAgent["claude"].SkillCount != 1 || byAgent["codex"].SkillCount != 1 || byAgent["qwen"].SkillCount != 1 {
		t.Fatalf("skill counts = claude:%d codex:%d qwen:%d", byAgent["claude"].SkillCount, byAgent["codex"].SkillCount, byAgent["qwen"].SkillCount)
	}
	if len(byAgent["claude"].Skills) != 1 || byAgent["claude"].Skills[0].Name != "pdf-helper" {
		t.Fatalf("claude skills = %+v", byAgent["claude"].Skills)
	}
	if len(byAgent["codex"].Skills) != 1 || byAgent["codex"].Skills[0].Name != "codex-helper" {
		t.Fatalf("codex skills = %+v", byAgent["codex"].Skills)
	}
	if len(byAgent["qwen"].Skills) != 1 || byAgent["qwen"].Skills[0].Name != "qwen-helper" {
		t.Fatalf("qwen skills = %+v", byAgent["qwen"].Skills)
	}
}

func TestHandleSkillPresetsReturnsPresetSchema(t *testing.T) {
	presetSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(skills.PresetsResponse{
			Version:   1,
			UpdatedAt: "2026-01-01T00:00:00Z",
			Skills: []skills.Preset{{
				Name:        "find-skills",
				DisplayName: "Find Skills",
				Source:      &skills.Source{Provider: "skills.sh", Name: "Skills.sh", URL: "https://skills.sh"},
				Pricing:     &skills.Pricing{Type: "free"},
			}},
		})
	}))
	defer presetSource.Close()
	skills.SetPresetsURL(presetSource.URL)
	t.Cleanup(func() { skills.SetPresetsURL("") })

	server := newTestAPIServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/skills/presets", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload skills.PresetsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Version != 1 || len(payload.Skills) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Skills[0].Source == nil || payload.Skills[0].Pricing == nil {
		t.Fatalf("preset missing source/pricing: %+v", payload.Skills[0])
	}
}
