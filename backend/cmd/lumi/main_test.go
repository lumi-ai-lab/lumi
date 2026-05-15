package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestRunRoutesCLICommands(t *testing.T) {
	if err := run([]string{"cron", "--help"}); err != nil {
		t.Fatalf("run cron help error = %v", err)
	}
	if err := run([]string{"sandbox", "--help"}); err != nil {
		t.Fatalf("run sandbox help error = %v", err)
	}
	if err := run([]string{"setup", "--help"}); err != nil {
		t.Fatalf("run setup help error = %v", err)
	}
	if err := run([]string{"wecom", "--help"}); err != nil {
		t.Fatalf("run wecom help error = %v", err)
	}
}

func TestRunRejectsUnknownBareCommand(t *testing.T) {
	if err := run([]string{"bogus"}); err == nil {
		t.Fatal("run bogus error = nil, want error")
	}
}

func TestPrintStartupInfoIncludesAutoAddedQwen(t *testing.T) {
	cfg := &config.Config{
		Agents: []config.AgentConfig{
			{ID: "claude", Name: "Claude Code", Command: "npx"},
			{ID: "codex", Name: "Codex CLI", Command: "npx"},
		},
		DefaultAgent: "claude",
	}
	cfg.EnsureBuiltInDefaults()

	output := captureStdout(t, func() {
		printStartupInfo(cfg, "/tmp/lumi.config.json")
	})
	for _, want := range []string{"Qwen Code", "ID: qwen", "Command: npx -y @qwen-code/qwen-code --acp"} {
		if !strings.Contains(output, want) {
			t.Fatalf("startup output missing %q:\n%s", want, output)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = original })

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() writer error = %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() reader error = %v", err)
	}
	os.Stdout = original
	return buf.String()
}
