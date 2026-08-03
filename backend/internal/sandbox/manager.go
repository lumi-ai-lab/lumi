package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/piruntime"
	sandboxdocker "github.com/pengmide/lumi/internal/sandbox/docker"
)

type backendTarget struct {
	URL        string
	ExtraHosts []string
}

type ensureResult struct {
	done    chan struct{}
	runtime RuntimeState
	err     *RuntimeError
}

type Manager struct {
	config  *config.Config
	devices *device.Registry
	store   *Store
	docker  dockerClient

	runtimeDir string

	ssotApplier func(workspaceID, credentialsRoot string)

	mu        sync.Mutex
	runtimes  map[string]*RuntimeRecord
	ensures   map[string]*ensureResult
	runtimeMu sync.Mutex

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func NewManager(cfg *config.Config, devices *device.Registry) (*Manager, error) {
	dockerClient, err := sandboxdocker.NewClient()
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		config:     cfg,
		devices:    devices,
		store:      NewStore(""),
		docker:     dockerClient,
		runtimeDir: DefaultRuntimeDir(),
		runtimes:   make(map[string]*RuntimeRecord),
		ensures:    make(map[string]*ensureResult),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	if err := manager.recover(context.Background()); err != nil {
		log.Printf("sandbox recover failed: %v", err)
	}
	go manager.runScheduler()
	return manager, nil
}

func (m *Manager) Shutdown() error {
	return m.ShutdownPreserveContainers()
}

func (m *Manager) ShutdownPreserveContainers() error {
	m.stopOnce.Do(func() {
		close(m.stop)
	})
	if m.done != nil {
		<-m.done
	}
	return m.docker.Close()
}

func (m *Manager) recover(ctx context.Context) error {
	records, err := m.store.Load()
	if err != nil {
		return err
	}

	m.mu.Lock()
	for i := range records {
		record := records[i]
		recordCopy := record
		m.runtimes[record.WorkspaceID] = &recordCopy
	}
	m.mu.Unlock()

	containers, err := m.docker.ListSandboxContainers(ctx)
	if err != nil {
		return nil
	}

	knownWorkspaces := make(map[string]bool, len(records))
	for _, record := range records {
		knownWorkspaces[record.WorkspaceID] = true
	}

	for _, container := range containers {
		workspaceID := container.Labels[sandboxdocker.LabelWorkspaceID]
		if workspaceID == "" {
			continue
		}
		if !knownWorkspaces[workspaceID] {
			_ = m.docker.StopRemoveContainer(ctx, container.ID)
			continue
		}
		m.mu.Lock()
		record := m.runtimes[workspaceID]
		shouldRemove := record != nil && shouldRemoveRecoveredContainer(*record, time.Now().UnixMilli())
		if record != nil {
			record.ContainerName = firstContainerName(container.Names, record.ContainerName)
			if container.State == "running" && record.Status == "" {
				record.Status = StatusPending
			}
			if container.State != "running" && record.Status == StatusRunning {
				record.Status = StatusPending
			}
		}
		m.mu.Unlock()
		if shouldRemove {
			_ = m.docker.StopRemoveContainer(ctx, container.ID)
			m.markTerminated(workspaceID)
		}
	}

	m.mu.Lock()
	for _, record := range m.runtimes {
		if record.Status == "" {
			record.Status = StatusTerminated
		}
	}
	err = m.persistLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) Preflight(ctx context.Context, req PreflightRequest) PreflightResponse {
	path := strings.TrimSpace(req.Path)
	if path != "" {
		if err := validateHostPath(path); err != nil {
			return invalidPathError(path, err).Response()
		}
	}

	if err := m.docker.Ping(ctx); err != nil {
		return normalizeDockerError(err).Response()
	}

	if _, err := m.resolveBackendTarget("http://localhost:3000"); err != nil {
		return err.Response()
	}

	image := strings.TrimSpace(req.Image)
	if image == "" {
		image = DefaultImage
	}

	exists, err := m.docker.ImageExists(ctx, image)
	if err != nil {
		return normalizeDockerError(err).Response()
	}
	if exists {
		return PreflightResponse{
			OK:          true,
			Code:        CodeReady,
			Recoverable: true,
		}
	}
	if !req.CheckImagePull {
		return errorForCode(CodeImageMissing, fmt.Sprintf("image %s not found locally", image)).Response()
	}
	if err := m.docker.PullImage(ctx, image); err != nil {
		return wrapRuntimeError(CodeImagePullFailed, err).Response()
	}
	return PreflightResponse{
		OK:          true,
		Code:        CodeReady,
		Recoverable: true,
	}
}

func (m *Manager) Status(workspace config.WorkspaceConfig) RuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(workspace)
}

