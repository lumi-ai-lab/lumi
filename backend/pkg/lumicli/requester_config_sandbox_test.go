package lumicli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/wecom"
)

func TestPrepareRunRejectsSandboxRequesterConfigInsideWorkspace(t *testing.T) {
	t.Run("direct path", func(t *testing.T) {
		state, workspace, home := newRequesterSandboxRunState(t)
		policyPath := filepath.Join(workspace, "requesters.json")
		writeRequesterSandboxPolicy(t, policyPath)

		_, _, err := PrepareRun(state, requesterSandboxRunOptions(workspace, policyPath))
		if err == nil || !strings.Contains(err.Error(), "requester config must be outside the sandbox workspace") {
			t.Fatalf("PrepareRun() error = %v, want sandbox requester config location rejection", err)
		}
		assertSandboxWeComConfigAbsent(t, home, workspace)
	})

	t.Run("symlink outside workspace targets file inside", func(t *testing.T) {
		state, workspace, home := newRequesterSandboxRunState(t)
		target := filepath.Join(workspace, "requesters.json")
		writeRequesterSandboxPolicy(t, target)
		link := filepath.Join(t.TempDir(), "requesters-link.json")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("Symlink() is unavailable: %v", err)
		}

		_, _, err := PrepareRun(state, requesterSandboxRunOptions(workspace, link))
		if err == nil || !strings.Contains(err.Error(), "requester config must be outside the sandbox workspace") {
			t.Fatalf("PrepareRun() error = %v, want resolved symlink location rejection", err)
		}
		assertSandboxWeComConfigAbsent(t, home, workspace)
	})
}

func TestPrepareRunAllowsSandboxRequesterConfigOutsideWorkspace(t *testing.T) {
	state, workspace, _ := newRequesterSandboxRunState(t)
	policyPath := filepath.Join(t.TempDir(), "requesters.json")
	writeRequesterSandboxPolicy(t, policyPath)

	cfg, _, err := PrepareRun(state, requesterSandboxRunOptions(workspace, policyPath))
	if err != nil {
		t.Fatalf("PrepareRun() error = %v", err)
	}
	saved, err := wecom.NewConfigStoreForInstance(cfg.DefaultWorkspace).Load()
	if err != nil {
		t.Fatalf("Load(saved WeCom config) error = %v", err)
	}
	if saved.RequesterConfigPath != policyPath {
		t.Fatalf("RequesterConfigPath = %q, want %q", saved.RequesterConfigPath, policyPath)
	}
}

func newRequesterSandboxRunState(t *testing.T) (*ConfigState, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	state, err := ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{{ID: "claude", Name: "Claude", Command: "npx"}}
	state.Config.DefaultAgent = "claude"
	if err := saveConfig(state.Config, state.Path); err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}
	state.HasAgents = true
	return state, workspace, home
}

func requesterSandboxRunOptions(workspace, policyPath string) RunOptions {
	return RunOptions{
		Workspace:           workspace,
		Kind:                "sandbox",
		AgentID:             "claude",
		BotID:               "bot-123",
		BotSecret:           "secret-456",
		RequesterConfigPath: policyPath,
	}
}

func writeRequesterSandboxPolicy(t *testing.T, path string) {
	t.Helper()
	const raw = `{"version":1,"botId":"bot-123","users":[{"userId":"u1","displayName":"U1","enabled":true,"capabilities":["qdm.cmr.query"],"scope":{"manageAreaIds":["CN18"],"categoryLevel1Ids":["12"]}}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(policy) error = %v", err)
	}
}

func assertSandboxWeComConfigAbsent(t *testing.T, home, workspace string) {
	t.Helper()
	workspaceID, err := resolveSandboxWorkspaceID("wecom", "bot-123", workspace, "")
	if err != nil {
		t.Fatalf("resolveSandboxWorkspaceID() error = %v", err)
	}
	path := filepath.Join(home, ".lumi", "wecom", "instances", workspaceID, "config.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("WeCom config stat error = %v, want no file at %s", err, path)
	}
}
