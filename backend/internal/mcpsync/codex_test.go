package mcpsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/mcpstore"
)

func TestApplyCodexInsertsManagedBlockIntoEmptyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	rec := mcpstore.Record{
		ID: "fs", Name: "filesystem", Transport: mcpstore.TransportStdio,
		Command: "npx", Args: []string{"@modelcontextprotocol/server-filesystem", "/tmp"},
		Env:    map[string]string{"FOO": "bar"},
		Apps:   mcpstore.Apps{Codex: true},
		Scopes: mcpstore.DefaultScopes(),
	}
	if err := ApplyCodex(home, []mcpstore.Record{rec}); err != nil {
		t.Fatalf("ApplyCodex: %v", err)
	}
	body := readFile(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(body, codexBeginMarker) || !strings.Contains(body, codexEndMarker) {
		t.Fatalf("missing markers in:\n%s", body)
	}
	for _, want := range []string{
		"[mcp_servers.filesystem]",
		`type = "stdio"`,
		`command = "npx"`,
		`args = ["@modelcontextprotocol/server-filesystem", "/tmp"]`,
		"[mcp_servers.filesystem.env]",
		`FOO = "bar"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q in:\n%s", want, body)
		}
	}
}

func TestApplyCodexPreservesUserContent(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".codex", "config.toml")
	must(t, os.MkdirAll(filepath.Dir(configPath), 0o755))
	initial := `# user comment
sandbox_mode = "workspace-write"

[mcp_servers.user-defined]
command = "echo"
`
	must(t, os.WriteFile(configPath, []byte(initial), 0o644))

	rec := mcpstore.Record{
		ID: "fs", Name: "filesystem", Transport: mcpstore.TransportStdio,
		Command: "npx", Apps: mcpstore.Apps{Codex: true},
		Scopes: mcpstore.DefaultScopes(),
	}
	must(t, ApplyCodex(home, []mcpstore.Record{rec}))
	body := readFile(t, configPath)
	for _, want := range []string{
		`# user comment`,
		`sandbox_mode = "workspace-write"`,
		`[mcp_servers.user-defined]`,
		`command = "echo"`,
		codexBeginMarker,
		`[mcp_servers.filesystem]`,
		codexEndMarker,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q in:\n%s", want, body)
		}
	}
}

func TestApplyCodexRewritesManagedBlock(t *testing.T) {
	home := t.TempDir()
	rec1 := mcpstore.Record{
		ID: "fs", Name: "filesystem", Transport: mcpstore.TransportStdio, Command: "npx",
		Apps: mcpstore.Apps{Codex: true}, Scopes: mcpstore.DefaultScopes(),
	}
	must(t, ApplyCodex(home, []mcpstore.Record{rec1}))
	// Re-apply with empty list — managed block should be empty (markers only).
	must(t, ApplyCodex(home, nil))
	body := readFile(t, filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(body, "[mcp_servers.filesystem]") {
		t.Fatalf("managed entry was not removed:\n%s", body)
	}
	if !strings.Contains(body, codexBeginMarker) || !strings.Contains(body, codexEndMarker) {
		t.Fatalf("markers missing after empty rewrite:\n%s", body)
	}
}

func TestApplyCodexHTTPHeaders(t *testing.T) {
	home := t.TempDir()
	rec := mcpstore.Record{
		ID: "remote", Name: "remote", Transport: mcpstore.TransportHTTP,
		URL:    "https://example.com",
		Headers: map[string]string{"Authorization": "Bearer XYZ"},
		Apps:   mcpstore.Apps{Codex: true},
		Scopes: mcpstore.DefaultScopes(),
	}
	must(t, ApplyCodex(home, []mcpstore.Record{rec}))
	body := readFile(t, filepath.Join(home, ".codex", "config.toml"))
	for _, want := range []string{
		`type = "http"`,
		`url = "https://example.com"`,
		`[mcp_servers.remote.http_headers]`,
		`Authorization = "Bearer XYZ"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
