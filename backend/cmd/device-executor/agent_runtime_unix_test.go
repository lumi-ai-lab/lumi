//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestSandboxImageRunAsPiCredentialSourceRealRPC(t *testing.T) {
	if os.Getenv("LUMI_TEST_SANDBOX_PI_RPC") != "1" {
		t.Skip("enabled only for a built Sandbox image with Pi installed")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("Sandbox Pi RPC boundary requires Linux root")
	}
	source := os.Getenv("LUMI_TEST_PI_SOURCE")
	home := os.Getenv("LUMI_TEST_PI_HOME")
	piCommand := os.Getenv("LUMI_TEST_PI_COMMAND")
	uid := mustTestID(t, "LUMI_TEST_PI_UID")
	gid := mustTestID(t, "LUMI_TEST_PI_GID")
	readerGID := mustTestID(t, "LUMI_TEST_PI_READER_GID")
	publisherUID := mustTestID(t, "LUMI_TEST_PUBLISHER_UID")
	publisherGID := mustTestID(t, "LUMI_TEST_PUBLISHER_GID")

	if err := prepareRunAsPiHome(source, home, uid, gid); err != nil {
		t.Fatal(err)
	}
	sourceAuth := filepath.Join(source, ".pi", "agent", "auth-boundary")
	homeAuth := filepath.Join(home, ".pi", "agent", "auth-boundary")
	if data, err := os.ReadFile(homeAuth); err != nil || string(data) != "sandbox-boundary" {
		t.Fatalf("materialized Pi credential boundary = %q, err=%v", data, err)
	}
	if info, err := os.Stat(homeAuth); err != nil || info.Mode().Perm() != 0o600 || fileOwner(info) != [2]uint32{uid, gid} {
		t.Fatalf("materialized Pi credential metadata = info=%v err=%v", info, err)
	}
	if info, err := os.Stat(sourceAuth); err != nil || info.Mode().Perm() != 0o600 || fileOwner(info) != [2]uint32{publisherUID, publisherGID} {
		t.Fatalf("publisher source metadata = info=%v err=%v", info, err)
	}
	if err := os.WriteFile(sourceAuth, []byte("mutation"), 0o600); err == nil {
		t.Fatal("read-only publisher credential source accepted a write")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pi := exec.CommandContext(ctx, piCommand, "--mode", "rpc", "--no-themes")
	pi.Dir = "/workspace"
	pi.Env = []string{
		"HOME=" + home,
		"PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent"),
		"PATH=/lumi/runtime/npm/bin:/usr/local/bin:/usr/bin:/bin",
	}
	pi.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uid, Gid: gid, Groups: []uint32{readerGID},
	}}
	stdin, err := pi.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := pi.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pi.Stderr = io.Discard
	if err := pi.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(stdin, `{"type":"get_state","id":"sandbox-boundary"}`); err != nil {
		t.Fatal(err)
	}
	rpcOK := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var response struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Success bool   `json:"success"`
		}
		if json.Unmarshal(scanner.Bytes(), &response) == nil && response.Type == "response" && response.ID == "sandbox-boundary" {
			rpcOK = response.Success
			break
		}
	}
	_ = pi.Process.Kill()
	_ = pi.Wait()
	if !rpcOK {
		t.Fatal("Pi 0.83.0 RPC get_state did not succeed")
	}

	sessionPath := filepath.Join(home, ".pi", "agent", "sessions", "sandbox-rpc-boundary")
	writer := exec.Command("sh", "-c", `printf session > "$SESSION_PATH"`)
	writer.Env = []string{"SESSION_PATH=" + sessionPath, "PATH=/usr/bin:/bin"}
	writer.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uid, Gid: gid, Groups: []uint32{readerGID},
	}}
	if output, err := writer.CombinedOutput(); err != nil {
		t.Fatalf("run-as Pi session write failed: %v, output=%s", err, output)
	}

	outsider := exec.Command("sh", "-c", `cat "$HOME/.pi/agent/auth-boundary"`)
	outsider.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	outsider.SysProcAttr = testCredential(uid+1, gid+1)
	if output, err := outsider.CombinedOutput(); err == nil {
		t.Fatalf("outsider read Pi credential boundary: %s", output)
	}
}

func mustTestID(t *testing.T, key string) uint32 {
	t.Helper()
	value, err := strconv.ParseUint(os.Getenv(key), 10, 32)
	if err != nil || value == 0 {
		t.Fatalf("invalid %s", key)
	}
	return uint32(value)
}

