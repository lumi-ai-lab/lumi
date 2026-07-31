package workspacecli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMetricCLIPathFindsExecutableWorkspaceCLI(t *testing.T) {
	workspace := t.TempDir()
	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "qdm-metric-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	metricCLI := filepath.Join(binDir, name)
	if err := os.WriteFile(metricCLI, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := MetricCLIPath(workspace)
	if !ok || got != metricCLI {
		t.Fatalf("MetricCLIPath() = %q, %v, want %q, true", got, ok, metricCLI)
	}
}

func TestMetricCLIPathRejectsMissingAndNonExecutableCLI(t *testing.T) {
	workspace := t.TempDir()
	if got, ok := MetricCLIPath(workspace); ok || got != "" {
		t.Fatalf("MetricCLIPath() = %q, %v, want empty, false", got, ok)
	}
	if runtime.GOOS == "windows" {
		return
	}

	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metricCLI := filepath.Join(binDir, "qdm-metric-cli")
	if err := os.WriteFile(metricCLI, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := MetricCLIPath(workspace); ok || got != "" {
		t.Fatalf("MetricCLIPath() = %q, %v for non-executable CLI, want empty, false", got, ok)
	}
}

func TestPathHelpersPreserveOrderAndAvoidDuplicates(t *testing.T) {
	separator := string(os.PathListSeparator)
	current := "/usr/bin" + separator + "/bin"
	dir := "/workspace/bin"

	got := PrependPath(current, dir)
	want := dir + separator + current
	if got != want {
		t.Fatalf("PrependPath() = %q, want %q", got, want)
	}
	if duplicate := PrependPath(got, dir); duplicate != want {
		t.Fatalf("PrependPath() duplicate = %q, want %q", duplicate, want)
	}
	if removed := RemovePath(got, dir); removed != current {
		t.Fatalf("RemovePath() = %q, want %q", removed, current)
	}
}
