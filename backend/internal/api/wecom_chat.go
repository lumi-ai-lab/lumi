package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/imdebug"
	"github.com/pengmide/lumi/internal/jsonrpc"
	"github.com/pengmide/lumi/internal/mcpstore"
	"github.com/pengmide/lumi/internal/requestercontext"
	"github.com/pengmide/lumi/internal/sessioninstruction"
	"github.com/pengmide/lumi/internal/storage"
	"github.com/pengmide/lumi/internal/wecom"
)

type wecomChatRuntime struct {
	config        *config.Config
	agents        *agent.Manager
	conversations *conversation.Manager
	mcpStore      *mcpstore.Store

	agentSessions              map[string]map[string]string
	agentSessionProfileDigests map[string]map[string]string
	agentSessionLoaded         map[string]map[string]bool
	initialized                map[string]bool
	agentInitializations       map[string]*wecomAgentInitialization
	cron                       *lumicron.Service
	mu                         sync.Mutex
}

type wecomAgentInitialization struct {
	done chan struct{}
	err  error
}

func newWeComChatRuntime(cfg *config.Config, cronService *lumicron.Service, mcp *mcpstore.Store) *wecomChatRuntime {
	return &wecomChatRuntime{
		config:                     cfg,
		agents:                     agent.NewManager(cfg),
		conversations:              conversation.NewManager(),
		mcpStore:                   mcp,
		agentSessions:              make(map[string]map[string]string),
		agentSessionProfileDigests: make(map[string]map[string]string),
		agentSessionLoaded:         make(map[string]map[string]bool),
		initialized:                make(map[string]bool),
		agentInitializations:       make(map[string]*wecomAgentInitialization),
		cron:                       cronService,
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
	agentChanged := shouldInjectIMAgentContext(conv.Messages, input.AgentID)
	turnContext := ""
	if agentChanged {
		if contextSummary := r.conversations.GetContextSummary(input.ConversationID, 10); contextSummary != "" {
			turnContext = contextSummary
		}
	}
	r.conversations.SetActiveAgent(input.ConversationID, input.AgentID)

	agentProc, err := r.agents.Get(input.AgentID)
	if err != nil {
		return r.emitError(sink, "Failed to get agent: "+err.Error())
	}
	agentProc.SetWorkingDir(input.WorkspacePath)

	if err := r.ensureInitialized(input.AgentID, input.WorkspaceID, input.WorkspacePath, sink); err != nil {
		return err
	}
	profile := buildIMSessionInstructionProfile(input.PromptPrefix, lumicron.ToolContext{
		Channel: lumicron.ChannelWeCom, ConversationID: input.ConversationID,
		AgentID: input.AgentID, WorkspaceID: input.WorkspaceID,
	})
	support := agentProc.SessionInstructionSupport()
	if !support.SupportsProfile() {
		return r.emitError(sink, "ACP adapter does not support recoverable Lumi Session instructions")
	}

	sessionID, _, err := r.ensureAgentSession(input, profile, support, sink)
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
		_ = r.handleWeComNotification(msg, sink, &streamItems, accumulator, toolCallMap, input.AgentID)
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

	promptParams := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": promptText}},
	}
	if input.RequesterContext != nil {
		bridge, bridgeErr := localRequesterContextBridge(input.WorkspaceID, input.AgentID)
		if bridgeErr != nil {
			return r.emitError(sink, bridgeErr.Error())
		}
		_, cleanup, bridgeErr := bridge.Write(sessionID, *input.RequesterContext)
		if bridgeErr != nil {
			return r.emitError(sink, bridgeErr.Error())
		}
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil {
				log.Printf("failed to clean requester context file: %v", cleanupErr)
			}
		}()
		promptParams["_meta"] = requestercontext.PromptMeta(*input.RequesterContext)
	}
	if err := sessioninstruction.ApplyProfile(promptParams, support, profile, sessioninstruction.PhasePrompt); err != nil {
		return r.emitError(sink, err.Error())
	}
	if turnContext != "" && !sessioninstruction.ApplyTurnContext(promptParams, support, turnContext) {
		promptText = sessioninstruction.WithUntrustedTurnContext(promptText, turnContext)
		promptParams["prompt"] = []map[string]string{{"type": "text", "text": promptText}}
	}

	response, err := agentProc.Request("session/prompt", promptParams)
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
	for convID, digests := range r.agentSessionProfileDigests {
		delete(digests, agentID)
		if len(digests) == 0 {
			delete(r.agentSessionProfileDigests, convID)
		}
	}
	for convID, loaded := range r.agentSessionLoaded {
		delete(loaded, agentID)
		if len(loaded) == 0 {
			delete(r.agentSessionLoaded, convID)
		}
	}
	return nil
}

func (r *wecomChatRuntime) Shutdown() error {
	return r.agents.Shutdown()
}

