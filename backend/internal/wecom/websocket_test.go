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

func TestHandleMsgCallbackAuthorizedRequesterPropagatesContext(t *testing.T) {
	runner := &capturingChatRunner{inputs: make(chan ChatRunInput, 1)}
	service := newTestService(t, runner)
	policy := loadWebSocketRequesterPolicyForTest(t)
	rt := newWebSocketRuntime(service, requesterWebSocketConfigForTest(), policy)
	conn, replies := newWebSocketCaptureForTest(t)
	rt.conn = conn

	rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
		t,
		"callback-req-authorized",
		"msg-authorized",
		"u1",
		"bot-1",
		"",
	))

	input := readChatRunInputForTest(t, runner.inputs)
	requester := input.RequesterContext
	if requester == nil {
		t.Fatal("ChatRunInput.RequesterContext = nil")
	}
	if requester.RequestID != "msg-authorized" || requester.PolicyRevision != policy.Revision() {
		t.Fatalf("requester context header = %+v", requester)
	}
	if requester.Principal.Channel != "wecom" || requester.Principal.BotID != "bot-1" ||
		requester.Principal.CanonicalUserID != "u1" || requester.Principal.DisplayName != "U1" {
		t.Fatalf("requester principal = %+v", requester.Principal)
	}
	if requester.Audience.ChatID != "chat-1" || requester.Audience.ChatType != "group" {
		t.Fatalf("requester audience = %+v", requester.Audience)
	}
	if strings.Join(requester.Authorization.Capabilities, ",") != "qdm.cmr.query,qdm.sql.select" ||
		strings.Join(requester.Authorization.Scope.ManageAreaIDs, ",") != "CN18" ||
		strings.Join(requester.Authorization.Scope.DCManageAreaIDs, ",") != "CN18" ||
		strings.Join(requester.Authorization.Scope.CategoryLevel1IDs, ",") != "12,13" {
		t.Fatalf("requester authorization = %+v", requester.Authorization)
	}

	_ = readWSFrameForTest(t, replies)
	waitForProcessedMessageForTest(t, rt, "msg-authorized")
}

func TestHandleMsgCallbackKeepsRequesterScopesSeparateAcrossUsers(t *testing.T) {
	runner := &capturingChatRunner{inputs: make(chan ChatRunInput, 2)}
	service := newTestService(t, runner)
	rt := newWebSocketRuntime(service, requesterWebSocketConfigForTest(), loadWebSocketRequesterPolicyForTest(t))

	rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
		t, "callback-req-u1", "msg-u1", "u1", "bot-1", "",
	))
	u1Input := readChatRunInputForTest(t, runner.inputs)
	waitForPersistedMessageForTest(t, service, "msg-u1")

	rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
		t, "callback-req-u2", "msg-u2", "u2", "bot-1", "",
	))
	u2Input := readChatRunInputForTest(t, runner.inputs)
	waitForPersistedMessageForTest(t, service, "msg-u2")

	byUser := make(map[string]ChatRunInput, 2)
	for _, input := range []ChatRunInput{u1Input, u2Input} {
		if input.RequesterContext == nil {
			t.Fatal("ChatRunInput.RequesterContext = nil")
		}
		byUser[input.RequesterContext.Principal.CanonicalUserID] = input
	}

	u1 := byUser["u1"].RequesterContext
	u2 := byUser["u2"].RequesterContext
	if u1 == nil || u2 == nil {
		t.Fatalf("captured requester users = %v, want u1 and u2", byUser)
	}
	if u1.RequestID != "msg-u1" || strings.Join(u1.Authorization.Scope.ManageAreaIDs, ",") != "CN18" || strings.Join(u1.Authorization.Scope.DCManageAreaIDs, ",") != "CN18" {
		t.Fatalf("u1 requester context = %+v", u1)
	}
	if u2.RequestID != "msg-u2" || strings.Join(u2.Authorization.Scope.ManageAreaIDs, ",") != "CN99" || strings.Join(u2.Authorization.Scope.DCManageAreaIDs, ",") != "CN99" {
		t.Fatalf("u2 requester context = %+v", u2)
	}
	if byUser["u1"].ConversationID == byUser["u2"].ConversationID {
		t.Fatalf("conversation IDs were shared across users: %q", byUser["u1"].ConversationID)
	}

	waitForProcessedMessageForTest(t, rt, "msg-u1")
	waitForProcessedMessageForTest(t, rt, "msg-u2")
}

