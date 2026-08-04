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
	return requestercontext.NewFileBridgeInScope(defaultRoot, localRequesterContextDirectoryScope, workspaceID, agentID)
}

func injectLocalRequesterContextEnv(cfg *config.Config, workspaceID, agentID string) error {
	agentCfg, bridge, _, err := resolveLocalRequesterContextBinding(cfg, workspaceID, agentID)
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
	agentCfg.Env[requestercontext.EnvRequesterContextDir] = nextDir
	return nil
}

func validateLocalRequesterContextEnv(cfg *config.Config, workspaceID, agentID string) error {
	agentCfg, bridge, _, err := resolveLocalRequesterContextBinding(cfg, workspaceID, agentID)
	if err != nil {
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
	bridge, err := localRequesterContextBridge(workspaceID, agentID)
	if err != nil {
		return nil, nil, false, err
	}
	return agentCfg, bridge, false, nil
}
