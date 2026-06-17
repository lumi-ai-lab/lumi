package device

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/setupcheck"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	store := NewStore(filepath.Join(t.TempDir(), "devices.json"))
	registry, err := NewRegistry(store, "test-secret")
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func newTestConnection() *Connection {
	return NewConnection(nil)
}

func registerReadyTestDevice(t *testing.T, registry *Registry, deviceID string) {
	t.Helper()

	_, err := registry.RegisterDevice(newTestConnection(), DeviceRegisterPayload{
		DeviceID: deviceID,
		Name:     "Office Mac",
		Agents:   []DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	})
	if err != nil {
		t.Fatalf("RegisterDevice() error = %v", err)
	}
	if err := registry.UpdateSetupStatus(deviceID, setupcheck.SetupStatus{Ready: true}); err != nil {
		t.Fatalf("UpdateSetupStatus() error = %v", err)
	}
}

func waitForQueuedTask(t *testing.T, registry *Registry, deviceID string, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		registry.mu.RLock()
		got := len(registry.deviceTaskWaiters[deviceID])
		registry.mu.RUnlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}

	registry.mu.RLock()
	got := len(registry.deviceTaskWaiters[deviceID])
	registry.mu.RUnlock()
	t.Fatalf("queued tasks for %s = %d, want %d", deviceID, got, want)
}

func TestNewRegistryMarksPersistedDevicesOffline(t *testing.T) {
	t.Parallel()

	store := NewStore(filepath.Join(t.TempDir(), "devices.json"))
	err := store.Save([]Device{{
		ID:         "dev-1",
		Name:       "Office Mac",
		Status:     StatusOnline,
		SetupReady: true,
	}})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	registry, err := NewRegistry(store, "secret")
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	device, ok := registry.GetDevice("dev-1")
	if !ok {
		t.Fatalf("GetDevice() ok = false, want true")
	}
	if device.Status != StatusOffline {
		t.Fatalf("device.Status = %q, want %q", device.Status, StatusOffline)
	}
}

func TestRegisterDeviceAndSetupLifecycle(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	conn := newTestConnection()

	device, err := registry.RegisterDevice(conn, DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents: []DeviceAgentInfo{
			{ID: "claude", Name: "Claude Code"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterDevice() error = %v", err)
	}
	if device.Status != StatusSetupRequired {
		t.Fatalf("device.Status = %q, want %q", device.Status, StatusSetupRequired)
	}
	if device.SetupReady {
		t.Fatalf("device.SetupReady = true, want false")
	}

	err = registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{Ready: false})
	if err != nil {
		t.Fatalf("UpdateSetupStatus(false) error = %v", err)
	}
	device, _ = registry.GetDevice("dev-1")
	if device.Status != StatusSetupRequired {
		t.Fatalf("device.Status after not-ready = %q, want %q", device.Status, StatusSetupRequired)
	}

	err = registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{
		Ready:       true,
		Environment: []setupcheck.DependencyItem{{Name: "npm", Status: "ready"}},
	})
	if err != nil {
		t.Fatalf("UpdateSetupStatus(true) error = %v", err)
	}
	device, _ = registry.GetDevice("dev-1")
	if !device.SetupReady {
		t.Fatalf("device.SetupReady = false, want true")
	}
	if device.Status != StatusOnline {
		t.Fatalf("device.Status = %q, want %q", device.Status, StatusOnline)
	}
}

func TestTaskLifecycleAndMappings(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	_, err := registry.RegisterDevice(newTestConnection(), DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents:   []DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	})
	if err != nil {
		t.Fatalf("RegisterDevice() error = %v", err)
	}
	if err := registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{Ready: true}); err != nil {
		t.Fatalf("UpdateSetupStatus() error = %v", err)
	}

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task2); !errors.Is(err, ErrDeviceBusy) {
		t.Fatalf("StartTask(task2) error = %v, want %v", err, ErrDeviceBusy)
	}

	registry.setTaskSession(task1.ID, "session-1")
	registry.RegisterPermission("tool-1", task1.ID)

	if task, ok := registry.TaskBySession("session-1"); !ok || task.ID != task1.ID {
		t.Fatalf("TaskBySession() = (%v, %v), want task-1", task, ok)
	}
	if task, ok := registry.TaskByToolCall("tool-1"); !ok || task.ID != task1.ID {
		t.Fatalf("TaskByToolCall() = (%v, %v), want task-1", task, ok)
	}

	registry.FinishTask(task1.ID)
	if _, ok := registry.TaskBySession("session-1"); ok {
		t.Fatalf("TaskBySession() ok = true after FinishTask, want false")
	}
	if _, ok := registry.TaskByToolCall("tool-1"); ok {
		t.Fatalf("TaskByToolCall() ok = true after FinishTask, want false")
	}

	device, _ := registry.GetDevice("dev-1")
	if device.Status != StatusOnline {
		t.Fatalf("device.Status after FinishTask = %q, want %q", device.Status, StatusOnline)
	}
}

