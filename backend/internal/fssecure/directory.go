package fssecure

import (
	"errors"
	"fmt"
	"os"
)

// EnsureDirectory creates one explicitly managed directory, or validates an
// existing directory without changing it. Its parent is never created or
// modified.
func EnsureDirectory(path string, mode os.FileMode, gid *uint32) error {
	info, err := os.Lstat(path)
	if err == nil {
		return validateDirectory(path, info, mode, gid)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect managed directory %q: %w", path, err)
	}

	if err := os.Mkdir(path, mode); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(path)
			if statErr != nil {
				return fmt.Errorf("inspect concurrently created directory %q: %w", path, statErr)
			}
			return validateDirectory(path, info, mode, gid)
		}
		return fmt.Errorf("create managed directory %q: %w", path, err)
	}
	if err := SetGroup(path, gid); err != nil {
		return fmt.Errorf("set managed directory group %q: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set managed directory mode %q: %w", path, err)
	}
	info, err = os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect created managed directory %q: %w", path, err)
	}
	return validateDirectory(path, info, mode, gid)
}

// ValidateRegularFile verifies that path is a publisher-owned, real regular
// file with the exact configured group and permissions.
func ValidateRegularFile(path string, mode os.FileMode, gid *uint32) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("managed file %q must be a real regular file", path)
	}
	if got := info.Mode().Perm(); got != mode.Perm() {
		return fmt.Errorf("managed file %q mode is %04o, want %04o", path, got, mode.Perm())
	}
	return ValidatePublisherOwnership(path, info, gid)
}

func validateDirectory(path string, info os.FileInfo, mode os.FileMode, gid *uint32) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed directory %q must be a real directory", path)
	}
	if got := info.Mode().Perm(); got != mode.Perm() {
		return fmt.Errorf("managed directory %q mode is %04o, want %04o", path, got, mode.Perm())
	}
	return ValidatePublisherOwnership(path, info, gid)
}
