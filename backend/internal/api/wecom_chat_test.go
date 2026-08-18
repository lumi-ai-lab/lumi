package api

import (
	"reflect"
	"testing"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/wecom"
)

func TestWithWeComSessionEnvPreservesExistingMeta(t *testing.T) {
	requesterContext := map[string]string{"requestId": "req-1"}
	meta := withWeComSessionEnv(map[string]any{
		"_auth": "qdm1enc.auth",
		"lumi":  map[string]any{"requesterContext": requesterContext},
	}, wecom.ChatRunInput{
		ConversationID: "wecom:chat-a:user-a",
		AgentID:        "pi",
		WorkspaceID:    "workspace-a",
		WorkspacePath:  "/workspace/a",
		CronTarget: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ChatID:   "chat-a",
			ChatType: "single",
			ReqID:    "turn-only",
		}},
	})

	if meta["_auth"] != "qdm1enc.auth" {
		t.Fatalf("_auth = %v", meta["_auth"])
	}
	lumi := meta["lumi"].(map[string]any)
	if !reflect.DeepEqual(lumi["requesterContext"], requesterContext) {
		t.Fatalf("requesterContext = %#v", lumi["requesterContext"])
	}
	want := map[string]string{
		"LUMI_CHANNEL":         "wecom",
		"LUMI_CONVERSATION_ID": "wecom:chat-a:user-a",
		"LUMI_AGENT_ID":        "pi",
		"LUMI_WORKSPACE_ID":    "workspace-a",
		"LUMI_WORKSPACE_PATH":  "/workspace/a",
		"LUMI_WECOM_CHAT_ID":   "chat-a",
	}
	if got := lumi["sessionEnv"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("sessionEnv = %#v, want %#v", got, want)
	}
}
