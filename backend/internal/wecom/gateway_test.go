package wecom

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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
	sends         []string
	media         []SendAction
	streams       []fakeStreamFrame
	events        []string
	failStreaming bool
	failFinal     bool
	finalErr      error
	finalErrAt    int
	finalCalls    int
	failSend      bool
	splitSends    bool
	streamSeq     int
}

type fakeStreamFrame struct {
	ID      string
	Content string
	Finish  bool
}

func finishedStreamFrames(frames []fakeStreamFrame) []fakeStreamFrame {
	out := make([]fakeStreamFrame, 0, len(frames))
	for _, frame := range frames {
		if frame.Finish {
			out = append(out, frame)
		}
	}
	return out
}

func streamFrameContents(frames []fakeStreamFrame) []string {
	out := make([]string, 0, len(frames))
	for _, frame := range frames {
		out = append(out, strings.TrimSpace(frame.Content))
	}
	return out
}

func finalDeliveryEvents(events []string) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		if strings.HasPrefix(event, "stream-final:") || strings.HasPrefix(event, "send:") || strings.HasPrefix(event, "send-media:") {
			out = append(out, event)
		}
	}
	return out
}

func isWeComStreamPlaceholder(text string) bool {
	for _, placeholder := range wecomStreamPlaceholders {
		if text == placeholder {
			return true
		}
	}
	return false
}

func smallMarkdownTableText() string {
	return "| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |"
}

func TestWeComStreamPlaceholdersHaveExpectedShape(t *testing.T) {
	if len(wecomStreamPlaceholders) != 20 {
		t.Fatalf("len(wecomStreamPlaceholders) = %d, want 20", len(wecomStreamPlaceholders))
	}
	seen := map[string]bool{}
	for _, placeholder := range wecomStreamPlaceholders {
		if !strings.HasSuffix(placeholder, "\n") {
			t.Fatalf("placeholder %q does not end with newline", placeholder)
		}
		if seen[placeholder] {
			t.Fatalf("duplicate placeholder %q", placeholder)
		}
		seen[placeholder] = true
	}
}

func (s *fakeSender) Reply(_ context.Context, _ replyContext, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, content)
	s.events = append(s.events, "reply:"+content)
	return nil
}

func (s *fakeSender) ReplyStream(_ context.Context, _ replyContext, streamID, content string, finish bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failStreaming {
		return errors.New("stream failed")
	}
	s.streams = append(s.streams, fakeStreamFrame{ID: streamID, Content: content, Finish: finish})
	s.events = append(s.events, "stream:"+content)
	return nil
}

func (s *fakeSender) ReplyStreamFinal(_ context.Context, _ replyContext, streamID, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failStreaming {
		return errors.New("wecom-ws: ack error: errcode=45009 errmsg=stream expired")
	}
	s.finalCalls++
	s.streams = append(s.streams, fakeStreamFrame{ID: streamID, Content: content, Finish: true})
	s.events = append(s.events, "stream-final:"+content)
	if s.finalErrAt > 0 && s.finalCalls == s.finalErrAt {
		if s.finalErr != nil {
			return s.finalErr
		}
		return errors.New("wecom-ws: ack error: errcode=45009 errmsg=stream expired")
	}
	if s.finalErr != nil {
		return s.finalErr
	}
	if s.failFinal {
		return errors.New("wecom-ws: ack error: errcode=45009 errmsg=stream expired")
	}
	return nil
}

func (s *fakeSender) NewStreamID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamSeq++
	if s.streamSeq == 1 {
		return "stream-test"
	}
	return "stream-test-" + strconv.Itoa(s.streamSeq)
}

func (s *fakeSender) Send(_ context.Context, _ replyContext, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSend {
		return errors.New("send failed")
	}
	if s.splitSends {
		for _, chunk := range splitWeComMarkdownMessages(content, wecomMarkdownSendMaxBytes) {
			s.sends = append(s.sends, chunk)
			s.replies = append(s.replies, chunk)
			s.events = append(s.events, "send:"+chunk)
		}
		return nil
	}
	s.sends = append(s.sends, content)
	s.replies = append(s.replies, content)
	s.events = append(s.events, "send:"+content)
	return nil
}

func (s *fakeSender) ReplyMedia(_ context.Context, _ replyContext, action SendAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media = append(s.media, action)
	s.events = append(s.events, "reply-media:"+action.Type+":"+action.Path)
	return nil
}

func (s *fakeSender) SendMedia(_ context.Context, _ replyContext, action SendAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media = append(s.media, action)
	s.events = append(s.events, "send-media:"+action.Type+":"+action.Path)
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

func TestGatewayNormalizesNonStreamMarkdownReply(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "行动建议### 行动一：利润下钻"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-normalize-reply",
		ReplyContext:    replyContext{ReqID: "req-normalize", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	want := "行动建议\n\n### 行动一：利润下钻"
	if len(sender.replies) != 1 || sender.replies[0] != want {
		t.Fatalf("replies = %v, want %q", sender.replies, want)
	}
}

func TestGatewayNonStreamLongMarkdownReplyUsesOrdinarySendSplitting(t *testing.T) {
	longBody := "开头\n\n" + strings.Repeat("这是一段很长的企业微信非流式回复，用来触发 markdown 分片。\n\n", 260)
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": longBody},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{splitSends: true}
	cfg := testGatewayConfig()
	cfg.Stream = false

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-nonstream-long",
		ReplyContext:    replyContext{ReqID: "req-nonstream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) != 0 {
		t.Fatalf("streams = %v, want no stream frames", sender.streams)
	}
	if len(sender.sends) < 2 {
		t.Fatalf("sends = %d, want ordinary markdown send chunks", len(sender.sends))
	}
	for _, send := range sender.sends {
		if len(send) > wecomMarkdownSendMaxBytes {
			t.Fatalf("send bytes = %d, want <= %d", len(send), wecomMarkdownSendMaxBytes)
		}
	}
	joined := strings.Join(sender.sends, "")
	for _, want := range []string{"开头", "企业微信非流式回复", "markdown 分片"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("send chunks missing %q: %q", want, joined)
		}
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
		t.Fatalf("streams = %v, want placeholder and complete", sender.streams)
	}
	for _, frame := range sender.streams {
		if frame.ID != "stream-test" {
			t.Fatalf("stream id = %q, want stream-test", frame.ID)
		}
	}
	first := sender.streams[0]
	if first.Finish || !isWeComStreamPlaceholder(first.Content) || !strings.HasSuffix(first.Content, "\n") {
		t.Fatalf("first stream frame = %+v, want unfinished placeholder with trailing newline", first)
	}
	last := sender.streams[len(sender.streams)-1]
	if !last.Finish || last.Content != "hello" {
		t.Fatalf("last stream frame = %+v, want finish hello", last)
	}
	if strings.Contains(last.Content, first.Content) {
		t.Fatalf("final stream content includes placeholder: %q", last.Content)
	}
}

func TestGatewayStreamLengthStopReasonAppendsNotice(t *testing.T) {
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
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "length"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-length",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) == 0 {
		t.Fatal("streams = nil, want stream frames")
	}
	finished := finishedStreamFrames(sender.streams)
	want := "reply text\n\n> " + lengthLimitNoticeText
	if got := strings.Join(streamFrameContents(finished), "\n\n"); got != want {
		t.Fatalf("finished stream content = %q, want %q; frames=%v", got, want, finished)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no continuation for short reply", sender.sends)
	}
}

