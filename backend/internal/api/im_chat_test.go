package api

import (
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
)

func TestShouldInjectIMAgentContext(t *testing.T) {
	tests := []struct {
		name     string
		messages []conversation.Message
		agentID  string
		want     bool
	}{
		{name: "empty", agentID: "codex", want: false},
		{
			name:     "only user history",
			messages: []conversation.Message{{Role: "user", Content: "hello"}},
			agentID:  "codex",
			want:     true,
		},
		{
			name:     "same assistant agent",
			messages: []conversation.Message{{Role: "assistant", Agent: "codex", Content: "ok"}},
			agentID:  "codex",
			want:     false,
		},
		{
			name:     "different assistant agent",
			messages: []conversation.Message{{Role: "assistant", Agent: "claude", Content: "ok"}},
			agentID:  "codex",
			want:     true,
		},
		{
			name: "uses latest assistant agent",
			messages: []conversation.Message{
				{Role: "assistant", Agent: "claude", Content: "old"},
				{Role: "user", Content: "middle"},
				{Role: "assistant", Agent: "codex", Content: "new"},
			},
			agentID: "codex",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldInjectIMAgentContext(tt.messages, tt.agentID); got != tt.want {
				t.Fatalf("shouldInjectIMAgentContext() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildIMSessionInstructionProfileSeparatesStableContextAndSecrets(t *testing.T) {
	profile := buildIMSessionInstructionProfile("source instruction", lumicron.ToolContext{
		APIBase:        "https://private.example.invalid/api",
		Channel:        lumicron.ChannelWeCom,
		ConversationID: "conv-1",
		AgentID:        "pi",
		WorkspaceID:    "workspace-1",
		WorkspacePath:  "/private/workspace/path",
		Target: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ReqID: "private-request", ChatID: "private-chat", UserID: "private-user",
		}},
	})

	for _, want := range []string{"source instruction", "You are running inside Lumi.", "$LUMI_CLI\" im run"} {
		if !strings.Contains(profile.BaseInstructions, want) {
			t.Fatalf("base instructions missing %q:\n%s", want, profile.BaseInstructions)
		}
	}
	for _, want := range []string{"Channel: wecom", "Conversation ID: conv-1", "Workspace ID: workspace-1", "Agent ID: pi"} {
		if !strings.Contains(profile.SessionContext, want) {
			t.Fatalf("Session context missing %q:\n%s", want, profile.SessionContext)
		}
	}
	for _, secret := range []string{"private.example", "/private/workspace/path", "private-request", "private-chat", "private-user"} {
		if strings.Contains(profile.Text(), secret) {
			t.Fatalf("Session instruction profile contains %q", secret)
		}
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("profile validation failed: %v", err)
	}
}