func (m *Manager) statusLocked(workspace config.WorkspaceConfig) RuntimeState {
	record := m.runtimes[workspace.ID]
	if record == nil {
		return RuntimeState{
			WorkspaceID:   workspace.ID,
			Image:         ResolveImage(workspace),
			HostPath:      workspace.Path,
			WorkspacePath: WorkspacePath,
			Status:        StatusTerminated,
		}
	}
	return *record
}

func (m *Manager) HasActiveRuntime() bool {
	return len(m.activeRuntimeWorkspaceIDs()) > 0
}

func (m *Manager) KeepAlive(workspace config.WorkspaceConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record := m.runtimes[workspace.ID]
	if record == nil {
		return
	}
	now := time.Now().UnixMilli()
	record.LastActivityAt = now
	record.ExpiresAt = now + int64(ResolveIdleTimeoutSec(workspace))*1000
	_ = m.persistLocked()
}

func (m *Manager) Ensure(ctx context.Context, opts EnsureOptions) (RuntimeState, *RuntimeError) {
	workspace := opts.Workspace
	if err := validateHostPath(workspace.Path); err != nil {
		return RuntimeState{}, invalidPathError(workspace.Path, err)
	}

	m.mu.Lock()
	if pending := m.ensures[workspace.ID]; pending != nil {
		m.mu.Unlock()
		<-pending.done
		return pending.runtime, pending.err
	}
	if record := m.runtimes[workspace.ID]; record != nil && record.Status == StatusRunning && m.runtimeHealthy(ctx, *record) {
		m.touchRecordLocked(record, workspace)
		snapshot := *record
		m.mu.Unlock()
		return snapshot, nil
	}
	waiter := &ensureResult{done: make(chan struct{})}
	m.ensures[workspace.ID] = waiter
	m.mu.Unlock()

	runtimeState, runtimeErr := m.doEnsure(ctx, opts)

	m.mu.Lock()
	waiter.runtime = runtimeState
	waiter.err = runtimeErr
	delete(m.ensures, workspace.ID)
	close(waiter.done)
	m.mu.Unlock()

	return runtimeState, runtimeErr
}

func (m *Manager) Warmup(ctx context.Context, opts EnsureOptions) RuntimeState {
	workspace := opts.Workspace
	if err := validateHostPath(workspace.Path); err != nil {
		m.failWorkspace(workspace, invalidPathError(workspace.Path, err), "")
		return m.Status(workspace)
	}

	m.mu.Lock()
	if record := m.runtimes[workspace.ID]; record != nil && record.Status == StatusRunning && m.runtimeHealthy(ctx, *record) {
		m.touchRecordLocked(record, workspace)
		snapshot := *record
		m.mu.Unlock()
		return snapshot
	}
	if pending := m.ensures[workspace.ID]; pending != nil {
		snapshot := m.statusLocked(workspace)
		m.mu.Unlock()
		return snapshot
	}
	record := m.getOrCreateRuntimeLocked(workspace)
	record.Status = StatusPending
	record.Stage = StageCheckingDocker
	record.ErrorCode = ""
	record.ErrorMessage = ""
	record.ErrorDetails = ""
	_ = m.persistLocked()
	snapshot := *record
	m.mu.Unlock()

	go func() {
		_, _ = m.Ensure(context.Background(), opts)
	}()

	return snapshot
}

