package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/setupcheck"
)

const (
	StatusSetupRequired = "setup_required"
	StatusOnline        = "online"
	StatusOffline       = "offline"
	StatusBusy          = "busy"
	StatusError         = "error"
)

var (
	ErrDeviceNotFound      = errors.New("device not found")
	ErrDeviceOffline       = errors.New("device is offline")
	ErrSetupNotReady       = errors.New("device setup is not ready")
	ErrDeviceBusy          = errors.New("device is busy")
	ErrDeviceQueueTimeout  = errors.New("device task queue wait timed out")
	ErrTaskNotFound        = errors.New("task not found")
	ErrTaskEventBufferFull = errors.New("task event buffer full")
)

type DeviceAgentInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Device struct {
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	Alias          string                  `json:"alias,omitempty"`
	Hidden         bool                    `json:"hidden,omitempty"`
	Status         string                  `json:"status"`
	SetupReady     bool                    `json:"setupReady"`
	SetupStatus    *setupcheck.SetupStatus `json:"setupStatus,omitempty"`
	DefaultAgentID string                  `json:"defaultAgentId,omitempty"`
	Agents         []DeviceAgentInfo       `json:"agents,omitempty"`
	WorkspaceID    string                  `json:"workspaceId,omitempty"`
	Version        string                  `json:"version,omitempty"`
	LastHeartbeat  int64                   `json:"lastHeartbeat"`
	RegisteredAt   int64                   `json:"registeredAt"`
	UpdatedAt      int64                   `json:"updatedAt"`
	RunningTaskIDs []string                `json:"runningTaskIds,omitempty"`
}

type TaskRun struct {
	ID             string
	DeviceID       string
	ConversationID string
	AgentID        string
	SessionID      string
	WorkspaceID    string
	WorkspacePath  string
	StartedAt      int64

	Events chan DeviceEvent
	Done   chan struct{}
}

type DeviceEventType string

const (
	DeviceEventSession           DeviceEventType = "session"
	DeviceEventNotification      DeviceEventType = "notification"
	DeviceEventPermissionRequest DeviceEventType = "permission_request"
	DeviceEventDone              DeviceEventType = "done"
	DeviceEventError             DeviceEventType = "error"
)

type DeviceEvent struct {
	Type    DeviceEventType
	TaskID  string
	Payload json.RawMessage
	Err     error
}

type taskWaiter struct{}

type Registry struct {
	store  *Store
	secret string

	mu                 sync.RWMutex
	taskCond           *sync.Cond
	onDeviceReset      func(string)
	onDeviceRegistered func(string)
	devices            map[string]*Device
	conns              map[string]*Connection
	tasks              map[string]*TaskRun
	sessionToTask      map[string]string
	toolCallToTask     map[string]string
	deviceCurrentTask  map[string]string
	deviceTaskWaiters  map[string][]*taskWaiter
}

func NewRegistry(store *Store, secret string) (*Registry, error) {
	if store == nil {
		return nil, errors.New("device store is required")
	}
	if secret == "" {
		return nil, errors.New("device secret is required")
	}

	devices, err := store.Load()
	if err != nil {
		return nil, err
	}

	registry := &Registry{
		store:             store,
		secret:            secret,
		devices:           make(map[string]*Device),
		conns:             make(map[string]*Connection),
		tasks:             make(map[string]*TaskRun),
		sessionToTask:     make(map[string]string),
		toolCallToTask:    make(map[string]string),
		deviceCurrentTask: make(map[string]string),
		deviceTaskWaiters: make(map[string][]*taskWaiter),
	}
	registry.taskCond = sync.NewCond(&registry.mu)

	for i := range devices {
		device := devices[i]
		device.Status = StatusOffline
		device.RunningTaskIDs = nil
		registry.devices[device.ID] = &device
	}
	if err := registry.persistLocked(); err != nil {
		return nil, err
	}

	go registry.monitorHeartbeats()
	return registry, nil
}

func NewTaskRun(id, deviceID, conversationID, agentID, workspaceID, workspacePath string) *TaskRun {
	return &TaskRun{
		ID:             id,
		DeviceID:       deviceID,
		ConversationID: conversationID,
		AgentID:        agentID,
		WorkspaceID:    workspaceID,
		WorkspacePath:  workspacePath,
		StartedAt:      time.Now().UnixMilli(),
		Events:         make(chan DeviceEvent, 64),
		Done:           make(chan struct{}),
	}
}

