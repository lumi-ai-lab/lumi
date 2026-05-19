package skillstore

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeLocal(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# X"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(t.TempDir(), "skills.json"), "", "")
	m := NewMaterializer(s)
	got, err := m.Materialize(context.Background(), Record{
		ID: "x", Name: "x",
		Source: Source{Type: SourceLocal, Path: src},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got != src {
		t.Fatalf("got %q, want %q", got, src)
	}
}

func TestMaterializeLocalWithSubdir(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "skills", "foo")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "SKILL.md"), []byte("# X"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(t.TempDir(), "skills.json"), "", "")
	m := NewMaterializer(s)
	got, err := m.Materialize(context.Background(), Record{
		ID: "x", Name: "x",
		Source: Source{Type: SourceLocal, Path: root, Subdir: "skills/foo"},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got != nested {
		t.Fatalf("got %q, want %q", got, nested)
	}
}

func TestMaterializeLocalMissingManifest(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "skills.json"), "", "")
	m := NewMaterializer(s)
	_, err := m.Materialize(context.Background(), Record{
		ID: "x", Name: "x",
		Source: Source{Type: SourceLocal, Path: t.TempDir()},
	})
	if err == nil {
		t.Fatal("want error for missing SKILL.md")
	}
}

func TestMaterializeArchive(t *testing.T) {
	dir := t.TempDir()
	archDir := filepath.Join(dir, "_archives")
	cacheDir := filepath.Join(dir, "_cache")
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(archDir, "foo.zip")
	writeMiniZip(t, zipPath, map[string]string{
		"SKILL.md":     "# Foo",
		"reference.md": "details",
	})
	s := New(filepath.Join(dir, "skills.json"), cacheDir, archDir)
	m := NewMaterializer(s)
	got, err := m.Materialize(context.Background(), Record{
		ID: "foo-1", Name: "foo",
		Source: Source{Type: SourceArchive, UploadKey: "foo.zip"},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	expect := filepath.Join(cacheDir, "foo-1")
	if got != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
	if _, err := os.Stat(filepath.Join(got, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
}

func TestMaterializeGitWithFakeRunner(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "_cache")
	s := New(filepath.Join(dir, "skills.json"), cacheDir, filepath.Join(dir, "_archives"))
	m := NewMaterializer(s).WithGit(func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		// args = ["clone","--depth","1","--branch","main","https://...","<dest>"]
		dest := args[len(args)-1]
		if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("# Foo"), 0o644)
	})
	got, err := m.Materialize(context.Background(), Record{
		ID: "foo-1", Name: "foo",
		Source: Source{Type: SourceGit, URL: "https://example.com/x.git", Ref: "main"},
	})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	expect := filepath.Join(cacheDir, "foo-1")
	if got != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

// writeMiniZip creates a small zip on disk for tests.
func writeMiniZip(t *testing.T, dest string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZipPathTraversal(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../escape.txt")
	_, _ = f.Write([]byte("nope"))
	_ = w.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(zipPath, filepath.Join(dir, "out")); err == nil {
		t.Fatalf("want error for path traversal")
	}
}