func (m *Manager) doEnsure(ctx context.Context, opts EnsureOptions) (RuntimeState, *RuntimeError) {
	workspace := opts.Workspace
	requesterContextSettings, settingsErr := resolveRequesterContextContainerSettings(m.config, workspace)
	if settingsErr != nil {
		runtimeErr := wrapRuntimeError(CodeUnknown, settingsErr)
		m.failWorkspace(workspace, runtimeErr, StageStartingContainer)
		return m.Status(workspace), runtimeErr
	}
	target, targetErr := m.resolveBackendTarget(opts.BackendURL)
	if targetErr != nil {
		m.failWorkspace(workspace, targetErr, "")
		return m.Status(workspace), targetErr
	}

	record := m.getOrCreateRuntime(workspace)
	record.Stage = StageCheckingDocker
	record.Status = StatusPending
	if err := m.saveRuntime(record); err != nil {
		return RuntimeState{}, wrapRuntimeError(CodeUnknown, err)
	}

	if err := m.docker.Ping(ctx); err != nil {
		runtimeErr := normalizeDockerError(err)
		m.failWorkspace(workspace, runtimeErr, StageCheckingDocker)
		return m.Status(workspace), runtimeErr
	}

	record.Stage = StagePreparingImage
	if err := m.saveRuntime(record); err != nil {
		return RuntimeState{}, wrapRuntimeError(CodeUnknown, err)
	}

	exists, err := m.docker.ImageExists(ctx, record.Image)
	if err != nil {
		runtimeErr := normalizeDockerError(err)
		m.failWorkspace(workspace, runtimeErr, StagePreparingImage)
		return m.Status(workspace), runtimeErr
	}
	if !exists {
		if err := m.docker.PullImage(ctx, record.Image); err != nil {
			runtimeErr := wrapRuntimeError(CodeImagePullFailed, err)
			m.failWorkspace(workspace, runtimeErr, StagePreparingImage)
			return m.Status(workspace), runtimeErr
		}
	}

	record.Stage = StageStartingContainer
	if err := m.saveRuntime(record); err != nil {
		return RuntimeState{}, wrapRuntimeError(CodeUnknown, err)
	}

	configPath, err := m.writeExecutorConfig(workspace, *record)
	if err != nil {
		runtimeErr := wrapRuntimeError(CodeUnknown, err)
		m.failWorkspace(workspace, runtimeErr, StageStartingContainer)
		return m.Status(workspace), runtimeErr
	}
	runtimeHostPath, err := m.prepareWorkspaceRuntime(workspace.ID)
	if err != nil {
		runtimeErr := wrapRuntimeError(CodeUnknown, err)
		m.failWorkspace(workspace, runtimeErr, StageStartingContainer)
		return m.Status(workspace), runtimeErr
	}
	credentialMounts := m.resolveCredentialMounts(workspace.ID, filterAgents(m.config, workspace.Agents))

	_ = m.docker.StopRemoveContainer(ctx, record.ContainerName)
	containerID, err := m.docker.CreateContainer(ctx, sandboxdocker.ContainerSpec{
		Name:                      record.ContainerName,
		Image:                     record.Image,
		WorkspacePath:             record.HostPath,
		ConfigHostPath:            configPath,
		RuntimeHostPath:           runtimeHostPath,
		BackendURL:                target.URL,
		Token:                     m.devices.Secret(),
		Labels:                    sandboxdocker.BuildLabels(workspace.ID, record.DeviceID),
		ExtraHosts:                target.ExtraHosts,
		CredentialMounts:          credentialMounts,
		RequesterContextRoot:      requesterContextSettings.Root,
		RequesterContextReaderGID: requesterContextSettings.ReaderGID,
	})
	if err != nil {
		runtimeErr := normalizeDockerError(err)
		m.failWorkspace(workspace, runtimeErr, StageStartingContainer)
		return m.Status(workspace), runtimeErr
	}
	if err := m.docker.StartContainer(ctx, containerID); err != nil {
		runtimeErr := normalizeDockerError(err)
		m.failWorkspace(workspace, runtimeErr, StageStartingContainer)
		return m.Status(workspace), runtimeErr
	}

	record.Stage = StageBootstrapping
	record.StartedAt = time.Now().UnixMilli()
	if err := m.saveRuntime(record); err != nil {
		return RuntimeState{}, wrapRuntimeError(CodeUnknown, err)
	}

	if runtimeErr := m.waitForDevice(ctx, record.DeviceID); runtimeErr != nil {
		m.failWorkspace(workspace, runtimeErr, StageBootstrapping)
		return m.Status(workspace), runtimeErr
	}

	m.mu.Lock()
	record = m.runtimes[workspace.ID]
	if record == nil {
		record = m.getOrCreateRuntime(workspace)
	}
	record.Status = StatusRunning
	record.Stage = ""
	record.ErrorCode = ""
	record.ErrorMessage = ""
	record.ErrorDetails = ""
	m.touchRecordLocked(record, workspace)
	_ = m.persistLocked()
	snapshot := *record
	m.mu.Unlock()
	return snapshot, nil
}

