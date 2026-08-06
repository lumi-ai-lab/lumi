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

	"github.com/pengmide/lumi/internal/acppatch"
	"github.com/pengmide/lumi/internal/setupcheck"
)

const bootstrapManifestPath = "/lumi/runtime/bootstrap.json"
const bootstrapManifestVersion = 1

var bootstrapManifestFile = bootstrapManifestPath

var agentNpmPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
	"qwen":   "@qwen-code/qwen-code",
	"pi":     "@earendil-works/pi-coding-agent@0.78.0",
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
		return errorsWithInstallHelp("node, npm, and npx are required before device-executor can install agent dependencies", status.Environment)
	}

	signature := setupSignature(status)
	// Bootstrap short-circuit must still re-ensure hostAuth patches: an older deploy
	// could leave marker+runner only (RPC missing) while status.Ready stays true.
	if status.Ready && bootstrapManifestReady(signature) {
		fmt.Println("Device setup dependencies already installed.")
		return ensureHostAuthPatches(status, false /* installIfMissing */)
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

	if err := ensureHostAuthPatches(status, true /* installIfMissing */); err != nil {
		return err
	}

	for _, item := range status.ACPPackages {
		if item.Package == "" || item.Status == "ready" {
			continue
		}
		if acppatch.IsTargetPiACP(item.Package) {
			continue // handled by ensureHostAuthPatches
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

	return writeBootstrapManifest(signature)
}

// ensureHostAuthPatches applies pi-acp + pi-coding-agent hostAuth patches.
// installIfMissing allows npm install when packages are absent; otherwise only
// in-process/file patches are applied on already-installed packages.
func ensureHostAuthPatches(status setupcheck.SetupStatus, installIfMissing bool) error {
	opts := acppatch.RuntimeOptions{Log: func(message string) {
		fmt.Printf("  %s\n", message)
	}}

	needPiStack := false
	piACPReady := false
	for _, item := range status.ACPPackages {
		if acppatch.IsTargetPiACP(item.Package) || isPiACPAgentItem(item) {
			needPiStack = true
			if item.Status == "ready" {
				piACPReady = true
			}
		}
	}
	if !needPiStack {
		for _, item := range status.Agents {
			if isPiACPAgentItem(item) {
				needPiStack = true
				if item.Status == "ready" {
					piACPReady = true
				}
				break
			}
		}
	}
	if !needPiStack {
		return nil
	}

	fmt.Println("Ensuring hostAuth patches (pi-acp + pi-coding-agent)…")
	if installIfMissing && !piACPReady {
		if _, err := acppatch.InstallAndPatch(opts); err != nil {
			return err
		}
	} else if _, err := acppatch.EnsurePiACPPatched(opts); err != nil {
		if installIfMissing {
			if _, err2 := acppatch.InstallAndPatch(opts); err2 != nil {
				return fmt.Errorf("ensure pi-acp hostAuth patch: %v (install: %v)", err, err2)
			}
		} else {
			return fmt.Errorf("ensure pi-acp hostAuth patch: %w", err)
		}
	}

	if _, err := acppatch.EnsurePiCodingAgentHostAuthPatched(opts); err != nil {
		if !installIfMissing {
			return fmt.Errorf("ensure pi-coding-agent hostAuth patch: %w", err)
		}
		if installErr := npmInstallGlobal(acppatch.PiCodingAgentPackageSpec); installErr != nil {
			return fmt.Errorf("pi-acp patched but pi-coding-agent hostAuth patch failed: %v (install: %v)", err, installErr)
		}
		if _, err2 := acppatch.EnsurePiCodingAgentHostAuthPatched(opts); err2 != nil {
			return err2
		}
	}
	return nil
}

func isPiACPAgentItem(item setupcheck.DependencyItem) bool {
	cmd := strings.ToLower(strings.TrimSpace(item.Command))
	pkg := strings.ToLower(strings.TrimSpace(item.Package))
	name := strings.ToLower(strings.TrimSpace(item.Name))
	if strings.Contains(cmd, "pi-acp") || cmd == "pi" {
		return true
	}
	if strings.Contains(pkg, "pi-acp") {
		return true
	}
	return name == "pi" || strings.Contains(name, "pi-acp")
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
		patchID := ""
		if acppatch.IsTargetPiACP(item.Package) {
			patchID = acppatch.PiACPHostAuthPatchID
		}
		values = append(values, "acp:"+item.Name+":"+item.Command+":"+item.Package+":"+patchID)
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
