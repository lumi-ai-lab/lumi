package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/setupcheck"
)

const bootstrapManifestPath = "/lumi/runtime/bootstrap.json"
const bootstrapManifestVersion = 1

var bootstrapManifestFile = bootstrapManifestPath

var agentNpmPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
	"qwen":   "@qwen-code/qwen-code",
}

type bootstrapManifest struct {
	Version     int    `json:"version"`
	Signature   string `json:"signature"`
	CompletedAt int64  `json:"completedAt"`
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

	signature := setupSignature(status)
	if status.Ready && bootstrapManifestReady(signature) {
		fmt.Println("Device setup dependencies already installed.")
		return nil
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
		fmt.Printf("Installing agent dependency: %s (command: %s, package: %s)\n", firstNonEmpty(item.Name, item.Command), item.Command, pkg)
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
		fmt.Printf("Installing ACP dependency: %s (package: %s)\n", firstNonEmpty(item.Name, item.Package), item.Package)
		if err := npmInstallGlobal(item.Package); err != nil {
			return err
		}
	}

	if status.Ready {
		return writeBootstrapManifest(signature)
	}
	if err := writeBootstrapManifest(signature); err != nil {
		return err
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func setupSignature(status setupcheck.SetupStatus) string {
	values := make([]string, 0, len(status.Agents)+len(status.ACPPackages))
	for _, item := range status.Agents {
		values = append(values, "agent:"+item.Name+":"+item.Command+":"+item.Package)
	}
	for _, item := range status.ACPPackages {
		values = append(values, "acp:"+item.Name+":"+item.Command+":"+item.Package)
	}
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(sum[:])
}

func bootstrapManifestReady(signature string) bool {
	data, err := os.ReadFile(bootstrapManifestFile)
	if err != nil {
		return false
	}
	var manifest bootstrapManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	return manifest.Version == bootstrapManifestVersion && manifest.Signature == signature
}

func writeBootstrapManifest(signature string) error {
	if err := os.MkdirAll(filepath.Dir(bootstrapManifestFile), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(bootstrapManifest{
		Version:     bootstrapManifestVersion,
		Signature:   signature,
		CompletedAt: time.Now().UnixMilli(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(bootstrapManifestFile, append(data, '\n'), 0o644)
}

func npmInstallGlobal(packageName string) error {
	fmt.Printf("Installing %s...\n", packageName)
	uninstallCmd := exec.Command("npm", "uninstall", "-g", packageName)
	_ = uninstallCmd.Run()
	cleanupNpmTempDirs(packageName)

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

func cleanupNpmTempDirs(packageName string) {
	cmd := exec.Command("npm", "config", "get", "prefix")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	prefix := strings.TrimSpace(string(output))
	nodeModulesPath := filepath.Join(prefix, "lib", "node_modules")
	parts := strings.Split(packageName, "/")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "@") {
		return
	}

	scopeDir := filepath.Join(nodeModulesPath, parts[0])
	name := parts[1]
	entries, err := os.ReadDir(scopeDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		entryName := entry.Name()
		if strings.HasPrefix(entryName, "."+name+"-") || entryName == name {
			_ = os.RemoveAll(filepath.Join(scopeDir, entryName))
		}
	}
}
