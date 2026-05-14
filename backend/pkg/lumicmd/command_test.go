package lumicmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/pkg/lumicli"
)

func TestCronEditParsesScopedFlagsAfterValue(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		fmt.Fprint(w, `{"job":{"id":"cron-1","name":"Greeting","enabled":false,"state":{"runCount":0}}}`)
	}))
	defer server.Close()

	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	err = runCronEdit([]string{
		"cron-1",
		"enabled",
		"false",
		"--api-base",
		server.URL,
		"--conversation-id",
		"conv-1",
	}, stdout)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/cron/jobs/cron-1" {
		t.Fatalf("path = %q, want /cron/jobs/cron-1", gotPath)
	}
	if gotQuery != "conversationId=conv-1" {
		t.Fatalf("query = %q, want conversationId=conv-1", gotQuery)
	}
	if !strings.Contains(gotBody, `"enabled":false`) {
		t.Fatalf("body = %q, want enabled false", gotBody)
	}
}

func TestWeComRunParsesIdleTimeoutFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	state, err := lumicli.ResolveConfigState("")
	if err != nil {
		t.Fatalf("ResolveConfigState() error = %v", err)
	}
	if err := lumicli.EnsureConfigFile(state); err != nil {
		t.Fatalf("EnsureConfigFile() error = %v", err)
	}
	state.Config.Agents = []config.AgentConfig{
		{ID: "claude", Name: "Claude Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := state.Config.Save(state.Path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	state.HasAgents = true

	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	err = runWeComRun([]string{
		"--workspace", workspace,
		"--kind", "sandbox",
		"--agent", "claude",
		"--bot-id", "bot-123",
		"--bot-secret", "secret-456",
		"--idle-timeout-sec", "-1",
	}, stdout, stderr)
	if err == nil || !strings.Contains(err.Error(), "idle timeout sec must be non-negative") {
		t.Fatalf("runWeComRun() error = %v, want idle timeout validation", err)
	}
}

func TestSandboxPruneCallsPruner(t *testing.T) {
	original := pruneSandboxes
	defer func() { pruneSandboxes = original }()

	var gotConfigPath string
	pruneSandboxes = func(ctx context.Context, configPath string) (lumicli.SandboxPruneResult, error) {
		gotConfigPath = configPath
		return lumicli.SandboxPruneResult{Containers: []lumicli.SandboxPrunedContainer{{
			WorkspaceID:    "cli-sandbox",
			ContainerName:  "lumi-sandbox-cli",
			Status:         "running",
			CreatedAt:      1715688000000,
			StartedAt:      1715688300000,
			LastActivityAt: 1715688600000,
		}}}, nil
	}

	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	if err := runSandboxPrune([]string{"--config", "/tmp/lumi.config.json"}, stdout); err != nil {
		t.Fatalf("runSandboxPrune() error = %v", err)
	}
	if gotConfigPath != "/tmp/lumi.config.json" {
		t.Fatalf("config path = %q, want /tmp/lumi.config.json", gotConfigPath)
	}
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("Seek(stdout) error = %v", err)
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Pruned Lumi sandbox containers:") ||
		!strings.Contains(text, "cli-sandbox") ||
		!strings.Contains(text, "lumi-sandbox-cli") ||
		!strings.Contains(text, "Total: 1") {
		t.Fatalf("stdout = %q, want prune table", text)
	}
}

func TestSandboxPrunePrintsEmptyResult(t *testing.T) {
	original := pruneSandboxes
	defer func() { pruneSandboxes = original }()

	pruneSandboxes = func(ctx context.Context, configPath string) (lumicli.SandboxPruneResult, error) {
		return lumicli.SandboxPruneResult{}, nil
	}

	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	if err := runSandboxPrune(nil, stdout); err != nil {
		t.Fatalf("runSandboxPrune() error = %v", err)
	}
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("Seek(stdout) error = %v", err)
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	if !strings.Contains(string(data), "No active Lumi sandbox containers found.") {
		t.Fatalf("stdout = %q, want empty prune message", string(data))
	}
}

func TestSandboxCommandUsageAndUnknownCommand(t *testing.T) {
	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	if err := runSandbox(nil, stdout, "lumi"); err != nil {
		t.Fatalf("runSandbox(nil) error = %v", err)
	}
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("Seek(stdout) error = %v", err)
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	if !strings.Contains(string(data), "lumi sandbox prune") {
		t.Fatalf("stdout = %q, want sandbox usage", string(data))
	}

	err = runSandbox([]string{"missing"}, stdout, "lumi")
	if err == nil || !strings.Contains(err.Error(), "unknown sandbox command") {
		t.Fatalf("runSandbox(missing) error = %v, want unknown command", err)
	}
}

func tempOutputFile(t *testing.T) (*os.File, error) {
	t.Helper()
	return os.CreateTemp(t.TempDir(), "stdout")
}
