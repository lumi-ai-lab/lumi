package api

import (
	"context"
	"log"
	"path/filepath"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/mcpsync"
	"github.com/pengmide/lumi/internal/skillsync"
)

// applySandboxSSOT is invoked by the sandbox.Manager once it has prepared
// the per-workspace credential staging dirs (claude-root/, codex/, qwen/, pi/).
// We mirror the on-disk layout the agents expect inside /root by writing
// SSOT-derived files into the host-side staging dirs; the Docker bind
// mounts then expose them at /root/.claude/skills/, /root/.codex/skills/
// etc. inside the container.
//
// Errors are logged but never propagated upward — credential preparation
// must succeed even when SSOT is unavailable.
func (s *Server) applySandboxSSOT(workspaceID, credentialsRoot string) {
	if s == nil {
		return
	}
	if s.mcpStore != nil {
		layout := mcpsync.SandboxLayout{
			ClaudeRoot: filepath.Join(credentialsRoot, "claude-root"),
			CodexRoot:  filepath.Join(credentialsRoot, "codex"),
			QwenRoot:   filepath.Join(credentialsRoot, "qwen"),
		}
		svc := mcpsync.New(s.mcpStore)
		for app, err := range svc.SyncSandbox(layout) {
			if err != nil {
				log.Printf("sandbox %s mcp sync failed for %s: %v", workspaceID, app, err)
			}
		}
	}
	if s.skillStore != nil {
		// Sandbox copies skills into claude-root/.claude/skills, codex/skills,
		// qwen/skills, and pi/agent/skills (the bind mount destinations match each agent's
		// expected layout under /root). We override the dot-app path for
		// claude because the staging dir is "claude-root", not ".claude".
		dotApps := map[skillsync.Backend]string{
			agentmode.BackendClaude: filepath.Join("claude-root", ".claude"),
			agentmode.BackendCodex:  "codex",
			agentmode.BackendQwen:   "qwen",
			agentmode.BackendPi:     filepath.Join("pi", "agent"),
		}
		svc := skillsync.New(s.skillStore, nil)
		results, err := svc.SyncToRoot(context.Background(), credentialsRoot, skillsync.ModeCopy, dotApps)
		if err != nil {
			log.Printf("sandbox %s skill sync failed: %v", workspaceID, err)
			return
		}
		for app, res := range results {
			if len(res.Errors) > 0 {
				log.Printf("sandbox %s skill sync (%s) errors: %v", workspaceID, app, res.Errors)
			}
		}
	}
}
