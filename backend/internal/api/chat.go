package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/jsonrpc"
	workspacepreview "github.com/pengmide/lumi/internal/workspace"
)

type chatFileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type chatRequest struct {
	Message        string         `json:"message"`
	ConversationID string         `json:"conversationId"`
	WorkspaceID    string         `json:"workspaceId"`
	Files          []chatFileInfo `json:"files"`
	DeviceID       string         `json:"deviceId,omitempty"`
	AgentID        string         `json:"agentId,omitempty"`
	Hidden         bool           `json:"hidden,omitempty"`
	CronJobID      string         `json:"cronJobId,omitempty"`
	CronJobName    string         `json:"cronJobName,omitempty"`
}

type streamItem struct {
	Type     string
	Text     string
	Tool     *conversation.ToolCallInfo
	Thinking *thinkingData
}

type chatRunResult struct {
	Err error
}

const maxRemoteMentionedFiles = 5

var fileMentionRegex = regexp.MustCompile(`@([\w./-]+)`)

type chatPrepared struct {
	ConvID        string
	IsNew         bool
	AgentID       string
	PreviousAgent string
	AgentChanged  bool
	WorkspaceID   string
	WorkspacePath string
	PromptText    string
	MessageFiles  []conversation.MessageFile
}

type chatRuntimeContext struct {
	Request     chatRequest
	Prepared    *chatPrepared
	SendEvent   func(string, any)
	HTTPRequest *http.Request
	Context     context.Context
	Result      *chatRunResult
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	sendEvent, ok := setupSSE(w)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	prepared, err := s.prepareChat(r.Context(), req)
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	ctx := chatRuntimeContext{
		Request:     req,
		Prepared:    prepared,
		SendEvent:   sendEvent,
		HTTPRequest: r,
		Context:     r.Context(),
		Result:      &chatRunResult{},
	}
	if !req.Hidden {
		if !s.acquireCronRun(prepared.ConvID) {
			ctx.setError(fmt.Errorf("conversation busy"))
			sendEvent("error", map[string]string{"message": "Conversation is busy"})
			return
		}
		defer s.releaseCronRun(prepared.ConvID)
	}

	if req.DeviceID == "" {
		runtime, err := s.resolveWorkspaceRuntime(r.Context(), prepared.WorkspaceID, r)
		if err != nil {
			sendEvent("error", runtimeErrorEventPayload(err))
			return
		}
		ctx.Prepared.WorkspacePath = runtime.WorkspacePath
		if runtime.Mode != "local" {
			ctx.Request.DeviceID = runtime.DeviceID
		}
	}

	if ctx.Request.DeviceID == "" {
		s.handleLocalChat(ctx)
		return
	}
	s.handleDeviceChat(ctx)
}

func setupSSE(w http.ResponseWriter) (func(string, any), bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}

	sendEvent := func(event string, data any) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
		flusher.Flush()
	}

	return sendEvent, true
}

func (s *Server) prepareChat(ctx context.Context, req chatRequest) (*chatPrepared, error) {
	convID, isNew := s.getOrCreateConversation(req)
	conv := s.conversations.Get(convID)
	if conv == nil {
		return nil, errors.New("conversation not found")
	}

	previousAgent := conv.ActiveAgent
	agentID := previousAgent
	if req.AgentID != "" && s.router.HasAgent(req.AgentID) {
		agentID = req.AgentID
	} else if mentionedAgent := s.router.DetectMention(req.Message); mentionedAgent != "" {
		agentID = mentionedAgent
	}
	if agentID == "" {
		agentID = s.config.DefaultAgent
	}

	workspaceID := req.WorkspaceID
	if workspaceID == "" {
		if conv.WorkspaceID != "" {
			workspaceID = conv.WorkspaceID
		} else {
			workspaceID = s.defaultWorkspaceID()
		}
	}
	workspacePath := s.resolveWorkspacePath(workspaceID)
	workspaceConfig, hasWorkspace := s.resolveWorkspace(workspaceID)

	promptText := req.Message
	if len(req.Files) > 0 {
		promptText = formatFileReferences(req.Files) + " " + promptText
	}
	if hasWorkspace && isRemoteWorkspaceConfig(*workspaceConfig) && req.DeviceID != "" {
		expandedPromptText, err := s.expandRemoteFileMentions(ctx, *workspaceConfig, req.Message, promptText)
		if err != nil {
			return nil, err
		}
		promptText = expandedPromptText
	}

	agentChanged := previousAgent != agentID && len(conv.Messages) > 0
	if agentChanged {
		context := s.conversations.GetContextSummary(convID, 10)
		if context != "" {
			promptText = context + "User: " + promptText
		}
	}
	if !req.Hidden {
		promptText = lumicron.WithConversationInstructionAndJobs(promptText, s.currentCronJobsForPrompt(lumicron.ChannelWeb, convID))
	}

	messageFiles := make([]conversation.MessageFile, 0, len(req.Files))
	for _, file := range req.Files {
		messageFiles = append(messageFiles, conversation.MessageFile{
			Name: file.Name,
			Path: file.Path,
			Size: file.Size,
		})
	}

	return &chatPrepared{
		ConvID:        convID,
		IsNew:         isNew,
		AgentID:       agentID,
		PreviousAgent: previousAgent,
		AgentChanged:  previousAgent != "" && previousAgent != agentID,
		WorkspaceID:   workspaceID,
		WorkspacePath: workspacePath,
		PromptText:    promptText,
		MessageFiles:  messageFiles,
	}, nil
}

