package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/jsonrpc"
)

func TestShouldAutoApproveAgent(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.FindAgent("claude").SessionMode = agentmode.ClaudeModeBypassPermissions
	server.config.FindAgent("codex").SessionMode = agentmode.CodexModeYolo

	if !server.shouldAutoApproveAgent("claude") {
		t.Fatalf("shouldAutoApproveAgent(claude) = false, want true")
	}
	if !server.shouldAutoApproveAgent("codex") {
		t.Fatalf("shouldAutoApproveAgent(codex) = false, want true")
	}
	if server.shouldAutoApproveAgent("missing") {
		t.Fatalf("shouldAutoApproveAgent(missing) = true, want false")
	}
}

func TestCollectFileMentionsSkipsAgentsAndDeduplicates(t *testing.T) {
	server := newTestAPIServer(t)

	got := collectFileMentions(
		"Review @claude and @qwen plus @src/app.ts plus @README.md and @src/app.ts again",
		server.router,
	)

	want := []string{"src/app.ts", "README.md"}
	if len(got) != len(want) {
		t.Fatalf("len(mentions) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mentions[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestPersistAndRestoreConversationKeepsAgentSessions(t *testing.T) {
	server := newTestAPIServer(t)
	conv := server.conversations.Create("conv-1", "claude", "default")
	server.conversations.AddUserMessage(conv.ID, "hello", nil)
	server.conversations.SetSessionID(conv.ID, "local-session-1")
	server.agentSessions[conv.ID] = map[string]string{"claude": "local-session-1"}
	server.setRemoteSessionForProfile(conv.ID, "dev-1", "claude", "remote-session-1", "profile-digest")

	server.persistConversation(conv.ID)
	stored, err := server.sessionStore.Load(conv.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := stored.AgentSessions["claude"]; got != "local-session-1" {
		t.Fatalf("stored.AgentSessions[claude] = %q, want local-session-1", got)
	}
	if got := stored.RemoteAgentSessions["dev-1"]["claude"]; got != "remote-session-1" {
		t.Fatalf("stored.RemoteAgentSessions[dev-1][claude] = %q, want remote-session-1", got)
	}
	if got := stored.RemoteAgentSessionProfileDigests["dev-1"]["claude"]; got != "profile-digest" {
		t.Fatalf("stored remote profile digest = %q", got)
	}

	restored := newTestAPIServer(t)
	restored.restoreConversation(stored)
	if got := restored.agentSessions[conv.ID]["claude"]; got != "local-session-1" {
		t.Fatalf("restored local session = %q, want local-session-1", got)
	}
	if got := restored.getRemoteSession(conv.ID, "dev-1", "claude"); got != "remote-session-1" {
		t.Fatalf("restored remote session = %q, want remote-session-1", got)
	}
	if got := restored.conversations.Get(conv.ID).CurrentSessionID; got != "local-session-1" {
		t.Fatalf("restored current session = %q, want local-session-1", got)
	}
}

func TestClearRemoteSessionRemovesProfileDigestMapping(t *testing.T) {
	server := newTestAPIServer(t)
	server.setRemoteSessionForProfile("conv-1", "dev-1", "pi", "remote-session-1", "profile-digest")

	server.clearRemoteSession("conv-1", "dev-1", "pi")

	if got := server.getRemoteSession("conv-1", "dev-1", "pi"); got != "" {
		t.Fatalf("getRemoteSession() = %q, want empty", got)
	}
	if got := server.getRemoteSessionForProfile("conv-1", "dev-1", "pi", "profile-digest"); got != "" {
		t.Fatalf("getRemoteSessionForProfile() = %q, want empty", got)
	}
}

func TestIsRemoteSessionInvalidError(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		{message: "Invalid params: Unknown sessionId", want: true},
		{message: "Session not found", want: true},
		{message: "No session found for id", want: true},
		{message: "Invalid params: bad session mode", want: false},
	}
	for _, tt := range tests {
		if got := isRemoteSessionInvalidError(tt.message); got != tt.want {
			t.Fatalf("isRemoteSessionInvalidError(%q) = %v, want %v", tt.message, got, tt.want)
		}
	}
}

func TestPrepareChatRoutesQwenMention(t *testing.T) {
	server := newTestAPIServer(t)

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:     "@qwen hello",
		WorkspaceID: "default",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	if prepared.AgentID != "qwen" {
		t.Fatalf("prepared.AgentID = %q, want qwen", prepared.AgentID)
	}
	if !prepared.AgentChanged {
		t.Fatal("prepared.AgentChanged = false, want true")
	}
}

func TestServerWithMigratedConfigExposesBuiltInAgentsAndSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := &config.Config{
		Agents: []config.AgentConfig{
			{ID: "claude", Name: "Claude Code", Command: "echo"},
			{ID: "codex", Name: "Codex CLI", Command: "echo"},
		},
		DefaultAgent: "claude",
		Workspaces: []config.WorkspaceConfig{
			{ID: "default", Name: "Default", Path: home},
		},
		DefaultWorkspace: "default",
	}
	cfg.EnsureBuiltInDefaults()
	server := NewServer(cfg, nil)

	agentsRec := httptest.NewRecorder()
	agentsReq := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	server.Handler().ServeHTTP(agentsRec, agentsReq)
	if agentsRec.Code != http.StatusOK {
		t.Fatalf("/api/agents status = %d, body = %s", agentsRec.Code, agentsRec.Body.String())
	}
	if !strings.Contains(agentsRec.Body.String(), `"id":"qwen"`) {
		t.Fatalf("/api/agents missing qwen: %s", agentsRec.Body.String())
	}
	if !strings.Contains(agentsRec.Body.String(), `"id":"pi"`) || !strings.Contains(agentsRec.Body.String(), `"backend":"pi"`) {
		t.Fatalf("/api/agents missing pi backend: %s", agentsRec.Body.String())
	}

	setupRec := httptest.NewRecorder()
	setupReq := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	server.Handler().ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("/api/setup/status status = %d, body = %s", setupRec.Code, setupRec.Body.String())
	}
	if !strings.Contains(setupRec.Body.String(), `@qwen-code/qwen-code`) {
		t.Fatalf("/api/setup/status missing qwen package: %s", setupRec.Body.String())
	}
	if strings.Contains(setupRec.Body.String(), config.PiACPPackageSpec) || !strings.Contains(setupRec.Body.String(), `"command":"pi"`) {
		t.Fatalf("/api/setup/status missing pi setup items: %s", setupRec.Body.String())
	}
}

