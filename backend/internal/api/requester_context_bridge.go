package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/lumipaths"
	"github.com/pengmide/lumi/internal/requestercontext"
)

const localRequesterContextDirectoryScope = "agents"

func localRequesterContextBridge(workspaceID, agentID string) (*requestercontext.FileBridge, error) {
	root := lumipaths.Path("runtime", "requester-context", strconv.Itoa(os.Getpid()))
	return requestercontext.NewFileBridgeInScope(root, localRequesterContextDirectoryScope, workspaceID, agentID)
}

func injectLocalRequesterContextEnv(cfg *config.Config, workspaceID, agentID string) error {
	if cfg == nil {
		return fmt.Errorf("Lumi config is required")
	}
	agentCfg := cfg.FindAgent(agentID)
	if agentCfg == nil {
		return fmt.Errorf("agent not found: %s", agentID)
	}
	bridge, err := localRequesterContextBridge(workspaceID, agentID)
	if err != nil {
		return err
	}
	if agentCfg.Env == nil {
		agentCfg.Env = make(map[string]string)
	}
	agentCfg.Env[requestercontext.EnvRequesterContextDir] = filepath.Clean(bridge.Dir())
	return nil
}