func TestGatewayNonStreamLengthStopReasonAppendsNotice(t *testing.T) {
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
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "length"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	cfg.Stream = false

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-nonstream-length",
		ReplyContext:    replyContext{ReqID: "req-nonstream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	want := "reply text\n\n> " + lengthLimitNoticeText
	if len(sender.replies) != 1 || sender.replies[0] != want {
		t.Fatalf("replies = %v, want %q", sender.replies, want)
	}
}

func TestGatewayEndTurnStopReasonDoesNotAppendLengthNotice(t *testing.T) {
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
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-end-turn-no-length-notice",
		ReplyContext:    replyContext{ReqID: "req-end-turn", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 1 || sender.replies[0] != "reply text" {
		t.Fatalf("replies = %v, want reply text", sender.replies)
	}
}

func TestGatewayEndTurnIncompleteReplyContinuesOnce(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			switch input.Message {
			case "hello":
				if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "代码如下：\n```go\nfmt.Println(\"x\")"},
					},
				}}); err != nil {
					return err
				}
			case continueReplyPrompt:
				if input.NewSession {
					t.Fatalf("continuation NewSession = true, want false")
				}
				if len(input.Files) != 0 {
					t.Fatalf("continuation Files = %v, want none", input.Files)
				}
				if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "\n```\n完成。"},
					},
				}}); err != nil {
					return err
				}
			default:
				t.Fatalf("unexpected input message %q", input.Message)
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-end-turn-incomplete-continue",
		ReplyContext:    replyContext{ReqID: "req-end-turn", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("runner inputs = %d, want first run plus one continuation", len(runner.inputs))
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], "fmt.Println") || !strings.Contains(sender.replies[0], "完成。") {
		t.Fatalf("replies = %v, want original plus continuation", sender.replies)
	}
	if strings.Contains(sender.replies[0], incompleteReplyNoticeText) {
		t.Fatalf("reply contains incomplete notice after successful continuation: %q", sender.replies[0])
	}
}

func TestGatewayEndTurnCompleteReplyDoesNotContinue(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if input.Message == continueReplyPrompt {
				t.Fatalf("unexpected continuation run")
			}
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "完整回答。"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-end-turn-complete-no-continue",
		ReplyContext:    replyContext{ReqID: "req-end-turn", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("runner inputs = %d, want one", len(runner.inputs))
	}
	if len(sender.replies) != 1 || sender.replies[0] != "完整回答。" {
		t.Fatalf("replies = %v, want complete reply", sender.replies)
	}
}

func TestGatewayEndTurnCompleteReplyAfterToolDoesNotContinueForEarlierIncompleteMarkdown(t *testing.T) {
	const completeReply = `## 查询结果

**粤西区在 2026 年 7 月 30 日的销售额为：` + "`13,228,167.07`" + `。**

查询口径：

- 指标：销售额（` + "`saleAmt`" + `）
- 日期：` + "`2026-07-30`" + `
- 聚合维度：业务日期（` + "`bizDate`" + `）
- 统计口径：系统默认口径，即汇总（` + "`SUMMARY`" + `）
- 过滤条件：管理区域（` + "`manageAreaId`" + `）=` + "`CN01`" + `
- 区域名称：“粤西区”
- 未进行估算、求和、转换或基于其他数据推算

## CLI 执行证据

### 1. 确认“粤西区”的维度值

实际执行的命令：

` + "```bash" + `
qdm-metric-cli dim values --code manageAreaId --keyword '粤西区'
` + "```" + `

标准输出：

` + "```json" + `
[
  {
    "dimFieldId": "CN01",
    "dimFieldValue": "粤西区"
  }
]
` + "```" + `

标准错误：

` + "```text" + `
（空）
` + "```" + `

退出状态：

` + "```text" + `
0
` + "```" + `

### 2. 查询销售额

实际执行的命令：

` + "```bash" + `
qdm-metric-cli analysis execute --start-date 2026-07-30 --end-date 2026-07-30 --metric saleAmt --agg-dim bizDate --statistic-policy SUMMARY --filter manageAreaId=CN01
` + "```" + `

标准输出：

` + "```json" + `
[
  {
    "bizDate": "2026-07-30",
    "saleAmt": 13228167.07
  }
]
` + "```" + `

标准错误：

` + "```text" + `
（空）
` + "```" + `

退出状态：

` + "```text" + `
0
` + "```" + `

以上指标数值直接来自公开的 ` + "`qdm-metric-cli`" + `。未使用 ` + "`qdm-cmr-cli`" + `、` + "`qdm-sql-cli`" + `、` + "`cas-cli`" + ` 或私有 Metric CLI。`

	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if input.Message == continueReplyPrompt {
				t.Fatalf("unexpected continuation run")
			}
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "正在准备证据：**"},
				},
			}}); err != nil {
				return err
			}
			if err := sink.Emit(ChatEvent{Name: "tool_call", Data: map[string]any{
				"toolName": "Bash",
				"status":   "completed",
			}}); err != nil {
				return err
			}
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": completeReply},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-end-turn-complete-after-tool-no-continue",
		ReplyContext:    replyContext{ReqID: "req-end-turn", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("runner inputs = %d, want one", len(runner.inputs))
	}
	if !strings.Contains(strings.Join(sender.replies, "\n"), "13,228,167.07") {
		t.Fatalf("replies missing business result: %v", sender.replies)
	}
}

func TestGatewayIncompleteContinuationFailureAddsNotice(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if input.Message == continueReplyPrompt {
				return errors.New("continuation failed")
			}
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "待办：\n-"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-end-turn-continue-fail",
		ReplyContext:    replyContext{ReqID: "req-end-turn", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("runner inputs = %d, want continuation attempt", len(runner.inputs))
	}
	if len(sender.replies) != 1 || !strings.Contains(sender.replies[0], incompleteReplyNoticeText) {
		t.Fatalf("replies = %v, want incomplete notice", sender.replies)
	}
}

func TestGatewayAdvancedStreamContinuationStaysInFinalStreams(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			switch input.Message {
			case "hello":
				if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "待办：\n-"},
					},
				}}); err != nil {
					return err
				}
			case continueReplyPrompt:
				if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": " 第一项"},
					},
				}}); err != nil {
					return err
				}
			default:
				t.Fatalf("unexpected input message %q", input.Message)
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
		MessageID:       "msg-advanced-stream-continue",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) == 0 {
		t.Fatalf("streams = %v, want final stream", sender.streams)
	}
	if got := finished[len(finished)-1].Content; !strings.Contains(got, "第一项") {
		t.Fatalf("final stream = %q, want continuation text included", got)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no ordinary continuation send", sender.sends)
	}
}

