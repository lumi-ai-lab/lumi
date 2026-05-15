package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pengmide/lumi/internal/setupcheck"
)

var agentNpmPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
	"qwen":   "@qwen-code/qwen-code",
}

func printSetupStatus(status setupcheck.SetupStatus) {
	fmt.Println("Device setup check")
	fmt.Println()
	printSetupSection("Environment", status.Environment)
	printSetupSection("Agent CLI", status.Agents)
	printSetupSection("ACP Packages", status.ACPPackages)
}

func printSetupSection(title string, items []setupcheck.DependencyItem) {
	if len(items) == 0 {
		return
	}

	fmt.Println(title + ":")
	for _, item := range items {
		detail := item.Command
		if detail == "" {
			detail = item.Package
		}
		if detail != "" {
			fmt.Printf("  [%s] %s (%s)", item.Status, item.Name, detail)
		} else {
			fmt.Printf("  [%s] %s", item.Status, item.Name)
		}
		if item.Message != "" {
			fmt.Printf(": %s", item.Message)
		}
		fmt.Println()
		if item.Install != "" && item.Status != "ready" {
			fmt.Printf("      install: %s\n", item.Install)
		}
	}
	fmt.Println()
}

func installSetupDependencies(status setupcheck.SetupStatus) error {
	if !environmentReady(status.Environment) {
		return errorsWithInstallHelp("npm and npx are required before device-executor can install agent dependencies", status.Environment)
	}

	seen := map[string]struct{}{}
	for _, item := range status.Agents {
		if item.Status == "ready" {
			continue
		}
		pkg, ok := agentNpmPackages[item.Command]
		if !ok {
			return fmt.Errorf("cannot auto-install agent command %q; install it manually", item.Command)
		}
		if _, ok := seen[pkg]; ok {
			continue
		}
		seen[pkg] = struct{}{}
		if err := npmInstallGlobal(pkg); err != nil {
			return err
		}
	}

	for _, item := range status.ACPPackages {
		if item.Status == "ready" {
			continue
		}
		if item.Package == "" {
			continue
		}
		if _, ok := seen[item.Package]; ok {
			continue
		}
		seen[item.Package] = struct{}{}
		if err := npmInstallGlobal(item.Package); err != nil {
			return err
		}
	}

	return nil
}

func environmentReady(items []setupcheck.DependencyItem) bool {
	for _, item := range items {
		if item.Status != "ready" {
			return false
		}
	}
	return true
}

func errorsWithInstallHelp(message string, items []setupcheck.DependencyItem) error {
	fmt.Fprintln(os.Stderr, message)
	for _, item := range items {
		if item.Status != "ready" && item.Install != "" {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", item.Name, item.Install)
		}
	}
	return errors.New(message)
}

func npmInstallGlobal(packageName string) error {
	fmt.Printf("Installing %s...\n", packageName)
	cmd := exec.Command("npm", "install", "-g", packageName)
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				fmt.Printf("  %s\n", line)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("npm install -g %s failed: %w", packageName, err)
	}
	return nil
}
