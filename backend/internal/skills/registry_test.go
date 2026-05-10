package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRegistryDiscoveryBoundaries(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "group", "deploy"), "Deploy", "Deploy things", "# Deploy\nUse deploy.")
	writeSkill(t, filepath.Join(root, "docs", "pdf"), "PDF Helper", "Use PDFs", "# PDF\nUse pdf.")
	writeSkill(t, filepath.Join(root, "other", "deploy"), "Duplicate Deploy", "Duplicate", "# Duplicate")
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# root ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	external := t.TempDir()
	writeSkill(t, filepath.Join(external, "linked"), "Linked", "Linked desc", "# Linked")
	if err := os.Symlink(external, filepath.Join(root, "linked-dir")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.SetDirs([]string{root})
	got := registry.ListAll()
	if len(got) != 3 {
		t.Fatalf("len(ListAll()) = %d, want 3 (%+v)", len(got), got)
	}
	names := make(map[string]bool)
	for _, skill := range got {
		names[skill.Name] = true
		if skill.Name == "deploy" && !strings.Contains(skill.Source, filepath.Join("group", "deploy")) {
			t.Fatalf("duplicate handling kept %q, want first deploy", skill.Source)
		}
	}
	for _, want := range []string{"deploy", "pdf", "linked"} {
		if !names[want] {
			t.Fatalf("missing skill %q in %+v", want, names)
		}
	}
}

func TestParseSkillFrontmatterAndFallback(t *testing.T) {
	skill := parseSkillMD("pdf-helper", `---
name: PDF Helper
description: >-
  Use this skill
  for PDF files.
---

# PDF Helper
Detailed instructions.`, "/tmp/pdf-helper")
	if skill == nil {
		t.Fatal("parseSkillMD() = nil")
	}
	if skill.DisplayName != "PDF Helper" {
		t.Fatalf("DisplayName = %q", skill.DisplayName)
	}
	if len(skill.Aliases) != 1 || skill.Aliases[0] != "PDF Helper" {
		t.Fatalf("Aliases = %+v, want PDF Helper", skill.Aliases)
	}
	if skill.Description != "Use this skill for PDF files." {
		t.Fatalf("Description = %q", skill.Description)
	}
	if skill.Body != "# PDF Helper\nDetailed instructions." {
		t.Fatalf("Body = %q", skill.Body)
	}
	if skill.SkillFile != filepath.Join("/tmp/pdf-helper", "SKILL.md") {
		t.Fatalf("SkillFile = %q", skill.SkillFile)
	}

	fallback := parseSkillMD("simple", "# A first line that becomes the description\nBody", "/tmp/simple")
	if fallback.Description != "# A first line that becomes the description" {
		t.Fatalf("fallback Description = %q", fallback.Description)
	}
}

func TestResolveTreatsHyphenAndUnderscoreEqually(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "pdf-helper"), "", "PDF", "# PDF")
	registry := NewRegistry()
	registry.SetDirs([]string{root})
	if registry.Resolve("pdf_helper") == nil {
		t.Fatal("Resolve(pdf_helper) = nil, want pdf-helper")
	}
}

func TestResolveSupportsFrontmatterNameAlias(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "nuwa-skill"), "huashu-nuwa", "Nuwa", "# Nuwa")
	registry := NewRegistry()
	registry.SetDirs([]string{root})
	skill := registry.Resolve("huashu_nuwa")
	if skill == nil {
		t.Fatal("Resolve(huashu_nuwa) = nil, want nuwa-skill")
	}
	if skill.Name != "nuwa-skill" {
		t.Fatalf("Name = %q, want canonical directory name nuwa-skill", skill.Name)
	}
}

func TestBuildInvocationPromptIncludesBodyAndArgs(t *testing.T) {
	prompt := BuildInvocationPrompt(&Skill{
		Name:        "pdf-helper",
		DisplayName: "PDF Helper",
		Description: "Use PDFs",
		Body:        "# Instructions",
	}, []string{"file.pdf"})
	for _, want := range []string{"## Skill: PDF Helper", "## Description: Use PDFs", "# Instructions", "## User Arguments:\nfile.pdf"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func writeSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := body
	if name != "" || description != "" {
		content = "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
