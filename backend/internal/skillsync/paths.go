// Package skillsync distributes SSOT skill records into per-agent skill
// directories on the local filesystem. It only manages files it created;
// the per-app .lumi-managed.json lockfile records every owned target so
// foreign content stays untouched.
package skillsync

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/agentmode"
)

// Backend identifies the agent backend the directory belongs to.
type Backend = agentmode.Backend

// AppKey returns the lower-case app identifier matching skillstore.Apps fields.
func AppKey(b Backend) string {
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

// SupportedBackends is the ordered list of backends skillsync writes to.
func SupportedBackends() []Backend {
	return []Backend{agentmode.BackendClaude, agentmode.BackendCodex, agentmode.BackendQwen}
}

// UserSkillDir returns the user-level skills directory for backend, anchored
// at the given home directory. An empty home falls back to os.UserHomeDir.
func UserSkillDir(home string, b Backend) (string, error) {
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	switch b {
	case agentmode.BackendClaude:
		dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
		if dir == "" {
			dir = filepath.Join(home, ".claude")
		}
		return filepath.Join(dir, "skills"), nil
	case agentmode.BackendCodex:
		dir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		return filepath.Join(dir, "skills"), nil
	case agentmode.BackendQwen:
		return filepath.Join(home, ".qwen", "skills"), nil
	default:
		return "", nil
	}
}