func TestGatewayLengthStopReasonWithEmptyFinalTextKeepsFallback(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "length"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{}

	err := service.handleInboundMessage(context.Background(), testGatewayConfig(), WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-empty-length",
		ReplyContext:    replyContext{ReqID: "req-empty-length", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.replies) != 1 || sender.replies[0] != fallbackDoneText {
		t.Fatalf("replies = %v, want fallback only", sender.replies)
	}
}

func TestGatewayStreamSkipsPlaceholderWithoutReqID(t *testing.T) {
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
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-no-req",
		ReplyContext:    replyContext{ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) != 0 {
		t.Fatalf("streams = %v, want no stream frames without req id", sender.streams)
	}
	if len(sender.replies) != 1 || sender.replies[0] != "reply text" {
		t.Fatalf("replies = %v, want final reply text", sender.replies)
	}
}

func TestGatewayStreamStabilizesMarkdownFrames(t *testing.T) {
	finalBody := "变化；- ✅ 毛利分析\n\n📌 口径说明\n| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |\n"
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			for _, text := range []string{
				"变化；- ✅ 毛利",
				"变化；- ✅ 毛利分析\n",
				"变化；- ✅ 毛利分析\n\n📌 口径说明\n| 项目 | 内容 |",
				"变化；- ✅ 毛利分析\n\n📌 口径说明\n| 项目 | 内容 |\n| --- | --- |",
				"变化；- ✅ 毛利分析\n\n📌 口径说明\n| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10",
				"变化；- ✅ 毛利分析\n\n📌 口径说明\n| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |\n",
			} {
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
		MessageID:       "msg-stream-markdown",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) == 0 {
		t.Fatal("streams = nil, want stream frames")
	}
	finished := finishedStreamFrames(sender.streams)
	want := normalizeWeComMarkdown(finalBody)
	if got := strings.Join(streamFrameContents(finished), "\n\n"); got != want {
		t.Fatalf("finished stream content = %q, want %q; frames=%v", got, want, finished)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no ordinary markdown table send", sender.sends)
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

func TestWeComStreamDispatcherUsesTraceID(t *testing.T) {
	sender := &fakeSender{}
	streamSender := newWeComStreamSenderWithTrace(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", "trace-test")
	if streamSender.traceID != "trace-test" {
		t.Fatalf("traceID = %q, want trace-test", streamSender.traceID)
	}

	generated := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", defaultWeComStreamPolicy)
	if generated.traceID == "" {
		t.Fatal("generated traceID is empty")
	}
}

func TestWeComStreamSenderUsesRuntimePolicyFromEnv(t *testing.T) {
	t.Setenv("LUMI_WECOM_STREAM_MAX_BYTES", "128")
	t.Setenv("LUMI_WECOM_STREAM_MAX_UPDATES", "3")

	streamSender := newWeComStreamSender(&fakeSender{}, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "")
	if streamSender.policy.MaxBytes != 128 {
		t.Fatalf("MaxBytes = %d, want env override 128", streamSender.policy.MaxBytes)
	}
	if streamSender.policy.MaxUpdates != 3 {
		t.Fatalf("MaxUpdates = %d, want env override 3", streamSender.policy.MaxUpdates)
	}
}

func TestWeComStreamDispatcherReschedulesWhenCoalesceGapBeforeMinUpdateGap(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = 40 * time.Millisecond
	policy.CoalesceGap = time.Millisecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	streamSender.Update(context.Background(), "hello")
	streamSender.Update(context.Background(), "hello world")

	deadline := time.After(500 * time.Millisecond)
	for {
		sender.mu.Lock()
		got := append([]fakeStreamFrame(nil), sender.streams...)
		sender.mu.Unlock()
		if len(got) >= 2 {
			if got[1].Content != "hello world" || got[1].Finish {
				t.Fatalf("second stream frame = %+v, want coalesced update", got[1])
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("streams = %v, want pending update rescheduled after min gap", got)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestGatewayStreamUpdateUsesPreviewByteLimit(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	ctx := context.Background()
	longText := strings.Repeat("a", wecomStreamMaxBytes+1000)

	streamSender.Update(ctx, longText)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.streams) < 2 {
		t.Fatalf("streams = %d, want max-bytes rotation and remaining update", len(sender.streams))
	}
	ids := map[string]bool{}
	for _, frame := range sender.streams {
		if frame.Finish {
			t.Fatalf("live stream frame = %+v, want unfinished provisional update", frame)
		}
		if len(frame.Content) > wecomStreamLivePreviewMaxBytes {
			t.Fatalf("stream update bytes = %d, want <= %d", len(frame.Content), wecomStreamLivePreviewMaxBytes)
		}
		if !utf8.ValidString(frame.Content) {
			t.Fatalf("stream update content is invalid utf8")
		}
		ids[frame.ID] = true
	}
	if len(ids) < 2 {
		t.Fatalf("stream IDs = %v, want rotated stream IDs", ids)
	}
}

func TestWeComStreamDispatcherRotatesFinalByMaxBytes(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.FinalMaxBytes = 12
	policy.MaxBytes = 12
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	result := streamSender.Complete(context.Background(), "alpha beta gamma delta")

	if !result.FullDelivered {
		t.Fatal("FullDelivered = false, want all text delivered by rotated streams")
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) < 2 {
		t.Fatalf("finished streams = %d, want max-bytes rotation", len(finished))
	}
	ids := map[string]bool{}
	for _, frame := range finished {
		if len(frame.Content) > policy.FinalMaxBytes {
			t.Fatalf("stream bytes = %d, want <= %d", len(frame.Content), policy.FinalMaxBytes)
		}
		ids[frame.ID] = true
	}
	if len(ids) < 2 {
		t.Fatalf("stream IDs = %v, want distinct rotated IDs", ids)
	}
}

func TestWeComStreamDispatcherRotatesUpdateByMaxUpdates(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MaxUpdates = 1
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	streamSender.Update(context.Background(), "first")
	streamSender.Update(context.Background(), "first second")

	if len(sender.streams) < 2 {
		t.Fatalf("streams = %v, want provisional rotation plus second update", sender.streams)
	}
	if sender.streams[0].Finish {
		t.Fatalf("first stream frame = %+v, want unfinished provisional slot", sender.streams[0])
	}
	last := sender.streams[len(sender.streams)-1]
	if last.Finish || last.ID == "stream-test" || last.Content != "second" {
		t.Fatalf("last update = %+v, want only accumulated suffix on rotated stream", last)
	}
}

func TestWeComStreamDispatcherRotatesUpdateByMaxAge(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MaxAge = time.Millisecond
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	streamSender.Update(context.Background(), "first")
	streamSender.startedAt = time.Now().Add(-2 * time.Millisecond)
	streamSender.Update(context.Background(), "first second")

	if len(sender.streams) < 2 {
		t.Fatalf("streams = %v, want max-age rotation", sender.streams)
	}
	if sender.streams[0].Finish {
		t.Fatalf("first stream frame = %+v, want unfinished provisional slot", sender.streams[0])
	}
	last := sender.streams[len(sender.streams)-1]
	if last.ID == "stream-test" || last.Content != "second" {
		t.Fatalf("last update = %+v, want only accumulated suffix on new stream", last)
	}
}

func TestWeComStreamDispatcherAdvancedLiveStreamsRawStructuredContent(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	table := "前缀\n\n| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |"

	streamSender.Update(context.Background(), table)

	if len(sender.streams) != 1 {
		t.Fatalf("streams = %v, want one raw live update", sender.streams)
	}
	frame := sender.streams[0]
	if frame.Finish {
		t.Fatalf("stream frame = %+v, want unfinished raw update", frame)
	}
	if !strings.Contains(frame.Content, "| 项目 | 内容 |") || !strings.Contains(frame.Content, "| 毛利 | 10 |") {
		t.Fatalf("stream content = %q, want raw markdown table", frame.Content)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no atomic send during advanced live", sender.sends)
	}
}

func TestWeComStreamDispatcherAdvancedLiveHidesSendProtocol(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, root, policy)
	body := "before\n\n[LUMI_WECOM_SEND]\n{\"type\":\"image\",\"path\":\"chart.png\"}\n[/LUMI_WECOM_SEND]\n\nafter"

	streamSender.Update(context.Background(), body)

	if len(sender.streams) != 1 {
		t.Fatalf("streams = %v, want one live update", sender.streams)
	}
	got := sender.streams[0].Content
	if strings.Contains(got, "LUMI_WECOM_SEND") || strings.Contains(got, "chart.png") {
		t.Fatalf("stream leaked hidden protocol content: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("stream content = %q, want visible text around protocol block", got)
	}
}

func TestWeComStreamDispatcherAdvancedLiveHidesUnclosedSendProtocol(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	streamSender.Update(context.Background(), "before\n\n[LUMI_WECOM_SEND]\n{\"type\":\"image\",\"path\":\"chart.png\"")

	if len(sender.streams) != 1 {
		t.Fatalf("streams = %v, want one live update before protocol block", sender.streams)
	}
	got := sender.streams[0].Content
	if got != "before" {
		t.Fatalf("stream content = %q, want only visible text before unclosed protocol block", got)
	}
	if strings.Contains(got, "LUMI_WECOM_SEND") || strings.Contains(got, "chart.png") {
		t.Fatalf("stream leaked unclosed protocol block: %q", got)
	}
}

func TestAdvancedExtraSlotNoticesFollowRenderingProgression(t *testing.T) {
	tests := []struct {
		count int
		want  []string
	}{
		{count: 1, want: []string{
			advancedAnswerCompleteNotice,
		}},
		{count: 2, want: []string{
			advancedAllOptimizedNotice,
			advancedAnswerCompleteNotice,
		}},
		{count: 3, want: []string{
			advancedTableOptimizedNotice,
			advancedAllOptimizedNotice,
			advancedAnswerCompleteNotice,
		}},
		{count: 4, want: []string{
			advancedRenderingStartNotice,
			advancedTableOptimizedNotice,
			advancedAllOptimizedNotice,
			advancedAnswerCompleteNotice,
		}},
		{count: 5, want: []string{
			advancedRenderingStartNotice,
			advancedRenderingStartNotice,
			advancedTableOptimizedNotice,
			advancedAllOptimizedNotice,
			advancedAnswerCompleteNotice,
		}},
		{count: 6, want: []string{
			advancedRenderingStartNotice,
			advancedRenderingStartNotice,
			advancedRenderingStartNotice,
			advancedTableOptimizedNotice,
			advancedAllOptimizedNotice,
			advancedAnswerCompleteNotice,
		}},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.count), func(t *testing.T) {
			got := advancedExtraSlotNotices(tt.count)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("advancedExtraSlotNotices(%d) = %#v, want %#v", tt.count, got, tt.want)
			}
		})
	}
}

func TestWeComStreamDispatcherAdvancedRotationKeepsSlotsUnfinishedUntilFinal(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MaxBytes = 8
	policy.LivePreviewMaxBytes = 8
	policy.FinalMaxBytes = 8
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	streamSender.Update(context.Background(), "alpha beta gamma")

	if len(sender.streams) < 2 {
		t.Fatalf("streams = %v, want rotated live slots", sender.streams)
	}
	for _, frame := range sender.streams {
		if frame.Finish {
			t.Fatalf("live rotation frame = %+v, want unfinished provisional slot", frame)
		}
	}
	ids := map[string]bool{}
	for _, frame := range sender.streams {
		ids[frame.ID] = true
	}
	if len(ids) < 2 {
		t.Fatalf("stream IDs = %v, want multiple provisional slots", ids)
	}

	streamSender.Complete(context.Background(), "final")

	finished := finishedStreamFrames(sender.streams)
	if len(finished) != len(ids) {
		t.Fatalf("finished streams = %v, want one final cover per provisional slot", finished)
	}
	if finished[0].Content != "final" {
		t.Fatalf("first final = %q, want final content", finished[0].Content)
	}
	extraContents := streamFrameContents(finished[1:])
	if want := advancedExtraSlotNotices(len(extraContents)); strings.Join(extraContents, "\n") != strings.Join(want, "\n") {
		t.Fatalf("extra final notices = %#v, want %#v", extraContents, want)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no duplicate ordinary send", sender.sends)
	}
}

func TestWeComStreamDispatcherAdvancedDefaultFinalSplitsForClientDisplay(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	body := strings.Repeat("这是一段用于验证企业微信客户端完整显示的最终报告内容。\n\n", 240)

	streamSender.Update(context.Background(), body)
	result := streamSender.Complete(context.Background(), body)

	if !result.FullDelivered {
		t.Fatal("FullDelivered = false, want all final chunks delivered")
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) < 2 {
		t.Fatalf("finished streams = %d, want final answer split across streams", len(finished))
	}
	parts := make([]string, 0, len(finished))
	for _, frame := range finished {
		if len(frame.Content) > wecomMarkdownSendMaxBytes {
			t.Fatalf("final stream bytes = %d, want <= %d", len(frame.Content), wecomMarkdownSendMaxBytes)
		}
		if !isAdvancedExtraSlotNotice(frame.Content) {
			parts = append(parts, frame.Content)
		}
	}
	if got := strings.Join(parts, "\n\n"); got != normalizeWeComMarkdown(body) {
		t.Fatalf("final chunks did not reconstruct body")
	}
}

func TestWeComStreamDispatcherAdvancedFinalKeepsFittingTableWhole(t *testing.T) {
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", defaultWeComStreamPolicy)
	prefix := strings.Repeat("a", wecomMarkdownSendMaxBytes-80)
	table := strings.Join([]string{
		"| 区域 | 销售额 | 同比 |",
		"| --- | ---: | ---: |",
		"| 华东 | 1280万 | 12% |",
		"| 华南 | 980万 | 8% |",
	}, "\n")
	if len(table) >= wecomMarkdownSendMaxBytes || len(prefix)+len("\n\n")+len(table) <= wecomMarkdownSendMaxBytes {
		t.Fatalf("bad fixture sizes: prefix=%d table=%d limit=%d", len(prefix), len(table), wecomMarkdownSendMaxBytes)
	}
	body := prefix + "\n\n" + table

	result := streamSender.Complete(context.Background(), body)

	if !result.FullDelivered {
		t.Fatalf("FullDelivered = false, want final chunks delivered")
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) != 2 {
		t.Fatalf("finished streams = %d, want prefix plus whole table", len(finished))
	}
	for _, frame := range finished {
		if len(frame.Content) > wecomMarkdownSendMaxBytes {
			t.Fatalf("final stream bytes = %d, want <= %d", len(frame.Content), wecomMarkdownSendMaxBytes)
		}
	}
	if finished[1].Content != table {
		t.Fatalf("table stream = %q, want whole table", finished[1].Content)
	}
	if strings.Contains(finished[0].Content, "| 区域 |") {
		t.Fatalf("first stream contains partial table: %q", finished[0].Content)
	}
}

func TestWeComStreamDispatcherAdvancedFinalSplitsOversizedTableAsTables(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.FinalMaxBytes = 220
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	header := "| 区域 | 销售额 | 同比 |"
	delimiter := "| --- | ---: | ---: |"
	rows := []string{header, delimiter}
	for i := 0; i < 30; i++ {
		rows = append(rows, "| 华南大区 | 12345 | 9.8% |")
	}
	body := strings.Join(rows, "\n")

	result := streamSender.Complete(context.Background(), body)

	if !result.FullDelivered {
		t.Fatalf("FullDelivered = false, want oversized table delivered")
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) < 2 {
		t.Fatalf("finished streams = %d, want oversized table split", len(finished))
	}
	for i, frame := range finished {
		if len(frame.Content) > policy.FinalMaxBytes {
			t.Fatalf("stream %d bytes = %d, want <= %d: %q", i, len(frame.Content), policy.FinalMaxBytes, frame.Content)
		}
		if !strings.HasPrefix(frame.Content, header+"\n"+delimiter+"\n") {
			t.Fatalf("stream %d missing table header/delimiter: %q", i, frame.Content)
		}
		lines := strings.Split(frame.Content, "\n")
		if len(lines) < 3 {
			t.Fatalf("stream %d has no table body rows: %q", i, frame.Content)
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
				t.Fatalf("stream %d contains non-table or partial line %q in %q", i, line, frame.Content)
			}
		}
	}
}

func TestWeComStreamDispatcherAdvancedFinalSplitsOversizedCodeAndJSONWithFences(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.FinalMaxBytes = 180
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	longCode := "```go\n" + strings.Repeat("fmt.Println(\"hello\")\n", 30) + "```"
	longJSON := "```json\n{\"items\":[" + strings.Repeat("{\"id\":1},", 40) + "{}]}\n```"
	body := longCode + "\n\n" + longJSON

	result := streamSender.Complete(context.Background(), body)

	if !result.FullDelivered {
		t.Fatalf("FullDelivered = false, want oversized code/json delivered")
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) < 3 {
		t.Fatalf("finished streams = %d, want code/json split across fenced chunks", len(finished))
	}
	for i, frame := range finished {
		content := strings.TrimSpace(frame.Content)
		if len(content) > policy.FinalMaxBytes {
			t.Fatalf("stream %d bytes = %d, want <= %d: %q", i, len(content), policy.FinalMaxBytes, content)
		}
		if !strings.HasPrefix(content, "```") || !strings.HasSuffix(content, "\n```") {
			t.Fatalf("stream %d is not a closed fenced block: %q", i, content)
		}
		if strings.Count(content, "```") != 2 {
			t.Fatalf("stream %d has unexpected fence count: %q", i, content)
		}
	}
}

func TestWeComStreamDispatcherAdvancedSafeDurationDoesNotUseOrdinaryFallback(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	streamSender.startedAt = time.Now().Add(-wecomStreamSafeDuration - time.Second)

	streamSender.Update(context.Background(), "partial answer")

	if streamSender.Failed() {
		t.Fatal("stream sender failed = true, want raw advanced stream update")
	}
	if preview, fallback := streamSender.SendFallbackPreview(); fallback || preview != "" {
		t.Fatalf("fallback preview = %q fallback=%v, want no ordinary fallback", preview, fallback)
	}
	if len(sender.streams) != 1 {
		t.Fatalf("streams = %v, want one raw live update", sender.streams)
	}
	if frame := sender.streams[0]; frame.Finish || frame.Content != "partial answer" {
		t.Fatalf("stream frame = %+v, want unfinished raw update", frame)
	}
}

func TestGatewayAdvancedFinalAckFailureDoesNotOrdinarySendFullAnswer(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "raw partial"},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{finalErr: errors.New("wecom-ws: ack timeout")}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-advanced-final-ack-error",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(finishedStreamFrames(sender.streams)) != 1 {
		t.Fatalf("streams = %v, want one attempted final overwrite", sender.streams)
	}
	if len(sender.sends) != 0 || len(sender.replies) != 0 {
		t.Fatalf("sends=%v replies=%v, want no ordinary full-answer fallback", sender.sends, sender.replies)
	}
}

func TestGatewayAdvancedLiveWriteFailureDoesNotOrdinarySendSegment(t *testing.T) {
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "raw partial"},
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
		MessageID:       "msg-advanced-live-write-error",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.sends) != 0 || len(sender.replies) != 0 {
		t.Fatalf("sends=%v replies=%v, want no ordinary fallback after advanced live write failure", sender.sends, sender.replies)
	}
}

