package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/jsonrpc"
	"github.com/pengmide/lumi/internal/storage"
	"github.com/pengmide/lumi/internal/wechat"
	"github.com/pengmide/lumi/internal/wecom"
)

type imHiddenConversationStore interface {
	Load(id string) (*storage.StoredSession, error)
	Save(session *storage.StoredSession) error
}

type imRunInput struct {
	Message           string
	ConversationID    string
	WorkspaceID       string
	WorkspacePath     string
	DeviceID          string
	AgentID           string
	Prompt            string
	Files             []device.TaskFileInfo
	ConversationStore imHiddenConversationStore
	NewSession        bool
}

type imEventSink interface {
	Emit(name string, data any) error
}

type wecomSinkAdapter struct {
	sink wecom.ChatEventSink
}

func (s wecomSinkAdapter) Emit(name string, data any) error {
	if name == "update" {
		data = normalizeIMUpdateData(data, toWeComUpdate)
	}
	return s.sink.Emit(wecom.ChatEvent{Name: name, Data: data})
}

type wechatSinkAdapter struct {
	sink wechat.ChatEventSink
}

func (s wechatSinkAdapter) Emit(name string, data any) error {
	if name == "update" {
		data = normalizeIMUpdateData(data, toWeChatUpdate)
	}
	return s.sink.Emit(wechat.ChatEvent{Name: name, Data: data})
}

func normalizeIMUpdateData(data any, convert func(sessionUpdate) map[string]any) any {
	switch payload := data.(type) {
	case struct {
		Update sessionUpdate `json:"update"`
	}:
		return map[string]any{"update": convert(payload.Update)}
	case *struct {
		Update sessionUpdate `json:"update"`
	}:
		if payload != nil {
			return map[string]any{"update": convert(payload.Update)}
		}
	case map[string]any:
		switch update := payload["update"].(type) {
		case sessionUpdate:
			return map[string]any{"update": convert(update)}
		case map[string]any:
			return data
		}
	}
	return data
}

func (s *Server) RunWeComChat(ctx context.Context, input wecom.ChatRunInput, sink wecom.ChatEventSink) error {
	if input.ConversationID == "" || input.WorkspaceID == "" || input.AgentID == "" || input.ConversationStore == nil {
		return errors.New("invalid wecom chat input")
	}
	runtime, err := s.resolveWorkspaceRuntime(ctx, input.WorkspaceID, nil)
	if err != nil {
		_ = sink.Emit(wecom.ChatEvent{Name: "error", Data: runtimeErrorEventPayload(err)})
		return nil
	}
	if runtime.Mode == "remote" {
		return errors.New("workspace must be local or sandbox")
	}
	if runtime.Mode == "local" {
		input.WorkspacePath = runtime.WorkspacePath
		return s.wecomChat.RunWeComChat(ctx, input, sink)
	}
	return s.runIMDeviceChat(ctx, imRunInput{
		Message:        input.Message,
		ConversationID: input.ConversationID,
		WorkspaceID:    runtime.WorkspaceID,
		WorkspacePath:  runtime.WorkspacePath,
		DeviceID:       runtime.DeviceID,
		AgentID:        input.AgentID,
		Prompt: buildIMPrompt(input.PromptPrefix, input.Message, lumicron.ToolContext{
			APIBase:        lumiAPIBaseForConfig(s.config),
			Channel:        lumicron.ChannelWeCom,
			ConversationID: input.ConversationID,
			AgentID:        input.AgentID,
			WorkspaceID:    runtime.WorkspaceID,
			WorkspacePath:  runtime.WorkspacePath,
			Target:         input.CronTarget,
		}),
		Files:             wecomTaskFiles(input.Files),
		ConversationStore: input.ConversationStore,
		NewSession:        input.NewSession,
	}, wecomSinkAdapter{sink: sink})
}

