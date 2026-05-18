package lumicmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/wechat"
	"github.com/pengmide/lumi/pkg/lumicli"
)

var pruneSandboxes = lumicli.PruneSandboxes
var startServer = func(cfg *config.Config, staticFS fs.FS, port string) (serverRuntime, error) {
	return lumicli.StartServer(cfg, staticFS, port)
}
var newWeChatLoginClient = func(baseURL string) wechatLoginClient {
	return wechat.NewLoginClient(baseURL)
}

func Run(args []string, stdin *os.File, stdout, stderr *os.File) error {
	return RunAs("lumi", args, stdin, stdout, stderr)
}

func RunAs(programName string, args []string, stdin *os.File, stdout, stderr *os.File) error {
	programName = strings.TrimSpace(programName)
	if programName == "" {
		programName = "lumi"
	}
	if len(args) == 0 {
		printUsage(stdout, programName)
		return nil
	}

	switch args[0] {
	case "cron":
		return runCron(args[1:], stdout, programName)
	case "sandbox":
		return runSandbox(args[1:], stdout, programName)
	case "setup":
		return runSetup(args[1:], stdin, stdout)
	case "wechat":
		return runWeChat(args[1:], stdout, stderr, programName)
	case "wecom":
		return runWeCom(args[1:], stdin, stdout, stderr, programName)
	case "-h", "--help", "help":
		printUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

type wechatLoginClient interface {
	GetQRCode(context.Context) (wechat.QRCode, error)
	GetQRCodeStatus(context.Context, string) (wechat.QRCodeStatus, error)
}

type wechatRunCredentials struct {
	AccountID string
	BotToken  string
	BaseURL   string
}

type serverRuntime interface {
	ListenAndServe() error
	ShutdownWithContext(context.Context) error
	Port() string
	EnsureSandbox(context.Context, string) (lumicli.SandboxWarmupState, error)
	SandboxStatus(string) (lumicli.SandboxWarmupState, error)
	WarmupSandbox(context.Context, string) (lumicli.SandboxWarmupState, error)
}

type sandboxWarmupMode string

const (
	sandboxWarmupAsync sandboxWarmupMode = "async"
	sandboxWarmupWait  sandboxWarmupMode = "wait"
	sandboxWarmupOff   sandboxWarmupMode = "off"
)

type cronSchedulePayload struct {
	Type     string `json:"type"`
	CronExpr string `json:"cronExpr,omitempty"`
}

type cronJobPayload struct {
	ID             string               `json:"id,omitempty"`
	Name           string               `json:"name,omitempty"`
	Description    string               `json:"description,omitempty"`
	Prompt         string               `json:"prompt,omitempty"`
	Exec           string               `json:"exec,omitempty"`
	AgentID        string               `json:"agentId,omitempty"`
	WorkspaceID    string               `json:"workspaceId,omitempty"`
	ConversationID string               `json:"conversationId,omitempty"`
	Channel        string               `json:"channel,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
	Schedule       *cronSchedulePayload `json:"schedule,omitempty"`
	Silent         *bool                `json:"silent,omitempty"`
	Mute           *bool                `json:"mute,omitempty"`
	SessionMode    string               `json:"sessionMode,omitempty"`
	WorkDir        string               `json:"workDir,omitempty"`
	Mode           string               `json:"mode,omitempty"`
	TimeoutMins    *int                 `json:"timeoutMins,omitempty"`
	Target         *cronTargetPayload   `json:"target,omitempty"`
	State          struct {
		NextRunAt  int64  `json:"nextRunAt,omitempty"`
		LastRunAt  int64  `json:"lastRunAt,omitempty"`
		LastStatus string `json:"lastStatus,omitempty"`
		LastError  string `json:"lastError,omitempty"`
		RunCount   int    `json:"runCount,omitempty"`
	} `json:"state,omitempty"`
}

type cronTargetPayload struct {
	WeChat *cronWeChatTargetPayload `json:"wechat,omitempty"`
	WeCom  *cronWeComTargetPayload  `json:"wecom,omitempty"`
}

type cronWeChatTargetPayload struct {
	ConversationKey string `json:"conversationKey,omitempty"`
	ContextToken    string `json:"contextToken,omitempty"`
}

type cronWeComTargetPayload struct {
	ReqID    string `json:"reqId,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
	ChatType string `json:"chatType,omitempty"`
	UserID   string `json:"userId,omitempty"`
}

func runSandbox(args []string, stdout *os.File, programName string) error {
	if len(args) == 0 {
		printSandboxUsage(stdout, programName)
		return nil
	}

	switch args[0] {
	case "prune":
		return runSandboxPrune(args[1:], stdout)
	case "-h", "--help", "help":
		printSandboxUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown sandbox command: %s", args[0])
	}
}

func runSandboxPrune(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(stdout)

	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	result, err := pruneSandboxes(context.Background(), *configPath)
	if err != nil {
		return err
	}
	printSandboxPruneResult(stdout, result)
	return nil
}

func printSandboxPruneResult(stdout *os.File, result lumicli.SandboxPruneResult) {
	if len(result.Containers) == 0 {
		fmt.Fprintln(stdout, "No active Lumi sandbox containers found.")
		return
	}
	fmt.Fprintln(stdout, "Pruned Lumi sandbox containers:")
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "%-18s %-32s %-12s %-19s %-19s %-19s\n", "WORKSPACE", "CONTAINER", "STATUS", "CREATED", "STARTED", "LAST ACTIVE")
	for _, item := range result.Containers {
		fmt.Fprintf(
			stdout,
			"%-18s %-32s %-12s %-19s %-19s %-19s\n",
			blankDash(item.WorkspaceID),
			blankDash(item.ContainerName),
			blankDash(item.Status),
			formatUnixMillis(item.CreatedAt),
			formatUnixMillis(item.StartedAt),
			formatUnixMillis(item.LastActivityAt),
		)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "Total: %d\n", len(result.Containers))
}

func blankDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatUnixMillis(value int64) string {
	if value <= 0 {
		return "-"
	}
	return time.UnixMilli(value).Local().Format("2006-01-02 15:04:05")
}

func runCron(args []string, stdout *os.File, programName string) error {
	if len(args) == 0 {
		printCronUsage(stdout, programName)
		return nil
	}
	switch args[0] {
	case "add":
		return runCronAdd(args[1:], stdout)
	case "list":
		return runCronList(args[1:], stdout)
	case "info":
		return runCronInfo(args[1:], stdout)
	case "edit":
		return runCronEdit(args[1:], stdout)
	case "del", "delete", "rm":
		return runCronDel(args[1:], stdout)
	case "-h", "--help", "help":
		printCronUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown cron command: %s", args[0])
	}
}

func runCronAdd(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("cron add", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cronExpr := fs.String("cron", "", "Cron expression")
	prompt := fs.String("prompt", "", "Agent prompt")
	execCmd := fs.String("exec", "", "Shell command")
	desc := fs.String("desc", "", "Description")
	workDir := fs.String("work-dir", envOrDefault("LUMI_WORKSPACE_PATH", ""), "Working directory")
	sessionMode := fs.String("session-mode", "reuse", "reuse or new-per-run")
	timeoutMins := fs.Int("timeout-mins", 30, "Timeout in minutes, 0 for unlimited")
	mode := fs.String("mode", "", "Agent mode override")
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	channel := fs.String("channel", envOrDefault("LUMI_CHANNEL", "web"), "Channel")
	conversationID := fs.String("conversation-id", envOrDefault("LUMI_CONVERSATION_ID", ""), "Conversation ID")
	agentID := fs.String("agent-id", envOrDefault("LUMI_AGENT_ID", ""), "Agent ID")
	workspaceID := fs.String("workspace-id", envOrDefault("LUMI_WORKSPACE_ID", ""), "Workspace ID")
	wechatConversationKey := fs.String("wechat-conversation-key", envOrDefault("LUMI_WECHAT_CONVERSATION_KEY", ""), "WeChat conversation key")
	wechatContextToken := fs.String("wechat-context-token", envOrDefault("LUMI_WECHAT_CONTEXT_TOKEN", ""), "WeChat context token")
	wecomReqID := fs.String("wecom-req-id", envOrDefault("LUMI_WECOM_REQ_ID", ""), "WeCom request ID")
	wecomChatID := fs.String("wecom-chat-id", envOrDefault("LUMI_WECOM_CHAT_ID", ""), "WeCom chat ID")
	wecomChatType := fs.String("wecom-chat-type", envOrDefault("LUMI_WECOM_CHAT_TYPE", ""), "WeCom chat type")
	wecomUserID := fs.String("wecom-user-id", envOrDefault("LUMI_WECOM_USER_ID", ""), "WeCom user ID")
	silent := fs.Bool("silent", false, "Suppress start notification")
	mute := fs.Bool("mute", false, "Suppress all messages")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(*cronExpr) == "" {
		return errors.New("cron add requires --cron")
	}
	if strings.TrimSpace(*prompt) == "" && strings.TrimSpace(*execCmd) == "" {
		return errors.New("cron add requires --prompt or --exec")
	}
	if strings.TrimSpace(*prompt) != "" && strings.TrimSpace(*execCmd) != "" {
		return errors.New("--prompt and --exec are mutually exclusive")
	}
	name := strings.TrimSpace(*desc)
	if name == "" {
		if strings.TrimSpace(*prompt) != "" {
			name = trimLabel(*prompt)
		} else {
			name = trimLabel(*execCmd)
		}
	}
	payload := cronJobPayload{
		Name:           name,
		Description:    name,
		Prompt:         strings.TrimSpace(*prompt),
		Exec:           strings.TrimSpace(*execCmd),
		AgentID:        strings.TrimSpace(*agentID),
		WorkspaceID:    strings.TrimSpace(*workspaceID),
		ConversationID: strings.TrimSpace(*conversationID),
		Channel:        strings.TrimSpace(*channel),
		Schedule:       &cronSchedulePayload{Type: "cron", CronExpr: strings.TrimSpace(*cronExpr)},
		SessionMode:    normalizeCliSessionMode(*sessionMode),
		WorkDir:        strings.TrimSpace(*workDir),
		Mode:           strings.TrimSpace(*mode),
		TimeoutMins:    timeoutMins,
	}
	if *silent {
		payload.Silent = silent
	}
	if *mute {
		payload.Mute = mute
	}
	if payload.Channel == "wechat" {
		payload.Target = &cronTargetPayload{WeChat: &cronWeChatTargetPayload{
			ConversationKey: strings.TrimSpace(*wechatConversationKey),
			ContextToken:    strings.TrimSpace(*wechatContextToken),
		}}
	}
	if payload.Channel == "wecom" {
		payload.Target = &cronTargetPayload{WeCom: &cronWeComTargetPayload{
			ReqID:    strings.TrimSpace(*wecomReqID),
			ChatID:   strings.TrimSpace(*wecomChatID),
			ChatType: strings.TrimSpace(*wecomChatType),
			UserID:   strings.TrimSpace(*wecomUserID),
		}}
	}
	var result struct {
		Job cronJobPayload `json:"job"`
	}
	if err := cronRequestWithBase(*apiBase, http.MethodPost, "/cron/jobs", nil, payload, &result); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created %s %s next=%s\n", result.Job.ID, result.Job.Name, formatMillis(result.Job.State.NextRunAt))
	return nil
}

func runCronList(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("cron list", flag.ContinueOnError)
	fs.SetOutput(stdout)
	channel := fs.String("channel", envOrDefault("LUMI_CHANNEL", ""), "Channel filter")
	conversationID := fs.String("conversation-id", envOrDefault("LUMI_CONVERSATION_ID", ""), "Conversation filter")
	apiBase := fs.String("api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	query := map[string]string{}
	if strings.TrimSpace(*channel) != "" {
		query["channel"] = strings.TrimSpace(*channel)
	}
	if strings.TrimSpace(*conversationID) != "" {
		query["conversationId"] = strings.TrimSpace(*conversationID)
	}
	var result struct {
		Jobs []cronJobPayload `json:"jobs"`
	}
	if err := cronRequestWithBase(*apiBase, http.MethodGet, "/cron/jobs", query, nil, &result); err != nil {
		return err
	}
	sort.Slice(result.Jobs, func(i, j int) bool {
		return result.Jobs[i].Name < result.Jobs[j].Name
	})
	for _, job := range result.Jobs {
		enabled := "disabled"
		if job.Enabled != nil && *job.Enabled {
			enabled = "enabled"
		}
		cronExpr := ""
		if job.Schedule != nil {
			cronExpr = job.Schedule.CronExpr
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\tnext=%s\n", job.ID, enabled, cronExpr, job.Name, formatMillis(job.State.NextRunAt))
	}
	return nil
}

func runCronInfo(args []string, stdout *os.File) error {
	opts, rest, err := parseCronScopedFlags("cron info", args, stdout)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("cron info requires <job-id>")
	}
	var result struct {
		Job cronJobPayload `json:"job"`
	}
	if err := cronRequestWithBase(opts.apiBase, http.MethodGet, "/cron/jobs/"+rest[0], opts.query(), nil, &result); err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result.Job)
}

func runCronEdit(args []string, stdout *os.File) error {
	opts, rest, err := parseCronScopedFlags("cron edit", args, stdout)
	if err != nil {
		return err
	}
	if len(rest) != 3 {
		return errors.New("cron edit requires <job-id> <field> <value>")
	}
	payload, err := editPayload(rest[1], rest[2])
	if err != nil {
		return err
	}
	var result struct {
		Job cronJobPayload `json:"job"`
	}
	if err := cronRequestWithBase(opts.apiBase, http.MethodPut, "/cron/jobs/"+rest[0], opts.query(), payload, &result); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Updated %s %s\n", result.Job.ID, result.Job.Name)
	return nil
}

func runCronDel(args []string, stdout *os.File) error {
	opts, rest, err := parseCronScopedFlags("cron del", args, stdout)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("cron del requires <job-id>")
	}
	if err := cronRequestWithBase(opts.apiBase, http.MethodDelete, "/cron/jobs/"+rest[0], opts.query(), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Deleted %s\n", rest[0])
	return nil
}

type cronScopedOptions struct {
	apiBase        string
	channel        string
	conversationID string
}

func (o cronScopedOptions) query() map[string]string {
	query := map[string]string{}
	if strings.TrimSpace(o.channel) != "" {
		query["channel"] = strings.TrimSpace(o.channel)
	}
	if strings.TrimSpace(o.conversationID) != "" {
		query["conversationId"] = strings.TrimSpace(o.conversationID)
	}
	return query
}

func parseCronScopedFlags(name string, args []string, stdout *os.File) (cronScopedOptions, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stdout)
	opts := cronScopedOptions{}
	fs.StringVar(&opts.apiBase, "api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	fs.StringVar(&opts.channel, "channel", envOrDefault("LUMI_CHANNEL", ""), "Channel scope")
	fs.StringVar(&opts.conversationID, "conversation-id", envOrDefault("LUMI_CONVERSATION_ID", ""), "Conversation scope")
	flagArgs, rest, err := splitCronScopedArgs(args)
	if err != nil {
		return cronScopedOptions{}, nil, err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return cronScopedOptions{}, nil, err
	}
	return opts, rest, nil
}

func splitCronScopedArgs(args []string) ([]string, []string, error) {
	flagArgs := make([]string, 0)
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasInlineValue := strings.Cut(arg, "=")
		switch name {
		case "--api-base", "-api-base", "--channel", "-channel", "--conversation-id", "-conversation-id":
			if hasInlineValue {
				flagArgs = append(flagArgs, name, value)
				continue
			}
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", arg)
			}
			flagArgs = append(flagArgs, name, args[i+1])
			i++
		default:
			rest = append(rest, arg)
		}
	}
	return flagArgs, rest, nil
}

