package setupcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestInitialStatusIncludesQwenPackageAndCLI(t *testing.T) {
	t.Parallel()

	status := InitialStatus([]config.AgentConfig{
		{
			ID:      "qwen",
			Name:    "Qwen Code",
			Command: "npx",
			Args:    []string{"-y", "@qwen-code/qwen-code", "--acp"},
		},
	})

	if len(status.ACPPackages) != 1 {
		t.Fatalf("len(ACPPackages) = %d, want 1", len(status.ACPPackages))
	}
	if got := status.ACPPackages[0].Package; got != "@qwen-code/qwen-code" {
		t.Fatalf("Qwen package = %q, want @qwen-code/qwen-code", got)
	}
	if len(status.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(status.Agents))
	}
	if got := status.Agents[0].Command; got != "qwen" {
		t.Fatalf("Qwen command = %q, want qwen", got)
	}
	if got := installInstructions["qwen"]; got != "npm install -g @qwen-code/qwen-code" {
		t.Fatalf("qwen install instruction = %q", got)
	}
}

func TestInitialStatusUsesEmbeddedPiACPBridgeAndStillRequiresPiCLI(t *testing.T) {
	t.Parallel()

	status := InitialStatus([]config.AgentConfig{
		{
			ID:      "pi",
			Name:    "PI",
			Command: "npx",
			Args:    []string{"-y", config.PiACPPackageSpec},
		},
	})

	if len(status.ACPPackages) != 0 {
		t.Fatalf("ACPPackages = %#v, want no upstream package for embedded bridge", status.ACPPackages)
	}
	if len(status.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(status.Agents))
	}
	if got := status.Agents[0].Command; got != "pi" {
		t.Fatalf("PI command = %q, want pi", got)
	}
	if got := status.Agents[0].Package; got != config.PiCodingAgentPackageSpec {
		t.Fatalf("PI package = %q, want %s", got, config.PiCodingAgentPackageSpec)
	}
	if got := installInstructions["pi"]; got != "npm install -g "+config.PiCodingAgentPackageSpec {
		t.Fatalf("pi install instruction = %q", got)
	}
}

func TestInitialStatusKeepsCustomPiACPAsExternalPackage(t *testing.T) {
	t.Parallel()

	status := InitialStatus([]config.AgentConfig{{
		ID: "pi", Name: "Custom PI", Command: "npx", Args: []string{"-y", "pi-acp@custom"},
	}})
	if len(status.ACPPackages) != 1 || status.ACPPackages[0].Package != "pi-acp@custom" {
		t.Fatalf("ACPPackages = %#v", status.ACPPackages)
	}
}

func TestCommandSatisfiesSemver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}

	binDir := t.TempDir()
	command := filepath.Join(binDir, "fake-version")
	if err := os.WriteFile(command, []byte("#!/bin/sh\necho 'pi 0.80.3'\n"), 0755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if commandSatisfiesSemver(command, config.PiCodingAgentMinimumVersion) {
		t.Fatal("0.80.3 unexpectedly satisfied PI minimum")
	}
	if err := os.WriteFile(command, []byte("#!/bin/sh\necho 'pi 0.83.0'\n"), 0755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if !commandSatisfiesSemver(command, config.PiCodingAgentMinimumVersion) {
		t.Fatal("0.83.0 did not satisfy PI minimum")
	}
}

func TestPinnedPackageCacheRequiresExactVersion(t *testing.T) {
	t.Parallel()

	oldListing := []byte(`{"dependencies":{"pi-acp":{"version":"0.0.27"}}}`)
	newListing := []byte(`{"dependencies":{"pi-acp":{"version":"0.0.33"}}}`)
	if installedPackageVersion(oldListing, "pi-acp", "0.0.33") {
		t.Fatal("old pi-acp version matched pinned cache requirement")
	}
	if !installedPackageVersion(newListing, "pi-acp", "0.0.33") {
		t.Fatal("current pi-acp version did not match pinned cache requirement")
	}
	if got := exactPackageVersion("@scope/agent@1.2.3"); got != "1.2.3" {
		t.Fatalf("exactPackageVersion(scoped) = %q, want 1.2.3", got)
	}
}

func TestCompareSemver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		actual  string
		minimum string
		want    int
	}{
		{actual: "v22.19.0", minimum: "22.19.0", want: 0},
		{actual: "22.22.2", minimum: "22.19.0", want: 1},
		{actual: "22.18.9", minimum: "22.19.0", want: -1},
		{actual: "20.0.0", minimum: "22.19.0", want: -1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.actual, func(t *testing.T) {
			t.Parallel()
			got := compareSemver(tt.actual, tt.minimum)
			if got != tt.want {
				t.Fatalf("compareSemver(%q, %q) = %d, want %d", tt.actual, tt.minimum, got, tt.want)
			}
		})
	}
}
