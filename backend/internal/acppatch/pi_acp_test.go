package acppatch

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

//go:embed assets/pi-acp-0.0.33-dist-index.original.js
var piACP0033OriginalDist []byte

func TestEnsurePiACPPatchedAppliesAndIsIdempotent(t *testing.T) {
	pkgDir := makePackage(t, string(piACP0033OriginalDist))

	status, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}
	if !status.Applied {
		t.Fatalf("status.Applied = false, want true")
	}

	source := readSource(t, pkgDir)
	if !strings.Contains(source, "extractHostAuthFromMeta") {
		t.Fatal("patched source missing extractHostAuthFromMeta")
	}
	if !strings.Contains(source, "shouldUseSingleLiveSession") {
		t.Fatal("patched source missing shouldUseSingleLiveSession")
	}
	if !strings.Contains(source, "hostAuth") {
		t.Fatal("patched source missing hostAuth")
	}
	if !strings.Contains(source, "extractLumiSessionEnvFromMeta") || !strings.Contains(source, "...params.sessionEnv") {
		t.Fatal("patched source missing Local Session env support")
	}
	if _, err := os.Stat(filepath.Join(pkgDir, ".lumi-patches", PiACPHostAuthPatchID+".json")); err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	second, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err != nil {
		t.Fatalf("second EnsurePiACPPatched() error = %v", err)
	}
	if !second.Applied {
		t.Fatalf("second status.Applied = false, want true")
	}
}

func TestEnsurePiACPPatchedRejectsWrongVersion(t *testing.T) {
	pkgDir := makePackage(t, string(piACP0033OriginalDist))
	writePackageJSON(t, pkgDir, "pi-acp", "0.0.28")

	_, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err == nil || !strings.Contains(err.Error(), "only supports pi-acp@0.0.33") {
		t.Fatalf("EnsurePiACPPatched() error = %v, want version rejection", err)
	}
}

func TestEnsurePiACPPatchedRejectsUnexpectedSource(t *testing.T) {
	pkgDir := makePackage(t, "not-the-official-dist\n")

	_, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if err == nil || !strings.Contains(err.Error(), "does not match expected official") {
		t.Fatalf("EnsurePiACPPatched() error = %v, want sha mismatch", err)
	}
}

func TestEnsurePiACPPatchedWritesMarkerWhenAlreadyPatched(t *testing.T) {
	pkgDir := makePackage(t, string(piACP0033PatchedDist))

	status := Status(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if status.Applied {
		t.Fatal("Status().Applied = true without marker, want false")
	}

	if _, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)}); err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, ".lumi-patches", PiACPHostAuthPatchID+".json")); err != nil {
		t.Fatalf("marker not written for already patched source: %v", err)
	}
	status = Status(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)})
	if !status.Applied {
		t.Fatalf("Status().Applied = false after marker write: %s", status.Message)
	}
}

func TestEnsurePiACPPatchedUpgradesPreviousLumiPatch(t *testing.T) {
	previous := strings.ReplaceAll(string(piACP0033PatchedDist), "extractLumiSessionEnvFromMeta", "extractPreviousSessionEnvFromMeta")
	pkgDir := makePackage(t, previous)

	if _, err := EnsurePiACPPatched(RuntimeOptions{Prefix: runtimePrefixForPackageDir(pkgDir)}); err != nil {
		t.Fatalf("EnsurePiACPPatched() error = %v", err)
	}
	if got := readSource(t, pkgDir); got != string(piACP0033PatchedDist) {
		t.Fatal("previous Lumi patch was not upgraded")
	}
}

func TestEnsurePiACPPatchedReplacesCopiedExecutableWithPackageLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked npm bin layout is only used on Unix")
	}
	pkgDir := makePackage(t, string(piACP0033OriginalDist))
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

func TestOriginalDistShaMatchesConstant(t *testing.T) {
	sum := sha256.Sum256(piACP0033OriginalDist)
	got := hex.EncodeToString(sum[:])
	if got != piACP0033OriginalDistSHA256 {
		t.Fatalf("embedded original sha = %s, want %s", got, piACP0033OriginalDistSHA256)
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
