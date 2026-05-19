package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pengmide/lumi/internal/mcpstore"
)

func TestMCPStoreCRUD(t *testing.T) {
	server := newTestAPIServer(t)

	// Empty list
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mcp/store", nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Create stdio MCP
	body, _ := json.Marshal(map[string]any{
		"name":      "filesystem",
		"transport": "stdio",
		"command":   "npx",
		"args":      []string{"@modelcontextprotocol/server-filesystem", "/tmp"},
		"apps":      map[string]bool{"claude": true, "codex": true, "qwen": false},
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/mcp/store", bytes.NewReader(body))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Server mcpstore.Record `json:"server"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Server.ID == "" || created.Server.Command != "npx" {
		t.Fatalf("created = %+v", created.Server)
	}

	// List should now include it; verify inline injection picks it up for Claude
	if got := server.agentMCPServers("claude"); len(got) != 1 {
		t.Fatalf("inline claude = %+v", got)
	}
	if got := server.agentMCPServers("qwen"); len(got) != 0 {
		t.Fatalf("inline qwen should be empty: %+v", got)
	}

	// Patch transport to http (with URL)
	patch, _ := json.Marshal(map[string]any{
		"transport": "http",
		"url":       "https://example.com",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/mcp/store/"+created.Server.ID, bytes.NewReader(patch))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var patched struct {
		Server mcpstore.Record `json:"server"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Server.Transport != "http" || patched.Server.URL != "https://example.com" {
		t.Fatalf("patched = %+v", patched.Server)
	}

	// Delete
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/mcp/store/"+created.Server.ID, nil)
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := server.agentMCPServers("claude"); len(got) != 0 {
		t.Fatalf("after delete, claude still has servers: %+v", got)
	}
}

func TestMCPStoreUpsertValidation(t *testing.T) {
	server := newTestAPIServer(t)
	body := []byte(`{"name":"x","transport":"http"}`) // missing url
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp/store", bytes.NewReader(body))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
