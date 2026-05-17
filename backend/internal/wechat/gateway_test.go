package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

func (r *scriptedRunner) RunWeChatChat(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, input, sink)
	}
	return nil
}

func TestGatewayHandlesPureTextReply(t *testing.T) {
	restoreTypingTestConfig(t, 10*time.Millisecond, 5*time.Millisecond, 24*time.Hour, 20*time.Millisecond)

	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if !strings.Contains(input.PromptPrefix, "LUMI_WECHAT_SEND") {
				t.Fatalf("PromptPrefix missing protocol instruction: %q", input.PromptPrefix)
			}
			if input.Message != "hello" {
				t.Fatalf("Message = %q, want hello", input.Message)
			}
			if !strings.HasPrefix(input.ConversationID, "wx_") {
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
			time.Sleep(25 * time.Millisecond)
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)

	var sentTexts []string
	var typingMu sync.Mutex
	var typingStatuses []int
	useHTTPClientFactory(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		switch req.URL.Path {
		case "/ilink/bot/getconfig":
			return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0,"typing_ticket":"ticket-1"}`), nil
		case "/ilink/bot/sendtyping":
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("Unmarshal(sendtyping body) error = %v", err)
			}
			typingMu.Lock()
			typingStatuses = append(typingStatuses, int(payload["status"].(float64)))
			typingMu.Unlock()
			return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
		case "/ilink/bot/sendmessage":
			sentTexts = append(sentTexts, string(body))
			return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.String())
			return nil, nil
		}
	}))

	cfg := Config{
		AccountID:   "wx-bot",
		BotToken:    "bot-token",
		BaseURL:     "https://wechat.test",
		WorkspaceID: "default",
		AgentID:     "claude",
	}
	err := service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
		ConversationKey: "user-1",
		MessageID:       "msg-1",
		ContextToken:    "ctx-1",
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sentTexts) == 0 || !strings.Contains(sentTexts[len(sentTexts)-1], `"text":"reply text"`) {
		t.Fatalf("unexpected sendmessage bodies: %v", sentTexts)
	}
	typingMu.Lock()
	defer typingMu.Unlock()
	if len(typingStatuses) < 3 {
		t.Fatalf("typingStatuses = %v, want active, active, cancel", typingStatuses)
	}
	if typingStatuses[0] != typingStatusActive || typingStatuses[len(typingStatuses)-1] != typingStatusCancel {
		t.Fatalf("typingStatuses = %v", typingStatuses)
	}
}

func TestGatewayAgentCommandListSkipsRunner(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	sentTexts := useSendTextRecorder(t)

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeChatInboundMessage{
		ConversationKey: "wx-agent-list",
		MessageID:       "msg-agent-list",
		ContextToken:    "ctx-agent-list",
		Text:            " /agent ",
		ReceivedAt:      time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(*sentTexts) != 1 || !strings.Contains((*sentTexts)[0], "当前 Agent：claude") || !strings.Contains((*sentTexts)[0], "* codex") {
		t.Fatalf("sentTexts = %v", *sentTexts)
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
	sentTexts := useSendTextRecorder(t)
	cfg := testGatewayConfig()
	conversationKey := "wx-agent-switch"

	err := service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
		ConversationKey: conversationKey,
		MessageID:       "msg-agent-switch",
		ContextToken:    "ctx-agent-switch",
		Text:            "/agent codex",
		ReceivedAt:      time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("handleInboundMessage(switch) error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs after switch = %d, want 0", len(runner.inputs))
	}
	if len(*sentTexts) != 1 || !strings.Contains((*sentTexts)[0], "已切换当前 Agent 为 codex。") {
		t.Fatalf("switch sentTexts = %v", *sentTexts)
	}

	err = service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
		ConversationKey: conversationKey,
		MessageID:       "msg-after-switch",
		ContextToken:    "ctx-after-switch",
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	})
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
	sentTexts := useSendTextRecorder(t)

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeChatInboundMessage{
		ConversationKey: "wx-agent-format",
		MessageID:       "msg-agent-format",
		ContextToken:    "ctx-agent-format",
		Text:            "/agent codex hello",
		ReceivedAt:      time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(*sentTexts) != 1 || !strings.Contains((*sentTexts)[0], "格式：/agent 或 /agent \\u003cid\\u003e") {
		t.Fatalf("sentTexts = %v", *sentTexts)
	}
}

func TestGatewayAgentCommandHonorsWorkspaceWhitelist(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Agents = []string{"claude"}
	sentTexts := useSendTextRecorder(t)

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeChatInboundMessage{
		ConversationKey: "wx-agent-limited",
		MessageID:       "msg-agent-limited",
		ContextToken:    "ctx-agent-limited",
		Text:            "/agent codex",
		ReceivedAt:      time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 0 {
		t.Fatalf("runner inputs = %d, want 0", len(runner.inputs))
	}
	if len(*sentTexts) != 1 || !strings.Contains((*sentTexts)[0], "未找到可用 Agent：codex") || !strings.Contains((*sentTexts)[0], "可用 Agent：claude") {
		t.Fatalf("sentTexts = %v", *sentTexts)
	}
}

func TestGatewayFallsBackWhenStoredAgentUnavailable(t *testing.T) {
	runner := &scriptedRunner{}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Agents = []string{"claude"}
	conversationID := deriveConversationID("wx-agent-fallback")
	if err := service.convStore.Save(storage.CreateSession(conversationID, "codex", "default")); err != nil {
		t.Fatalf("Save(seed) error = %v", err)
	}
	_ = useSendTextRecorder(t)

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeChatInboundMessage{
		ConversationKey: "wx-agent-fallback",
		MessageID:       "msg-agent-fallback",
		ContextToken:    "ctx-agent-fallback",
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 1 || runner.inputs[0].AgentID != "claude" {
		t.Fatalf("runner inputs = %+v, want fallback claude", runner.inputs)
	}
}

func TestGatewayHandlesAttachmentFailuresAndBusyState(t *testing.T) {
	t.Run("partial attachment failure still runs agent", func(t *testing.T) {
		runner := &scriptedRunner{
			run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
				if len(input.Files) != 1 {
					t.Fatalf("len(Files) = %d, want 1", len(input.Files))
				}
				if !strings.Contains(input.Message, "[WeChat attachments]") || !strings.Contains(input.Message, "- failed: bad.txt") {
					t.Fatalf("attachment block missing failure details:\n%s", input.Message)
				}
				if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "processed attachments"},
					},
				}}); err != nil {
					return err
				}
				return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
			},
		}
		service := newTestService(t, runner)

		var sentTexts []string
		useHTTPClientFactory(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/ilink/bot/getconfig":
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0,"typing_ticket":"ticket-1"}`), nil
			case "/ilink/bot/sendtyping":
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			case "/c2c/download":
				if strings.Contains(req.URL.RawQuery, "ok-file") {
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("\x89PNGstub"))}
					return resp, nil
				}
				return jsonResponse(http.StatusInternalServerError, `{"error":"download failed"}`), nil
			case "/ilink/bot/sendmessage":
				body, _ := io.ReadAll(req.Body)
				sentTexts = append(sentTexts, string(body))
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.String())
				return nil, nil
			}
		}))

		cfg := Config{AccountID: "wx-bot", BotToken: "bot-token", BaseURL: "https://wechat.test", WorkspaceID: "default", AgentID: "claude"}
		err := service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
			ConversationKey: "user-2",
			MessageID:       "msg-2",
			ContextToken:    "ctx-2",
			Text:            "please review",
			Attachments: []WeChatAttachment{
				{Name: "image.png", downloadQuery: "ok-file", aesKeyHex: "", Size: 10},
				{Name: "bad.txt", downloadQuery: "bad-file", aesKeyHex: "", Size: 10},
			},
			ReceivedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			t.Fatalf("handleInboundMessage() error = %v", err)
		}
		if len(sentTexts) == 0 || !strings.Contains(sentTexts[len(sentTexts)-1], "processed attachments") {
			t.Fatalf("unexpected sendmessage bodies: %v", sentTexts)
		}
	})

	t.Run("all attachments fail without text replies error and skips agent", func(t *testing.T) {
		runnerCalled := false
		runner := &scriptedRunner{
			run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
				runnerCalled = true
				return nil
			},
		}
		service := newTestService(t, runner)
		var sentTexts []string
		var sendTypingMu sync.Mutex
		sendTypingCalls := 0
		useHTTPClientFactory(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/c2c/download":
				return jsonResponse(http.StatusInternalServerError, `{"error":"download failed"}`), nil
			case "/ilink/bot/sendtyping":
				sendTypingMu.Lock()
				sendTypingCalls++
				sendTypingMu.Unlock()
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			case "/ilink/bot/sendmessage":
				body, _ := io.ReadAll(req.Body)
				sentTexts = append(sentTexts, string(body))
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			default:
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0,"typing_ticket":"ticket-1"}`), nil
			}
		}))

		cfg := Config{AccountID: "wx-bot", BotToken: "bot-token", BaseURL: "https://wechat.test", WorkspaceID: "default", AgentID: "claude"}
		err := service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
			ConversationKey: "user-3",
			MessageID:       "msg-3",
			ContextToken:    "ctx-3",
			Attachments: []WeChatAttachment{
				{Name: "bad.txt", downloadQuery: "bad-file", Size: 10},
			},
			ReceivedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			t.Fatalf("handleInboundMessage() error = %v", err)
		}
		if runnerCalled {
			t.Fatal("runner should not be called when all attachments fail and text is empty")
		}
		if len(sentTexts) == 0 || !strings.Contains(sentTexts[0], attachmentFailedReplyText) {
			t.Fatalf("unexpected sendmessage bodies: %v", sentTexts)
		}
		sendTypingMu.Lock()
		defer sendTypingMu.Unlock()
		if sendTypingCalls != 0 {
			t.Fatalf("sendTypingCalls = %d, want 0", sendTypingCalls)
		}
	})

	t.Run("busy conversation sends busy reply", func(t *testing.T) {
		runner := &scriptedRunner{}
		service := newTestService(t, runner)
		var sentTexts []string
		var sendTypingMu sync.Mutex
		sendTypingCalls := 0
		useHTTPClientFactory(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/ilink/bot/getconfig":
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0,"typing_ticket":"ticket-1"}`), nil
			case "/ilink/bot/sendtyping":
				sendTypingMu.Lock()
				sendTypingCalls++
				sendTypingMu.Unlock()
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			case "/ilink/bot/sendmessage":
				body, _ := io.ReadAll(req.Body)
				sentTexts = append(sentTexts, string(body))
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.String())
				return nil, nil
			}
		}))

		cfg := Config{AccountID: "wx-bot", BotToken: "bot-token", BaseURL: "https://wechat.test", WorkspaceID: "default", AgentID: "claude"}
		conversationID := deriveConversationID("user-4")
		unlock, ok := service.locks.TryLock(conversationID)
		if !ok {
			t.Fatal("failed to pre-lock conversation")
		}
		err := service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
			ConversationKey: "user-4",
			MessageID:       "msg-5",
			ContextToken:    "ctx-5",
			Text:            "second",
			ReceivedAt:      time.Now().UnixMilli(),
		})
		unlock()
		if err != nil {
			t.Fatalf("busy handleInboundMessage() error = %v", err)
		}
		foundBusy := false
		for _, body := range sentTexts {
			if strings.Contains(body, busyReplyText) {
				foundBusy = true
			}
		}
		if !foundBusy {
			t.Fatalf("busy reply not observed: %v", sentTexts)
		}
		sendTypingMu.Lock()
		defer sendTypingMu.Unlock()
		if sendTypingCalls != 0 {
			t.Fatalf("sendTypingCalls = %d, want 0", sendTypingCalls)
		}
	})

	t.Run("agent error still cancels typing", func(t *testing.T) {
		runner := &scriptedRunner{
			run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
				time.Sleep(25 * time.Millisecond)
				if err := sink.Emit(ChatEvent{Name: "error", Data: map[string]string{"message": "agent failed"}}); err != nil {
					return err
				}
				return nil
			},
		}
		service := newTestService(t, runner)
		var typingMu sync.Mutex
		var typingStatuses []int
		useHTTPClientFactory(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			switch req.URL.Path {
			case "/ilink/bot/getconfig":
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0,"typing_ticket":"ticket-1"}`), nil
			case "/ilink/bot/sendtyping":
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("Unmarshal(sendtyping body) error = %v", err)
				}
				typingMu.Lock()
				typingStatuses = append(typingStatuses, int(payload["status"].(float64)))
				typingMu.Unlock()
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			case "/ilink/bot/sendmessage":
				return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.String())
				return nil, nil
			}
		}))

		cfg := Config{AccountID: "wx-bot", BotToken: "bot-token", BaseURL: "https://wechat.test", WorkspaceID: "default", AgentID: "claude"}
		err := service.handleInboundMessage(context.Background(), cfg, WeChatInboundMessage{
			ConversationKey: "user-error",
			MessageID:       "msg-error",
			ContextToken:    "ctx-error",
			Text:            "hello",
			ReceivedAt:      time.Now().UnixMilli(),
		})
		if err != nil {
			t.Fatalf("handleInboundMessage() error = %v", err)
		}
		typingMu.Lock()
		defer typingMu.Unlock()
		if len(typingStatuses) < 2 || typingStatuses[len(typingStatuses)-1] != typingStatusCancel {
			t.Fatalf("typingStatuses = %v, want final cancel", typingStatuses)
		}
	})
}

func testGatewayConfig() Config {
	return Config{
		AccountID:   "wx-bot",
		BotToken:    "bot-token",
		BaseURL:     "https://wechat.test",
		WorkspaceID: "default",
		AgentID:     "claude",
	}
}

func useSendTextRecorder(t *testing.T) *[]string {
	t.Helper()

	sentTexts := []string{}
	useHTTPClientFactory(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/ilink/bot/getconfig":
			return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0,"typing_ticket":"ticket-1"}`), nil
		case "/ilink/bot/sendtyping":
			return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
		case "/ilink/bot/sendmessage":
			body, _ := io.ReadAll(req.Body)
			sentTexts = append(sentTexts, string(body))
			return jsonResponse(http.StatusOK, `{"ret":0,"errcode":0}`), nil
		default:
			t.Fatalf("unexpected request path: %s", req.URL.String())
			return nil, nil
		}
	}))
	return &sentTexts
}
