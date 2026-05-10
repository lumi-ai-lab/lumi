package skills

import (
	"fmt"
	"strings"
)

func BuildInvocationPrompt(skill *Skill, args []string) string {
	var sb strings.Builder
	sb.WriteString("The user is asking you to execute the following skill.\n\n")

	name := skill.DisplayName
	if name == "" {
		name = skill.Name
	}
	fmt.Fprintf(&sb, "## Skill: %s\n", name)
	if skill.Description != "" {
		fmt.Fprintf(&sb, "## Description: %s\n", skill.Description)
	}

	sb.WriteString("\n## Skill Instructions:\n")
	sb.WriteString(skill.Body)
	if len(args) > 0 {
		sb.WriteString("\n\n## User Arguments:\n")
		sb.WriteString(strings.Join(args, " "))
	}
	sb.WriteString("\n\nPlease follow the skill instructions above to complete the task.")
	return sb.String()
}
