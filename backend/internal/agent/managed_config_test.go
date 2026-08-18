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

	index, pkg := npxPackageArg("npx", []string{"-y", "pi-acp@0.0.33", "--flag"})
	if index != 1 || pkg != "pi-acp@0.0.33" {
		t.Fatalf("npxPackageArg() = (%d, %q), want (1, pi-acp@0.0.33)", index, pkg)
	}
}

func TestResolveManagedConfigUsesPatchedPiACPExecutable(t *testing.T) {
	prefix := makePiRuntime(t, false)
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

	prefix := makePiRuntime(t, true)
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

// makePiRuntime installs an official-looking pi-acp@0.0.33 package and applies the Lumi patch.
// When runnable is true, replaces dist/index.js with a shell stub that still looks patched
// (so Ensure is idempotent) and can write MARKER for process start tests.
func makePiRuntime(t *testing.T, runnable bool) string {
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

	// Official 0.0.33 dist so EnsurePiACPPatched can replace with embedded host-auth build.
	originalPath := filepath.Join("..", "acppatch", "assets", "pi-acp-0.0.33-dist-index.original.js")
	original, err := os.ReadFile(originalPath)
	if err != nil {
		// Fallback when tests run from module root differently.
		original, err = os.ReadFile(filepath.Join("internal", "acppatch", "assets", "pi-acp-0.0.33-dist-index.original.js"))
	}
	if err != nil {
		t.Fatalf("ReadFile(original dist) error = %v", err)
	}
	index := filepath.Join(pkgDir, "dist", "index.js")
	if err := os.WriteFile(index, original, 0644); err != nil {
		t.Fatalf("WriteFile(index.js) error = %v", err)
	}

	if _, err := acppatch.EnsurePiACPPatched(acppatch.RuntimeOptions{Prefix: prefix}); err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}

	if runnable {
		stub := "#!/bin/sh\n# extractHostAuthFromMeta shouldUseSingleLiveSession hostAuth extractLumiSessionEnvFromMeta ...params.sessionEnv\nprintf started > \"$MARKER\"\ntrap 'exit 0' INT TERM\nsleep 30\n"
		if err := os.WriteFile(index, []byte(stub), 0755); err != nil {
			t.Fatalf("WriteFile(stub index) error = %v", err)
		}
		if err := os.Chmod(index, 0755); err != nil {
			t.Fatalf("Chmod(stub index) error = %v", err)
		}
		// Symlink itself is not +x on some FS; ensure bin path is executable via chmod -h not always available.
		// Replace symlink with a tiny launcher that execs the stub script.
		bin := filepath.Join(prefix, "bin", "pi-acp")
		launcher := "#!/bin/sh\nexec \"" + index + "\" \"$@\"\n"
		_ = os.Remove(bin)
		if err := os.WriteFile(bin, []byte(launcher), 0755); err != nil {
			t.Fatalf("WriteFile(bin launcher) error = %v", err)
		}
	}
	return prefix
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
