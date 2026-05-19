package mcpstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreUpsertGetListDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "mcp.json"))
	if err := s.Load(); err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List empty = %v, want 0", got)
	}

	rec := Record{
		Name:      "filesystem",
		Transport: TransportStdio,
		Command:   "npx",
		Args:      []string{"@modelcontextprotocol/server-filesystem", "/tmp"},
		Apps:      Apps{Claude: true, Codex: true, Qwen: false},
		Scopes:    DefaultScopes(),
	}
	saved, err := s.Upsert(rec)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.ID == "" || saved.CreatedAt == 0 || saved.UpdatedAt == 0 {
		t.Fatalf("Upsert auto-fields missing: %+v", saved)
	}
	if got, ok := s.Get(saved.ID); !ok || got.Name != "filesystem" {
		t.Fatalf("Get(%s) = %+v ok=%v", saved.ID, got, ok)
	}

	updated := saved.Clone()
	updated.Args = []string{"@modelcontextprotocol/server-filesystem", "/srv"}
	if _, err := s.Upsert(updated); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	got, _ := s.Get(saved.ID)
	if len(got.Args) != 2 || got.Args[1] != "/srv" {
		t.Fatalf("update args = %v", got.Args)
	}
	if got.CreatedAt != saved.CreatedAt {
		t.Fatalf("update reset createdAt: %d vs %d", got.CreatedAt, saved.CreatedAt)
	}

	removed, err := s.Delete(saved.ID)
	if err != nil || !removed {
		t.Fatalf("Delete = %v err=%v", removed, err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List after delete = %v", got)
	}
}

func TestStorePersistsAcrossLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	s1 := New(path)
	if _, err := s1.Upsert(Record{
		Name: "remote", Transport: TransportHTTP, URL: "https://example.com",
		Apps: Apps{Claude: true},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	s2 := New(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s2.List()
	if len(got) != 1 || got[0].URL != "https://example.com" {
		t.Fatalf("reload mismatch: %+v", got)
	}
}

func TestStoreLoadHonorsExistingVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	raw := `{"version":42,"servers":[{"id":"x","name":"x","transport":"stdio","command":"echo","apps":{"claude":true},"scopes":{"local":true}}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(path)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.List(); len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("list = %+v", got)
	}
}

func TestRecordValidate(t *testing.T) {
	cases := []struct {
		name string
		r    Record
		ok   bool
	}{
		{"missing id+name", Record{}, false},
		{"stdio missing command", Record{ID: "a", Name: "a", Transport: TransportStdio}, false},
		{"http missing url", Record{ID: "a", Name: "a", Transport: TransportHTTP}, false},
		{"unknown transport", Record{ID: "a", Name: "a", Transport: Transport("foo")}, false},
		{"stdio ok", Record{ID: "a", Name: "a", Transport: TransportStdio, Command: "x"}, true},
		{"http ok", Record{ID: "a", Name: "a", Transport: TransportHTTP, URL: "https://x"}, true},
		{"default transport", Record{ID: "a", Name: "a", Command: "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !tc.ok && !IsValidationError(err) {
				t.Fatalf("want validation error, got %T", err)
			}
		})
	}
}

func TestAppsHelpers(t *testing.T) {
	a := Apps{Claude: true, Codex: false, Qwen: true}
	if !a.IsEnabledFor("claude") || a.IsEnabledFor("codex") || !a.IsEnabledFor("qwen") {
		t.Fatalf("IsEnabledFor mismatch: %+v", a)
	}
	a.SetEnabledFor("Codex", true)
	if !a.Codex {
		t.Fatalf("SetEnabledFor case-insensitive failed")
	}
}

func TestStoreFileShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	s := New(path)
	if _, err := s.Upsert(Record{
		Name: "fs", Transport: TransportStdio, Command: "x",
		Apps: Apps{Claude: true},
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if f.Version != CurrentVersion || len(f.Servers) != 1 {
		t.Fatalf("shape = %+v", f)
	}
}