func (r *Registry) SetDeviceResetHook(fn func(string)) {
	r.mu.Lock()
	r.onDeviceReset = fn
	r.mu.Unlock()
}

// SetDeviceRegisteredHook installs a callback fired every time a device
// completes registration. The API layer uses it to push current SSOT state
// to freshly-connected devices.
func (r *Registry) SetDeviceRegisteredHook(fn func(string)) {
	r.mu.Lock()
	r.onDeviceRegistered = fn
	r.mu.Unlock()
}

func (r *Registry) ListDevices() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()

	devices := make([]Device, 0, len(r.devices))
	for _, device := range r.devices {
		devices = append(devices, cloneDevice(*device))
	}

	sort.Slice(devices, func(i, j int) bool {
		if devices[i].RegisteredAt == devices[j].RegisteredAt {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].RegisteredAt < devices[j].RegisteredAt
	})
	return devices
}

func (r *Registry) GetDevice(id string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	device, ok := r.devices[id]
	if !ok {
		return Device{}, false
	}
	return cloneDevice(*device), true
}

func (r *Registry) RegisterDevice(conn *Connection, payload DeviceRegisterPayload) (Device, error) {
	if strings.TrimSpace(payload.DeviceID) == "" {
		return Device{}, errors.New("deviceId is required")
	}
	if strings.TrimSpace(payload.Name) == "" {
		return Device{}, errors.New("name is required")
	}

	r.mu.Lock()
	notifyReset := false

	if old := r.conns[payload.DeviceID]; old != nil && old != conn {
		old.Close("device reconnected")
		if taskID := r.deviceCurrentTask[payload.DeviceID]; taskID != "" {
			r.failTaskLocked(taskID, "Device reconnected")
			r.releaseTaskRoutingLocked(taskID)
		}
		notifyReset = true
	}

	now := time.Now().UnixMilli()
	device := r.devices[payload.DeviceID]
	if device == nil {
		device = &Device{
			ID:           payload.DeviceID,
			RegisteredAt: now,
		}
		r.devices[payload.DeviceID] = device
	}

	device.Name = payload.Name
	device.Hidden = payload.Hidden
	if payload.DefaultAgentID == "" && len(payload.Agents) > 0 {
		payload.DefaultAgentID = payload.Agents[0].ID
	}
	device.DefaultAgentID = payload.DefaultAgentID
	device.Agents = append([]DeviceAgentInfo(nil), payload.Agents...)
	device.WorkspaceID = payload.WorkspaceID
	device.Version = payload.Version
	device.Status = StatusSetupRequired
	device.SetupReady = false
	device.LastHeartbeat = now
	device.UpdatedAt = now
	device.RunningTaskIDs = nil

	conn.DeviceID = payload.DeviceID
	r.conns[payload.DeviceID] = conn

	if err := r.persistLocked(); err != nil {
		r.mu.Unlock()
		return Device{}, err
	}
	cloned := cloneDevice(*device)
	resetHook := r.onDeviceReset
	registeredHook := r.onDeviceRegistered
	r.taskCond.Broadcast()
	r.mu.Unlock()

	if notifyReset && resetHook != nil {
		resetHook(payload.DeviceID)
	}
	if registeredHook != nil {
		go registeredHook(payload.DeviceID)
	}
	return cloned, nil
}

func (r *Registry) Secret() string {
	return r.secret
}

func (r *Registry) UpdateSetupStatus(deviceID string, status setupcheck.SetupStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	device := r.devices[deviceID]
	if device == nil {
		return ErrDeviceNotFound
	}

	statusCopy := cloneSetupStatus(status)
	device.SetupStatus = &statusCopy
	device.SetupReady = status.Ready
	device.UpdatedAt = time.Now().UnixMilli()

	switch {
	case !status.Ready:
		device.Status = StatusSetupRequired
	case r.deviceCurrentTask[deviceID] != "":
		device.Status = StatusBusy
	default:
		device.Status = StatusOnline
	}

	err := r.persistLocked()
	r.taskCond.Broadcast()
	return err
}

