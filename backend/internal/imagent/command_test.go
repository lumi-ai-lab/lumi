package imagent

import (
	"os"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/storage"
)

type memoryStore struct {
	session *storage.StoredSession
}

func TestHandleDebugCommandShowsDefaultWithoutPersisting(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{session: &storage.StoredSession{
		ID:          "conv-1",
		Messages:    nil,
		ActiveAgent: "claude",
		WorkspaceID: workspace.ID,
	}}
	reply, handled, err := HandleCommand("/debug", "conv-1", workspace.ID, "claude", cfg, workspace, store)
	if err != nil {
		t.Fatalf("HandleCommand(/debug) error = %v", err)
	}
	if !handled || !strings.Contains(reply, "Debug 状态：thinking=off, tools=off") || !strings.Contains(reply, DebugHelp) {
		t.Fatalf("handled=%v reply=%q", handled, reply)
	}
	if len(store.session.Messages) != 0 {
		t.Fatalf("/debug modified messages")
	}
}

func TestHandleDebugCommandTogglesAndSetsThinkingTools(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{}

	tests := []struct {
		text     string
		thinking bool
		tools    bool
	}{
		{text: "/debug thinking", thinking: true, tools: false},
		{text: "/debug tools", thinking: true, tools: true},
		{text: "/debug thinking off", thinking: false, tools: true},
		{text: "/debug tools off", thinking: false, tools: false},
		{text: "/debug thinking on", thinking: true, tools: false},
		{text: "/debug tools on", thinking: true, tools: true},
	}

	for _, tt := range tests {
		reply, handled, err := HandleCommand(tt.text, "conv-1", workspace.ID, "claude", cfg, workspace, store)
		if err != nil {
			t.Fatalf("HandleCommand(%q) error = %v", tt.text, err)
		}
		if !handled || reply == DebugHelp {
			t.Fatalf("HandleCommand(%q) handled=%v reply=%q", tt.text, handled, reply)
		}
		if store.session.IMDebug.Thinking != tt.thinking || store.session.IMDebug.Tools != tt.tools {
			t.Fatalf("%q stored debug = %+v, want thinking=%v tools=%v", tt.text, store.session.IMDebug, tt.thinking, tt.tools)
		}
	}
}

func TestHandleDebugCommandAllRulesAndInvalidFormat(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{}

	for _, tt := range []struct {
		text     string
		thinking bool
		tools    bool
	}{
		{text: "/debug all", thinking: true, tools: true},
		{text: "/debug all", thinking: false, tools: false},
		{text: "/debug all on", thinking: true, tools: true},
		{text: "/debug all off", thinking: false, tools: false},
	} {
		if _, handled, err := HandleCommand(tt.text, "conv-1", workspace.ID, "claude", cfg, workspace, store); err != nil || !handled {
			t.Fatalf("HandleCommand(%q) handled=%v err=%v", tt.text, handled, err)
		}
		if store.session.IMDebug.Thinking != tt.thinking || store.session.IMDebug.Tools != tt.tools {
			t.Fatalf("%q stored debug = %+v, want thinking=%v tools=%v", tt.text, store.session.IMDebug, tt.thinking, tt.tools)
		}
	}

	before := store.session.IMDebug
	for _, text := range []string{"/debug nope", "/debug tools maybe", "/debug all on extra"} {
		reply, handled, err := HandleCommand(text, "conv-1", workspace.ID, "claude", cfg, workspace, store)
		if err != nil {
			t.Fatalf("HandleCommand(%q) error = %v", text, err)
		}
		if !handled || reply != DebugHelp {
			t.Fatalf("HandleCommand(%q) handled=%v reply=%q", text, handled, reply)
		}
		if store.session.IMDebug != before {
			t.Fatalf("invalid command %q changed debug from %+v to %+v", text, before, store.session.IMDebug)
		}
	}
}

func TestHandleDebugCommandIgnoresBoundaryMentions(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{}

	for _, text := range []string{
		"@机器人 /debug all on",
		"/debug all on @机器人",
		"@机器人\n/debug all on",
		"＠机器人 /debug all on",
		"@bot /debug all on @机器人",
	} {
		store.session = nil
		reply, handled, err := HandleCommand(text, "conv-1", workspace.ID, "claude", cfg, workspace, store)
		if err != nil {
			t.Fatalf("HandleCommand(%q) error = %v", text, err)
		}
		if !handled || reply == DebugHelp {
			t.Fatalf("HandleCommand(%q) handled=%v reply=%q", text, handled, reply)
		}
		if store.session == nil || !store.session.IMDebug.Thinking || !store.session.IMDebug.Tools {
			t.Fatalf("%q stored debug = %+v, want all on", text, store.session)
		}
	}
}

func (s *memoryStore) Load(id string) (*storage.StoredSession, error) {
	if s.session == nil || s.session.ID != id {
		return nil, os.ErrNotExist
	}
	return s.session, nil
}

func (s *memoryStore) Save(session *storage.StoredSession) error {
	s.session = session
	return nil
}