func TestSecureRunAsPiHomeRealCredentialAndSessionBoundary(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root to exercise real UID/GID boundaries")
	}
	parent, err := os.MkdirTemp("", "lumi-run-as-pi-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o711); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "pi-source")
	home := filepath.Join(parent, "pi-home")
	sourceAuthPath := filepath.Join(source, ".pi", "agent", "auth.json")
	authPath := filepath.Join(home, ".pi", "agent", "auth.json")
	if err := os.MkdirAll(filepath.Dir(sourceAuthPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceAuthPath, []byte("credential-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	publisherUID, publisherGID := uint32(62411), uint32(62412)
	uid, gid := uint32(62401), uint32(62402)
	if err := chownTestTree(source, publisherUID, publisherGID); err != nil {
		t.Fatal(err)
	}
	if err := prepareRunAsPiHome(source, home, uid, gid); err != nil {
		t.Fatal(err)
	}

	reader := exec.Command("sh", "-c", `test ! -r "$SOURCE/.pi/agent/auth.json" && test "$(cat "$HOME/.pi/agent/auth.json")" = credential-marker && printf session > "$HOME/.pi/agent/sessions/rpc.json"`)
	reader.Env = []string{"HOME=" + home, "SOURCE=" + source, "PATH=/usr/bin:/bin"}
	reader.SysProcAttr = testCredential(uid, gid)
	if output, err := reader.CombinedOutput(); err != nil {
		t.Fatalf("run-as Pi credential/session RPC boundary failed: %v, output=%s", err, output)
	}
	if data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "sessions", "rpc.json")); err != nil || string(data) != "session" {
		t.Fatalf("session write = %q, err=%v", data, err)
	}
	if info, err := os.Stat(sourceAuthPath); err != nil || info.Mode().Perm() != 0o600 || fileOwner(info) != [2]uint32{publisherUID, publisherGID} {
		t.Fatalf("publisher source metadata changed: info=%v err=%v", info, err)
	}
	publisherRefresh := exec.Command("sh", "-c", `printf refreshed > "$SOURCE/.pi/agent/auth.json"`)
	publisherRefresh.Env = []string{"SOURCE=" + source, "PATH=/usr/bin:/bin"}
	publisherRefresh.SysProcAttr = testCredential(publisherUID, publisherGID)
	if output, err := publisherRefresh.CombinedOutput(); err != nil {
		t.Fatalf("publisher could not refresh source after Pi HOME preparation: %v, output=%s", err, output)
	}
	if err := prepareRunAsPiHome(source, home, uid, gid); err != nil {
		t.Fatalf("validate existing Pi HOME after publisher refresh: %v", err)
	}
	if data, err := os.ReadFile(authPath); err != nil || string(data) != "credential-marker" {
		t.Fatalf("existing container Pi HOME was unexpectedly replaced: %q, err=%v", data, err)
	}
	rebuiltHome := filepath.Join(parent, "rebuilt-pi-home")
	if err := prepareRunAsPiHome(source, rebuiltHome, uid, gid); err != nil {
		t.Fatalf("prepare rebuilt Sandbox Pi HOME: %v", err)
	}
	rebuiltAuthPath := filepath.Join(rebuiltHome, ".pi", "agent", "auth.json")
	if data, err := os.ReadFile(rebuiltAuthPath); err != nil || string(data) != "refreshed" {
		t.Fatalf("rebuilt Sandbox Pi HOME credential = %q, err=%v", data, err)
	}
	if info, err := os.Stat(rebuiltAuthPath); err != nil || info.Mode().Perm() != 0o600 || fileOwner(info) != [2]uint32{uid, gid} {
		t.Fatalf("rebuilt Sandbox Pi HOME metadata = info=%v err=%v", info, err)
	}
	if info, err := os.Stat(sourceAuthPath); err != nil || info.Mode().Perm() != 0o600 || fileOwner(info) != [2]uint32{publisherUID, publisherGID} {
		t.Fatalf("publisher source metadata changed after rebuild: info=%v err=%v", info, err)
	}

	outsider := exec.Command("sh", "-c", `cat "$HOME/.pi/agent/auth.json"`)
	outsider.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	outsider.SysProcAttr = testCredential(62403, 62404)
	if output, err := outsider.CombinedOutput(); err == nil {
		t.Fatalf("outsider read Pi credential: %s", output)
	}
}

func chownTestTree(root string, uid, gid uint32) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	})
}

func fileOwner(info os.FileInfo) [2]uint32 {
	stat := info.Sys().(*syscall.Stat_t)
	return [2]uint32{stat.Uid, stat.Gid}
}

func TestSecureRunAsPiHomeRejectsSymlink(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root to exercise ownership setup")
	}
	home := filepath.Join(t.TempDir(), "pi-home")
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(home, ".pi", "agent", "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err := secureRunAsPiHome(home, 62401, 62402); err == nil {
		t.Fatal("secureRunAsPiHome accepted a symlink")
	}
}

func testCredential(uid, gid uint32) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
}
