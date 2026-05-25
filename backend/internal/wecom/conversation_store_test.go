package wecom

import (
	"testing"

	"github.com/pengmide/lumi/internal/storage"
)

func TestConversationStorePreservesAgentSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LUMI_WECOM_INSTANCE_ID", "wecom-test")

	store := NewConversationStore()
	session := &storage.StoredSession{
		ID:          "wecom_hidden",
		Title:       "Hidden",
		ActiveAgent: "claude",
		WorkspaceID: "default",
		AgentSessions: map[string]string{
			"claude": "local-session-1",
		},
		CreatedAt: 100,
		UpdatedAt: 200,
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.AgentSessions["claude"]; got != "local-session-1" {
		t.Fatalf("agent session = %q, want local-session-1", got)
	}
}
