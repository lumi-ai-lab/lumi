package imfile

import (
	"os"
	"path/filepath"
	"strings"
)

const sandboxWorkspacePath = "/workspace"

type ResolvedFile struct {
	Path string
	Info os.FileInfo
}

func ResolveWorkspaceFile(rawPath, workspaceRoot string) (ResolvedFile, string) {
	resolvedPath := strings.TrimSpace(rawPath)
	if resolvedPath == "" {
		return ResolvedFile{}, "path is required"
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return ResolvedFile{}, "workspace root is empty"
	}

	if rel, ok := sandboxRelativePath(resolvedPath); ok {
		resolvedPath = filepath.Join(workspaceRoot, rel)
	} else if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(workspaceRoot, resolvedPath)
	}

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return ResolvedFile{}, "file does not exist"
	}

	canonicalRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return ResolvedFile{}, "workspace root is invalid"
	}
	canonicalPath, err := filepath.EvalSymlinks(resolvedPath)
	if err != nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ResolvedFile{}, "symlink target is invalid"
		}
		return ResolvedFile{}, "path is invalid"
	}

	inside, err := isInsideWorkspace(canonicalRoot, canonicalPath)
	if err != nil || !inside {
		return ResolvedFile{}, "path escapes workspace"
	}

	statTarget := info
	if info.Mode()&os.ModeSymlink != 0 {
		if statTarget, err = os.Stat(canonicalPath); err != nil {
			return ResolvedFile{}, "symlink target is invalid"
		}
	}
	if !statTarget.Mode().IsRegular() {
		return ResolvedFile{}, "path is not a regular file"
	}

	return ResolvedFile{
		Path: canonicalPath,
		Info: statTarget,
	}, ""
}

func sandboxRelativePath(path string) (string, bool) {
	if path == sandboxWorkspacePath {
		return ".", true
	}
	prefix := sandboxWorkspacePath + "/"
	if strings.HasPrefix(path, prefix) {
		return strings.TrimPrefix(path, prefix), true
	}
	return "", false
}

func isInsideWorkspace(root, target string) (bool, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return false, nil
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..", nil
}
