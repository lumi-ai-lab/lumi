package agent

import (
	"fmt"
	"strings"

	"github.com/pengmide/lumi/internal/acppatch"
	"github.com/pengmide/lumi/internal/config"
)

// ResolveManagedConfig rewrites agent commands that Lumi manages internally.
// It currently only targets pi-acp@0.0.27, preserving non-package npx args.
func ResolveManagedConfig(agentCfg *config.AgentConfig) (*config.AgentConfig, error) {
	if agentCfg == nil {
		return nil, nil
	}
	packageIndex, packageSpec := npxPackageArg(agentCfg.Command, agentCfg.Args)
	if !acppatch.IsTargetPiACP(packageSpec) {
		return agentCfg, nil
	}
	exe, err := acppatch.ResolvePiACPExecutable(acppatch.RuntimeOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to prepare Lumi patched %s: %w", acppatch.PiACPPackageSpec, err)
	}
	next := *agentCfg
	next.Command = exe
	if packageIndex+1 < len(agentCfg.Args) {
		next.Args = append([]string(nil), agentCfg.Args[packageIndex+1:]...)
	} else {
		next.Args = nil
	}
	return &next, nil
}

func npxPackageArg(command string, args []string) (int, string) {
	if strings.TrimSpace(command) != "npx" {
		return -1, ""
	}
	for i, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return i, arg
		}
	}
	return -1, ""
}
