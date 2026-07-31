package workspacecli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUsesZsh(t *testing.T) {
	for _, shell := range []string{"/bin/zsh", "zsh", `C:\tools\zsh.exe`, "/bin/zsh -l"} {
		if !UsesZsh(shell) {
			t.Fatalf("UsesZsh(%q) = false, want true", shell)
		}
	}
	for _, shell := range []string{"", "/bin/sh", "/bin/bash"} {
		if UsesZsh(shell) {
			t.Fatalf("UsesZsh(%q) = true, want false", shell)
		}
	}
}

func TestConfigureZshStartupEnvPreservesCustomZDOTDir(t *testing.T) {
	root := t.TempDir()
	sourceDir := t.TempDir()
	requiredPath := filepath.Join(t.TempDir(), "bin")
	env := map[string]string{
		"ZDOTDIR": sourceDir,
	}

	if err := ConfigureZshStartupEnv(env, root, "/bin/zsh", requiredPath); err != nil {
		t.Fatal(err)
	}
	bridge := env[ZDOTDirEnv]
	if bridge == "" || bridge == sourceDir {
		t.Fatalf("ZDOTDIR = %q, want managed bridge", bridge)
	}
	if env[OriginalZDOTDirEnv] != sourceDir {
		t.Fatalf("LUMI_ORIGINAL_ZDOTDIR = %q, want %q", env[OriginalZDOTDirEnv], sourceDir)
	}
	if env[ManagedZDOTDirEnv] != bridge {
		t.Fatalf("LUMI_MANAGED_ZDOTDIR = %q, want %q", env[ManagedZDOTDirEnv], bridge)
	}
	for _, name := range zshStartupFiles {
		if info, err := os.Stat(filepath.Join(bridge, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("bridge file %s: info=%v err=%v", name, info, err)
		}
	}

	if err := ConfigureZshStartupEnv(env, root, "/bin/zsh", ""); err != nil {
		t.Fatal(err)
	}
	if env[ZDOTDirEnv] != sourceDir {
		t.Fatalf("restored ZDOTDIR = %q, want %q", env[ZDOTDirEnv], sourceDir)
	}
	if _, ok := env[ManagedZDOTDirEnv]; ok {
		t.Fatal("managed ZDOTDIR marker remains after bridge removal")
	}
}

func TestConfigureZshStartupEnvReplacesWorkspaceBridge(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"ZDOTDIR": "/custom/zsh"}

	if err := ConfigureZshStartupEnv(env, root, "/bin/zsh", "/workspace-a/bin"); err != nil {
		t.Fatal(err)
	}
	first := env[ZDOTDirEnv]
	if err := ConfigureZshStartupEnv(env, root, "/bin/zsh", "/workspace-b/bin"); err != nil {
		t.Fatal(err)
	}
	if second := env[ZDOTDirEnv]; second == "" || second == first {
		t.Fatalf("replacement ZDOTDIR = %q, want different from %q", second, first)
	}
	if env[OriginalZDOTDirEnv] != "/custom/zsh" {
		t.Fatalf("LUMI_ORIGINAL_ZDOTDIR = %q", env[OriginalZDOTDirEnv])
	}
}

func TestZshLoginShellKeepsRequiredPathAfterAbsoluteProfileOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zsh startup behavior is Unix-specific")
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}

	home := t.TempDir()
	workspace := t.TempDir()
	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metricCLI := filepath.Join(binDir, "qdm-metric-cli")
	if err := os.WriteFile(metricCLI, []byte("#!/bin/sh\nprintf 'metric-version-test\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zprofile"), []byte("export PATH=/usr/bin:/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{}
	bridgeRoot := filepath.Join(t.TempDir(), "shell-env")
	if err := ConfigureZshStartupEnv(env, bridgeRoot, zsh, binDir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(zsh, "-lc", "command -v qdm-metric-cli; qdm-metric-cli version; printf 'PATH=%s\\n' \"$PATH\"")
	cmd.Env = []string{
		"HOME=" + home,
		"SHELL=" + zsh,
		"PATH=" + binDir + ":/usr/bin:/bin",
		"ZDOTDIR=" + env[ZDOTDirEnv],
		OriginalZDOTDirEnv + "=" + env[OriginalZDOTDirEnv],
		ManagedZDOTDirEnv + "=" + env[ManagedZDOTDirEnv],
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh -lc failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, metricCLI+"\nmetric-version-test\n") {
		t.Fatalf("unexpected command output:\n%s", text)
	}
	pathLine := strings.TrimSpace(strings.TrimPrefix(text[strings.LastIndex(text, "PATH="):], "PATH="))
	parts := filepath.SplitList(pathLine)
	if len(parts) == 0 || parts[0] != binDir {
		t.Fatalf("PATH = %q, want first entry %q", pathLine, binDir)
	}
	count := 0
	for _, part := range parts {
		if part == binDir {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("PATH = %q, workspace bin appears %d times", pathLine, count)
	}
}