func TestPrepareChatLeavesLocalWorkspaceMentionsUnchanged(t *testing.T) {
	server := newTestAPIServer(t)

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:     "Review @README.md",
		WorkspaceID: "default",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	if !strings.Contains(prepared.PromptText, "User: Review @README.md") {
		t.Fatalf("prepared.PromptText = %q, want original message preserved", prepared.PromptText)
	}
	if strings.Contains(prepared.PromptText, "Content of @README.md") {
		t.Fatalf("prepared.PromptText = %q, want local mention left unexpanded", prepared.PromptText)
	}
}

func TestPrepareChatInjectsCronToolInstructionsForNaturalLanguage(t *testing.T) {
	server := newTestAPIServer(t)

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:        "现在我们有哪些定时任务?",
		ConversationID: "conv-1",
		WorkspaceID:    "default",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	if !strings.Contains(prepared.PromptText, "lumi cron add") {
		t.Fatalf("prepared.PromptText missing cron CLI instructions: %q", prepared.PromptText)
	}
	if strings.Contains(prepared.PromptText, "[CRON_") {
		t.Fatalf("prepared.PromptText contains old hidden protocol: %q", prepared.PromptText)
	}
	if !strings.Contains(prepared.PromptText, "User: 现在我们有哪些定时任务?") {
		t.Fatalf("prepared.PromptText missing original user prompt: %q", prepared.PromptText)
	}
}

func TestPrepareChatListsSkillsCommand(t *testing.T) {
	server := newTestAPIServer(t)
	workspace := server.config.Workspaces[0].Path
	writeAPITestSkill(t, filepath.Join(workspace, ".claude", "skills", "pdf-helper"), "PDF Helper", "Use PDFs", "# PDF\nInstructions")

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:     "/skills",
		WorkspaceID: "default",
		AgentID:     "claude",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	if !strings.Contains(prepared.PromptText, "/pdf-helper - PDF Helper: Use PDFs") {
		t.Fatalf("PromptText = %q, want skill list", prepared.PromptText)
	}
	if strings.Contains(prepared.PromptText, "Lumi scheduled task protocol:") {
		t.Fatalf("PromptText includes cron protocol for /skills: %q", prepared.PromptText)
	}
}

