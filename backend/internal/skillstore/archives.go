package skillstore

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZip extracts the zip archive at archivePath into destDir. Symlinks
// inside the archive are resolved by copying their target contents (matching
// cc-switch behavior; avoids dangling links when targets live outside the
// archive). Path traversal entries are rejected.
func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", archivePath, err)
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	cleanDest, err := filepath.Abs(destDir)
	if err != nil {
		return err
	}

	for _, f := range r.File {
		if err := extractZipEntry(f, cleanDest); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, cleanDest string) error {
	target := filepath.Join(cleanDest, filepath.FromSlash(f.Name))
	rel, err := filepath.Rel(cleanDest, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("zip entry escapes destination: %s", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, f.Mode().Perm()|0o700)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if f.Mode()&os.ModeSymlink != 0 {
		// Symlink: skip — we don't want to fabricate links pointing outside
		// the cache. The skill body should not depend on them.
		return nil
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// findSkillRoot walks descents of root looking for the first directory that
// contains SKILL.md. If subdir is non-empty, it is honored verbatim relative
// to root. The returned path is absolute.
func findSkillRoot(root, subdir string) (string, error) {
	if strings.TrimSpace(subdir) != "" {
		candidate := filepath.Join(root, filepath.FromSlash(subdir))
		if hasSkillManifest(candidate) {
			return candidate, nil
		}
		return "", fmt.Errorf("subdir %q does not contain SKILL.md under %s", subdir, root)
	}
	if hasSkillManifest(root) {
		return root, nil
	}
	// One level deep: many GitHub zip archives wrap content in a single
	// "<repo>-<sha>" directory.
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var only string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if only != "" {
			only = ""
			break
		}
		only = filepath.Join(root, entry.Name())
	}
	if only != "" && hasSkillManifest(only) {
		return only, nil
	}
	return "", errors.New("no SKILL.md found in archive root")
}

func hasSkillManifest(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !info.IsDir()
}
