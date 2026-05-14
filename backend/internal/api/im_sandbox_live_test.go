package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/storage"
	"github.com/pengmide/lumi/internal/wecom"
)

func TestLiveWeComSandboxRunner(t *testing.T) {
	if os.Getenv("LUMI_LIVE_SANDBOX_TEST") != "1" {
		t.Skip("set LUMI_LIVE_SANDBOX_TEST=1 to run live Docker sandbox integration")
	}

	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	scriptPath := filepath.Join(workspace, "fake-acp.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"live-session"}}\n' "$id"
      ;;
    *'"method":"session/prompt"'*)
      printf '{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"live sandbox reply"}}}}\n'
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      ;;
  esac
done
`), 0o755); err != nil {
		t.Fatalf("write fake ACP script: %v", err)
	}

	cfg := &config.Config{
		PublicServerURL: "http://127.0.0.1:39321",
		Agents: []config.AgentConfig{{
			ID:          "fake",
			Name:        "Fake ACP",
			Command:     "/workspace/fake-acp.sh",
			SessionMode: agentmode.ClaudeModeBypassPermissions,
		}},
		DefaultAgent: "fake",
		Workspaces: []config.WorkspaceConfig{{
			ID:             "live-sandbox",
			Name:           "Live Sandbox",
			Path:           workspace,
			Kind:           "sandbox",
			Image:          sandbox.DefaultImage,
			IdleTimeoutSec: 300,
			Agents:         []string{"fake"},
		}},
		DefaultWorkspace: "live-sandbox",
	}

	server := NewServer(cfg, nil)
	t.Cleanup(func() {
		_ = server.sandbox.Terminate(context.Background(), "live-sandbox")
		_ = server.Shutdown()
	})

	httpServer := newLiveHTTPServer(t, "127.0.0.1:39321", server)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	sink := &recordingWeComSink{}
	store := &memoryIMStore{}
	err := server.RunWeComChat(ctx, wecom.ChatRunInput{
		Message:           "hello",
		ConversationID:    "wecom_live",
		WorkspaceID:       "live-sandbox",
		AgentID:           "fake",
		ConversationStore: store,
	}, sink)
	if err != nil {
		t.Fatalf("RunWeComChat() error = %v", err)
	}
	if !sink.hasUpdateText("live sandbox reply") {
		t.Fatalf("sink events missing live sandbox reply: %+v", sink.events)
	}
	if store.session == nil || !storedSessionHasText(store.session, "live sandbox reply") {
		t.Fatalf("hidden IM session was not persisted with reply: %+v", store.session)
	}
}

func newLiveHTTPServer(t *testing.T, addr string, server *Server) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	httpServer := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: server.Handler()},
	}
	httpServer.Start()
	return httpServer
}

func storedSessionHasText(session *storage.StoredSession, text string) bool {
	for _, message := range session.Messages {
		if message.Role == "assistant" && strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}