func TestPrepareChatInvokesSkillByHyphenOrUnderscore(t *testing.T) {
	server := newTestAPIServer(t)
	workspace := server.config.Workspaces[0].Path
	writeAPITestSkill(t, filepath.Join(workspace, ".claude", "skills", "pdf-helper"), "PDF Helper", "Use PDFs", "# PDF\nInstructions")

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:     "/pdf_helper report.pdf",
		WorkspaceID: "default",
		AgentID:     "claude",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	for _, want := range []string{"## Skill: PDF Helper", "## Description: Use PDFs", "# PDF\nInstructions", "## User Arguments:\nreport.pdf"} {
		if !strings.Contains(prepared.PromptText, want) {
			t.Fatalf("PromptText missing %q:\n%s", want, prepared.PromptText)
		}
	}
	if strings.Contains(prepared.PromptText, "Lumi scheduled task protocol:") {
		t.Fatalf("PromptText includes cron protocol for skill invocation: %q", prepared.PromptText)
	}
}

func TestPrepareChatLeavesUnknownSlashCommandForAgent(t *testing.T) {
	server := newTestAPIServer(t)

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:     "/missing_skill arg",
		WorkspaceID: "default",
		AgentID:     "claude",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	if !strings.Contains(prepared.PromptText, "User: /missing_skill arg") {
		t.Fatalf("prepared.PromptText = %q, want slash command passed through", prepared.PromptText)
	}
}

func TestHandleNotificationSeparatesThinkingFromAssistantText(t *testing.T) {
	server := newTestAPIServer(t)
	server.conversations.Create("conv", "claude", "default")
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)
	events := make([]string, 0)

	send := func(event string, data any) {
		events = append(events, event)
	}

	server.handleNotification(testSessionUpdate(t, `{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello "}}}`), send, &items, accumulator, toolMap, "claude")
	server.handleNotification(testSessionUpdate(t, `{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"secret"}}}`), send, &items, accumulator, toolMap, "claude")
	server.handleNotification(testSessionUpdate(t, `{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"world"}}}`), send, &items, accumulator, toolMap, "claude")

	server.finalizeAssistantStream("conv", "claude", items, accumulator)

	conv := server.conversations.Get("conv")
	if conv == nil {
		t.Fatal("conversation not found")
	}
	if len(conv.Messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3 (%+v)", len(conv.Messages), conv.Messages)
	}
	if conv.Messages[0].Type != "" || conv.Messages[0].Content != "hello " {
		t.Fatalf("messages[0] = %+v, want hello text", conv.Messages[0])
	}
	if conv.Messages[1].Type != "thinking" || conv.Messages[1].Content != "secret" {
		t.Fatalf("messages[1] = %+v, want thinking secret", conv.Messages[1])
	}
	if conv.Messages[1].Duration < 0 {
		t.Fatalf("thinking duration = %d, want non-negative", conv.Messages[1].Duration)
	}
	if conv.Messages[2].Type != "" || conv.Messages[2].Content != "world" {
		t.Fatalf("messages[2] = %+v, want world text", conv.Messages[2])
	}
	if len(events) != 4 || events[0] != "update" || events[1] != "thinking" || events[2] != "thinking" || events[3] != "update" {
		t.Fatalf("events = %v, want update/thinking/thinking/update", events)
	}
}