func TestWeComStreamDispatcherAdvancedFinalAckFailureContinuesCoveringSlotsAndSkipsMedia(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeSender{finalErrAt: 2}
	policy := defaultWeComStreamPolicy
	policy.MaxBytes = 8
	policy.LivePreviewMaxBytes = 8
	policy.FinalMaxBytes = 8
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, root, policy)
	body := strings.Join([]string{
		"alpha beta gamma",
		"",
		"[LUMI_WECOM_SEND]",
		`{"type":"image","path":"chart.png","caption":"chart"}`,
		"[/LUMI_WECOM_SEND]",
	}, "\n")

	streamSender.Update(context.Background(), body)
	liveIDs := map[string]bool{}
	for _, frame := range sender.streams {
		if !frame.Finish {
			liveIDs[frame.ID] = true
		}
	}
	if len(liveIDs) < 2 {
		t.Fatalf("live stream IDs = %v, want multiple provisional slots", liveIDs)
	}

	result := streamSender.Complete(context.Background(), body)

	if result.FullDelivered {
		t.Fatal("FullDelivered = true, want failed final slot")
	}
	if !streamSender.Failed() {
		t.Fatal("stream sender failed = false, want advanced final failure recorded")
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) < len(liveIDs) {
		t.Fatalf("finished streams = %v, want attempted final cover for every live slot", finished)
	}
	if len(sender.media) != 0 || len(sender.sends) != 0 {
		t.Fatalf("media=%v sends=%v, want media/caption skipped after text slot final failure", sender.media, sender.sends)
	}
}