func TestWaitStartTaskStartsAfterCurrentTaskFinishes(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(context.Background(), task2, time.Second)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("WaitStartTask(task2) returned before task1 finished: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	registry.FinishTask(task1.ID)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("WaitStartTask(task2) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task2 to start")
	}

	device, _ := registry.GetDevice("dev-1")
	if len(device.RunningTaskIDs) != 1 || device.RunningTaskIDs[0] != task2.ID {
		t.Fatalf("RunningTaskIDs = %v, want [%s]", device.RunningTaskIDs, task2.ID)
	}
	registry.FinishTask(task2.ID)
}

func TestUpdateDeviceStatusOnlineReconcilesStaleCurrentTask(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}
	registry.setTaskSession(task1.ID, "session-1")
	registry.RegisterPermission("tool-1", task1.ID)

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(context.Background(), task2, time.Second)
	}()
	waitForQueuedTask(t, registry, "dev-1", 1)

	if err := registry.UpdateDeviceStatus("dev-1", StatusOnline); err != nil {
		t.Fatalf("UpdateDeviceStatus(online) error = %v", err)
	}

	select {
	case event := <-task1.Events:
		if event.Type != DeviceEventError || event.Err == nil {
			t.Fatalf("task1 event = %+v, want error", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task1 reconciliation error")
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("WaitStartTask(task2) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task2 to start")
	}
	if _, ok := registry.TaskBySession("session-1"); ok {
		t.Fatal("TaskBySession(session-1) ok = true after reconciliation, want false")
	}
	if _, ok := registry.TaskByToolCall("tool-1"); ok {
		t.Fatal("TaskByToolCall(tool-1) ok = true after reconciliation, want false")
	}

	registry.FinishTask(task1.ID)
	device, _ := registry.GetDevice("dev-1")
	if device.Status != StatusBusy || len(device.RunningTaskIDs) != 1 || device.RunningTaskIDs[0] != task2.ID {
		t.Fatalf("device after stale FinishTask = status:%s running:%v, want busy task2", device.Status, device.RunningTaskIDs)
	}
	registry.FinishTask(task2.ID)
}

func TestHeartbeatEmptyRunningTasksReconcilesStaleCurrentTask(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(context.Background(), task2, time.Second)
	}()
	waitForQueuedTask(t, registry, "dev-1", 1)

	if err := registry.Heartbeat("dev-1", nil); err != nil {
		t.Fatalf("Heartbeat(empty) error = %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("WaitStartTask(task2) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task2 to start")
	}
	device, _ := registry.GetDevice("dev-1")
	if device.Status != StatusBusy || len(device.RunningTaskIDs) != 1 || device.RunningTaskIDs[0] != task2.ID {
		t.Fatalf("device = status:%s running:%v, want busy task2", device.Status, device.RunningTaskIDs)
	}
	registry.FinishTask(task1.ID)
	registry.FinishTask(task2.ID)
}

func TestHeartbeatWithRunningTaskDoesNotReconcileCurrentTask(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(context.Background(), task2, time.Second)
	}()
	waitForQueuedTask(t, registry, "dev-1", 1)

	if err := registry.Heartbeat("dev-1", []string{task1.ID}); err != nil {
		t.Fatalf("Heartbeat(running task1) error = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("WaitStartTask(task2) returned before task1 finished: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	device, _ := registry.GetDevice("dev-1")
	if device.Status != StatusBusy || len(device.RunningTaskIDs) != 1 || device.RunningTaskIDs[0] != task1.ID {
		t.Fatalf("device = status:%s running:%v, want busy task1", device.Status, device.RunningTaskIDs)
	}

	registry.FinishTask(task1.ID)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("WaitStartTask(task2) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task2 to start after FinishTask")
	}
	registry.FinishTask(task2.ID)
}

func TestWaitStartTaskTimesOutWhileQueued(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	err := registry.WaitStartTask(context.Background(), task2, 25*time.Millisecond)
	if !errors.Is(err, ErrDeviceQueueTimeout) {
		t.Fatalf("WaitStartTask(task2) error = %v, want %v", err, ErrDeviceQueueTimeout)
	}

	registry.FinishTask(task1.ID)
}

func TestWaitStartTaskReturnsContextCancellationWhileQueued(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(waitCtx, task2, time.Second)
	}()

	waitForQueuedTask(t, registry, "dev-1", 1)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitStartTask(task2) error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for context cancellation")
	}

	registry.FinishTask(task1.ID)
}

