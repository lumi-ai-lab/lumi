package skillsync

import (
	"path/filepath"
	"testing"
)

func TestLockfileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	lf, err := LoadLockfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(lf.Entries) != 0 {
		t.Fatalf("fresh dir should be empty, got %+v", lf.Entries)
	}
	lf.Upsert(LockEntry{ID: "a-1", Name: "a", Kind: LockKindSymlink, Target: "a"})
	lf.Upsert(LockEntry{ID: "b-1", Name: "b", Kind: LockKindCopy, Target: "b"})
	if err := SaveLockfile(dir, lf); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadLockfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("loaded entries = %+v", loaded.Entries)
	}
	if _, found := loaded.FindByID("a-1"); found == nil {
		t.Fatal("a-1 not found")
	}
	if !loaded.RemoveByID("a-1") {
		t.Fatal("RemoveByID a-1 returned false")
	}
	if _, found := loaded.FindByID("a-1"); found != nil {
		t.Fatal("a-1 still present after remove")
	}
}

func TestLockfileNamesIgnoresUnknown(t *testing.T) {
	lf := &Lockfile{Entries: []LockEntry{{ID: "a", Name: "a", Target: "a"}}}
	names := lf.Names()
	if _, ok := names["a"]; !ok {
		t.Fatal("expected a in Names map")
	}
	if _, ok := names["foreign"]; ok {
		t.Fatal("foreign should not appear in Names")
	}
}

func TestLoadLockfileMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	lf, err := LoadLockfile(dir)
	if err != nil {
		t.Fatalf("LoadLockfile missing = %v", err)
	}
	if lf.Version != 1 {
		t.Fatalf("version = %d", lf.Version)
	}
}
