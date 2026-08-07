package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/jsonrpc"
)

type nopWriteCloser struct {
	*bytes.Buffer
}

func (n *nopWriteCloser) Close() error {
	return nil
}

func TestHandlePermissionRequestSupportsImmediateConfirm(t *testing.T) {
	proc := NewProcess(&config.AgentConfig{
		ID:      "claude",
		Name:    "Claude",
		Command: "echo",
	})
	var out bytes.Buffer
	proc.stdin = &nopWriteCloser{Buffer: &out}

	done := make(chan struct{})
	cleanup := proc.OnPermission("session-1", func(req *PermissionRequest) {
		proc.ConfirmPermission(req.ToolCall.ToolCallID, "allow-once")
		close(done)
	})
	defer cleanup()

	msg := jsonrpc.NewRequest(1, "session/request_permission", map[string]any{
		"sessionId": "session-1",
		"options": []map[string]any{
			{
				"optionId": "allow-once",
				"name":     "Allow once",
				"kind":     "allow_once",
			},
		},
		"toolCall": map[string]any{
			"toolCallId": "tool-1",
			"title":      "Run command",
		},
	})
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var message jsonrpc.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	go proc.handlePermissionRequest(&message)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("permission handler did not receive request")
	}

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(out.String(), `"optionId":"allow-once"`) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("permission response was not written: %s", out.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	proc.mu.Lock()
	_, ok := proc.permissions["tool-1"]
	proc.mu.Unlock()
	if ok {
		t.Fatal("permission request should be removed after confirm")
	}
}

func newTestProcess() *Process {
	return NewProcess(&config.AgentConfig{
		ID:      "test",
		Name:    "test",
		Command: "echo",
	})
}

func TestNotificationRoutesBySession(t *testing.T) {
	t.Parallel()

	proc := newTestProcess()
	var calledA, calledB bool

	cleanupA := proc.OnNotification("session-a", func(msg *jsonrpc.Message) { calledA = true })
	defer cleanupA()
	cleanupB := proc.OnNotification("session-b", func(msg *jsonrpc.Message) { calledB = true })
	defer cleanupB()

	// Notification for session-a only
	proc.handleMessage(&jsonrpc.Message{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(`{"sessionId":"session-a","update":{"sessionUpdate":"agent_message_chunk"}}`),
	})

	if calledA != true {
		t.Error("handler for session-a was not called")
	}
	if calledB != false {
		t.Error("handler for session-b was called for session-a notification")
	}
}

func TestNotificationCatchAllReceivesAll(t *testing.T) {
	t.Parallel()

	proc := newTestProcess()
	var catchAll, scoped int

	cleanupAll := proc.OnNotification("", func(msg *jsonrpc.Message) { catchAll++ })
	defer cleanupAll()
	cleanupScoped := proc.OnNotification("session-x", func(msg *jsonrpc.Message) { scoped++ })
	defer cleanupScoped()

	// Notification for session-x — both catch-all and scoped should fire
	proc.handleMessage(&jsonrpc.Message{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(`{"sessionId":"session-x","update":{"sessionUpdate":"agent_message_chunk"}}`),
	})
	// Notification for session-y — only catch-all should fire
	proc.handleMessage(&jsonrpc.Message{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(`{"sessionId":"session-y","update":{"sessionUpdate":"agent_message_chunk"}}`),
	})

	if catchAll != 2 {
		t.Fatalf("catch-all handler called %d times, want 2", catchAll)
	}
	if scoped != 1 {
		t.Fatalf("scoped handler called %d times, want 1", scoped)
	}
}

func TestPermissionRoutesBySession(t *testing.T) {
	t.Parallel()

	proc := newTestProcess()
	var out bytes.Buffer
	proc.stdin = &nopWriteCloser{Buffer: &out}

	var calledA, calledB bool

	cleanupA := proc.OnPermission("session-a", func(req *PermissionRequest) {
		calledA = true
		proc.ConfirmPermission(req.ToolCall.ToolCallID, "allow-once")
	})
	defer cleanupA()
	cleanupB := proc.OnPermission("session-b", func(req *PermissionRequest) {
		calledB = true
		proc.ConfirmPermission(req.ToolCall.ToolCallID, "allow-once")
	})
	defer cleanupB()

	msg := jsonrpc.NewRequest(1, "session/request_permission", map[string]any{
		"sessionId": "session-a",
		"options": []map[string]any{
			{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
		},
		"toolCall": map[string]any{"toolCallId": "perm-routed"},
	})
	raw, _ := json.Marshal(msg)
	var message jsonrpc.Message
	json.Unmarshal(raw, &message)

	done := make(chan struct{})
	go func() {
		proc.handlePermissionRequest(&message)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("permission request timed out")
	}

	if calledA != true {
		t.Error("handler for session-a was not called")
	}
	if calledB != false {
		t.Error("handler for session-b was called for session-a permission request")
	}
}

func TestNotificationWithoutSessionIDBroadcasts(t *testing.T) {
	t.Parallel()

	proc := newTestProcess()
	var calledA, calledB bool

	cleanupA := proc.OnNotification("session-a", func(msg *jsonrpc.Message) { calledA = true })
	defer cleanupA()
	cleanupB := proc.OnNotification("session-b", func(msg *jsonrpc.Message) { calledB = true })
	defer cleanupB()

	// Notification without sessionId field — backward compatible broadcast
	proc.handleMessage(&jsonrpc.Message{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk"}}`),
	})

	if calledA != true {
		t.Error("handler for session-a was not called (backward compat)")
	}
	if calledB != true {
		t.Error("handler for session-b was not called (backward compat)")
	}
}
