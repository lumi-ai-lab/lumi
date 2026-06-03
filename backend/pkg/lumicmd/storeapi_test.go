package lumicmd

import "testing"

func TestStoreAppsFromCSVIncludesPiForSkillsOnly(t *testing.T) {
	mcp, skill := storeAppsFromCSV("claude,pi")

	if !mcp.Claude || mcp.Codex || mcp.Qwen {
		t.Fatalf("mcp apps = %+v, want claude only", mcp)
	}
	if !skill.Claude || !skill.Pi || skill.Codex || skill.Qwen {
		t.Fatalf("skill apps = %+v, want claude and pi", skill)
	}
}

func TestStoreAppsAllIncludesPiSkillsButNotMCP(t *testing.T) {
	mcp, skill := storeAppsFromCSV("all")

	if !mcp.Claude || !mcp.Codex || !mcp.Qwen {
		t.Fatalf("mcp apps = %+v, want claude/codex/qwen", mcp)
	}
	if !skill.Claude || !skill.Codex || !skill.Qwen || !skill.Pi {
		t.Fatalf("skill apps = %+v, want claude/codex/qwen/pi", skill)
	}
	if got := skillAppsToCSV(skill); got != "claude,codex,qwen,pi" {
		t.Fatalf("skillAppsToCSV() = %q", got)
	}
}
