package skills

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func discoverSkillsInDir(scanRoot, currentDir string, seen, visited map[string]bool) []*Skill {
	realDir := realPath(currentDir)
	if visited[realDir] {
		return nil
	}
	visited[realDir] = true

	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return nil
	}

	var result []*Skill
	for _, entry := range entries {
		fullPath := filepath.Join(currentDir, entry.Name())
		if entry.Name() == "SKILL.md" {
			skillDir := filepath.Dir(fullPath)
			if sameFilePath(skillDir, scanRoot) {
				continue
			}
			skillName := filepath.Base(skillDir)
			if seen[strings.ToLower(skillName)] {
				continue
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}
			skill := parseSkillMD(skillName, string(data), skillDir)
			if skill == nil {
				continue
			}
			seen[strings.ToLower(skillName)] = true
			result = append(result, skill)
			slog.Debug("skill discovered", "name", skillName, "dir", skillDir)
			continue
		}
		if shouldDescendIntoSkillPath(fullPath, entry) {
			result = append(result, discoverSkillsInDir(scanRoot, fullPath, seen, visited)...)
		}
	}
	return result
}

func shouldDescendIntoSkillPath(path string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func sameFilePath(a, b string) bool {
	return realPath(a) == realPath(b)
}

func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}
