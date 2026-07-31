package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func writeTestMetricCLI(t *testing.T, workspace string, mode os.FileMode) string {
	t.Helper()
	binDir := filepath.Join(workspace, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "qdm-metric-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	metricCLI := filepath.Join(binDir, name)
	if err := os.WriteFile(metricCLI, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return metricCLI
}

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

	if err := injectLumiAgentEnv(cfg, "claude", "http://example.test/api", "workspace-1", "/workspace"); err != nil {
		t.Fatal(err)
	}

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

	if err := injectLumiAgentEnv(cfg, "pi", "", "", ""); err != nil {
		t.Fatal(err)
	}

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

	if err := injectLumiAgentEnv(cfg, "pi", "", "", ""); err != nil {
		t.Fatal(err)
	}

	agent := cfg.FindAgent("pi")
	if agent.Env["PI_ACP_PI_COMMAND"] != "/custom/pi" {
		t.Fatalf("PI_ACP_PI_COMMAND = %q, want explicit value", agent.Env["PI_ACP_PI_COMMAND"])
	}
}

func TestInjectLumiAgentEnvExposesWorkspaceMetricCLI(t *testing.T) {
	workspace := t.TempDir()
	metricCLI := writeTestMetricCLI(t, workspace, 0o755)
	t.Setenv("LUMI_CLI", "")
	t.Setenv("LUMI_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")

	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID:  "codex",
		Env: map[string]string{"PATH": "/usr/bin:/bin"},
	}}}

	if err := injectLumiAgentEnv(cfg, "codex", "", "workspace-1", workspace); err != nil {
		t.Fatal(err)
	}

	agent := cfg.FindAgent("codex")
	if got := agent.Env["QDM_METRIC_CLI"]; got != metricCLI {
		t.Fatalf("QDM_METRIC_CLI = %q, want %q", got, metricCLI)
	}
	parts := filepath.SplitList(agent.Env["PATH"])
	found := false
	for _, part := range parts {
		if part == filepath.Dir(metricCLI) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("PATH = %q, want workspace bin %q", agent.Env["PATH"], filepath.Dir(metricCLI))
	}
	if got := agent.Env["ZDOTDIR"]; got == "" {
		t.Fatal("ZDOTDIR is empty, want Lumi-managed zsh startup bridge")
	}
	if got := agent.Env["LUMI_MANAGED_ZDOTDIR"]; got != agent.Env["ZDOTDIR"] {
		t.Fatalf("LUMI_MANAGED_ZDOTDIR = %q, want %q", got, agent.Env["ZDOTDIR"])
	}
}

func TestInjectLumiAgentEnvReplacesStaleWorkspaceMetricCLI(t *testing.T) {
	oldWorkspace := t.TempDir()
	oldMetricCLI := writeTestMetricCLI(t, oldWorkspace, 0o755)
	newWorkspace := t.TempDir()
	newMetricCLI := writeTestMetricCLI(t, newWorkspace, 0o755)
	separator := string(os.PathListSeparator)
	lumiHome := t.TempDir()
	t.Setenv("LUMI_HOME", lumiHome)
	t.Setenv("SHELL", "/bin/zsh")

	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID: "codex",
		Env: map[string]string{
			"QDM_METRIC_CLI": oldMetricCLI,
			"PATH":           filepath.Dir(oldMetricCLI) + separator + "/usr/bin",
		},
	}}}

	if err := injectLumiAgentEnv(cfg, "codex", "", "workspace-2", newWorkspace); err != nil {
		t.Fatal(err)
	}

	agent := cfg.FindAgent("codex")
	firstBridge := agent.Env["ZDOTDIR"]
	if got := agent.Env["QDM_METRIC_CLI"]; got != newMetricCLI {
		t.Fatalf("QDM_METRIC_CLI = %q, want %q", got, newMetricCLI)
	}
	for _, part := range filepath.SplitList(agent.Env["PATH"]) {
		if part == filepath.Dir(oldMetricCLI) {
			t.Fatalf("PATH = %q, stale workspace bin remains", agent.Env["PATH"])
		}
	}

	thirdWorkspace := t.TempDir()
	thirdMetricCLI := writeTestMetricCLI(t, thirdWorkspace, 0o755)
	if err := injectLumiAgentEnv(cfg, "codex", "", "workspace-3", thirdWorkspace); err != nil {
		t.Fatal(err)
	}
	if got := agent.Env["QDM_METRIC_CLI"]; got != thirdMetricCLI {
		t.Fatalf("QDM_METRIC_CLI after second switch = %q, want %q", got, thirdMetricCLI)
	}
	if got := agent.Env["ZDOTDIR"]; got == "" || got == firstBridge {
		t.Fatalf("ZDOTDIR after workspace switch = %q, want a replacement for %q", got, firstBridge)
	}
}

