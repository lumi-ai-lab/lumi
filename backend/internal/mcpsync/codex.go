package mcpsync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pengmide/lumi/internal/mcpstore"
)

// Codex managed-block markers. Anything between these lines is regenerated
// from the SSOT on every sync; user-authored mcp_servers blocks placed
// outside the markers are preserved verbatim.
const (
	codexBeginMarker = "# >>> lumi managed mcp_servers >>>"
	codexEndMarker   = "# <<< lumi managed mcp_servers <<<"
)

// codexFileFor returns ~/.codex/config.toml respecting CODEX_HOME.
func codexFileFor(home string) string {
	if h := strings.TrimSpace(os.Getenv("CODEX_HOME")); h != "" {
		return filepath.Join(h, "config.toml")
	}
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".codex", "config.toml")
}

// ApplyCodex writes the SSOT-managed mcp_servers block into Codex's TOML
// config. Missing files are created.
func ApplyCodex(home string, records []mcpstore.Record) error {
	return applyCodexAt(codexFileFor(home), records, true)
}

// ApplyCodexAt writes to a specific config path (sandbox staging).
func ApplyCodexAt(path string, records []mcpstore.Record) error {
	return applyCodexAt(path, records, true)
}

func applyCodexAt(path string, records []mcpstore.Record, createIfMissing bool) error {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if !createIfMissing {
			return nil
		}
		existing = []byte{}
	} else if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	managedBlock := buildCodexManagedBlock(records)
	updated := replaceManagedBlock(string(existing), managedBlock)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(updated), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// buildCodexManagedBlock renders the marker-wrapped TOML block. Empty
// record set produces just the markers (so removing all entries is idempotent
// and visible).
func buildCodexManagedBlock(records []mcpstore.Record) string {
	var b strings.Builder
	b.WriteString(codexBeginMarker)
	b.WriteString("\n")

	enabled := make([]mcpstore.Record, 0, len(records))
	for _, r := range records {
		if !r.Apps.Codex {
			continue
		}
		if !r.Scopes.Local && !r.Scopes.Sandbox && !r.Scopes.Remote {
			continue
		}
		enabled = append(enabled, r)
	}
	sort.SliceStable(enabled, func(i, j int) bool { return enabled[i].Name < enabled[j].Name })

	for _, r := range enabled {
		writeCodexServer(&b, r)
		b.WriteString("\n")
	}
	b.WriteString(codexEndMarker)
	b.WriteString("\n")
	return b.String()
}

func writeCodexServer(b *strings.Builder, r mcpstore.Record) {
	transport := r.Transport
	if transport == "" {
		transport = mcpstore.TransportStdio
	}
	fmt.Fprintf(b, "[mcp_servers.%s]\n", r.Name)
	fmt.Fprintf(b, "type = %q\n", string(transport))
	switch transport {
	case mcpstore.TransportStdio:
		fmt.Fprintf(b, "command = %q\n", r.Command)
		if len(r.Args) > 0 {
			fmt.Fprintf(b, "args = %s\n", tomlStringArray(r.Args))
		}
	case mcpstore.TransportHTTP, mcpstore.TransportSSE:
		fmt.Fprintf(b, "url = %q\n", r.URL)
	}
	if len(r.Env) > 0 && transport == mcpstore.TransportStdio {
		fmt.Fprintf(b, "[mcp_servers.%s.env]\n", r.Name)
		for _, k := range sortedKeys(r.Env) {
			fmt.Fprintf(b, "%s = %q\n", k, r.Env[k])
		}
	}
	if len(r.Headers) > 0 && (transport == mcpstore.TransportHTTP || transport == mcpstore.TransportSSE) {
		fmt.Fprintf(b, "[mcp_servers.%s.http_headers]\n", r.Name)
		for _, k := range sortedKeys(r.Headers) {
			fmt.Fprintf(b, "%s = %q\n", k, r.Headers[k])
		}
	}
}

func tomlStringArray(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%q", v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// replaceManagedBlock substitutes the managed block in-place; if no markers
// exist yet, the block is appended (with a leading blank line for clarity).
func replaceManagedBlock(existing, block string) string {
	begin := strings.Index(existing, codexBeginMarker)
	if begin < 0 {
		trimmed := strings.TrimRight(existing, "\r\n\t ")
		if trimmed == "" {
			return block
		}
		return trimmed + "\n\n" + block
	}
	end := strings.Index(existing, codexEndMarker)
	if end < 0 || end < begin {
		// Marker pair broken; append managed block at EOF and leave the
		// stray opener alone so the user can repair manually.
		return strings.TrimRight(existing, "\r\n\t ") + "\n\n" + block
	}
	endLineEnd := end + len(codexEndMarker)
	if endLineEnd < len(existing) && existing[endLineEnd] == '\n' {
		endLineEnd++
	}
	return existing[:begin] + block + existing[endLineEnd:]
}
