package cron

import (
	"strings"
	"testing"
)

func TestDetectCommandsCreateAndStrip(t *testing.T) {
	body := `Please create it.

[CRON_CREATE]
name: Daily report
schedule: interval:60
schedule_description: Every hour
message: Summarize the repo
[/CRON_CREATE]

Done.`
	commands := DetectCommands(body)
	if len(commands) != 1 {
		t.Fatalf("len(commands) = %d, want 1", len(commands))
	}
	cmd := commands[0]
	if cmd.Kind != "create" || cmd.Name != "Daily report" || cmd.Prompt != "Summarize the repo" {
		t.Fatalf("command = %#v", cmd)
	}
	if cmd.Schedule.Type != "interval" || cmd.Schedule.EverySeconds != 3600 {
		t.Fatalf("schedule = %#v", cmd.Schedule)
	}
	if got := StripCommands(body); got != "Please create it.\n\nDone." {
		t.Fatalf("StripCommands() = %q", got)
	}
}

func TestDetectCommandsIgnoresCodeBlocks(t *testing.T) {
	commands := DetectCommands("```text\n[CRON_LIST]\n```")
	if len(commands) != 0 {
		t.Fatalf("commands = %#v, want none", commands)
	}
}

func TestConversationInstructionUsesLumiScheduleGrammar(t *testing.T) {
	prompt := WithConversationInstruction("remind me tomorrow")
	if !strings.Contains(prompt, "schedule: interval:<minutes> or once:<YYYY-MM-DD HH:mm>") {
		t.Fatalf("prompt missing Lumi schedule grammar: %q", prompt)
	}
	if strings.Contains(prompt, "0 9 * *") {
		t.Fatalf("prompt should not teach unsupported cron expressions: %q", prompt)
	}
	if !strings.Contains(prompt, "User: remind me tomorrow") {
		t.Fatalf("prompt missing user content: %q", prompt)
	}
}

func TestConversationInstructionIncludesCurrentJobs(t *testing.T) {
	prompt := WithConversationInstructionAndJobs("delete current task", []Job{{
		ID:      "cron_123",
		Name:    "Greeting",
		Enabled: true,
		State:   JobState{NextRunAt: 1778279400000},
	}})
	if !strings.Contains(prompt, "id: cron_123; name: Greeting; status: enabled") {
		t.Fatalf("prompt missing current job list: %q", prompt)
	}
	if !strings.Contains(prompt, "Do not list first") {
		t.Fatalf("prompt missing single-task delete guidance: %q", prompt)
	}
}