func editPayload(field, value string) (map[string]any, error) {
	switch field {
	case "cronExpr", "cron", "schedule":
		return map[string]any{"schedule": cronSchedulePayload{Type: "cron", CronExpr: value}}, nil
	case "enabled", "mute", "silent":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, err
		}
		return map[string]any{field: parsed}, nil
	case "timeoutMins", "timeout-mins":
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"timeoutMins": parsed}, nil
	case "sessionMode", "session-mode":
		return map[string]any{"sessionMode": normalizeCliSessionMode(value)}, nil
	case "prompt", "exec", "description", "name", "workDir", "work-dir", "mode":
		key := field
		if key == "work-dir" {
			key = "workDir"
		}
		return map[string]any{key: value}, nil
	default:
		return nil, fmt.Errorf("unsupported cron edit field: %s", field)
	}
}

func cronScopeQuery() map[string]string {
	query := map[string]string{}
	if conversationID := envOrDefault("LUMI_CONVERSATION_ID", ""); conversationID != "" {
		query["conversationId"] = conversationID
	}
	return query
}

func cronRequest(method, path string, query map[string]string, payload any, out any) error {
	return cronRequestWithBase("", method, path, query, payload, out)
}

func cronRequestWithBase(apiBase, method, path string, query map[string]string, payload any, out any) error {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = strings.TrimRight(envOrDefault("LUMI_API_BASE", "http://127.0.0.1:3000/api"), "/")
	}
	body := io.Reader(nil)
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	values := req.URL.Query()
	for k, v := range query {
		if strings.TrimSpace(v) != "" {
			values.Set(k, v)
		}
	}
	req.URL.RawQuery = values.Encode()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cron API %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func trimLabel(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 40 {
		return value
	}
	runes := []rune(value)
	return string(runes[:40])
}

