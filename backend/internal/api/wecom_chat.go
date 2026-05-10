package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/jsonrpc"
	"github.com/pengmide/lumi/internal/storage"
	"github.com/pengmide/lumi/internal/wecom"
)

type wecomChatRuntime struct {
	config        *config.Config
	agents        *agent.Manager
	conversations *conversation.Manager

	agentSessions map[string]map[string]string
	initialized   map[string]bool
	cron          *lumicron.Service
	mu            sync.Mutex
}

func newWeComChatRuntime(cfg *config.Config, cronService *lumicron.Service) *wecomChatRuntime {
	return &wecomChatRuntime{
		config:        cfg,
		agents:        agent.NewManager(cfg),
		conversations: conversation.NewManager(),
		agentSessions: make(map[string]map[string]string),
		initialized:   make(map[string]bool),
		cron:          cronService,
	}
}

func (r *wecomChatRuntime) RunWeComChat(ctx context.Context, input wecom.ChatRunInput, sink wecom.ChatEventSink) error {
	if input.ConversationID == "" || input.WorkspaceID == "" || input.WorkspacePath == "" || input.AgentID == "" || input.ConversationStore == nil {
		return errors.New("invalid wecom chat input")
	}

	conv, isNew, err := r.ensureConversation(input)
	if err != nil {
		return err
	}

	agentProc, err := r.agents.Get(input.AgentID)
	if err != nil {
		return r.emitError(sink, "Failed to get agent: "+err.Error())
	}
	agentProc.SetWorkingDir(input.WorkspacePath)

	if err := r.ensureInitialized(input.AgentID, sink); err != nil {
		return err
	}

	sessionID, _, err := r.ensureAgentSession(input, sink)
	if err != nil {
		return err
	}

	files := make([]conversation.MessageFile, 0, len(input.Files))
	for _, file := range input.Files {
		files = append(files, conversation.MessageFile{
			Name: file.Name,
			Path: file.Path,
			Size: file.Size,
		})
	}

	r.conversations.AddUserMessage(input.ConversationID, input.Message, files)
	r.conversations.SetSessionID(input.ConversationID, sessionID)

	if err := sink.Emit(wecom.ChatEvent{
		Name: "session",
		Data: map[string]any{
			"conversationId": input.ConversationID,
			"sessionId":      sessionID,
			"agent":          input.AgentID,
			"isNew":          isNew,
		},
	}); err != nil {
		return err
	}
	if err := sink.Emit(wecom.ChatEvent{
		Name: "status",
		Data: map[string]string{"message": "Processing..."},
	}); err != nil {
		return err
	}

	streamItems := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolCallMap := make(map[string]int)
	autoPermissionErr := ""

	cleanupNotification := agentProc.OnNotification(func(msg *jsonrpc.Message) {
		_ = r.handleWeComNotification(msg, sink, &streamItems, accumulator, toolCallMap)
	})
	defer cleanupNotification()

	cleanupPermission := agentProc.OnPermission(func(req *agent.PermissionRequest) {
		_ = sink.Emit(wecom.ChatEvent{Name: "permission_request", Data: req})

		optionID := firstAllowOption(req.Options)
		if optionID == "" {
			autoPermissionErr = "permission request does not allow auto approval"
			optionID = firstFallbackOption(req.Options)
			if optionID != "" {
				agentProc.ConfirmPermission(req.ToolCall.ToolCallID, optionID)
			}
			_ = agentProc.Notify("session/cancel", map[string]string{"sessionId": req.SessionID})
			_ = sink.Emit(wecom.ChatEvent{Name: "error", Data: map[string]string{"message": autoPermissionErr}})
			return
		}
		agentProc.ConfirmPermission(req.ToolCall.ToolCallID, optionID)
	})
	defer cleanupPermission()

	stopCancelWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = agentProc.Notify("session/cancel", map[string]string{"sessionId": sessionID})
		case <-stopCancelWatcher:
		}
	}()
	defer close(stopCancelWatcher)

	promptText := input.Message
	promptText = lumicron.WithAgentToolInstructionsForContext(promptText, lumicron.ToolContext{
		APIBase:        lumiAPIBaseForConfig(r.config),
		Channel:        lumicron.ChannelWeCom,
		ConversationID: input.ConversationID,
		AgentID:        input.AgentID,
		WorkspaceID:    input.WorkspaceID,
		WorkspacePath:  input.WorkspacePath,
		Target:         input.CronTarget,
	})
	if input.PromptPrefix != "" {
		promptText = input.PromptPrefix + "\n\n" + promptText
	}

	response, err := agentProc.Request("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": promptText}},
	})
	if err != nil {
		if autoPermissionErr != "" {
			return nil
		}
		return r.emitError(sink, err.Error())
	}

	r.finalizeAssistantStream(conv.ID, input.AgentID, streamItems, accumulator)
	if err := r.persistConversation(conv.ID, input.ConversationStore); err != nil {
		return err
	}

	if autoPermissionErr != "" {
		return nil
	}

	var result map[string]any
	response.ParseResult(&result)
	if result == nil {
		result = make(map[string]any)
	}
	if result["stopReason"] == nil {
		result["stopReason"] = "end_turn"
	}
	if err := sink.Emit(wecom.ChatEvent{Name: "done", Data: result}); err != nil {
		return err
	}
	return nil
}