func (s *Server) RunWeChatChat(ctx context.Context, input wechat.ChatRunInput, sink wechat.ChatEventSink) error {
	if input.ConversationID == "" || input.WorkspaceID == "" || input.AgentID == "" || input.ConversationStore == nil {
		return errors.New("invalid wechat chat input")
	}
	runtime, err := s.resolveWorkspaceRuntime(ctx, input.WorkspaceID, nil)
	if err != nil {
		_ = sink.Emit(wechat.ChatEvent{Name: "error", Data: runtimeErrorEventPayload(err)})
		return nil
	}
	if runtime.Mode == "remote" {
		return errors.New("workspace must be local or sandbox")
	}
	if runtime.Mode == "local" {
		input.WorkspacePath = runtime.WorkspacePath
		return s.wechatChat.RunWeChatChat(ctx, input, sink)
	}
	return s.runIMDeviceChat(ctx, imRunInput{
		Message:        input.Message,
		ConversationID: input.ConversationID,
		WorkspaceID:    runtime.WorkspaceID,
		WorkspacePath:  runtime.WorkspacePath,
		DeviceID:       runtime.DeviceID,
		AgentID:        input.AgentID,
		Prompt: buildIMPrompt(input.PromptPrefix, input.Message, lumicron.ToolContext{
			APIBase:        lumiAPIBaseForConfig(s.config),
			Channel:        lumicron.ChannelWeChat,
			ConversationID: input.ConversationID,
			AgentID:        input.AgentID,
			WorkspaceID:    runtime.WorkspaceID,
			WorkspacePath:  runtime.WorkspacePath,
			Target:         input.CronTarget,
		}),
		Files:             wechatTaskFiles(input.Files),
		ConversationStore: input.ConversationStore,
		NewSession:        input.NewSession,
	}, wechatSinkAdapter{sink: sink})
}

func (s *Server) runIMDeviceChat(ctx context.Context, input imRunInput, sink imEventSink) error {
	deviceID := input.DeviceID
	if deviceID == "" {
		_ = sink.Emit("error", map[string]string{"message": "Sandbox device is not ready"})
		return nil
	}
	deviceInfo, ok := s.devices.GetDevice(deviceID)
	if !ok || deviceInfo.Status == device.StatusOffline {
		_ = sink.Emit("error", map[string]string{"message": "Device is offline"})
		return nil
	}
	if !deviceInfo.SetupReady {
		_ = sink.Emit("error", map[string]string{"message": "Device setup is not ready"})
		return nil
	}
	if !deviceHasAgent(deviceInfo, input.AgentID) {
		_ = sink.Emit("error", map[string]string{"message": deviceAgentUnavailableMessage(deviceInfo, input.AgentID)})
		return nil
	}

	conv, err := restoreIMConversation(input)
	if err != nil {
		return err
	}
	isNew := len(conv.Messages) == 0
	conv.Messages = append(conv.Messages, conversation.Message{
		Role:      "user",
		Content:   input.Message,
		Files:     imMessageFiles(input.Files),
		Timestamp: time.Now().UnixMilli(),
	})

	taskID := newTaskRunID()
	remoteSessionID := s.getRemoteSession(input.ConversationID, deviceID, input.AgentID)
	if input.NewSession {
		remoteSessionID = ""
	}
	task := device.NewTaskRun(taskID, deviceID, input.ConversationID, input.AgentID, input.WorkspaceID, input.WorkspacePath)
	task.SessionID = remoteSessionID
	if err := s.devices.StartTask(task); err != nil {
		_ = sink.Emit("error", map[string]string{"message": deviceErrorMessage(err)})
		return nil
	}
	defer s.devices.FinishTask(task.ID)

	payload := device.TaskExecutePayload{
		ConversationID: input.ConversationID,
		AgentID:        input.AgentID,
		SessionID:      remoteSessionID,
		WorkspaceID:    input.WorkspaceID,
		WorkspacePath:  input.WorkspacePath,
		Prompt:         input.Prompt,
		Files:          input.Files,
	}
	if err := s.devices.SendToDevice(ctx, deviceID, device.MsgTaskExecute, task.ID, payload); err != nil {
		_ = sink.Emit("error", map[string]string{"message": err.Error()})
		return nil
	}

	streamItems, err := s.consumeIMDeviceTaskEvents(ctx, input, task, sink, isNew)
	if err != nil {
		return nil
	}
	appendStreamItems(&conv.Messages, input.AgentID, streamItems)
	return saveIMConversation(input.ConversationStore, conv, input.AgentID, input.WorkspaceID)
}

