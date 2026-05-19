package device

import "encoding/json"

// SSOTSyncPayload carries the full or delta SSOT state the server pushes to
// every connected device. When Reset=true, the device first removes every
// lockfile-tracked entry from its local skill / MCP configurations before
// applying the new state.
type SSOTSyncPayload struct {
	Skills []SSOTSkillBlob  `json:"skills,omitempty"`
	MCP    []json.RawMessage `json:"mcp,omitempty"` // mcpstore.Record values as opaque JSON
	Reset  bool              `json:"reset,omitempty"`
}

// SSOTSkillBlob describes a single skill record being delivered over the wire.
// Files contains every blob inside the skill directory (including SKILL.md).
type SSOTSkillBlob struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Apps  map[string]bool `json:"apps"`
	Files []SSOTSkillFile `json:"files"`
}

// SSOTSkillFile is one file inside a skill blob. Path is relative to the
// skill root and uses forward slashes; Content is base64-encoded raw bytes.
type SSOTSkillFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    uint32 `json:"mode,omitempty"`
}

// SSOTSyncAckPayload reports the result of applying an SSOT push.
type SSOTSyncAckPayload struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}
