package skillsync

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/skillstore"
)

// RemoteFile is a single skill blob entry suitable for transport over a
// JSON / WebSocket envelope. Path is forward-slash-relative to the skill
// root; Content is base64-encoded raw bytes.
type RemoteFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode,omitempty"`
}

// RemoteSkill is the on-the-wire form of a SSOT skill record plus its files.
type RemoteSkill struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Apps  map[string]bool `json:"apps"`
	Files []RemoteFile    `json:"files"`
}

// MaxRemoteSkillBytes caps the total uncompressed file payload per skill.
// Anything bigger is rejected to keep individual SSOT pushes within the
// existing 8 MiB device websocket frame budget.
const MaxRemoteSkillBytes = 4 << 20

// BuildRemoteSkills materializes every SSOT record and packs its directory
// contents into RemoteSkill entries. Records that fail to materialize or
// exceed the size cap are skipped and reported in the returned error slice.
func BuildRemoteSkills(ctx context.Context, store *skillstore.Store, resolver Resolver) ([]RemoteSkill, []error) {
	if store == nil {
		return nil, []error{fmt.Errorf("skillsync.BuildRemoteSkills: store is nil")}
	}
	if resolver == nil {
		resolver = skillstore.NewMaterializer(store)
	}
	var out []RemoteSkill
	var errs []error
	for _, rec := range store.List() {
		select {
		case <-ctx.Done():
			return out, append(errs, ctx.Err())
		default:
		}
		src, err := resolver.Materialize(ctx, rec)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rec.Name, err))
			continue
		}
		files, err := readSkillFiles(src)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rec.Name, err))
			continue
		}
		out = append(out, RemoteSkill{
			ID:    rec.ID,
			Name:  rec.Name,
			Apps:  appsToMap(rec.Apps),
			Files: files,
		})
	}
	return out, errs
}

func appsToMap(a skillstore.Apps) map[string]bool {
	return map[string]bool{
		"claude": a.Claude,
		"codex":  a.Codex,
		"qwen":   a.Qwen,
		"pi":     a.Pi,
	}
}

func readSkillFiles(root string) ([]RemoteFile, error) {
	var files []RemoteFile
	var totalBytes int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Skip Lumi metadata files inside source dirs.
		if d.Name() == LockfileName {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Resolve symlinks pointing inside the skill root; reject
			// everything else to avoid dragging in user files.
			real, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			if !strings.HasPrefix(real, filepath.Clean(root)+string(filepath.Separator)) {
				return nil
			}
			info, err = os.Stat(real)
			if err != nil || info.IsDir() {
				return nil
			}
			path = real
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		totalBytes += int64(len(data))
		if totalBytes > MaxRemoteSkillBytes {
			return errors.New("skill exceeds max remote payload size")
		}
		files = append(files, RemoteFile{
			Path:    filepath.ToSlash(rel),
			Content: base64.StdEncoding.EncodeToString(data),
			Mode:    uint32(info.Mode().Perm()),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// WriteRemoteSkill applies the wire payload to <appDir>/<skill.Name>, using
// the provided mode (typically ModeCopy on the executor side). The lockfile
// at appDir is updated to reflect the placement.
func WriteRemoteSkill(appDir string, skill RemoteSkill) error {
	if appDir == "" {
		return errors.New("appDir is empty")
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	lf, err := LoadLockfile(appDir)
	if err != nil {
		return err
	}
	target := filepath.Join(appDir, skill.Name)
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	for _, file := range skill.Files {
		dest := filepath.Join(target, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		raw, err := base64.StdEncoding.DecodeString(file.Content)
		if err != nil {
			return err
		}
		mode := os.FileMode(file.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(dest, raw, mode); err != nil {
			return err
		}
	}
	lf.Upsert(LockEntry{ID: skill.ID, Name: skill.Name, Kind: LockKindCopy, Target: skill.Name})
	return SaveLockfile(appDir, lf)
}

// RemoveRemoteSkillsByID prunes entries from the lockfile + filesystem when
// the SSOT push instructs a reset (Reset=true).
func RemoveRemoteSkillsByID(appDir string, ids map[string]struct{}) error {
	lf, err := LoadLockfile(appDir)
	if err != nil {
		return err
	}
	keep := lf.Entries[:0]
	for _, entry := range lf.Entries {
		if _, drop := ids[entry.ID]; drop {
			_ = os.RemoveAll(filepath.Join(appDir, entry.Target))
			continue
		}
		keep = append(keep, entry)
	}
	lf.Entries = keep
	return SaveLockfile(appDir, lf)
}
