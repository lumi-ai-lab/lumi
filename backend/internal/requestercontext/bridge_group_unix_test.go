//go:build !windows

package requestercontext

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestFileBridgeWithReaderGIDUsesSharedContractPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "requester-context")
	gid := uint32(os.Getgid())
	if gid == 0 {
		gid = 62002
	}
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

func TestUnsafeSecureRootsAreRejectedWithoutMetadataChanges(t *testing.T) {
	for _, path := range []string{"/", "/run", "/var", "/opt", "/tmp"} {
		t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
			before, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				t.Skipf("%s does not exist on this host", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvRequesterContextRoot, path)
			t.Setenv(EnvRequesterContextReaderGID, "2003")
			if _, err := RuntimeSettingsFromEnv("unused"); err == nil {
				t.Fatalf("RuntimeSettingsFromEnv(%q) error = nil", path)
			}
			after, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeStat := before.Sys().(*syscall.Stat_t)
			afterStat := after.Sys().(*syscall.Stat_t)
			if before.Mode() != after.Mode() || beforeStat.Uid != afterStat.Uid || beforeStat.Gid != afterStat.Gid {
				t.Fatalf("unsafe root %s metadata changed after rejection", path)
			}
		})
	}
}

func TestSecuredFileBridgeRejectsGroupMismatchWithoutRepair(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing a directory to a deliberately wrong numeric group requires root")
	}
	root := filepath.Join(t.TempDir(), "requester-context")
	if err := os.Mkdir(root, 0o710); err != nil {
		t.Fatal(err)
	}
	wrongGID := uint32(62001)
	wantGID := uint32(62002)
	if err := os.Chown(root, -1, int(wrongGID)); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewFileBridge(root, "workspace", "pi", WithReaderGID(wantGID))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bridge.Write("session", testContext()); err == nil || !strings.Contains(err.Error(), "group GID") {
		t.Fatalf("Write() error = %v, want group mismatch", err)
	}
	after, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() || before.Sys().(*syscall.Stat_t).Gid != after.Sys().(*syscall.Stat_t).Gid {
		t.Fatal("existing root metadata changed after group mismatch")
	}
}

func TestSecuredFileBridgeRejectsOwnerMismatchWithoutRepair(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing a directory to a deliberately wrong numeric owner requires root")
	}
	root := filepath.Join(t.TempDir(), "requester-context")
	gid := uint32(62002)
	if err := os.Mkdir(root, 0o710); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(root, 62005, int(gid)); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewFileBridge(root, "workspace", "pi", WithReaderGID(gid))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bridge.Write("session", testContext()); err == nil || !strings.Contains(err.Error(), "owner UID") {
		t.Fatalf("Write() error = %v, want owner mismatch", err)
	}
	after, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	beforeStat := before.Sys().(*syscall.Stat_t)
	afterStat := after.Sys().(*syscall.Stat_t)
	if before.Mode() != after.Mode() || beforeStat.Uid != afterStat.Uid || beforeStat.Gid != afterStat.Gid {
		t.Fatal("existing root metadata changed after owner mismatch")
	}
}

func TestSecuredFileBridgeRejectsModeMismatchWithoutRepair(t *testing.T) {
	root := filepath.Join(t.TempDir(), "requester-context")
	gid := uint32(os.Getgid())
	if gid == 0 {
		gid = 62002
	}
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(root, -1, int(gid)); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewFileBridge(root, "workspace", "pi", WithReaderGID(gid))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bridge.Write("session", testContext()); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("Write() error = %v, want mode mismatch", err)
	}
	after, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() {
		t.Fatalf("secure root mode changed after rejection: %v -> %v", before.Mode(), after.Mode())
	}
}

func TestSecuredFileBridgeRealReaderBoundary(t *testing.T) {
	if os.Getenv("LUMI_REQUESTER_CONTEXT_HELPER") == "1" {
		runRequesterContextBoundaryHelper()
		return
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("real cross-UID/GID boundary test requires Linux root")
	}

	parent, err := os.MkdirTemp("", "lumi-requester-boundary-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o711); err != nil {
		t.Fatal(err)
	}
	helper := stageRequesterContextBoundaryHelper(t, parent)
	readerUID, readerGID := uint32(62101), uint32(62102)
	root := filepath.Join(parent, "requester-context")
	bridge, err := NewFileBridge(root, "workspace", "pi", WithReaderGID(readerGID))
	if err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := bridge.Write("raw/session/one", testContext())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	runBoundaryHelper(t, helper, readerUID, readerGID, "reader", path, bridge.Dir())
	runBoundaryHelper(t, helper, 62103, 62104, "outsider", path, bridge.Dir())
}

func stageRequesterContextBoundaryHelper(t *testing.T, parent string) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(parent, "requester-context-boundary-helper")
	destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		destination.Close()
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	return target
}

func runBoundaryHelper(t *testing.T, helper string, uid, gid uint32, role, path, dir string) {
	t.Helper()
	cmd := exec.Command(helper, "-test.run=^TestSecuredFileBridgeRealReaderBoundary$")
	cmd.Env = append(os.Environ(),
		"LUMI_REQUESTER_CONTEXT_HELPER=1",
		"LUMI_BOUNDARY_ROLE="+role,
		"LUMI_BOUNDARY_PATH="+path,
		"LUMI_BOUNDARY_DIR="+dir,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{gid}}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s helper error = %v, output = %s", role, err, output)
	}
}

func runRequesterContextBoundaryHelper() {
	path := os.Getenv("LUMI_BOUNDARY_PATH")
	dir := os.Getenv("LUMI_BOUNDARY_DIR")
	role := os.Getenv("LUMI_BOUNDARY_ROLE")
	_, readErr := os.ReadFile(path)
	_, listErr := os.ReadDir(dir)
	writeErr := os.WriteFile(path, []byte("tamper"), 0o640)
	removeErr := os.Remove(path)
	renameErr := os.Rename(path, path+".replaced")
	if role == "reader" {
		if readErr != nil || listErr == nil || writeErr == nil || removeErr == nil || renameErr == nil {
			fmt.Fprintf(os.Stderr, "reader results: read=%v list=%v write=%v remove=%v rename=%v\n", readErr, listErr, writeErr, removeErr, renameErr)
			os.Exit(2)
		}
		return
	}
	if readErr == nil || !errors.Is(readErr, os.ErrPermission) {
		fmt.Fprintf(os.Stderr, "outsider read result: %v\n", readErr)
		os.Exit(3)
	}
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
