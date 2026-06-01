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
