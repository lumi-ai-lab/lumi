package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

type connectOptions struct {
	Server     string
	Token      string
	ConfigPath string
	SkipSetup  bool
	Install    bool
}

type setupOptions struct {
	ConfigPath string
	Install    bool
}

func main() {
	if len(os.Args) < 2 || isHelpArg(os.Args[1]) {
		fmt.Println(usage())
		return
	}

	switch os.Args[1] {
	case "connect":
		if err := runConnect(os.Args[2:]); err != nil {
			code := 1
			if errors.Is(err, errUsage) {
				code = 2
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(code)
		}
	case "setup":
		if err := runSetupCommand(os.Args[2:]); err != nil {
			code := 1
			if errors.Is(err, errUsage) {
				code = 2
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(code)
		}
	default:
		fmt.Fprintf(os.Stderr, "unsupported command %q\n%s\n", os.Args[1], usage())
		os.Exit(2)
	}
}

var errUsage = errors.New("usage error")

func isHelpArg(arg string) bool {
	switch arg {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func runConnect(args []string) error {
	opts, err := parseConnectArgs(args)
	if err != nil {
		return err
	}

	cfg, err := LoadOrCreateConfig(opts.ConfigPath)
	if err != nil {
		return err
	}

	if opts.Install {
		status := runSetupCheck(cfg)
		if !status.Ready {
			if err := installSetupDependencies(status); err != nil {
				return err
			}
		}
	} else if !opts.SkipSetup {
		status := runSetupCheck(cfg)
		if !status.Ready {
			printSetupStatus(status)
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "Device setup is not ready. Fix the items above, or run:")
			fmt.Fprintf(os.Stderr, "  %s setup --install", os.Args[0])
			if opts.ConfigPath != "" {
				fmt.Fprintf(os.Stderr, " --config %s", opts.ConfigPath)
			}
			fmt.Fprintln(os.Stderr)
			return errors.New("device setup is not ready")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := NewClient(opts.Server, opts.Token, cfg)
	if err := client.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runSetupCommand(args []string) error {
	opts, err := parseSetupArgs(args)
	if err != nil {
		return err
	}

	cfg, err := LoadOrCreateConfig(opts.ConfigPath)
	if err != nil {
		return err
	}

	status := runSetupCheck(cfg)
	printSetupStatus(status)
	if status.Ready {
		fmt.Println()
		fmt.Println("Device setup is ready.")
		return nil
	}

	if !opts.Install {
		fmt.Println()
		fmt.Println("Device setup is not ready. Run with --install to install npm-based dependencies.")
		return errors.New("device setup is not ready")
	}

	if err := installSetupDependencies(status); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Rechecking setup...")
	status = runSetupCheck(cfg)
	printSetupStatus(status)
	if !status.Ready {
		return errors.New("device setup is still not ready")
	}

	fmt.Println()
	fmt.Println("Device setup is ready.")
	return nil
}

func parseConnectArgs(args []string) (connectOptions, error) {
	var opts connectOptions

	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Server, "server", "", "Lumi backend base URL")
	fs.StringVar(&opts.Token, "token", "", "device secret")
	fs.StringVar(&opts.ConfigPath, "config", "", "executor config file path")
	fs.BoolVar(&opts.SkipSetup, "skip-setup", false, "connect without local setup preflight")
	fs.BoolVar(&opts.Install, "install", false, "install missing npm-based dependencies before connecting")

	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("%w: %v", errUsage, err)
	}
	if opts.Server == "" {
		return opts, fmt.Errorf("%w: --server is required", errUsage)
	}
	if opts.Token == "" {
		return opts, fmt.Errorf("%w: --token is required", errUsage)
	}
	if len(fs.Args()) > 0 {
		return opts, fmt.Errorf("%w: unexpected arguments: %v", errUsage, fs.Args())
	}
	return opts, nil
}

func parseSetupArgs(args []string) (setupOptions, error) {
	var opts setupOptions

	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.ConfigPath, "config", "", "executor config file path")
	fs.BoolVar(&opts.Install, "install", false, "install missing npm-based dependencies")

	if err := fs.Parse(args); err != nil {
		return opts, fmt.Errorf("%w: %v", errUsage, err)
	}
	if len(fs.Args()) > 0 {
		return opts, fmt.Errorf("%w: unexpected arguments: %v", errUsage, fs.Args())
	}
	return opts, nil
}

func usage() string {
	return "usage:\n  device-executor setup [--install] [--config <path>]\n  device-executor connect --server <url> --token <token> [--config <path>] [--install] [--skip-setup]"
}
