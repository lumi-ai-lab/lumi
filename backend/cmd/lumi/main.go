package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pengmide/lumi/internal/api"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/pkg/lumicmd"
	"github.com/pengmide/lumi/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "server":
			return runServer(args[1:])
		case "cron", "sandbox", "setup", "wecom":
			return lumicmd.Run(args, os.Stdin, os.Stdout, os.Stderr)
		case "help", "-h", "--help":
			printUsage()
			return nil
		}
		if !strings.HasPrefix(args[0], "-") {
			return fmt.Errorf("unknown command: %s", args[0])
		}
	}
	return runServer(args)
}

func runServer(args []string) error {
	serverFlags := flag.NewFlagSet("server", flag.ContinueOnError)
	serverFlags.SetOutput(os.Stdout)
	var (
		configPath = serverFlags.String("config", "", "Config file path")
		port       = serverFlags.String("port", "3000", "Server port")
		webDir     = serverFlags.String("web", "", "Web directory (overrides embedded)")
	)
	if err := serverFlags.Parse(args); err != nil {
		return err
	}
	if serverFlags.NArg() > 0 {
		return fmt.Errorf("unexpected server argument: %s", serverFlags.Arg(0))
	}

	// Ensure config exists (copy example if needed)
	if err := config.EnsureConfigExists(); err != nil {
		fmt.Printf("⚠️  Config initialization: %v\n", err)
	}

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Print startup info
	printStartupInfo(cfg, config.LoadedConfigPath)

	// Determine static files source
	var staticFS fs.FS
	if *webDir != "" {
		staticFS = os.DirFS(*webDir)
		fmt.Printf("   Web directory: %s\n\n", *webDir)
	} else {
		var err error
		staticFS, err = web.FS()
		if err == nil {
			fmt.Println("   Web: embedded files")
			fmt.Println()
		} else {
			fmt.Println("   Web: no files available")
			fmt.Println()
		}
	}

	// Create server
	server := api.NewServer(cfg, staticFS)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		done := make(chan error, 1)
		go func() {
			done <- server.Shutdown()
		}()

		select {
		case err := <-done:
			if err != nil {
				fmt.Fprintf(os.Stderr, "Shutdown error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		case <-sigCh:
			fmt.Fprintln(os.Stderr, "Forced shutdown.")
			os.Exit(130)
		case <-time.After(10 * time.Second):
			fmt.Fprintln(os.Stderr, "Shutdown timed out; forcing exit.")
			os.Exit(1)
		}
	}()

	// Start server
	printServerBanner(*port)
	addr := ":" + *port
	if err := server.ListenAndServe(addr); err != nil {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

func printUsage() {
	fmt.Fprintln(os.Stdout, "Usage:")
	fmt.Fprintln(os.Stdout, "  lumi [--config <path>] [--port <port>] [--web <dir>]")
	fmt.Fprintln(os.Stdout, "  lumi server [--config <path>] [--port <port>] [--web <dir>]")
	fmt.Fprintln(os.Stdout, "  lumi cron <command> [flags]")
	fmt.Fprintln(os.Stdout, "  lumi sandbox <command> [flags]")
	fmt.Fprintln(os.Stdout, "  lumi setup [flags]")
	fmt.Fprintln(os.Stdout, "  lumi wecom <command> [flags]")
}

func printStartupInfo(cfg *config.Config, configPath string) {
	fmt.Println("\n📋 Configuration")
	fmt.Println(strings.Repeat("─", 50))
	if configPath != "" {
		fmt.Printf("   Config file: %s\n", configPath)
	} else {
		fmt.Println("   Config file: (using defaults)")
	}
	fmt.Printf("   Default agent: %s\n", cfg.DefaultAgent)
	if cfg.DefaultWorkspace != "" {
		fmt.Printf("   Default workspace: %s\n", cfg.DefaultWorkspace)
	}
	fmt.Println()

	fmt.Println("📦 Agents")
	fmt.Println(strings.Repeat("─", 50))
	for _, agent := range cfg.Agents {
		isDefault := ""
		if agent.ID == cfg.DefaultAgent {
			isDefault = " (default)"
		}
		permission := getPermissionLabel(agent.PermissionMode)
		fmt.Printf("   %s%s\n", agent.Name, isDefault)
		fmt.Printf("     ID: %s\n", agent.ID)
		fmt.Printf("     Permission: %s\n", permission)
		fmt.Printf("     Command: %s %s\n", agent.Command, strings.Join(agent.Args, " "))
		fmt.Println()
	}

	if len(cfg.Workspaces) > 0 {
		fmt.Println("📁 Workspaces")
		fmt.Println(strings.Repeat("─", 50))
		for _, ws := range cfg.Workspaces {
			isDefault := ""
			if ws.ID == cfg.DefaultWorkspace {
				isDefault = " (default)"
			}
			fmt.Printf("   %s%s\n", ws.Name, isDefault)
			fmt.Printf("     Path: %s\n", ws.Path)
			fmt.Println()
		}
	}
}

func getPermissionLabel(mode string) string {
	switch mode {
	case "", "default":
		return "User Confirmation"
	case "bypass":
		return "Auto Approve"
	default:
		return mode
	}
}

func printServerBanner(port string) {
	fmt.Printf(`
╔════════════════════════════════════════════════╗
║            Lumi Web Interface                  ║
╠════════════════════════════════════════════════╣
║  Open http://localhost:%s in your browser   ║
║  Press Ctrl+C to stop                          ║
╚════════════════════════════════════════════════╝

`, port)
}