func (r *Registry) UpdateDeviceStatus(deviceID string, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	device := r.devices[deviceID]
	if device == nil {
		return ErrDeviceNotFound
	}

	switch status {
	case StatusOnline:
		if !device.SetupReady {
			device.Status = StatusSetupRequired
		} else if r.deviceCurrentTask[deviceID] != "" {
			device.Status = StatusBusy
		} else {
			device.Status = StatusOnline
		}
	case StatusSetupRequired:
		device.Status = StatusSetupRequired
	case StatusBusy:
		device.Status = StatusBusy
	case StatusError:
		device.Status = StatusError
	default:
		return fmt.Errorf("unsupported device status: %s", status)
	}

	device.UpdatedAt = time.Now().UnixMilli()
	err := r.persistLocked()
	r.taskCond.Broadcast()
	return err
}

func (r *Registry) Heartbeat(deviceID string, runningTaskIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	device := r.devices[deviceID]
	if device == nil {
		return ErrDeviceNotFound
	}

	now := time.Now().UnixMilli()
	device.LastHeartbeat = now
	device.UpdatedAt = now
	device.RunningTaskIDs = append([]string(nil), runningTaskIDs...)

	switch {
	case !device.SetupReady:
		device.Status = StatusSetupRequired
	case len(runningTaskIDs) > 0 || r.deviceCurrentTask[deviceID] != "":
		device.Status = StatusBusy
	default:
		device.Status = StatusOnline
	}

	return nil
}

func (r *Registry) MarkDisconnected(deviceID string, reason string) {
	r.markDisconnected(deviceID, nil, reason)
}

func (r *Registry) MarkDisconnectedConnection(deviceID string, conn *Connection, reason string) {
	r.markDisconnected(deviceID, conn, reason)
}

func (r *Registry) markDisconnected(deviceID string, expectedConn *Connection, reason string) {
	r.mu.Lock()

	conn := r.conns[deviceID]
	if expectedConn != nil && conn != expectedConn {
		r.mu.Unlock()
		return
	}
	delete(r.conns, deviceID)
	if conn != nil {
		conn.Close(reason)
	}

	device := r.devices[deviceID]
	if device != nil {
		device.Status = StatusOffline
		device.RunningTaskIDs = nil
		device.UpdatedAt = time.Now().UnixMilli()
	}

	if taskID := r.deviceCurrentTask[deviceID]; taskID != "" {
		if reason == "device reconnected" {
			r.failTaskLocked(taskID, "Device reconnected")
		} else {
			r.failTaskLocked(taskID, "Device disconnected")
		}
		r.releaseTaskRoutingLocked(taskID)
	}

	resetHook := r.onDeviceReset
	_ = r.persistLocked()
	r.taskCond.Broadcast()
	r.mu.Unlock()

	if resetHook != nil {
		resetHook(deviceID)
	}
}

func (r *Registry) StartTask(task *TaskRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.startTaskLocked(task)
}

func (r *Registry) WaitStartTask(ctx context.Context, task *TaskRun, maxWait time.Duration) error {
	if task == nil {
		return errors.New("task is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var timeout <-chan time.Time
	var timer *time.Timer
	if maxWait > 0 {
		timer = time.NewTimer(maxWait)
		timeout = timer.C
		defer timer.Stop()
	}

	wakeDone := make(chan struct{})
	timedOut := false
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.taskCond.Broadcast()
			r.mu.Unlock()
		case <-timeout:
			r.mu.Lock()
			timedOut = true
			r.taskCond.Broadcast()
			r.mu.Unlock()
		case <-wakeDone:
		}
	}()
	defer close(wakeDone)

	waiter := &taskWaiter{}
	queued := false

	r.mu.Lock()
	defer r.mu.Unlock()

	for {
		if timedOut {
			if queued {
				r.removeTaskWaiterLocked(task.DeviceID, waiter)
				r.taskCond.Broadcast()
			}
			return ErrDeviceQueueTimeout
		}
		if err := ctx.Err(); err != nil {
			if queued {
				r.removeTaskWaiterLocked(task.DeviceID, waiter)
				r.taskCond.Broadcast()
			}
			return err
		}

		if !queued && len(r.deviceTaskWaiters[task.DeviceID]) == 0 {
			err := r.startTaskLocked(task)
			if err == nil {
				return nil
			}
			if !errors.Is(err, ErrDeviceBusy) {
				return err
			}
		}

		if !queued {
			r.deviceTaskWaiters[task.DeviceID] = append(r.deviceTaskWaiters[task.DeviceID], waiter)
			queued = true
		}

		if r.isFirstTaskWaiterLocked(task.DeviceID, waiter) {
			err := r.startTaskLocked(task)
			if err == nil {
				r.removeTaskWaiterLocked(task.DeviceID, waiter)
				return nil
			}
			if !errors.Is(err, ErrDeviceBusy) {
				r.removeTaskWaiterLocked(task.DeviceID, waiter)
				r.taskCond.Broadcast()
				return err
			}
		}

		r.taskCond.Wait()
	}
}

