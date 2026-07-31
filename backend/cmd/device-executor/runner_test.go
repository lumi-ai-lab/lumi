package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/config"
)

func newTestRunner() *Runner {
	cfg := &ExecutorConfig{
		DeviceID:     "dev-1",
		DefaultAgent: "claude",
		Agents: []config.AgentConfig{
			{ID: "claude", Name: "Claude Code", Command: "echo"},
		},
	}
	client := NewClient("http://example.com", "token", cfg)
	return client.runner
}

func TestAbortCurrentTaskClearsRunnerState(t *testing.T) {
	t.Parallel()

	runner := newTestRunner()
	proc := agent.NewProcess(&config.AgentConfig{
		ID:      "claude",
		Name:    "Claude Code",
		Command: "echo",
	})

	runner.agents["claude"] = proc
	runner.initialized["claude"] = true
	runner.sessions["task-1"] = "session-1"
	runner.currentTask = &runningTask{
		TaskID:    "task-1",
		AgentID:   "claude",
		SessionID: "session-1",
		Process:   proc,
	}

	runner.AbortCurrentTask("connection lost")

	if running := runner.RunningTaskIDs(); len(running) != 0 {
		t.Fatalf("RunningTaskIDs() = %v, want empty", running)
	}
	if got := runner.sessionForTask("task-1"); got != "" {
		t.Fatalf("sessionForTask(task-1) = %q, want empty", got)
	}
	if _, ok := runner.agents["claude"]; ok {
		t.Fatal("agent process still cached after AbortCurrentTask")
	}
	if runner.initialized["claude"] {
		t.Fatal("agent initialization state still cached after AbortCurrentTask")
	}
}

func TestFinishTaskKeepsNewerTaskRegistered(t *testing.T) {
	t.Parallel()

	runner := newTestRunner()
	runner.client.setSetupReady(true)
	runner.sessions["task-old"] = "session-old"
	runner.currentTask = &runningTask{TaskID: "task-new"}

	runner.finishTask("task-old")

	if running := runner.RunningTaskIDs(); len(running) != 1 || running[0] != "task-new" {
		t.Fatalf("RunningTaskIDs() = %v, want [task-new]", running)
	}
	if got := runner.sessionForTask("task-old"); got != "" {
		t.Fatalf("sessionForTask(task-old) = %q, want empty", got)
	}
}

func TestCancelAbortsCurrentTaskImmediately(t *testing.T) {
	t.Parallel()

	runner := newTestRunner()
	proc := agent.NewProcess(&config.AgentConfig{
		ID:      "claude",
		Name:    "Claude Code",
		Command: "echo",
	})

	runner.agents["claude"] = proc
	runner.initialized["claude"] = true
	runner.sessions["task-1"] = "session-1"
	runner.currentTask = &runningTask{
		TaskID:  "task-1",
		AgentID: "claude",
		Process: proc,
	}

	runner.Cancel(context.Background(), Envelope{
		TaskID: "task-1",
		Payload: mustMarshalTaskCancelPayload(t, TaskCancelPayload{
			SessionID: "session-1",
			Reason:    "client_disconnected",
		}),
	})

	if running := runner.RunningTaskIDs(); len(running) != 0 {
		t.Fatalf("RunningTaskIDs() = %v, want empty", running)
	}
	if got := runner.sessionForTask("task-1"); got != "" {
		t.Fatalf("sessionForTask(task-1) = %q, want empty", got)
	}
	if _, ok := runner.agents["claude"]; ok {
		t.Fatal("agent process still cached after Cancel")
	}
}