func TestHandleMsgCallbackRejectsUnknownAndDisabledRequesterBeforeAttachmentDownload(t *testing.T) {
	tests := []struct {
		name   string
		userID string
	}{
		{name: "unknown", userID: "unknown-user"},
		{name: "disabled", userID: "disabled-user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mediaRequested := make(chan struct{}, 1)
			mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mediaRequested <- struct{}{}
				w.Header().Set("Content-Disposition", `attachment; filename="blocked.png"`)
				_, _ = w.Write([]byte("should not be downloaded"))
			}))
			defer mediaServer.Close()

			runner := &capturingChatRunner{inputs: make(chan ChatRunInput, 1)}
			service := newTestService(t, runner)
			rt := newWebSocketRuntime(service, requesterWebSocketConfigForTest(), loadWebSocketRequesterPolicyForTest(t))
			conn, replies := newWebSocketCaptureForTest(t)
			rt.conn = conn

			msgID := "msg-" + tt.name
			rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
				t,
				"callback-req-"+tt.name,
				msgID,
				tt.userID,
				"bot-1",
				mediaServer.URL+"/blocked.png",
			))

			reply := readWSFrameForTest(t, replies)
			if got := streamContentForTest(t, reply); got != requesterUnauthorizedReplyText {
				t.Fatalf("unauthorized reply = %q, want %q", got, requesterUnauthorizedReplyText)
			}
			select {
			case input := <-runner.inputs:
				t.Fatalf("runner called for %s requester: %+v", tt.name, input)
			default:
			}
			select {
			case <-mediaRequested:
				t.Fatalf("attachment downloaded for %s requester", tt.name)
			default:
			}
			waitForProcessedMessageForTest(t, rt, msgID)
		})
	}
}

func TestHandleMsgCallbackStrictRequesterPolicyRejectsInvalidIdentityFields(t *testing.T) {
	tests := []struct {
		name    string
		msgID   string
		userID  string
		aibotID string
	}{
		{name: "missing msgid", msgID: "", userID: "u1", aibotID: "bot-1"},
		{name: "missing userid", msgID: "msg-missing-user", userID: "", aibotID: "bot-1"},
		{name: "missing aibotid", msgID: "msg-missing-bot", userID: "u1", aibotID: ""},
		{name: "bot mismatch", msgID: "msg-bot-mismatch", userID: "u1", aibotID: "another-bot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mediaRequested := make(chan struct{}, 1)
			mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mediaRequested <- struct{}{}
				_, _ = w.Write([]byte("should not be downloaded"))
			}))
			defer mediaServer.Close()

			runner := &capturingChatRunner{inputs: make(chan ChatRunInput, 1)}
			service := newTestService(t, runner)
			rt := newWebSocketRuntime(service, requesterWebSocketConfigForTest(), loadWebSocketRequesterPolicyForTest(t))
			rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
				t,
				"callback-req-invalid",
				tt.msgID,
				tt.userID,
				tt.aibotID,
				mediaServer.URL+"/blocked.png",
			))

			timer := time.NewTimer(100 * time.Millisecond)
			defer timer.Stop()
			select {
			case input := <-runner.inputs:
				t.Fatalf("runner called for invalid callback: %+v", input)
			case <-mediaRequested:
				t.Fatal("attachment downloaded for invalid callback")
			case <-timer.C:
			}
			if got := processedMessageCountForTest(rt); got != 0 {
				t.Fatalf("processed message count = %d, want 0", got)
			}
		})
	}
}

func TestHandleMsgCallbackWithoutRequesterPolicyKeepsAllowFromBehavior(t *testing.T) {
	t.Run("allowed user reaches runner without requester context", func(t *testing.T) {
		runner := &capturingChatRunner{inputs: make(chan ChatRunInput, 1)}
		service := newTestService(t, runner)
		cfg := requesterWebSocketConfigForTest()
		cfg.AllowFrom = "another-user, allowed-user"
		rt := newWebSocketRuntime(service, cfg, nil)
		conn, replies := newWebSocketCaptureForTest(t)
		rt.conn = conn

		rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
			t,
			"callback-req-legacy",
			"msg-legacy-allowed",
			"allowed-user",
			"",
			"",
		))

		input := readChatRunInputForTest(t, runner.inputs)
		if input.RequesterContext != nil {
			t.Fatalf("legacy ChatRunInput.RequesterContext = %+v, want nil", input.RequesterContext)
		}
		_ = readWSFrameForTest(t, replies)
		waitForProcessedMessageForTest(t, rt, "msg-legacy-allowed")
	})

	t.Run("disallowed user is silently ignored", func(t *testing.T) {
		mediaRequested := make(chan struct{}, 1)
		mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mediaRequested <- struct{}{}
			_, _ = w.Write([]byte("should not be downloaded"))
		}))
		defer mediaServer.Close()

		runner := &capturingChatRunner{inputs: make(chan ChatRunInput, 1)}
		service := newTestService(t, runner)
		cfg := requesterWebSocketConfigForTest()
		cfg.AllowFrom = "allowed-user"
		rt := newWebSocketRuntime(service, cfg, nil)
		rt.handleMsgCallback(context.Background(), requesterCallbackFrameForTest(
			t,
			"callback-req-legacy-denied",
			"msg-legacy-denied",
			"denied-user",
			"",
			mediaServer.URL+"/blocked.png",
		))

		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case input := <-runner.inputs:
			t.Fatalf("runner called for legacy disallowed user: %+v", input)
		case <-mediaRequested:
			t.Fatal("attachment downloaded for legacy disallowed user")
		case <-timer.C:
		}
		if got := processedMessageCountForTest(rt); got != 0 {
			t.Fatalf("processed message count = %d, want 0", got)
		}
	})
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