func TestWeComStreamDispatcherAdvancedSendsProtocolCaptionAndMediaAfterFinalText(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, root, defaultWeComStreamPolicy)
	body := strings.Join([]string{
		"before",
		"",
		"[LUMI_WECOM_SEND]",
		`{"type":"image","path":"chart.png","caption":"chart"}`,
		"[/LUMI_WECOM_SEND]",
		"",
		"after",
	}, "\n")

	streamSender.Update(context.Background(), body)
	streamSender.Complete(context.Background(), body)

	finalEvents := finalDeliveryEvents(sender.events)
	want := []string{
		"stream-final:before\n\nafter",
		"send:chart",
		"send-media:image:chart.png",
	}
	if strings.Join(finalEvents, "\n") != strings.Join(want, "\n") {
		t.Fatalf("final events = %#v, want %#v; all=%#v", finalEvents, want, sender.events)
	}
	for _, frame := range sender.streams {
		if strings.Contains(frame.Content, "LUMI_WECOM_SEND") || strings.Contains(frame.Content, "chart.png") {
			t.Fatalf("stream leaked protocol: %+v", frame)
		}
	}
}

func TestWeComStreamDispatcherAdvancedCaptionSubstringStillSendsCaption(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, root, defaultWeComStreamPolicy)
	body := strings.Join([]string{
		"chart analysis is ready",
		"",
		"[LUMI_WECOM_SEND]",
		`{"type":"image","path":"chart.png","caption":"chart"}`,
		"[/LUMI_WECOM_SEND]",
	}, "\n")

	streamSender.Update(context.Background(), body)
	streamSender.Complete(context.Background(), body)

	finalEvents := finalDeliveryEvents(sender.events)
	want := []string{
		"stream-final:chart analysis is ready",
		"send:chart",
		"send-media:image:chart.png",
	}
	if strings.Join(finalEvents, "\n") != strings.Join(want, "\n") {
		t.Fatalf("final events = %#v, want %#v; all=%#v", finalEvents, want, sender.events)
	}
}

func TestWeComStreamDispatcherAdvancedLongCodeAndJSONStayCoverableText(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.FinalMaxBytes = 3000
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)
	longCode := "```go\n" + strings.Repeat("fmt.Println(\"hello\")\n", 260) + "```"
	longJSON := "```json\n{\"items\":[" + strings.Repeat("{\"id\":1},", 520) + "{}]}\n```"
	body := longCode + "\n\n" + longJSON

	streamSender.Update(context.Background(), body)
	result := streamSender.Complete(context.Background(), body)

	if !result.FullDelivered {
		t.Fatalf("FullDelivered = false, want long code/json covered in stream slots")
	}
	if len(sender.media) != 0 || len(sender.sends) != 0 {
		t.Fatalf("media=%v sends=%v, want no file conversion or ordinary send", sender.media, sender.sends)
	}
	finalText := strings.Join(streamFrameContents(finishedStreamFrames(sender.streams)), "\n\n")
	if !strings.Contains(finalText, "fmt.Println(\"hello\")") || !strings.Contains(finalText, "\"items\"") {
		t.Fatalf("final streams missing long code/json content")
	}
	if strings.Contains(finalText, "已转为文件发送") {
		t.Fatalf("final streams used file summary: %q", finalText)
	}
}

