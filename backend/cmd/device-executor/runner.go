package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/jsonrpc"
	"github.com/pengmide/lumi/internal/requestercontext"
	"github.com/pengmide/lumi/internal/sessioninstruction"
)

type Runner struct {
	cfg    *ExecutorConfig
	client *Client

	mu          sync.Mutex
	agents      map[string]*agent.Process
	initialized map[string]bool
	sessions    map[string]string
	currentTask *runningTask
}

type runningTask struct {
	TaskID    string
	AgentID   string
	SessionID string
	Process   *agent.Process
}

func NewRunner(cfg *ExecutorConfig, client *Client) *Runner {
	return &Runner{
		cfg:         cfg,
		client:      client,
		agents:      make(map[string]*agent.Process),
		initialized: make(map[string]bool),
		sessions:    make(map[string]string),
	}
}

func (r *Runner) Execute(ctx context.Context, env Envelope) {
	payload, err := decodePayload[TaskExecutePayload](env)
	if err != nil {
		r.sendTaskError(env.TaskID, fmt.Sprintf("invalid task.execute payload: %v", err))
		return
	}
	if !r.client.SetupReady() {
		r.sendTaskError(env.TaskID, "Device setup is not ready")
		return
	}

	claimed, proc, err := r.beginTask(env.TaskID, payload.AgentID, payload.WorkspacePath)
	if err != nil {
		r.sendTaskError(env.TaskID, err.Error())
		return
	}
	if !claimed {
		r.sendTaskError(env.TaskID, "Device is busy")
		return
	}
	defer r.finishTask(env.TaskID)

	cleanupNotification := proc.OnNotification(func(msg *jsonrpc.Message) {
		if err := r.client.Send(MsgTaskEvent, env.TaskID, TaskEventPayload{
			SessionID:    r.sessionForTask(env.TaskID),
			Notification: toACPNotification(msg),
		}); err != nil {
			log.Printf("failed to forward notification for task %s: %v", env.TaskID, err)
		}
	})
	defer cleanupNotification()

	cleanupPermission := proc.OnPermission(func(req *agent.PermissionRequest) {
		if err := r.client.Send(MsgPermissionRequest, env.TaskID, toPermissionRequestPayload(req)); err != nil {
			log.Printf("failed to forward permission request for task %s: %v", env.TaskID, err)
		}
	})
	defer cleanupPermission()

	sessionID := payload.SessionID
	if sessionID == "" {
		newSessionID, err := r.createSession(env.TaskID, proc, payload)
		if err != nil {
			r.sendTaskError(env.TaskID, err.Error())
			return
		}
		sessionID = newSessionID
	} else {
		r.setSessionForTask(env.TaskID, sessionID)
		if err := r.client.Send(MsgTaskSession, env.TaskID, TaskSessionPayload{SessionID: sessionID}); err != nil {
			r.sendTaskError(env.TaskID, err.Error())
			return
		}
	}

	resp, err := r.promptWithRequesterContext(proc, sessionID, payload)
	if err != nil {
		if shouldRecoverUnknownSession(err) && payload.SessionID != "" {
			log.Printf("remote session %s is no longer valid for task %s; creating a replacement session", payload.SessionID, env.TaskID)
			newSessionID, newErr := r.createSession(env.TaskID, proc, payload)
			if newErr != nil {
				r.sendTaskError(env.TaskID, fmt.Sprintf("%s; failed to recover session: %v", err.Error(), newErr))
				return
			}
			sessionID = newSessionID
			resp, err = r.promptWithRequesterContext(proc, sessionID, payload)
			if err == nil {
				if err := r.client.Send(MsgTaskDone, env.TaskID, TaskDonePayload{Result: resp.Result}); err != nil {
					log.Printf("failed to send task.done for %s: %v", env.TaskID, err)
				}
				return
			}
		}
		r.sendTaskError(env.TaskID, err.Error())
		return
	}

	if err := r.client.Send(MsgTaskDone, env.TaskID, TaskDonePayload{Result: resp.Result}); err != nil {
		log.Printf("failed to send task.done for %s: %v", env.TaskID, err)
	}
}

