package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/conversation"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/storage"
	"github.com/pengmide/lumi/internal/wechat"
	"github.com/pengmide/lumi/internal/wecom"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func TestHandleDeviceChatBridgesSSEAndRoutesPermissionConfirm(t *testing.T) {
	server := newTestAPIServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	registerAndReadyDevice(t, ctx, conn)

	bodyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		payload := bytes.NewBufferString(`{"message":"hello remote","conversationId":"","workspaceId":"default","deviceId":"dev-1"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/chat", payload)
		if err != nil {
			errCh <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			errCh <- err
			return
		}
		bodyCh <- string(data)
	}()

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-session", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "remote-session-1",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgPermissionRequest, "msg-perm", "dev-1", taskExecute.TaskID, device.PermissionRequestPayload{
		SessionID: "remote-session-1",
		Options: []device.PermissionOption{
			{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
		},
		ToolCall: device.PermissionToolCall{
			ToolCallID: "tool-1",
			Title:      "Run command",
			Kind:       "command",
			RawInput:   json.RawMessage(`{"command":"pwd"}`),
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskEvent, "msg-event", "dev-1", taskExecute.TaskID, device.TaskEventPayload{
		SessionID: "remote-session-1",
		Notification: device.ACPNotification{
			JSONRPC: "2.0",
			Method:  "session/update",
			Params:  json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello from device"}}}`),
		},
	}))

	confirmAckCh := make(chan error, 1)
	go func() {
		permissionConfirm := readEnvelope(t, ctx, conn)
		if permissionConfirm.Type != device.MsgPermissionConfirm {
			confirmAckCh <- io.ErrUnexpectedEOF
			return
		}
		confirmAckCh <- wsjson.Write(ctx, conn, device.AckEnvelope(permissionConfirm.ID))
	}()

	confirmPayload := bytes.NewBufferString(`{"agentId":"claude","toolCallId":"tool-1","optionId":"allow-once"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/permission/confirm", confirmPayload)
	if err != nil {
		t.Fatalf("NewRequest(permission.confirm) error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("permission confirm request error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("permission confirm status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if err := <-confirmAckCh; err != nil {
		t.Fatalf("permission confirm ack error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-done", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		t.Fatalf("chat request error = %v", err)
	case body := <-bodyCh:
		if !strings.Contains(body, `event: session`) || !strings.Contains(body, `remote-session-1`) {
			t.Fatalf("SSE body missing session event: %s", body)
		}
		if !strings.Contains(body, `event: permission_request`) || !strings.Contains(body, `tool-1`) {
			t.Fatalf("SSE body missing permission request: %s", body)
		}
		if !strings.Contains(body, `hello from device`) {
			t.Fatalf("SSE body missing streamed text: %s", body)
		}
		if !strings.Contains(body, `event: done`) {
			t.Fatalf("SSE body missing done event: %s", body)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE response")
	}

	if got := server.getRemoteSession(server.sessionStore.List()[0].ID, "dev-1", "claude"); got != "remote-session-1" {
		t.Fatalf("getRemoteSession() = %q, want %q", got, "remote-session-1")
	}

	convID := server.sessionStore.List()[0].ID
	secondBodyCh := make(chan string, 1)
	secondErrCh := make(chan error, 1)
	go func() {
		payload := bytes.NewBufferString(`{"message":"write a file","conversationId":"` + convID + `","workspaceId":"default","deviceId":"dev-1"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/chat", payload)
		if err != nil {
			secondErrCh <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			secondErrCh <- err
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			secondErrCh <- err
			return
		}
		secondBodyCh <- string(data)
	}()

	secondTaskExecute := readEnvelope(t, ctx, conn)
	if secondTaskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("second taskExecute.Type = %q, want %q", secondTaskExecute.Type, device.MsgTaskExecute)
	}
	var secondPayload device.TaskExecutePayload
	if err := json.Unmarshal(secondTaskExecute.Payload, &secondPayload); err != nil {
		t.Fatalf("Unmarshal(second task.execute payload) error = %v", err)
	}
	if secondPayload.SessionID != "remote-session-1" {
		t.Fatalf("second task sessionId = %q, want remote-session-1", secondPayload.SessionID)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(secondTaskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(second task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgPermissionRequest, "msg-perm-2", "dev-1", secondTaskExecute.TaskID, device.PermissionRequestPayload{
		SessionID: "remote-session-1",
		Options: []device.PermissionOption{
			{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
		},
		ToolCall: device.PermissionToolCall{
			ToolCallID: "tool-2",
			Title:      "Write file",
			Kind:       "edit",
			RawInput:   json.RawMessage(`{"file_path":"a.txt"}`),
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-done-2", "dev-1", secondTaskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-secondErrCh:
		t.Fatalf("second chat request error = %v", err)
	case body := <-secondBodyCh:
		if strings.Contains(body, "Device protocol error") {
			t.Fatalf("second SSE body should not contain protocol error: %s", body)
		}
		if !strings.Contains(body, `event: permission_request`) || !strings.Contains(body, `tool-2`) {
			t.Fatalf("second SSE body missing permission request: %s", body)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for second SSE response")
	}
}

func TestRunWeComChatRoutesSandboxWorkspaceToDeviceTask(t *testing.T) {
	server := newTestAPIServer(t)
	workspace := config.WorkspaceConfig{
		ID:    "sandbox-ws",
		Name:  "Sandbox",
		Path:  t.TempDir(),
		Kind:  "sandbox",
		Image: sandbox.DefaultImage,
	}
	server.config.Workspaces = append(server.config.Workspaces, workspace)
	server.sandbox = &fakeSandboxManager{ensureState: sandbox.RuntimeState{
		WorkspaceID:   workspace.ID,
		DeviceID:      "dev-1",
		WorkspacePath: sandbox.WorkspacePath,
		Status:        sandbox.StatusRunning,
	}}

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgDeviceRegister, "msg-register-codex", "dev-1", "", device.DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents: []device.DeviceAgentInfo{
			{ID: "claude", Name: "Claude Code"},
			{ID: "codex", Name: "Codex CLI"},
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgSetupStatus, "msg-setup-codex", "dev-1", "", device.SetupStatusPayload{
		Ready: true,
	}))

	sink := &recordingWeComSink{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.RunWeComChat(ctx, wecom.ChatRunInput{
			Message:           "hello sandbox",
			ConversationID:    "wecom_test",
			WorkspaceID:       workspace.ID,
			WorkspacePath:     workspace.Path,
			AgentID:           "claude",
			PromptPrefix:      "prefix",
			ConversationStore: &memoryIMStore{},
		}, sink)
	}()

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	var taskPayload device.TaskExecutePayload
	if err := json.Unmarshal(taskExecute.Payload, &taskPayload); err != nil {
		t.Fatalf("Unmarshal(task.execute) error = %v", err)
	}
	if taskPayload.WorkspacePath != sandbox.WorkspacePath {
		t.Fatalf("WorkspacePath = %q, want %q", taskPayload.WorkspacePath, sandbox.WorkspacePath)
	}
	if !strings.Contains(taskPayload.Prompt, "prefix") || !strings.Contains(taskPayload.Prompt, "hello sandbox") {
		t.Fatalf("Prompt missing IM content: %q", taskPayload.Prompt)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-im-session", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "sandbox-session",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgPermissionRequest, "msg-im-permission", "dev-1", taskExecute.TaskID, device.PermissionRequestPayload{
		SessionID: "sandbox-session",
		Options: []device.PermissionOption{
			{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
		},
		ToolCall: device.PermissionToolCall{
			ToolCallID: "tool-im",
			Title:      "Write file",
			Kind:       "edit",
			RawInput:   json.RawMessage(`{"file_path":"a.txt"}`),
		},
	}))
	permissionConfirm := readEnvelope(t, ctx, conn)
	if permissionConfirm.Type != device.MsgPermissionConfirm {
		t.Fatalf("permissionConfirm.Type = %q, want %q", permissionConfirm.Type, device.MsgPermissionConfirm)
	}
	var permissionPayload device.PermissionConfirmPayload
	if err := json.Unmarshal(permissionConfirm.Payload, &permissionPayload); err != nil {
		t.Fatalf("Unmarshal(permission.confirm) error = %v", err)
	}
	if permissionPayload.ToolCallID != "tool-im" || permissionPayload.OptionID != "allow-once" {
		t.Fatalf("permission confirm payload = %+v, want tool-im allow-once", permissionPayload)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(permissionConfirm.ID)); err != nil {
		t.Fatalf("wsjson.Write(permission.confirm ack) error = %v", err)
	}
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskEvent, "msg-im-event", "dev-1", taskExecute.TaskID, device.TaskEventPayload{
		SessionID: "sandbox-session",
		Notification: device.ACPNotification{
			JSONRPC: "2.0",
			Method:  "session/update",
			Params:  json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello from sandbox"}}}`),
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-im-done", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWeComChat() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for RunWeComChat")
	}
	if !sink.hasUpdateText("hello from sandbox") {
		t.Fatalf("sink events missing sandbox response: %+v", sink.events)
	}
}

func TestRunWeComChatSandboxSwitchPersistsActiveAgentAndInjectsContext(t *testing.T) {
	server := newTestAPIServer(t)
	workspace := config.WorkspaceConfig{
		ID:    "sandbox-ws",
		Name:  "Sandbox",
		Path:  t.TempDir(),
		Kind:  "sandbox",
		Image: sandbox.DefaultImage,
	}
	server.config.Workspaces = append(server.config.Workspaces, workspace)
	server.sandbox = &fakeSandboxManager{ensureState: sandbox.RuntimeState{
		WorkspaceID:   workspace.ID,
		DeviceID:      "dev-1",
		WorkspacePath: sandbox.WorkspacePath,
		Status:        sandbox.StatusRunning,
	}}

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgDeviceRegister, "msg-register-codex-context", "dev-1", "", device.DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents: []device.DeviceAgentInfo{
			{ID: "claude", Name: "Claude Code"},
			{ID: "codex", Name: "Codex CLI"},
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgSetupStatus, "msg-setup-codex-context", "dev-1", "", device.SetupStatusPayload{
		Ready: true,
	}))

	store := &memoryIMStore{session: &storage.StoredSession{
		ID:          "wecom_context",
		ActiveAgent: "claude",
		WorkspaceID: workspace.ID,
		CreatedAt:   time.Now().UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
		Messages: []conversation.Message{
			{Role: "user", Content: "previous question", Timestamp: time.Now().UnixMilli()},
			{Role: "assistant", Agent: "claude", Content: "previous answer", Timestamp: time.Now().UnixMilli()},
		},
	}}

	sink := &recordingWeComSink{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.RunWeComChat(ctx, wecom.ChatRunInput{
			Message:           "new question",
			ConversationID:    "wecom_context",
			WorkspaceID:       workspace.ID,
			WorkspacePath:     workspace.Path,
			AgentID:           "codex",
			PromptPrefix:      "prefix",
			ConversationStore: store,
		}, sink)
	}()

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	var taskPayload device.TaskExecutePayload
	if err := json.Unmarshal(taskExecute.Payload, &taskPayload); err != nil {
		t.Fatalf("Unmarshal(task.execute) error = %v", err)
	}
	if taskPayload.AgentID != "codex" {
		t.Fatalf("taskPayload.AgentID = %q, want codex", taskPayload.AgentID)
	}
	for _, want := range []string{"[Previous conversation context]", "User: previous question", "Assistant (claude): previous answer", "prefix", "new question"} {
		if !strings.Contains(taskPayload.Prompt, want) {
			t.Fatalf("task prompt missing %q:\n%s", want, taskPayload.Prompt)
		}
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-im-session", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "sandbox-session",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-im-done", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWeComChat() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for RunWeComChat")
	}
	if store.session == nil || store.session.ActiveAgent != "codex" {
		t.Fatalf("stored session = %+v, want active codex", store.session)
	}
}

func TestRunWeChatChatRoutesSandboxWorkspaceToDeviceTask(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.FindAgent("claude").SessionMode = agentmode.ClaudeModeBypassPermissions
	workspace := config.WorkspaceConfig{
		ID:    "sandbox-ws",
		Name:  "Sandbox",
		Path:  t.TempDir(),
		Kind:  "sandbox",
		Image: sandbox.DefaultImage,
	}
	server.config.Workspaces = append(server.config.Workspaces, workspace)
	server.sandbox = &fakeSandboxManager{ensureState: sandbox.RuntimeState{
		WorkspaceID:   workspace.ID,
		DeviceID:      "dev-1",
		WorkspacePath: sandbox.WorkspacePath,
		Status:        sandbox.StatusRunning,
	}}

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")
	registerAndReadyDevice(t, ctx, conn)

	sink := &recordingWeChatSink{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.RunWeChatChat(ctx, wechat.ChatRunInput{
			Message:           "hello sandbox",
			ConversationID:    "wechat_test",
			WorkspaceID:       workspace.ID,
			WorkspacePath:     workspace.Path,
			AgentID:           "claude",
			PromptPrefix:      "prefix",
			ConversationStore: &memoryIMStore{},
		}, sink)
	}()

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	var taskPayload device.TaskExecutePayload
	if err := json.Unmarshal(taskExecute.Payload, &taskPayload); err != nil {
		t.Fatalf("Unmarshal(task.execute) error = %v", err)
	}
	if taskPayload.WorkspacePath != sandbox.WorkspacePath {
		t.Fatalf("WorkspacePath = %q, want %q", taskPayload.WorkspacePath, sandbox.WorkspacePath)
	}
	if !strings.Contains(taskPayload.Prompt, "prefix") || !strings.Contains(taskPayload.Prompt, "hello sandbox") {
		t.Fatalf("Prompt missing IM content: %q", taskPayload.Prompt)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-im-session", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "sandbox-session",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskEvent, "msg-im-event", "dev-1", taskExecute.TaskID, device.TaskEventPayload{
		SessionID: "sandbox-session",
		Notification: device.ACPNotification{
			JSONRPC: "2.0",
			Method:  "session/update",
			Params:  json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello from sandbox"}}}`),
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-im-done", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunWeChatChat() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for RunWeChatChat")
	}
	if !sink.hasUpdateText("hello from sandbox") {
		t.Fatalf("sink events missing sandbox response: %+v", sink.events)
	}
}

func TestHandleDeviceChatBuffersNotificationsBeforeSession(t *testing.T) {
	server := newTestAPIServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	registerAndReadyDevice(t, ctx, conn)

	bodyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		payload := bytes.NewBufferString(`{"message":"hello remote","workspaceId":"default","deviceId":"dev-1"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/chat", payload)
		if err != nil {
			errCh <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			errCh <- err
			return
		}
		bodyCh <- string(data)
	}()

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskEvent, "msg-event-before-session", "dev-1", taskExecute.TaskID, device.TaskEventPayload{
		Notification: device.ACPNotification{
			JSONRPC: "2.0",
			Method:  "session/update",
			Params:  json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"early hello"}}}`),
		},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-session-after-event", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "remote-session-early",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-done-after-event", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		t.Fatalf("chat request error = %v", err)
	case body := <-bodyCh:
		if strings.Contains(body, "Device protocol error") {
			t.Fatalf("SSE body should not contain protocol error: %s", body)
		}
		if !strings.Contains(body, `event: session`) || !strings.Contains(body, `remote-session-early`) {
			t.Fatalf("SSE body missing session event: %s", body)
		}
		if !strings.Contains(body, `early hello`) {
			t.Fatalf("SSE body missing buffered text: %s", body)
		}
		if !strings.Contains(body, `event: done`) {
			t.Fatalf("SSE body missing done event: %s", body)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE response")
	}
}

func TestHandleDeviceChatAutoApprovesPermissionsForYoloMode(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.FindAgent("claude").SessionMode = agentmode.ClaudeModeBypassPermissions
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	registerAndReadyDevice(t, ctx, conn)

	bodyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		payload := bytes.NewBufferString(`{"message":"create a file","conversationId":"","workspaceId":"default","deviceId":"dev-1"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/chat", payload)
		if err != nil {
			errCh <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			errCh <- err
			return
		}
		bodyCh <- string(data)
	}()

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-session-auto", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "remote-session-auto",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgPermissionRequest, "msg-perm-auto", "dev-1", taskExecute.TaskID, device.PermissionRequestPayload{
		SessionID: "remote-session-auto",
		Options: []device.PermissionOption{
			{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
		},
		ToolCall: device.PermissionToolCall{
			ToolCallID: "tool-auto",
			Title:      "Write file",
			Kind:       "edit",
			RawInput:   json.RawMessage(`{"file_path":"a.txt"}`),
		},
	}))

	permissionConfirm := readEnvelope(t, ctx, conn)
	if permissionConfirm.Type != device.MsgPermissionConfirm {
		t.Fatalf("permissionConfirm.Type = %q, want %q", permissionConfirm.Type, device.MsgPermissionConfirm)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(permissionConfirm.ID)); err != nil {
		t.Fatalf("wsjson.Write(permission.confirm ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-done-auto", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		t.Fatalf("chat request error = %v", err)
	case body := <-bodyCh:
		if strings.Contains(body, `event: permission_request`) {
			t.Fatalf("SSE body should not contain permission_request when yolo mode is enabled: %s", body)
		}
		if !strings.Contains(body, `event: done`) {
			t.Fatalf("SSE body missing done event: %s", body)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE response")
	}
}

func TestHandleDeviceChatInlinesRemoteWorkspaceFileMentions(t *testing.T) {
	server := newTestAPIServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	registerAndReadyDevice(t, ctx, conn)

	remoteDir := t.TempDir()
	server.config.Workspaces = append(server.config.Workspaces, config.WorkspaceConfig{
		ID:         "remote-ws",
		Name:       "Remote",
		Path:       remoteDir,
		Kind:       "remote",
		DeviceID:   "dev-1",
		DeviceName: "Office Mac",
		RemotePath: remoteDir,
	})

	bodyCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		payload := bytes.NewBufferString(`{"message":"Review @src/app.ts for me","conversationId":"","workspaceId":"remote-ws","deviceId":"dev-1"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/api/chat", payload)
		if err != nil {
			errCh <- err
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			errCh <- err
			return
		}
		bodyCh <- string(data)
	}()

	workspaceReq := readEnvelope(t, ctx, conn)
	if workspaceReq.Type != device.MsgWorkspaceText {
		t.Fatalf("workspaceReq.Type = %q, want %q", workspaceReq.Type, device.MsgWorkspaceText)
	}
	var workspacePayload device.WorkspaceRequestPayload
	if err := json.Unmarshal(workspaceReq.Payload, &workspacePayload); err != nil {
		t.Fatalf("Unmarshal(workspace request payload) error = %v", err)
	}
	if workspacePayload.WorkspacePath != remoteDir {
		t.Fatalf("workspacePayload.WorkspacePath = %q, want %q", workspacePayload.WorkspacePath, remoteDir)
	}
	if workspacePayload.Path != "src/app.ts" {
		t.Fatalf("workspacePayload.Path = %q, want %q", workspacePayload.Path, "src/app.ts")
	}

	workspaceResp, err := device.NewEnvelope(device.MsgWorkspaceResponse, workspaceReq.ID, "dev-1", "", device.WorkspaceResponsePayload{
		OK: true,
		Payload: json.RawMessage(`{
			"meta":{"path":"src/app.ts","name":"app.ts","size":18,"modifiedAt":1700000000000,"previewKind":"code"},
			"content":"export const ok = true;\n"
		}`),
	})
	if err != nil {
		t.Fatalf("NewEnvelope(workspace.response) error = %v", err)
	}
	if err := wsjson.Write(ctx, conn, workspaceResp); err != nil {
		t.Fatalf("wsjson.Write(workspace.response) error = %v", err)
	}

	taskExecute := readEnvelope(t, ctx, conn)
	if taskExecute.Type != device.MsgTaskExecute {
		t.Fatalf("taskExecute.Type = %q, want %q", taskExecute.Type, device.MsgTaskExecute)
	}
	var taskPayload device.TaskExecutePayload
	if err := json.Unmarshal(taskExecute.Payload, &taskPayload); err != nil {
		t.Fatalf("Unmarshal(task.execute payload) error = %v", err)
	}
	if !strings.Contains(taskPayload.Prompt, "[Remote workspace @file context]") {
		t.Fatalf("taskPayload.Prompt = %q, want remote file context header", taskPayload.Prompt)
	}
	if !strings.Contains(taskPayload.Prompt, "--- BEGIN REMOTE FILE: src/app.ts ---") {
		t.Fatalf("taskPayload.Prompt = %q, want inlined remote file block", taskPayload.Prompt)
	}
	if !strings.Contains(taskPayload.Prompt, "export const ok = true;") {
		t.Fatalf("taskPayload.Prompt = %q, want inlined remote file content", taskPayload.Prompt)
	}
	if !strings.Contains(taskPayload.Prompt, "[User message]\nReview @src/app.ts for me") {
		t.Fatalf("taskPayload.Prompt = %q, want original user message section", taskPayload.Prompt)
	}
	if err := wsjson.Write(ctx, conn, device.AckEnvelope(taskExecute.ID)); err != nil {
		t.Fatalf("wsjson.Write(task.execute ack) error = %v", err)
	}

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskSession, "msg-session-inline", "dev-1", taskExecute.TaskID, device.TaskSessionPayload{
		SessionID: "remote-session-inline",
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgTaskDone, "msg-done-inline", "dev-1", taskExecute.TaskID, device.TaskDonePayload{
		Result: json.RawMessage(`{"stopReason":"end_turn"}`),
	}))

	select {
	case err := <-errCh:
		t.Fatalf("chat request error = %v", err)
	case body := <-bodyCh:
		if strings.Contains(body, `event: error`) {
			t.Fatalf("SSE body should not contain error: %s", body)
		}
		if !strings.Contains(body, `event: done`) {
			t.Fatalf("SSE body missing done event: %s", body)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE response")
	}
}

func connectTestDevice(t *testing.T, ctx context.Context, serverURL string, secret string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/devices/ws"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+secret)

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}
	return conn
}

func registerAndReadyDevice(t *testing.T, ctx context.Context, conn *websocket.Conn) {
	t.Helper()

	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgDeviceRegister, "msg-register", "dev-1", "", device.DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "Office Mac",
		Agents:   []device.DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	}))
	sendDeviceEventWithAck(t, ctx, conn, mustEnvelope(t, device.MsgSetupStatus, "msg-setup", "dev-1", "", device.SetupStatusPayload{
		Ready: true,
	}))
}

