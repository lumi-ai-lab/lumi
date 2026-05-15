package setupcheck

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/sysutil"
)

type InstallLogger func(string)

type InstallEvent struct {
	Index   int
	Type    string
	Status  string
	Message string
}

type InstallResult struct {
	Success     bool
	Environment []DependencyItem
	Agents      []DependencyItem
	ACPPackages []DependencyItem
}

var agentNpmPackages = map[string]string{
	"claude": "@anthropic-ai/claude-code",
	"codex":  "@openai/codex",
	"qwen":   "@qwen-code/qwen-code",
}

var npmRegistries = []struct {
	Name string
	URL  string
}{
	{"China (npmmirror)", "https://registry.npmmirror.com"},
	{"Official (npmjs)", "https://registry.npmjs.org"},
}

var (
	cachedRegistry string
	registryOnce   sync.Once
)

func InstallMissing(status SetupStatus, progress func(InstallEvent), logFn InstallLogger) InstallResult {
	result := InstallResult{
		Environment: append([]DependencyItem{}, status.Environment...),
		Agents:      append([]DependencyItem{}, status.Agents...),
		ACPPackages: append([]DependencyItem{}, status.ACPPackages...),
	}
	if logFn == nil {
		logFn = func(string) {}
	}
	if progress == nil {
		progress = func(InstallEvent) {}
	}

	if !environmentReady(result.Environment) {
		return InstallResult{
			Success:     false,
			Environment: result.Environment,
			Agents:      result.Agents,
			ACPPackages: result.ACPPackages,
		}
	}

	allSuccess := true
	for i := range result.Agents {
		item := result.Agents[i]
		if item.Status == "ready" {
			progress(InstallEvent{Index: i, Type: "agent", Status: "ready", Message: "Already installed"})
			continue
		}

		npmPkg, canInstall := agentNpmPackages[item.Command]
		if !canInstall {
			result.Agents[i].Status = "error"
			result.Agents[i].Message = "Cannot auto-install"
			progress(InstallEvent{Index: i, Type: "agent", Status: "error", Message: "Cannot auto-install"})
			allSuccess = false
			continue
		}

		result.Agents[i].Status = "installing"
		result.Agents[i].Message = "Installing..."
		progress(InstallEvent{Index: i, Type: "agent", Status: "installing", Message: fmt.Sprintf("Installing %s...", npmPkg)})

		err := installGlobalPackage(npmPkg, logFn)
		if err != nil {
			result.Agents[i].Status = "error"
			result.Agents[i].Message = err.Error()
			progress(InstallEvent{Index: i, Type: "agent", Status: "error", Message: err.Error()})
			allSuccess = false
			continue
		}

		result.Agents[i].Status = "ready"
		result.Agents[i].Message = "Installed"
		progress(InstallEvent{Index: i, Type: "agent", Status: "ready", Message: "Installed"})
	}

	for i := range result.ACPPackages {
		item := result.ACPPackages[i]
		if item.Status == "ready" {
			progress(InstallEvent{Index: i, Type: "acp", Status: "ready", Message: "Already installed"})
			continue
		}
		if item.Status == "blocked" {
			progress(InstallEvent{Index: i, Type: "acp", Status: "blocked", Message: item.Message})
			allSuccess = false
			continue
		}

		result.ACPPackages[i].Status = "installing"
		result.ACPPackages[i].Message = "Installing..."
		progress(InstallEvent{Index: i, Type: "acp", Status: "installing", Message: fmt.Sprintf("Installing %s...", item.Package)})

		err := installPackageWithProgress(item.Package, logFn)
		if err != nil {
			result.ACPPackages[i].Status = "error"
			result.ACPPackages[i].Message = err.Error()
			progress(InstallEvent{Index: i, Type: "acp", Status: "error", Message: err.Error()})
			allSuccess = false
			continue
		}

		result.ACPPackages[i].Status = "ready"
		result.ACPPackages[i].Message = "Installed"
		progress(InstallEvent{Index: i, Type: "acp", Status: "ready", Message: "Installed"})
	}

	result.Success = allSuccess
	return result
}

func environmentReady(items []DependencyItem) bool {
	for _, item := range items {
		if item.Status != "ready" {
			return false
		}
	}
	return true
}