func (r *wecomChatRuntime) StopAgent(agentID string) error {
	_ = r.agents.Stop(agentID)

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.initialized, agentID)
	for convID, sessions := range r.agentSessions {
		delete(sessions, agentID)
		if len(sessions) == 0 {
			delete(r.agentSessions, convID)
		}
	}
	return nil
}

func (r *wecomChatRuntime) Shutdown() error {
	return r.agents.Shutdown()
}

func (r *wecomChatRuntime) ensureConversation(input wecom.ChatRunInput) (*conversation.Conversation, bool, error) {
	if conv := r.conversations.Get(input.ConversationID); conv != nil {
		return conv, false, nil
	}

	stored, err := input.ConversationStore.Load(input.ConversationID)
	switch {
	case err == nil:
		conv := r.restoreStoredConversation(stored)
		return conv, false, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, false, err
	}

	conv := r.conversations.Create(input.ConversationID, input.AgentID, input.WorkspaceID)
	return conv, true, nil
}

func (r *wecomChatRuntime) restoreStoredConversation(session *storage.StoredSession) *conversation.Conversation {
	conv := r.conversations.Create(session.ID, session.ActiveAgent, session.WorkspaceID)
	conv.Messages = append([]conversation.Message(nil), session.Messages...)
	conv.ActiveAgent = session.ActiveAgent
	conv.WorkspaceID = session.WorkspaceID
	conv.CreatedAt = session.CreatedAt

	r.mu.Lock()
	if _, ok := r.agentSessions[session.ID]; !ok {
		r.agentSessions[session.ID] = make(map[string]string)
	}
	r.mu.Unlock()
	return conv
}

func (r *wecomChatRuntime) persistConversation(convID string, store wecom.HiddenConversationStore) error {
	conv := r.conversations.Get(convID)
	if conv == nil {
		return nil
	}
	session := &storage.StoredSession{
		ID:          conv.ID,
		Title:       storage.GenerateTitle(conv.Messages),
		Messages:    append([]conversation.Message(nil), conv.Messages...),
		ActiveAgent: conv.ActiveAgent,
		WorkspaceID: conv.WorkspaceID,
		CreatedAt:   conv.CreatedAt,
		UpdatedAt:   time.Now().UnixMilli(),
	}
	return store.Save(session)
}

func (r *wecomChatRuntime) ensureInitialized(agentID string, sink wecom.ChatEventSink) error {
	r.mu.Lock()
	initialized := r.initialized[agentID]
	r.mu.Unlock()
	if initialized {
		return nil
	}

	if err := sink.Emit(wecom.ChatEvent{
		Name: "status",
		Data: map[string]string{"message": fmt.Sprintf("Initializing %s...", agentID)},
	}); err != nil {
		return err
	}
	injectLumiAgentEnv(r.config, agentID, lumiAPIBaseForConfig(r.config))
	if _, err := r.agents.Request(agentID, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]bool{"readTextFile": true, "writeTextFile": true},
		},
		"clientInfo": map[string]string{"name": "lumi-go-wecom", "version": "0.1.0"},
	}); err != nil {
		return r.emitError(sink, err.Error())
	}

	r.mu.Lock()
	r.initialized[agentID] = true
	r.mu.Unlock()
	return nil
}