func sendDeviceEventWithAck(t *testing.T, ctx context.Context, conn *websocket.Conn, env device.Envelope) {
	t.Helper()

	if err := wsjson.Write(ctx, conn, env); err != nil {
		t.Fatalf("wsjson.Write(%s) error = %v", env.Type, err)
	}
	ack := readEnvelope(t, ctx, conn)
	if ack.Type != device.MsgAck || ack.ID != env.ID {
		t.Fatalf("ack = %+v, want ack for %s", ack, env.Type)
	}
}

func readEnvelope(t *testing.T, ctx context.Context, conn *websocket.Conn) device.Envelope {
	t.Helper()

	var env device.Envelope
	if err := wsjson.Read(ctx, conn, &env); err != nil {
		t.Fatalf("wsjson.Read() error = %v", err)
	}
	return env
}

func mustEnvelope(t *testing.T, typ device.MessageType, id, deviceID, taskID string, payload any) device.Envelope {
	t.Helper()

	env, err := device.NewEnvelope(typ, id, deviceID, taskID, payload)
	if err != nil {
		t.Fatalf("NewEnvelope(%s) error = %v", typ, err)
	}
	return env
}

type recordingWeComSink struct {
	events []wecom.ChatEvent
}

func (s *recordingWeComSink) Emit(event wecom.ChatEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *recordingWeComSink) hasUpdateText(text string) bool {
	for _, event := range s.events {
		if event.Name != "update" {
			continue
		}
		asMap, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		update, ok := asMap["update"].(map[string]any)
		if !ok {
			continue
		}
		if content, _ := update["content"].(map[string]any); strings.Contains(extractTextContent(content), text) {
			return true
		}
	}
	return false
}

