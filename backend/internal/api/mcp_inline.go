package api

import (
	"context"
	"log"
	"os"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/mcpstore"
	"github.com/pengmide/lumi/internal/mcpsync"
	"github.com/pengmide/lumi/internal/skillstore"
	"github.com/pengmide/lumi/internal/skillsync"
)

// newDefaultSkillStore loads ~/.lumi/skills.json. A missing file is treated as
// an empty store; load errors are logged and a fresh empty store is returned
// so the server still boots.
func newDefaultSkillStore() *skillstore.Store {
	store, err := skillstore.Default()
	if err != nil {
		log.Printf("skill store init: %v", err)
		return skillstore.New("", "", "")
	}
	if err := store.Load(); err != nil {
		log.Printf("skill store load: %v", err)
	}
	return store
}

// newDefaultMCPStore loads ~/.lumi/mcp.json with the same tolerant policy.
func newDefaultMCPStore() *mcpstore.Store {
	store, err := mcpstore.Default()
	if err != nil {
		log.Printf("mcp store init: %v", err)
		return mcpstore.New("")
	}
	if err := store.Load(); err != nil {
		log.Printf("mcp store load: %v", err)
	}
	return store
}

// agentMCPServers builds the inline session/new mcpServers payload for the
// given agent id. It silently returns an empty slice when the store is nil
// or the agent's backend cannot be detected.
func (s *Server) agentMCPServers(agentID string) []any {
	if s == nil || s.mcpStore == nil {
		return nil
	}
	cfg := s.config.FindAgent(agentID)
	if cfg == nil {
		return nil
	}
	backend := agentmode.DetectBackend(cfg.ID, cfg.Command, cfg.Args)
	servers := mcpsync.BuildSessionMCP(backend, s.mcpStore.List())
	return mcpsync.AsAnySlice(servers)
}

// AgentMCPServersFor mirrors agentMCPServers but accepts an explicit agent
// configuration list. Used by the wechat / wecom chat runtimes that hold
// their own copy of the agents slice.
func AgentMCPServersFor(agents []config.AgentConfig, agentID string, store *mcpstore.Store) []any {
	if store == nil {
		return nil
	}
	for _, cfg := range agents {
		if cfg.ID != agentID {
			continue
		}
		backend := agentmode.DetectBackend(cfg.ID, cfg.Command, cfg.Args)
		servers := mcpsync.BuildSessionMCP(backend, store.List())
		return mcpsync.AsAnySlice(servers)
	}
	return nil
}

// SyncSummary captures aggregated results of a local SSOT sync.
type SyncSummary struct {
	MCP    map[string]string           `json:"mcp,omitempty"`
	Skill  map[string]skillsync.Result `json:"skill,omitempty"`
	Remote map[string]string           `json:"remote,omitempty"`
}

// localSyncAll applies the current SSOT to supported local agent config roots
// using the in-process stores. Errors are aggregated into the summary so the
// caller can return them in HTTP responses.
func (s *Server) localSyncAll(ctx context.Context) SyncSummary {
	summary := SyncSummary{}
	if s == nil {
		return summary
	}
	home, _ := os.UserHomeDir()
	if s.mcpStore != nil {
		summary.MCP = map[string]string{}
		svc := mcpsync.New(s.mcpStore)
		for app, err := range svc.SyncLocal(home) {
			if err != nil {
				summary.MCP[app] = err.Error()
			}
		}
	}
	if s.skillStore != nil {
		svc := skillsync.New(s.skillStore, nil)
		results, err := svc.SyncLocal(ctx)
		if err != nil {
			log.Printf("skillsync local: %v", err)
		}
		summary.Skill = results
	}
	if s.sandbox != nil {
		for _, workspaceID := range s.sandbox.RunningWorkspaceIDs() {
			root := s.sandbox.CredentialsRoot(workspaceID)
			if root == "" {
				continue
			}
			s.applySandboxSSOT(workspaceID, root)
		}
	}
	if s.devices != nil {
		summary.Remote = s.broadcastRemoteSSOT(ctx, true)
	}
	return summary
}
