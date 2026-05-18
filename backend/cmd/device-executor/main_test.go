package main

import (
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/setupcheck"
)

func TestParseConnectArgsInstall(t *testing.T) {
	opts, err := parseConnectArgs([]string{
		"--server", "http://localhost:3000",
		"--token", "secret",
		"--config", "/tmp/config.json",
		"--install",
	})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if !opts.Install {
		t.Fatal("Install = false, want true")
	}
	if opts.SkipSetup {
		t.Fatal("SkipSetup = true, want false")
	}
}

func TestInstallSetupDependenciesWritesBootstrapManifest(t *testing.T) {
	original := bootstrapManifestFile
	bootstrapManifestFile = filepath.Join(t.TempDir(), "bootstrap.json")
	t.Cleanup(func() { bootstrapManifestFile = original })

	status := setupcheck.SetupStatus{
		Ready: true,
		Environment: []setupcheck.DependencyItem{
			{Name: "npm", Command: "npm", Status: "ready"},
			{Name: "npx", Command: "npx", Status: "ready"},
		},
		Agents: []setupcheck.DependencyItem{
			{Name: "Claude", Command: "claude", Status: "ready"},
		},
	}

	if err := installSetupDependencies(status); err != nil {
		t.Fatalf("installSetupDependencies() error = %v", err)
	}
	if !bootstrapManifestReady(setupSignature(status)) {
		t.Fatal("bootstrap manifest was not written")
	}
}
