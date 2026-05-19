package mcpsync

import (
	"os"
	"path/filepath"

	"github.com/pengmide/lumi/internal/mcpstore"
)

// claudeFileFor resolves the JSON config path. We mirror cc-switch and write
// to ~/.claude.json (the file the Claude Code CLI consumes).
func claudeFileFor(home string) string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".claude.json")
}

// qwenFileFor resolves ~/.qwen/settings.json. Qwen-code reads MCP servers
// from this JSON file (top-level mcpServers map, identical shape to Claude).
func qwenFileFor(home string) string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".qwen", "settings.json")
}

// ApplyClaude writes the SSOT-managed mcpServers into ~/.claude.json. If the
// file does not exist (Claude Code not installed yet), the call is a no-op.
func ApplyClaude(home string, records []mcpstore.Record) error {
	return applyJSONMerge(jsonMergeOptions{
		Path:            claudeFileFor(home),
		AppKey:          "claude",
		Records:         records,
		CreateIfMissing: false,
	})
}

// ApplyClaudeAt writes to a specific path. Used by sandbox staging where the
// target lives under <runtimeDir>/sandboxes/<id>/credentials/claude-root/.claude.json
// rather than the user's home directory.
func ApplyClaudeAt(path string, records []mcpstore.Record) error {
	return applyJSONMerge(jsonMergeOptions{
		Path:            path,
		AppKey:          "claude",
		Records:         records,
		CreateIfMissing: true,
	})
}

// ApplyQwen writes managed mcpServers to ~/.qwen/settings.json. Unlike Claude,
// the file may not exist yet on first install — we create it.
func ApplyQwen(home string, records []mcpstore.Record) error {
	return applyJSONMerge(jsonMergeOptions{
		Path:            qwenFileFor(home),
		AppKey:          "qwen",
		Records:         records,
		CreateIfMissing: true,
	})
}

// ApplyQwenAt writes to a specific path (sandbox staging).
func ApplyQwenAt(path string, records []mcpstore.Record) error {
	return applyJSONMerge(jsonMergeOptions{
		Path:            path,
		AppKey:          "qwen",
		Records:         records,
		CreateIfMissing: true,
	})
}
