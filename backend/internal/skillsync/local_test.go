package skillsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/skillstore"
)

type fakeResolver struct{ paths map[string]string }

func (f *fakeResolver) Materialize(_ context.Context, r skillstore.Record) (string, error) {
	return f.paths[r.ID], nil
}

func writeSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# X"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApplyToDirSymlink(t *testing.T) {
	cache := t.TempDir()
	src := filepath.Join(cache, "skill-1")
	writeSkill(t, src)

	target := filepath.Join(t.TempDir(), "skills")
	res, err := ApplyToDir(ApplyOptions{
		AppDir: target,
		AppKey: "claude",
		Records: []skillstore.Record{{
			ID: "skill-1", Name: "code-reviewer",
			Apps:   skillstore.Apps{Claude: true},
			Scopes: skillstore.DefaultScopes(),
		}},
		Resolver: &fakeResolver{paths: map[string]string{"skill-1": src}},
		Mode:     ModeAuto,
		Scope:    "local",
	})
	if err != nil {
		t.Fatalf("ApplyToDir: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0] != "code-reviewer" {
		t.Fatalf("Added = %+v", res.Added)
	}

	link := filepath.Join(target, "code-reviewer")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("link not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink, got %v", info.Mode())
	}

	lf, err := LoadLockfile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(lf.Entries) != 1 || lf.Entries[0].Kind != LockKindSymlink {
		t.Fatalf("lockfile = %+v", lf.Entries)
	}
}

func TestBuildRemoteSkillsIncludesPiAppFlag(t *testing.T) {
	src := filepath.Join(t.TempDir(), "skill-1")
	writeSkill(t, src)
	store := skillstore.New(filepath.Join(t.TempDir(), "skills.json"), "", "")
	rec, err := store.Upsert(skillstore.Record{
		Name:   "pi-helper",
		Source: skillstore.Source{Type: skillstore.SourceLocal, Path: src},
		Apps:   skillstore.Apps{Pi: true},
		Scopes: skillstore.DefaultScopes(),
	})
	if err != nil {
		t.Fatal(err)
	}

	blobs, errs := BuildRemoteSkills(context.Background(), store, &fakeResolver{paths: map[string]string{rec.ID: src}})
	if len(errs) != 0 {
		t.Fatalf("BuildRemoteSkills errors = %v", errs)
	}
	if len(blobs) != 1 || !blobs[0].Apps["pi"] {
		t.Fatalf("remote blobs = %+v, want pi app flag", blobs)
	}
}

func TestApplyToDirRemovesDisabled(t *testing.T) {
	src := filepath.Join(t.TempDir(), "skill-1")
	writeSkill(t, src)
	target := filepath.Join(t.TempDir(), "skills")

	// First apply: claude enabled.
	rec := skillstore.Record{
		ID: "skill-1", Name: "rev",
		Apps:   skillstore.Apps{Claude: true},
		Scopes: skillstore.DefaultScopes(),
	}
	if _, err := ApplyToDir(ApplyOptions{
		AppDir: target, AppKey: "claude", Scope: "local",
		Records:  []skillstore.Record{rec},
		Resolver: &fakeResolver{paths: map[string]string{"skill-1": src}},
	}); err != nil {
		t.Fatal(err)
	}

	// Second apply with claude disabled — should be removed.
	rec.Apps.Claude = false
	res, err := ApplyToDir(ApplyOptions{
		AppDir: target, AppKey: "claude", Scope: "local",
		Records:  []skillstore.Record{rec},
		Resolver: &fakeResolver{paths: map[string]string{"skill-1": src}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "rev" {
		t.Fatalf("Removed = %+v", res.Removed)
	}
	if _, err := os.Stat(filepath.Join(target, "rev")); !os.IsNotExist(err) {
		t.Fatalf("target should be gone: %v", err)
	}
}

func TestApplyToDirSkipsForeignContent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(filepath.Join(target, "user-owned"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "skill-1")
	writeSkill(t, src)

	res, err := ApplyToDir(ApplyOptions{
		AppDir: target, AppKey: "claude", Scope: "local",
		Records: []skillstore.Record{{
			ID: "skill-1", Name: "user-owned", // collision with foreign
			Apps:   skillstore.Apps{Claude: true},
			Scopes: skillstore.DefaultScopes(),
		}},
		Resolver: &fakeResolver{paths: map[string]string{"skill-1": src}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", res.Conflicts)
	}
	if _, err := os.Stat(filepath.Join(target, "user-owned")); err != nil {
		t.Fatalf("foreign content was disturbed: %v", err)
	}
}

func TestApplyToDirAppFiltering(t *testing.T) {
	src := filepath.Join(t.TempDir(), "skill-1")
	writeSkill(t, src)
	target := filepath.Join(t.TempDir(), "skills")

	// Record enabled only for codex; claude apply must skip it.
	res, err := ApplyToDir(ApplyOptions{
		AppDir: target, AppKey: "claude", Scope: "local",
		Records: []skillstore.Record{{
			ID: "skill-1", Name: "rev",
			Apps:   skillstore.Apps{Codex: true},
			Scopes: skillstore.DefaultScopes(),
		}},
		Resolver: &fakeResolver{paths: map[string]string{"skill-1": src}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 {
		t.Fatalf("claude pass should skip codex-only skill, got %+v", res.Added)
	}
}

func TestPlaceDirCopyMode(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	writeSkill(t, src)
	dst := filepath.Join(t.TempDir(), "dst")
	kind, err := PlaceDir(src, dst, ModeCopy)
	if err != nil {
		t.Fatal(err)
	}
	if kind != LockKindCopy {
		t.Fatalf("kind = %q", kind)
	}
	if info, _ := os.Lstat(dst); info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected real directory, got symlink")
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing in copy: %v", err)
	}
}

func TestPlaceDirRejectsMissingManifest(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst")
	if _, err := PlaceDir(src, dst, ModeCopy); err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
}
