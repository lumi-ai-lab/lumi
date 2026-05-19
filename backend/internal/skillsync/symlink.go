package skillsync

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Mode chooses how skill content is propagated to the destination directory.
type Mode string

const (
	// ModeAuto tries symlink first, falling back to deep copy on error.
	ModeAuto Mode = "auto"
	// ModeSymlink fails fast when the OS rejects symlinks.
	ModeSymlink Mode = "symlink"
	// ModeCopy always deep-copies. Used in sandbox containers where bind-
	// mounted symlinks cannot reach outside the mount.
	ModeCopy Mode = "copy"
)

// PlaceDir mirrors src to dst using the chosen mode. On success, the kind of
// the resulting placement (LockKindSymlink or LockKindCopy) is returned.
func PlaceDir(src, dst string, mode Mode) (string, error) {
	if err := validateSource(src); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := removePath(dst); err != nil {
		return "", err
	}
	switch mode {
	case ModeSymlink:
		if err := os.Symlink(src, dst); err != nil {
			return "", fmt.Errorf("symlink %s -> %s: %w", src, dst, err)
		}
		return LockKindSymlink, nil
	case ModeCopy:
		if err := copyDir(src, dst); err != nil {
			return "", err
		}
		return LockKindCopy, nil
	case "", ModeAuto:
		if err := os.Symlink(src, dst); err == nil {
			return LockKindSymlink, nil
		}
		if err := copyDir(src, dst); err != nil {
			return "", err
		}
		return LockKindCopy, nil
	default:
		return "", fmt.Errorf("unknown skillsync mode %q", mode)
	}
}

// RemovePlaced deletes a previously-placed target, accepting either a
// symlink or a real directory. Missing targets are ignored.
func RemovePlaced(dst string) error { return removePath(dst) }

func validateSource(src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source unavailable %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return fmt.Errorf("source missing SKILL.md: %s", src)
	}
	return nil
}

func removePath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())
		switch {
		case entry.IsDir():
			if err := copyDir(s, d); err != nil {
				return err
			}
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(s)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, d); err != nil {
				return err
			}
		default:
			if err := copyFile(s, d); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