func (r *Runner) createSession(taskID string, proc *agent.Process, payload TaskExecutePayload) (string, error) {
	cwd := payload.WorkspacePath
	if cwd == "" {
		cwd = r.cfg.Workspace
	}

	agentCfg := findAgentConfig(r.cfg, payload.AgentID)
	backend := agentmode.BackendUnknown
	sessionMode := agentmode.ModeDefault
	if agentCfg != nil {
		backend = agentmode.DetectBackend(agentCfg.ID, agentCfg.Command, agentCfg.Args)
		sessionMode = agentmode.ResolveSessionMode(backend, agentCfg.SessionMode, agentCfg.PermissionMode)
		if err := agentmode.PrepareSessionMode(backend, sessionMode); err != nil {
			return "", fmt.Errorf("failed to prepare agent mode: %v", err)
		}
	}

	sessionNewParams := map[string]any{
		"cwd":        cwd,
		"mcpServers": MCPRecordsForBackend(backend),
	}
	if payload.InstructionProfile != nil {
		if err := sessioninstruction.ApplyProfile(sessionNewParams, proc.SessionInstructionSupport(), *payload.InstructionProfile, sessioninstruction.PhaseNew); err != nil {
			return "", err
		}
	}
	resp, err := proc.Request("session/new", sessionNewParams)
	if err != nil {
		return "", err
	}

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := resp.ParseResult(&result); err != nil {
		return "", fmt.Errorf("failed to parse session/new result: %v", err)
	}
	if result.SessionID == "" {
		return "", fmt.Errorf("session/new returned empty sessionId")
	}

	r.setSessionForTask(taskID, result.SessionID)
	if err := r.client.Send(MsgTaskSession, taskID, TaskSessionPayload{SessionID: result.SessionID}); err != nil {
		return "", err
	}

	if agentmode.ShouldSetACPMode(backend, sessionMode) {
		if _, err := proc.Request("session/set_mode", map[string]any{
			"sessionId": result.SessionID,
			"modeId":    sessionMode,
		}); err != nil {
			return "", fmt.Errorf("failed to set session mode: %v", err)
		}
	}
	return result.SessionID, nil
}

func shouldRecoverUnknownSession(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unknown sessionid") ||
		strings.Contains(message, "unknown session id") ||
		strings.Contains(message, "session not found") ||
		strings.Contains(message, "no session found") ||
		strings.Contains(message, "missing session")
}

func (r *Runner) Cancel(_ context.Context, env Envelope) {
	payload, err := decodePayload[TaskCancelPayload](env)
	if err != nil {
		log.Printf("invalid task.cancel payload: %v", err)
		return
	}

	r.mu.Lock()
	current := r.currentTask
	if current == nil || current.TaskID != env.TaskID || current.Process == nil {
		r.mu.Unlock()
		return
	}

	sessionID := payload.SessionID
	if sessionID == "" {
		sessionID = r.sessions[env.TaskID]
	}
	if sessionID != "" {
		current.SessionID = sessionID
	}
	r.mu.Unlock()

	r.AbortCurrentTask("task cancelled")
}

func (r *Runner) ConfirmPermission(_ context.Context, env Envelope) {
	payload, err := decodePayload[PermissionConfirmPayload](env)
	if err != nil {
		log.Printf("invalid permission.confirm payload: %v", err)
		return
	}

	r.mu.Lock()
	current := r.currentTask
	r.mu.Unlock()
	if current == nil || current.TaskID != env.TaskID || current.Process == nil {
		return
	}

	current.Process.ConfirmPermission(payload.ToolCallID, payload.OptionID)
}

func (r *Runner) RunningTaskIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.currentTask == nil {
		return nil
	}
	return []string{r.currentTask.TaskID}
}

func (r *Runner) AbortCurrentTask(reason string) {
	r.mu.Lock()
	current := r.currentTask
	if current == nil {
		r.mu.Unlock()
		return
	}

	delete(r.sessions, current.TaskID)
	r.currentTask = nil
	if current.AgentID != "" {
		delete(r.agents, current.AgentID)
		delete(r.initialized, current.AgentID)
	}
	r.mu.Unlock()

	if current.Process == nil {
		return
	}
	if current.SessionID != "" {
		if err := current.Process.Notify("session/cancel", map[string]string{
			"sessionId": current.SessionID,
		}); err != nil {
			log.Printf("failed to cancel task %s during %s: %v", current.TaskID, reason, err)
		}
	}
	if err := current.Process.Stop(); err != nil {
		log.Printf("failed to stop agent process for task %s during %s: %v", current.TaskID, reason, err)
	}
}

func (r *Runner) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, proc := range r.agents {
		if err := proc.Stop(); err != nil {
			log.Printf("failed to stop agent %s: %v", proc.ID, err)
		}
	}
}

