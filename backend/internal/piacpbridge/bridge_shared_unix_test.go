//go:build !windows

package piacpbridge

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

func TestMaterializeSharedUsesExactReadOnlyContract(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pi-acp-bridge")
	gid := uint32(os.Getgid())
	if gid == 0 {
		gid = 62302
	}
	entrypoint, err := MaterializeShared(root, gid)
	if err != nil {
		t.Fatalf("MaterializeShared() error = %v", err)
	}
	for _, dir := range []string{root, filepath.Dir(entrypoint)} {
		assertSharedModeAndGID(t, dir, 0o750, gid)
	}
	for _, name := range []string{"index.js", "package.json", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		assertSharedModeAndGID(t, filepath.Join(filepath.Dir(entrypoint), name), 0o640, gid)
	}
	if second, err := MaterializeShared(root, gid); err != nil || second != entrypoint {
		t.Fatalf("second MaterializeShared() = %q, %v", second, err)
	}
}

func TestMaterializeSharedRejectsExistingMismatchAndSymlink(t *testing.T) {
	gid := uint32(os.Getgid())
	if gid == 0 {
		gid = 62302
	}
	t.Run("root mode", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "pi-acp-bridge")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		before, _ := os.Lstat(root)
		if _, err := MaterializeShared(root, gid); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Fatalf("MaterializeShared() error = %v, want mode mismatch", err)
		}
		after, _ := os.Lstat(root)
		if before.Mode() != after.Mode() {
			t.Fatal("shared root mode was repaired after rejection")
		}
	})
	t.Run("root symlink", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target")
		if err := os.Mkdir(target, 0o750); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(parent, "pi-acp-bridge")
		if err := os.Symlink(target, root); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := MaterializeShared(root, gid); err == nil || !strings.Contains(err.Error(), "real directory") {
			t.Fatalf("MaterializeShared() error = %v, want symlink rejection", err)
		}
	})
	t.Run("signature symlink", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "pi-acp-bridge")
		if err := os.Mkdir(root, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(root, -1, int(gid)); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "target")
		if err := os.Mkdir(target, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, Signature())); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := MaterializeShared(root, gid); err == nil || !strings.Contains(err.Error(), "real directory") {
			t.Fatalf("MaterializeShared() error = %v, want signature symlink rejection", err)
		}
	})
	t.Run("asset symlink", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "pi-acp-bridge")
		entrypoint, err := MaterializeShared(root, gid)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(entrypoint); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(parent, "target.js")
		if err := os.WriteFile(target, []byte("target"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, entrypoint); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := MaterializeShared(root, gid); err == nil || !strings.Contains(err.Error(), "real regular file") {
			t.Fatalf("MaterializeShared() error = %v, want asset symlink rejection", err)
		}
	})
}

func TestMaterializeSharedRealRunAsReadOnlyBoundary(t *testing.T) {
	if os.Getenv("LUMI_PI_BRIDGE_HELPER") == "1" {
		runSharedBridgeHelper()
		return
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("real cross-UID/GID boundary test requires Linux root")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to verify the run-as bridge startup boundary")
	}
	parent, err := os.MkdirTemp("", "lumi-pi-bridge-boundary-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o711); err != nil {
		t.Fatal(err)
	}
	helper := stageSharedBridgeBoundaryHelper(t, parent)
	readerUID, readerGID := uint32(62301), uint32(62302)
	root := filepath.Join(parent, "pi-acp-bridge")
	entrypoint, err := MaterializeShared(root, readerGID)
	if err != nil {
		t.Fatal(err)
	}

	check := exec.Command(node, "--check", entrypoint)
	check.SysProcAttr = credential(readerUID, readerGID)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("run-as node could not load embedded bridge: %v, output=%s", err, output)
	}
	runSharedHelper(t, helper, readerUID, readerGID, "reader", entrypoint)
	runSharedHelper(t, helper, 62303, 62304, "outsider", entrypoint)
}

func stageSharedBridgeBoundaryHelper(t *testing.T, parent string) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(parent, "pi-bridge-boundary-helper")
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

func runSharedHelper(t *testing.T, helper string, uid, gid uint32, role, entrypoint string) {
	t.Helper()
	cmd := exec.Command(helper, "-test.run=^TestMaterializeSharedRealRunAsReadOnlyBoundary$")
	cmd.Env = append(os.Environ(), "LUMI_PI_BRIDGE_HELPER=1", "LUMI_BOUNDARY_ROLE="+role, "LUMI_BOUNDARY_PATH="+entrypoint)
	cmd.SysProcAttr = credential(uid, gid)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s bridge helper error = %v, output=%s", role, err, output)
	}
}

func runSharedBridgeHelper() {
	path := os.Getenv("LUMI_BOUNDARY_PATH")
	readErr := func() error { _, err := os.ReadFile(path); return err }()
	metadataErr := func() error { _, err := os.ReadFile(filepath.Join(filepath.Dir(path), "package.json")); return err }()
	writeErr := os.WriteFile(path, []byte("tamper"), 0o640)
	removeErr := os.Remove(path)
	renameErr := os.Rename(path, path+".replaced")
	if os.Getenv("LUMI_BOUNDARY_ROLE") == "reader" {
		if readErr != nil || metadataErr != nil || writeErr == nil || removeErr == nil || renameErr == nil {
			fmt.Fprintf(os.Stderr, "reader results: read=%v metadata=%v write=%v remove=%v rename=%v\n", readErr, metadataErr, writeErr, removeErr, renameErr)
			os.Exit(2)
		}
		return
	}
	if readErr == nil || !errors.Is(readErr, os.ErrPermission) {
		fmt.Fprintf(os.Stderr, "outsider read result: %v\n", readErr)
		os.Exit(3)
	}
}

func credential(uid, gid uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{gid}}}
}

func assertSharedModeAndGID(t *testing.T, path string, mode os.FileMode, gid uint32) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != mode || uint32(stat.Gid) != gid {
		t.Fatalf("%s metadata = mode %o gid %d, want %o/%d", path, info.Mode().Perm(), stat.Gid, mode, gid)
	}
}