func TestBuildLumiRuntimeEnv(t *testing.T) {
	workspace := t.TempDir()
	cliPath := filepath.Join(workspace, "lumi")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metricName := "qdm-metric-cli"
	if runtime.GOOS == "windows" {
		metricName += ".exe"
	}
	metricCLI := filepath.Join(binDir, metricName)
	if err := os.WriteFile(metricCLI, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUMI_CLI", "")
	t.Setenv("PATH", "/usr/bin:/bin")

	env, err := buildLumiRuntimeEnv("http://host.docker.internal:3000", &ExecutorConfig{
		WorkspaceID: "cli-sandbox-wecom-d9c429b4",
		Workspace:   workspace,
	}, map[string]string{"SHELL": "/bin/zsh", "ZDOTDIR": "/custom/zsh"})
	if err != nil {
		t.Fatal(err)
	}

	if env["LUMI_API_BASE"] != "http://host.docker.internal:3000/api" {
		t.Fatalf("LUMI_API_BASE = %q", env["LUMI_API_BASE"])
	}
	if env["LUMI_WORKSPACE_ID"] != "cli-sandbox-wecom-d9c429b4" {
		t.Fatalf("LUMI_WORKSPACE_ID = %q", env["LUMI_WORKSPACE_ID"])
	}
	if env["LUMI_WORKSPACE_PATH"] != workspace {
		t.Fatalf("LUMI_WORKSPACE_PATH = %q, want %q", env["LUMI_WORKSPACE_PATH"], workspace)
	}
	if env["LUMI_CLI"] != cliPath {
		t.Fatalf("LUMI_CLI = %q, want %q", env["LUMI_CLI"], cliPath)
	}
	if env["QDM_METRIC_CLI"] != metricCLI {
		t.Fatalf("QDM_METRIC_CLI = %q, want %q", env["QDM_METRIC_CLI"], metricCLI)
	}
	parts := filepath.SplitList(env["PATH"])
	if len(parts) == 0 || parts[0] != binDir {
		t.Fatalf("PATH = %q, want first entry %q", env["PATH"], binDir)
	}
	if env["ZDOTDIR"] == "" || env["ZDOTDIR"] == "/custom/zsh" {
		t.Fatalf("ZDOTDIR = %q, want managed bridge", env["ZDOTDIR"])
	}
	if env["LUMI_ORIGINAL_ZDOTDIR"] != "/custom/zsh" {
		t.Fatalf("LUMI_ORIGINAL_ZDOTDIR = %q", env["LUMI_ORIGINAL_ZDOTDIR"])
	}
}

func TestShouldRecoverUnknownSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "unknown session id compact", err: errors.New("Invalid params: Unknown sessionId"), want: true},
		{name: "session not found", err: errors.New("Session not found"), want: true},
		{name: "invalid params session", err: errors.New("Invalid params: bad session"), want: false},
		{name: "other error", err: errors.New("model unavailable"), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldRecoverUnknownSession(tt.err); got != tt.want {
				t.Fatalf("shouldRecoverUnknownSession(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func mustMarshalTaskCancelPayload(t *testing.T, payload TaskCancelPayload) []byte {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMergeAgentEnvRuntimeOverridesLumiValues(t *testing.T) {
	agentCfg := &config.AgentConfig{
		ID:      "claude",
		Command: "codex",
		Env: map[string]string{
			"KEEP_ME":               "yes",
			"LUMI_API_BASE":         "http://stale.test/api",
			"LUMI_WORKSPACE_ID":     "stale",
			"LUMI_WORKSPACE_PATH":   "/host/workspace",
			"LUMI_CLI":              "/host/lumi",
			"QDM_METRIC_CLI":        "/host/workspace/bin/qdm-metric-cli",
			"PATH":                  "/host/workspace/bin:/usr/bin",
			"ZDOTDIR":               "/host/lumi-zdotdir",
			"LUMI_MANAGED_ZDOTDIR":  "/host/lumi-zdotdir",
			"LUMI_ORIGINAL_ZDOTDIR": "/host/custom-zsh",
		},
	}
	merged := mergeAgentEnv(agentCfg, map[string]string{
		"LUMI_API_BASE":         "http://host.docker.internal:3000/api",
		"LUMI_WORKSPACE_ID":     "cli-sandbox-wecom-d9c429b4",
		"LUMI_WORKSPACE_PATH":   "/workspace",
		"QDM_METRIC_CLI":        "/workspace/bin/qdm-metric-cli",
		"PATH":                  "/workspace/bin:/usr/bin",
		"ZDOTDIR":               "/runtime/lumi-zdotdir",
		"LUMI_MANAGED_ZDOTDIR":  "/runtime/lumi-zdotdir",
		"LUMI_ORIGINAL_ZDOTDIR": "/runtime/custom-zsh",
	})

	if merged == agentCfg {
		t.Fatal("mergeAgentEnv returned original config pointer")
	}
	if merged.Env["KEEP_ME"] != "yes" {
		t.Fatalf("KEEP_ME = %q, want preserved", merged.Env["KEEP_ME"])
	}
	if merged.Env["LUMI_API_BASE"] != "http://host.docker.internal:3000/api" {
		t.Fatalf("LUMI_API_BASE = %q", merged.Env["LUMI_API_BASE"])
	}
	if merged.Env["LUMI_WORKSPACE_ID"] != "cli-sandbox-wecom-d9c429b4" {
		t.Fatalf("LUMI_WORKSPACE_ID = %q", merged.Env["LUMI_WORKSPACE_ID"])
	}
	if merged.Env["LUMI_WORKSPACE_PATH"] != "/workspace" {
		t.Fatalf("LUMI_WORKSPACE_PATH = %q", merged.Env["LUMI_WORKSPACE_PATH"])
	}
	if merged.Env["QDM_METRIC_CLI"] != "/workspace/bin/qdm-metric-cli" {
		t.Fatalf("QDM_METRIC_CLI = %q", merged.Env["QDM_METRIC_CLI"])
	}
	if merged.Env["PATH"] != "/workspace/bin:/usr/bin" {
		t.Fatalf("PATH = %q", merged.Env["PATH"])
	}
	if merged.Env["ZDOTDIR"] != "/runtime/lumi-zdotdir" {
		t.Fatalf("ZDOTDIR = %q", merged.Env["ZDOTDIR"])
	}
	if merged.Env["LUMI_ORIGINAL_ZDOTDIR"] != "/runtime/custom-zsh" {
		t.Fatalf("LUMI_ORIGINAL_ZDOTDIR = %q", merged.Env["LUMI_ORIGINAL_ZDOTDIR"])
	}
	if _, ok := merged.Env["LUMI_CLI"]; ok {
		t.Fatalf("LUMI_CLI = %q, want stale value removed when runtime does not set it", merged.Env["LUMI_CLI"])
	}
	if agentCfg.Env["LUMI_WORKSPACE_PATH"] != "/host/workspace" {
		t.Fatalf("original agent env mutated: %#v", agentCfg.Env)
	}
}

func TestMergeAgentEnvRemovesStaleManagedZDOTDir(t *testing.T) {
	agentCfg := &config.AgentConfig{Env: map[string]string{
		"KEEP_ME":               "yes",
		"ZDOTDIR":               "/host/lumi-zdotdir",
		"LUMI_MANAGED_ZDOTDIR":  "/host/lumi-zdotdir",
		"LUMI_ORIGINAL_ZDOTDIR": "",
	}}

	merged := mergeAgentEnv(agentCfg, map[string]string{"LUMI_WORKSPACE_ID": "workspace-1"})
	if _, ok := merged.Env["ZDOTDIR"]; ok {
		t.Fatalf("ZDOTDIR = %q, want stale managed value removed", merged.Env["ZDOTDIR"])
	}
	if _, ok := merged.Env["LUMI_MANAGED_ZDOTDIR"]; ok {
		t.Fatal("LUMI_MANAGED_ZDOTDIR remains")
	}
	if merged.Env["KEEP_ME"] != "yes" {
		t.Fatalf("KEEP_ME = %q", merged.Env["KEEP_ME"])
	}
	if agentCfg.Env["ZDOTDIR"] != "/host/lumi-zdotdir" {
		t.Fatalf("original agent env mutated: %#v", agentCfg.Env)
	}
}
