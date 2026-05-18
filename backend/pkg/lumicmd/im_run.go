package lumicmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	imRunPollInterval = 200 * time.Millisecond
	imRunStableChecks = 2
)

type repeatedStrings []string

func (v *repeatedStrings) String() string {
	return strings.Join(*v, ",")
}

func (v *repeatedStrings) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value must not be empty")
	}
	*v = append(*v, value)
	return nil
}

type imRunOptions struct {
	apiBase        string
	channel        string
	workspaceID    string
	workspacePath  string
	conversationID string
	wecomReqID     string
	wecomChatID    string
	wecomChatType  string
	wecomUserID    string
	caption        string
	shellCommand   string
	cleanup        bool
	timeout        time.Duration
	imageOut       repeatedStrings
	fileOut        repeatedStrings
	sendImages     repeatedStrings
	sendFiles      repeatedStrings
}

type imRunWatch struct {
	Type    string
	Path    string
	Cleanup bool
	sent    bool
}

type imSendPayload struct {
	Channel        string             `json:"channel"`
	Type           string             `json:"type"`
	Text           string             `json:"text,omitempty"`
	Path           string             `json:"path,omitempty"`
	Caption        string             `json:"caption,omitempty"`
	WorkspaceID    string             `json:"workspaceId,omitempty"`
	WorkspacePath  string             `json:"workspacePath,omitempty"`
	ConversationID string             `json:"conversationId,omitempty"`
	WeCom          imSendWeComPayload `json:"wecom,omitempty"`
}

type imSendWeComPayload struct {
	ReqID    string `json:"reqId,omitempty"`
	ChatID   string `json:"chatId,omitempty"`
	ChatType string `json:"chatType,omitempty"`
	UserID   string `json:"userId,omitempty"`
}

func runIM(args []string, stdout, stderr *os.File, programName string) error {
	if len(args) == 0 {
		printIMUsage(stdout, programName)
		return nil
	}
	switch args[0] {
	case "run":
		return runIMRun(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printIMUsage(stdout, programName)
		return nil
	default:
		return fmt.Errorf("unknown im command: %s", args[0])
	}
}

func runIMRun(args []string, stdout, stderr *os.File) error {
	opts, commandArgs, err := parseIMRunFlags(args, stdout)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := validateIMRunOptions(opts, commandArgs); err != nil {
		return err
	}

	watches, envAdditions, err := prepareIMRunWatches(opts)
	if err != nil {
		return err
	}
	if opts.cleanup {
		defer cleanupIMRunWatches(watches, stderr)
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if opts.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.timeout)
		defer cancel()
	}

	cmd := buildIMRunCommand(ctx, opts, commandArgs)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), envAdditions...)

	if err := cmd.Start(); err != nil {
		return err
	}

	waitCh := make(chan error, 1)
	processDone := make(chan struct{})
	go func() {
		waitCh <- cmd.Wait()
		close(processDone)
	}()

	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		watchAndSendIMFiles(ctx, opts, watches, stderr, processDone)
	}()

	waitErr := <-waitCh
	<-sendDone

	if ctx.Err() == context.DeadlineExceeded {
		return ExitError{Code: 124, Err: errors.New("lumi im run timed out")}
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return ExitError{Code: exitErr.ExitCode(), Err: waitErr}
		}
		return waitErr
	}
	return nil
}

