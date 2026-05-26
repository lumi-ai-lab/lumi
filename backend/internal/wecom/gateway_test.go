package wecom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/conversation"
	"github.com/pengmide/lumi/internal/imdebug"
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
	mu            sync.Mutex
	replies       []string
	media         []SendAction
	streams       []fakeStreamFrame
	failStreaming bool
	streamSeq     int
}

type fakeStreamFrame struct {
	ID      string
	Content string
	Finish  bool
}

func (s *fakeSender) Reply(_ context.Context, _ replyContext, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, content)
	return nil
}

func (s *fakeSender) ReplyStream(_ context.Context, _ replyContext, streamID, content string, finish bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failStreaming {
		return errors.New("stream failed")
	}
	s.streams = append(s.streams, fakeStreamFrame{ID: streamID, Content: content, Finish: finish})
	return nil
}

func (s *fakeSender) NewStreamID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamSeq++
	return "stream-test"
}

func (s *fakeSender) Send(_ context.Context, _ replyContext, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, content)
	return nil
}

func (s *fakeSender) ReplyMedia(_ context.Context, _ replyContext, action SendAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media = append(s.media, action)
	return nil
}

func (s *fakeSender) SendMedia(_ context.Context, _ replyContext, action SendAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func TestGatewayStreamsAgentMessageChunksWhenEnabled(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			for _, text := range []string{"hel", "hello"} {
				if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": text},
					},
				}}); err != nil {
					return err
				}
				time.Sleep(wecomStreamFlushInterval)
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 0 {
		t.Fatalf("replies = %v, want none", sender.replies)
	}
	if len(sender.streams) < 2 {
		t.Fatalf("streams = %v, want update and complete", sender.streams)
	}
	for _, frame := range sender.streams {
		if frame.ID != "stream-test" {
			t.Fatalf("stream id = %q, want stream-test", frame.ID)
		}
	}
	if sender.streams[0].Finish {
		t.Fatalf("first stream frame finish = true, want false")
	}
	last := sender.streams[len(sender.streams)-1]
	if !last.Finish || last.Content != "hello" {
		t.Fatalf("last stream frame = %+v, want finish hello", last)
	}
}

func TestGatewayStreamFlushesPendingChunkAfterInterval(t *testing.T) {
	sender := &fakeSender{}
	streamSender := newWeComStreamSender(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "")
	ctx := context.Background()

	streamSender.Update(ctx, "hello")
	streamSender.Update(ctx, "hello world")

	deadline := time.After(2 * wecomStreamFlushInterval)
	for {
		sender.mu.Lock()
		got := append([]fakeStreamFrame(nil), sender.streams...)
		sender.mu.Unlock()
		if len(got) >= 2 {
			if got[0].Content != "hello" || got[0].Finish {
				t.Fatalf("first stream frame = %+v, want unfinished hello", got[0])
			}
			if got[1].Content != "hello world" || got[1].Finish {
				t.Fatalf("second stream frame = %+v, want unfinished hello world", got[1])
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("streams = %v, want pending chunk flushed after interval", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestGatewayStreamFailureFallsBackToFinalReply(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
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
	sender := &fakeSender{failStreaming: true}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-fallback",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
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

func TestGatewayDebugCommandReturnsImmediately(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			t.Fatal("runner should not be called for /debug")
			return nil
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
		ConversationKey: "wecom:debug-command:user",
		MessageID:       "msg-debug-command",
		ReplyContext:    replyContext{ReqID: "req-debug-command", ChatID: "chat", UserID: "user"},
		Text:            "/debug all on",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], "thinking=on, tools=on") {
		t.Fatalf("replies = %v, want debug command reply", sender.replies)
	}
}

func TestGatewayHidesDebugOutputByDefault(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_thought_chunk",
					"content":       map[string]any{"type": "text", "text": "hidden thought"},
				},
			}})
			_ = sink.Emit(ChatEvent{Name: "tool_call", Data: map[string]any{
				"toolName": "Read",
				"status":   "completed",
				"input":    "secret.txt",
			}})
			_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "reply text"},
				},
			}})
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:debug-default:user",
		MessageID:       "msg-debug-default",
		ReplyContext:    replyContext{ReqID: "req-debug-default", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 1 || sender.replies[0] != "reply text" {
		t.Fatalf("replies = %v, want final reply only", sender.replies)
	}
	if strings.Contains(sender.replies[0], "hidden thought") || strings.Contains(sender.replies[0], "🪄") {
		t.Fatalf("default reply leaked debug output: %s", sender.replies[0])
	}
}

