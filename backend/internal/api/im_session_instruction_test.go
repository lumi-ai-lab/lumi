package api

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/wecom"
)

func TestLocalWeComSessionInstructionProfilePersistsLoadsAndKeepsPromptClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP adapter uses a POSIX shell")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	requestLog := filepath.Join(root, "requests.ndjson")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$REQUEST_LOG"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"_meta":{"lumi":{"sessionInstructions":{"transportVersion":1,"systemPromptAppend":true,"rehydrateOnRestore":true,"turnContext":true}}}}}\n' "$id"
      ;;
    *'"method":"session/new"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"logical-session"}}\n' "$id"
      ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn"}}\n' "$id"
      ;;
  esac
done`
	cfg := &config.Config{
		Agents: []config.AgentConfig{{
			ID: "fake", Name: "Fake ACP", Command: "sh", Args: []string{"-c", script},
			Env: map[string]string{"REQUEST_LOG": requestLog},
		}},
		DefaultAgent: "fake",
	}
	store := &memoryIMStore{}

	first := newWeComChatRuntime(cfg, nil, nil)
	if err := first.RunWeComChat(context.Background(), wecom.ChatRunInput{
		Message: "first question", PromptPrefix: "stable channel protocol",
		ConversationID: "conversation-1", WorkspaceID: "workspace-1", WorkspacePath: workspace,
		AgentID: "fake", ConversationStore: store,
	}, &recordingWeComSink{}); err != nil {
		t.Fatalf("first RunWeComChat() error = %v", err)
	}
	if err := first.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if store.session == nil || store.session.AgentSessionProfileDigests["fake"] == "" {
		t.Fatalf("stored Session profile digest = %#v", store.session)
	}
	firstDigest := store.session.AgentSessionProfileDigests["fake"]

	second := newWeComChatRuntime(cfg, nil, nil)
	t.Cleanup(func() { _ = second.Shutdown() })
	if err := second.RunWeComChat(context.Background(), wecom.ChatRunInput{
		Message: "second question", PromptPrefix: "stable channel protocol",
		ConversationID: "conversation-1", WorkspaceID: "workspace-1", WorkspacePath: workspace,
		AgentID: "fake", ConversationStore: store,
	}, &recordingWeComSink{}); err != nil {
		t.Fatalf("restored RunWeComChat() error = %v", err)
	}
	if got := store.session.AgentSessionProfileDigests["fake"]; got != firstDigest {
		t.Fatalf("restored digest = %q, want %q", got, firstDigest)
	}

	if err := second.RunWeComChat(context.Background(), wecom.ChatRunInput{
		Message: "third question", PromptPrefix: "updated channel protocol",
		ConversationID: "conversation-1", WorkspaceID: "workspace-1", WorkspacePath: workspace,
		AgentID: "fake", ConversationStore: store,
	}, &recordingWeComSink{}); err != nil {
		t.Fatalf("digest-changed RunWeComChat() error = %v", err)
	}
	if got := store.session.AgentSessionProfileDigests["fake"]; got == "" || got == firstDigest {
		t.Fatalf("updated digest = %q, first = %q", got, firstDigest)
	}

	requests := readACPRequests(t, requestLog)
	assertACPMethodCountAtLeast(t, requests, "session/new", 2)
	assertACPMethodCountAtLeast(t, requests, "session/load", 1)
	assertCleanProfilePrompt(t, requests, "second question", "stable channel protocol")
	assertCleanProfilePrompt(t, requests, "third question", "updated channel protocol")
}

func readACPRequests(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	requests := make([]map[string]any, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var request map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			t.Fatalf("decode ACP request: %v", err)
		}
		requests = append(requests, request)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return requests
}

func assertACPMethodCountAtLeast(t *testing.T, requests []map[string]any, method string, minimum int) {
	t.Helper()
	count := 0
	for _, request := range requests {
		if request["method"] == method {
			count++
		}
	}
	if count < minimum {
		t.Fatalf("%s requests = %d, want at least %d", method, count, minimum)
	}
}

func assertCleanProfilePrompt(t *testing.T, requests []map[string]any, question, protocol string) {
	t.Helper()
	for _, request := range requests {
		if request["method"] != "session/prompt" {
			continue
		}
		params, _ := request["params"].(map[string]any)
		prompt, _ := params["prompt"].([]any)
		if len(prompt) != 1 {
			continue
		}
		block, _ := prompt[0].(map[string]any)
		if block["text"] != question {
			continue
		}
		encodedMeta, _ := json.Marshal(params["_meta"])
		meta := string(encodedMeta)
		for _, want := range []string{"sessionInstructions", protocol, "Current Lumi Session context:"} {
			if !strings.Contains(meta, want) {
				t.Fatalf("prompt metadata for %q missing %q: %s", question, want, meta)
			}
		}
		return
	}
	t.Fatalf("clean session/prompt request for %q was not found", question)
}