func TestInjectLumiAgentEnvDoesNotExposeMissingWorkspaceMetricCLI(t *testing.T) {
	t.Setenv("LUMI_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID: "codex",
		Env: map[string]string{
			"PATH":                  "/old/workspace/bin:/usr/bin:/bin",
			"QDM_METRIC_CLI":        "/old/workspace/bin/qdm-metric-cli",
			"ZDOTDIR":               "/tmp/lumi-managed",
			"LUMI_MANAGED_ZDOTDIR":  "/tmp/lumi-managed",
			"LUMI_ORIGINAL_ZDOTDIR": "/custom/zsh",
		},
	}}}

	if err := injectLumiAgentEnv(cfg, "codex", "", "workspace-1", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	agent := cfg.FindAgent("codex")
	if got := agent.Env["QDM_METRIC_CLI"]; got != "" {
		t.Fatalf("QDM_METRIC_CLI = %q, want empty", got)
	}
	if got := agent.Env["ZDOTDIR"]; got != "/custom/zsh" {
		t.Fatalf("ZDOTDIR = %q, want original custom directory", got)
	}
	if _, ok := agent.Env["LUMI_MANAGED_ZDOTDIR"]; ok {
		t.Fatal("LUMI_MANAGED_ZDOTDIR remains after CLI removal")
	}
}

func TestInjectLumiAgentEnvDoesNotBridgeNonZshShell(t *testing.T) {
	workspace := t.TempDir()
	writeTestMetricCLI(t, workspace, 0o755)
	t.Setenv("LUMI_CLI", "")
	t.Setenv("LUMI_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/bash")

	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID:  "codex",
		Env: map[string]string{"PATH": "/usr/bin:/bin"},
	}}}

	if err := injectLumiAgentEnv(cfg, "codex", "", "workspace-1", workspace); err != nil {
		t.Fatal(err)
	}
	agent := cfg.FindAgent("codex")
	if got := agent.Env["QDM_METRIC_CLI"]; got == "" {
		t.Fatal("QDM_METRIC_CLI is empty")
	}
	if got := agent.Env["ZDOTDIR"]; got != "" {
		t.Fatalf("ZDOTDIR = %q for non-zsh shell, want empty", got)
	}
}

func TestInjectLumiAgentEnvEmptyWorkspaceRemovesManagedCLIEnv(t *testing.T) {
	t.Setenv("LUMI_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID: "codex",
		Env: map[string]string{
			"PATH":                  "/old/workspace/bin:/usr/bin:/bin",
			"QDM_METRIC_CLI":        "/old/workspace/bin/qdm-metric-cli",
			"ZDOTDIR":               "/tmp/lumi-managed",
			"LUMI_MANAGED_ZDOTDIR":  "/tmp/lumi-managed",
			"LUMI_ORIGINAL_ZDOTDIR": "/custom/zsh",
		},
	}}}

	if err := injectLumiAgentEnv(cfg, "codex", "", "", ""); err != nil {
		t.Fatal(err)
	}
	agent := cfg.FindAgent("codex")
	if _, ok := agent.Env["QDM_METRIC_CLI"]; ok {
		t.Fatalf("QDM_METRIC_CLI remains: %#v", agent.Env)
	}
	if agent.Env["ZDOTDIR"] != "/custom/zsh" {
		t.Fatalf("ZDOTDIR = %q, want restored original", agent.Env["ZDOTDIR"])
	}
}

func TestLocalAgentACPModeIDPrefersConfiguredCodexMode(t *testing.T) {
	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID:          "codex",
		Command:     "npx",
		Args:        []string{"-y", "@agentclientprotocol/codex-acp@1.1.7"},
		SessionMode: "yoloNoSandbox",
	}}}

	if got := localAgentACPModeID(cfg, "codex", "auto"); got != "agent-full-access" {
		t.Fatalf("localAgentACPModeID() = %q, want agent-full-access", got)
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