func (s *Server) consumeIMDeviceTaskEvents(ctx context.Context, input imRunInput, task *device.TaskRun, sink imEventSink, isNew bool) ([]streamItem, error) {
	streamItems := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolCallMap := make(map[string]int)
	pendingNotifications := make([]device.DeviceEvent, 0)
	sessionReady := task.SessionID != ""
	sessionAnnounced := false
	sessionTimer := time.NewTimer(30 * time.Second)
	defer sessionTimer.Stop()

	sendCancel := func(reason string) {
		_ = s.devices.SendToDevice(contextWithoutCancel(ctx), task.DeviceID, device.MsgTaskCancel, task.ID, device.TaskCancelPayload{
			SessionID: task.SessionID,
			Reason:    reason,
		})
	}
	announceSession := func(sessionID string) {
		_ = sink.Emit("session", map[string]any{
			"conversationId": input.ConversationID,
			"sessionId":      sessionID,
			"agent":          input.AgentID,
			"isNew":          isNew,
		})
		_ = sink.Emit("status", map[string]string{"message": "Processing..."})
		sessionAnnounced = true
	}
	handleNotification := func(event device.DeviceEvent) bool {
		payload, err := device.DecodePayload[device.TaskEventPayload](device.Envelope{Payload: event.Payload})
		if err != nil {
			_ = sink.Emit("error", map[string]string{"message": "Invalid device event"})
			return false
		}
		msg := &jsonrpc.Message{JSONRPC: payload.Notification.JSONRPC, Method: payload.Notification.Method, Params: payload.Notification.Params}
		s.handleNotification(msg, func(name string, data any) {
			_ = sink.Emit(name, data)
		}, &streamItems, accumulator, toolCallMap, input.AgentID)
		return true
	}

	if sessionReady {
		if !sessionTimer.Stop() {
			select {
			case <-sessionTimer.C:
			default:
			}
		}
		announceSession(task.SessionID)
	}

	for {
		select {
		case <-ctx.Done():
			sendCancel("client_disconnected")
			return nil, ctx.Err()
		case <-sessionTimer.C:
			if !sessionReady {
				_ = sink.Emit("error", map[string]string{"message": "Device did not create session"})
				sendCancel("session_timeout")
				return nil, errors.New("device did not create session")
			}
		case event := <-task.Events:
			switch event.Type {
			case device.DeviceEventSession:
				payload, err := device.DecodePayload[device.TaskSessionPayload](device.Envelope{Payload: event.Payload})
				if err != nil || payload.SessionID == "" {
					_ = sink.Emit("error", map[string]string{"message": "Device returned an invalid session"})
					sendCancel("invalid_session")
					return nil, errors.New("device returned an invalid session")
				}
				task.SessionID = payload.SessionID
				s.setRemoteSession(input.ConversationID, task.DeviceID, input.AgentID, payload.SessionID)
				sessionReady = true
				if !sessionTimer.Stop() {
					select {
					case <-sessionTimer.C:
					default:
					}
				}
				if !sessionAnnounced {
					announceSession(payload.SessionID)
				}
				for _, pending := range pendingNotifications {
					if !handleNotification(pending) {
						return nil, errors.New("invalid device event")
					}
				}
				pendingNotifications = nil
			case device.DeviceEventNotification:
				if !sessionReady {
					pendingNotifications = append(pendingNotifications, event)
					continue
				}
				if !handleNotification(event) {
					return nil, errors.New("invalid device event")
				}
			case device.DeviceEventPermissionRequest:
				if !sessionReady {
					_ = sink.Emit("error", map[string]string{"message": "Device protocol error"})
					sendCancel("protocol_error")
					return nil, errors.New("device protocol error")
				}
				payload, err := device.DecodePayload[device.PermissionRequestPayload](device.Envelope{Payload: event.Payload})
				if err != nil {
					_ = sink.Emit("error", map[string]string{"message": "Invalid permission request"})
					return nil, errors.New("invalid permission request")
				}
				optionID, ok := selectRemotePermissionOption(payload)
				if !ok {
					_ = sink.Emit("error", map[string]string{"message": "permission request does not allow auto approval"})
					sendCancel("permission_denied")
					return nil, errors.New("permission request does not allow auto approval")
				}
				if err := s.devices.SendToDevice(contextWithoutCancel(ctx), task.DeviceID, device.MsgPermissionConfirm, task.ID, device.PermissionConfirmPayload{
					ToolCallID: payload.ToolCall.ToolCallID,
					OptionID:   optionID,
				}); err != nil {
					_ = sink.Emit("error", map[string]string{"message": "Failed to auto-confirm permission"})
					return nil, err
				}
			case device.DeviceEventDone:
				if !sessionReady {
					_ = sink.Emit("error", map[string]string{"message": "Device protocol error"})
					sendCancel("protocol_error")
					return nil, errors.New("device protocol error")
				}
				accumulator.Finish(&streamItems)
				payload, err := device.DecodePayload[device.TaskDonePayload](device.Envelope{Payload: event.Payload})
				result := map[string]any{"stopReason": "end_turn"}
				if err == nil && len(payload.Result) > 0 {
					_ = json.Unmarshal(payload.Result, &result)
				}
				if result == nil {
					result = map[string]any{"stopReason": "end_turn"}
				}
				if result["stopReason"] == nil {
					result["stopReason"] = "end_turn"
				}
				_ = sink.Emit("done", result)
				return streamItems, nil
			case device.DeviceEventError:
				if event.Err != nil {
					_ = sink.Emit("error", map[string]string{"message": event.Err.Error()})
					return nil, event.Err
				}
				payload, err := device.DecodePayload[device.TaskErrorPayload](device.Envelope{Payload: event.Payload})
				if err != nil || payload.Message == "" {
					_ = sink.Emit("error", map[string]string{"message": "Device execution failed"})
					return nil, errors.New("device execution failed")
				}
				_ = sink.Emit("error", map[string]string{"message": payload.Message})
				return nil, errors.New(payload.Message)
			}
		}
	}
}