func (r *Registry) startTaskLocked(task *TaskRun) error {
	if task == nil {
		return errors.New("task is required")
	}

	device := r.devices[task.DeviceID]
	if device == nil {
		return ErrDeviceNotFound
	}
	if device.Status == StatusOffline || r.conns[task.DeviceID] == nil {
		return ErrDeviceOffline
	}
	if !device.SetupReady {
		return ErrSetupNotReady
	}
	if r.deviceCurrentTask[task.DeviceID] != "" {
		return ErrDeviceBusy
	}

	r.tasks[task.ID] = task
	r.deviceCurrentTask[task.DeviceID] = task.ID
	device.Status = StatusBusy
	device.RunningTaskIDs = []string{task.ID}
	device.UpdatedAt = time.Now().UnixMilli()
	return nil
}

func (r *Registry) isFirstTaskWaiterLocked(deviceID string, waiter *taskWaiter) bool {
	queue := r.deviceTaskWaiters[deviceID]
	return len(queue) > 0 && queue[0] == waiter
}

func (r *Registry) removeTaskWaiterLocked(deviceID string, waiter *taskWaiter) bool {
	queue := r.deviceTaskWaiters[deviceID]
	for i, queued := range queue {
		if queued != waiter {
			continue
		}
		copy(queue[i:], queue[i+1:])
		queue[len(queue)-1] = nil
		queue = queue[:len(queue)-1]
		if len(queue) == 0 {
			delete(r.deviceTaskWaiters, deviceID)
		} else {
			r.deviceTaskWaiters[deviceID] = queue
		}
		return true
	}
	return false
}

func (r *Registry) FinishTask(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	task := r.tasks[taskID]
	if task == nil {
		return
	}

	delete(r.tasks, taskID)
	if task.SessionID != "" {
		delete(r.sessionToTask, task.SessionID)
	}
	delete(r.deviceCurrentTask, task.DeviceID)

	for toolCallID, mappedTaskID := range r.toolCallToTask {
		if mappedTaskID == taskID {
			delete(r.toolCallToTask, toolCallID)
		}
	}

	if device := r.devices[task.DeviceID]; device != nil {
		device.RunningTaskIDs = nil
		device.UpdatedAt = time.Now().UnixMilli()
		switch {
		case r.conns[task.DeviceID] == nil:
			device.Status = StatusOffline
		case !device.SetupReady:
			device.Status = StatusSetupRequired
		default:
			device.Status = StatusOnline
		}
	}

	select {
	case <-task.Done:
	default:
		close(task.Done)
	}
	r.taskCond.Broadcast()
}

