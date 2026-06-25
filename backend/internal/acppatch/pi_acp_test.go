package acppatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsurePiACPPatchedAppliesAndIsIdempotent(t *testing.T) {
	pkgDir := makePackage(t, originalSource())

	status, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}
	if !status.Applied {
		t.Fatalf("status.Applied = false, want true")
	}

	source := readSource(t, pkgDir)
	if strings.Count(source, "function shouldUseSingleLiveSession()") != 1 {
		t.Fatalf("helper count = %d, want 1", strings.Count(source, "function shouldUseSingleLiveSession()"))
	}
	if strings.Count(source, closeAllNew) != 2 {
		t.Fatalf("patched closeAll count = %d, want 2", strings.Count(source, closeAllNew))
	}
	if !strings.Contains(source, "var PiAcpAgent = class") {
		t.Fatal("patched source lost PiAcpAgent class")
	}
	if _, err := os.Stat(filepath.Join(pkgDir, ".lumi-patches", PiACPMultiSessionID+".json")); err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	second, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err != nil {
		t.Fatalf("second EnsurePiACPPatched() error = %v", err)
	}
	if !second.Applied {
		t.Fatalf("second status.Applied = false, want true")
	}
	source = readSource(t, pkgDir)
	if strings.Count(source, "function shouldUseSingleLiveSession()") != 1 {
		t.Fatalf("helper duplicated after idempotent patch")
	}
}

func TestEnsurePiACPPatchedRejectsWrongVersion(t *testing.T) {
	pkgDir := makePackage(t, originalSource())
	writePackageJSON(t, pkgDir, "pi-acp", "0.0.28")

	_, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err == nil || !strings.Contains(err.Error(), "only supports pi-acp@0.0.27") {
		t.Fatalf("EnsurePiACPPatched() error = %v, want version rejection", err)
	}
}

func TestEnsurePiACPPatchedRejectsUnexpectedSource(t *testing.T) {
	pkgDir := makePackage(t, "var pkg = readNearestPackageJson(import.meta.url);\n")

	_, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err == nil || !strings.Contains(err.Error(), "source does not match") {
		t.Fatalf("EnsurePiACPPatched() error = %v, want source mismatch", err)
	}
}

func TestEnsurePiACPPatchedWritesMarkerWhenAlreadyPatched(t *testing.T) {
	pkgDir := makePackage(t, strings.ReplaceAll(strings.Replace(originalSource(), helperOld, helperNew, 1), closeAllOld, closeAllNew))

	status := Status(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if status.Applied {
		t.Fatal("Status().Applied = true without marker, want false")
	}

	if _, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)}); err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, ".lumi-patches", PiACPMultiSessionID+".json")); err != nil {
		t.Fatalf("marker not written for already patched source: %v", err)
	}
	status = Status(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if !status.Applied {
		t.Fatalf("Status().Applied = false after marker write: %s", status.Message)
	}
}

func TestEnsurePiACPPatchedReplacesCopiedExecutableWithPackageLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked npm bin layout is only used on Unix")
	}
	pkgDir := makePackage(t, originalSource())
	prefix := runtimePrefixForPackageDir(pkgDir)
	exe := filepath.Join(prefix, "bin", "pi-acp")
	if err := os.MkdirAll(filepath.Dir(exe), 0755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	if err := os.WriteFile(exe, []byte("#!/usr/bin/env node\nimport \"@agentclientprotocol/sdk\";\n"), 0755); err != nil {
		t.Fatalf("WriteFile(exe) error = %v", err)
	}

	if _, err := EnsurePiACPPatched(RuntimeOptions{Prefix: prefix}); err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}

	linkTarget, err := os.Readlink(exe)
	if err != nil {
		t.Fatalf("Readlink(exe) error = %v", err)
	}
	if !filepath.IsAbs(linkTarget) {
		linkTarget = filepath.Join(filepath.Dir(exe), linkTarget)
	}
	want := filepath.Join(pkgDir, piACPSourceFile)
	if filepath.Clean(linkTarget) != filepath.Clean(want) {
		t.Fatalf("pi-acp executable link = %q, want %q", linkTarget, want)
	}
}

func TestRuntimePrefixIgnoresNonSandboxNPMConfigPrefix(t *testing.T) {
	home := t.TempDir()
	userPrefix := filepath.Join(t.TempDir(), "user-npm")
	t.Setenv("HOME", home)
	t.Setenv("NPM_CONFIG_PREFIX", userPrefix)
	t.Setenv("LUMI_NPM_RUNTIME_PREFIX", "")

	want := filepath.Join(home, ".lumi", "runtime", "shared", "runtime", "npm")
	if got := RuntimePrefix(); got != want {
		t.Fatalf("RuntimePrefix() = %q, want %q", got, want)
	}
}

func TestRuntimePrefixAllowsSandboxNPMConfigPrefix(t *testing.T) {
	t.Setenv("NPM_CONFIG_PREFIX", "/lumi/runtime/npm")
	t.Setenv("LUMI_NPM_RUNTIME_PREFIX", "")

	if got := RuntimePrefix(); got != "/lumi/runtime/npm" {
		t.Fatalf("RuntimePrefix() = %q, want /lumi/runtime/npm", got)
	}
}

func originalSource() string {
	return helperOld + `
  async newSession(params) {
    this.sessions.closeAllExcept?.(session.sessionId);
  }
  async loadSession(params) {
    this.sessions.closeAllExcept?.(session.sessionId);
  }
};`
}

func makePackage(t *testing.T, source string) string {
	t.Helper()
	prefix := t.TempDir()
	pkgDir := filepath.Join(prefix, "lib", "node_modules", PiACPPackage)
	if err := os.MkdirAll(filepath.Join(pkgDir, "dist"), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writePackageJSON(t, pkgDir, PiACPPackage, PiACPVersion)
	if err := os.WriteFile(filepath.Join(pkgDir, piACPSourceFile), []byte(source), 0644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	return pkgDir
}

func writePackageJSON(t *testing.T, pkgDir, name, version string) {
	t.Helper()
	data, err := json.Marshal(packageJSON{Name: name, Version: version})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0644); err != nil {
		t.Fatalf("WriteFile(package.json) error = %v", err)
	}
}

func readSource(t *testing.T, pkgDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(pkgDir, piACPSourceFile))
	if err != nil {
		t.Fatalf("ReadFile(source) error = %v", err)
	}
	return string(data)
}

func runtimePrefixForPackageDir(pkgDir string) string {
	return filepath.Clean(filepath.Join(pkgDir, "..", "..", ".."))
}
