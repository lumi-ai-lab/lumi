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
	agentCfg, bridge, err := resolveLocalRequesterContextBinding(cfg, workspaceID, agentID)
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

func resolveLocalRequesterContextBinding(cfg *config.Config, workspaceID, agentID string) (*config.AgentConfig, *requestercontext.FileBridge, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("Lumi config is required")
	}
	agentCfg := cfg.FindAgent(agentID)
	if agentCfg == nil {
		return nil, nil, fmt.Errorf("agent not found: %s", agentID)
	}
	bridge, err := localRequesterContextBridge(workspaceID, agentID)
	if err != nil {
		return nil, nil, err
	}
	return agentCfg, bridge, nil
}

func publishLocalHostAuth(sessionID, workspaceID, agentID string, auth requestercontext.HostAuth, requester *requestercontext.Context) (requestercontext.CleanupFunc, error) {
	bridge, err := localRequesterContextBridge(workspaceID, agentID)
	if err != nil {
		return nil, err
	}
	_, cleanup, err := bridge.Write(sessionID, auth, requester)
	return cleanup, err
}
