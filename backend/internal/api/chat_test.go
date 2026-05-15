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

func TestServerWithMigratedConfigExposesQwenAgentAndSetup(t *testing.T) {
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

	setupRec := httptest.NewRecorder()
	setupReq := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	server.Handler().ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("/api/setup/status status = %d, body = %s", setupRec.Code, setupRec.Body.String())
	}
	if !strings.Contains(setupRec.Body.String(), `@qwen-code/qwen-code`) {
		t.Fatalf("/api/setup/status missing qwen package: %s", setupRec.Body.String())
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
