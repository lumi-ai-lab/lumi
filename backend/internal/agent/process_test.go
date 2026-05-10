package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/jsonrpc"
)

func TestStopUnblocksPermissionRequest(t *testing.T) {
	t.Parallel()

	proc := NewProcess(&config.AgentConfig{
		ID:      "claude",
		Name:    "Claude Code",
		Command: "echo",
	})

	params, err := json.Marshal(map[string]any{
		"sessionId": "session-1",
		"options": []map[string]any{
			{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
		},
		"toolCall": map[string]any{
			"toolCallId": "tool-1",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	msg := &jsonrpc.Message{
		JSONRPC: jsonrpc.Version,
		ID:      json.RawMessage(`1`),
		Method:  "session/request_permission",
		Params:  params,
	}

	done := make(chan struct{})
	go func() {
		proc.handlePermissionRequest(msg)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		proc.mu.Lock()
		_, ok := proc.permissions["tool-1"]
		proc.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := proc.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("permission request remained blocked after Stop")
	}
}

func TestStopInterruptsBeforeClosingStdin(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell signal handling")
	}

	marker := filepath.Join(t.TempDir(), "marker")
	proc := NewProcess(&config.AgentConfig{
		ID:      "test",
		Name:    "test",
		Command: "sh",
		Args: []string{
			"-c",
			`trap 'printf interrupted > "$MARKER"; exit 0' INT TERM; if read line; then printf read > "$MARKER"; else printf eof > "$MARKER"; exit 0; fi`,
		},
		Env: map[string]string{
			"MARKER": marker,
		},
	})

	if err := proc.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := proc.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile(marker) error = %v", err)
	}
	if string(got) != "interrupted" {
		t.Fatalf("stop marker = %q, want interrupted", got)
	}
}
