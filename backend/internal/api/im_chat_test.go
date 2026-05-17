package api

import (
	"testing"

	"github.com/pengmide/lumi/internal/conversation"
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
