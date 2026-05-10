package wecom

import (
	"context"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/storage"
)

type ChatFileInfo struct {
	Name string
	Path string
	Size int64
}

type HiddenConversationStore interface {
	Load(id string) (*storage.StoredSession, error)
	Save(session *storage.StoredSession) error
}

type ChatRunInput struct {
	Message             string
	ConversationID      string
	WorkspaceID         string
	WorkspacePath       string
	AgentID             string
	Files               []ChatFileInfo
	PromptPrefix        string
	SessionModeOverride string
	NewSession          bool
	ConversationStore   HiddenConversationStore
	CronTarget          lumicron.Target
}

type ChatEvent struct {
	Name string
	Data any
}

type ChatEventSink interface {
	Emit(ChatEvent) error
}

type ChatRunner interface {
	RunWeComChat(ctx context.Context, input ChatRunInput, sink ChatEventSink) error
}