func TestWaitStartTaskReturnsSetupNotReadyWhileQueued(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(context.Background(), task2, time.Second)
	}()

	waitForQueuedTask(t, registry, "dev-1", 1)
	if err := registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{Ready: false}); err != nil {
		t.Fatalf("UpdateSetupStatus(false) error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrSetupNotReady) {
			t.Fatalf("WaitStartTask(task2) error = %v, want %v", err, ErrSetupNotReady)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for setup-not-ready error")
	}

	registry.FinishTask(task1.ID)
}

func TestWaitStartTaskReturnsOfflineWhileQueued(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	registerReadyTestDevice(t, registry, "dev-1")

	task1 := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task1); err != nil {
		t.Fatalf("StartTask(task1) error = %v", err)
	}

	task2 := NewTaskRun("task-2", "dev-1", "conv-2", "claude", "ws-1", "/tmp/project")
	errCh := make(chan error, 1)
	go func() {
		errCh <- registry.WaitStartTask(context.Background(), task2, time.Second)
	}()

	waitForQueuedTask(t, registry, "dev-1", 1)
	registry.MarkDisconnected("dev-1", "offline")

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrDeviceOffline) {
			t.Fatalf("WaitStartTask(task2) error = %v, want %v", err, ErrDeviceOffline)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for offline error")
	}

	registry.FinishTask(task1.ID)
}

func TestReconnectFailsRunningTask(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	conn1 := newTestConnection()
	_, err := registry.RegisterDevice(conn1, DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents:   []DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	})
	if err != nil {
		t.Fatalf("RegisterDevice(conn1) error = %v", err)
	}
	if err := registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{Ready: true}); err != nil {
		t.Fatalf("UpdateSetupStatus() error = %v", err)
	}

	task := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task); err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}

	_, err = registry.RegisterDevice(newTestConnection(), DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents:   []DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	})
	if err != nil {
		t.Fatalf("RegisterDevice(conn2) error = %v", err)
	}

	event := <-task.Events
	if event.Type != DeviceEventError {
		t.Fatalf("event.Type = %q, want %q", event.Type, DeviceEventError)
	}
	if event.Err == nil || event.Err.Error() != "Device reconnected" {
		t.Fatalf("event.Err = %v, want Device reconnected", event.Err)
	}
}

func TestMarkDisconnectedConnectionIgnoresStaleConnection(t *testing.T) {
	t.Parallel()

	registry := newTestRegistry(t)
	conn1 := newTestConnection()
	if _, err := registry.RegisterDevice(conn1, DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents:   []DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	}); err != nil {
		t.Fatalf("RegisterDevice(conn1) error = %v", err)
	}
	if err := registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{Ready: true}); err != nil {
		t.Fatalf("UpdateSetupStatus(conn1) error = %v", err)
	}

	conn2 := newTestConnection()
	if _, err := registry.RegisterDevice(conn2, DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents:   []DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	}); err != nil {
		t.Fatalf("RegisterDevice(conn2) error = %v", err)
	}
	if err := registry.UpdateSetupStatus("dev-1", setupcheck.SetupStatus{Ready: true}); err != nil {
		t.Fatalf("UpdateSetupStatus(conn2) error = %v", err)
	}

	registry.MarkDisconnectedConnection("dev-1", conn1, "connection closed")

	device, ok := registry.GetDevice("dev-1")
	if !ok {
		t.Fatalf("GetDevice() ok = false, want true")
	}
	if device.Status != StatusOnline {
		t.Fatalf("device.Status after stale disconnect = %q, want %q", device.Status, StatusOnline)
	}

	task := NewTaskRun("task-1", "dev-1", "conv-1", "claude", "ws-1", "/tmp/project")
	if err := registry.StartTask(task); err != nil {
		t.Fatalf("StartTask() after stale disconnect error = %v", err)
	}
}
