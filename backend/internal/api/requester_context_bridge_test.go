package api

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/requestercontext"
	"github.com/pengmide/lumi/internal/wecom"
)

func TestLocalRequesterContextBridgeDirMatchesInjectedAgentEnv(t *testing.T) {
	lumiHome := filepath.Join(t.TempDir(), "lumi-home")
	t.Setenv("LUMI_HOME", lumiHome)

	cfg := &config.Config{Agents: []config.AgentConfig{{
		ID: "claude",
		Env: map[string]string{
			requestercontext.EnvRequesterContextDir: filepath.Join(t.TempDir(), "stale"),
		},
	}}}
	if err := injectLocalRequesterContextEnv(cfg, "workspace-1", "claude"); err != nil {
		t.Fatalf("injectLocalRequesterContextEnv() error = %v", err)
	}

	bridge, err := localRequesterContextBridge("workspace-1", "claude")
	if err != nil {
		t.Fatalf("localRequesterContextBridge() error = %v", err)
	}
	agent := cfg.FindAgent("claude")
	if agent == nil {
		t.Fatal("agent not found after requester context env injection")
	}
	got := filepath.Clean(agent.Env[requestercontext.EnvRequesterContextDir])
	if got != bridge.Dir() {
		t.Fatalf("agent %s = %q, bridge.Dir() = %q", requestercontext.EnvRequesterContextDir, got, bridge.Dir())
	}
	want := filepath.Join(lumiHome, "runtime", "requester-context", localRequesterContextDirectoryScope, "claude")
	if bridge.Dir() != want {
		t.Fatalf("bridge.Dir() = %q, want %q", bridge.Dir(), want)
	}
}

func TestLocalRequesterContextBridgeIsStableAcrossWorkspaces(t *testing.T) {
	t.Setenv("LUMI_HOME", t.TempDir())

	first, err := localRequesterContextBridge("workspace-a", "claude")
	if err != nil {
		t.Fatalf("localRequesterContextBridge(first) error = %v", err)
	}
	second, err := localRequesterContextBridge("workspace-b", "claude")
	if err != nil {
		t.Fatalf("localRequesterContextBridge(second) error = %v", err)
	}
	if first.Dir() != second.Dir() {
		t.Fatalf("bridge dirs differ across workspaces: %q != %q", first.Dir(), second.Dir())
	}
}

func TestInjectLocalRequesterContextEnvInitializesEnvAndRejectsInvalidTargets(t *testing.T) {
	t.Setenv("LUMI_HOME", t.TempDir())

	cfg := &config.Config{Agents: []config.AgentConfig{{ID: "claude"}}}
	if err := injectLocalRequesterContextEnv(cfg, "workspace-1", "claude"); err != nil {
		t.Fatalf("injectLocalRequesterContextEnv() error = %v", err)
	}
	if got := cfg.FindAgent("claude").Env[requestercontext.EnvRequesterContextDir]; got == "" {
		t.Fatalf("%s was not initialized", requestercontext.EnvRequesterContextDir)
	}

	for _, test := range []struct {
		name        string
		cfg         *config.Config
		workspaceID string
		agentID     string
	}{
		{name: "nil config", cfg: nil, workspaceID: "workspace-1", agentID: "claude"},
		{name: "missing agent", cfg: cfg, workspaceID: "workspace-1", agentID: "codex"},
		{name: "unsafe workspace", cfg: cfg, workspaceID: "../workspace-1", agentID: "claude"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := injectLocalRequesterContextEnv(test.cfg, test.workspaceID, test.agentID); err == nil {
				t.Fatal("injectLocalRequesterContextEnv() error = nil")
			}
		})
	}
}

func TestWeComConcurrentInitializationInjectsRequesterEnvOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ACP process uses a POSIX shell")
	}

	testRoot := t.TempDir()
	t.Setenv("LUMI_HOME", filepath.Join(testRoot, "lumi-home"))
	requestCountPath := filepath.Join(testRoot, "initialize-count")
	envCapturePath := filepath.Join(testRoot, "requester-context-dir")
	workspacePath := filepath.Join(testRoot, "workspace")
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}

	cfg := &config.Config{
		Agents: []config.AgentConfig{{
			ID:      "fake-agent",
			Name:    "Fake ACP Agent",
			Command: "sh",
			Args: []string{
				"-c",
				`printf '%s' "$LUMI_REQUESTER_CONTEXT_DIR" > "$ENV_CAPTURE_PATH"; count=0; while IFS= read -r line; do count=$((count + 1)); printf '%s' "$count" > "$REQUEST_COUNT_PATH"; printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$count"; done`,
			},
			Env: map[string]string{
				"ENV_CAPTURE_PATH":   envCapturePath,
				"REQUEST_COUNT_PATH": requestCountPath,
			},
		}},
		DefaultAgent: "fake-agent",
		Workspaces: []config.WorkspaceConfig{{
			ID:   "workspace-1",
			Path: workspacePath,
		}},
	}
	runtime := newWeComChatRuntime(cfg, nil, nil)
	t.Cleanup(func() { _ = runtime.Shutdown() })
	sink := noopRequesterWeComSink{}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errs <- runtime.ensureInitialized("fake-agent", "workspace-1", workspacePath, sink)
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("ensureInitialized() error = %v", err)
		}
	}

	count, err := os.ReadFile(requestCountPath)
	if err != nil {
		t.Fatalf("ReadFile(initialize count) error = %v", err)
	}
	if string(count) != "1" {
		t.Fatalf("initialize request count = %q, want 1", count)
	}
	capturedDir, err := os.ReadFile(envCapturePath)
	if err != nil {
		t.Fatalf("ReadFile(requester env) error = %v", err)
	}
	bridge, err := localRequesterContextBridge("workspace-1", "fake-agent")
	if err != nil {
		t.Fatalf("localRequesterContextBridge() error = %v", err)
	}
	if got := filepath.Clean(string(capturedDir)); got != bridge.Dir() {
		t.Fatalf("agent requester context dir = %q, want %q", got, bridge.Dir())
	}
}

type noopRequesterWeComSink struct{}

func (noopRequesterWeComSink) Emit(wecom.ChatEvent) error { return nil }
