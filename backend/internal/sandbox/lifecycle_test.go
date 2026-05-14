package sandbox

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/pengmide/lumi/internal/sandbox/docker"
)

func TestActiveRuntimeWorkspaceIDsExcludesTerminated(t *testing.T) {
	manager := &Manager{
		runtimes: map[string]*RuntimeRecord{
			"running":     {WorkspaceID: "running", Status: StatusRunning},
			"pending":     {WorkspaceID: "pending", Status: StatusPending},
			"failed":      {WorkspaceID: "failed", Status: StatusFailed},
			"terminated":  {WorkspaceID: "terminated", Status: StatusTerminated},
			"terminating": {WorkspaceID: "terminating", Status: StatusTerminating},
		},
	}

	got := manager.activeRuntimeWorkspaceIDs()
	want := []string{"failed", "pending", "running", "terminating"}
	if len(got) != len(want) {
		t.Fatalf("activeRuntimeWorkspaceIDs() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("activeRuntimeWorkspaceIDs() = %#v, want %#v", got, want)
		}
	}
}

func TestShouldRemoveRecoveredContainer(t *testing.T) {
	now := int64(1000)

	tests := []struct {
		name   string
		record RuntimeRecord
		want   bool
	}{
		{
			name:   "terminated records should not keep containers",
			record: RuntimeRecord{Status: StatusTerminated},
			want:   true,
		},
		{
			name:   "expired running records are collected on startup",
			record: RuntimeRecord{Status: StatusRunning, ExpiresAt: now},
			want:   true,
		},
		{
			name:   "active running records are kept",
			record: RuntimeRecord{Status: StatusRunning, ExpiresAt: now + 1},
			want:   false,
		},
		{
			name:   "running records without expiry are kept",
			record: RuntimeRecord{Status: StatusRunning},
			want:   false,
		},
		{
			name:   "pending records are recovered for next ensure",
			record: RuntimeRecord{Status: StatusPending, ExpiresAt: now - 1},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRemoveRecoveredContainer(tt.record, now); got != tt.want {
				t.Fatalf("shouldRemoveRecoveredContainer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShutdownPreserveContainersStopsSchedulerAndClosesClient(t *testing.T) {
	fakeDocker := &fakeDockerClient{}
	manager := &Manager{
		docker: fakeDocker,
		store:  NewStore(t.TempDir() + "/sandboxes.json"),
		runtimes: map[string]*RuntimeRecord{
			"running": {WorkspaceID: "running", Status: StatusRunning, ContainerName: "lumi-running"},
		},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go manager.runScheduler()

	if err := manager.ShutdownPreserveContainers(); err != nil {
		t.Fatalf("ShutdownPreserveContainers() error = %v", err)
	}
	if !fakeDocker.closed {
		t.Fatal("docker client was not closed")
	}
	if fakeDocker.stopRemoveCalls != 0 {
		t.Fatalf("StopRemoveContainer calls = %d, want 0", fakeDocker.stopRemoveCalls)
	}
	if got := manager.runtimes["running"].Status; got != StatusRunning {
		t.Fatalf("runtime status = %q, want %q", got, StatusRunning)
	}
}

func TestShutdownPreservesContainers(t *testing.T) {
	fakeDocker := &fakeDockerClient{}
	manager := &Manager{
		docker: fakeDocker,
		store:  NewStore(t.TempDir() + "/sandboxes.json"),
		runtimes: map[string]*RuntimeRecord{
			"running": {WorkspaceID: "running", Status: StatusRunning, ContainerName: "lumi-running"},
		},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go manager.runScheduler()

	if err := manager.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if fakeDocker.stopRemoveCalls != 0 {
		t.Fatalf("StopRemoveContainer calls = %d, want 0", fakeDocker.stopRemoveCalls)
	}
}

func TestTerminateAllRemovesActiveRuntimesAndMarksTerminated(t *testing.T) {
	fakeDocker := &fakeDockerClient{}
	manager := &Manager{
		docker: fakeDocker,
		store:  NewStore(t.TempDir() + "/sandboxes.json"),
		runtimes: map[string]*RuntimeRecord{
			"running": {
				WorkspaceID:    "running",
				Status:         StatusRunning,
				ContainerName:  "lumi-running",
				StartedAt:      100,
				ExpiresAt:      200,
				LastActivityAt: 150,
			},
			"pending": {
				WorkspaceID:   "pending",
				Status:        StatusPending,
				ContainerName: "lumi-pending",
			},
			"terminated": {
				WorkspaceID:   "terminated",
				Status:        StatusTerminated,
				ContainerName: "lumi-terminated",
			},
		},
	}

	pruned, err := manager.PruneAll(context.Background())
	if err != nil {
		t.Fatalf("PruneAll() error = %v", err)
	}
	if fakeDocker.stopRemoveCalls != 2 {
		t.Fatalf("StopRemoveContainer calls = %d, want 2", fakeDocker.stopRemoveCalls)
	}
	if len(pruned) != 2 {
		t.Fatalf("pruned records = %d, want 2: %+v", len(pruned), pruned)
	}
	if pruned[0].WorkspaceID != "pending" || pruned[1].WorkspaceID != "running" {
		t.Fatalf("pruned records = %+v, want pending then running", pruned)
	}
	if pruned[1].StartedAt != 100 || pruned[1].ExpiresAt != 200 || pruned[1].LastActivityAt != 150 {
		t.Fatalf("running prune snapshot lost timestamps: %+v", pruned[1])
	}
	for _, workspaceID := range []string{"running", "pending"} {
		record := manager.runtimes[workspaceID]
		if record.Status != StatusTerminated {
			t.Fatalf("%s status = %q, want terminated", workspaceID, record.Status)
		}
		if record.StartedAt != 0 || record.ExpiresAt != 0 || record.LastActivityAt != 0 {
			t.Fatalf("%s timestamps not cleared: %+v", workspaceID, record)
		}
	}
}

type fakeDockerClient struct {
	closed          bool
	stopRemoveCalls int
}

func (f *fakeDockerClient) Close() error {
	f.closed = true
	return nil
}

func (f *fakeDockerClient) CreateContainer(context.Context, docker.ContainerSpec) (string, error) {
	return "", nil
}

func (f *fakeDockerClient) ImageExists(context.Context, string) (bool, error) {
	return true, nil
}

func (f *fakeDockerClient) InspectContainer(context.Context, string) (types.ContainerJSON, error) {
	return types.ContainerJSON{}, nil
}

func (f *fakeDockerClient) ListSandboxContainers(context.Context) ([]types.Container, error) {
	return nil, nil
}

func (f *fakeDockerClient) Ping(context.Context) error {
	return nil
}

func (f *fakeDockerClient) PullImage(context.Context, string) error {
	return nil
}

func (f *fakeDockerClient) StartContainer(context.Context, string) error {
	return nil
}

func (f *fakeDockerClient) StopRemoveContainer(context.Context, string) error {
	f.stopRemoveCalls++
	return nil
}
