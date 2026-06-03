package skillsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pengmide/lumi/internal/skillstore"
)

// Resolver materializes a skill record into an absolute source directory.
// Production uses skillstore.Materializer; tests can inject a fake.
type Resolver interface {
	Materialize(ctx context.Context, r skillstore.Record) (string, error)
}

// Plan describes what ApplyToDir will do for a single app directory. Useful
// for tests and for surfacing conflict information through the API.
type Plan struct {
	AppDir   string
	Adds     []skillstore.Record  // records to (re)place
	Removes  []LockEntry          // entries to clean up (lockfile-tracked but no longer enabled)
	Conflict []ConflictDescriptor // names that would clash with foreign content
}

// ConflictDescriptor reports a record that could not be placed because the
// destination name is already used by content Lumi does not own.
type ConflictDescriptor struct {
	RecordID string
	Name     string
	Path     string
}

// ApplyOptions controls a single ApplyToDir invocation.
type ApplyOptions struct {
	AppDir   string              // absolute target like ~/.claude/skills
	AppKey   string              // "claude" | "codex" | "qwen" | "pi"
	Records  []skillstore.Record // full record set; ApplyToDir filters by AppKey + Scopes
	Resolver Resolver            // source materializer
	Mode     Mode                // ModeAuto by default (symlink-first)
	Scope    string              // "local" | "sandbox" | "remote"; controls record.Scopes filtering
	Ctx      context.Context     // optional cancellation context (defaults to Background)
}

// Result captures what actually changed.
type Result struct {
	Added     []string
	Removed   []string
	Conflicts []ConflictDescriptor
	Errors    []string
}

// ApplyToDir reconciles the lockfile + filesystem of a single app skills
// directory with the desired records. Foreign content is left untouched.
func ApplyToDir(opts ApplyOptions) (Result, error) {
	if opts.Ctx == nil {
		opts.Ctx = context.Background()
	}
	if opts.Mode == "" {
		opts.Mode = ModeAuto
	}
	res := Result{}
	if opts.AppDir == "" || opts.AppKey == "" {
		return res, fmt.Errorf("ApplyToDir requires AppDir and AppKey")
	}
	if err := os.MkdirAll(opts.AppDir, 0o755); err != nil {
		return res, fmt.Errorf("mkdir %s: %w", opts.AppDir, err)
	}
	lf, err := LoadLockfile(opts.AppDir)
	if err != nil {
		return res, err
	}

	desired := filterDesired(opts.Records, opts.AppKey, opts.Scope)

	// Drop entries that are no longer desired.
	desiredIDs := map[string]struct{}{}
	for _, r := range desired {
		desiredIDs[r.ID] = struct{}{}
	}
	keptEntries := make([]LockEntry, 0, len(lf.Entries))
	for _, entry := range lf.Entries {
		if _, ok := desiredIDs[entry.ID]; ok {
			keptEntries = append(keptEntries, entry)
			continue
		}
		target := filepath.Join(opts.AppDir, entry.Target)
		if err := RemovePlaced(target); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("remove %s: %v", target, err))
			continue
		}
		res.Removed = append(res.Removed, entry.Target)
	}
	lf.Entries = keptEntries

	// Snapshot foreign-content names for conflict detection (anything in
	// the directory not tracked by the lockfile is foreign).
	foreign, err := foreignNames(opts.AppDir, lf)
	if err != nil {
		return res, err
	}

	// Place desired records.
	for _, rec := range desired {
		select {
		case <-opts.Ctx.Done():
			return res, opts.Ctx.Err()
		default:
		}
		if _, owned := lockfileTargets(lf)[rec.Name]; !owned {
			if _, conflicts := foreign[rec.Name]; conflicts {
				res.Conflicts = append(res.Conflicts, ConflictDescriptor{
					RecordID: rec.ID, Name: rec.Name, Path: filepath.Join(opts.AppDir, rec.Name),
				})
				continue
			}
		}
		src, err := opts.Resolver.Materialize(opts.Ctx, rec)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", rec.Name, err))
			continue
		}
		dst := filepath.Join(opts.AppDir, rec.Name)
		kind, err := PlaceDir(src, dst, opts.Mode)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", rec.Name, err))
			continue
		}
		lf.Upsert(LockEntry{ID: rec.ID, Name: rec.Name, Kind: kind, Target: rec.Name})
		res.Added = append(res.Added, rec.Name)
	}

	if err := SaveLockfile(opts.AppDir, lf); err != nil {
		return res, err
	}
	return res, nil
}

func filterDesired(records []skillstore.Record, appKey, scope string) []skillstore.Record {
	out := make([]skillstore.Record, 0, len(records))
	for _, r := range records {
		if !r.Apps.IsEnabledFor(appKey) {
			continue
		}
		switch scope {
		case "local":
			if !r.Scopes.Local {
				continue
			}
		case "sandbox":
			if !r.Scopes.Sandbox {
				continue
			}
		case "remote":
			if !r.Scopes.Remote {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

func lockfileTargets(lf *Lockfile) map[string]struct{} {
	return lf.Names()
}

func foreignNames(dir string, lf *Lockfile) (map[string]struct{}, error) {
	owned := lf.Names()
	out := map[string]struct{}{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if name == LockfileName {
			continue
		}
		if _, ok := owned[name]; ok {
			continue
		}
		out[name] = struct{}{}
	}
	return out, nil
}