func (r *Runner) beginTask(taskID, agentID, workspacePath string) (bool, *agent.Process, error) {
	proc, err := r.getOrStartAgent(agentID, workspacePath)
	if err != nil {
		return false, nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.currentTask != nil && r.currentTask.TaskID != taskID {
		return false, nil, nil
	}

	r.currentTask = &runningTask{
		TaskID:  taskID,
		AgentID: agentID,
		Process: proc,
	}
	if err := r.client.Send(MsgDeviceStatus, "", DeviceStatusPayload{Status: "busy"}); err != nil {
		log.Printf("failed to send busy status: %v", err)
	}
	return true, proc, nil
}

func (r *Runner) finishTask(taskID string) {
	r.mu.Lock()
	delete(r.sessions, taskID)
	if r.currentTask != nil && r.currentTask.TaskID == taskID {
		r.currentTask = nil
	}
	hasRunningTask := r.currentTask != nil
	r.mu.Unlock()

	nextStatus := "setup_required"
	if r.client.SetupReady() {
		if hasRunningTask {
			nextStatus = "busy"
		} else {
			nextStatus = "online"
		}
	}
	if err := r.client.Send(MsgDeviceStatus, "", DeviceStatusPayload{Status: nextStatus}); err != nil {
		log.Printf("failed to send device status: %v", err)
	}
}

func (r *Runner) getOrStartAgent(agentID, workspacePath string) (*agent.Process, error) {
	if agentID == "" {
		agentID = r.cfg.DefaultAgent
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	proc, ok := r.agents[agentID]
	if !ok {
		agentCfg := findAgentConfig(r.cfg, agentID)
		if agentCfg == nil {
			return nil, fmt.Errorf("agent not found: %s", agentID)
		}
		runtimeEnv := buildLumiRuntimeEnv(r.client.server, r.cfg)
		bridge, err := r.requesterContextBridge(agentID)
		if err != nil {
			return nil, err
		}
		runtimeEnv[requestercontext.EnvRequesterContextDir] = bridge.Dir()
		mergedAgentCfg := mergeAgentEnv(agentCfg, runtimeEnv)
		if err := prepareAgentRuntime(mergedAgentCfg); err != nil {
			return nil, err
		}
		proc = agent.NewProcess(mergedAgentCfg)
		r.agents[agentID] = proc
	}

	if workspacePath == "" {
		workspacePath = r.cfg.Workspace
	}
	proc.SetWorkingDir(workspacePath)
	if err := proc.Start(); err != nil {
		return nil, err
	}
	if !r.initialized[agentID] {
		if _, err := proc.Request("initialize", map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs": map[string]bool{"readTextFile": true, "writeTextFile": true},
			},
			"clientInfo": map[string]string{
				"name":    "device-executor",
				"version": "0.1.0",
			},
		}); err != nil {
			return nil, err
		}
		r.initialized[agentID] = true
	}

	return proc, nil
}

func (r *Runner) setSessionForTask(taskID, sessionID string) {
	r.mu.Lock()
	r.sessions[taskID] = sessionID
	if r.currentTask != nil && r.currentTask.TaskID == taskID {
		r.currentTask.SessionID = sessionID
	}
	r.mu.Unlock()
}

func (r *Runner) sessionForTask(taskID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[taskID]
}

func (r *Runner) sendTaskError(taskID, message string) {
	if err := r.client.Send(MsgTaskError, taskID, TaskErrorPayload{Message: message}); err != nil {
		log.Printf("failed to send task.error for %s: %v", taskID, err)
	}
}

func findAgentConfig(cfg *ExecutorConfig, agentID string) *config.AgentConfig {
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == agentID {
			return &cfg.Agents[i]
		}
	}
	return nil
}

func toACPNotification(msg *jsonrpc.Message) ACPNotification {
	params := json.RawMessage(nil)
	if len(msg.Params) > 0 {
		params = append(json.RawMessage(nil), msg.Params...)
	}
	return ACPNotification{
		JSONRPC: msg.JSONRPC,
		Method:  msg.Method,
		Params:  params,
	}
}

func toPermissionRequestPayload(req *agent.PermissionRequest) PermissionRequestPayload {
	options := make([]PermissionOption, 0, len(req.Options))
	for _, option := range req.Options {
		options = append(options, PermissionOption{
			OptionID: option.OptionID,
			Name:     option.Name,
			Kind:     option.Kind,
		})
	}

	var rawInput json.RawMessage
	if req.ToolCall.RawInput != nil {
		if data, err := json.Marshal(req.ToolCall.RawInput); err == nil {
			rawInput = data
		}
	}

	return PermissionRequestPayload{
		SessionID: req.SessionID,
		Options:   options,
		ToolCall: PermissionToolCall{
			ToolCallID: req.ToolCall.ToolCallID,
			RawInput:   rawInput,
			Status:     req.ToolCall.Status,
			Title:      req.ToolCall.Title,
			Kind:       req.ToolCall.Kind,
		},
	}
}