func (s *Server) expandRemoteFileMentions(ctx context.Context, workspace config.WorkspaceConfig, message string, promptText string) (string, error) {
	mentions := collectFileMentions(message, s.router)
	if len(mentions) == 0 {
		return promptText, nil
	}

	contextBlocks := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		if len(contextBlocks) >= maxRemoteMentionedFiles {
			break
		}

		var textFile workspacepreview.TextFile
		err := s.remoteWorkspacePayload(ctx, workspace, device.MsgWorkspaceText, device.WorkspaceRequestPayload{
			Path: mention,
		}, &textFile)
		if err != nil {
			switch {
			case errors.Is(err, workspacepreview.ErrNotFound):
				continue
			case errors.Is(err, workspacepreview.ErrIsDirectory):
				contextBlocks = append(contextBlocks, fmt.Sprintf("Remote workspace mention %q points to a directory and was not inlined.", mention))
				continue
			case errors.Is(err, workspacepreview.ErrUnsupportedTextFile):
				contextBlocks = append(contextBlocks, fmt.Sprintf("Remote workspace mention %q is not a text-previewable file and was not inlined.", mention))
				continue
			default:
				return "", err
			}
		}

		block := []string{
			fmt.Sprintf("--- BEGIN REMOTE FILE: %s ---", textFile.Meta.Path),
			textFile.Content,
			fmt.Sprintf("--- END REMOTE FILE: %s ---", textFile.Meta.Path),
		}
		if textFile.Truncated {
			block = append(block, fmt.Sprintf("Note: %s was truncated while loading remote @file context.", textFile.Meta.Path))
		}
		contextBlocks = append(contextBlocks, strings.Join(block, "\n"))
	}

	if len(contextBlocks) == 0 {
		return promptText, nil
	}

	var builder strings.Builder
	builder.WriteString("[Remote workspace @file context]\n")
	builder.WriteString(strings.Join(contextBlocks, "\n\n"))
	builder.WriteString("\n\n[User message]\n")
	builder.WriteString(promptText)
	return builder.String(), nil
}

func collectFileMentions(message string, router interface{ HasAgent(string) bool }) []string {
	matches := fileMentionRegex.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}

	mentions := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		mention := strings.TrimSpace(match[1])
		if mention == "" {
			continue
		}
		if router != nil && router.HasAgent(mention) {
			continue
		}
		if _, ok := seen[mention]; ok {
			continue
		}
		seen[mention] = struct{}{}
		mentions = append(mentions, mention)
	}
	return mentions
}