func TestWeComStreamDispatcherAdvancedLogsSlotAndCompletionEvents(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
	})
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, root, defaultWeComStreamPolicy)
	body := strings.Join([]string{
		"before",
		"",
		"[LUMI_WECOM_SEND]",
		`{"type":"image","path":"chart.png","caption":"chart"}`,
		"[/LUMI_WECOM_SEND]",
		"",
		"after",
	}, "\n")

	streamSender.Update(context.Background(), body)
	streamSender.Complete(context.Background(), body)

	got := logs.String()
	for _, want := range []string{
		"event=slot_created",
		"event=slot_updated",
		"event=slot_finalizing",
		"event=slot_final_ack",
		"event=advanced_complete",
		"event=media_delivered",
		"mode=advanced",
		"traceID=",
		"streamID=stream-test",
		"slotIndex=0",
		"slotStatus=finalized",
		"errClass=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %q:\n%s", want, got)
		}
	}
}

func TestClassifyWeComStreamError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want WeComStreamErrorClass
	}{
		{name: "nil", want: WeComStreamErrorNone},
		{name: "ack timeout", err: errors.New("wecom-ws: ack timeout"), want: WeComStreamErrorAckTimeout},
		{name: "stream expired", err: errors.New("wecom-ws: ack error: stream expired"), want: WeComStreamErrorExpired},
		{name: "not connected", err: errors.New("wecom-ws: not connected"), want: WeComStreamErrorDisconnected},
		{name: "connection closed", err: errors.New("wecom-ws: connection closed"), want: WeComStreamErrorDisconnected},
		{name: "write failed", err: errors.New("write tcp: broken pipe"), want: WeComStreamErrorWriteFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyWeComStreamError(tt.err); got != tt.want {
				t.Fatalf("classifyWeComStreamError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestWeComStreamDispatcherCompleteAfterUpdateRotationDoesNotRepeatDeliveredPrefix(t *testing.T) {
	sender := &fakeSender{}
	policy := defaultWeComStreamPolicy
	policy.MaxUpdates = 1
	policy.MinUpdateGap = time.Nanosecond
	policy.CoalesceGap = time.Nanosecond
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", policy)

	streamSender.Update(context.Background(), "first")
	streamSender.Update(context.Background(), "first second")
	streamSender.Complete(context.Background(), "first second")

	finished := finishedStreamFrames(sender.streams)
	if len(finished) < 2 {
		t.Fatalf("finished streams = %v, want rotated update and final stream", finished)
	}
	if finished[0].Content != "first second" {
		t.Fatalf("first finished stream = %+v, want complete final answer", finished[0])
	}
	if !isAdvancedExtraSlotNotice(finished[len(finished)-1].Content) {
		t.Fatalf("extra final stream = %+v, want status notice", finished[len(finished)-1])
	}
}

func TestWeComStreamDispatcherMergesFinalMarkdownBlocksIntoSingleStream(t *testing.T) {
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", defaultWeComStreamPolicy)
	body := strings.Join([]string{
		"## 结论",
		"",
		"昨天数据已查询完成。",
		"",
		"**结果如下：**",
		"1. 来客数正常",
		"2. 销售额正常",
		"",
		"完成。",
	}, "\n")

	result := streamSender.Complete(context.Background(), body)

	if !result.FullDelivered {
		t.Fatalf("FullDelivered = false, want merged final stream delivered; result=%+v", result)
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) != 1 {
		t.Fatalf("finished streams = %v, want one merged final stream", finished)
	}
	got := finished[0].Content
	if strings.Contains(got, "**结果如下：**1.") {
		t.Fatalf("merged final stream has broken list spacing: %q", got)
	}
	for _, want := range []string{
		"## 结论\n\n昨天数据已查询完成。",
		"**结果如下：**\n1. 来客数正常",
		"2. 销售额正常\n\n完成。",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged final stream = %q, want to contain %q", got, want)
		}
	}
}

func TestWeComStreamDispatcherTrimDeliveredHeadingFromMergedFinal(t *testing.T) {
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "", defaultWeComStreamPolicy)
	streamSender.deliveredPrefix = "## 结论"

	result := streamSender.Complete(context.Background(), "## 结论\n\n昨天数据已查询完成。")

	if !result.FullDelivered {
		t.Fatalf("FullDelivered = false, want remaining paragraph delivered; result=%+v", result)
	}
	finished := finishedStreamFrames(sender.streams)
	if len(finished) != 1 {
		t.Fatalf("finished streams = %v, want one remaining final stream", finished)
	}
	if got := finished[0].Content; got != "## 结论\n\n昨天数据已查询完成。" {
		t.Fatalf("final stream = %q, want complete final answer", got)
	}
}

func TestWeComStreamDispatcherPreservesProtocolMediaSourceOrder(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeSender{}
	streamSender := NewWeComStreamDispatcher(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, root, defaultWeComStreamPolicy)
	body := strings.Join([]string{
		"before",
		"",
		"[LUMI_WECOM_SEND]",
		`{"type":"image","path":"chart.png","caption":"chart"}`,
		"[/LUMI_WECOM_SEND]",
		"",
		"after",
	}, "\n")

	streamSender.Complete(context.Background(), body)

	finalEvents := finalDeliveryEvents(sender.events)
	want := []string{
		"stream-final:before\n\nafter",
		"send:chart",
		"send-media:image:chart.png",
	}
	if strings.Join(finalEvents, "\n") != strings.Join(want, "\n") {
		t.Fatalf("final events = %#v, want %#v; all=%#v", finalEvents, want, sender.events)
	}
}

func TestGatewayStreamFallbackSendsOnlyRemainingText(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	finalText := "第一段\n\n第二段\n\n第三段"
	preview := "第一段\n\n第二段"

	sent, err := service.sendTextSegmentAfterStreamFallback(context.Background(), sender, msg, "", finalText, preview)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallback() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want continuation send")
	}
	if len(sender.sends) != 1 || sender.sends[0] != "续上：\n\n第三段" {
		t.Fatalf("sends = %v, want only remaining text", sender.sends)
	}
	if strings.Contains(sender.sends[0], "第一段") || strings.Contains(sender.sends[0], "第二段") {
		t.Fatalf("continuation duplicated stream preview: %q", sender.sends[0])
	}
}

func TestGatewayStreamFallbackSendsCompleteAnswerWhenPreviewMismatch(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}

	sent, err := service.sendTextSegmentAfterStreamFallback(context.Background(), sender, msg, "", "最终完整回答", "旧的前缀")
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallback() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want complete answer send")
	}
	want := "完整回答：\n\n最终完整回答"
	if len(sender.sends) != 1 || sender.sends[0] != want {
		t.Fatalf("sends = %v, want %q", sender.sends, want)
	}
}

func TestGatewayStreamFallbackSendsRemainingAndMedia(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	finalText := "报告开头\n\n后续说明\n\n[LUMI_WECOM_SEND]\n{\"type\":\"image\",\"path\":\"chart.png\",\"caption\":\"chart\"}\n[/LUMI_WECOM_SEND]"

	sent, err := service.sendTextSegmentAfterStreamFallback(context.Background(), sender, msg, root, finalText, "报告开头")
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallback() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want continuation and media")
	}
	if len(sender.media) != 1 || sender.media[0].Type != "image" {
		t.Fatalf("media = %v, want one image", sender.media)
	}
	if len(sender.sends) != 2 || sender.sends[0] != "chart" || sender.sends[1] != "续上：\n\n后续说明" {
		t.Fatalf("sends = %v, want media caption then continuation", sender.sends)
	}
	if strings.Contains(sender.sends[1], "LUMI_WECOM_SEND") || strings.Contains(sender.sends[1], "chart.png") {
		t.Fatalf("continuation leaked protocol: %q", sender.sends[1])
	}
}

