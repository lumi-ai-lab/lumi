package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pengmide/lumi/internal/config"
)

func TestDialAndSubscribeTimesOutWaitingForSubscribeResponse(t *testing.T) {
	upgrader := websocket.Upgrader{}
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer close(closed)
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read subscribe frame error = %v", err)
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	originalEndpoint := wsEndpoint
	originalDialer := wsDialer
	wsEndpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	wsDialer = websocket.DefaultDialer
	t.Cleanup(func() {
		wsEndpoint = originalEndpoint
		wsDialer = originalDialer
	})

	rt := &wsRuntime{
		cfg: Config{
			BotID:            "bot-1",
			BotSecret:        "secret-1",
			ConnectTimeoutMs: 100,
		},
	}

	start := time.Now()
	conn, err := rt.dialAndSubscribe(context.Background())
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("dialAndSubscribe() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "subscribe response") {
		t.Fatalf("error = %q, want subscribe response timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("dialAndSubscribe() elapsed = %s, want under 2s", elapsed)
	}

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("server connection was not closed after subscribe timeout")
	}
}

func TestStopClosesIdleWebSocketConnection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	upgrader := websocket.Upgrader{}
	subscribed := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer close(closed)
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read subscribe frame error = %v", err)
			return
		}
		resp := wsFrame{ErrCode: intPtr(0)}
		raw, _ := json.Marshal(resp)
		if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
			t.Errorf("write subscribe response error = %v", err)
			return
		}
		close(subscribed)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	originalEndpoint := wsEndpoint
	originalDialer := wsDialer
	wsEndpoint = "ws" + strings.TrimPrefix(server.URL, "http")
	wsDialer = websocket.DefaultDialer
	t.Cleanup(func() {
		wsEndpoint = originalEndpoint
		wsDialer = originalDialer
	})

	cfg := &config.Config{
		Workspaces: []config.WorkspaceConfig{
			{ID: "default", Name: "Default", Path: t.TempDir()},
		},
		Agents: []config.AgentConfig{
			{ID: "agent-1", Name: "Agent"},
		},
	}
	svc := NewService(cfg, &noopChatRunner{})
	if err := svc.configStore.Save(Config{
		Enabled:             true,
		Mode:                defaultMode,
		BotID:               "bot-1",
		BotSecret:           "secret-1",
		WorkspaceID:         "default",
		AgentID:             "agent-1",
		ConnectTimeoutMs:    1000,
		HeartbeatIntervalMs: 30000,
		MessageAckTimeoutMs: 1000,
	}); err != nil {
		t.Fatalf("save config error = %v", err)
	}

	if err := svc.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not subscribe")
	}

	done := make(chan error, 1)
	go func() {
		done <- svc.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return after closing idle websocket")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("server connection was not closed")
	}
}

func TestSendReturnsErrorOnAckTimeout(t *testing.T) {
	upgrader := websocket.Upgrader{}
	frameRead := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read send frame error = %v", err)
			return
		}
		if frame.Cmd != "aibot_send_msg" {
			t.Errorf("frame cmd = %q, want aibot_send_msg", frame.Cmd)
		}
		close(frameRead)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	rt := &wsRuntime{
		cfg: Config{
			MessageAckTimeoutMs: 20,
		},
		conn: conn,
	}
	err = rt.Send(context.Background(), replyContext{ChatID: "chat"}, "hello")
	if err == nil || !strings.Contains(err.Error(), "ack timeout") {
		t.Fatalf("Send() error = %v, want ack timeout", err)
	}
	select {
	case <-frameRead:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive aibot_send_msg")
	}
}

