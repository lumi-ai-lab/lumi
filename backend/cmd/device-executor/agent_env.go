package main

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/lumipaths"
	"github.com/pengmide/lumi/internal/workspacecli"
)

func buildLumiRuntimeEnv(server string, cfg *ExecutorConfig, agentEnv map[string]string) (map[string]string, error) {
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
	metricBin := ""
	if metricCLI, ok := workspacecli.MetricCLIPath(env["LUMI_WORKSPACE_PATH"]); ok {
		env[workspacecli.MetricCLIEnv] = metricCLI
		metricBin = filepath.Dir(metricCLI)
		env["PATH"] = workspacecli.PrependPath(os.Getenv("PATH"), metricBin)
	}
	startupEnv := cloneEnv(agentEnv)
	shell := strings.TrimSpace(startupEnv["SHELL"])
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	bridgeRoot := lumipaths.Path("runtime", "shell-env", strconv.Itoa(os.Getpid()))
	hadManagedZDOTDir := strings.TrimSpace(startupEnv[workspacecli.ManagedZDOTDirEnv]) != "" ||
		workspacecli.IsManagedZDOTDir(startupEnv[workspacecli.ZDOTDirEnv], bridgeRoot)
	if err := workspacecli.ConfigureZshStartupEnv(startupEnv, bridgeRoot, shell, metricBin); err != nil {
		return nil, err
	}
	copyZshRuntimeEnv(env, startupEnv, hadManagedZDOTDir)
	if cliPath := resolveExecutorLumiCLI(env["LUMI_WORKSPACE_PATH"]); cliPath != "" {
		env["LUMI_CLI"] = cliPath
	}
	return env, nil
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
		if isLumiRuntimeEnvKey(k) || shouldReplaceZshEnv(agentCfg.Env, runtimeEnv, k) {
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
	case "LUMI_API_BASE", "LUMI_WORKSPACE_ID", "LUMI_WORKSPACE_PATH", "LUMI_CLI", "LUMI_REQUESTER_CONTEXT_DIR", workspacecli.MetricCLIEnv, "PATH", workspacecli.OriginalZDOTDirEnv, workspacecli.ManagedZDOTDirEnv:
		return true
	default:
		return false
	}
}

func cloneEnv(env map[string]string) map[string]string {
	cloned := make(map[string]string, len(env))
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}

func copyZshRuntimeEnv(runtimeEnv, startupEnv map[string]string, hadManaged bool) {
	if strings.TrimSpace(startupEnv[workspacecli.ManagedZDOTDirEnv]) != "" {
		runtimeEnv[workspacecli.ZDOTDirEnv] = startupEnv[workspacecli.ZDOTDirEnv]
		runtimeEnv[workspacecli.OriginalZDOTDirEnv] = startupEnv[workspacecli.OriginalZDOTDirEnv]
		runtimeEnv[workspacecli.ManagedZDOTDirEnv] = startupEnv[workspacecli.ManagedZDOTDirEnv]
		return
	}
	if hadManaged {
		if original := strings.TrimSpace(startupEnv[workspacecli.ZDOTDirEnv]); original != "" {
			runtimeEnv[workspacecli.ZDOTDirEnv] = original
		}
	}
}

func shouldReplaceZshEnv(agentEnv, runtimeEnv map[string]string, key string) bool {
	if key != workspacecli.ZDOTDirEnv {
		return false
	}
	if _, ok := runtimeEnv[key]; ok {
		return true
	}
	return strings.TrimSpace(agentEnv[workspacecli.ManagedZDOTDirEnv]) != ""
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