func TestGatewayStreamFallbackUsesLedgerPendingUnits(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	units := []DeliveredUnit{
		{
			ID:             "stream-final",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "已确认预览",
			ContentHash:    "hash-1",
			DeliveryMethod: DeliveryMethodStream,
			Status:         DeliveryStatusDelivered,
		},
		{
			ID:             "text-remaining",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "只补这一段",
			ContentHash:    "hash-2",
			DeliveryMethod: DeliveryMethodSend,
			Status:         DeliveryStatusPending,
		},
	}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "完整回答不应使用", "不匹配", units)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallbackWithLedger() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want ledger continuation")
	}
	if len(sender.sends) != 1 || sender.sends[0] != "续上：\n\n只补这一段" {
		t.Fatalf("sends = %v, want only pending ledger unit", sender.sends)
	}
	if strings.Contains(sender.sends[0], "已确认预览") || strings.Contains(sender.sends[0], "完整回答不应使用") {
		t.Fatalf("ledger fallback duplicated delivered/full text: %q", sender.sends[0])
	}
}

func TestGatewayStreamFallbackUsesFailedStreamLedgerReplacement(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	units := []DeliveredUnit{
		{
			ID:             "stream-final",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "stream 未确认文本",
			ContentHash:    "hash-1",
			DeliveryMethod: DeliveryMethodStream,
			Status:         DeliveryStatusFailed,
			Error:          "stream expired",
		},
	}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "完整回答不应使用", "stream 未确认文本", units)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallbackWithLedger() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want failed stream replacement")
	}
	if len(sender.sends) != 1 || sender.sends[0] != "stream 未确认文本" {
		t.Fatalf("sends = %v, want failed stream unit replacement", sender.sends)
	}
}

func TestGatewayStreamFallbackAfterRotatedFinalAckFailureDoesNotResendDeliveredStream(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	units := []DeliveredUnit{
		{
			ID:             "stream-1",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "已确认第一段",
			ContentHash:    "hash-1",
			DeliveryMethod: DeliveryMethodStream,
			Status:         DeliveryStatusDelivered,
		},
		{
			ID:             "stream-2",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "失败第二段",
			ContentHash:    "hash-2",
			DeliveryMethod: DeliveryMethodStream,
			Status:         DeliveryStatusFailed,
			Error:          "stream expired",
		},
	}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "不应使用完整回答", "已确认第一段", units)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallbackWithLedger() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want failed stream fallback")
	}
	if len(sender.sends) != 1 || sender.sends[0] != "失败第二段" {
		t.Fatalf("sends = %v, want only failed rotated stream unit", sender.sends)
	}
}

func TestGatewayStreamFallbackDoesNotResendWhenLedgerAlreadyDelivered(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	units := []DeliveredUnit{
		{
			ID:             "stream-final",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "已确认完整回答",
			ContentHash:    "hash-1",
			DeliveryMethod: DeliveryMethodStream,
			Status:         DeliveryStatusDelivered,
		},
	}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "已确认完整回答", "已确认完整回答", units)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallbackWithLedger() error = %v", err)
	}
	if sent {
		t.Fatal("sent = true, want no resend when ledger is fully delivered")
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no duplicate full-answer fallback", sender.sends)
	}
}

func TestGatewayStreamFallbackFallsBackToCompleteAnswerWhenLedgerInvalid(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	units := []DeliveredUnit{
		{
			ID:             "",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "损坏 ledger 文本",
			ContentHash:    "",
			DeliveryMethod: DeliveryMethodSend,
			Status:         DeliveryStatusPending,
		},
	}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "最终完整回答", "旧预览", units)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallbackWithLedger() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want full fallback")
	}
	want := "完整回答：\n\n最终完整回答"
	if len(sender.sends) != 1 || sender.sends[0] != want {
		t.Fatalf("sends = %v, want damaged ledger full fallback %q", sender.sends, want)
	}
}

func TestGatewayStreamFallbackFallsBackToCompleteAnswerWhenLedgerMissing(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "第一段\n\n第二段", "第一段", nil)
	if err != nil {
		t.Fatalf("sendTextSegmentAfterStreamFallbackWithLedger() error = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want full fallback")
	}
	want := "完整回答：\n\n第一段\n\n第二段"
	if len(sender.sends) != 1 || sender.sends[0] != want {
		t.Fatalf("sends = %v, want missing ledger full fallback %q", sender.sends, want)
	}
}

func TestGatewayStreamFallbackReturnsErrorWhenLedgerSendFails(t *testing.T) {
	service := newTestService(t, nil)
	sender := &fakeSender{failSend: true}
	msg := WeComInboundMessage{ReplyContext: replyContext{ChatID: "chat", UserID: "user"}}
	units := []DeliveredUnit{
		{
			ID:             "text-remaining",
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           "发送会失败",
			ContentHash:    "hash-1",
			DeliveryMethod: DeliveryMethodSend,
			Status:         DeliveryStatusPending,
		},
	}

	sent, err := service.sendTextSegmentAfterStreamFallbackWithLedger(context.Background(), sender, msg, "", "完整回答", "预览", units)
	if err == nil {
		t.Fatal("sendTextSegmentAfterStreamFallbackWithLedger() error = nil, want send failure")
	}
	if sent {
		t.Fatal("sent = true, want false when first fallback send fails")
	}
}