func normalizeCliSessionMode(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "-", "_")
}

func formatMillis(value int64) string {
	if value <= 0 {
		return "-"
	}
	return time.UnixMilli(value).Format(time.RFC3339)
}

func runSetup(args []string, stdin *os.File, stdout *os.File) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stdout)

	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	state, err := lumicli.ResolveConfigState(*configPath)
	if err != nil {
		return err
	}
	createdConfig := !state.Exists
	if !state.Exists {
		if err := lumicli.EnsureConfigFile(state); err != nil {
			return err
		}
	}

	fmt.Fprintf(stdout, "Using config: %s\n", state.Path)
	if createdConfig {
		fmt.Fprintln(stdout, "Created example config from Lumi defaults.")
	}
	fmt.Fprintln(stdout, "Setup checks use agents defined in the current lumi.config.json.")
	status := lumicli.CheckSetup(state)
	printSetupStatus(stdout, status)

	if !hasInstallableItems(status) {
		printAgentGuidance(stdout, state)
		return nil
	}

	reader := bufio.NewReader(stdin)
	confirmed, err := promptYesNo(reader, stdout, "检测到可自动安装的依赖，是否继续安装？", true)
	if err != nil {
		return err
	}
	if !confirmed {
		printAgentGuidance(stdout, state)
		return nil
	}

	result := lumicli.InstallSetup(status, func(event lumicli.SetupInstallEvent) {
		fmt.Fprintf(stdout, "[%s] %s\n", event.Type, event.Message)
	}, func(message string) {
		fmt.Fprintf(stdout, "  %s\n", message)
	})

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "安装完成，当前状态：")
	printSetupStatus(stdout, lumicli.SetupStatus{
		Ready:       result.Success,
		Environment: result.Environment,
		Agents:      result.Agents,
		ACPPackages: result.ACPPackages,
	})
	printAgentGuidance(stdout, state)
	return nil
}

