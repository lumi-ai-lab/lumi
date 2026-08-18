package api

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/acppatch"
	"github.com/pengmide/lumi/internal/config"
	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/wecom"
)

func TestLiveWeComLocalPiSessionEnv(t *testing.T) {
	if os.Getenv("LUMI_LIVE_LOCAL_PI_TEST") != "1" {
		t.Skip("set LUMI_LIVE_LOCAL_PI_TEST=1 to run the Local pi-acp process integration")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LUMI_HOME", filepath.Join(home, ".lumi"))
	t.Setenv("LUMI_NPM_RUNTIME_PREFIX", "")

	fakePi := filepath.Join(t.TempDir(), "fake-pi.mjs")
	if err := os.WriteFile(fakePi, []byte(`#!/usr/bin/env node
import readline from "node:readline";
import path from "node:path";

const keys = [
  "LUMI_CHANNEL",
  "LUMI_CONVERSATION_ID",
  "LUMI_AGENT_ID",
  "LUMI_WORKSPACE_ID",
  "LUMI_WORKSPACE_PATH",
  "LUMI_WECOM_CHAT_ID",
];
const env = Object.fromEntries(keys.map((key) => [key, process.env[key] ?? ""]));
const suffix = env.LUMI_CONVERSATION_ID.replace(/[^a-zA-Z0-9_-]/g, "_");
const sessionId = "fake-" + suffix;
const sessionFile = path.join(process.cwd(), "." + sessionId + ".jsonl");
const restored = process.argv.includes("--session");
const send = (message) => process.stdout.write(JSON.stringify(message) + "\n");
const respond = (id, data = {}) => send({ type: "response", id, success: true, data });

const input = readline.createInterface({ input: process.stdin });
input.on("line", (line) => {
  const request = JSON.parse(line);
  switch (request.type) {
    case "get_state":
      respond(request.id, {
        sessionId,
        sessionFile,
        thinkingLevel: "medium",
        model: { provider: "fake", id: "model" },
      });
      break;
    case "get_available_models":
      respond(request.id, { models: [{ provider: "fake", id: "model", name: "Model" }] });
      break;
    case "get_commands":
      respond(request.id, { commands: [] });
      break;
    case "prompt":
      send({
        type: "message_update",
        assistantMessageEvent: {
          type: "text_delta",
          delta: JSON.stringify({ env, restored, acpPid: process.ppid }),
        },
      });
      respond(request.id);
      send({ type: "agent_settled" });
      break;
    default:
      respond(request.id);
  }
});
`), 0o755); err != nil {
		t.Fatalf("write fake PI: %v", err)
	}

	workspaceA := makeQuietPiWorkspace(t)
	workspaceB := makeQuietPiWorkspace(t)
	cfg := &config.Config{
		PublicServerURL: "http://127.0.0.1:3000",
		Agents: []config.AgentConfig{{
			ID:      "pi",
			Name:    "PI",
			Command: "npx",
			Args:    []string{"-y", acppatch.PiACPPackageSpec},
			Env:     map[string]string{"PI_ACP_PI_COMMAND": fakePi},
		}},
		DefaultAgent: "pi",
		Workspaces: []config.WorkspaceConfig{
			{ID: "workspace-a", Name: "A", Path: workspaceA, Kind: "local", Agents: []string{"pi"}},
			{ID: "workspace-b", Name: "B", Path: workspaceB, Kind: "local", Agents: []string{"pi"}},
		},
		DefaultWorkspace: "workspace-a",
	}
	server := NewServer(cfg, nil)
	t.Cleanup(func() { _ = server.Shutdown() })

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	storeA := &memoryIMStore{}
	storeB := &memoryIMStore{}

	gotA := runLocalPiEnvTurn(t, ctx, server, storeA, "conversation-a", "workspace-a", "chat-a")
	assertLocalPiEnv(t, gotA, map[string]string{
		"LUMI_CHANNEL":         "wecom",
		"LUMI_CONVERSATION_ID": "conversation-a",
		"LUMI_AGENT_ID":        "pi",
		"LUMI_WORKSPACE_ID":    "workspace-a",
		"LUMI_WORKSPACE_PATH":  workspaceA,
		"LUMI_WECOM_CHAT_ID":   "chat-a",
	}, false)

	gotB := runLocalPiEnvTurn(t, ctx, server, storeB, "conversation-b", "workspace-b", "chat-b")
	assertLocalPiEnv(t, gotB, map[string]string{
		"LUMI_CHANNEL":         "wecom",
		"LUMI_CONVERSATION_ID": "conversation-b",
		"LUMI_AGENT_ID":        "pi",
		"LUMI_WORKSPACE_ID":    "workspace-b",
		"LUMI_WORKSPACE_PATH":  workspaceB,
		"LUMI_WECOM_CHAT_ID":   "chat-b",
	}, false)
	if gotA.ACPPid != gotB.ACPPid {
		t.Fatalf("ACP PIDs = %d and %d, want one shared Lumi ACP process", gotA.ACPPid, gotB.ACPPid)
	}

	if err := server.wecomChat.agents.Stop("pi"); err != nil {
		t.Fatalf("stop ACP process: %v", err)
	}
	restoredA := runLocalPiEnvTurn(t, ctx, server, storeA, "conversation-a", "workspace-a", "chat-a")
	assertLocalPiEnv(t, restoredA, gotA.Env, true)
}

type localPiEnvResult struct {
	Env      map[string]string `json:"env"`
	Restored bool              `json:"restored"`
	ACPPid   int               `json:"acpPid"`
}

func runLocalPiEnvTurn(t *testing.T, ctx context.Context, server *Server, store *memoryIMStore, conversationID, workspaceID, chatID string) localPiEnvResult {
	t.Helper()
	sink := &recordingWeComSink{}
	if err := server.RunWeComChat(ctx, wecom.ChatRunInput{
		Message:           "report env",
		ConversationID:    conversationID,
		WorkspaceID:       workspaceID,
		AgentID:           "pi",
		ConversationStore: store,
		CronTarget: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ChatID: chatID,
		}},
	}, sink); err != nil {
		t.Fatalf("RunWeComChat(%s): %v", conversationID, err)
	}
	for _, event := range sink.events {
		if event.Name != "update" {
			continue
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			continue
		}
		update, ok := data["update"].(map[string]any)
		if !ok {
			continue
		}
		content, _ := update["content"].(map[string]any)
		text := extractTextContent(content)
		if !strings.Contains(text, `"LUMI_CHANNEL"`) {
			continue
		}
		var got localPiEnvResult
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("decode PI env response %q: %v", text, err)
		}
		return got
	}
	t.Fatalf("PI env response missing from events: %+v", sink.events)
	return localPiEnvResult{}
}

func assertLocalPiEnv(t *testing.T, got localPiEnvResult, want map[string]string, restored bool) {
	t.Helper()
	if got.Restored != restored {
		t.Fatalf("restored = %v, want %v", got.Restored, restored)
	}
	for key, value := range want {
		if got.Env[key] != value {
			t.Fatalf("%s = %q, want %q; all env = %#v", key, got.Env[key], value, got.Env)
		}
	}
}

func makeQuietPiWorkspace(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	settingsDir := filepath.Join(workspace, ".pi")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create PI settings dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o644); err != nil {
		t.Fatalf("write PI settings: %v", err)
	}
	return workspace
}
