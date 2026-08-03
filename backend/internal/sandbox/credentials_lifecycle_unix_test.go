//go:build !windows

package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/piruntime"
)

const runAsPiLifecycleHelperEnv = "LUMI_RUN_AS_PI_LIFECYCLE_HELPER"

func TestRunAsPiCredentialSourceSurvivesPublisherRefreshAndRebuild(t *testing.T) {
	if os.Getenv(runAsPiLifecycleHelperEnv) == "1" {
		runPublisherCredentialStageHelper()
		return
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("requires Linux root to exercise publisher and Pi UID separation")
	}

	parent, err := os.MkdirTemp("", "lumi-pi-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o711); err != nil {
		t.Fatal(err)
	}
	publisherUID, publisherGID := uint32(62611), uint32(62612)
	runAsUID, runAsGID := uint32(62621), uint32(62622)
	home := filepath.Join(parent, "publisher-home")
	runtimeDir := filepath.Join(parent, "runtime")
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := chownLifecycleTree(home, publisherUID, publisherGID); err != nil {
		t.Fatal(err)
	}
	if err := chownLifecycleTree(runtimeDir, publisherUID, publisherGID); err != nil {
		t.Fatal(err)
	}

	helper := stageLifecycleHelper(t, parent)
	runPublisherCredentialStage(t, helper, home, runtimeDir, publisherUID, publisherGID, runAsUID, runAsGID, "first")
	staging := filepath.Join(runtimeDir, "pi-source")
	stagedAuth := filepath.Join(staging, ".pi", "agent", "auth.json")
	assertLifecycleOwner(t, stagedAuth, publisherUID, publisherGID)

	containerHome := filepath.Join(parent, "container-pi-home")
	if err := copyDir(staging, containerHome); err != nil {
		t.Fatal(err)
	}
	if err := chownLifecycleTree(containerHome, runAsUID, runAsGID); err != nil {
		t.Fatal(err)
	}
	assertLifecycleOwner(t, stagedAuth, publisherUID, publisherGID)

	runPublisherCredentialStage(t, helper, home, runtimeDir, publisherUID, publisherGID, runAsUID, runAsGID, "second")
	assertLifecycleOwner(t, stagedAuth, publisherUID, publisherGID)
	data, err := os.ReadFile(stagedAuth)
	if err != nil || string(data) != "second" {
		t.Fatalf("refreshed publisher staging = %q, err=%v", data, err)
	}
}

func runPublisherCredentialStage(
	t *testing.T,
	helper, home, runtimeDir string,
	publisherUID, publisherGID, runAsUID, runAsGID uint32,
	value string,
) {
	t.Helper()
	cmd := exec.Command(helper, "-test.run=^TestRunAsPiCredentialSourceSurvivesPublisherRefreshAndRebuild$")
	cmd.Env = append(os.Environ(),
		runAsPiLifecycleHelperEnv+"=1",
		"LUMI_TEST_PUBLISHER_HOME="+home,
		"LUMI_TEST_RUNTIME_DIR="+runtimeDir,
		"LUMI_TEST_RUN_AS_UID="+fmt.Sprint(runAsUID),
		"LUMI_TEST_RUN_AS_GID="+fmt.Sprint(runAsGID),
		"LUMI_TEST_CREDENTIAL_VALUE="+value,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: publisherUID, Gid: publisherGID, Groups: []uint32{},
	}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("publisher credential staging failed: %v, output=%s", err, output)
	}
}

func runPublisherCredentialStageHelper() {
	home := os.Getenv("LUMI_TEST_PUBLISHER_HOME")
	runtimeDir := os.Getenv("LUMI_TEST_RUNTIME_DIR")
	uid := parseLifecycleID("LUMI_TEST_RUN_AS_UID")
	gid := parseLifecycleID("LUMI_TEST_RUN_AS_GID")
	value := os.Getenv("LUMI_TEST_CREDENTIAL_VALUE")
	authPath := filepath.Join(home, ".pi", "agent", "auth.json")
	if err := os.WriteFile(authPath, []byte(value), 0o600); err != nil {
		os.Exit(2)
	}
	agents := []config.AgentConfig{{
		ID: "pi", Command: "npx", Args: []string{"-y", config.PiACPPackageSpec},
		RunAsUID: &uid, RunAsGID: &gid,
	}}
	mounts := resolveCredentialMountsFromHomeForAgents(home, runtimeDir, agents)
	mount := findCredentialMount(mounts, piruntime.SandboxCredentialSource)
	if mount == nil || !mount.ReadOnly || findCredentialMount(mounts, piruntime.SandboxHome) != nil {
		os.Exit(3)
	}
	data, err := os.ReadFile(filepath.Join(mount.Source, ".pi", "agent", "auth.json"))
	if err != nil || string(data) != value {
		os.Exit(4)
	}
}

func parseLifecycleID(key string) uint32 {
	var value uint32
	if _, err := fmt.Sscan(os.Getenv(key), &value); err != nil {
		os.Exit(5)
	}
	return value
}

func stageLifecycleHelper(t *testing.T, parent string) string {
	t.Helper()
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target := filepath.Join(parent, "publisher-lifecycle-helper")
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
	return target
}

func chownLifecycleTree(root string, uid, gid uint32) error {
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

func assertLifecycleOwner(t *testing.T, path string, uid, gid uint32) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != uid || stat.Gid != gid || info.Mode().Perm() != 0o600 {
		t.Fatalf("publisher staging metadata changed: uid=%d gid=%d mode=%#o", stat.Uid, stat.Gid, info.Mode().Perm())
	}
}
