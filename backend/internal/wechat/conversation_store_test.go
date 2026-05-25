package wechat

import (
	"testing"

	"github.com/pengmide/lumi/internal/conversation"
	"github.com/pengmide/lumi/internal/storage"
)

func TestConversationStoreIsHiddenAndPreservesSessionFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store := NewConversationStore()
	session := &storage.StoredSession{
		ID:          "wx_hidden",
		Title:       "Hidden",
		ActiveAgent: "claude",
		WorkspaceID: "default",
		AgentSessions: map[string]string{
			"claude": "local-session-1",
		},
		CreatedAt: 100,
		UpdatedAt: 200,
		Messages: []conversation.Message{
			{
				Role:    "user",
				Content: "hello",
				Files: []conversation.MessageFile{
					{Name: "spec.pdf", Path: ".lumi-uploads/wechat/wx_hidden/spec.pdf", Size: 12},
				},
				Timestamp: 111,
			},
			{
				Role: "assistant",
				ToolCall: &conversation.ToolCallInfo{
					ToolCallID: "tool-1",
					ToolName:   "bash",
					Status:     "completed",
					Output:     "ok",
				},
				Timestamp: 222,
			},
		},
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load("wx_hidden")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.CreatedAt != 100 || loaded.Messages[0].Timestamp != 111 {
		t.Fatalf("stored timestamps were not preserved: %+v", loaded)
	}
	if loaded.Messages[1].ToolCall == nil || loaded.Messages[1].ToolCall.ToolCallID != "tool-1" {
		t.Fatalf("tool call not preserved: %+v", loaded.Messages[1].ToolCall)
	}
	if got := loaded.AgentSessions["claude"]; got != "local-session-1" {
		t.Fatalf("agent session not preserved: %q", got)
	}

	regularSessions := storage.NewSessionStore("")
	if got := regularSessions.List(); len(got) != 0 {
		t.Fatalf("hidden wechat session leaked into normal SessionStore: %+v", got)
	}
}
