//go:build !windows

package requestercontext

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFileBridgeWithReaderGIDUsesSharedContractPermissions(t *testing.T) {
	root := t.TempDir()
	gid := uint32(os.Getgid())
	bridge, err := NewFileBridge(root, "workspace-1", "pi", WithReaderGID(gid))
	if err != nil {
		t.Fatalf("NewFileBridge() error = %v", err)
	}
	path, cleanup, err := bridge.Write("session-1", testContext())
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	defer cleanup()

	for _, dir := range []string{root, filepath.Dir(bridge.Dir()), bridge.Dir()} {
		assertModeAndGID(t, dir, 0o710, gid)
	}
	assertModeAndGID(t, path, 0o640, gid)
}

func assertModeAndGID(t *testing.T, path string, wantMode os.FileMode, wantGID uint32) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Errorf("mode(%q) = %o, want %o", path, got, wantMode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("Stat(%q).Sys() = %T, want *syscall.Stat_t", path, info.Sys())
	}
	if got := uint32(stat.Gid); got != wantGID {
		t.Errorf("gid(%q) = %d, want %d", path, got, wantGID)
	}
}
