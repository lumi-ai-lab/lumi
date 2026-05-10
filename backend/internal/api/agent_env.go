package api

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/config"
)

func injectLumiAgentEnv(cfg *config.Config, agentID string, apiBase string) {
	if cfg == nil {
		return
	}
	agent := cfg.FindAgent(agentID)
	if agent == nil {
		return
	}
	if agent.Env == nil {
		agent.Env = make(map[string]string)
	}
	if strings.TrimSpace(apiBase) != "" && strings.TrimSpace(agent.Env["LUMI_API_BASE"]) == "" {
		agent.Env["LUMI_API_BASE"] = apiBase
	}
	if strings.TrimSpace(agent.Env["LUMI_CLI"]) == "" {
		if cliPath := resolveLumiCLIPath(); cliPath != "" {
			agent.Env["LUMI_CLI"] = cliPath
			prependAgentPath(agent.Env, filepath.Dir(cliPath))
		}
	}
}

func lumiAPIBaseForConfig(cfg *config.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.PublicServerURL) != "" {
		return strings.TrimRight(strings.TrimSpace(cfg.PublicServerURL), "/") + "/api"
	}
	return "http://127.0.0.1:3000/api"
}

func resolveLumiCLIPath() string {
	candidates := []string{
		os.Getenv("LUMI_CLI"),
		filepath.Join(executableDir(), "lumi-cli"),
		filepath.Join(executableDir(), "..", "cli", "lumi-cli"),
		filepath.Join(executableDir(), "..", "cli", "lumi-cli.exe"),
		filepath.Join(currentWorkingDir(), "cli", "lumi-cli"),
		filepath.Join(currentWorkingDir(), "cli", "lumi-cli.exe"),
		filepath.Join(currentWorkingDir(), "..", "cli", "lumi-cli"),
		filepath.Join(currentWorkingDir(), "..", "cli", "lumi-cli.exe"),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err == nil {
			candidate = abs
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func prependAgentPath(env map[string]string, dir string) {
	if dir == "" {
		return
	}
	current := env["PATH"]
	if current == "" {
		current = os.Getenv("PATH")
	}
	for _, part := range filepath.SplitList(current) {
		if part == dir {
			env["PATH"] = current
			return
		}
	}
	if current == "" {
		env["PATH"] = dir
		return
	}
	env["PATH"] = dir + string(os.PathListSeparator) + current
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

func currentWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
