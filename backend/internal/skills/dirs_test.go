package skills

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClaudeDirsWalkParentsStopAtGitRootAndUseConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configHome := filepath.Join(home, "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", configHome)

	repo := filepath.Join(home, "repo")
	work := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := ClaudeDirs(work)
	want := []string{
		filepath.Join(work, ".claude", "skills"),
		filepath.Join(repo, "apps", ".claude", "skills"),
		filepath.Join(repo, ".claude", "skills"),
		filepath.Join(configHome, "skills"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaudeDirs() = %#v, want %#v", got, want)
	}
}

func TestClaudeDirsUsesHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	work := filepath.Join(home, "repo")
	got := ClaudeDirs(work)
	if got[len(got)-1] != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("last Claude dir = %q, want home fallback", got[len(got)-1])
	}
}

func TestCodexDirsWalkParentsStopAtJJRootAndUseHomeDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := filepath.Join(home, "codex-home")
	t.Setenv("CODEX_HOME", codexHome)

	repo := filepath.Join(home, "repo")
	work := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(filepath.Join(repo, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := CodexDirs(work, "")
	want := []string{
		filepath.Join(work, ".agents", "skills"),
		filepath.Join(work, ".codex", "skills"),
		filepath.Join(repo, "apps", ".agents", "skills"),
		filepath.Join(repo, "apps", ".codex", "skills"),
		filepath.Join(repo, ".agents", "skills"),
		filepath.Join(repo, ".codex", "skills"),
		filepath.Join(codexHome, "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexDirs() = %#v, want %#v", got, want)
	}
}

func TestCodexDirsStopsAtGitRootAndUsesHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	repo := filepath.Join(home, "repo")
	work := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := CodexDirs(work, "")
	wantTail := []string{
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".agents", "skills"),
	}
	if !reflect.DeepEqual(got[len(got)-2:], wantTail) {
		t.Fatalf("CodexDirs tail = %#v, want %#v", got[len(got)-2:], wantTail)
	}
	if got[4] != filepath.Join(repo, ".agents", "skills") || got[5] != filepath.Join(repo, ".codex", "skills") {
		t.Fatalf("CodexDirs did not include git root dirs before stopping: %#v", got)
	}
}

func TestQwenDirsWalkParentsStopAtGitRootAndUseHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(home, "repo")
	work := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := QwenDirs(work)
	want := []string{
		filepath.Join(work, ".qwen", "skills"),
		filepath.Join(repo, "apps", ".qwen", "skills"),
		filepath.Join(repo, ".qwen", "skills"),
		filepath.Join(home, ".qwen", "skills"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QwenDirs() = %#v, want %#v", got, want)
	}
}