func (r *wecomChatRuntime) ensureConversation(input wecom.ChatRunInput) (*conversation.Conversation, bool, error) {
	if input.NewSession {
		conv := r.conversations.Create(input.ConversationID, input.AgentID, input.WorkspaceID)
		if stored, err := input.ConversationStore.Load(input.ConversationID); err == nil {
			conv.ActiveAgent = stored.ActiveAgent
			if conv.ActiveAgent == "" {
				conv.ActiveAgent = input.AgentID
			}
			conv.WorkspaceID = stored.WorkspaceID
			if conv.WorkspaceID == "" {
				conv.WorkspaceID = input.WorkspaceID
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
		return conv, true, nil
	}
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
	if sessionID := session.AgentSessions[session.ActiveAgent]; sessionID != "" {
		conv.CurrentSessionID = sessionID
	}

	r.mu.Lock()
	r.agentSessions[session.ID] = cloneAgentSessions(session.AgentSessions)
	r.agentSessionProfileDigests[session.ID] = cloneAgentSessions(session.AgentSessionProfileDigests)
	delete(r.agentSessionLoaded, session.ID)
	r.mu.Unlock()
	return conv
}

func (r *wecomChatRuntime) persistConversation(convID string, store wecom.HiddenConversationStore) error {
	conv := r.conversations.Get(convID)
	if conv == nil {
		return nil
	}
	debug := imdebug.ToolDebugEnabled(store, convID)
	session := &storage.StoredSession{
		ID:                         conv.ID,
		Title:                      storage.GenerateTitle(conv.Messages),
		Messages:                   append([]conversation.Message(nil), conv.Messages...),
		ActiveAgent:                conv.ActiveAgent,
		WorkspaceID:                conv.WorkspaceID,
		AgentSessions:              r.snapshotAgentSessions(conv.ID),
		AgentSessionProfileDigests: r.snapshotAgentSessionProfileDigests(conv.ID),
		IMDebug:                    debug,
		CreatedAt:                  conv.CreatedAt,
		UpdatedAt:                  time.Now().UnixMilli(),
	}
	return store.Save(session)
}

func (r *wecomChatRuntime) snapshotAgentSessions(convID string) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneAgentSessions(r.agentSessions[convID])
}

func (r *wecomChatRuntime) snapshotAgentSessionProfileDigests(convID string) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneAgentSessions(r.agentSessionProfileDigests[convID])
}

func (r *wecomChatRuntime) ensureInitialized(agentID string, workspaceID string, workspacePath string, sink wecom.ChatEventSink) error {
	r.mu.Lock()
	if r.initialized == nil {
		r.initialized = make(map[string]bool)
	}
	if r.agentInitializations == nil {
		r.agentInitializations = make(map[string]*wecomAgentInitialization)
	}
	if r.initialized[agentID] {
		bindingErr := validateLocalRequesterContextEnv(r.config, workspaceID, agentID)
		r.mu.Unlock()
		if bindingErr != nil {
			return r.emitError(sink, bindingErr.Error())
		}
		return nil
	}
	if initialization := r.agentInitializations[agentID]; initialization != nil {
		bindingErr := validateLocalRequesterContextEnv(r.config, workspaceID, agentID)
		r.mu.Unlock()
		if bindingErr != nil {
			return r.emitError(sink, bindingErr.Error())
		}
		<-initialization.done
		if initialization.err != nil {
			return r.emitError(sink, initialization.err.Error())
		}
		return nil
	}
	if err := injectLocalRequesterContextEnv(r.config, workspaceID, agentID); err != nil {
		r.mu.Unlock()
		return r.emitError(sink, err.Error())
	}
	injectLumiAgentEnv(r.config, agentID, lumiAPIBaseForWorkspace(r.config, workspaceID), workspaceID, workspacePath)
	initialization := &wecomAgentInitialization{done: make(chan struct{})}
	r.agentInitializations[agentID] = initialization
	r.mu.Unlock()

	initializeErr := r.initializeAgent(agentID, sink)
	r.mu.Lock()
	initialization.err = initializeErr
	if initializeErr == nil {
		r.initialized[agentID] = true
	}
	delete(r.agentInitializations, agentID)
	close(initialization.done)
	r.mu.Unlock()
	if initializeErr != nil {
		return r.emitError(sink, initializeErr.Error())
	}
	return nil
}

func (r *wecomChatRuntime) initializeAgent(agentID string, sink wecom.ChatEventSink) error {
	if err := sink.Emit(wecom.ChatEvent{
		Name: "status",
		Data: map[string]string{"message": fmt.Sprintf("Initializing %s...", agentID)},
	}); err != nil {
		return err
	}
	if _, err := r.agents.Request(agentID, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]bool{"readTextFile": true, "writeTextFile": true},
		},
		"clientInfo": map[string]string{"name": "lumi-go-wecom", "version": "0.1.0"},
	}); err != nil {
		return err
	}
	return nil
}

