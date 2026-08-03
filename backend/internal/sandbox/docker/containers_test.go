package docker

import (
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
	secure, err := containerEnvironment(ContainerSpec{
		RequesterContextRoot:      "/lumi/runtime/requester-context",
		RequesterContextReaderGID: &gid,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LUMI_REQUESTER_CONTEXT_ROOT=/lumi/runtime/requester-context",
		"LUMI_REQUESTER_CONTEXT_READER_GID=2003",
	} {
		if !slices.Contains(secure, want) {
			t.Fatalf("secure env = %#v, missing %q", secure, want)
		}
	}
}

func TestContainerEnvironmentRejectsPartialOrHostRequesterRoot(t *testing.T) {
	gid := uint32(2003)
	tests := []ContainerSpec{
		{RequesterContextRoot: "/lumi/runtime/requester-context"},
		{RequesterContextReaderGID: &gid},
		{RequesterContextRoot: "/run/lumi/requester-context", RequesterContextReaderGID: &gid},
	}
	for _, spec := range tests {
		if _, err := containerEnvironment(spec); err == nil {
			t.Fatalf("containerEnvironment(%+v) error = nil", spec)
		}
	}
}