func TestGatewayDebugOutputUsesSessionSettings(t *testing.T) {
	longOutput := strings.Repeat("x", 400)
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_thought_chunk",
					"content":       map[string]any{"type": "text", "text": "hidden thought"},
				},
			}})
			_ = sink.Emit(ChatEvent{Name: "tool_call", Data: map[string]any{
				"toolName": "Bash",
				"status":   "completed",
				"output":   longOutput,
			}})
			_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "reply text"},
				},
			}})
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	conversationID := deriveConversationID("wecom:debug:user")
	session := storage.CreateSession(conversationID, "claude", "default")
	session.IMDebug.Thinking = true
	session.IMDebug.Tools = true
	if err := service.convStore.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

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
		ConversationKey: "wecom:debug:user",
		MessageID:       "msg-debug",
		ReplyContext:    replyContext{ReqID: "req-debug", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 3 {
		t.Fatalf("replies = %v, want thinking, tool, final reply", sender.replies)
	}
	if sender.replies[0] != "🤔\nhidden thought" {
		t.Fatalf("thinking reply = %q", sender.replies[0])
	}
	if !strings.HasPrefix(sender.replies[1], "🪄 Bash completed:") {
		t.Fatalf("tool reply = %q", sender.replies[1])
	}
	if sender.replies[2] != "reply text" {
		t.Fatalf("final reply = %q", sender.replies[2])
	}
	if len([]rune(sender.replies[1])) != 300 {
		t.Fatalf("debug tool summary was not truncated: len=%d reply=%q", len([]rune(sender.replies[1])), sender.replies[1])
	}
}

func TestGatewayDebugOutputHonorsIndependentSwitches(t *testing.T) {
	tests := []struct {
		name        string
		debug       storage.IMDebugSettings
		wantThought bool
		wantTool    bool
	}{
		{name: "thinking only", debug: storage.IMDebugSettings{Thinking: true}, wantThought: true},
		{name: "tools only", debug: storage.IMDebugSettings{Tools: true}, wantTool: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &scriptedRunner{
				run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
					_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
						"update": map[string]any{
							"sessionUpdate": "agent_thought_chunk",
							"content":       map[string]any{"type": "text", "text": "hidden thought"},
						},
					}})
					_ = sink.Emit(ChatEvent{Name: "tool_call", Data: map[string]any{
						"toolName": "Read",
						"status":   "completed",
						"input":    "file.go",
					}})
					_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "reply text"},
						},
					}})
					return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
				},
			}
			service := newTestService(t, runner)
			conversationKey := "wecom:debug:" + strings.ReplaceAll(tt.name, " ", "-")
			conversationID := deriveConversationID(conversationKey)
			session := storage.CreateSession(conversationID, "claude", "default")
			session.IMDebug = tt.debug
			if err := service.convStore.Save(session); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
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
				ConversationKey: conversationKey,
				MessageID:       "msg-debug-" + tt.name,
				ReplyContext:    replyContext{ReqID: "req-debug", ChatID: "chat", UserID: "user"},
				Text:            "hello",
				ReceivedAt:      time.Now().UnixMilli(),
			}, sender)
			if err != nil {
				t.Fatalf("handleInboundMessage() error = %v", err)
			}
			replies := strings.Join(sender.replies, "\n\n")
			if strings.Contains(sender.replies[len(sender.replies)-1], "🤔") || strings.Contains(sender.replies[len(sender.replies)-1], "🪄") {
				t.Fatalf("final reply mixed debug output: %q", sender.replies[len(sender.replies)-1])
			}
			if strings.Contains(replies, "🤔") != tt.wantThought {
				t.Fatalf("thinking presence = %v, want %v in:\n%s", strings.Contains(replies, "🤔"), tt.wantThought, replies)
			}
			if strings.Contains(replies, "🪄") != tt.wantTool {
				t.Fatalf("tool presence = %v, want %v in:\n%s", strings.Contains(replies, "🪄"), tt.wantTool, replies)
			}
		})
	}
}

