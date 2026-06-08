package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/acppatch"
	"github.com/pengmide/lumi/internal/config"
)

func TestNpxPackageArg(t *testing.T) {
	t.Parallel()

	index, pkg := npxPackageArg("npx", []string{"-y", "pi-acp@0.0.27", "--flag"})
	if index != 1 || pkg != "pi-acp@0.0.27" {
		t.Fatalf("npxPackageArg() = (%d, %q), want (1, pi-acp@0.0.27)", index, pkg)
	}
}

func TestResolveManagedConfigUsesPatchedPiACPExecutable(t *testing.T) {
	prefix := makePiRuntime(t)
	t.Setenv("LUMI_NPM_RUNTIME_PREFIX", prefix)
	t.Setenv("NPM_CONFIG_PREFIX", "")

	resolved, err := ResolveManagedConfig(&config.AgentConfig{
		ID:      "pi",
		Command: "npx",
		Args:    []string{"-y", acppatch.PiACPPackageSpec, "--trace"},
		Env:     map[string]string{"KEEP": "1"},
	})
	if err != nil {
		t.Fatalf("ResolveManagedConfig() error = %v", err)
	}
	if resolved.Command != acppatch.ExecutablePath(prefix) {
		t.Fatalf("Command = %q, want %q", resolved.Command, acppatch.ExecutablePath(prefix))
	}
	if len(resolved.Args) != 1 || resolved.Args[0] != "--trace" {
		t.Fatalf("Args = %#v, want [--trace]", resolved.Args)
	}
	if resolved.Env["KEEP"] != "1" {
		t.Fatalf("Env[KEEP] = %q, want 1", resolved.Env["KEEP"])
	}
}

func TestProcessStartUsesManagedPiACPExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell script")
	}

	prefix := makePiRuntime(t)
	t.Setenv("LUMI_NPM_RUNTIME_PREFIX", prefix)
	t.Setenv("NPM_CONFIG_PREFIX", "")

	marker := filepath.Join(t.TempDir(), "started")
	proc := NewProcess(&config.AgentConfig{
		ID:      "pi",
		Name:    "PI",
		Command: "npx",
		Args:    []string{"-y", acppatch.PiACPPackageSpec},
		Env:     map[string]string{"MARKER": marker},
	})

	if err := proc.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer proc.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "started" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("managed pi-acp executable did not write marker at %s", marker)
}

func makePiRuntime(t *testing.T) string {
	t.Helper()

	prefix := t.TempDir()
	pkgDir := filepath.Join(prefix, "lib", "node_modules", acppatch.PiACPPackage)
	if err := os.MkdirAll(filepath.Join(pkgDir, "dist"), 0755); err != nil {
		t.Fatalf("MkdirAll(package) error = %v", err)
	}
	data, err := json.Marshal(map[string]string{
		"name":    acppatch.PiACPPackage,
		"version": acppatch.PiACPVersion,
	})
	if err != nil {
		t.Fatalf("Marshal(package.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "dist", "index.js"), []byte(piACPOriginalSource()), 0644); err != nil {
		t.Fatalf("WriteFile(index.js) error = %v", err)
	}

	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	bin := filepath.Join(binDir, "pi-acp")
	script := "#!/bin/sh\nprintf started > \"$MARKER\"\ntrap 'exit 0' INT TERM\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(pi-acp) error = %v", err)
	}
	return prefix
}

func piACPOriginalSource() string {
	return `var pkg = readNearestPackageJson(import.meta.url);
var PiAcpAgent = class {
  async newSession(params) {
    this.sessions.closeAllExcept?.(session.sessionId);
  }
  async loadSession(params) {
    this.sessions.closeAllExcept?.(session.sessionId);
  }
};`
}

func TestResolveManagedConfigLeavesOtherAgentsUnchanged(t *testing.T) {
	t.Parallel()

	cfg := &config.AgentConfig{ID: "codex", Command: "npx", Args: []string{"-y", "@zed-industries/codex-acp"}}
	resolved, err := ResolveManagedConfig(cfg)
	if err != nil {
		t.Fatalf("ResolveManagedConfig() error = %v", err)
	}
	if resolved != cfg {
		t.Fatal("ResolveManagedConfig returned a copy for an unmanaged agent")
	}
	if strings.Join(resolved.Args, " ") != "-y @zed-industries/codex-acp" {
		t.Fatalf("Args = %#v", resolved.Args)
	}
}
