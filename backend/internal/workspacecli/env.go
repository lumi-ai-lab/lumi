package workspacecli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const MetricCLIEnv = "QDM_METRIC_CLI"

func MetricCLIPath(workspacePath string) (string, bool) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", false
	}

	name := "qdm-metric-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(workspacePath, "bin", name)
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	return candidate, true
}

func PrependPath(current, dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return current
	}
	for _, part := range filepath.SplitList(current) {
		if part == dir {
			return current
		}
	}
	if current == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + current
}

func RemovePath(current, dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return current
	}
	parts := filepath.SplitList(current)
	filtered := parts[:0]
	for _, part := range parts {
		if part != dir {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, string(os.PathListSeparator))
}
