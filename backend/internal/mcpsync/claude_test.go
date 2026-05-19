package mcpsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/mcpstore"
)

func TestApplyClaudeWritesAndPreservesUserKeys(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	// Pre-existing file with a user key + a user-defined mcp server.
	initial := map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"user-defined": map[string]any{"type": "stdio", "command": "echo"},
		},
	}
	must(t, writeJSONAtomic(path, initial))

	rec := mcpstore.Record{
		ID: "fs", Name: "filesystem", Transport: mcpstore.TransportStdio,
		Command: "npx", Args: []string{"@modelcontextprotocol/server-filesystem", "/tmp"},
		Apps:   mcpstore.Apps{Claude: true},
		Scopes: mcpstore.DefaultScopes(),
	}
	if err := ApplyClaude(home, []mcpstore.Record{rec}); err != nil {
		t.Fatalf("ApplyClaude: %v", err)
	}

	var got map[string]any
	must(t, readJSON(path, &got))
	if got["theme"] != "dark" {
		t.Fatalf("user key dropped: %+v", got)
	}
	servers := got["mcpServers"].(map[string]any)
	if servers["user-defined"] == nil {
		t.Fatal("user-defined entry was destroyed")
	}
	fs := servers["filesystem"].(map[string]any)
	if fs["command"] != "npx" || fs["type"] != "stdio" {
		t.Fatalf("filesystem entry shape: %+v", fs)
	}
	managed := got[managedKey].([]any)
	if len(managed) != 1 || managed[0] != "filesystem" {
		t.Fatalf("managed = %+v", managed)
	}

	// Disable the record → managed entry must be removed, user-defined kept.
	rec.Apps.Claude = false
	if err := ApplyClaude(home, []mcpstore.Record{rec}); err != nil {
		t.Fatalf("ApplyClaude disable: %v", err)
	}
	must(t, readJSON(path, &got))
	servers = got["mcpServers"].(map[string]any)
	if servers["user-defined"] == nil {
		t.Fatal("user-defined was destroyed on disable")
	}
	if _, ok := servers["filesystem"]; ok {
		t.Fatal("filesystem entry should have been removed")
	}
}

func TestApplyClaudeSkipsMissingFile(t *testing.T) {
	home := t.TempDir() // no ~/.claude.json
	if err := ApplyClaude(home, []mcpstore.Record{{
		ID: "x", Name: "x", Transport: mcpstore.TransportStdio, Command: "echo",
		Apps: mcpstore.Apps{Claude: true}, Scopes: mcpstore.DefaultScopes(),
	}}); err != nil {
		t.Fatalf("ApplyClaude on fresh home should be no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("file should not be created when missing: %v", err)
	}
}

func TestApplyQwenCreatesIfMissing(t *testing.T) {
	home := t.TempDir()
	if err := ApplyQwen(home, []mcpstore.Record{{
		ID: "x", Name: "remote", Transport: mcpstore.TransportHTTP, URL: "https://x",
		Apps: mcpstore.Apps{Qwen: true}, Scopes: mcpstore.DefaultScopes(),
	}}); err != nil {
		t.Fatalf("ApplyQwen: %v", err)
	}
	path := filepath.Join(home, ".qwen", "settings.json")
	var got map[string]any
	must(t, readJSON(path, &got))
	servers := got["mcpServers"].(map[string]any)
	if servers["remote"] == nil {
		t.Fatalf("remote entry missing: %+v", got)
	}
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