func (m *Manager) TerminateAll(ctx context.Context) error {
	_, err := m.PruneAll(ctx)
	return err
}

func (m *Manager) PruneAll(ctx context.Context) ([]RuntimeRecord, error) {
	workspaceIDs := m.activeRuntimeWorkspaceIDs()
	pruned := make([]RuntimeRecord, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		if record, ok := m.runtimeRecord(workspaceID); ok {
			pruned = append(pruned, record)
		}
		if err := m.Terminate(ctx, workspaceID); err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

func (m *Manager) Terminate(ctx context.Context, workspaceID string) error {
	m.mu.Lock()
	record := m.runtimes[workspaceID]
	if record == nil {
		m.mu.Unlock()
		return nil
	}
	record.Status = StatusTerminating
	record.Stage = ""
	_ = m.persistLocked()
	containerName := record.ContainerName
	m.mu.Unlock()

	if strings.TrimSpace(containerName) != "" {
		if err := m.docker.StopRemoveContainer(ctx, containerName); err != nil {
			return err
		}
	}

	m.mu.Lock()
	record = m.runtimes[workspaceID]
	if record != nil {
		record.Status = StatusTerminated
		record.Stage = ""
		record.ErrorCode = ""
		record.ErrorMessage = ""
		record.ErrorDetails = ""
		record.StartedAt = 0
		record.ExpiresAt = 0
		record.LastActivityAt = 0
	}
	err := m.persistLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) activeRuntimeWorkspaceIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	workspaceIDs := make([]string, 0, len(m.runtimes))
	for workspaceID, record := range m.runtimes {
		if record == nil || record.Status == StatusTerminated {
			continue
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
	}
	sort.Strings(workspaceIDs)
	return workspaceIDs
}

func (m *Manager) runtimeRecord(workspaceID string) (RuntimeRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record := m.runtimes[workspaceID]
	if record == nil {
		return RuntimeRecord{}, false
	}
	return *record, true
}

func (m *Manager) markTerminated(workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record := m.runtimes[workspaceID]
	if record == nil {
		return
	}
	record.Status = StatusTerminated
	record.Stage = ""
	record.ErrorCode = ""
	record.ErrorMessage = ""
	record.ErrorDetails = ""
	record.StartedAt = 0
	record.ExpiresAt = 0
	record.LastActivityAt = 0
	_ = m.persistLocked()
}

func shouldRemoveRecoveredContainer(record RuntimeRecord, now int64) bool {
	if record.Status == StatusTerminated {
		return true
	}
	return record.Status == StatusRunning && record.ExpiresAt > 0 && record.ExpiresAt <= now
}

func (m *Manager) saveRuntime(record *RuntimeRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.runtimes[record.WorkspaceID] = record
	return m.persistLocked()
}

func (m *Manager) failWorkspace(workspace config.WorkspaceConfig, runtimeErr *RuntimeError, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	record := m.getOrCreateRuntimeLocked(workspace)
	record.Status = StatusFailed
	record.Stage = stage
	if runtimeErr != nil {
		record.ErrorCode = runtimeErr.Code
		record.ErrorMessage = runtimeErr.Message
		record.ErrorDetails = runtimeErr.Details
	}
	_ = m.persistLocked()
}

func (m *Manager) getOrCreateRuntime(workspace config.WorkspaceConfig) *RuntimeRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrCreateRuntimeLocked(workspace)
}

func (m *Manager) getOrCreateRuntimeLocked(workspace config.WorkspaceConfig) *RuntimeRecord {
	record := m.runtimes[workspace.ID]
	if record != nil {
		record.Image = ResolveImage(workspace)
		record.HostPath = workspace.Path
		record.WorkspacePath = WorkspacePath
		if record.ContainerName == "" {
			record.ContainerName = containerNameForWorkspace(workspace.ID)
		}
		if record.DeviceID == "" {
			record.DeviceID = deviceIDForWorkspace(workspace.ID)
		}
		if record.CreatedAt == 0 {
			record.CreatedAt = time.Now().UnixMilli()
		}
		return record
	}

	now := time.Now().UnixMilli()
	record = &RuntimeRecord{
		WorkspaceID:   workspace.ID,
		DeviceID:      deviceIDForWorkspace(workspace.ID),
		ContainerName: containerNameForWorkspace(workspace.ID),
		Image:         ResolveImage(workspace),
		HostPath:      workspace.Path,
		WorkspacePath: WorkspacePath,
		Status:        StatusTerminated,
		CreatedAt:     now,
	}
	m.runtimes[workspace.ID] = record
	return record
}

func (m *Manager) prepareWorkspaceRuntime(workspaceID string) (string, error) {
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()

	dir := filepath.Join(m.runtimeDir, "shared", "runtime")
	if err := m.seedSharedRuntimeFromLegacy(workspaceID, dir); err != nil {
		return "", err
	}
	for _, child := range []string{"npm/bin", "npm/lib/node_modules", "npm-cache"} {
		if err := os.MkdirAll(filepath.Join(dir, child), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (m *Manager) seedSharedRuntimeFromLegacy(workspaceID, sharedRuntimeDir string) error {
	if hasNPMModules(sharedRuntimeDir) {
		return nil
	}
	candidate := m.bestLegacyRuntime(workspaceID, sharedRuntimeDir)
	if candidate == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(sharedRuntimeDir), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(sharedRuntimeDir)
	if err := os.Rename(candidate, sharedRuntimeDir); err == nil {
		return nil
	}
	return nil
}

func (m *Manager) bestLegacyRuntime(workspaceID, sharedRuntimeDir string) string {
	sandboxRoot := filepath.Join(m.runtimeDir, "sandboxes")
	candidates := []string{}
	if strings.TrimSpace(workspaceID) != "" {
		candidates = append(candidates, filepath.Join(sandboxRoot, workspaceID, "runtime"))
	}
	entries, err := os.ReadDir(sandboxRoot)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidates = append(candidates, filepath.Join(sandboxRoot, entry.Name(), "runtime"))
		}
	}

	var best string
	var bestSize int64 = -1
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == sharedRuntimeDir || seen[candidate] || !hasNPMModules(candidate) {
			continue
		}
		seen[candidate] = true
		size := dirSize(candidate)
		if size > bestSize {
			best = candidate
			bestSize = size
		}
	}
	return best
}

func hasNPMModules(runtimeDir string) bool {
	entries, err := os.ReadDir(filepath.Join(runtimeDir, "npm", "lib", "node_modules"))
	return err == nil && len(entries) > 0
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func (m *Manager) writeExecutorConfig(workspace config.WorkspaceConfig, runtimeState RuntimeState) (string, error) {
	dir := filepath.Join(m.runtimeDir, "sandboxes", workspace.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	selectedAgents := filterAgents(m.config, workspace.Agents)
	credentialMounts := m.resolveCredentialMounts(workspace.ID, selectedAgents)
	agents := configureSandboxRunAsPiAgents(sanitizeAgentsForCredentialMounts(selectedAgents, credentialMounts))
	defaultAgent := m.config.DefaultAgent
	if !agentAllowed(agents, defaultAgent) && len(agents) > 0 {
		defaultAgent = agents[0].ID
	}

	payload := map[string]any{
		"deviceId":     runtimeState.DeviceID,
		"name":         "Sandbox " + workspace.Name,
		"workspace":    WorkspacePath,
		"workspaceId":  workspace.ID,
		"hidden":       true,
		"agents":       agents,
		"defaultAgent": defaultAgent,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}

	configPath := filepath.Join(dir, "config.json")
	return configPath, os.WriteFile(configPath, append(data, '\n'), 0o600)
}

func (m *Manager) resolveBackendTarget(rawURL string) (backendTarget, *RuntimeError) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return backendTarget{}, errorForCode(CodeHostConnectUnresolved, "backend URL is missing or invalid")
	}

	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		return backendTarget{}, errorForCode(CodeHostConnectUnresolved, "backend host is empty")
	}

	resolvedHost := host
	extraHosts := []string{}
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0":
		switch runtime.GOOS {
		case "darwin", "windows":
			resolvedHost = "host.docker.internal"
		case "linux":
			resolvedHost = "host.docker.internal"
			extraHosts = append(extraHosts, "host.docker.internal:host-gateway")
		default:
			return backendTarget{}, errorForCode(CodeHostConnectUnresolved, "unsupported host OS")
		}
	}

	if port != "" {
		parsed.Host = net.JoinHostPort(resolvedHost, port)
	} else {
		parsed.Host = resolvedHost
	}
	return backendTarget{URL: parsed.String(), ExtraHosts: extraHosts}, nil
}

