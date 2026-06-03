package skillsync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/agentmode"
)

func TestUserSkillDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")

	cases := map[Backend]string{
		agentmode.BackendClaude: filepath.Join(home, ".claude", "skills"),
		agentmode.BackendCodex:  filepath.Join(home, ".codex", "skills"),
		agentmode.BackendQwen:   filepath.Join(home, ".qwen", "skills"),
		agentmode.BackendPi:     filepath.Join(home, ".pi", "agent", "skills"),
	}
	for backend, want := range cases {
		got, err := UserSkillDir(home, backend)
		if err != nil {
			t.Fatalf("backend %s: %v", backend, err)
		}
		if got != want {
			t.Fatalf("UserSkillDir(%s) = %q, want %q", backend, got, want)
		}
	}
}

func TestUserSkillDirHonorsEnv(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	got, err := UserSkillDir(home, agentmode.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(custom, "skills") {
		t.Fatalf("got %q", got)
	}
}

func TestUserSkillDirFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got, err := UserSkillDir("", agentmode.BackendClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, ".claude", "skills") {
		t.Fatalf("got %q", got)
	}
}

func TestAppKey(t *testing.T) {
	if AppKey(agentmode.BackendClaude) != "claude" || AppKey(agentmode.BackendCodex) != "codex" || AppKey(agentmode.BackendQwen) != "qwen" || AppKey(agentmode.BackendPi) != "pi" {
		t.Fatal("AppKey mismatch")
	}
	if AppKey(agentmode.BackendUnknown) != "" {
		t.Fatal("unknown backend should yield empty key")
	}
}

func TestSupportedBackendsCover(t *testing.T) {
	got := SupportedBackends()
	want := []Backend{agentmode.BackendClaude, agentmode.BackendCodex, agentmode.BackendQwen, agentmode.BackendPi}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: %d vs %d", len(got), len(want))
	}
	for i, b := range want {
		if got[i] != b {
			t.Fatalf("at %d: got %q want %q", i, got[i], b)
		}
	}
}

// Sanity check unused import removal kept the package compiling without
// reading the lockfile from a test-controlled tempdir.
func TestPathsEnsureDirInteraction(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
