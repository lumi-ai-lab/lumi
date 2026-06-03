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
			ID: "claude",
			Env: map[string]string{
				"PATH":                "/usr/bin",
				"LUMI_API_BASE":       "http://stale.test/api",
				"LUMI_WORKSPACE_ID":   "stale",
				"LUMI_WORKSPACE_PATH": "/stale",
			},
		}},
	}

	injectLumiAgentEnv(cfg, "claude", "http://example.test/api", "workspace-1", "/workspace")

	agent := cfg.FindAgent("claude")
	if agent == nil {
		t.Fatal("agent not found")
	}
	if agent.Env["LUMI_API_BASE"] != "http://example.test/api" {
		t.Fatalf("LUMI_API_BASE = %q", agent.Env["LUMI_API_BASE"])
	}
	if agent.Env["LUMI_WORKSPACE_ID"] != "workspace-1" {
		t.Fatalf("LUMI_WORKSPACE_ID = %q", agent.Env["LUMI_WORKSPACE_ID"])
	}
	if agent.Env["LUMI_WORKSPACE_PATH"] != "/workspace" {
		t.Fatalf("LUMI_WORKSPACE_PATH = %q", agent.Env["LUMI_WORKSPACE_PATH"])
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

func TestInjectLumiAgentEnvSetsPiCommand(t *testing.T) {
	binDir := t.TempDir()
	piPath := filepath.Join(binDir, "pi")
	if err := os.WriteFile(piPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin")
	t.Setenv("PI_ACP_PI_COMMAND", "")

	cfg := &config.Config{
		Agents: []config.AgentConfig{{
			ID:  "pi",
			Env: map[string]string{"PATH": "/usr/bin"},
		}},
	}

	injectLumiAgentEnv(cfg, "pi", "", "", "")

	agent := cfg.FindAgent("pi")
	if agent == nil {
		t.Fatal("agent not found")
	}
	if agent.Env["PI_ACP_PI_COMMAND"] != piPath {
		t.Fatalf("PI_ACP_PI_COMMAND = %q, want %q", agent.Env["PI_ACP_PI_COMMAND"], piPath)
	}
	parts := filepath.SplitList(agent.Env["PATH"])
	if len(parts) == 0 || parts[0] != binDir {
		t.Fatalf("PATH = %q, want first entry %q", agent.Env["PATH"], binDir)
	}
}

func TestInjectLumiAgentEnvKeepsExplicitPiCommand(t *testing.T) {
	cfg := &config.Config{
		Agents: []config.AgentConfig{{
			ID:  "pi",
			Env: map[string]string{"PI_ACP_PI_COMMAND": "/custom/pi"},
		}},
	}

	injectLumiAgentEnv(cfg, "pi", "", "", "")

	agent := cfg.FindAgent("pi")
	if agent.Env["PI_ACP_PI_COMMAND"] != "/custom/pi" {
		t.Fatalf("PI_ACP_PI_COMMAND = %q, want explicit value", agent.Env["PI_ACP_PI_COMMAND"])
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