func (s *Server) handleLocalChat(ctx chatRuntimeContext) {
	s.applyPreparedAgentSwitch(ctx.Prepared)
	if !s.initialized[ctx.Prepared.AgentID] {
		ctx.SendEvent("status", map[string]string{"message": fmt.Sprintf("Initializing %s...", ctx.Prepared.AgentID)})
		if err := s.initializeAgent(ctx.Prepared.AgentID); err != nil {
			ctx.setError(err)
			ctx.SendEvent("error", map[string]string{"message": err.Error()})
			return
		}
		s.initialized[ctx.Prepared.AgentID] = true
	}

	agentProc, err := s.agents.Get(ctx.Prepared.AgentID)
	if err != nil {
		ctx.setError(err)
		ctx.SendEvent("error", map[string]string{"message": "Failed to get agent: " + err.Error()})
		return
	}
	agentProc.SetWorkingDir(ctx.Prepared.WorkspacePath)

	streamItems := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolCallMap := make(map[string]int)
	cronCommandStream := &cronCommandStreamState{}

	cleanupNotification := agentProc.OnNotification(func(msg *jsonrpc.Message) {
		s.handleNotification(msg, ctx.SendEvent, &streamItems, accumulator, toolCallMap, ctx.Prepared.AgentID, cronCommandStream)
	})
	defer cleanupNotification()

	cleanupPermission := agentProc.OnPermission(func(req *agent.PermissionRequest) {
		if s.shouldAutoApproveAgent(ctx.Prepared.AgentID) {
			if optionID, ok := selectLocalPermissionOption(req); ok {
				agentProc.ConfirmPermission(req.ToolCall.ToolCallID, optionID)
				return
			}
		}
		ctx.SendEvent("permission_request", req)
	})
	defer cleanupPermission()

	sessionsMap := s.agentSessions[ctx.Prepared.ConvID]
	if sessionsMap == nil {
		sessionsMap = make(map[string]string)
		s.agentSessions[ctx.Prepared.ConvID] = sessionsMap
	}

	sessionID := sessionsMap[ctx.Prepared.AgentID]
	if sessionID == "" {
		sessionID, err = s.createAgentSession(ctx.Prepared.AgentID, ctx.Prepared.WorkspacePath)
		if err != nil {
			ctx.setError(err)
			ctx.SendEvent("error", map[string]string{"message": err.Error()})
			return
		}
		sessionsMap[ctx.Prepared.AgentID] = sessionID
	}

	s.addChatUserMessage(ctx)
	s.conversations.SetSessionID(ctx.Prepared.ConvID, sessionID)

	ctx.SendEvent("session", map[string]any{
		"conversationId": ctx.Prepared.ConvID,
		"sessionId":      sessionID,
		"agent":          ctx.Prepared.AgentID,
		"isNew":          ctx.Prepared.IsNew,
	})
	ctx.SendEvent("status", map[string]string{"message": "Processing..."})

	response, err := agentProc.Request("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": ctx.Prepared.PromptText}},
	})
	if err != nil {
		ctx.setError(err)
		ctx.SendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	if err := s.executeCronCommandsForChat(ctx, &streamItems, &accumulator.currentText); err != nil {
		ctx.setError(err)
		ctx.SendEvent("error", map[string]string{"message": err.Error()})
		return
	}
	s.finalizeAssistantStream(ctx.Prepared.ConvID, ctx.Prepared.AgentID, streamItems, accumulator)

	var result map[string]any
	response.ParseResult(&result)
	if result == nil {
		result = make(map[string]any)
	}
	if result["stopReason"] == nil {
		result["stopReason"] = "end_turn"
	}
	ctx.SendEvent("done", result)
}