func (m *Manager) touchRecordLocked(record *RuntimeRecord, workspace config.WorkspaceConfig) {
	now := time.Now().UnixMilli()
	record.LastActivityAt = now
	record.ExpiresAt = now + int64(ResolveIdleTimeoutSec(workspace))*1000
}

func (m *Manager) persistLocked() error {
	records := make([]RuntimeRecord, 0, len(m.runtimes))
	for _, record := range m.runtimes {
		records = append(records, *record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].WorkspaceID < records[j].WorkspaceID
	})
	return m.store.Save(records)
}

func validateHostPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required")
	}
	if !isAbsolutePath(path) {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	return nil
}

func isAbsolutePath(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return regexp.MustCompile(`^[A-Za-z]:[\\/].+`).MatchString(path)
}

func firstContainerName(names []string, fallback string) string {
	for _, name := range names {
		name = strings.TrimPrefix(name, "/")
		if strings.TrimSpace(name) != "" {
			return name
		}
	}
	return fallback
}

func containerNameForWorkspace(workspaceID string) string {
	slug := strings.ToLower(strings.TrimSpace(workspaceID))
	slug = regexp.MustCompile(`[^a-z0-9_.-]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "sandbox"
	}
	return "lumi-sandbox-" + slug
}

func deviceIDForWorkspace(workspaceID string) string {
	slug := strings.ToLower(strings.TrimSpace(workspaceID))
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "sandbox"
	}
	return "sandbox-" + slug
}

func filterAgents(cfg *config.Config, allowed []string) []config.AgentConfig {
	if len(allowed) == 0 {
		return append([]config.AgentConfig(nil), cfg.Agents...)
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = true
	}
	result := make([]config.AgentConfig, 0, len(cfg.Agents))
	for _, agentCfg := range cfg.Agents {
		if allowedSet[agentCfg.ID] {
			result = append(result, agentCfg)
		}
	}
	return result
}

func (m *Manager) resolveCredentialMounts(workspaceID string, agents []config.AgentConfig) []sandboxdocker.CredentialMount {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	dir := filepath.Join(m.runtimeDir, "sandboxes", workspaceID, "credentials")
	mounts := resolveCredentialMountsFromHomeForAgents(home, dir, agents)
	if m.ssotApplier != nil {
		m.ssotApplier(workspaceID, dir)
	}
	return mounts
}

// SetSSOTApplier registers a callback invoked after credential staging dirs
// have been prepared, allowing the API layer to drop SSOT skills + MCP
// configs into them. The callback receives the workspace id and the
// credentials root (e.g., <runtimeDir>/sandboxes/<id>/credentials/).
func (m *Manager) SetSSOTApplier(fn func(workspaceID, credentialsRoot string)) {
	m.ssotApplier = fn
}

// RunningWorkspaceIDs returns the workspace ids of every runtime currently
// in Running state. Callers use it to re-apply SSOT (skills + MCP) into
// already-running containers when records change.
func (m *Manager) RunningWorkspaceIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.runtimes))
	for id, record := range m.runtimes {
		if record != nil && record.Status == StatusRunning {
			out = append(out, id)
		}
	}
	return out
}

// CredentialsRoot returns the host-side staging dir for the given workspace
// (matches the directory passed to the SSOT applier). Empty string when no
// runtime is tracked.
func (m *Manager) CredentialsRoot(workspaceID string) string {
	if strings.TrimSpace(workspaceID) == "" {
		return ""
	}
	return filepath.Join(m.runtimeDir, "sandboxes", workspaceID, "credentials")
}

func resolveCredentialMountsFromHome(home string, runtimeDir string) []sandboxdocker.CredentialMount {
	return resolveCredentialMountsFromHomeForAgents(home, runtimeDir, nil)
}

func resolveCredentialMountsFromHomeForAgents(home string, runtimeDir string, agents []config.AgentConfig) []sandboxdocker.CredentialMount {
	mounts := make([]sandboxdocker.CredentialMount, 0, 3)
	seenTargets := make(map[string]bool, 2)
	if source, ok := prepareWritableClaudeRoot(home, filepath.Join(runtimeDir, "claude-root")); ok {
		seenTargets["/root"] = true
		mounts = append(mounts, sandboxdocker.CredentialMount{
			Source:   source,
			Target:   "/root",
			ReadOnly: false,
		})
	}

	if source, ok := prepareWritableCodexHome(home, filepath.Join(runtimeDir, "codex")); ok && !seenTargets["/root/.codex"] {
		mounts = append(mounts, sandboxdocker.CredentialMount{
			Source:   source,
			Target:   "/root/.codex",
			ReadOnly: false,
		})
	}

	if source, ok := prepareWritableQwenHome(home, filepath.Join(runtimeDir, "qwen")); ok {
		mounts = append(mounts, sandboxdocker.CredentialMount{
			Source:   source,
			Target:   "/root/.qwen",
			ReadOnly: false,
		})
	}
	if hasRunAsBuiltInPi(agents) {
		if source, ok := prepareRunAsPiCredentialSource(home, filepath.Join(runtimeDir, "pi-source")); ok {
			mounts = append(mounts, sandboxdocker.CredentialMount{
				Source:   source,
				Target:   piruntime.SandboxCredentialSource,
				ReadOnly: true,
			})
		}
	} else if source, ok := prepareWritablePiHome(home, filepath.Join(runtimeDir, "pi")); ok {
		mounts = append(mounts, sandboxdocker.CredentialMount{
			Source:   source,
			Target:   "/root/.pi",
			ReadOnly: false,
		})
	}
	return mounts
}

func hasRunAsBuiltInPi(agents []config.AgentConfig) bool {
	for _, agentCfg := range agents {
		if config.IsBuiltInPIACP(agentCfg) && agentCfg.RunAsUID != nil {
			return true
		}
	}
	return false
}

func resolveCredentialFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && strings.TrimSpace(resolved) != "" {
		return resolved, true
	}
	return path, true
}

func prepareWritableClaudeRoot(home string, targetDir string) (string, bool) {
	if err := os.MkdirAll(filepath.Join(targetDir, ".claude"), 0o700); err != nil {
		return "", false
	}
	for _, dir := range []string{".codex", ".config", ".npm", ".cache"} {
		_ = os.MkdirAll(filepath.Join(targetDir, dir), 0o700)
	}

	copied := false
	if copyCredentialFile(filepath.Join(home, ".claude.json"), filepath.Join(targetDir, ".claude.json")) {
		copied = true
	}
	for _, name := range []string{".credentials.json", "settings.json", "settings.local.json"} {
		if copyCredentialFile(filepath.Join(home, ".claude", name), filepath.Join(targetDir, ".claude", name)) {
			copied = true
		}
	}
	if !copied {
		return "", false
	}
	return targetDir, true
}

func prepareWritableCodexHome(home string, targetDir string) (string, bool) {
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", false
	}

	for _, name := range []string{"auth.json", "config.toml"} {
		_ = copyCredentialFile(filepath.Join(home, ".codex", name), filepath.Join(targetDir, name))
	}
	return targetDir, true
}

func prepareWritableQwenHome(home string, targetDir string) (string, bool) {
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", false
	}

	copied := false
	for _, name := range []string{"settings.json", ".env"} {
		if copyCredentialFile(filepath.Join(home, ".qwen", name), filepath.Join(targetDir, name)) {
			copied = true
		}
	}
	if !copied {
		return "", false
	}
	return targetDir, true
}

func prepareWritablePiHome(home string, targetDir string) (string, bool) {
	sourceDir := filepath.Join(home, ".pi")
	if info, err := os.Stat(sourceDir); err != nil || !info.IsDir() {
		return "", false
	}
	if err := copyDir(sourceDir, targetDir); err != nil {
		return "", false
	}
	return targetDir, true
}

func prepareRunAsPiCredentialSource(home string, targetDir string) (string, bool) {
	piTarget := filepath.Join(targetDir, ".pi")
	if err := os.MkdirAll(filepath.Join(piTarget, "agent", "sessions"), 0o700); err != nil {
		return "", false
	}
	sourceDir := filepath.Join(home, ".pi")
	if info, err := os.Stat(sourceDir); err == nil && info.IsDir() {
		if err := copyDir(sourceDir, piTarget); err != nil {
			return "", false
		}
	}
	return targetDir, true
}

func copyCredentialFile(sourcePath string, targetPath string) bool {
	source, ok := resolveCredentialFile(sourcePath)
	if !ok {
		return false
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return false
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		return false
	}
	return true
}

func copyDir(sourceDir, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil || rel == "." {
			return err
		}
		targetPath := filepath.Join(targetDir, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case d.Type()&os.ModeSymlink != 0:
			resolved, ok := resolveCredentialFile(path)
			if !ok {
				return nil
			}
			data, err := os.ReadFile(resolved)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
				return err
			}
			return os.WriteFile(targetPath, data, 0o600)
		case d.IsDir():
			return os.MkdirAll(targetPath, mode.Perm())
		case mode.IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
				return err
			}
			perm := mode.Perm()
			if perm == 0 {
				perm = 0o600
			}
			return os.WriteFile(targetPath, data, perm)
		default:
			return nil
		}
	})
}

func sanitizeAgentsForCredentialMounts(agents []config.AgentConfig, mounts []sandboxdocker.CredentialMount) []config.AgentConfig {
	if len(agents) == 0 || len(mounts) == 0 {
		return agents
	}

	hasClaudeAuth := hasClaudeCredentialMount(mounts)

	sanitized := make([]config.AgentConfig, len(agents))
	copy(sanitized, agents)
	for i := range sanitized {
		backend := agentmode.DetectBackend(sanitized[i].ID, sanitized[i].Command, sanitized[i].Args)
		switch backend {
		case agentmode.BackendClaude:
			if hasClaudeAuth {
				sanitized[i].Env = withoutCredentialEnv(sanitized[i].Env, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN")
			}
			sanitized[i].Env = withoutHostClaudeExecutableEnv(sanitized[i].Env)
		case agentmode.BackendCodex:
			// Keep explicit Codex API env from agent config. A mounted ~/.codex/auth.json
			// can be ChatGPT auth, while OPENAI_API_KEY/OPENAI_BASE_URL may intentionally
			// point Codex at an API-compatible provider.
		}
	}
	return sanitized
}

func configureSandboxRunAsPiAgents(agents []config.AgentConfig) []config.AgentConfig {
	configured := make([]config.AgentConfig, len(agents))
	copy(configured, agents)
	for i := range configured {
		if !config.IsBuiltInPIACP(configured[i]) || configured[i].RunAsUID == nil {
			continue
		}
		env := make(map[string]string, len(configured[i].Env)+4)
		for key, value := range configured[i].Env {
			env[key] = value
		}
		env["HOME"] = piruntime.SandboxHome
		env[piruntime.EnvPiAgentDir] = piruntime.SandboxAgentDir
		env[piruntime.EnvPiCredentialSource] = piruntime.SandboxCredentialSource
		if strings.TrimSpace(env[piruntime.EnvPiCommand]) == "" {
			env[piruntime.EnvPiCommand] = piruntime.SandboxPiCommand
		}
		configured[i].Env = env
	}
	return configured
}

func withoutHostClaudeExecutableEnv(env map[string]string) map[string]string {
	value := strings.TrimSpace(env["CLAUDE_CODE_EXECUTABLE"])
	if value == "" || value == "/lumi/runtime/npm/bin/claude" || value == "claude" {
		return env
	}
	if !filepath.IsAbs(value) {
		return env
	}
	return withoutCredentialEnv(env, "CLAUDE_CODE_EXECUTABLE")
}

func hasCredentialTarget(mounts []sandboxdocker.CredentialMount, target string) bool {
	for _, mount := range mounts {
		if mount.Target == target {
			return true
		}
	}
	return false
}

func hasClaudeCredentialMount(mounts []sandboxdocker.CredentialMount) bool {
	for _, mount := range mounts {
		switch mount.Target {
		case "/root", "/root/.claude.json", "/root/.claude", "/root/.claude/.credentials.json":
			return true
		}
	}
	return false
}

func withoutCredentialEnv(env map[string]string, keys ...string) map[string]string {
	if len(env) == 0 {
		return env
	}
	next := make(map[string]string, len(env))
	for key, value := range env {
		next[key] = value
	}
	for _, key := range keys {
		delete(next, key)
	}
	return next
}

func agentAllowed(agents []config.AgentConfig, agentID string) bool {
	for _, agentCfg := range agents {
		if agentCfg.ID == agentID {
			return true
		}
	}
	return false
}