type capturingChatRunner struct {
	inputs chan ChatRunInput
}

func (r *capturingChatRunner) RunWeComChat(_ context.Context, input ChatRunInput, _ ChatEventSink) error {
	r.inputs <- input
	return nil
}

func requesterWebSocketConfigForTest() Config {
	return Config{
		BotID:               "bot-1",
		WorkspaceID:         "default",
		AgentID:             "claude",
		MessageAckTimeoutMs: 1000,
	}
}

func loadWebSocketRequesterPolicyForTest(t *testing.T) *RequesterPolicy {
	t.Helper()
	const raw = `{
  "version": 1,
  "botId": "bot-1",
  "users": [
    {
      "userId": "u1",
      "displayName": "U1",
      "enabled": true,
      "capabilities": ["qdm.cmr.query", "qdm.sql.select"],
      "scope": {"manageAreaIds":["CN18"],"dcManageAreaIds":["CN18"],"categoryLevel1Ids":["12", "13"]}
    },
    {
      "userId": "disabled-user",
      "displayName": "Disabled",
      "enabled": false,
      "capabilities": [],
      "scope": {"manageAreaIds":[],"dcManageAreaIds":[],"categoryLevel1Ids":[]}
    },
    {
      "userId": "u2",
      "displayName": "U2",
      "enabled": true,
      "capabilities": ["qdm.indicators.query"],
      "scope": {"manageAreaIds":["CN99"],"dcManageAreaIds":["CN99"],"categoryLevel1Ids":["88"]}
    }
  ]
}`
	policy, err := LoadRequesterPolicy(writeRequesterPolicyFile(t, raw), "bot-1")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy() error = %v", err)
	}
	return policy
}

func requesterCallbackFrameForTest(t *testing.T, reqID, msgID, userID, aibotID, mediaURL string) wsFrame {
	t.Helper()
	body := map[string]any{
		"msgid":    msgID,
		"aibotid":  aibotID,
		"chatid":   "chat-1",
		"chattype": "group",
		"from":     map[string]string{"userid": userID},
		"msgtype":  "text",
		"text":     map[string]string{"content": "hello"},
	}
	if mediaURL != "" {
		body["msgtype"] = "image"
		body["image"] = map[string]string{"url": mediaURL}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal(callback body) error = %v", err)
	}
	return wsFrame{
		Cmd:     "aibot_msg_callback",
		Headers: wsFrameHeaders{ReqID: reqID},
		Body:    raw,
	}
}

func newWebSocketCaptureForTest(t *testing.T) (*websocket.Conn, <-chan wsFrame) {
	t.Helper()
	frames := make(chan wsFrame, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade capture websocket error = %v", err)
			return
		}
		defer conn.Close()
		var frame wsFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Errorf("read captured websocket frame error = %v", err)
			return
		}
		frames <- frame
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatalf("Dial(capture websocket) error = %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		server.Close()
	})
	return conn, frames
}

func readChatRunInputForTest(t *testing.T, inputs <-chan ChatRunInput) ChatRunInput {
	t.Helper()
	select {
	case input := <-inputs:
		return input
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ChatRunInput")
		return ChatRunInput{}
	}
}

func streamContentForTest(t *testing.T, frame wsFrame) string {
	t.Helper()
	if frame.Cmd != "aibot_respond_msg" {
		t.Fatalf("frame cmd = %q, want aibot_respond_msg", frame.Cmd)
	}
	var body struct {
		MsgType string `json:"msgtype"`
		Stream  struct {
			Content string `json:"content"`
		} `json:"stream"`
	}
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatalf("Unmarshal(reply body) error = %v", err)
	}
	if body.MsgType != "stream" {
		t.Fatalf("reply msgtype = %q, want stream", body.MsgType)
	}
	return body.Stream.Content
}

func waitForProcessedMessageForTest(t *testing.T, rt *wsRuntime, msgID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !rt.hasProcessed(msgID) {
		if time.Now().After(deadline) {
			t.Fatalf("message %q was not marked processed", msgID)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rt.service != nil {
		waitForPersistedMessageForTest(t, rt.service, msgID)
	}
}

func waitForPersistedMessageForTest(t *testing.T, service *Service, msgID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, err := service.runtimeStore.Load()
		if err == nil {
			for _, processedID := range state.ProcessedMessageIDs {
				if processedID == msgID {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("message %q was not persisted: %v", msgID, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func processedMessageCountForTest(rt *wsRuntime) int {
	rt.processedMu.Lock()
	defer rt.processedMu.Unlock()
	return len(rt.processedIDs)
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