func parseIMRunFlags(args []string, stdout *os.File) (imRunOptions, []string, error) {
	opts := imRunOptions{}
	fs := flag.NewFlagSet("im run", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&opts.apiBase, "api-base", envOrDefault("LUMI_API_BASE", ""), "Lumi API base URL")
	fs.StringVar(&opts.channel, "channel", envOrDefault("LUMI_CHANNEL", ""), "IM channel")
	fs.StringVar(&opts.workspaceID, "workspace-id", envOrDefault("LUMI_WORKSPACE_ID", ""), "Workspace ID")
	fs.StringVar(&opts.workspacePath, "workspace-path", envOrDefault("LUMI_WORKSPACE_PATH", ""), "Workspace path")
	fs.StringVar(&opts.conversationID, "conversation-id", envOrDefault("LUMI_CONVERSATION_ID", ""), "Conversation ID")
	fs.StringVar(&opts.wecomReqID, "wecom-req-id", envOrDefault("LUMI_WECOM_REQ_ID", ""), "WeCom request ID")
	fs.StringVar(&opts.wecomChatID, "wecom-chat-id", envOrDefault("LUMI_WECOM_CHAT_ID", ""), "WeCom chat ID")
	fs.StringVar(&opts.wecomChatType, "wecom-chat-type", envOrDefault("LUMI_WECOM_CHAT_TYPE", ""), "WeCom chat type")
	fs.StringVar(&opts.wecomUserID, "wecom-user-id", envOrDefault("LUMI_WECOM_USER_ID", ""), "WeCom user ID")
	fs.StringVar(&opts.caption, "caption", "", "Caption sent before the first media")
	fs.StringVar(&opts.shellCommand, "sh", "", "Shell command to run")
	fs.BoolVar(&opts.cleanup, "cleanup", false, "Delete generated output files after the command exits")
	fs.DurationVar(&opts.timeout, "timeout", 0, "Overall timeout")
	fs.Var(&opts.imageOut, "image-out", "Environment variable for an auto-generated image path")
	fs.Var(&opts.fileOut, "file-out", "Environment variable for an auto-generated file path")
	fs.Var(&opts.sendImages, "send-image", "Existing image path to watch and send")
	fs.Var(&opts.sendFiles, "send-file", "Existing file path to watch and send")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, flag.ErrHelp
		}
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func validateIMRunOptions(opts imRunOptions, commandArgs []string) error {
	if strings.TrimSpace(opts.apiBase) == "" {
		return errors.New("lumi im run requires LUMI_API_BASE or --api-base")
	}
	if strings.TrimSpace(opts.channel) != "wecom" {
		return errors.New("lumi im run requires LUMI_CHANNEL=wecom or --channel wecom")
	}
	if strings.TrimSpace(opts.workspacePath) == "" {
		return errors.New("lumi im run requires LUMI_WORKSPACE_PATH or --workspace-path")
	}
	if strings.TrimSpace(opts.wecomChatID) == "" && strings.TrimSpace(opts.wecomReqID) == "" {
		return errors.New("lumi im run requires LUMI_WECOM_CHAT_ID or LUMI_WECOM_REQ_ID in IM context")
	}
	if strings.TrimSpace(opts.shellCommand) != "" && len(commandArgs) > 0 {
		return errors.New("--sh and -- <command> are mutually exclusive")
	}
	if strings.TrimSpace(opts.shellCommand) == "" && len(commandArgs) == 0 {
		return errors.New("lumi im run requires --sh <command> or -- <command> [args...]")
	}
	if err := validateEnvNames(opts.imageOut); err != nil {
		return fmt.Errorf("--image-out: %w", err)
	}
	if err := validateEnvNames(opts.fileOut); err != nil {
		return fmt.Errorf("--file-out: %w", err)
	}
	return nil
}

