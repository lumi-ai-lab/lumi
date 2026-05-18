package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestInjectLumiAgentEnv(t *testing.T) {
	cliPath := filepath.Join(t.TempDir(), "lumi")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUMI_CLI", cliPath)

	cfg := &config.Config{
		Agents: []config.AgentConfig{{
			ID:  "claude",
			Env: map[string]string{"PATH": "/usr/bin"},
		}},
	}

	injectLumiAgentEnv(cfg, "claude", "http://example.test/api")

	agent := cfg.FindAgent("claude")
	if agent == nil {
		t.Fatal("agent not found")
	}
	if agent.Env["LUMI_API_BASE"] != "http://example.test/api" {
		t.Fatalf("LUMI_API_BASE = %q", agent.Env["LUMI_API_BASE"])
	}
	if agent.Env["LUMI_CLI"] != cliPath {
		t.Fatalf("LUMI_CLI = %q, want %q", agent.Env["LUMI_CLI"], cliPath)
	}
	parts := filepath.SplitList(agent.Env["PATH"])
	if len(parts) == 0 || parts[0] != filepath.Dir(cliPath) {
		t.Fatalf("PATH = %q, want first entry %q", agent.Env["PATH"], filepath.Dir(cliPath))
	}
	if !strings.Contains(agent.Env["PATH"], "/usr/bin") {
		t.Fatalf("PATH = %q, want original PATH preserved", agent.Env["PATH"])
	}
}

func TestLumiAPIBaseForWorkspaceMapsSandboxToHostDockerInternal(t *testing.T) {
	cfg := &config.Config{
		PublicServerURL: "http://127.0.0.1:3000",
		Workspaces: []config.WorkspaceConfig{{
			ID:   "sandbox-1",
			Kind: "sandbox",
		}},
	}
	if got := lumiAPIBaseForWorkspace(cfg, "sandbox-1"); !strings.Contains(got, "host.docker.internal") {
		t.Fatalf("lumiAPIBaseForWorkspace() = %q, want host.docker.internal", got)
	}
}