func (s *Server) handleDeviceChat(ctx chatRuntimeContext) {
	deviceInfo, ok := s.devices.GetDevice(ctx.Request.DeviceID)
	if !ok {
		ctx.setError(fmt.Errorf("device not found"))
		ctx.SendEvent("error", map[string]string{"message": "Device not found"})
		return
	}
	if deviceInfo.Status == device.StatusOffline {
		ctx.setError(fmt.Errorf("device is offline"))
		ctx.SendEvent("error", map[string]string{"message": "Device is offline"})
		return
	}
	if !deviceInfo.SetupReady {
		ctx.setError(fmt.Errorf("device setup is not ready"))
		ctx.SendEvent("error", map[string]string{"message": "Device setup is not ready"})
		return
	}
	if !deviceHasAgent(deviceInfo, ctx.Prepared.AgentID) {
		ctx.setError(errors.New(deviceAgentUnavailableMessage(deviceInfo, ctx.Prepared.AgentID)))
		ctx.SendEvent("error", map[string]string{"message": deviceAgentUnavailableMessage(deviceInfo, ctx.Prepared.AgentID)})
		return
	}
	s.applyPreparedAgentSwitch(ctx.Prepared)

	taskID := newTaskRunID()
	remoteSessionID := s.getRemoteSession(ctx.Prepared.ConvID, ctx.Request.DeviceID, ctx.Prepared.AgentID)
	task := device.NewTaskRun(
		taskID,
		ctx.Request.DeviceID,
		ctx.Prepared.ConvID,
		ctx.Prepared.AgentID,
		ctx.Prepared.WorkspaceID,
		ctx.Prepared.WorkspacePath,
	)
	task.SessionID = remoteSessionID
	if err := s.devices.StartTask(task); err != nil {
		ctx.setError(err)
		ctx.SendEvent("error", map[string]string{"message": deviceErrorMessage(err)})
		return
	}
	defer s.devices.FinishTask(task.ID)

	payload := device.TaskExecutePayload{
		ConversationID: ctx.Prepared.ConvID,
		AgentID:        ctx.Prepared.AgentID,
		SessionID:      remoteSessionID,
		WorkspaceID:    ctx.Prepared.WorkspaceID,
		WorkspacePath:  ctx.Prepared.WorkspacePath,
		Prompt:         ctx.Prepared.PromptText,
		Files:          toTaskFiles(ctx.Request.Files),
	}

	if err := s.devices.SendToDevice(ctx.requestContext(), ctx.Request.DeviceID, device.MsgTaskExecute, task.ID, payload); err != nil {
		ctx.setError(err)
		ctx.SendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	s.addChatUserMessage(ctx)
	s.consumeDeviceTaskEvents(ctx, task)
}

func (s *Server) addChatUserMessage(ctx chatRuntimeContext) {
	msg := conversation.Message{
		Role:    "user",
		Content: ctx.Request.Message,
		Files:   ctx.Prepared.MessageFiles,
		Hidden:  ctx.Request.Hidden,
	}
	if ctx.Request.CronJobID != "" {
		msg.Cron = &conversation.CronMessageMeta{
			JobID:       ctx.Request.CronJobID,
			JobName:     ctx.Request.CronJobName,
			TriggeredAt: time.Now().UnixMilli(),
		}
	}
	s.conversations.AddMessage(ctx.Prepared.ConvID, msg)
}

func (ctx chatRuntimeContext) requestContext() context.Context {
	if ctx.Context != nil {
		return ctx.Context
	}
	if ctx.HTTPRequest != nil {
		return ctx.HTTPRequest.Context()
	}
	return context.Background()
}

func (ctx chatRuntimeContext) setError(err error) {
	if err != nil && ctx.Result != nil && ctx.Result.Err == nil {
		ctx.Result.Err = err
	}
}

func (s *Server) applyPreparedAgentSwitch(prepared *chatPrepared) {
	if prepared == nil || !prepared.AgentChanged {
		return
	}
	s.conversations.SetActiveAgent(prepared.ConvID, prepared.AgentID)
	log.Printf("Agent switched via @mention: %s -> %s", prepared.PreviousAgent, prepared.AgentID)
	prepared.AgentChanged = false
}

func (s *Server) consumeDeviceTaskEvents(ctx chatRuntimeContext, task *device.TaskRun) {
	streamItems := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolCallMap := make(map[string]int)
	cronCommandStream := &cronCommandStreamState{}
	pendingNotifications := make([]device.DeviceEvent, 0)
	sessionReady := task.SessionID != ""
	sessionAnnounced := false
	sessionTimer := time.NewTimer(30 * time.Second)
	defer sessionTimer.Stop()

	handleDeviceNotification := func(event device.DeviceEvent) bool {
		payload, err := device.DecodePayload[device.TaskEventPayload](device.Envelope{Payload: event.Payload})
		if err != nil {
			ctx.SendEvent("error", map[string]string{"message": "Invalid device event"})
			ctx.setError(fmt.Errorf("invalid device event"))
			return false
		}
		msg := &jsonrpc.Message{
			JSONRPC: payload.Notification.JSONRPC,
			Method:  payload.Notification.Method,
			Params:  payload.Notification.Params,
		}
		s.handleNotification(msg, ctx.SendEvent, &streamItems, accumulator, toolCallMap, ctx.Prepared.AgentID, cronCommandStream)
		return true
	}

	if sessionReady {
		if !sessionTimer.Stop() {
			select {
			case <-sessionTimer.C:
			default:
			}
		}
		ctx.SendEvent("session", map[string]any{
			"conversationId": ctx.Prepared.ConvID,
			"sessionId":      task.SessionID,
			"agent":          ctx.Prepared.AgentID,
			"isNew":          ctx.Prepared.IsNew,
		})
		ctx.SendEvent("status", map[string]string{"message": "Processing..."})
		sessionAnnounced = true
	}

	sendCancel := func(reason string) {
		_ = s.devices.SendToDevice(contextWithoutCancel(ctx.requestContext()), task.DeviceID, device.MsgTaskCancel, task.ID, device.TaskCancelPayload{
			SessionID: task.SessionID,
			Reason:    reason,
		})
	}

	for {
		select {
		case <-ctx.requestContext().Done():
			sendCancel("client_disconnected")
			return

		case <-sessionTimer.C:
			if !sessionReady {
				ctx.SendEvent("error", map[string]string{"message": "Device did not create session"})
				ctx.setError(fmt.Errorf("device did not create session"))
				sendCancel("session_timeout")
				return
			}

		case event := <-task.Events:
			switch event.Type {
			case device.DeviceEventSession:
				payload, err := device.DecodePayload[device.TaskSessionPayload](device.Envelope{Payload: event.Payload})
				if err != nil || payload.SessionID == "" {
					ctx.SendEvent("error", map[string]string{"message": "Device returned an invalid session"})
					ctx.setError(fmt.Errorf("device returned an invalid session"))
					sendCancel("invalid_session")
					return
				}

				task.SessionID = payload.SessionID
				s.setRemoteSession(ctx.Prepared.ConvID, task.DeviceID, task.AgentID, payload.SessionID)
				s.conversations.SetSessionID(ctx.Prepared.ConvID, payload.SessionID)

				sessionReady = true
				if !sessionTimer.Stop() {
					select {
					case <-sessionTimer.C:
					default:
					}
				}

				if !sessionAnnounced {
					ctx.SendEvent("session", map[string]any{
						"conversationId": ctx.Prepared.ConvID,
						"sessionId":      payload.SessionID,
						"agent":          ctx.Prepared.AgentID,
						"isNew":          ctx.Prepared.IsNew,
					})
					ctx.SendEvent("status", map[string]string{"message": "Processing..."})
					sessionAnnounced = true
				}
				for _, pending := range pendingNotifications {
					if !handleDeviceNotification(pending) {
						return
					}
				}
				pendingNotifications = nil

			case device.DeviceEventNotification:
				if !sessionReady {
					pendingNotifications = append(pendingNotifications, event)
					continue
				}

				if !handleDeviceNotification(event) {
					return
				}

			case device.DeviceEventPermissionRequest:
				if !sessionReady {
					ctx.SendEvent("error", map[string]string{"message": "Device protocol error"})
					ctx.setError(fmt.Errorf("device protocol error"))
					sendCancel("protocol_error")
					return
				}

				payload, err := device.DecodePayload[device.PermissionRequestPayload](device.Envelope{Payload: event.Payload})
				if err != nil {
					ctx.SendEvent("error", map[string]string{"message": "Invalid permission request"})
					ctx.setError(fmt.Errorf("invalid permission request"))
					return
				}
				if s.shouldAutoApproveAgent(ctx.Prepared.AgentID) {
					optionID, ok := selectRemotePermissionOption(payload)
					if ok {
						if err := s.devices.SendToDevice(contextWithoutCancel(ctx.requestContext()), task.DeviceID, device.MsgPermissionConfirm, task.ID, device.PermissionConfirmPayload{
							ToolCallID: payload.ToolCall.ToolCallID,
							OptionID:   optionID,
						}); err != nil {
							ctx.SendEvent("error", map[string]string{"message": "Failed to auto-confirm permission"})
							ctx.setError(fmt.Errorf("failed to auto-confirm permission"))
							return
						}
						continue
					}
				}
				ctx.SendEvent("permission_request", payload)

			case device.DeviceEventDone:
				if !sessionReady {
					ctx.SendEvent("error", map[string]string{"message": "Device protocol error"})
					ctx.setError(fmt.Errorf("device protocol error"))
					sendCancel("protocol_error")
					return
				}

				if err := s.executeCronCommandsForChat(ctx, &streamItems, &accumulator.currentText); err != nil {
					ctx.setError(err)
					ctx.SendEvent("error", map[string]string{"message": err.Error()})
					return
				}
				s.finalizeAssistantStream(ctx.Prepared.ConvID, ctx.Prepared.AgentID, streamItems, accumulator)

				payload, err := device.DecodePayload[device.TaskDonePayload](device.Envelope{Payload: event.Payload})
				if err != nil {
					ctx.SendEvent("done", map[string]any{"stopReason": "end_turn"})
					return
				}

				var result map[string]any
				if len(payload.Result) > 0 {
					_ = json.Unmarshal(payload.Result, &result)
				}
				if result == nil {
					result = map[string]any{"stopReason": "end_turn"}
				}
				if result["stopReason"] == nil {
					result["stopReason"] = "end_turn"
				}
				ctx.SendEvent("done", result)
				return

			case device.DeviceEventError:
				if event.Err != nil {
					ctx.SendEvent("error", map[string]string{"message": event.Err.Error()})
					ctx.setError(event.Err)
					return
				}

				payload, err := device.DecodePayload[device.TaskErrorPayload](device.Envelope{Payload: event.Payload})
				if err != nil || payload.Message == "" {
					ctx.SendEvent("error", map[string]string{"message": "Device execution failed"})
					ctx.setError(fmt.Errorf("device execution failed"))
					return
				}
				ctx.SendEvent("error", map[string]string{"message": payload.Message})
				ctx.setError(errors.New(payload.Message))
				return
			}
		}
	}
}

func (s *Server) finalizeAssistantStream(convID string, agentID string, streamItems []streamItem, accumulator *streamAccumulator) {
	if accumulator != nil {
		accumulator.Finish(&streamItems)
	}

	for _, item := range streamItems {
		if item.Type == "text" {
			s.conversations.AddAssistantMessage(convID, item.Text, agentID)
			continue
		}
		if item.Type == "thinking" && item.Thinking != nil {
			s.conversations.AddThinkingMessage(convID, item.Thinking.Content, agentID, item.Thinking.Status, item.Thinking.Duration)
			continue
		}
		if item.Tool != nil {
			s.conversations.AddToolCall(convID, item.Tool, agentID)
		}
	}
	s.persistConversation(convID)
}

func (s *Server) getOrCreateConversation(req chatRequest) (string, bool) {
	if req.ConversationID != "" && s.conversations.Has(req.ConversationID) {
		return req.ConversationID, false
	}

	if req.ConversationID != "" {
		stored, err := s.sessionStore.Load(req.ConversationID)
		if err == nil {
			s.restoreConversation(stored)
			return req.ConversationID, false
		}
	}

	convID := generateUUID()
	workspaceID := req.WorkspaceID
	if workspaceID == "" {
		workspaceID = s.defaultWorkspaceID()
	}
	s.conversations.Create(convID, s.config.DefaultAgent, workspaceID)
	s.agentSessions[convID] = make(map[string]string)
	return convID, true
}

func (s *Server) initializeAgent(agentID string) error {
	_, err := s.agents.Request(agentID, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]bool{"readTextFile": true, "writeTextFile": true},
		},
		"clientInfo": map[string]string{"name": "lumi-go", "version": "0.1.0"},
	})
	return err
}

