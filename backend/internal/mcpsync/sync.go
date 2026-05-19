package mcpsync

import (
	"fmt"

	"github.com/pengmide/lumi/internal/mcpstore"
)

// Service is the high-level orchestration callers use to flush MCP records
// to all enabled per-agent configuration files on the local machine.
type Service struct {
	store *mcpstore.Store
}

// New constructs a Service backed by the given store.
func New(store *mcpstore.Store) *Service { return &Service{store: store} }

// SyncLocal applies the SSOT to ~/.claude.json, ~/.codex/config.toml, and
// ~/.qwen/settings.json. Each adapter handles its own missing-file policy.
// Per-app errors are returned as a map; callers decide how to surface them.
func (s *Service) SyncLocal(home string) map[string]error {
	out := map[string]error{}
	if s == nil || s.store == nil {
		out["error"] = fmt.Errorf("mcpsync: store is nil")
		return out
	}
	records := s.store.List()
	out["claude"] = ApplyClaude(home, records)
	out["codex"] = ApplyCodex(home, records)
	out["qwen"] = ApplyQwen(home, records)
	return out
}

// SyncToRoot writes the per-agent config files into a sandbox staging root
// where claude config goes to <root>/claude-root/.claude.json, codex to
// <root>/codex/config.toml, and qwen to <root>/qwen/settings.json — matching
// the layout produced by sandbox.Manager.resolveCredentialMounts.
type SandboxLayout struct {
	ClaudeRoot string // .../credentials/claude-root
	CodexRoot  string // .../credentials/codex
	QwenRoot   string // .../credentials/qwen
}

// SyncSandbox applies the SSOT into the per-workspace credential staging
// directories. Caller is responsible for ensuring the dirs exist beforehand
// (sandbox.Manager already does that for credentials).
func (s *Service) SyncSandbox(layout SandboxLayout) map[string]error {
	out := map[string]error{}
	if s == nil || s.store == nil {
		out["error"] = fmt.Errorf("mcpsync: store is nil")
		return out
	}
	records := s.store.List()
	if layout.ClaudeRoot != "" {
		out["claude"] = ApplyClaudeAt(layout.ClaudeRoot+"/.claude.json", records)
	}
	if layout.CodexRoot != "" {
		out["codex"] = ApplyCodexAt(layout.CodexRoot+"/config.toml", records)
	}
	if layout.QwenRoot != "" {
		out["qwen"] = ApplyQwenAt(layout.QwenRoot+"/settings.json", records)
	}
	return out
}
