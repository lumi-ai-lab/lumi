// Package mcpsync turns SSOT MCP records into per-agent configurations.
//
// inline.go: build the in-memory mcpServers payload passed to ACP session/new.
// claude.go / codex.go / qwen.go (P4): write the corresponding on-disk config.
package mcpsync

import (
	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/mcpstore"
)

// SessionMCPServer mirrors the shape Claude Code / Codex / Qwen ACP packages
// expect inside session/new mcpServers. Fields are omitempty so JSON output
// stays minimal.
type SessionMCPServer struct {
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// BuildSessionMCP returns the ordered list of MCP servers enabled for the
// given backend. Servers are taken from the store's current snapshot.
func BuildSessionMCP(backend agentmode.Backend, records []mcpstore.Record) []SessionMCPServer {
	if len(records) == 0 {
		return nil
	}
	backendKey := backendToApp(backend)
	out := make([]SessionMCPServer, 0, len(records))
	for _, r := range records {
		if backendKey == "" || !r.Apps.IsEnabledFor(backendKey) {
			continue
		}
		if !r.Scopes.Local && !r.Scopes.Sandbox && !r.Scopes.Remote {
			continue
		}
		entry := SessionMCPServer{
			Name:    r.Name,
			Type:    string(r.Transport),
			Command: r.Command,
			Args:    append([]string(nil), r.Args...),
			Env:     copyMap(r.Env),
			URL:     r.URL,
			Headers: copyMap(r.Headers),
		}
		if entry.Type == "" {
			entry.Type = string(mcpstore.TransportStdio)
		}
		out = append(out, entry)
	}
	return out
}

// AsAnySlice converts the typed slice to []any so callers that build untyped
// JSON-RPC params (e.g. session/new) can drop it in directly.
func AsAnySlice(servers []SessionMCPServer) []any {
	out := make([]any, len(servers))
	for i, s := range servers {
		out[i] = s
	}
	return out
}

func backendToApp(b agentmode.Backend) string {
	switch b {
	case agentmode.BackendClaude:
		return "claude"
	case agentmode.BackendCodex:
		return "codex"
	case agentmode.BackendQwen:
		return "qwen"
	default:
		return ""
	}
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
