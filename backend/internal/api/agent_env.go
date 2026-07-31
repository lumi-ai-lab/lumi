package api

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/lumipaths"
	"github.com/pengmide/lumi/internal/workspacecli"
)

func injectLumiAgentEnv(cfg *config.Config, agentID string, apiBase string, workspaceID string, workspacePath string) error {
	if cfg == nil {
		return nil
	}
	agent := cfg.FindAgent(agentID)
	if agent == nil {
		return nil
	}
	if agent.Env == nil {
		agent.Env = make(map[string]string)
	}
	if strings.TrimSpace(apiBase) != "" {
		agent.Env["LUMI_API_BASE"] = strings.TrimSpace(apiBase)
	}
	if strings.TrimSpace(workspaceID) != "" {
		agent.Env["LUMI_WORKSPACE_ID"] = strings.TrimSpace(workspaceID)
	}
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath != "" {
		agent.Env["LUMI_WORKSPACE_PATH"] = workspacePath
	}
	if previousMetricCLI := strings.TrimSpace(agent.Env[workspacecli.MetricCLIEnv]); previousMetricCLI != "" {
		agent.Env["PATH"] = workspacecli.RemovePath(agent.Env["PATH"], filepath.Dir(previousMetricCLI))
		delete(agent.Env, workspacecli.MetricCLIEnv)
	}
	metricCLI, metricCLIAvailable := workspacecli.MetricCLIPath(workspacePath)
	metricBin := ""
	if metricCLIAvailable {
		agent.Env[workspacecli.MetricCLIEnv] = metricCLI
		metricBin = filepath.Dir(metricCLI)
		prependAgentPath(agent.Env, metricBin)
	}
	shell := strings.TrimSpace(agent.Env["SHELL"])
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	bridgeRoot := lumipaths.Path("runtime", "shell-env", strconv.Itoa(os.Getpid()))
	if err := workspacecli.ConfigureZshStartupEnv(agent.Env, bridgeRoot, shell, metricBin); err != nil {
		delete(agent.Env, workspacecli.MetricCLIEnv)
		agent.Env["PATH"] = workspacecli.RemovePath(agent.Env["PATH"], metricBin)
		return err
	}
	if strings.TrimSpace(agent.Env["LUMI_CLI"]) == "" {
		if cliPath := resolveLumiCLIPath(); cliPath != "" {
			agent.Env["LUMI_CLI"] = cliPath
			prependAgentPath(agent.Env, filepath.Dir(cliPath))
		}
	}
	if strings.EqualFold(strings.TrimSpace(agent.ID), "pi") && strings.TrimSpace(agent.Env["PI_ACP_PI_COMMAND"]) == "" {
		if piPath := resolvePiCLIPath(); piPath != "" {
			agent.Env["PI_ACP_PI_COMMAND"] = piPath
			prependAgentPath(agent.Env, filepath.Dir(piPath))
		}
	}
	return nil
}

func localAgentACPModeID(cfg *config.Config, agentID, requestedMode string) string {
	if cfg == nil {
		return ""
	}
	agent := cfg.FindAgent(agentID)
	if agent == nil {
		return ""
	}
	backend := agentmode.DetectBackend(agent.ID, agent.Command, agent.Args)
	mode := strings.TrimSpace(requestedMode)
	if backend == agentmode.BackendCodex {
		configured := agentmode.ResolveSessionMode(backend, agent.SessionMode, agent.PermissionMode)
		if configured != agentmode.ModeDefault {
			mode = configured
		}
	}
	return agentmode.ACPModeID(backend, mode)
}

func lumiAPIBaseForConfig(cfg *config.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.PublicServerURL) != "" {
		return strings.TrimRight(strings.TrimSpace(cfg.PublicServerURL), "/") + "/api"
	}
	return "http://127.0.0.1:3000/api"
}

func lumiAPIBaseForWorkspace(cfg *config.Config, workspaceID string) string {
	base := lumiAPIBaseForConfig(cfg)
	if cfg == nil {
		return base
	}
	workspace := cfg.FindWorkspace(strings.TrimSpace(workspaceID))
	if workspace == nil || workspace.Kind != "sandbox" {
		return base
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return base
	}
	host := parsed.Hostname()
	port := parsed.Port()
	switch host {
	case "127.0.0.1", "localhost", "0.0.0.0":
		parsed.Host = "host.docker.internal"
		if port != "" {
			parsed.Host = "host.docker.internal:" + port
		}
		return strings.TrimRight(parsed.String(), "/")
	default:
		return base
	}
}

func resolveLumiCLIPath() string {
	candidates := []string{
		os.Getenv("LUMI_CLI"),
		filepath.Join(executableDir(), "lumi"),
		filepath.Join(executableDir(), "lumi.exe"),
		filepath.Join(executableDir(), "..", "backend", "lumi"),
		filepath.Join(executableDir(), "..", "backend", "lumi.exe"),
		filepath.Join(currentWorkingDir(), "backend", "lumi"),
		filepath.Join(currentWorkingDir(), "backend", "lumi.exe"),
		filepath.Join(currentWorkingDir(), "lumi"),
		filepath.Join(currentWorkingDir(), "lumi.exe"),
		filepath.Join(currentWorkingDir(), "..", "backend", "lumi"),
		filepath.Join(currentWorkingDir(), "..", "backend", "lumi.exe"),
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

func resolvePiCLIPath() string {
	if configured := strings.TrimSpace(os.Getenv("PI_ACP_PI_COMMAND")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured
		}
	}
	if found, err := exec.LookPath("pi"); err == nil {
		if abs, absErr := filepath.Abs(found); absErr == nil {
			return abs
		}
		return found
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
