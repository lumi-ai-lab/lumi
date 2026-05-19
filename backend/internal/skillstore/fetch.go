package skillstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// GitRunner executes a git command and returns combined stdout/stderr.
// Tests inject a fake; production uses the host `git` binary.
type GitRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// DefaultGitRunner shells out to the system git binary.
func DefaultGitRunner(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// Materializer resolves a Record.Source to an absolute directory containing
// SKILL.md. local sources reuse the user's path; git sources are cloned
// into the cache; archive sources are extracted from <archDir>/<uploadKey>.
type Materializer struct {
	cacheDir string
	archDir  string
	git      GitRunner
	timeout  time.Duration
}

// NewMaterializer constructs a Materializer using the store's cache layout.
func NewMaterializer(s *Store) *Materializer {
	return &Materializer{
		cacheDir: s.cacheDir,
		archDir:  s.archDir,
		git:      DefaultGitRunner,
		timeout:  120 * time.Second,
	}
}

// WithGit overrides the git runner (for tests).
func (m *Materializer) WithGit(g GitRunner) *Materializer { m.git = g; return m }

// WithTimeout overrides the per-clone timeout.
func (m *Materializer) WithTimeout(d time.Duration) *Materializer { m.timeout = d; return m }

// Materialize returns the absolute directory containing SKILL.md for the
// given record. It is idempotent: re-calling for the same record reuses the
// already-materialized directory.
func (m *Materializer) Materialize(ctx context.Context, r Record) (string, error) {
	switch r.Source.Type {
	case SourceLocal:
		return m.local(r)
	case SourceArchive:
		return m.archive(r)
	case SourceGit:
		return m.git_(ctx, r)
	default:
		return "", fmt.Errorf("unsupported source type %q", r.Source.Type)
	}
}

func (m *Materializer) local(r Record) (string, error) {
	abs, err := filepath.Abs(r.Source.Path)
	if err != nil {
		return "", err
	}
	if r.Source.Subdir != "" {
		abs = filepath.Join(abs, filepath.FromSlash(r.Source.Subdir))
	}
	if !hasSkillManifest(abs) {
		return "", fmt.Errorf("local source %s missing SKILL.md", abs)
	}
	return abs, nil
}

func (m *Materializer) archive(r Record) (string, error) {
	if strings.TrimSpace(r.Source.UploadKey) == "" {
		return "", errors.New("archive uploadKey is empty")
	}
	if m.archDir == "" {
		return "", errors.New("archive dir not configured")
	}
	archive := filepath.Join(m.archDir, filepath.FromSlash(r.Source.UploadKey))
	if _, err := os.Stat(archive); err != nil {
		return "", fmt.Errorf("archive missing: %w", err)
	}
	dest := filepath.Join(m.cacheDir, r.ID)
	if !hasSkillManifest(dest) && !hasNestedSkillManifest(dest) {
		if err := os.RemoveAll(dest); err != nil {
			return "", err
		}
		if err := extractZip(archive, dest); err != nil {
			return "", err
		}
	}
	return findSkillRoot(dest, r.Source.Subdir)
}

func (m *Materializer) git_(ctx context.Context, r Record) (string, error) {
	if m.cacheDir == "" {
		return "", errors.New("cache dir not configured")
	}
	dest := filepath.Join(m.cacheDir, r.ID)
	cctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err := m.ensureGitClone(cctx, r, dest); err != nil {
		return "", err
	}
	return findSkillRoot(dest, r.Source.Subdir)
}

func (m *Materializer) ensureGitClone(ctx context.Context, r Record, dest string) error {
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		// Already cloned; pull latest if a ref is pinned to a branch.
		if r.Source.Ref != "" {
			if _, err := m.git(ctx, dest, "fetch", "origin", r.Source.Ref); err != nil {
				return fmt.Errorf("git fetch: %w", err)
			}
			if _, err := m.git(ctx, dest, "checkout", r.Source.Ref); err != nil {
				return fmt.Errorf("git checkout: %w", err)
			}
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	args := []string{"clone", "--depth", "1"}
	if r.Source.Ref != "" {
		args = append(args, "--branch", r.Source.Ref)
	}
	args = append(args, r.Source.URL, dest)
	if out, err := m.git(ctx, "", args...); err != nil {
		return fmt.Errorf("git clone failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func hasNestedSkillManifest(root string) bool {
	if hasSkillManifest(root) {
		return true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && hasSkillManifest(filepath.Join(root, entry.Name())) {
			return true
		}
	}
	return false
}