func (r *Registry) SendToDevice(ctx context.Context, deviceID string, typ MessageType, taskID string, payload any) error {
	r.mu.RLock()
	conn := r.conns[deviceID]
	r.mu.RUnlock()
	if conn == nil {
		return ErrDeviceOffline
	}

	messageID := newMessageID()
	waiter := conn.expectResponse(messageID)

	env, err := NewEnvelope(typ, messageID, deviceID, taskID, payload)
	if err != nil {
		return err
	}
	if err := conn.Send(ctx, env); err != nil {
		return err
	}

	timeout := 15 * time.Second
	if typ == MsgSetupCheck {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case response, ok := <-waiter:
		if !ok {
			return ErrDeviceOffline
		}
		if response.Type == MsgError {
			payload, err := DecodePayload[ErrorPayload](response)
			if err != nil {
				return errors.New("device returned an error")
			}
			return fmt.Errorf("%s", payload.Message)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("%s ack timed out", typ)
	case <-ctx.Done():
		return ctx.Err()
	case <-conn.closed:
		return ErrDeviceOffline
	}
}

func (r *Registry) SendWorkspaceRequest(ctx context.Context, deviceID string, typ MessageType, payload WorkspaceRequestPayload) (json.RawMessage, error) {
	r.mu.RLock()
	conn := r.conns[deviceID]
	dev := r.devices[deviceID]
	r.mu.RUnlock()
	if dev == nil {
		return nil, ErrDeviceNotFound
	}
	if conn == nil || dev.Status == StatusOffline {
		return nil, ErrDeviceOffline
	}
	if !dev.SetupReady {
		return nil, ErrSetupNotReady
	}

	messageID := newMessageID()
	waiter := conn.expectResponse(messageID)

	env, err := NewEnvelope(typ, messageID, deviceID, "", payload)
	if err != nil {
		return nil, err
	}
	if err := conn.Send(ctx, env); err != nil {
		return nil, err
	}

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case response, ok := <-waiter:
		if !ok {
			return nil, ErrDeviceOffline
		}
		switch response.Type {
		case MsgWorkspaceResponse:
			payload, err := DecodePayload[WorkspaceResponsePayload](response)
			if err != nil {
				return nil, errors.New("device returned an invalid workspace response")
			}
			if !payload.OK {
				if payload.Error == nil {
					return nil, errors.New("device workspace request failed")
				}
				return nil, WorkspaceError{Code: payload.Error.Code, Message: payload.Error.Message}
			}
			return payload.Payload, nil
		case MsgError:
			payload, err := DecodePayload[ErrorPayload](response)
			if err != nil {
				return nil, errors.New("device returned an error")
			}
			return nil, WorkspaceError{Code: payload.Code, Message: payload.Message}
		default:
			return nil, errors.New("device returned an unexpected workspace response")
		}
	case <-timer.C:
		return nil, WorkspaceError{Code: "timeout", Message: fmt.Sprintf("%s timed out", typ)}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-conn.closed:
		return nil, ErrDeviceOffline
	}
}

type WorkspaceError struct {
	Code    string
	Message string
}

func (e WorkspaceError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "workspace request failed"
}

func (r *Registry) RegisterPermission(toolCallID string, taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if toolCallID == "" {
		return
	}
	r.toolCallToTask[toolCallID] = taskID
}

func (r *Registry) TaskBySession(sessionID string) (*TaskRun, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	taskID := r.sessionToTask[sessionID]
	if taskID == "" {
		return nil, false
	}
	task := r.tasks[taskID]
	return task, task != nil
}

func (r *Registry) TaskByToolCall(toolCallID string) (*TaskRun, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	taskID := r.toolCallToTask[toolCallID]
	if taskID == "" {
		return nil, false
	}
	task := r.tasks[taskID]
	return task, task != nil
}

func (r *Registry) DeleteDevice(deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	device := r.devices[deviceID]
	if device == nil {
		return ErrDeviceNotFound
	}
	if taskID := r.deviceCurrentTask[deviceID]; taskID != "" {
		return ErrDeviceBusy
	}

	delete(r.devices, deviceID)
	err := r.persistLocked()
	r.taskCond.Broadcast()
	return err
}

func (r *Registry) UpdateAlias(deviceID, alias string) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	device := r.devices[deviceID]
	if device == nil {
		return Device{}, ErrDeviceNotFound
	}

	device.Alias = strings.TrimSpace(alias)
	if len(device.Alias) > 100 {
		device.Alias = device.Alias[:100]
	}
	device.UpdatedAt = time.Now().UnixMilli()

	if err := r.persistLocked(); err != nil {
		return Device{}, err
	}
	return cloneDevice(*device), nil
}

func (r *Registry) setTaskSession(taskID, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	task := r.tasks[taskID]
	if task == nil {
		return
	}
	task.SessionID = sessionID
	if sessionID != "" {
		r.sessionToTask[sessionID] = taskID
	}
}