type recordingWeChatSink struct {
	events []wechat.ChatEvent
}

func (s *recordingWeChatSink) Emit(event wechat.ChatEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *recordingWeChatSink) hasUpdateText(text string) bool {
	for _, event := range s.events {
		if event.Name != "update" {
			continue
		}
		asMap, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		update, ok := asMap["update"].(map[string]any)
		if !ok {
			continue
		}
		if content, _ := update["content"].(map[string]any); strings.Contains(extractTextContent(content), text) {
			return true
		}
	}
	return false
}

type memoryIMStore struct {
	session *storage.StoredSession
}

func (s *memoryIMStore) Load(id string) (*storage.StoredSession, error) {
	if s.session == nil || s.session.ID != id {
		return nil, os.ErrNotExist
	}
	return s.session, nil
}

func (s *memoryIMStore) Save(session *storage.StoredSession) error {
	s.session = session
	return nil
}

func TestHandleDeviceChatReturnsOfflineError(t *testing.T) {
	server := newTestAPIServer(t)
	registerTestDevice(t, server, "dev-offline", false)
	server.devices.MarkDisconnected("dev-offline", "offline")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(`{"message":"hello","workspaceId":"default","deviceId":"dev-offline"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if !strings.Contains(string(body), `Device is offline`) {
		t.Fatalf("body = %s, want offline error", body)
	}
}

func TestHandleDeviceChatUnavailableMentionDoesNotSwitchActiveAgent(t *testing.T) {
	server := newTestAPIServer(t)
	registerTestDevice(t, server, "dev-1", true)
	server.conversations.Create("conv-1", "claude", "default")
	server.agentSessions["conv-1"] = make(map[string]string)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewBufferString(`{"message":"@codex hello","conversationId":"conv-1","workspaceId":"default","deviceId":"dev-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if !strings.Contains(string(body), `Agent not available on device: codex`) {
		t.Fatalf("body = %s, want unavailable agent error", body)
	}

	conv := server.conversations.Get("conv-1")
	if conv == nil {
		t.Fatalf("conversation conv-1 not found")
	}
	if conv.ActiveAgent != "claude" {
		t.Fatalf("conv.ActiveAgent = %q, want claude", conv.ActiveAgent)
	}
}
