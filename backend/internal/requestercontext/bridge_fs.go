package requestercontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (bridge *FileBridge) publishFile(temporaryPath, path string) (os.FileInfo, error) {
	fileBridgePathMu.Lock()
	defer fileBridgePathMu.Unlock()

	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, fmt.Errorf("publish requester context file: %w", err)
	}
	if err := os.Chmod(path, bridge.fileMode); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("set requester context file permissions: %w", err)
	}
	writtenInfo, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("inspect requester context file: %w", err)
	}
	return writtenInfo, nil
}

func (bridge *FileBridge) ensureDir() error {
	workspaceDir := filepath.Dir(bridge.dir)
	contextRoot := filepath.Dir(workspaceDir)
	for _, dir := range []string{contextRoot, workspaceDir, bridge.dir} {
		if err := os.MkdirAll(dir, bridge.dirMode); err != nil {
			return fmt.Errorf("create private requester context directory %q: %w", dir, err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect private requester context directory %q: %w", dir, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("requester context directory %q must be a real directory", dir)
		}
		if err := os.Chmod(dir, bridge.dirMode); err != nil {
			return fmt.Errorf("set private requester context directory mode %q: %w", dir, err)
		}
	}
	return nil
}

func validatePathSegment(label, value string) error {
	if value == "" {
		return fmt.Errorf("requester context %s must not be empty", label)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("requester context %s %q is not a safe path segment", label, value)
	}
	if strings.ContainsAny(value, "/\\\x00") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return fmt.Errorf("requester context %s %q is not a safe path segment", label, value)
	}
	if filepath.Clean(value) != value || filepath.Base(value) != value {
		return fmt.Errorf("requester context %s %q is not a safe path segment", label, value)
	}
	return nil
}
