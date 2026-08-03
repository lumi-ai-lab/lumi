package piacpbridge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMaterializeCreatesPrivateVersionedSelfContainedRuntime(t *testing.T) {
	t.Setenv("LUMI_HOME", t.TempDir())

	entrypoint, err := Materialize()
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if !strings.Contains(entrypoint, Signature()) {
		t.Fatalf("entrypoint %q does not include signature %q", entrypoint, Signature())
	}
	for _, path := range []string{
		entrypoint,
		filepath.Join(filepath.Dir(entrypoint), "package.json"),
		filepath.Join(filepath.Dir(entrypoint), "LICENSE"),
		filepath.Join(filepath.Dir(entrypoint), "THIRD_PARTY_NOTICES.md"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", filepath.Base(path), got)
		}
	}
	notices, err := os.ReadFile(filepath.Join(filepath.Dir(entrypoint), "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@agentclientprotocol/sdk", "Apache License", "zod", "MIT License"} {
		if !strings.Contains(string(notices), want) {
			t.Fatalf("third-party notices missing %q", want)
		}
	}
	if info, err := os.Stat(filepath.Dir(entrypoint)); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("runtime directory mode = %o, want 700", got)
	}

	first, err := os.ReadFile(entrypoint)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := Materialize()
	if err != nil {
		t.Fatalf("second Materialize() error = %v", err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if secondPath != entrypoint || string(second) != string(first) {
		t.Fatal("Materialize() is not idempotent")
	}
}

func TestSharedRootUsesTraversableLocationOutsidePrivateHome(t *testing.T) {
	t.Setenv("LUMI_HOME", filepath.Join(t.TempDir(), "private-home"))
	root, err := SharedRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && filepath.Dir(root) != "/tmp" {
		t.Fatalf("SharedRoot() = %q, want direct child of /tmp", root)
	}
	if !strings.HasPrefix(filepath.Base(root), "lumi-pi-acp-bridge-") {
		t.Fatalf("SharedRoot() = %q, want publisher-scoped bridge name", root)
	}
}

func TestVersionMatchesForkMetadata(t *testing.T) {
	got, err := Version()
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.0.33-lumi.1" {
		t.Fatalf("Version() = %q", got)
	}
}