func (s *Server) createAgentSession(agentID, cwd string) (string, error) {
	agentConfig := s.config.FindAgent(agentID)
	backend := agentmode.BackendUnknown
	sessionMode := agentmode.ModeDefault
	if agentConfig != nil {
		backend = agentmode.DetectBackend(agentConfig.ID, agentConfig.Command, agentConfig.Args)
		sessionMode = agentmode.ResolveSessionMode(backend, agentConfig.SessionMode, agentConfig.PermissionMode)
		if err := agentmode.PrepareSessionMode(backend, sessionMode); err != nil {
			return "", err
		}
	}

	result, err := s.agents.Request(agentID, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", err
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid response")
	}

	sessionID, _ := resultMap["sessionId"].(string)
	if sessionID == "" {
		return "", fmt.Errorf("no sessionId in response")
	}

	if agentmode.ShouldSetACPMode(backend, sessionMode) {
		if _, err := s.agents.Request(agentID, "session/set_mode", map[string]any{
			"sessionId": sessionID,
			"modeId":    sessionMode,
		}); err != nil {
			return "", fmt.Errorf("failed to set %s mode for %s session %s: %w", sessionMode, agentID, sessionID, err)
		}
	}

	return sessionID, nil
}

func (s *Server) shouldAutoApproveAgent(agentID string) bool {
	agentConfig := s.config.FindAgent(agentID)
	if agentConfig == nil {
		return false
	}

	backend := agentmode.DetectBackend(agentConfig.ID, agentConfig.Command, agentConfig.Args)
	mode := agentmode.ResolveSessionMode(backend, agentConfig.SessionMode, agentConfig.PermissionMode)
	return agentmode.IsAutoApproveMode(backend, mode)
}