func runWeChat(args []string, stdout, stderr *os.File, programName string) error {
	if len(args) == 0 {
		printWeChatUsage(stdout, programName)
		return nil
	}

	switch args[0] {
	case "run":
		return runWeChatRun(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printWeChatUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown wechat command: %s", args[0])
	}
}

func runWeChatRun(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stdout)

	configPath := fs.String("config", "", "Config file path")
	workspace := fs.String("workspace", envOrDefault("LUMI_WORKSPACE", ""), "Local workspace path")
	kind := fs.String("kind", envOrDefault("LUMI_WORKSPACE_KIND", "local"), "Workspace kind: local or sandbox")
	agentID := fs.String("agent", envOrDefault("LUMI_AGENT", ""), "Default agent ID")
	agentIDs := fs.String("agents", envOrDefault("LUMI_AGENTS", ""), "Comma-separated agent IDs available to this IM workspace; defaults to all configured agents")
	baseURL := fs.String("base-url", envOrDefault("LUMI_WECHAT_BASE_URL", ""), "WeChat login API base URL")
	port := fs.String("port", envOrDefault("LUMI_PORT", "3000"), "Server port")
	idleTimeoutSec := fs.Int("idle-timeout-sec", 0, "Sandbox idle timeout in seconds for IM CLI runs; defaults to 10 years")
	sandboxWarmup := fs.String("sandbox-warmup", envOrDefault("LUMI_SANDBOX_WARMUP", string(sandboxWarmupWait)), "Sandbox warmup mode for sandbox workspaces: wait, async, or off")
	loginTimeoutSec := fs.Int("login-timeout-sec", 300, "WeChat QR login timeout in seconds")
	forceLogin := fs.Bool("force-login", false, "Force WeChat QR login even when saved credentials exist")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	state, err := lumicli.ResolveConfigState(*configPath)
	if err != nil {
		return err
	}
	if !state.Exists || !state.HasAgents {
		return errors.New("no agents configured; run `lumi setup` first, then prepare agents in lumi.config.json")
	}

	if strings.TrimSpace(*workspace) == "" || strings.TrimSpace(*agentID) == "" {
		return errors.New("wechat run requires --workspace and --agent")
	}
	if *loginTimeoutSec <= 0 {
		return errors.New("login timeout sec must be positive")
	}
	warmupMode, err := parseSandboxWarmupMode(*sandboxWarmup)
	if err != nil {
		return err
	}

	loginCtx, stopLoginSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	credentials, reusedLogin, err := resolveWeChatRunCredentials(loginCtx, stdout, strings.TrimSpace(*baseURL), time.Duration(*loginTimeoutSec)*time.Second, *forceLogin)
	stopLoginSignals()
	if err != nil {
		return err
	}

	cfg, workspacePath, err := lumicli.PrepareWeChatRun(state, lumicli.WeChatRunOptions{
		ConfigPath:     *configPath,
		Workspace:      *workspace,
		Kind:           *kind,
		AgentID:        *agentID,
		AgentIDs:       parseAgentIDs(*agentIDs),
		AccountID:      credentials.AccountID,
		BotToken:       credentials.BotToken,
		BaseURL:        credentials.BaseURL,
		Port:           *port,
		IdleTimeoutSec: *idleTimeoutSec,
	})
	if err != nil {
		return err
	}

	runtime, err := startServer(cfg, nil, *port)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Config file: %s\n", state.Path)
	fmt.Fprintf(stdout, "Workspace: %s\n", workspacePath)
	fmt.Fprintf(stdout, "Workspace kind: %s\n", strings.TrimSpace(*kind))
	fmt.Fprintf(stdout, "Default agent: %s\n", *agentID)
	fmt.Fprintf(stdout, "Workspace agents: %s\n", strings.Join(workspaceAgentIDs(cfg), ", "))
	fmt.Fprintf(stdout, "Server: http://localhost:%s\n", runtime.Port())
	if reusedLogin {
		fmt.Fprintf(stdout, "WeChat: using saved account %s\n", credentials.AccountID)
	} else {
		fmt.Fprintf(stdout, "WeChat: enabled for account %s\n", credentials.AccountID)
	}
	fmt.Fprintln(stdout, "Agent credentials are inherited from the current shell environment or existing config env.")

	return serveRuntimeUntilSignal(runtime, stdout, stderr, func() error {
		if strings.TrimSpace(*kind) != "sandbox" {
			return nil
		}
		return startSandboxWarmup(context.Background(), runtime, lumicli.SandboxWorkspaceID, warmupMode, stdout)
	})
}

