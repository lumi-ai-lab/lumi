package wecom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/storage"
)

type scriptedRunner struct {
	mu     sync.Mutex
	inputs []ChatRunInput
	run    func(context.Context, ChatRunInput, ChatEventSink) error
}

func (r *scriptedRunner) RunWeComChat(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, input, sink)
	}
	return nil
}

type fakeSender struct {
	replies []string
	media   []SendAction
}

func (s *fakeSender) Reply(_ context.Context, _ replyContext, content string) error {
	s.replies = append(s.replies, content)
	return nil
}

func (s *fakeSender) Send(_ context.Context, _ replyContext, content string) error {
	s.replies = append(s.replies, content)
	return nil
}

func (s *fakeSender) ReplyMedia(_ context.Context, _ replyContext, action SendAction) error {
	s.media = append(s.media, action)
	return nil
}

func (s *fakeSender) SendMedia(_ context.Context, _ replyContext, action SendAction) error {
	s.media = append(s.media, action)
	return nil
}

func TestGatewayHandlesPureTextReply(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if !strings.Contains(input.PromptPrefix, "LUMI_WECOM_SEND") {
				t.Fatalf("PromptPrefix missing protocol instruction: %q", input.PromptPrefix)
			}
			if input.Message != "hello" {
				t.Fatalf("Message = %q, want hello", input.Message)
			}
			if !strings.HasPrefix(input.ConversationID, "wecom_") {
				t.Fatalf("ConversationID = %q", input.ConversationID)
			}
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "reply text"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	cfg := Config{
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "default",
		AgentID:             "claude",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}
	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-1",
		ReplyContext:    replyContext{ReqID: "req-1", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 1 || sender.replies[0] != "reply text" {
		t.Fatalf("replies = %v", sender.replies)
	}
}

func TestGatewayAgentCommandListSkipsRunner(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:agent:list",
		MessageID:       "msg-agent-list",
		ReplyContext:    replyContext{ReqID: "req-agent-list", ChatID: "chat", UserID: "user"},
		Text:            " /agent ",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], "当前 Agent：claude") || !strings.Contains(sender.replies[0], "* codex") {
		t.Fatalf("replies = %v", sender.replies)
	}
}

func TestGatewayAgentCommandSwitchPersistsAndNextMessageUsesAgent(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			return sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "codex reply"},
				},
			}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	conversationKey := "wecom:agent:switch"

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: conversationKey,
		MessageID:       "msg-agent-switch",
		ReplyContext:    replyContext{ReqID: "req-agent-switch", ChatID: "chat", UserID: "user"},
		Text:            "/agent codex",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage(switch) error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs after switch = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || sender.replies[0] != "已切换当前 Agent 为 codex。" {
		t.Fatalf("switch replies = %v", sender.replies)
	}

	err = service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: conversationKey,
		MessageID:       "msg-after-switch",
		ReplyContext:    replyContext{ReqID: "req-after-switch", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage(normal) error = %v", err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].AgentID != "codex" {
		t.Fatalf("runner inputs = %+v, want one codex input", runner.inputs)
	}
	stored, err := service.convStore.Load(deriveConversationID(conversationKey))
	if err != nil {
		t.Fatalf("Load(stored conversation) error = %v", err)
	}
	if stored.ActiveAgent != "codex" || stored.WorkspaceID != "default" {
		t.Fatalf("stored = %+v, want active codex default workspace", stored)
	}
}

func TestGatewayAgentCommandFormatErrorSkipsRunner(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:agent:format",
		MessageID:       "msg-agent-format",
		ReplyContext:    replyContext{ReqID: "req-agent-format", ChatID: "chat", UserID: "user"},
		Text:            "/agent codex hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || sender.replies[0] != "格式：/agent 或 /agent <id>" {
		t.Fatalf("replies = %v", sender.replies)
	}
}

func TestGatewayAgentCommandHonorsWorkspaceWhitelist(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Agents = []string{"claude"}
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:agent:limited",
		MessageID:       "msg-agent-limited",
		ReplyContext:    replyContext{ReqID: "req-agent-limited", ChatID: "chat", UserID: "user"},
		Text:            "/agent codex",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], "未找到可用 Agent：codex") || !strings.Contains(sender.replies[0], "可用 Agent：claude") {
		t.Fatalf("replies = %v", sender.replies)
	}
}

func TestGatewayFallsBackWhenStoredAgentUnavailable(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Agents = []string{"claude"}
	conversationID := deriveConversationID("wecom:agent:fallback")
	if err := service.convStore.Save(storage.CreateSession(conversationID, "codex", "default")); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:agent:fallback",
		MessageID:       "msg-agent-fallback",
		ReplyContext:    replyContext{ReqID: "req-agent-fallback", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, &fakeSender{})
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].AgentID != "claude" {
		t.Fatalf("runner inputs = %+v, want fallback claude", runner.inputs)
	}
}

func TestGatewayHandlesMediaSendProtocol(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			return sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content": map[string]any{
						"type": "text",
						"text": "[LUMI_WECOM_SEND]\n{\"type\":\"image\",\"path\":\"chart.png\",\"caption\":\"chart\"}\n[/LUMI_WECOM_SEND]",
					},
				},
			}})
		},
	}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Path = root
	sender := &fakeSender{}

	cfg := Config{
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "default",
		AgentID:             "claude",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}
	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-2",
		ReplyContext:    replyContext{ReqID: "req-2", ChatID: "chat", UserID: "user"},
		Text:            "send chart",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 1 || sender.replies[0] != "chart" {
		t.Fatalf("replies = %v", sender.replies)
	}
	if len(sender.media) != 1 || sender.media[0].Type != "image" {
		t.Fatalf("media = %v", sender.media)
	}
}

func testGatewayConfig() Config {
	return Config{
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "default",
		AgentID:             "claude",
		ConnectTimeoutMs:    defaultConnectTimeoutMs,
		HeartbeatIntervalMs: defaultHeartbeatMs,
		MessageAckTimeoutMs: defaultMessageAckTimeoutMs,
	}
}