func TestStripAgentStartupBanner(t *testing.T) {
	startup := testPiStartupInfo()
	tests := []struct {
		name    string
		agentID string
		text    string
		want    string
	}{
		{
			name:    "complete startup info",
			agentID: "pi",
			text:    startup,
			want:    "",
		},
		{
			name:    "startup info followed by english body",
			agentID: "pi",
			text:    startup + "\nHi! I'm here to help you with your data analysis and coding tasks.",
			want:    "Hi! I'm here to help you with your data analysis and coding tasks.",
		},
		{
			name:    "startup info followed by chinese body",
			agentID: "pi",
			text:    "New version available: v0.79.10 (installed v0.78.0). Run: `npm i -g @earendil-works/pi-coding-agent`\n我来帮您分析。",
			want:    "我来帮您分析。",
		},
		{
			name:    "skills and extensions sections",
			agentID: "pi",
			text:    "pi v0.78.0\n## Skills\n- /path/to/.pi/skills/foo\n## Extensions\n- npm:pi-provider-litellm\n  - index.ts\n",
			want:    "",
		},
		{
			name:    "bullet body after startup delimiter",
			agentID: "pi",
			text:    "pi v0.78.0\n## Skills\n- /path/to/.pi/skills/foo\n---\n- Real answer item\n",
			want:    "- Real answer item\n",
		},
		{
			name:    "non pi agent unchanged",
			agentID: "claude",
			text:    startup,
			want:    startup,
		},
		{
			name:    "ordinary upgrade suggestion unchanged",
			agentID: "pi",
			text:    "我建议运行 npm i -g @earendil-works/pi-coding-agent 升级 PI。",
			want:    "我建议运行 npm i -g @earendil-works/pi-coding-agent 升级 PI。",
		},
		{
			name:    "ordinary skills answer unchanged",
			agentID: "pi",
			text:    "## Skills\n- Go\n- SQL\n这些技能可以用于数据分析。",
			want:    "## Skills\n- Go\n- SQL\n这些技能可以用于数据分析。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripAgentStartupBanner(tt.agentID, tt.text); got != tt.want {
				t.Fatalf("stripAgentStartupBanner() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleNotificationStripsPiStartupPreludeForWeb(t *testing.T) {
	server := newTestAPIServer(t)
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)
	events := make([]any, 0)

	send := func(event string, data any) {
		if event == "update" {
			events = append(events, data)
		}
	}

	server.handleNotification(testTextSessionUpdate(t, testPiStartupInfo()), send, &items, accumulator, toolMap, "pi")
	server.handleNotification(testTextSessionUpdate(t, testPiStartupInfo()+"\nHi! I'm here to help."), send, &items, accumulator, toolMap, "pi")
	server.handleNotification(testTextSessionUpdate(t, "pi v0.78.0\n"), send, &items, accumulator, toolMap, "claude")

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if len(items) != 0 {
		t.Fatalf("items before finalize = %+v, want no flushed stream items", items)
	}
	accumulator.Finish(&items)
	if len(items) != 1 || items[0].Text != "Hi! I'm here to help.pi v0.78.0\n" {
		t.Fatalf("items = %+v, want startup stripped only for pi agent", items)
	}
}

func TestHandleWeComNotificationStripsPiStartupBeforeAccumulator(t *testing.T) {
	runtime := &wecomChatRuntime{}
	sink := &recordingWeComSink{}
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)

	if err := runtime.handleWeComNotification(testTextSessionUpdate(t, testPiStartupInfo()), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeComNotification() error = %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("sink events = %+v, want no startup event", sink.events)
	}
	if accumulator.Text() != "" {
		t.Fatalf("accumulator text = %q, want empty", accumulator.Text())
	}
	accumulator.Finish(&items)
	if len(items) != 0 {
		t.Fatalf("items = %+v, want startup excluded from stream items", items)
	}
}

func TestHandleWeComNotificationKeepsPiBodyAndThoughts(t *testing.T) {
	runtime := &wecomChatRuntime{}
	sink := &recordingWeComSink{}
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)

	if err := runtime.handleWeComNotification(testTextSessionUpdate(t, testPiStartupInfo()+"\nHi! I'm here to help."), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeComNotification() error = %v", err)
	}
	if !sink.hasUpdateText("Hi! I'm here to help.") {
		t.Fatalf("sink events = %+v, want real assistant body", sink.events)
	}
	if sink.hasUpdateText("pi v0.78.0") || sink.hasUpdateText("## Skills") || sink.hasUpdateText("New version available") {
		t.Fatalf("sink events = %+v, want startup banner stripped", sink.events)
	}
	if accumulator.Text() != "Hi! I'm here to help." {
		t.Fatalf("accumulator text = %q, want real body only", accumulator.Text())
	}

	if err := runtime.handleWeComNotification(testTextSessionUpdateKind(t, "agent_thought_chunk", testPiStartupInfo()), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeComNotification(thought) error = %v", err)
	}
	if !sink.hasUpdateText("pi v0.78.0") {
		t.Fatalf("sink events = %+v, want thought chunks left unfiltered", sink.events)
	}
	if accumulator.Text() != "Hi! I'm here to help." {
		t.Fatalf("accumulator text = %q, want thought chunk outside message accumulator", accumulator.Text())
	}
}

