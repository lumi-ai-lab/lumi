package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/requestercontext"
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

func TestPrepareWorkspaceRuntimeCreatesNpmDirs(t *testing.T) {
	runtimeDir := t.TempDir()
	manager := &Manager{runtimeDir: runtimeDir}

	got, err := manager.prepareWorkspaceRuntime("workspace-1")
	if err != nil {
		t.Fatalf("prepareWorkspaceRuntime() error = %v", err)
	}

	want := filepath.Join(runtimeDir, "shared", "runtime")
	if got != want {
		t.Fatalf("runtime path = %q, want %q", got, want)
	}
	for _, child := range []string{"npm/bin", "npm/lib/node_modules", "npm-cache"} {
		path := filepath.Join(got, child)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("os.Stat(%q) error = %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", path)
		}
	}
}

func TestPrepareWorkspaceRuntimeSeedsSharedRuntimeFromLargestLegacyRuntime(t *testing.T) {
	runtimeDir := t.TempDir()
	manager := &Manager{runtimeDir: runtimeDir}

	smallPkg := filepath.Join(runtimeDir, "sandboxes", "small", "runtime", "npm", "lib", "node_modules", "pkg")
	largePkg := filepath.Join(runtimeDir, "sandboxes", "large", "runtime", "npm", "lib", "node_modules", "pkg")
	if err := os.MkdirAll(smallPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smallPkg, "small.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(largePkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(largePkg, "large.bin"), []byte("larger"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := manager.prepareWorkspaceRuntime("small")
	if err != nil {
		t.Fatalf("prepareWorkspaceRuntime() error = %v", err)
	}

	if got != filepath.Join(runtimeDir, "shared", "runtime") {
		t.Fatalf("runtime path = %q", got)
	}
	if _, err := os.Stat(filepath.Join(got, "npm", "lib", "node_modules", "pkg", "large.bin")); err != nil {
		t.Fatalf("shared runtime was not seeded from largest legacy runtime: %v", err)
	}
}

func TestResolveRequesterContextContainerSettings(t *testing.T) {
	uid, primaryGID, readerGID := uint32(2001), uint32(2002), uint32(2003)
	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID:                "pi",
		RunAsUID:          &uid,
		RunAsGID:          &primaryGID,
		SupplementaryGIDs: []uint32{readerGID},
	}}}
	workspace := config.WorkspaceConfig{ID: "sandbox"}

	t.Run("legacy", func(t *testing.T) {
		t.Setenv(requestercontext.EnvRequesterContextRoot, "")
		t.Setenv(requestercontext.EnvRequesterContextReaderGID, "")
		settings, err := resolveRequesterContextContainerSettings(cfg, workspace)
		if err != nil || settings.Root != "" || settings.ReaderGID != nil {
			t.Fatalf("settings = %+v, error = %v", settings, err)
		}
	})
	t.Run("secure mapping", func(t *testing.T) {
		t.Setenv(requestercontext.EnvRequesterContextRoot, filepath.Join(t.TempDir(), "requester-context"))
		t.Setenv(requestercontext.EnvRequesterContextReaderGID, "2003")
		settings, err := resolveRequesterContextContainerSettings(cfg, workspace)
		if err != nil {
			t.Fatal(err)
		}
		if settings.Root != RequesterContextPath || settings.ReaderGID == nil || *settings.ReaderGID != readerGID {
			t.Fatalf("settings = %+v", settings)
		}
	})
	t.Run("partial host settings", func(t *testing.T) {
		t.Setenv(requestercontext.EnvRequesterContextRoot, filepath.Join(t.TempDir(), "requester-context"))
		t.Setenv(requestercontext.EnvRequesterContextReaderGID, "")
		_, err := resolveRequesterContextContainerSettings(cfg, workspace)
		if err == nil || !strings.Contains(err.Error(), "configured together") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("pi missing reader group", func(t *testing.T) {
		t.Setenv(requestercontext.EnvRequesterContextRoot, filepath.Join(t.TempDir(), "requester-context"))
		t.Setenv(requestercontext.EnvRequesterContextReaderGID, "2003")
		withoutReader := &config.Config{Agents: []config.AgentConfig{{ID: "pi", RunAsUID: &uid, RunAsGID: &primaryGID}}}
		_, err := resolveRequesterContextContainerSettings(withoutReader, workspace)
		if err == nil || !strings.Contains(err.Error(), "does not receive") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestDoEnsurePassesSecureRequesterContextToContainerSpec(t *testing.T) {
	uid, primaryGID, readerGID := uint32(2001), uint32(2002), uint32(2003)
	cfg := &config.Config{
		Agents: []config.AgentConfig{{
			ID:                "pi",
			Name:              "PI",
			Command:           "npx",
			RunAsUID:          &uid,
			RunAsGID:          &primaryGID,
			SupplementaryGIDs: []uint32{readerGID},
		}},
		DefaultAgent: "pi",
	}
	t.Setenv(requestercontext.EnvRequesterContextRoot, filepath.Join(t.TempDir(), "requester-context"))
	t.Setenv(requestercontext.EnvRequesterContextReaderGID, "2003")
	registry, err := device.NewRegistry(device.NewStore(filepath.Join(t.TempDir(), "devices.json")), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	fakeDocker := &fakeDockerClient{createErr: errors.New("stop after capturing container spec")}
	manager := &Manager{
		config:     cfg,
		devices:    registry,
		store:      NewStore(filepath.Join(t.TempDir(), "sandboxes.json")),
		docker:     fakeDocker,
		runtimeDir: t.TempDir(),
		runtimes:   make(map[string]*RuntimeRecord),
		ensures:    make(map[string]*ensureResult),
	}
	workspace := config.WorkspaceConfig{ID: "sandbox", Name: "Sandbox", Path: t.TempDir()}
	_, runtimeErr := manager.doEnsure(context.Background(), EnsureOptions{Workspace: workspace, BackendURL: "http://localhost:3000"})
	if runtimeErr == nil {
		t.Fatal("doEnsure() error = nil, want fake CreateContainer stop")
	}
	spec := fakeDocker.createdSpec
	if spec.RequesterContextRoot != RequesterContextPath || spec.RequesterContextReaderGID == nil || *spec.RequesterContextReaderGID != readerGID {
		t.Fatalf("captured ContainerSpec requester context = %q/%v", spec.RequesterContextRoot, spec.RequesterContextReaderGID)
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
	createdSpec     docker.ContainerSpec
	createErr       error
}

func (f *fakeDockerClient) Close() error {
	f.closed = true
	return nil
}

func (f *fakeDockerClient) CreateContainer(_ context.Context, spec docker.ContainerSpec) (string, error) {
	f.createdSpec = spec
	return "", f.createErr
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
