package cron

import (
	"strings"
	"testing"
)

func TestAgentToolInstructionsForContextDoesNotAppendUserPrompt(t *testing.T) {
	instructions := AgentToolInstructionsForContext(ToolContext{
		Channel:        ChannelWeCom,
		ConversationID: "conv-1",
		AgentID:        "claude",
	})

	if !strings.Contains(instructions, "You are running inside Lumi.") {
		t.Fatalf("instructions missing Lumi runtime text: %q", instructions)
	}
	if strings.Contains(instructions, "User:") {
		t.Fatalf("instructions should not contain user prompt marker: %q", instructions)
	}
}

func TestWithAgentToolInstructionsForContextKeepsCompatibility(t *testing.T) {
	prompt := WithAgentToolInstructionsForContext("hello", ToolContext{Channel: ChannelWeCom})

	if !strings.Contains(prompt, "You are running inside Lumi.") {
		t.Fatalf("prompt missing Lumi runtime text: %q", prompt)
	}
	if !strings.Contains(prompt, "User: hello") {
		t.Fatalf("prompt missing compatible user wrapper: %q", prompt)
	}
}

func TestAgentToolInstructionsDoNotExposeRuntimeOrIMReplySecrets(t *testing.T) {
	instructions := AgentToolInstructionsForContext(ToolContext{
		APIBase: "https://private.example.invalid/api", Channel: ChannelWeCom,
		ConversationID: "conversation-1", AgentID: "agent-1",
		WorkspaceID: "workspace-1", WorkspacePath: "/private/workspace/path",
		Target: Target{WeCom: &WeComTarget{ReqID: "private-request", ChatID: "private-chat", UserID: "private-user"}},
	})
	for _, secret := range []string{"private.example", "/private/workspace/path", "private-request", "private-chat", "private-user"} {
		if strings.Contains(instructions, secret) {
			t.Fatalf("instructions contain %q: %s", secret, instructions)
		}
	}
	for _, want := range []string{`--channel "wecom"`, `--conversation-id "conversation-1"`, `--agent-id "agent-1"`, `--workspace-id "workspace-1"`} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing stable routing flag %q: %s", want, instructions)
		}
	}
	if !strings.Contains(instructions, "server resolves the current IM reply target") {
		t.Fatalf("instructions missing server-side target guidance: %s", instructions)
	}
}