func resolveWeChatRunCredentials(parent context.Context, stdout *os.File, baseURL string, timeout time.Duration, forceLogin bool) (wechatRunCredentials, bool, error) {
	baseURL = strings.TrimSpace(baseURL)
	store := wechat.NewConfigStore()
	saved, err := store.Load()
	if err != nil {
		return wechatRunCredentials{}, false, err
	}
	if baseURL == "" {
		baseURL = saved.BaseURL
	}
	if !forceLogin && strings.TrimSpace(saved.AccountID) != "" && strings.TrimSpace(saved.BotToken) != "" {
		if baseURL == "" {
			baseURL = saved.BaseURL
		}
		return wechatRunCredentials{
			AccountID: strings.TrimSpace(saved.AccountID),
			BotToken:  strings.TrimSpace(saved.BotToken),
			BaseURL:   baseURL,
		}, true, nil
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	credentials, err := performWeChatQRLogin(ctx, stdout, baseURL)
	return credentials, false, err
}

func performWeChatQRLogin(ctx context.Context, stdout *os.File, baseURL string) (wechatRunCredentials, error) {
	client := newWeChatLoginClient(baseURL)
	qr, err := client.GetQRCode(ctx)
	if err != nil {
		return wechatRunCredentials{}, err
	}
	if err := printTerminalQRCode(stdout, qr.ImageURL); err != nil {
		fmt.Fprintf(stdout, "QR render failed: %v\n", err)
	}
	fmt.Fprintln(stdout, "Scan this WeChat QR code to log in.")
	fmt.Fprintf(stdout, "Ticket: %s\n", qr.Ticket)
	fmt.Fprintf(stdout, "URL: %s\n", qr.ImageURL)

	scanned := false
	for {
		status, err := client.GetQRCodeStatus(ctx, qr.Ticket)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return wechatRunCredentials{}, errors.New("wechat qr login timed out")
			}
			if ctx.Err() != nil {
				return wechatRunCredentials{}, ctx.Err()
			}
			return wechatRunCredentials{}, err
		}
		switch status.Status {
		case "wait", "":
		case "scaned":
			if !scanned {
				fmt.Fprintln(stdout, "Scanned; waiting for confirmation...")
				scanned = true
			}
		case "confirmed":
			accountID := strings.TrimSpace(status.AccountID)
			botToken := strings.TrimSpace(status.BotToken)
			if accountID == "" || botToken == "" {
				return wechatRunCredentials{}, errors.New("wechat qr login confirmed but credentials are incomplete")
			}
			return wechatRunCredentials{
				AccountID: accountID,
				BotToken:  botToken,
				BaseURL:   strings.TrimSpace(status.BaseURL),
			}, nil
		case "expired":
			return wechatRunCredentials{}, errors.New("wechat qr code expired; rerun `lumi wechat run` to scan a new code")
		default:
			return wechatRunCredentials{}, fmt.Errorf("unexpected wechat qr status: %s", status.Status)
		}
	}
}

