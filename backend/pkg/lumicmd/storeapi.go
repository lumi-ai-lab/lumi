package lumicmd

import (
	"fmt"
	"strings"

	"github.com/pengmide/lumi/internal/mcpstore"
	"github.com/pengmide/lumi/internal/skillstore"
)

// storeAppsFromCSV parses a comma-separated app list (e.g. "claude,codex")
// into the per-app boolean struct used by both stores. Unknown tokens are
// ignored so users can pass values like "all".
func storeAppsFromCSV(csv string) (mcpstore.Apps, skillstore.Apps) {
	mcp := mcpstore.Apps{}
	skill := skillstore.Apps{}
	for _, raw := range strings.Split(csv, ",") {
		token := strings.ToLower(strings.TrimSpace(raw))
		switch token {
		case "claude":
			mcp.Claude = true
			skill.Claude = true
		case "codex":
			mcp.Codex = true
			skill.Codex = true
		case "qwen":
			mcp.Qwen = true
			skill.Qwen = true
		case "pi":
			skill.Pi = true
		case "all", "*":
			mcp = mcpstore.Apps{Claude: true, Codex: true, Qwen: true}
			skill = skillstore.Apps{Claude: true, Codex: true, Qwen: true, Pi: true}
		}
	}
	return mcp, skill
}

func mcpAppsToCSV(a mcpstore.Apps) string {
	parts := make([]string, 0, 3)
	if a.Claude {
		parts = append(parts, "claude")
	}
	if a.Codex {
		parts = append(parts, "codex")
	}
	if a.Qwen {
		parts = append(parts, "qwen")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func skillAppsToCSV(a skillstore.Apps) string {
	parts := make([]string, 0, 3)
	if a.Claude {
		parts = append(parts, "claude")
	}
	if a.Codex {
		parts = append(parts, "codex")
	}
	if a.Qwen {
		parts = append(parts, "qwen")
	}
	if a.Pi {
		parts = append(parts, "pi")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// stringSliceFlag implements flag.Value for repeatable --arg/--header flags.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSliceFlag) Slice() []string    { return append([]string(nil), (*s)...) }

// storeKVFlag implements flag.Value for repeatable --env KEY=VAL flags.
type storeKVFlag map[string]string

func (s storeKVFlag) String() string {
	pairs := make([]string, 0, len(s))
	for k, v := range s {
		pairs = append(pairs, k+"="+v)
	}
	return strings.Join(pairs, ",")
}

func (s storeKVFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("expected KEY=VAL, got %q", v)
	}
	s[strings.TrimSpace(k)] = strings.TrimSpace(val)
	return nil
}