func (r *wecomChatRuntime) ensureAgentSession(input wecom.ChatRunInput, profile sessioninstruction.Profile, support sessioninstruction.Support, sink wecom.ChatEventSink) (string, bool, error) {
	r.mu.Lock()
	sessions := r.agentSessions[input.ConversationID]
	if sessions == nil {
		sessions = make(map[string]string)
		r.agentSessions[input.ConversationID] = sessions
	}
	sessionID := ""
	loaded := false
	if !input.NewSession {
		if r.agentSessionProfileDigests[input.ConversationID][input.AgentID] == profile.ProfileDigest {
			sessionID = sessions[input.AgentID]
			loaded = r.agentSessionLoaded[input.ConversationID][input.AgentID]
		}
	}
	r.mu.Unlock()
	if sessionID != "" {
		if loaded {
			return sessionID, false, nil
		}
		loadParams := map[string]any{
			"sessionId":  sessionID,
			"cwd":        input.WorkspacePath,
			"mcpServers": AgentMCPServersFor(r.config.Agents, input.AgentID, r.mcpStore),
		}
		if err := sessioninstruction.ApplyProfile(loadParams, support, profile, sessioninstruction.PhaseLoad); err != nil {
			return "", false, r.emitError(sink, err.Error())
		}
		if _, err := r.agents.Request(input.AgentID, "session/load", loadParams); err == nil {
			r.mu.Lock()
			if r.agentSessionLoaded[input.ConversationID] == nil {
				r.agentSessionLoaded[input.ConversationID] = make(map[string]bool)
			}
			r.agentSessionLoaded[input.ConversationID][input.AgentID] = true
			r.mu.Unlock()
			return sessionID, false, nil
		} else if !isRemoteSessionInvalidError(err.Error()) {
			return "", false, r.emitError(sink, err.Error())
		}
	}

	newParams := map[string]any{
		"cwd":        input.WorkspacePath,
		"mcpServers": AgentMCPServersFor(r.config.Agents, input.AgentID, r.mcpStore),
	}
	if err := sessioninstruction.ApplyProfile(newParams, support, profile, sessioninstruction.PhaseNew); err != nil {
		return "", false, r.emitError(sink, err.Error())
	}
	result, err := r.agents.Request(input.AgentID, "session/new", newParams)
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
	if r.shouldSetSessionMode(input.AgentID, input.SessionModeOverride) {
		if _, err := r.agents.Request(input.AgentID, "session/set_mode", map[string]any{
			"sessionId": sessionID,
			"modeId":    input.SessionModeOverride,
		}); err != nil {
			return "", false, r.emitError(sink, err.Error())
		}
	}

	r.mu.Lock()
	if !input.NewSession {
		if r.agentSessions[input.ConversationID] == nil {
			r.agentSessions[input.ConversationID] = make(map[string]string)
		}
		r.agentSessions[input.ConversationID][input.AgentID] = sessionID
		if r.agentSessionProfileDigests[input.ConversationID] == nil {
			r.agentSessionProfileDigests[input.ConversationID] = make(map[string]string)
		}
		r.agentSessionProfileDigests[input.ConversationID][input.AgentID] = profile.ProfileDigest
		if r.agentSessionLoaded[input.ConversationID] == nil {
			r.agentSessionLoaded[input.ConversationID] = make(map[string]bool)
		}
		r.agentSessionLoaded[input.ConversationID][input.AgentID] = true
	}
	r.mu.Unlock()
	return sessionID, true, nil
}

func (r *wecomChatRuntime) shouldSetSessionMode(agentID string, sessionMode string) bool {
	agentConfig := r.config.FindAgent(agentID)
	if agentConfig == nil {
		return false
	}
	backend := agentmode.DetectBackend(agentConfig.ID, agentConfig.Command, agentConfig.Args)
	return agentmode.ShouldSetACPMode(backend, sessionMode)
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
	agentID string,
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
			text = stripAgentStartupBanner(agentID, text)
			if text == "" {
				return nil
			}
			visibleText, _ := accumulator.AddMessageChunk(text, streamItems)
			if visibleText == "" {
				return nil
			}
			update.Content = map[string]any{"type": "text", "text": visibleText}
			return sink.Emit(wecom.ChatEvent{Name: "update", Data: map[string]any{"update": toWeComUpdate(update)}})
		}
		return nil

	case "agent_thought_chunk":
		if text := extractTextContent(update.Content); text != "" {
			update.Content = map[string]any{"type": "text", "text": text}
			return sink.Emit(wecom.ChatEvent{Name: "update", Data: map[string]any{"update": toWeComUpdate(update)}})
		}
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
