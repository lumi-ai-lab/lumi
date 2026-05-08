package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/agentmode"
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
		"Review @claude and @src/app.ts plus @README.md and @src/app.ts again",
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

func TestPrepareChatInjectsCronProtocolForNaturalLanguage(t *testing.T) {
	server := newTestAPIServer(t)

	prepared, err := server.prepareChat(context.Background(), chatRequest{
		Message:        "现在我们有哪些定时任务?",
		ConversationID: "conv-1",
		WorkspaceID:    "default",
	})
	if err != nil {
		t.Fatalf("prepareChat() error = %v", err)
	}
	if !strings.Contains(prepared.PromptText, "Lumi scheduled task protocol:") {
		t.Fatalf("prepared.PromptText missing cron protocol: %q", prepared.PromptText)
	}
	if !strings.Contains(prepared.PromptText, "User: 现在我们有哪些定时任务?") {
		t.Fatalf("prepared.PromptText missing original user prompt: %q", prepared.PromptText)
	}
}

func TestHandleNotificationSeparatesThinkingFromAssistantText(t *testing.T) {
	server := newTestAPIServer(t)
	server.conversations.Create("conv", "claude", "default")
	items := make([]streamItem, 0)
	accumulator := &streamAccumulator{}
	toolMap := make(map[string]int)
	cronStream := &cronCommandStreamState{}
	events := make([]string, 0)

	send := func(event string, data any) {
		events = append(events, event)
	}

	server.handleNotification(testSessionUpdate(t, `{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello "}}}`), send, &items, accumulator, toolMap, "claude", cronStream)
	server.handleNotification(testSessionUpdate(t, `{"update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"secret"}}}`), send, &items, accumulator, toolMap, "claude", cronStream)
	server.handleNotification(testSessionUpdate(t, `{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"world"}}}`), send, &items, accumulator, toolMap, "claude", cronStream)

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
