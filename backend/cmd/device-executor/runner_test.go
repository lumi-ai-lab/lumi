package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	t.Setenv("LUMI_CLI", "")

	env := buildLumiRuntimeEnv("http://host.docker.internal:3000", &ExecutorConfig{
		WorkspaceID: "cli-sandbox-wecom-d9c429b4",
		Workspace:   workspace,
	})

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
			"KEEP_ME":             "yes",
			"LUMI_API_BASE":       "http://stale.test/api",
			"LUMI_WORKSPACE_ID":   "stale",
			"LUMI_WORKSPACE_PATH": "/host/workspace",
			"LUMI_CLI":            "/host/lumi",
		},
	}
	merged := mergeAgentEnv(agentCfg, map[string]string{
		"LUMI_API_BASE":       "http://host.docker.internal:3000/api",
		"LUMI_WORKSPACE_ID":   "cli-sandbox-wecom-d9c429b4",
		"LUMI_WORKSPACE_PATH": "/workspace",
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
	if _, ok := merged.Env["LUMI_CLI"]; ok {
		t.Fatalf("LUMI_CLI = %q, want stale value removed when runtime does not set it", merged.Env["LUMI_CLI"])
	}
	if agentCfg.Env["LUMI_WORKSPACE_PATH"] != "/host/workspace" {
		t.Fatalf("original agent env mutated: %#v", agentCfg.Env)
	}
}