func installPackageWithProgress(packageName string, logFn InstallLogger) error {
	registry := selectFastestRegistry()

	cmdStr := fmt.Sprintf("npx -y --registry=%s %s --help", registry, packageName)
	log.Printf("[Setup] Installing ACP package: %s", packageName)
	log.Printf("[Setup] Command: %s", cmdStr)
	logFn(fmt.Sprintf("Running: %s", cmdStr))

	cmd := exec.Command("npx", "-y", "--registry="+registry, packageName, "--help")
	sysutil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if outputStr != "" {
		for _, line := range strings.Split(outputStr, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				log.Printf("[Setup]   %s", line)
			}
		}
	}

	if strings.Contains(outputStr, "npm ERR!") || strings.Contains(outputStr, "404 Not Found") {
		log.Printf("[Setup] Failed to install %s", packageName)
		return fmt.Errorf("failed to install: %s", strings.TrimSpace(outputStr))
	}

	if err != nil && strings.Contains(outputStr, "npm ERR!") {
		log.Printf("[Setup] Failed to install %s: %v", packageName, err)
		return fmt.Errorf("install failed: %w", err)
	}

	log.Printf("[Setup] Successfully installed %s", packageName)
	logFn("Installation completed")
	return nil
}

func installGlobalPackage(packageName string, logFn InstallLogger) error {
	registry := selectFastestRegistry()

	log.Printf("[Setup] Uninstalling existing %s (if any)...", packageName)
	uninstallCmd := exec.Command("npm", "uninstall", "-g", packageName)
	sysutil.HideWindow(uninstallCmd)
	uninstallCmd.Run()

	cleanupNpmTempDirs(packageName)

	cmdStr := fmt.Sprintf("npm install -g --registry=%s %s", registry, packageName)
	log.Printf("[Setup] Installing global package: %s", packageName)
	log.Printf("[Setup] Command: %s", cmdStr)
	logFn(fmt.Sprintf("Running: %s", cmdStr))

	cmd := exec.Command("npm", "install", "-g", "--registry="+registry, packageName)
	sysutil.HideWindow(cmd)
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if outputStr != "" {
		for _, line := range strings.Split(outputStr, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				log.Printf("[Setup]   %s", line)
			}
		}
	}

	if err != nil {
		log.Printf("[Setup] Failed to install %s: %v", packageName, err)
		if strings.Contains(outputStr, "npm ERR!") {
			return fmt.Errorf("failed to install: %s", strings.TrimSpace(outputStr))
		}
		return fmt.Errorf("install failed: %w", err)
	}

	log.Printf("[Setup] Successfully installed %s", packageName)
	logFn("Installation completed")
	return nil
}

func cleanupNpmTempDirs(packageName string) {
	cmd := exec.Command("npm", "config", "get", "prefix")
	sysutil.HideWindow(cmd)
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
			targetPath := filepath.Join(scopeDir, entryName)
			log.Printf("[Setup] Cleaning up: %s", targetPath)
			os.RemoveAll(targetPath)
		}
	}
}

func selectFastestRegistry() string {
	registryOnce.Do(func() {
		log.Println("[Setup] Testing npm registry speeds...")

		type result struct {
			url      string
			name     string
			duration time.Duration
			err      error
		}

		results := make(chan result, len(npmRegistries))
		for _, reg := range npmRegistries {
			go func(name, url string) {
				start := time.Now()
				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(url + "/-/ping")
				duration := time.Since(start)
				if err != nil {
					results <- result{url: url, name: name, duration: duration, err: err}
					return
				}
				resp.Body.Close()
				results <- result{url: url, name: name, duration: duration, err: nil}
			}(reg.Name, reg.URL)
		}

		var fastest result
		fastest.duration = time.Hour
		for i := 0; i < len(npmRegistries); i++ {
			r := <-results
			if r.err != nil {
				log.Printf("[Setup]   %s: failed (%v)", r.name, r.err)
				continue
			}
			log.Printf("[Setup]   %s: %v", r.name, r.duration.Round(time.Millisecond))
			if r.duration < fastest.duration {
				fastest = r
			}
		}

		if fastest.url != "" {
			cachedRegistry = fastest.url
			log.Printf("[Setup] Selected registry: %s (%s)", fastest.name, fastest.url)
		} else {
			cachedRegistry = npmRegistries[1].URL
			log.Printf("[Setup] All registries failed, using official: %s", cachedRegistry)
		}
	})

	return cachedRegistry
}
