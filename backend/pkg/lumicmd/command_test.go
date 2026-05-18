package lumicmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/wechat"
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
		{ID: "codex", Name: "Codex CLI", Command: "npx"},
		{ID: "qwen", Name: "Qwen Code", Command: "npx"},
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

func TestWeChatRunUsesSavedCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := createCLIConfigWithAgent(t, home)
	if err := wechat.NewConfigStore().Save(wechat.Config{
		Enabled:   true,
		LoginMode: "qr",
		AccountID: "wx-saved",
		BotToken:  "saved-token",
		BaseURL:   "https://saved.test",
	}); err != nil {
		t.Fatalf("Save(wechat) error = %v", err)
	}

	originalStartServer := startServer
	originalLoginClient := newWeChatLoginClient
	defer func() {
		startServer = originalStartServer
		newWeChatLoginClient = originalLoginClient
	}()
	startServer = func(cfg *config.Config, staticFS fs.FS, port string) (serverRuntime, error) {
		return &fakeServerRuntime{port: port}, nil
	}
	newWeChatLoginClient = func(baseURL string) wechatLoginClient {
		t.Fatalf("newWeChatLoginClient called for saved credentials")
		return nil
	}

	stdout, stderr := tempStdoutStderr(t)
	if err := runWeChatRun([]string{
		"--workspace", workspace,
		"--agent", "claude",
		"--port", "3456",
	}, stdout, stderr); err != nil {
		t.Fatalf("runWeChatRun() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".lumi", "wechat", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wechat) error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"accountId": "wx-saved"`) || !strings.Contains(text, `"workspaceId": "cli-local"`) {
		t.Fatalf("wechat config = %s, want saved account with cli workspace", text)
	}
	if !stdoutContains(t, stdout, "WeChat: using saved account wx-saved") {
		t.Fatalf("stdout missing saved account message")
	}
	if !stdoutContains(t, stdout, "Workspace agents: claude, codex, qwen") {
		t.Fatalf("stdout missing workspace agents")
	}
}

func TestWeChatRunParsesWorkspaceAgentsFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := createCLIConfigWithAgent(t, home)
	if err := wechat.NewConfigStore().Save(wechat.Config{
		Enabled:   true,
		AccountID: "wx-saved",
		BotToken:  "saved-token",
		BaseURL:   "https://saved.test",
	}); err != nil {
		t.Fatalf("Save(wechat) error = %v", err)
	}

	originalStartServer := startServer
	originalLoginClient := newWeChatLoginClient
	defer func() {
		startServer = originalStartServer
		newWeChatLoginClient = originalLoginClient
	}()
	var startedConfig *config.Config
	startServer = func(cfg *config.Config, staticFS fs.FS, port string) (serverRuntime, error) {
		startedConfig = cfg
		return &fakeServerRuntime{port: port}, nil
	}
	newWeChatLoginClient = func(baseURL string) wechatLoginClient {
		t.Fatalf("newWeChatLoginClient called for saved credentials")
		return nil
	}

	stdout, stderr := tempStdoutStderr(t)
	if err := runWeChatRun([]string{
		"--workspace", workspace,
		"--agent", "codex",
		"--agents", "claude,codex",
	}, stdout, stderr); err != nil {
		t.Fatalf("runWeChatRun() error = %v", err)
	}
	if startedConfig == nil {
		t.Fatal("startServer was not called")
	}
	ws := startedConfig.FindWorkspace(lumicli.WorkspaceID)
	if ws == nil {
		t.Fatal("cli-local workspace not found")
	}
	if got := strings.Join(ws.Agents, ","); got != "claude,codex" {
		t.Fatalf("workspace agents = %q, want claude,codex", got)
	}
	if !stdoutContains(t, stdout, "Default agent: codex") || !stdoutContains(t, stdout, "Workspace agents: claude, codex") {
		t.Fatalf("stdout missing default/workspace agents")
	}
}

func TestWeChatRunSandboxWarmupOffDoesNotWarmup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := createCLIConfigWithAgent(t, home)
	if err := wechat.NewConfigStore().Save(wechat.Config{
		Enabled:   true,
		AccountID: "wx-saved",
		BotToken:  "saved-token",
		BaseURL:   "https://saved.test",
	}); err != nil {
		t.Fatalf("Save(wechat) error = %v", err)
	}

	originalStartServer := startServer
	originalLoginClient := newWeChatLoginClient
	defer func() {
		startServer = originalStartServer
		newWeChatLoginClient = originalLoginClient
	}()
	fakeRuntime := &fakeServerRuntime{}
	startServer = func(cfg *config.Config, staticFS fs.FS, port string) (serverRuntime, error) {
		return fakeRuntime, nil
	}
	newWeChatLoginClient = func(baseURL string) wechatLoginClient {
		t.Fatalf("newWeChatLoginClient called for saved credentials")
		return nil
	}

	stdout, stderr := tempStdoutStderr(t)
	if err := runWeChatRun([]string{
		"--workspace", workspace,
		"--kind", "sandbox",
		"--agent", "claude",
		"--sandbox-warmup", "off",
	}, stdout, stderr); err != nil {
		t.Fatalf("runWeChatRun() error = %v", err)
	}
	if fakeRuntime.warmupCalls != 0 {
		t.Fatalf("warmup calls = %d, want 0", fakeRuntime.warmupCalls)
	}
	if !stdoutContains(t, stdout, "Sandbox: warmup disabled") {
		t.Fatalf("stdout missing warmup disabled message")
	}
}

func TestWeChatRunSandboxWarmupWaitByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := createCLIConfigWithAgent(t, home)
	if err := wechat.NewConfigStore().Save(wechat.Config{
		Enabled:   true,
		AccountID: "wx-saved",
		BotToken:  "saved-token",
		BaseURL:   "https://saved.test",
	}); err != nil {
		t.Fatalf("Save(wechat) error = %v", err)
	}

	originalStartServer := startServer
	originalLoginClient := newWeChatLoginClient
	defer func() {
		startServer = originalStartServer
		newWeChatLoginClient = originalLoginClient
	}()
	fakeRuntime := &fakeServerRuntime{
		warmupState: sandbox.RuntimeState{Status: sandbox.StatusPending, Stage: sandbox.StageCheckingDocker},
		statuses: []lumicli.SandboxWarmupState{
			sandbox.RuntimeState{Status: sandbox.StatusPending, Stage: sandbox.StagePreparingImage},
			sandbox.RuntimeState{Status: sandbox.StatusRunning},
		},
	}
	startServer = func(cfg *config.Config, staticFS fs.FS, port string) (serverRuntime, error) {
		return fakeRuntime, nil
	}
	newWeChatLoginClient = func(baseURL string) wechatLoginClient {
		t.Fatalf("newWeChatLoginClient called for saved credentials")
		return nil
	}

	stdout, stderr := tempStdoutStderr(t)
	if err := runWeChatRun([]string{
		"--workspace", workspace,
		"--kind", "sandbox",
		"--agent", "claude",
	}, stdout, stderr); err != nil {
		t.Fatalf("runWeChatRun() error = %v", err)
	}
	if fakeRuntime.warmupCalls != 1 {
		t.Fatalf("warmup calls = %d, want 1", fakeRuntime.warmupCalls)
	}
	if fakeRuntime.statusCalls < 2 {
		t.Fatalf("status calls = %d, want at least 2", fakeRuntime.statusCalls)
	}
	for _, want := range []string{"Sandbox: warming up", "Sandbox: checking Docker", "Sandbox: preparing image", "Sandbox: ready"} {
		if !stdoutContains(t, stdout, want) {
			t.Fatalf("stdout missing %q", want)
		}
	}
}