func (r *Registry) releaseTaskRoutingLocked(taskID string) {
	task := r.tasks[taskID]
	if task == nil {
		return
	}
	if task.SessionID != "" {
		delete(r.sessionToTask, task.SessionID)
	}
	delete(r.deviceCurrentTask, task.DeviceID)
	for toolCallID, mappedTaskID := range r.toolCallToTask {
		if mappedTaskID == taskID {
			delete(r.toolCallToTask, toolCallID)
		}
	}
	if device := r.devices[task.DeviceID]; device != nil {
		device.RunningTaskIDs = nil
		if r.conns[task.DeviceID] == nil {
			device.Status = StatusOffline
		} else if !device.SetupReady {
			device.Status = StatusSetupRequired
		} else {
			device.Status = StatusOnline
		}
	}
}

func (r *Registry) failTaskLocked(taskID, message string) {
	task := r.tasks[taskID]
	if task == nil {
		return
	}

	select {
	case task.Events <- DeviceEvent{Type: DeviceEventError, TaskID: taskID, Err: errors.New(message)}:
	default:
	}
}

func (r *Registry) forwardTaskEvent(env Envelope) {
	r.mu.RLock()
	task := r.tasks[env.TaskID]
	r.mu.RUnlock()
	if task == nil {
		return
	}

	switch env.Type {
	case MsgTaskSession:
		payload, err := DecodePayload[TaskSessionPayload](env)
		if err == nil && payload.SessionID != "" {
			r.setTaskSession(env.TaskID, payload.SessionID)
		}
	case MsgPermissionRequest:
		payload, err := DecodePayload[PermissionRequestPayload](env)
		if err == nil {
			r.RegisterPermission(payload.ToolCall.ToolCallID, env.TaskID)
			if payload.SessionID != "" {
				r.setTaskSession(env.TaskID, payload.SessionID)
			}
		}
	}

	event := DeviceEvent{TaskID: env.TaskID, Payload: env.Payload}
	switch env.Type {
	case MsgTaskSession:
		event.Type = DeviceEventSession
	case MsgTaskEvent:
		event.Type = DeviceEventNotification
	case MsgPermissionRequest:
		event.Type = DeviceEventPermissionRequest
	case MsgTaskDone:
		event.Type = DeviceEventDone
	case MsgTaskError:
		event.Type = DeviceEventError
	default:
		return
	}

	select {
	case task.Events <- event:
	default:
		go func() {
			task.Events <- DeviceEvent{Type: DeviceEventError, TaskID: env.TaskID, Err: ErrTaskEventBufferFull}
		}()
	}
}

func (r *Registry) monitorHeartbeats() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().UnixMilli()
		var stale []string

		r.mu.RLock()
		for id, device := range r.devices {
			if r.conns[id] == nil {
				continue
			}
			if now-device.LastHeartbeat > 45_000 {
				stale = append(stale, id)
			}
		}
		r.mu.RUnlock()

		for _, id := range stale {
			r.MarkDisconnected(id, "heartbeat timeout")
		}
	}
}

func (r *Registry) persistLocked() error {
	devices := make([]Device, 0, len(r.devices))
	for _, device := range r.devices {
		devices = append(devices, cloneDevice(*device))
	}
	return r.store.Save(devices)
}

func cloneDevice(device Device) Device {
	cloned := device
	cloned.Agents = append([]DeviceAgentInfo(nil), device.Agents...)
	cloned.RunningTaskIDs = append([]string(nil), device.RunningTaskIDs...)
	if device.SetupStatus != nil {
		status := cloneSetupStatus(*device.SetupStatus)
		cloned.SetupStatus = &status
	}
	return cloned
}

func cloneSetupStatus(status setupcheck.SetupStatus) setupcheck.SetupStatus {
	cloned := status
	cloned.Environment = append([]setupcheck.DependencyItem(nil), status.Environment...)
	cloned.Agents = append([]setupcheck.DependencyItem(nil), status.Agents...)
	cloned.ACPPackages = append([]setupcheck.DependencyItem(nil), status.ACPPackages...)
	return cloned
}
