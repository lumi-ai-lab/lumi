package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pengmide/lumi/pkg/lumicli"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdin *os.File, stdout, stderr *os.File) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "setup":
		return runSetup(args[1:], stdin, stdout)
	case "wecom":
		return runWeCom(args[1:], stdin, stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runSetup(args []string, stdin *os.File, stdout *os.File) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stdout)

	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
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

func runWeCom(args []string, _ *os.File, stdout, stderr *os.File) error {
	if len(args) == 0 {
		printWeComUsage(stdout)
		return nil
	}

	switch args[0] {
	case "run":
		return runWeComRun(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printWeComUsage(stdout)
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
	agentID := fs.String("agent", envOrDefault("LUMI_AGENT", ""), "Configured agent ID")
	botID := fs.String("bot-id", envOrDefault("LUMI_BOT_ID", ""), "WeCom bot ID")
	botSecret := fs.String("bot-secret", envOrDefault("LUMI_BOT_SECRET", ""), "WeCom bot secret")
	port := fs.String("port", envOrDefault("LUMI_PORT", "3000"), "Server port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	state, err := lumicli.ResolveConfigState(*configPath)
	if err != nil {
		return err
	}
	if !state.Exists || !state.HasAgents {
		return errors.New("no agents configured; run `lumi-cli setup` first, then prepare agents in lumi.config.json")
	}

	if strings.TrimSpace(*workspace) == "" || strings.TrimSpace(*agentID) == "" || strings.TrimSpace(*botID) == "" || strings.TrimSpace(*botSecret) == "" {
		return errors.New("wecom run requires --workspace, --agent, --bot-id, and --bot-secret")
	}

	cfg, workspacePath, err := lumicli.PrepareRun(state, lumicli.RunOptions{
		ConfigPath: *configPath,
		Workspace:  *workspace,
		AgentID:    *agentID,
		BotID:      *botID,
		BotSecret:  *botSecret,
		Port:       *port,
	})
	if err != nil {
		return err
	}

	runtime, err := lumicli.StartServer(cfg, nil, *port)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Config file: %s\n", state.Path)
	fmt.Fprintf(stdout, "Workspace: %s\n", workspacePath)
	fmt.Fprintf(stdout, "Agent: %s\n", *agentID)
	fmt.Fprintf(stdout, "Server: http://localhost:%s\n", runtime.Port())
	fmt.Fprintf(stdout, "WeCom: enabled for bot %s\n", *botID)
	fmt.Fprintln(stdout, "Agent credentials are inherited from the current shell environment or existing config env.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.ListenAndServe()
	}()

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

func printUsage(stdout *os.File) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  lumi-cli setup [flags]")
	fmt.Fprintln(stdout, "  lumi-cli wecom <command> [flags]")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "setup checks and optionally installs runtime dependencies. It does not create agents or manage API keys.")
}

func printWeComUsage(stdout *os.File) {
	fmt.Fprintln(stdout, "Usage:")
	fmt.Fprintln(stdout, "  lumi-cli wecom run --workspace <path> --agent <id> --bot-id <id> --bot-secret <secret> [flags]")
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
		fmt.Fprintln(stdout, "请手动准备 agents/defaultAgent；lumi-cli setup 只负责 /setup 的依赖检查和安装。")
		fmt.Fprintln(stdout, "Agent 运行时会继承当前 shell 环境变量。")
		return
	}
	fmt.Fprintf(stdout, "可用 agents: %s\n", strings.Join(lumicli.AgentIDs(state), ", "))
	fmt.Fprintln(stdout, "lumi-cli wecom run 会直接复用这些 agent 定义。")
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