func validateEnvNames(values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			return errors.New("environment variable name is required")
		}
		for i, r := range value {
			valid := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9'
			if !valid || i == 0 && r >= '0' && r <= '9' {
				return fmt.Errorf("invalid environment variable name %q", value)
			}
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("duplicate environment variable name %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func prepareIMRunWatches(opts imRunOptions) ([]*imRunWatch, []string, error) {
	watches := make([]*imRunWatch, 0, len(opts.imageOut)+len(opts.fileOut)+len(opts.sendImages)+len(opts.sendFiles))
	envAdditions := make([]string, 0, len(opts.imageOut)+len(opts.fileOut))
	for _, name := range opts.imageOut {
		path, err := generatedIMRunPath(opts.workspacePath, name, ".png")
		if err != nil {
			return nil, nil, err
		}
		envAdditions = append(envAdditions, name+"="+path)
		watches = append(watches, &imRunWatch{Type: "image", Path: path, Cleanup: true})
	}
	for _, name := range opts.fileOut {
		path, err := generatedIMRunPath(opts.workspacePath, name, "")
		if err != nil {
			return nil, nil, err
		}
		envAdditions = append(envAdditions, name+"="+path)
		watches = append(watches, &imRunWatch{Type: "file", Path: path, Cleanup: true})
	}
	for _, path := range opts.sendImages {
		watches = append(watches, &imRunWatch{Type: "image", Path: path})
	}
	for _, path := range opts.sendFiles {
		watches = append(watches, &imRunWatch{Type: "file", Path: path})
	}
	return watches, envAdditions, nil
}

func generatedIMRunPath(workspacePath, envName, ext string) (string, error) {
	dir := filepath.Join(workspacePath, ".lumi", "im-run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := strings.ToLower(strings.ReplaceAll(envName, "_", "-"))
	return filepath.Join(dir, fmt.Sprintf("%s-%d%s", name, time.Now().UnixNano(), ext)), nil
}

func buildIMRunCommand(ctx context.Context, opts imRunOptions, commandArgs []string) *exec.Cmd {
	if strings.TrimSpace(opts.shellCommand) != "" {
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd", "/C", opts.shellCommand)
		}
		return exec.CommandContext(ctx, "sh", "-c", opts.shellCommand)
	}
	return exec.CommandContext(ctx, commandArgs[0], commandArgs[1:]...)
}

func watchAndSendIMFiles(ctx context.Context, opts imRunOptions, watches []*imRunWatch, stderr io.Writer, done <-chan struct{}) {
	if len(watches) == 0 {
		return
	}
	ticker := time.NewTicker(imRunPollInterval)
	defer ticker.Stop()
	captionSent := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			for _, watch := range watches {
				if !watch.sent {
					trySendIMWatch(ctx, opts, watch, stderr, &captionSent)
				}
			}
			return
		case <-ticker.C:
			allSent := true
			for _, watch := range watches {
				if watch.sent {
					continue
				}
				allSent = false
				trySendIMWatch(ctx, opts, watch, stderr, &captionSent)
			}
			if allSent {
				return
			}
		}
	}
}

func trySendIMWatch(ctx context.Context, opts imRunOptions, watch *imRunWatch, stderr io.Writer, captionSent *bool) {
	if !fileLooksStable(watch.Path) {
		return
	}
	caption := ""
	if captionSent != nil && !*captionSent {
		caption = opts.caption
	}
	payload := imSendPayload{
		Channel:        opts.channel,
		Type:           watch.Type,
		Path:           watch.Path,
		Caption:        caption,
		WorkspaceID:    opts.workspaceID,
		WorkspacePath:  opts.workspacePath,
		ConversationID: opts.conversationID,
		WeCom: imSendWeComPayload{
			ReqID:    opts.wecomReqID,
			ChatID:   opts.wecomChatID,
			ChatType: opts.wecomChatType,
			UserID:   opts.wecomUserID,
		},
	}
	if err := apiRequestWithBase(opts.apiBase, httpMethodPost, "/im/send", nil, payload, nil); err != nil {
		fmt.Fprintf(stderr, "[lumi im] send %s failed: %v\n", watch.Path, err)
		return
	}
	watch.sent = true
	if captionSent != nil && caption != "" {
		*captionSent = true
	}
	fmt.Fprintf(stderr, "[lumi im] sent %s: %s\n", watch.Type, watch.Path)
}

const httpMethodPost = "POST"

func fileLooksStable(path string) bool {
	var previous os.FileInfo
	for i := 0; i < imRunStableChecks; i++ {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return false
		}
		if previous != nil && (info.Size() != previous.Size() || !info.ModTime().Equal(previous.ModTime())) {
			return false
		}
		previous = info
		if i+1 < imRunStableChecks {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return true
}

func cleanupIMRunWatches(watches []*imRunWatch, stderr io.Writer) {
	for _, watch := range watches {
		if !watch.Cleanup {
			continue
		}
		if err := os.Remove(watch.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "[lumi im] cleanup %s failed: %v\n", watch.Path, err)
		}
	}
}

func printIMUsage(stdout *os.File, programName string) {
	fmt.Fprintf(stdout, `Usage:
  %s im run [options] --sh '<shell command>'
  %s im run [options] -- <command> [args...]

Options:
  --image-out NAME       Generate an image output path and export it as NAME
  --file-out NAME        Generate a file output path and export it as NAME
  --send-image PATH      Watch and send an existing image path
  --send-file PATH       Watch and send an existing file path
  --caption TEXT         Send text before the first media
  --cleanup              Remove generated output files after the command exits
  --timeout DURATION     Overall timeout
`, programName, programName)
}
