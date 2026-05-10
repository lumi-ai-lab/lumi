package skills

import (
	"path/filepath"
	"strings"
)

func parseSkillMD(skillName, raw, sourceDir string) *Skill {
	content := strings.TrimSpace(raw)
	if content == "" {
		return nil
	}

	var frontmatter map[string]string
	body := content
	if strings.HasPrefix(content, "---") {
		rest := content[3:]
		endIdx := strings.Index(rest, "\n---")
		if endIdx >= 0 {
			frontmatter = parseFrontmatter(rest[:endIdx])
			body = strings.TrimSpace(rest[endIdx+4:])
		}
	}
	if body == "" {
		return nil
	}

	displayName := ""
	description := ""
	var aliases []string
	if frontmatter != nil {
		displayName = frontmatter["name"]
		if displayName != "" && normalizeCommandName(displayName) != normalizeCommandName(skillName) {
			aliases = append(aliases, displayName)
		}
		description = frontmatter["description"]
	}
	if description == "" {
		first, _, _ := strings.Cut(body, "\n")
		first = strings.TrimSpace(first)
		runes := []rune(first)
		if len(runes) > 80 {
			first = string(runes[:80]) + "..."
		}
		description = first
	}

	return &Skill{
		Name:        skillName,
		Aliases:     aliases,
		DisplayName: displayName,
		Description: description,
		Body:        body,
		Source:      sourceDir,
		SkillFile:   filepath.Join(sourceDir, "SKILL.md"),
	}
}

func parseFrontmatter(block string) map[string]string {
	values := make(map[string]string)
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		if val == ">-" || val == "|-" || val == ">" || val == "|" {
			var blockLines []string
			for i+1 < len(lines) {
				next := lines[i+1]
				if len(next) != 0 && next[0] != ' ' && next[0] != '\t' {
					break
				}
				i++
				if strings.TrimSpace(next) != "" {
					blockLines = append(blockLines, strings.TrimSpace(next))
				}
			}
			val = strings.Join(blockLines, " ")
		}
		val = strings.Trim(val, `"'`)
		if key != "" {
			values[key] = val
		}
	}
	return values
}
