package skillsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LockfileName is written into each managed app skill directory.
const LockfileName = ".lumi-managed.json"

// LockKindSymlink and LockKindCopy describe how the entry was synced; used
// when removing entries to pick the right cleanup path.
const (
	LockKindSymlink = "symlink"
	LockKindCopy    = "copy"
)

// LockEntry tracks a single Lumi-managed item inside a skill directory.
type LockEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// Lockfile is the on-disk format for .lumi-managed.json.
type Lockfile struct {
	Version int         `json:"version"`
	Entries []LockEntry `json:"entries"`
}

// LoadLockfile reads the lockfile in dir. Missing file is treated as empty.
func LoadLockfile(dir string) (*Lockfile, error) {
	path := filepath.Join(dir, LockfileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Lockfile{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return &Lockfile{Version: 1}, nil
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if lf.Version == 0 {
		lf.Version = 1
	}
	return &lf, nil
}

// SaveLockfile atomically writes the lockfile under dir, creating dir if
// necessary. An empty entries slice writes [] not null for stable output.
func SaveLockfile(dir string, lf *Lockfile) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if lf.Entries == nil {
		lf.Entries = []LockEntry{}
	}
	if lf.Version == 0 {
		lf.Version = 1
	}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, LockfileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FindByID returns the entry matching id (and its index), or -1 if absent.
func (lf *Lockfile) FindByID(id string) (int, *LockEntry) {
	for i := range lf.Entries {
		if lf.Entries[i].ID == id {
			return i, &lf.Entries[i]
		}
	}
	return -1, nil
}

// Upsert inserts or replaces an entry by id, preserving order.
func (lf *Lockfile) Upsert(entry LockEntry) {
	if i, _ := lf.FindByID(entry.ID); i >= 0 {
		lf.Entries[i] = entry
		return
	}
	lf.Entries = append(lf.Entries, entry)
}

// RemoveByID drops the entry matching id; returns true if removed.
func (lf *Lockfile) RemoveByID(id string) bool {
	if i, _ := lf.FindByID(id); i >= 0 {
		lf.Entries = append(lf.Entries[:i], lf.Entries[i+1:]...)
		return true
	}
	return false
}

// Names returns the set of target names tracked by the lockfile (used for
// safe collision detection — a name that exists in the directory but not
// in this set is foreign content that Lumi must never touch).
func (lf *Lockfile) Names() map[string]struct{} {
	out := make(map[string]struct{}, len(lf.Entries))
	for _, entry := range lf.Entries {
		out[entry.Target] = struct{}{}
	}
	return out
}