func TestWeChatRunForceLoginScansAndStarts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := createCLIConfigWithAgent(t, home)
	if err := wechat.NewConfigStore().Save(wechat.Config{
		AccountID: "wx-old",
		BotToken:  "old-token",
		BaseURL:   "https://old.test",
	}); err != nil {
		t.Fatalf("Save(wechat) error = %v", err)
	}

	originalStartServer := startServer
	originalLoginClient := newWeChatLoginClient
	defer func() {
		startServer = originalStartServer
		newWeChatLoginClient = originalLoginClient
	}()
	startServer = func(cfg *config.Config, staticFS fs.FS, port string) (serverRuntime, error) {
		return &fakeServerRuntime{port: port}, nil
	}
	fakeLogin := &fakeWeChatLoginClient{
		qr: wechat.QRCode{Ticket: "ticket-1", ImageURL: "https://img.test/1"},
		statuses: []wechat.QRCodeStatus{
			{Status: "wait"},
			{Status: "scaned"},
			{Status: "confirmed", AccountID: "wx-new", BotToken: "new-token", BaseURL: "https://new.test/"},
		},
	}
	newWeChatLoginClient = func(baseURL string) wechatLoginClient {
		if baseURL != "https://old.test" {
			t.Fatalf("baseURL = %q, want saved base", baseURL)
		}
		return fakeLogin
	}

	stdout, stderr := tempStdoutStderr(t)
	if err := runWeChatRun([]string{
		"--workspace", workspace,
		"--agent", "claude",
		"--force-login",
	}, stdout, stderr); err != nil {
		t.Fatalf("runWeChatRun(force) error = %v", err)
	}
	if fakeLogin.statusCalls != 3 {
		t.Fatalf("status calls = %d, want 3", fakeLogin.statusCalls)
	}
	data, err := os.ReadFile(filepath.Join(home, ".lumi", "wechat", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wechat) error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"accountId": "wx-new"`) || !strings.Contains(text, `"botToken": "new-token"`) || !strings.Contains(text, `"baseUrl": "https://new.test"`) {
		t.Fatalf("wechat config = %s, want new credentials", text)
	}
	if !stdoutContains(t, stdout, "Scanned; waiting for confirmation") || !stdoutContains(t, stdout, "Ticket: ticket-1") {
		t.Fatalf("stdout missing QR login progress")
	}
}

func TestWeChatRunExpiredDoesNotOverwriteSavedCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := createCLIConfigWithAgent(t, home)
	if err := wechat.NewConfigStore().Save(wechat.Config{
		AccountID: "wx-old",
		BotToken:  "old-token",
		BaseURL:   "https://old.test",
	}); err != nil {
		t.Fatalf("Save(wechat) error = %v", err)
	}

	originalLoginClient := newWeChatLoginClient
	defer func() { newWeChatLoginClient = originalLoginClient }()
	newWeChatLoginClient = func(baseURL string) wechatLoginClient {
		return &fakeWeChatLoginClient{
			qr:       wechat.QRCode{Ticket: "ticket-1", ImageURL: "https://img.test/1"},
			statuses: []wechat.QRCodeStatus{{Status: "expired"}},
		}
	}

	stdout, stderr := tempStdoutStderr(t)
	err := runWeChatRun([]string{
		"--workspace", workspace,
		"--agent", "claude",
		"--force-login",
	}, stdout, stderr)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("runWeChatRun(expired) error = %v, want expired", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".lumi", "wechat", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(wechat) error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `"accountId": "wx-old"`) || !strings.Contains(text, `"botToken": "old-token"`) {
		t.Fatalf("wechat config overwritten after expired login: %s", text)
	}
}

func TestWeChatCommandUsageAndUnknownCommand(t *testing.T) {
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

	if err := runWeChat(nil, stdout, stderr, "lumi"); err != nil {
		t.Fatalf("runWeChat(nil) error = %v", err)
	}
	if !stdoutContains(t, stdout, "lumi wechat run") {
		t.Fatalf("stdout missing wechat usage")
	}
	err = runWeChat([]string{"missing"}, stdout, stderr, "lumi")
	if err == nil || !strings.Contains(err.Error(), "unknown wechat command") {
		t.Fatalf("runWeChat(missing) error = %v, want unknown command", err)
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

func tempStdoutStderr(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdout.Close()
		_ = stderr.Close()
	})
	return stdout, stderr
}

func stdoutContains(t *testing.T, stdout *os.File, want string) bool {
	t.Helper()
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("Seek(stdout) error = %v", err)
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("ReadAll(stdout) error = %v", err)
	}
	return strings.Contains(string(data), want)
}

func createCLIConfigWithAgent(t *testing.T, home string) string {
	t.Helper()
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
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
		{ID: "codex", Name: "Codex CLI", Command: "npx"},
		{ID: "qwen", Name: "Qwen Code", Command: "npx"},
	}
	state.Config.DefaultAgent = "claude"
	if err := state.Config.Save(state.Path); err != nil {
		t.Fatalf("Save(config) error = %v", err)
	}
	return workspace
}

type fakeServerRuntime struct {
	port        string
	warmupCalls int
	statusCalls int
	warmupState lumicli.SandboxWarmupState
	statuses    []lumicli.SandboxWarmupState
}

func (r *fakeServerRuntime) ListenAndServe() error {
	return nil
}

func (r *fakeServerRuntime) ShutdownWithContext(context.Context) error {
	return nil
}

func (r *fakeServerRuntime) Port() string {
	if r.port == "" {
		return "3000"
	}
	return r.port
}

func (r *fakeServerRuntime) WarmupSandbox(context.Context, string) (lumicli.SandboxWarmupState, error) {
	r.warmupCalls++
	if r.warmupState.Status == "" {
		r.warmupState = sandbox.RuntimeState{Status: sandbox.StatusRunning}
	}
	return r.warmupState, nil
}

func (r *fakeServerRuntime) EnsureSandbox(context.Context, string) (lumicli.SandboxWarmupState, error) {
	return r.WarmupSandbox(context.Background(), "")
}

func (r *fakeServerRuntime) SandboxStatus(string) (lumicli.SandboxWarmupState, error) {
	r.statusCalls++
	if len(r.statuses) > 0 {
		state := r.statuses[0]
		r.statuses = r.statuses[1:]
		return state, nil
	}
	return sandbox.RuntimeState{Status: sandbox.StatusRunning}, nil
}

type fakeWeChatLoginClient struct {
	qr          wechat.QRCode
	statuses    []wechat.QRCodeStatus
	statusCalls int
	err         error
}

func (c *fakeWeChatLoginClient) GetQRCode(context.Context) (wechat.QRCode, error) {
	if c.err != nil {
		return wechat.QRCode{}, c.err
	}
	return c.qr, nil
}

func (c *fakeWeChatLoginClient) GetQRCodeStatus(context.Context, string) (wechat.QRCodeStatus, error) {
	if c.err != nil {
		return wechat.QRCodeStatus{}, c.err
	}
	c.statusCalls++
	if len(c.statuses) == 0 {
		return wechat.QRCodeStatus{Status: "wait"}, nil
	}
	status := c.statuses[0]
	c.statuses = c.statuses[1:]
	return status, nil
}
