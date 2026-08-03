package docker

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestContainerEnvironmentRequesterContextPairing(t *testing.T) {
	legacy, err := containerEnvironment(ContainerSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 4 {
		t.Fatalf("legacy env = %#v, want four base variables", legacy)
	}
	for _, entry := range legacy {
		if strings.HasPrefix(entry, "LUMI_REQUESTER_CONTEXT_") {
			t.Fatalf("legacy env contains secure setting %q", entry)
		}
	}

	gid := uint32(2003)
	hostPath := filepath.Join(t.TempDir(), "requester-context")
	secure, err := containerEnvironment(ContainerSpec{
		RequesterContextHostPath:  hostPath,
		RequesterContextRoot:      "/run/lumi/requester-context",
		RequesterContextReaderGID: &gid,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LUMI_REQUESTER_CONTEXT_ROOT=/run/lumi/requester-context",
		"LUMI_REQUESTER_CONTEXT_READER_GID=2003",
	} {
		if !slices.Contains(secure, want) {
			t.Fatalf("secure env = %#v, missing %q", secure, want)
		}
	}
}

func TestContainerEnvironmentRejectsPartialOrHostRequesterRoot(t *testing.T) {
	gid := uint32(2003)
	zeroGID := uint32(0)
	hostPath := filepath.Join(t.TempDir(), "requester-context")
	tests := []ContainerSpec{
		{RequesterContextHostPath: hostPath},
		{RequesterContextRoot: "/run/lumi/requester-context"},
		{RequesterContextReaderGID: &gid},
		{RequesterContextRoot: "/run/lumi/requester-context", RequesterContextReaderGID: &gid},
		{RequesterContextHostPath: hostPath, RequesterContextRoot: "/lumi/runtime/requester-context", RequesterContextReaderGID: &gid},
		{RequesterContextHostPath: filepath.Join(t.TempDir(), "wrong-name"), RequesterContextRoot: "/run/lumi/requester-context", RequesterContextReaderGID: &gid},
		{RequesterContextHostPath: hostPath, RequesterContextRoot: "/run/lumi/requester-context", RequesterContextReaderGID: &zeroGID},
	}
	for _, spec := range tests {
		if _, err := containerEnvironment(spec); err == nil {
			t.Fatalf("containerEnvironment(%+v) error = nil", spec)
		}
	}
}

func TestContainerMountsIsolateRequesterContextFromSharedRuntime(t *testing.T) {
	gid := uint32(2003)
	hostPath := filepath.Join(t.TempDir(), "requester-context")
	runtimePath := filepath.Join(t.TempDir(), "runtime")
	mounts, err := containerMounts(ContainerSpec{
		WorkspacePath:             t.TempDir(),
		ConfigHostPath:            filepath.Join(t.TempDir(), "config.json"),
		RuntimeHostPath:           runtimePath,
		RequesterContextHostPath:  hostPath,
		RequesterContextRoot:      "/run/lumi/requester-context",
		RequesterContextReaderGID: &gid,
	})
	if err != nil {
		t.Fatal(err)
	}

	var runtimeSource, requesterSource string
	for _, mounted := range mounts {
		switch mounted.Target {
		case "/lumi/runtime":
			runtimeSource = mounted.Source
		case "/run/lumi/requester-context":
			requesterSource = mounted.Source
			if mounted.ReadOnly {
				t.Fatal("requester-context mount must remain writable by device-executor publisher")
			}
		}
	}
	if runtimeSource != runtimePath || requesterSource != hostPath {
		t.Fatalf("mount sources runtime=%q requester-context=%q", runtimeSource, requesterSource)
	}
	if requesterSource == runtimeSource || strings.HasPrefix(requesterSource, runtimeSource+string(filepath.Separator)) {
		t.Fatal("requester-context mount source must be independent from the shared Agent runtime")
	}
}