func TestWeComPiStartupPreludeDoesNotPersistToConversationStore(t *testing.T) {
	server := newTestAPIServer(t)
	runtime := server.wecomChat
	store := &memoryIMStore{}
	conv := runtime.conversations.Create("conv-pi-startup", "pi", "default")
	sink := &recordingWeComSink{}
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)

	if err := runtime.handleWeComNotification(testTextSessionUpdate(t, testPiStartupInfo()), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeComNotification(startup) error = %v", err)
	}
	if err := runtime.handleWeComNotification(testTextSessionUpdate(t, testPiStartupInfo()+"\nHi! I'm here to help."), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeComNotification(body) error = %v", err)
	}

	runtime.finalizeAssistantStream(conv.ID, "pi", items, accumulator)
	if err := runtime.persistConversation(conv.ID, store); err != nil {
		t.Fatalf("persistConversation() error = %v", err)
	}
	stored, err := store.Load(conv.ID)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", conv.ID, err)
	}
	if len(stored.Messages) != 1 {
		t.Fatalf("stored messages = %+v, want one assistant message", stored.Messages)
	}
	msg := stored.Messages[0]
	if msg.Role != "assistant" || msg.Agent != "pi" || msg.Content != "Hi! I'm here to help." {
		t.Fatalf("stored message = %+v, want pi assistant body only", msg)
	}
	for _, forbidden := range []string{"pi v0.78.0", "## Skills", "## Extensions", "New version available"} {
		if strings.Contains(msg.Content, forbidden) {
			t.Fatalf("stored message contains startup fragment %q: %q", forbidden, msg.Content)
		}
	}
}

func TestHandleWeChatNotificationStripsPiStartupBeforeAccumulator(t *testing.T) {
	runtime := &wechatChatRuntime{}
	sink := &recordingWeChatSink{}
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)

	if err := runtime.handleWeChatNotification(testTextSessionUpdate(t, testPiStartupInfo()), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeChatNotification() error = %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("sink events = %+v, want no startup event", sink.events)
	}
	if accumulator.Text() != "" {
		t.Fatalf("accumulator text = %q, want empty", accumulator.Text())
	}
	accumulator.Finish(&items)
	if len(items) != 0 {
		t.Fatalf("items = %+v, want startup excluded from stream items", items)
	}

	if err := runtime.handleWeChatNotification(testTextSessionUpdate(t, testPiStartupInfo()+"\n我来帮您分析。"), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeChatNotification(body) error = %v", err)
	}
	if !sink.hasUpdateText("我来帮您分析。") {
		t.Fatalf("sink events = %+v, want real assistant body", sink.events)
	}
	if sink.hasUpdateText("pi v0.78.0") || sink.hasUpdateText("## Extensions") || sink.hasUpdateText("New version available") {
		t.Fatalf("sink events = %+v, want startup banner stripped", sink.events)
	}
	if accumulator.Text() != "我来帮您分析。" {
		t.Fatalf("accumulator text = %q, want real body only", accumulator.Text())
	}
}

func TestWeChatPiStartupPreludeDoesNotPersistToConversationStore(t *testing.T) {
	server := newTestAPIServer(t)
	runtime := server.wechatChat
	store := &memoryIMStore{}
	conv := runtime.conversations.Create("wechat-pi-startup", "pi", "default")
	sink := &recordingWeChatSink{}
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)

	if err := runtime.handleWeChatNotification(testTextSessionUpdate(t, testPiStartupInfo()), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeChatNotification(startup) error = %v", err)
	}
	if err := runtime.handleWeChatNotification(testTextSessionUpdate(t, testPiStartupInfo()+"\n我来帮您分析。"), sink, &items, accumulator, toolMap, "pi"); err != nil {
		t.Fatalf("handleWeChatNotification(body) error = %v", err)
	}

	runtime.finalizeAssistantStream(conv.ID, "pi", items, accumulator)
	if err := runtime.persistConversation(conv.ID, store); err != nil {
		t.Fatalf("persistConversation() error = %v", err)
	}
	stored, err := store.Load(conv.ID)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", conv.ID, err)
	}
	if len(stored.Messages) != 1 {
		t.Fatalf("stored messages = %+v, want one assistant message", stored.Messages)
	}
	msg := stored.Messages[0]
	if msg.Role != "assistant" || msg.Agent != "pi" || msg.Content != "我来帮您分析。" {
		t.Fatalf("stored message = %+v, want pi assistant body only", msg)
	}
	for _, forbidden := range []string{"pi v0.78.0", "## Skills", "## Extensions", "New version available"} {
		if strings.Contains(msg.Content, forbidden) {
			t.Fatalf("stored message contains startup fragment %q: %q", forbidden, msg.Content)
		}
	}
}

