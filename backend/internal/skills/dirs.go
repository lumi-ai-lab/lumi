package skills

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
)

func BuildDirs(workspacePath string, agent config.AgentConfig) []string {
	absDir, err := filepath.Abs(workspacePath)
	if err != nil {
		absDir = workspacePath
	}
	switch agentmode.DetectBackend(agent.ID, agent.Command, agent.Args) {
	case agentmode.BackendClaude:
		return ClaudeDirs(absDir)
	case agentmode.BackendCodex:
		return CodexDirs(absDir, "")
	case agentmode.BackendQwen:
		return QwenDirs(absDir)
	default:
		return nil
	}
}

func ClaudeDirs(workDir string) []string {
	configHome := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if configHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			configHome = filepath.Join(home, ".claude")
		}
	}
	home, _ := os.UserHomeDir()
	projectDirs := walkUpClaudeSkillDirs(workDir, home)
	if configHome == "" {
		return projectDirs
	}
	return uniqueDirs(append(projectDirs, filepath.Join(configHome, "skills")))
}

func CodexDirs(workDir, explicitCodexHome string) []string {
	home, _ := os.UserHomeDir()
	codexHome := strings.TrimSpace(explicitCodexHome)
	if codexHome == "" {
		codexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if codexHome == "" && home != "" {
		codexHome = filepath.Join(home, ".codex")
	}

	projectDirs := walkUpCodexSkillDirs(workDir, home)
	userDirs := make([]string, 0, 2)
	if codexHome != "" {
		userDirs = append(userDirs, filepath.Join(codexHome, "skills"))
	}
	if home != "" {
		userDirs = append(userDirs, filepath.Join(home, ".agents", "skills"))
	}
	return uniqueDirs(append(projectDirs, userDirs...))
}

func QwenDirs(workDir string) []string {
	home, _ := os.UserHomeDir()
	projectDirs := walkUpSkillDirs(workDir, home, ".qwen")
	if home == "" {
		return projectDirs
	}
	return uniqueDirs(append(projectDirs, filepath.Join(home, ".qwen", "skills")))
}

func walkUpClaudeSkillDirs(workDir, home string) []string {
	return walkUpSkillDirs(workDir, home, ".claude")
}

func walkUpCodexSkillDirs(workDir, home string) []string {
	current := filepath.Clean(workDir)
	home = filepath.Clean(home)
	stopAt := findProjectRoot(current, ".git", ".jj")
	var dirs []string
	for {
		if home != "" && sameCleanPath(current, home) {
			break
		}
		dirs = append(dirs,
			filepath.Join(current, ".agents", "skills"),
			filepath.Join(current, ".codex", "skills"),
		)
		if stopAt != "" && sameCleanPath(current, stopAt) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return uniqueDirs(dirs)
}

func walkUpSkillDirs(workDir, home, dirName string) []string {
	current := filepath.Clean(workDir)
	home = filepath.Clean(home)
	stopAt := findProjectRoot(current, ".git")
	var dirs []string
	for {
		if home != "" && sameCleanPath(current, home) {
			break
		}
		dirs = append(dirs, filepath.Join(current, dirName, "skills"))
		if stopAt != "" && sameCleanPath(current, stopAt) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return uniqueDirs(dirs)
}

func findProjectRoot(start string, markers ...string) string {
	current := filepath.Clean(start)
	for {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(current, marker)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func sameCleanPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func uniqueDirs(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