func TestHandleCommandListsAndSwitchesAgents(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{}

	reply, handled, err := HandleCommand(" /agent ", "conv-1", workspace.ID, "claude", cfg, workspace, store)
	if err != nil {
		t.Fatalf("HandleCommand(list) error = %v", err)
	}
	if !handled {
		t.Fatal("HandleCommand(list) handled = false, want true")
	}
	for _, want := range []string{"当前 Agent：claude", "* claude 当前", "* codex", "切换：/agent <id>"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("list reply missing %q in:\n%s", want, reply)
		}
	}
	if store.session != nil {
		t.Fatalf("list command persisted session: %+v", store.session)
	}

	reply, handled, err = HandleCommand("/agent codex", "conv-1", workspace.ID, "claude", cfg, workspace, store)
	if err != nil {
		t.Fatalf("HandleCommand(switch) error = %v", err)
	}
	if !handled || reply != "已切换当前 Agent 为 codex。" {
		t.Fatalf("switch handled=%v reply=%q", handled, reply)
	}
	if store.session == nil || store.session.ActiveAgent != "codex" {
		t.Fatalf("stored session = %+v, want active codex", store.session)
	}
}

func TestHandleCommandRejectsFormatAndUnavailableAgent(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{}

	reply, handled, err := HandleCommand("/agent codex hello", "conv-1", workspace.ID, "claude", cfg, workspace, store)
	if err != nil {
		t.Fatalf("HandleCommand(format) error = %v", err)
	}
	if !handled || reply != FormatHelp {
		t.Fatalf("format handled=%v reply=%q", handled, reply)
	}
	if store.session != nil {
		t.Fatalf("format command persisted session: %+v", store.session)
	}

	reply, handled, err = HandleCommand("/agent foo", "conv-1", workspace.ID, "claude", cfg, workspace, store)
	if err != nil {
		t.Fatalf("HandleCommand(missing) error = %v", err)
	}
	if !handled || !strings.Contains(reply, "未找到可用 Agent：foo") || !strings.Contains(reply, "可用 Agent：claude, codex") {
		t.Fatalf("missing handled=%v reply=%q", handled, reply)
	}
	if store.session != nil {
		t.Fatalf("missing command persisted session: %+v", store.session)
	}
}

func TestHandleCommandIgnoresUnsupportedCommandsAndMentions(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("default")
	store := &memoryStore{}

	for _, text := range []string{"/agents", "/agentcodex", "@codex hello", "hello /agent codex", "hello\n/debug all on", "hello /debug all on @bot"} {
		reply, handled, err := HandleCommand(text, "conv-1", workspace.ID, "claude", cfg, workspace, store)
		if err != nil {
			t.Fatalf("HandleCommand(%q) error = %v", text, err)
		}
		if handled || reply != "" {
			t.Fatalf("HandleCommand(%q) handled=%v reply=%q, want ordinary text", text, handled, reply)
		}
	}
	if store.session != nil {
		t.Fatalf("ordinary text persisted session: %+v", store.session)
	}
}

func TestResolveActiveAgentUsesWorkspaceWhitelistAndFallback(t *testing.T) {
	cfg := testConfig()
	workspace := cfg.FindWorkspace("limited")
	store := &memoryStore{session: storage.CreateSession("conv-1", "codex", workspace.ID)}

	got, err := ResolveActiveAgent(store, "conv-1", workspace.ID, "claude", cfg, workspace)
	if err != nil {
		t.Fatalf("ResolveActiveAgent(fallback) error = %v", err)
	}
	if got != "claude" {
		t.Fatalf("ResolveActiveAgent(fallback) = %q, want claude", got)
	}

	if _, err := ResolveActiveAgent(store, "conv-1", workspace.ID, "codex", cfg, workspace); err == nil {
		t.Fatal("ResolveActiveAgent(unavailable default) error = nil, want unavailable default error")
	}

	store.session.ActiveAgent = "claude"
	got, err = ResolveActiveAgent(store, "conv-1", workspace.ID, "codex", cfg, workspace)
	if err != nil {
		t.Fatalf("ResolveActiveAgent() error = %v", err)
	}
	if got != "claude" {
		t.Fatalf("ResolveActiveAgent() = %q, want claude", got)
	}

	emptyWorkspace := cfg.FindWorkspace("default")
	store.session.ActiveAgent = "codex"
	got, err = ResolveActiveAgent(store, "conv-1", emptyWorkspace.ID, "claude", cfg, emptyWorkspace)
	if err != nil {
		t.Fatalf("ResolveActiveAgent(empty whitelist) error = %v", err)
	}
	if got != "codex" {
		t.Fatalf("ResolveActiveAgent(empty whitelist) = %q, want codex", got)
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Agents: []config.AgentConfig{
			{ID: "claude", Name: "Claude", Command: "echo"},
			{ID: "codex", Name: "Codex", Command: "echo"},
		},
		DefaultAgent: "claude",
		Workspaces: []config.WorkspaceConfig{
			{ID: "default", Name: "Default", Path: "."},
			{ID: "limited", Name: "Limited", Path: ".", Agents: []string{"claude"}},
		},
		DefaultWorkspace: "default",
	}
}
