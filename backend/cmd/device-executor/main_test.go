package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/config"
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

func TestSetupSignatureIncludesPinnedPiVersions(t *testing.T) {
	status := setupcheck.SetupStatus{
		Agents: []setupcheck.DependencyItem{
			{Name: "PI", Command: "pi", Package: config.PiCodingAgentPackageSpec, Status: "ready"},
		},
		ACPPackages: []setupcheck.DependencyItem{
			{Name: "PI", Package: config.PiACPPackageSpec, Status: "ready"},
		},
	}
	current := setupSignature(status)
	status.ACPPackages[0].Package = config.LegacyPiACPPackageSpec
	if current == setupSignature(status) {
		t.Fatal("setupSignature did not include PI ACP version")
	}
	status.ACPPackages[0].Package = config.PiACPPackageSpec
	status.Agents[0].Package = "@earendil-works/pi-coding-agent@0.82.1"
	if current == setupSignature(status) {
		t.Fatal("setupSignature did not include PI CLI version")
	}
}

func TestBootstrapManifestRejectsPreviousVersion(t *testing.T) {
	original := bootstrapManifestFile
	bootstrapManifestFile = filepath.Join(t.TempDir(), "bootstrap.json")
	t.Cleanup(func() { bootstrapManifestFile = original })

	if err := os.WriteFile(bootstrapManifestFile, []byte(`{"version":1,"signature":"legacy"}`), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if bootstrapManifestReady("legacy") {
		t.Fatal("bootstrapManifestReady() accepted the pre-upgrade manifest version")
	}
}