func selectLocalPermissionOption(req *agent.PermissionRequest) (string, bool) {
	if req == nil {
		return "", false
	}

	options := make([]agentmode.PermissionOption, 0, len(req.Options))
	for _, option := range req.Options {
		options = append(options, agentmode.PermissionOption{
			OptionID: option.OptionID,
			Kind:     option.Kind,
		})
	}

	return agentmode.SelectAllowOption(options)
}

func selectRemotePermissionOption(req device.PermissionRequestPayload) (string, bool) {
	options := make([]agentmode.PermissionOption, 0, len(req.Options))
	for _, option := range req.Options {
		options = append(options, agentmode.PermissionOption{
			OptionID: option.OptionID,
			Kind:     option.Kind,
		})
	}

	return agentmode.SelectAllowOption(options)
}

func formatFileReferences(files []chatFileInfo) string {
	if len(files) == 0 {
		return ""
	}

	refs := make([]string, 0, len(files))
	for _, file := range files {
		filename := file.Name
		if filename == "" {
			filename = filepath.Base(file.Path)
		}
		refs = append(refs, "@"+filename)
	}
	return strings.Join(refs, " ")
}

func deviceHasAgent(dev device.Device, agentID string) bool {
	if agentID == "" {
		return true
	}
	for _, agentInfo := range dev.Agents {
		if agentInfo.ID == agentID {
			return true
		}
	}
	return false
}

