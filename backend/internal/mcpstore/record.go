// Package mcpstore manages the SSOT (single source of truth) JSON file for
// MCP server records: ~/.lumi/mcp.json. This package is intentionally
// transport-agnostic; it owns persistence and CRUD only. Distribution to
// individual agents lives in internal/mcpsync.
package mcpstore

import "strings"

// Transport identifies the protocol Lumi should use to talk to the MCP server.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
)

// Apps tracks which agent backends should receive this MCP server.
type Apps struct {
	Claude bool `json:"claude"`
	Codex  bool `json:"codex"`
	Qwen   bool `json:"qwen"`
}

// IsEnabledFor reports whether the record should be applied to backend.
func (a Apps) IsEnabledFor(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		return a.Claude
	case "codex":
		return a.Codex
	case "qwen":
		return a.Qwen
	default:
		return false
	}
}

// SetEnabledFor updates the per-app flag for the given backend (no-op for
// unknown backends).
func (a *Apps) SetEnabledFor(backend string, enabled bool) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		a.Claude = enabled
	case "codex":
		a.Codex = enabled
	case "qwen":
		a.Qwen = enabled
	}
}

// Scopes tracks which deployment surfaces should receive the record.
type Scopes struct {
	Local   bool `json:"local"`
	Sandbox bool `json:"sandbox"`
	Remote  bool `json:"remote"`
}

// DefaultScopes returns the all-enabled scopes used for newly created records.
func DefaultScopes() Scopes {
	return Scopes{Local: true, Sandbox: true, Remote: true}
}

// Record describes a single MCP server entry persisted to the SSOT JSON file.
type Record struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Transport Transport         `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Apps      Apps              `json:"apps"`
	Scopes    Scopes            `json:"scopes"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
}

// Validate sanity-checks the record fields prior to save.
func (r *Record) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errInvalidf("id is required")
	}
	if strings.TrimSpace(r.Name) == "" {
		return errInvalidf("name is required")
	}
	t := r.Transport
	if t == "" {
		t = TransportStdio
		r.Transport = t
	}
	switch t {
	case TransportStdio:
		if strings.TrimSpace(r.Command) == "" {
			return errInvalidf("stdio MCP requires command")
		}
	case TransportHTTP, TransportSSE:
		if strings.TrimSpace(r.URL) == "" {
			return errInvalidf("%s MCP requires url", t)
		}
	default:
		return errInvalidf("unsupported transport: %s", t)
	}
	return nil
}

// Clone returns a deep copy safe for callers to mutate.
func (r Record) Clone() Record {
	out := r
	if r.Args != nil {
		out.Args = append([]string(nil), r.Args...)
	}
	if r.Env != nil {
		out.Env = make(map[string]string, len(r.Env))
		for k, v := range r.Env {
			out.Env[k] = v
		}
	}
	if r.Headers != nil {
		out.Headers = make(map[string]string, len(r.Headers))
		for k, v := range r.Headers {
			out.Headers[k] = v
		}
	}
	return out
}