func TestWeComStreamSenderSafeDurationFinalAckErrorMarksStreamFailed(t *testing.T) {
	sender := &fakeSender{failFinal: true}
	streamSender := newWeComStreamSender(sender, replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"}, "")
	streamSender.startedAt = time.Now().Add(-wecomStreamSafeDuration - time.Second)

	streamSender.Complete(context.Background(), "final answer")

	if !streamSender.Failed() {
		t.Fatal("stream sender failed = false, want final ack failure")
	}
	if _, fallback := streamSender.SendFallbackPreview(); fallback {
		t.Fatal("fallback = true, want ordinary full-send fallback after final ack failure")
	}
	units := streamSender.LedgerSnapshot()
	if len(units) == 0 || units[0].Status != DeliveryStatusFailed {
		t.Fatalf("ledger = %+v, want failed final stream unit", units)
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

func TestGatewayStreamLongReplySendsPreviewThenRemaining(t *testing.T) {
	longBody := "开头\n\n" + strings.Repeat("这是一段很长的回答内容，用来触发企业微信 stream 安全阈值。\n\n", 260)
	normalized := normalizeWeComMarkdown(longBody)
	if len(normalized) <= wecomStreamFinalMaxBytes {
		t.Fatal("test fixture did not exceed stream rotation threshold")
	}

	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": longBody},
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
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-long",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) == 0 {
		t.Fatal("streams = nil, want stream frames")
	}
	if len(sender.streams) < 2 {
		t.Fatalf("streams = %d, want multiple rotated streams", len(sender.streams))
	}
	ids := map[string]bool{}
	parts := make([]string, 0, len(sender.streams))
	for _, frame := range sender.streams {
		if !frame.Finish {
			continue
		}
		if len(frame.Content) > wecomStreamFinalMaxBytes {
			t.Fatalf("stream bytes = %d, want <= %d", len(frame.Content), wecomStreamFinalMaxBytes)
		}
		ids[frame.ID] = true
		parts = append(parts, strings.TrimSpace(frame.Content))
	}
	if len(ids) < 2 {
		t.Fatalf("stream IDs = %v, want rotated IDs", ids)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no ordinary continuation for plain long text", sender.sends)
	}
	gotFull := strings.Join(parts, "\n\n")
	if gotFull != normalized {
		t.Fatalf("rotated streams did not reconstruct normalized reply")
	}
}

func TestGatewayStreamLongChineseReplyUsesBytePreviewLimit(t *testing.T) {
	longBody := strings.Repeat("中", wecomStreamFinalMaxBytes+500)
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": longBody},
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
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-long-chinese",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	last := sender.streams[len(sender.streams)-1]
	if !last.Finish {
		t.Fatalf("last stream finish = false, want true")
	}
	if len(sender.streams) < 2 {
		t.Fatalf("streams = %d, want rotated streams", len(sender.streams))
	}
	parts := make([]string, 0, len(sender.streams))
	for _, frame := range sender.streams {
		if !frame.Finish {
			continue
		}
		if !utf8.ValidString(frame.Content) {
			t.Fatalf("stream content is invalid utf8")
		}
		if len(frame.Content) > wecomStreamFinalMaxBytes {
			t.Fatalf("stream bytes = %d, want <= %d", len(frame.Content), wecomStreamFinalMaxBytes)
		}
		parts = append(parts, strings.TrimSpace(frame.Content))
	}
	if got := strings.Join(parts, ""); got != longBody {
		t.Fatalf("rotated Chinese streams did not reconstruct reply: got bytes=%d want=%d", len(got), len(longBody))
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no continuation", sender.sends)
	}
}

func TestGatewayStreamShortReplyBelowPreviewLimitStaysInFinalStream(t *testing.T) {
	body := strings.Repeat("a", wecomMarkdownSendMaxBytes-100)
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": body},
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
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-short-preview",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	last := sender.streams[len(sender.streams)-1]
	if !last.Finish || last.Content != body {
		t.Fatalf("last stream frame len = %d finish=%v, want full short body", len(last.Content), last.Finish)
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no continuation", sender.sends)
	}
}

func TestGatewayStreamRemainingUsesOrdinaryMarkdownSendSplitting(t *testing.T) {
	longBody := "开头\n\n" + strings.Repeat("这是一段很长的回答内容，用来触发企业微信普通 markdown 分片。\n\n", 760)
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": longBody},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	sender := &fakeSender{splitSends: true}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-remaining-split",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	if len(sender.streams) < 2 {
		t.Fatalf("streams = %d, want rotated stream chunks", len(sender.streams))
	}
	for _, frame := range sender.streams {
		if len(frame.Content) > wecomStreamFinalMaxBytes {
			t.Fatalf("stream bytes = %d, want <= %d", len(frame.Content), wecomStreamFinalMaxBytes)
		}
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no ordinary markdown continuation", sender.sends)
	}
}

func TestGatewayStreamFinalReplyAround18KBStaysInFinalStream(t *testing.T) {
	section := strings.Join([]string{
		"## 盈利分析",
		"- 收入增长来自华东与华南门店。",
		"- 毛利率受促销和履约成本影响。",
		"",
		"| 区域 | 收入 | 同比 |",
		"| --- | ---: | ---: |",
		"| 华东 | 1280万 | 12% |",
		"| 华南 | 980万 | 8% |",
		"",
		"结论：优先处理低毛利 SKU，并持续跟踪 😊。",
	}, "\n")
	longBody := strings.Repeat(section+"\n\n", 70)
	normalized := normalizeWeComMarkdown(longBody)
	if len(normalized) < 18000 {
		t.Fatalf("fixture bytes = %d, want around 18KB", len(normalized))
	}
	if len(normalized) > wecomStreamFinalMaxBytes {
		t.Fatalf("fixture bytes = %d, want <= final stream limit %d", len(normalized), wecomStreamFinalMaxBytes)
	}
	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": longBody},
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
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-long-report",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "hello",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	last := sender.streams[len(sender.streams)-1]
	if !last.Finish {
		t.Fatalf("last stream finish = false, want true")
	}
	if len(sender.streams) == 0 {
		t.Fatal("streams = nil, want paragraph streams")
	}
	streamText := make([]string, 0, len(sender.streams))
	for _, frame := range sender.streams {
		if !frame.Finish {
			continue
		}
		if len(frame.Content) > wecomStreamFinalMaxBytes {
			t.Fatalf("stream bytes = %d, want <= %d", len(frame.Content), wecomStreamFinalMaxBytes)
		}
		if !utf8.ValidString(frame.Content) {
			t.Fatalf("stream content is invalid utf8")
		}
		streamText = append(streamText, strings.TrimSpace(frame.Content))
	}
	if len(sender.sends) != 0 {
		t.Fatalf("sends = %v, want no ordinary markdown table sends", sender.sends)
	}
	delivered := strings.Join(streamText, "\n\n")
	if !strings.Contains(delivered, "| 区域 | 收入 | 同比 |") || !strings.Contains(delivered, "| --- |") || strings.Contains(delivered, "- 区域: 华东") || strings.Contains(delivered, "```") || !strings.Contains(delivered, "华东") || !strings.Contains(delivered, "😊") {
		t.Fatalf("delivered content did not preserve markdown table or lost emoji")
	}
}

func TestGatewayStreamLongReplyWithMediaDoesNotLeakProtocolAndSendsMedia(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "chart.png")
	if err := os.WriteFile(out, []byte("pngdata"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	longVisible := "报告\n\n" + strings.Repeat("明细内容用于触发长文补发。\n\n", 620)
	finalText := longVisible + "\n\n[LUMI_WECOM_SEND]\n{\"type\":\"image\",\"path\":\"chart.png\",\"caption\":\"chart\"}\n[/LUMI_WECOM_SEND]"

	runner := &scriptedRunner{
		run: func(ctx context.Context, input ChatRunInput, sink ChatEventSink) error {
			if err := sink.Emit(ChatEvent{Name: "update", Data: map[string]any{
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": finalText},
				},
			}}); err != nil {
				return err
			}
			return sink.Emit(ChatEvent{Name: "done", Data: map[string]any{"stopReason": "end_turn"}})
		},
	}
	service := newTestService(t, runner)
	service.config.Workspaces[0].Path = root
	sender := &fakeSender{}
	cfg := testGatewayConfig()
	cfg.Stream = true

	err := service.handleInboundMessage(context.Background(), cfg, WeComInboundMessage{
		ConversationKey: "wecom:chat:user",
		MessageID:       "msg-stream-long-media",
		ReplyContext:    replyContext{ReqID: "req-stream", ChatID: "chat", UserID: "user"},
		Text:            "send chart",
		ReceivedAt:      time.Now().UnixMilli(),
	}, sender)
	if err != nil {
		t.Fatalf("handleInboundMessage() error = %v", err)
	}
	for _, frame := range sender.streams {
		if strings.Contains(frame.Content, "LUMI_WECOM_SEND") {
			t.Fatalf("stream leaked protocol block: %+v", frame)
		}
	}
	if len(sender.media) != 1 || sender.media[0].Type != "image" {
		t.Fatalf("media = %v", sender.media)
	}
	if len(sender.sends) != 1 || sender.sends[0] != "chart" {
		t.Fatalf("sends = %v, want media caption only", sender.sends)
	}
	for _, frame := range sender.streams {
		if strings.Contains(frame.Content, "chart") || strings.Contains(frame.Content, "LUMI_WECOM_SEND") {
			t.Fatalf("stream leaked media caption or protocol: %+v", frame)
		}
	}
	if len(sender.replies) != 1 || sender.replies[0] != "chart" {
		t.Fatalf("replies = %v, want media caption only", sender.replies)
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
