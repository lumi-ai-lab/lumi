package skillstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "skills.json"), filepath.Join(dir, "_cache"), filepath.Join(dir, "_archives"))
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List empty = %+v", got)
	}

	saved, err := s.Upsert(Record{
		Name:   "code-reviewer",
		Source: Source{Type: SourceLocal, Path: "/abs/path"},
		Apps:   Apps{Claude: true},
		Scopes: DefaultScopes(),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if saved.ID == "" {
		t.Fatalf("Upsert returned empty id")
	}

	if got, ok := s.Get(saved.ID); !ok || got.Name != "code-reviewer" {
		t.Fatalf("Get(%s)=%+v ok=%v", saved.ID, got, ok)
	}

	updated := saved
	updated.Description = "updated"
	if _, err := s.Upsert(updated); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}

	got, _ := s.Get(saved.ID)
	if got.Description != "updated" || got.CreatedAt != saved.CreatedAt {
		t.Fatalf("update mismatch: %+v vs %+v", got, saved)
	}

	if removed, err := s.Delete(saved.ID); err != nil || !removed {
		t.Fatalf("Delete = %v err=%v", removed, err)
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills.json")
	s1 := New(path, filepath.Join(dir, "_cache"), filepath.Join(dir, "_archives"))
	if _, err := s1.Upsert(Record{
		Name:   "foo",
		Source: Source{Type: SourceGit, URL: "https://example.com/repo.git", Ref: "main"},
		Apps:   Apps{Codex: true},
		Scopes: DefaultScopes(),
	}); err != nil {
		t.Fatal(err)
	}
	s2 := New(path, "", "")
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	got := s2.List()
	if len(got) != 1 || got[0].Source.URL != "https://example.com/repo.git" {
		t.Fatalf("reload mismatch: %+v", got)
	}
}

func TestRecordValidate(t *testing.T) {
	cases := []struct {
		name string
		r    Record
		ok   bool
	}{
		{"missing id+name", Record{}, false},
		{"missing source path", Record{ID: "a", Name: "a", Source: Source{Type: SourceLocal}}, false},
		{"missing git url", Record{ID: "a", Name: "a", Source: Source{Type: SourceGit}}, false},
		{"missing archive key", Record{ID: "a", Name: "a", Source: Source{Type: SourceArchive}}, false},
		{"unknown type", Record{ID: "a", Name: "a", Source: Source{Type: SourceType("wat")}}, false},
		{"local ok", Record{ID: "a", Name: "a", Source: Source{Type: SourceLocal, Path: "/x"}}, true},
		{"git ok", Record{ID: "a", Name: "a", Source: Source{Type: SourceGit, URL: "https://x"}}, true},
		{"archive ok", Record{ID: "a", Name: "a", Source: Source{Type: SourceArchive, UploadKey: "x.zip"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want error")
			}
			if !tc.ok && !IsValidationError(err) {
				t.Fatalf("want validation error, got %T", err)
			}
		})
	}
}

func TestStoreLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "missing.json"), "", "")
	if err := s.Load(); err != nil {
		t.Fatalf("Load missing = %v, want nil", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestStoreLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills.json")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(path, "", "")
	if err := s.Load(); err != nil {
		t.Fatalf("Load empty = %v", err)
	}
}

func TestAppsHelpersIncludePi(t *testing.T) {
	a := Apps{Claude: true, Qwen: true}
	if !a.IsEnabledFor("claude") || a.IsEnabledFor("codex") || !a.IsEnabledFor("qwen") || a.IsEnabledFor("pi") {
		t.Fatalf("IsEnabledFor mismatch: %+v", a)
	}
	a.SetEnabledFor("Pi", true)
	if !a.Pi {
		t.Fatalf("SetEnabledFor(pi) failed")
	}
}