func printTerminalQRCode(stdout *os.File, value string) error {
	qr, err := qrcode.New(value, qrcode.Medium)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, qr.ToSmallString(false))
	return nil
}

func runWeCom(args []string, _ *os.File, stdout, stderr *os.File, programName string) error {
	if len(args) == 0 {
		printWeComUsage(stdout, programName)
		return nil
	}

	switch args[0] {
	case "run":
		return runWeComRun(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printWeComUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown wecom command: %s", args[0])
	}
}

func runWeComRun(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stdout)

	configPath := fs.String("config", "", "Config file path")
	workspace := fs.String("workspace", envOrDefault("LUMI_WORKSPACE", ""), "Local workspace path")
	kind := fs.String("kind", envOrDefault("LUMI_WORKSPACE_KIND", "local"), "Workspace kind: local or sandbox")
	agentID := fs.String("agent", envOrDefault("LUMI_AGENT", ""), "Default agent ID")
	agentIDs := fs.String("agents", envOrDefault("LUMI_AGENTS", ""), "Comma-separated agent IDs available to this IM workspace; defaults to all configured agents")
	botID := fs.String("bot-id", envOrDefault("LUMI_BOT_ID", ""), "WeCom bot ID")
	botSecret := fs.String("bot-secret", envOrDefault("LUMI_BOT_SECRET", ""), "WeCom bot secret")
	port := fs.String("port", envOrDefault("LUMI_PORT", "3000"), "Server port")
	idleTimeoutSec := fs.Int("idle-timeout-sec", 0, "Sandbox idle timeout in seconds for IM CLI runs; defaults to 10 years")
	sandboxWarmup := fs.String("sandbox-warmup", envOrDefault("LUMI_SANDBOX_WARMUP", string(sandboxWarmupWait)), "Sandbox warmup mode for sandbox workspaces: wait, async, or off")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	state, err := lumicli.ResolveConfigState(*configPath)
	if err != nil {
		return err
	}
	if !state.Exists || !state.HasAgents {
		return errors.New("no agents configured; run `lumi setup` first, then prepare agents in lumi.config.json")
	}

	if strings.TrimSpace(*workspace) == "" || strings.TrimSpace(*agentID) == "" || strings.TrimSpace(*botID) == "" || strings.TrimSpace(*botSecret) == "" {
		return errors.New("wecom run requires --workspace, --agent, --bot-id, and --bot-secret")
	}
	warmupMode, err := parseSandboxWarmupMode(*sandboxWarmup)
	if err != nil {
		return err
	}

	cfg, workspacePath, err := lumicli.PrepareRun(state, lumicli.RunOptions{
		ConfigPath:     *configPath,
		Workspace:      *workspace,
		Kind:           *kind,
		AgentID:        *agentID,
		AgentIDs:       parseAgentIDs(*agentIDs),
		BotID:          *botID,
		BotSecret:      *botSecret,
		Port:           *port,
		IdleTimeoutSec: *idleTimeoutSec,
	})
	if err != nil {
		return err
	}

	runtime, err := startServer(cfg, nil, *port)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Config file: %s\n", state.Path)
	fmt.Fprintf(stdout, "Workspace: %s\n", workspacePath)
	fmt.Fprintf(stdout, "Workspace kind: %s\n", strings.TrimSpace(*kind))
	fmt.Fprintf(stdout, "Default agent: %s\n", *agentID)
	fmt.Fprintf(stdout, "Workspace agents: %s\n", strings.Join(workspaceAgentIDs(cfg), ", "))
	fmt.Fprintf(stdout, "Server: http://localhost:%s\n", runtime.Port())
	fmt.Fprintf(stdout, "WeCom: enabled for bot %s\n", *botID)
	fmt.Fprintln(stdout, "Agent credentials are inherited from the current shell environment or existing config env.")

	return serveRuntimeUntilSignal(runtime, stdout, stderr, func() error {
		if strings.TrimSpace(*kind) != "sandbox" {
			return nil
		}
		return startSandboxWarmup(context.Background(), runtime, lumicli.SandboxWorkspaceID, warmupMode, stdout)
	})
}