func deviceAgentUnavailableMessage(dev device.Device, agentID string) string {
	available := make([]string, 0, len(dev.Agents))
	for _, agentInfo := range dev.Agents {
		if agentInfo.ID != "" {
			available = append(available, agentInfo.ID)
		}
	}
	if len(available) == 0 {
		return fmt.Sprintf("Agent not available on device: %s. The remote device did not advertise any agents; check ~/.device-executor/config.json on that device and reconnect device-executor.", agentID)
	}
	return fmt.Sprintf("Agent not available on device: %s. Available agents on this device: %s", agentID, strings.Join(available, ", "))
}

func deviceErrorMessage(err error) string {
	switch {
	case errors.Is(err, device.ErrDeviceNotFound):
		return "Device not found"
	case errors.Is(err, device.ErrDeviceOffline):
		return "Device is offline"
	case errors.Is(err, device.ErrSetupNotReady):
		return "Device setup is not ready"
	case errors.Is(err, device.ErrDeviceBusy):
		return "Device is busy"
	default:
		return err.Error()
	}
}

func toTaskFiles(files []chatFileInfo) []device.TaskFileInfo {
	result := make([]device.TaskFileInfo, 0, len(files))
	for _, file := range files {
		result = append(result, device.TaskFileInfo{
			Name: file.Name,
			Path: file.Path,
			Size: file.Size,
		})
	}
	return result
}

func newTaskRunID() string {
	return "task_" + strings.ReplaceAll(generateUUID(), "-", "")
}

func (s *Server) getRemoteSession(conversationID, deviceID, agentID string) string {
	s.remoteSessionsMu.RLock()
	defer s.remoteSessionsMu.RUnlock()

	byDevice := s.remoteAgentSessions[conversationID]
	if byDevice == nil {
		return ""
	}
	byAgent := byDevice[deviceID]
	if byAgent == nil {
		return ""
	}
	return byAgent[agentID]
}

func (s *Server) setRemoteSession(conversationID, deviceID, agentID, sessionID string) {
	s.remoteSessionsMu.Lock()
	defer s.remoteSessionsMu.Unlock()

	byDevice := s.remoteAgentSessions[conversationID]
	if byDevice == nil {
		byDevice = make(map[string]map[string]string)
		s.remoteAgentSessions[conversationID] = byDevice
	}
	byAgent := byDevice[deviceID]
	if byAgent == nil {
		byAgent = make(map[string]string)
		byDevice[deviceID] = byAgent
	}
	byAgent[agentID] = sessionID
}

func (s *Server) clearRemoteSessionsForDevice(deviceID string) {
	if deviceID == "" {
		return
	}

	s.remoteSessionsMu.Lock()
	defer s.remoteSessionsMu.Unlock()

	for conversationID, byDevice := range s.remoteAgentSessions {
		delete(byDevice, deviceID)
		if len(byDevice) == 0 {
			delete(s.remoteAgentSessions, conversationID)
		}
	}
}

func contextWithoutCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