func TestGatewayEventSinkIgnoresUsageUpdatesWithinThinkingSegment(t *testing.T) {
	var sent []imdebug.Segment
	sink := &gatewayEventSink{
		debug: storage.IMDebugSettings{Thinking: true},
		sendSegment: func(segment imdebug.Segment) error {
			sent = append(sent, segment)
			return nil
		},
	}
	_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_thought_chunk",
			"content":       map[string]any{"type": "text", "text": "first "},
		},
	}})
	_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
		"update": map[string]any{
			"sessionUpdate": "usage_update",
			"used":          nil,
			"size":          1000000,
		},
	}})
	_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_thought_chunk",
			"content":       map[string]any{"type": "text", "text": "second"},
		},
	}})
	if len(sent) != 0 {
		t.Fatalf("sent before segment boundary = %+v", sent)
	}
	_ = sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "reply"},
		},
	}})
	if len(sent) != 1 || sent[0].Kind != imdebug.SegmentDebug || sent[0].Text != "🤔\nfirst second" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestGatewayStopCommandCancelsRunningTask(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			close(started)
			<-ctx.Done()
			close(release)
			return nil
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}
	cfg := testGatewayConfig()

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
			ConversationKey: "wecom:stop",
			MessageID:       "msg-running",
			ReplyContext:    replyContext{ReqID: "req-running", ChatID: "chat", UserID: "user"},
			Text:            "run",
			ReceivedAt:      time.Now().UnixMilli(),
		}, sender)
	}()
	<-started

	if err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:stop",
		MessageID:       "msg-stop",
		ReplyContext:    replyContext{ReqID: "req-stop", ChatID: "chat", UserID: "user"},
		Text:            "/stop",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender); err != nil {
		t.Fatalf("handleInboundMessage(stop) error = %v", err)
	}

	<-release
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("running handleInboundMessage() error = %v, want context.Canceled", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	joined := strings.Join(sender.replies, "\n")
	if !strings.Contains(joined, "已请求停止当前任务。") {
		t.Fatalf("stop reply not observed: %v", sender.replies)
	}
	if strings.Contains(joined, busyReplyText) {
		t.Fatalf("stop command returned busy reply: %v", sender.replies)
	}
	if strings.Contains(joined, fallbackDoneText) {
		t.Fatalf("canceled run sent fallback completion: %v", sender.replies)
	}
}

func TestGatewayStopCommandWithoutRunningTask(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:stop:idle",
		MessageID:       "msg-stop-idle",
		ReplyContext:    replyContext{ReqID: "req-stop-idle", ChatID: "chat", UserID: "user"},
		Text:            " /stop ",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || sender.replies[0] != "当前没有正在处理的任务。" {
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

func TestGatewayNewCommandResetsAndNextMessageStartsNewSession(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "fresh reply"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	conversationKey := "wecom:new:reset"
	conversationID := deriveConversationID(conversationKey)
	seed := storage.CreateSession(conversationID, "codex", "default")
	seed.Messages = []conversation.Message{{Role: "user", Content: "old"}}
	seed.IMDebug = storage.IMDebugSettings{Thinking: true, Tools: true}
	if err := service.convStore.Save(seed); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: conversationKey,
		MessageID:       "msg-new",
		ReplyContext:    replyContext{ReqID: "req-new", ChatID: "chat", UserID: "user"},
		Text:            "/new",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage(/new) error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs after /new = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], "已重置当前会话") {
		t.Fatalf("replies = %v", sender.replies)
	}
	stored, err := service.convStore.Load(conversationID)
	if err != nil {
		t.Fatalf("Load(after /new) error = %v", err)
	}
	if len(stored.Messages) != 0 || stored.ActiveAgent != "codex" || !stored.IMDebug.Thinking || !stored.IMDebug.Tools || !stored.IMNewSessionPending {
		t.Fatalf("stored after /new = %+v", stored)
	}

	err = service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: conversationKey,
		MessageID:       "msg-after-new",
		ReplyContext:    replyContext{ReqID: "req-after-new", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage(normal) error = %v", err)
	}
	if len(runner.inputs) != 1 || !runner.inputs[0].NewSession || runner.inputs[0].AgentID != "codex" {
		t.Fatalf("runner inputs = %+v, want one codex NewSession input", runner.inputs)
	}
	stored, err = service.convStore.Load(conversationID)
	if err != nil {
		t.Fatalf("Load(after normal) error = %v", err)
	}
	if stored.IMNewSessionPending {
		t.Fatalf("IMNewSessionPending still true after successful run")
	}
}

func TestGatewayNewCommandFormatErrorSkipsRunner(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:new:format",
		MessageID:       "msg-new-format",
		ReplyContext:    replyContext{ReqID: "req-new-format", ChatID: "chat", UserID: "user"},
		Text:            "/new please",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(sender.replies) != 1 || sender.replies[0] != "格式：/new" {
		t.Fatalf("replies = %v", sender.replies)
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

func TestGatewayStreamHidesMediaProtocolUntilFinalSend(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "visible before\n"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content": map[string]any{
						"type": "text",
						"text": "visible before\n[LUMI_WECOM_SEND]\n{\"type\":\"image\",\"path\":\"chart.png\",\"caption\":\"chart\"}\n[/LUMI_WECOM_SEND]",
					},
				},
			}})
		},
	}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Path = root
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-media",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "send chart",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) == 0 {
		t.Fatal("streams = nil, want stream frames")
	}
	for _, frame := range sender.streams {
		if strings.Contains(frame.Content, "LUMI_WECOM_SEND") {
			t.Fatalf("stream leaked protocol block: %+v", frame)
		}
	}
	if len(sender.replies) != 1 || sender.replies[0] != "chart" {
		t.Fatalf("replies = %v, want media caption only", sender.replies)
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