func TestReplyStreamReturnsContextErrorBeforeWrite(t *testing.T) {
	rt := &wsRuntime{cfg: Config{MessageAckTimeoutMs: 1000}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := rt.ReplyStream(ctx, replyContext{ReqID: "req-1", ChatID: "chat"}, "stream-1", "hello", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReplyStream() error = %v, want context canceled", err)
	}
}

func TestReplyStreamFramesUseOriginalReqIDForEveryChunk(t *testing.T) {
	upgrader := websocket.Upgrader{}
	frames := make(chan wsFrame, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer conn.Close()

		for i := 0; i < 2; i++ {
			var frame wsFrame
			if err := conn.ReadJSON(&frame); err != nil {
				t.Errorf("read stream frame error = %v", err)
				return
			}
			frames <- frame
			ack := wsFrame{Headers: wsFrameHeaders{ReqID: frame.Headers.ReqID}, ErrCode: intPtr(0)}
			if err := conn.WriteJSON(ack); err != nil {
				t.Errorf("write stream ack error = %v", err)
				return
			}
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	rt := &wsRuntime{cfg: Config{MessageAckTimeoutMs: 1000}, conn: conn}
	rctx := replyContext{ReqID: "callback-req-1", ChatID: "chat"}
	if err := rt.ReplyStream(context.Background(), rctx, "stream-1", "hello", false); err != nil {
		t.Fatalf("ReplyStream(update) error = %v", err)
	}
	if err := rt.ReplyStream(context.Background(), rctx, "stream-1", "hello world", true); err != nil {
		t.Fatalf("ReplyStream(final) error = %v", err)
	}

	first := readWSFrameForTest(t, frames)
	second := readWSFrameForTest(t, frames)
	assertStreamFrameForTest(t, first, "callback-req-1", "stream-1", "hello", false)
	assertStreamFrameForTest(t, second, "callback-req-1", "stream-1", "hello world", true)
}

func TestReplyStreamAckErrorIsCurrentlyBestEffort(t *testing.T) {
	upgrader := websocket.Upgrader{}
	ackWritten := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read stream frame error = %v", err)
			return
		}
		ack := wsFrame{
			Headers: wsFrameHeaders{ReqID: frame.Headers.ReqID},
			ErrCode: intPtr(45009),
			ErrMsg:  "stream expired",
		}
		if err := conn.WriteJSON(ack); err != nil {
			t.Errorf("write stream ack error = %v", err)
			return
		}
		close(ackWritten)
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	rt := &wsRuntime{cfg: Config{MessageAckTimeoutMs: 1000}, conn: conn}
	ackHandled := make(chan error, 1)
	go func() {
		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			ackHandled <- err
			return
		}
		rt.handleFrame(context.Background(), frame)
		ackHandled <- nil
	}()

	err = rt.ReplyStream(context.Background(), replyContext{ReqID: "callback-req-1", ChatID: "chat"}, "stream-1", "final", true)
	if err != nil {
		t.Fatalf("ReplyStream() error = %v, want nil because stream ack is not awaited yet", err)
	}
	select {
	case <-ackWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not write stream ack error")
	}
	if _, ok := rt.pendingAcks.Load("callback-req-1"); ok {
		t.Fatal("ReplyStream registered a pending ack unexpectedly")
	}
	select {
	case err := <-ackHandled:
		if err != nil {
			t.Fatalf("read stream ack error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not handle stream ack error")
	}
}

func TestReplyStreamFinalReturnsAckError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	frameRead := make(chan wsFrame, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read stream frame error = %v", err)
			return
		}
		frameRead <- frame
		ack := wsFrame{
			Headers: wsFrameHeaders{ReqID: frame.Headers.ReqID},
			ErrCode: intPtr(45009),
			ErrMsg:  "stream expired",
		}
		if err := conn.WriteJSON(ack); err != nil {
			t.Errorf("write stream ack error = %v", err)
			return
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	rt := &wsRuntime{cfg: Config{MessageAckTimeoutMs: 1000}, conn: conn}
	ackHandled := make(chan error, 1)
	go handleOneAckForTest(rt, conn, ackHandled)

	err = rt.ReplyStreamFinal(context.Background(), replyContext{ReqID: "callback-req-1", ChatID: "chat"}, "stream-1", "final")
	if err == nil || !strings.Contains(err.Error(), "stream expired") {
		t.Fatalf("ReplyStreamFinal() error = %v, want stream expired ack error", err)
	}
	frame := readWSFrameForTest(t, frameRead)
	assertStreamFrameForTest(t, frame, "callback-req-1", "stream-1", "final", true)
	select {
	case err := <-ackHandled:
		if err != nil {
			t.Fatalf("handle ack error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not handle stream final ack")
	}
}

func TestReplyStreamFinalReturnsAckTimeout(t *testing.T) {
	upgrader := websocket.Upgrader{}
	frameRead := make(chan wsFrame, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade error = %v", err)
			return
		}
		defer conn.Close()

		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read stream frame error = %v", err)
			return
		}
		frameRead <- frame
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	rt := &wsRuntime{cfg: Config{MessageAckTimeoutMs: 20}, conn: conn}
	err = rt.ReplyStreamFinal(context.Background(), replyContext{ReqID: "callback-req-1", ChatID: "chat"}, "stream-1", "final")
	if err == nil || !strings.Contains(err.Error(), "ack timeout") {
		t.Fatalf("ReplyStreamFinal() error = %v, want ack timeout", err)
	}
	frame := readWSFrameForTest(t, frameRead)
	assertStreamFrameForTest(t, frame, "callback-req-1", "stream-1", "final", true)
	if _, ok := rt.pendingAcks.Load("callback-req-1"); ok {
		t.Fatal("ReplyStreamFinal left pending ack after timeout")
	}
}

func TestMessageAckTimeoutUsesDefaultAndMinimum(t *testing.T) {
	if got := (&wsRuntime{}).messageAckTimeout(); got != time.Duration(defaultMessageAckTimeoutMs)*time.Millisecond {
		t.Fatalf("default messageAckTimeout = %s, want %dms", got, defaultMessageAckTimeoutMs)
	}
	if got := (&wsRuntime{cfg: Config{MessageAckTimeoutMs: 20}}).messageAckTimeout(); got != time.Second {
		t.Fatalf("minimum messageAckTimeout = %s, want 1s", got)
	}
}

func handleOneAckForTest(rt *wsRuntime, conn *websocket.Conn, done chan<- error) {
	var frame wsFrame
	if err := conn.ReadJSON(&frame); err != nil {
		done <- err
		return
	}
	rt.handleFrame(context.Background(), frame)
	done <- nil
}

type noopChatRunner struct{}

func (r *noopChatRunner) RunWeComChat(context.Context, ChatRunInput, ChatEventSink) error {
	return nil
}

func intPtr(v int) *int {
	return &v
}

func readWSFrameForTest(t *testing.T, ch <-chan wsFrame) wsFrame {
	t.Helper()
	select {
	case frame := <-ch:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket frame")
		return wsFrame{}
	}
}

func assertStreamFrameForTest(t *testing.T, frame wsFrame, reqID, streamID, content string, finish bool) {
	t.Helper()
	if frame.Cmd != "aibot_respond_msg" {
		t.Fatalf("frame cmd = %q, want aibot_respond_msg", frame.Cmd)
	}
	if frame.Headers.ReqID != reqID {
		t.Fatalf("frame req_id = %q, want %q", frame.Headers.ReqID, reqID)
	}
	var body struct {
		MsgType string `json:"msgtype"`
		Stream  struct {
			ID      string `json:"id"`
			Finish  bool   `json:"finish"`
			Content string `json:"content"`
		} `json:"stream"`
	}
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatalf("unmarshal stream body error = %v", err)
	}
	if body.MsgType != "stream" {
		t.Fatalf("body msgtype = %q, want stream", body.MsgType)
	}
	if body.Stream.ID != streamID {
		t.Fatalf("stream id = %q, want %q", body.Stream.ID, streamID)
	}
	if body.Stream.Content != content {
		t.Fatalf("stream content = %q, want %q", body.Stream.Content, content)
	}
	if body.Stream.Finish != finish {
		t.Fatalf("stream finish = %v, want %v", body.Stream.Finish, finish)
	}
}
