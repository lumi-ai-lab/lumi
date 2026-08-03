package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/lumipaths"
	"github.com/pengmide/lumi/internal/requestercontext"
)

const localRequesterContextDirectoryScope = "agents"

func localRequesterContextBridge(workspaceID, agentID string) (*requestercontext.FileBridge, error) {
	defaultRoot := lumipaths.Path("runtime", "requester-context", strconv.Itoa(os.Getpid()))
	settings, err := requestercontext.RuntimeSettingsFromEnv(defaultRoot)
	if err != nil {
		return nil, err
	}
	if settings.Secure() && agentID == "pi" {
		return requestercontext.NewFileBridge(settings.Root, workspaceID, agentID, settings.BridgeOptions()...)
	}
	return requestercontext.NewFileBridgeInScope(defaultRoot, localRequesterContextDirectoryScope, workspaceID, agentID)
}

func injectLocalRequesterContextEnv(cfg *config.Config, workspaceID, agentID string) error {
	agentCfg, bridge, securedPi, err := resolveLocalRequesterContextBinding(cfg, workspaceID, agentID)
	if err != nil {
		return err
	}
	if agentCfg.Env == nil {
		agentCfg.Env = make(map[string]string)
	}
	nextDir := filepath.Clean(bridge.Dir())
	currentDir := strings.TrimSpace(agentCfg.Env[requestercontext.EnvRequesterContextDir])
	if currentDir != "" && filepath.Clean(currentDir) == nextDir {
		return nil
	}
	if securedPi && currentDir != "" {
		return fmt.Errorf("secured agent %s is already bound to requester context directory %s", agentID, filepath.Clean(currentDir))
	}
	agentCfg.Env[requestercontext.EnvRequesterContextDir] = nextDir
	return nil
}

func validateLocalRequesterContextEnv(cfg *config.Config, workspaceID, agentID string) error {
	agentCfg, bridge, securedPi, err := resolveLocalRequesterContextBinding(cfg, workspaceID, agentID)
	if err != nil || !securedPi {
		return err
	}
	currentDir := strings.TrimSpace(agentCfg.Env[requestercontext.EnvRequesterContextDir])
	if currentDir == "" || filepath.Clean(currentDir) != filepath.Clean(bridge.Dir()) {
		return fmt.Errorf("secured agent %s is already bound to a different requester context directory", agentID)
	}
	return nil
}

func resolveLocalRequesterContextBinding(cfg *config.Config, workspaceID, agentID string) (*config.AgentConfig, *requestercontext.FileBridge, bool, error) {
	if cfg == nil {
		return nil, nil, false, fmt.Errorf("Lumi config is required")
	}
	agentCfg := cfg.FindAgent(agentID)
	if agentCfg == nil {
		return nil, nil, false, fmt.Errorf("agent not found: %s", agentID)
	}
	settings, err := requestercontext.RuntimeSettingsFromEnv("")
	if err != nil {
		return nil, nil, false, err
	}
	securedPi := settings.Secure() && agentCfg.ID == "pi"
	if securedPi {
		if err := agentCfg.ValidateRunAsIdentity(); err != nil || agentCfg.RunAsUID == nil {
			return nil, nil, false, fmt.Errorf("secured pi agent requires a complete non-root run-as identity")
		}
		if !agentCfg.HasRunAsGroup(*settings.ReaderGID) {
			return nil, nil, false, fmt.Errorf("secured pi agent does not receive requester context reader GID %d", *settings.ReaderGID)
		}
	}
	bridge, err := localRequesterContextBridge(workspaceID, agentID)
	if err != nil {
		return nil, nil, false, err
	}
	return agentCfg, bridge, securedPi, nil
}