func TestHiddenCronFinalizePersistsAssistantResultWithoutPrompt(t *testing.T) {
	server := newTestAPIServer(t)
	server.conversations.Create("conv", "claude", "default")
	items := []streamItem{{Type: "text", Text: "cron result"}}

	server.finalizeAssistantStream("conv", "claude", items, &streamAccumulator{})

	conv := server.conversations.Get("conv")
	if conv == nil {
		t.Fatal("conversation not found")
	}
	if len(conv.Messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1 (%+v)", len(conv.Messages), conv.Messages)
	}
	if conv.Messages[0].Role != "assistant" || conv.Messages[0].Content != "cron result" || conv.Messages[0].Hidden {
		t.Fatalf("message = %+v, want visible assistant cron result", conv.Messages[0])
	}
}

func TestAddChatUserMessagePersistsBeforeAgentCompletes(t *testing.T) {
	server := newTestAPIServer(t)
	server.conversations.Create("conv", "claude", "default")
	ctx := chatRuntimeContext{
		Request: chatRequest{
			Message: "create a scheduled task",
		},
		Prepared: &chatPrepared{
			ConvID:      "conv",
			AgentID:     "claude",
			WorkspaceID: "default",
		},
	}

	server.addChatUserMessage(ctx)

	stored, err := server.sessionStore.Load("conv")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Messages) != 1 || stored.Messages[0].Content != "create a scheduled task" {
		t.Fatalf("stored messages = %+v, want persisted user prompt", stored.Messages)
	}
}

func TestInlineThinkTagIsExtractedAndStripped(t *testing.T) {
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}

	visible, thinking := accumulator.AddMessageChunk("<think>secret</think>answer", &items)
	accumulator.Finish(&items)

	if visible != "answer" {
		t.Fatalf("visible = %q, want answer", visible)
	}
	if len(thinking) != 1 || thinking[0].Thinking == nil || thinking[0].Thinking.Content != "secret" {
		t.Fatalf("thinking = %+v, want secret", thinking)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (%+v)", len(items), items)
	}
	if items[0].Type != "thinking" || items[1].Type != "text" || items[1].Text != "answer" {
		t.Fatalf("items = %+v, want thinking then answer text", items)
	}
}

func TestOrphanClosingThinkTagIsStripped(t *testing.T) {
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}

	visible, thinking := accumulator.AddMessageChunk("secret reasoning</think>\nanswer", &items)
	accumulator.Finish(&items)

	if visible != "answer" {
		t.Fatalf("visible = %q, want answer", visible)
	}
	if len(thinking) != 0 {
		t.Fatalf("thinking = %+v, want no extracted thinking for orphan close", thinking)
	}
	if len(items) != 1 || items[0].Type != "text" || items[0].Text != "answer" {
		t.Fatalf("items = %+v, want answer text only", items)
	}
}

func testSessionUpdate(t *testing.T, params string) *jsonrpc.Message {
	t.Helper()
	return &jsonrpc.Message{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(params),
	}
}

func testTextSessionUpdate(t *testing.T, text string) *jsonrpc.Message {
	t.Helper()
	return testTextSessionUpdateKind(t, "agent_message_chunk", text)
}

func testTextSessionUpdateKind(t *testing.T, kind, text string) *jsonrpc.Message {
	t.Helper()
	params := map[string]any{
		"update": map[string]any{
			"sessionUpdate": kind,
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
		},
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal session update: %v", err)
	}
	return testSessionUpdate(t, string(data))
}

func testPiStartupInfo() string {
	return "pi v0.78.0\n" +
		"---\n\n" +
		"## Context\n" +
		"- AGENTS.md\n\n" +
		"## Skills\n" +
		"- /path/to/.pi/skills/pdf-helper\n\n" +
		"## Prompts\n" +
		"- analyze\n\n" +
		"## Extensions\n" +
		"- npm:pi-provider-litellm\n" +
		"  - index.ts\n\n" +
		"---\n" +
		"New version available: v0.79.10 (installed v0.78.0). Run: `npm i -g @earendil-works/pi-coding-agent`\n"
}

func writeAPITestSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