func (r *wecomChatRuntime) ensureAgentSession(input wecom.ChatRunInput, sink wecom.ChatEventSink) (string, bool, error) {
	r.mu.Lock()
	sessions := r.agentSessions[input.ConversationID]
	if sessions == nil {
		sessions = make(map[string]string)
		r.agentSessions[input.ConversationID] = sessions
	}
	sessionID := ""
	if !input.NewSession {
		sessionID = sessions[input.AgentID]
	}
	r.mu.Unlock()
	if sessionID != "" {
		return sessionID, false, nil
	}

	result, err := r.agents.Request(input.AgentID, "session/new", map[string]any{
		"cwd":        input.WorkspacePath,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", false, r.emitError(sink, err.Error())
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		return "", false, r.emitError(sink, "invalid session/new response")
	}
	sessionID, _ = resultMap["sessionId"].(string)
	if sessionID == "" {
		return "", false, r.emitError(sink, "session/new response missing sessionId")
	}
	if input.SessionModeOverride != "" {
		if _, err := r.agents.Request(input.AgentID, "session/set_mode", map[string]any{
			"sessionId": sessionID,
			"modeId":    input.SessionModeOverride,
		}); err != nil {
			return "", false, r.emitError(sink, err.Error())
		}
	}

	r.mu.Lock()
	if !input.NewSession {
		r.agentSessions[input.ConversationID][input.AgentID] = sessionID
	}
	r.mu.Unlock()
	return sessionID, true, nil
}

func (r *wecomChatRuntime) finalizeAssistantStream(convID, agentID string, streamItems []streamItem, accumulator *streamAccumulator) {
	if accumulator != nil {
		accumulator.FlushText(&streamItems)
		accumulator.SetText("")
	}
	for _, item := range streamItems {
		if item.Type == "text" {
			r.conversations.AddAssistantMessage(convID, item.Text, agentID)
			continue
		}
		if item.Tool != nil {
			r.conversations.AddToolCall(convID, item.Tool, agentID)
		}
	}
}

func (r *wecomChatRuntime) handleWeComNotification(
	msg *jsonrpc.Message,
	sink wecom.ChatEventSink,
	streamItems *[]streamItem,
	accumulator *streamAccumulator,
	toolCallMap map[string]int,
) error {
	if msg.Method != "session/update" {
		return nil
	}

	var params struct {
		Update sessionUpdate `json:"update"`
	}
	if err := msg.ParseParams(&params); err != nil {
		return nil
	}

	update := params.Update
	switch update.SessionUpdate {
	case "agent_message_chunk":
		if text := extractTextContent(update.Content); text != "" {
			visibleText, _ := accumulator.AddMessageChunk(text, streamItems)
			if visibleText == "" {
				return nil
			}
			update.Content = map[string]any{"type": "text", "text": visibleText}
			return sink.Emit(wecom.ChatEvent{Name: "update", Data: map[string]any{"update": toWeComUpdate(update)}})
		}
		return nil

	case "agent_thought_chunk":
		return nil

	case "tool_call", "tool_call_update":
		accumulator.FlushText(streamItems)
		accumulator.SetText("")

		toolID := update.ToolCallID
		if toolID == "" {
			return nil
		}
		toolName := update.Kind
		if update.Meta != nil && update.Meta.ClaudeCode != nil && update.Meta.ClaudeCode.ToolName != "" {
			toolName = update.Meta.ClaudeCode.ToolName
		}
		title := update.Title
		if title == "" {
			title = toolID
		}
		status := "pending"
		hasError := update.Error != "" || (update.Meta != nil && update.Meta.ClaudeCode != nil && update.Meta.ClaudeCode.Error != "")
		if hasError {
			status = "error"
		} else if update.Status == "completed" {
			status = "completed"
		}
		input := extractInput(update.RawInput)
		output, errMsg := extractOutput(update)
		description := ""
		if update.Status != "completed" {
			description = extractDescription(update.Content)
		}
		var rawInputJSON string
		if len(update.RawInput) > 0 {
			if data, err := json.Marshal(update.RawInput); err == nil {
				rawInputJSON = string(data)
			}
		}
		toolCall := &conversation.ToolCallInfo{
			ToolCallID:  toolID,
			ToolName:    toolName,
			Kind:        update.Kind,
			Title:       title,
			Description: description,
			Status:      status,
			Input:       input,
			RawInput:    rawInputJSON,
			Output:      output,
			Error:       errMsg,
		}
		if idx, ok := toolCallMap[toolID]; ok {
			(*streamItems)[idx] = streamItem{Type: "tool", Tool: toolCall}
		} else {
			toolCallMap[toolID] = len(*streamItems)
			*streamItems = append(*streamItems, streamItem{Type: "tool", Tool: toolCall})
		}
		return sink.Emit(wecom.ChatEvent{Name: "tool_call", Data: map[string]any{
			"toolCallId":    toolID,
			"toolName":      toolName,
			"kind":          update.Kind,
			"title":         title,
			"description":   description,
			"status":        status,
			"input":         input,
			"rawInput":      rawInputJSON,
			"output":        output,
			"error":         errMsg,
			"sessionUpdate": update.SessionUpdate,
		}})

	default:
		return sink.Emit(wecom.ChatEvent{Name: "update", Data: map[string]any{"update": toWeComUpdate(update)}})
	}
}

func (r *wecomChatRuntime) emitError(sink wecom.ChatEventSink, message string) error {
	if err := sink.Emit(wecom.ChatEvent{Name: "error", Data: map[string]string{"message": message}}); err != nil {
		return err
	}
	return errors.New(message)
}

func toWeComUpdate(update sessionUpdate) map[string]any {
	return toWeChatUpdate(update)
}
