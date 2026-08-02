package main

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/config"
)

func buildLumiRuntimeEnv(server string, cfg *ExecutorConfig) map[string]string {
	env := make(map[string]string)
	if apiBase := lumiAPIBaseForServer(server); apiBase != "" {
		env["LUMI_API_BASE"] = apiBase
	}
	workspaceID := defaultWorkspace
	if cfg != nil && strings.TrimSpace(cfg.WorkspaceID) != "" {
		workspaceID = strings.TrimSpace(cfg.WorkspaceID)
	}
	env["LUMI_WORKSPACE_ID"] = workspaceID
	if cfg != nil && strings.TrimSpace(cfg.Workspace) != "" {
		env["LUMI_WORKSPACE_PATH"] = strings.TrimSpace(cfg.Workspace)
	}
	if cliPath := resolveExecutorLumiCLI(env["LUMI_WORKSPACE_PATH"]); cliPath != "" {
		env["LUMI_CLI"] = cliPath
	}
	return env
}

func mergeAgentEnv(agentCfg *config.AgentConfig, runtimeEnv map[string]string) *config.AgentConfig {
	if agentCfg == nil {
		return nil
	}
	next := *agentCfg
	if len(agentCfg.Args) > 0 {
		next.Args = append([]string(nil), agentCfg.Args...)
	}
	next.Env = make(map[string]string, len(agentCfg.Env)+len(runtimeEnv))
	for k, v := range agentCfg.Env {
		if isLumiRuntimeEnvKey(k) {
			continue
		}
		next.Env[k] = v
	}
	for k, v := range runtimeEnv {
		if strings.TrimSpace(v) == "" {
			continue
		}
		next.Env[k] = v
	}
	return &next
}

func isLumiRuntimeEnvKey(key string) bool {
	switch key {
	case "LUMI_API_BASE", "LUMI_WORKSPACE_ID", "LUMI_WORKSPACE_PATH", "LUMI_CLI", "LUMI_REQUESTER_CONTEXT_DIR":
		return true
	default:
		return false
	}
}

func lumiAPIBaseForServer(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	parsed, err := url.Parse(server)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(server, "/") + "/api"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/api") {
		parsed.Path += "/api"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func resolveExecutorLumiCLI(workspacePath string) string {
	if cliPath := strings.TrimSpace(os.Getenv("LUMI_CLI")); cliPath != "" {
		return cliPath
	}
	if workspacePath = strings.TrimSpace(workspacePath); workspacePath != "" {
		for _, name := range []string{"lumi", "lumi.exe"} {
			candidate := filepath.Join(workspacePath, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	if cliPath, err := exec.LookPath("lumi"); err == nil {
		return cliPath
	}
	return ""
}