func restoreIMConversation(input imRunInput) (*conversation.Conversation, error) {
	conv := &conversation.Conversation{
		ID:          input.ConversationID,
		Messages:    []conversation.Message{},
		ActiveAgent: input.AgentID,
		WorkspaceID: input.WorkspaceID,
		CreatedAt:   time.Now().UnixMilli(),
	}
	stored, err := input.ConversationStore.Load(input.ConversationID)
	if err == nil {
		conv.Messages = append(conv.Messages, stored.Messages...)
		conv.ActiveAgent = stored.ActiveAgent
		if conv.ActiveAgent == "" {
			conv.ActiveAgent = input.AgentID
		}
		conv.WorkspaceID = stored.WorkspaceID
		if conv.WorkspaceID == "" {
			conv.WorkspaceID = input.WorkspaceID
		}
		conv.CreatedAt = stored.CreatedAt
		return conv, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return conv, nil
	}
	return nil, err
}

func saveIMConversation(store imHiddenConversationStore, conv *conversation.Conversation, agentID string, workspaceID string) error {
	session := &storage.StoredSession{
		ID:          conv.ID,
		Title:       storage.GenerateTitle(conv.Messages),
		Messages:    append([]conversation.Message(nil), conv.Messages...),
		ActiveAgent: agentID,
		WorkspaceID: workspaceID,
		CreatedAt:   conv.CreatedAt,
		UpdatedAt:   time.Now().UnixMilli(),
	}
	return store.Save(session)
}

func appendStreamItems(messages *[]conversation.Message, agentID string, streamItems []streamItem) {
	now := time.Now().UnixMilli()
	for _, item := range streamItems {
		switch {
		case item.Type == "text":
			*messages = append(*messages, conversation.Message{Role: "assistant", Content: item.Text, Agent: agentID, Timestamp: now})
		case item.Type == "thinking" && item.Thinking != nil:
			*messages = append(*messages, conversation.Message{Role: "assistant", Type: "thinking", Content: item.Thinking.Content, Agent: agentID, Status: item.Thinking.Status, Duration: item.Thinking.Duration, Timestamp: now})
		case item.Tool != nil:
			*messages = append(*messages, conversation.Message{Role: "assistant", Agent: agentID, ToolCall: item.Tool, Timestamp: now})
		}
	}
}

func buildIMPrompt(prefix string, message string, toolContext lumicron.ToolContext) string {
	message = lumicron.WithAgentToolInstructionsForContext(message, toolContext)
	if prefix == "" {
		return message
	}
	return prefix + "\n\n" + message
}

func imMessageFiles(files []device.TaskFileInfo) []conversation.MessageFile {
	result := make([]conversation.MessageFile, 0, len(files))
	for _, file := range files {
		result = append(result, conversation.MessageFile{Name: file.Name, Path: file.Path, Size: file.Size})
	}
	return result
}

func wecomTaskFiles(files []wecom.ChatFileInfo) []device.TaskFileInfo {
	result := make([]device.TaskFileInfo, 0, len(files))
	for _, file := range files {
		result = append(result, device.TaskFileInfo{Name: file.Name, Path: file.Path, Size: file.Size})
	}
	return result
}

func wechatTaskFiles(files []wechat.ChatFileInfo) []device.TaskFileInfo {
	result := make([]device.TaskFileInfo, 0, len(files))
	for _, file := range files {
		result = append(result, device.TaskFileInfo{Name: file.Name, Path: file.Path, Size: file.Size})
	}
	return result
}