func parseSandboxWarmupMode(value string) (sandboxWarmupMode, error) {
	switch sandboxWarmupMode(strings.TrimSpace(value)) {
	case "", sandboxWarmupWait:
		return sandboxWarmupWait, nil
	case sandboxWarmupAsync:
		return sandboxWarmupAsync, nil
	case sandboxWarmupOff:
		return sandboxWarmupOff, nil
	default:
		return "", errors.New("sandbox warmup mode must be async, wait, or off")
	}
}

func startSandboxWarmup(ctx context.Context, runtime serverRuntime, workspaceID string, mode sandboxWarmupMode, stdout *os.File) error {
	if mode == sandboxWarmupOff {
		fmt.Fprintln(stdout, "Sandbox: warmup disabled")
		return nil
	}
	fmt.Fprintln(stdout, "Sandbox: warming up")
	if mode == sandboxWarmupAsync {
		go func() {
			state, err := runtime.WarmupSandbox(context.Background(), workspaceID)
			if err != nil {
				fmt.Fprintf(stdout, "Sandbox: warmup failed: %v\n", err)
				return
			}
			if state.Status == "failed" {
				fmt.Fprintf(stdout, "Sandbox: warmup failed: %s\n", firstNonEmpty(state.ErrorMessage, state.ErrorCode, "unknown"))
				return
			}
			fmt.Fprintf(stdout, "Sandbox: %s\n", firstNonEmpty(sandboxProgressLabel(state), "starting"))
		}()
		return nil
	}

	state, err := runtime.WarmupSandbox(ctx, workspaceID)
	if err != nil {
		return err
	}
	if err := printSandboxWarmupProgress(ctx, runtime, workspaceID, state, stdout); err != nil {
		return err
	}
	return nil
}

func printSandboxWarmupProgress(ctx context.Context, runtime serverRuntime, workspaceID string, initial lumicli.SandboxWarmupState, stdout *os.File) error {
	last := ""
	state := initial
	for {
		label := sandboxProgressLabel(state)
		if label != "" && label != last {
			fmt.Fprintf(stdout, "Sandbox: %s\n", label)
			last = label
		}
		switch state.Status {
		case "running":
			return nil
		case "failed":
			return fmt.Errorf("sandbox warmup failed: %s", firstNonEmpty(state.ErrorMessage, state.ErrorCode, "unknown"))
		}

		timer := time.NewTimer(1500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		next, err := runtime.SandboxStatus(workspaceID)
		if err != nil {
			return err
		}
		state = next
	}
}

func sandboxProgressLabel(state lumicli.SandboxWarmupState) string {
	switch strings.TrimSpace(state.Stage) {
	case "checking_docker":
		return "checking Docker"
	case "preparing_image":
		return "preparing image"
	case "starting_container":
		return "starting container"
	case "connecting_executor":
		return "connecting executor"
	case "bootstrapping_runtime":
		return "preparing runtime"
	}
	switch strings.TrimSpace(state.Status) {
	case "pending":
		return "starting"
	case "running":
		return "ready"
	case "failed":
		return "failed"
	default:
		return strings.TrimSpace(state.Status)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func serveRuntimeUntilSignal(runtime serverRuntime, stdout, stderr *os.File, afterListen func() error) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.ListenAndServe()
	}()
	if afterListen != nil {
		if err := afterListen(); err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = runtime.ShutdownWithContext(ctx)
			cancel()
			return err
		}
	}

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	case <-sigCh:
		fmt.Fprintln(stdout, "\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		shutdownDone := make(chan error, 1)
		go func() {
			shutdownDone <- runtime.ShutdownWithContext(ctx)
		}()

		select {
		case err := <-shutdownDone:
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				fmt.Fprintln(stderr, "Shutdown timed out; forcing exit.")
				return nil
			}
			return err
		case <-sigCh:
			fmt.Fprintln(stderr, "Forced shutdown.")
			return nil
		case <-ctx.Done():
			fmt.Fprintln(stderr, "Shutdown timed out; forcing exit.")
			return nil
		}
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func printUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s cron <command> [flags]\n", programName)
	fmt.Fprintf(stdout, "  %s sandbox <command> [flags]\n", programName)
	fmt.Fprintf(stdout, "  %s setup [flags]\n", programName)
	fmt.Fprintf(stdout, "  %s wechat <command> [flags]\n", programName)
	fmt.Fprintf(stdout, "  %s wecom <command> [flags]\n", programName)
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "setup checks and optionally installs runtime dependencies. It does not create agents or manage API keys.")
}

func printCronUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s cron add --cron \"0 8 * * *\" (--prompt <text> | --exec <command>) --desc <label> [flags]\n", programName)
	fmt.Fprintf(stdout, "  %s cron list\n", programName)
	fmt.Fprintf(stdout, "  %s cron info <job-id>\n", programName)
	fmt.Fprintf(stdout, "  %s cron edit <job-id> <field> <value>\n", programName)
	fmt.Fprintf(stdout, "  %s cron del <job-id>\n", programName)
}

func printWeComUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s wecom run --workspace <path> --kind local|sandbox --agent <id> --bot-id <id> --bot-secret <secret> [--idle-timeout-sec <seconds>] [flags]\n", programName)
}

func printWeChatUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s wechat run --workspace <path> --kind local|sandbox --agent <id> [--base-url <url>] [--force-login] [--login-timeout-sec <seconds>] [--idle-timeout-sec <seconds>] [flags]\n", programName)
}

func printSandboxUsage(stdout *os.File, programName string) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintf(stdout, "  %s sandbox prune [--config <path>]\n", programName)
}

func printSetupStatus(stdout *os.File, status lumicli.SetupStatus) {
	printSetupSection(stdout, "Environment", status.Environment)
	printSetupSection(stdout, "Agents", status.Agents)
	printSetupSection(stdout, "ACP Packages", status.ACPPackages)
}

func printSetupSection(stdout *os.File, title string, items []lumicli.SetupDependencyItem) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(stdout, "\n%s:\n", title)
	for _, item := range items {
		detail := item.Command
		if detail == "" {
			detail = item.Package
		}
		if detail != "" {
			fmt.Fprintf(stdout, "  - %s (%s): %s\n", item.Name, detail, item.Message)
		} else {
			fmt.Fprintf(stdout, "  - %s: %s\n", item.Name, item.Message)
		}
		if item.Install != "" && item.Status != "ready" {
			fmt.Fprintf(stdout, "    install: %s\n", item.Install)
		}
	}
}

func hasInstallableItems(status lumicli.SetupStatus) bool {
	for _, item := range status.Agents {
		if item.Status == "missing" {
			return true
		}
	}
	for _, item := range status.ACPPackages {
		if item.Status == "not_installed" {
			return true
		}
	}
	return false
}

func printAgentGuidance(stdout *os.File, state *lumicli.ConfigState) {
	fmt.Fprintln(stdout, "")
	if !state.HasAgents {
		fmt.Fprintln(stdout, "lumi.config.json 中还没有 agent 配置。")
		fmt.Fprintln(stdout, "请手动准备 agents/defaultAgent；lumi setup 只负责 /setup 的依赖检查和安装。")
		fmt.Fprintln(stdout, "Agent 运行时会继承当前 shell 环境变量。")
		return
	}
	fmt.Fprintf(stdout, "可用 agents: %s\n", strings.Join(lumicli.AgentIDs(state), ", "))
	fmt.Fprintln(stdout, "lumi wechat run / lumi wecom run 会直接复用这些 agent 定义。")
}

func parseAgentIDs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func workspaceAgentIDs(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	workspaceID := cfg.DefaultWorkspace
	if workspaceID == "" {
		return nil
	}
	workspace := cfg.FindWorkspace(workspaceID)
	if workspace == nil {
		return nil
	}
	return append([]string(nil), workspace.Agents...)
}

func promptYesNo(reader *bufio.Reader, stdout *os.File, label string, defaultYes bool) (bool, error) {
	suffix := "y/N"
	if defaultYes {
		suffix = "Y/n"
	}
	for {
		fmt.Fprintf(stdout, "%s [%s]: ", label, suffix)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false, err
		}
		answer := strings.TrimSpace(strings.ToLower(line))
		if answer == "" {
			return defaultYes, nil
		}
		if answer == "y" || answer == "yes" {
			return true, nil
		}
		if answer == "n" || answer == "no" {
			return false, nil
		}
	}
}
