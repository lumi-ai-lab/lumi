package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	sandboxdocker "github.com/pengmide/lumi/internal/sandbox/docker"
)

func TestResolveCredentialMountsUsesWritableClaudeRoot(t *testing.T) {
	home := t.TempDir()
	runtimeDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatalf("WriteFile(.claude.json) error = %v", err)
	}
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude) error = %v", err)
	}
	settingsPath := filepath.Join(home, ".config", "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"model":"test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(settings target) error = %v", err)
	}
	if err := os.Symlink(settingsPath, filepath.Join(claudeDir, "settings.json")); err != nil {
		t.Fatalf("Symlink(settings.json) error = %v", err)
	}

	mounts := resolveCredentialMountsFromHome(home, runtimeDir)

	claudeMount := findCredentialMount(mounts, "/root")
	if claudeMount == nil {
		t.Fatalf("claude root mount not found in %#v", mounts)
	}
	if claudeMount.ReadOnly {
		t.Fatalf("claude root mount ReadOnly = true, want false")
	}
	if claudeMount.Source == home {
		t.Fatalf("claude root mount source points at host home, want runtime copy")
	}

	data, err := os.ReadFile(filepath.Join(claudeMount.Source, ".claude.json"))
	if err != nil {
		t.Fatalf("ReadFile(runtime .claude.json copy) error = %v", err)
	}
	if string(data) != `{"projects":{}}` {
		t.Fatalf("runtime .claude.json copy = %q", data)
	}

	data, err = os.ReadFile(filepath.Join(claudeMount.Source, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile(runtime settings copy) error = %v", err)
	}
	if string(data) != `{"model":"test"}` {
		t.Fatalf("runtime settings copy = %q", data)
	}
	if _, err := os.Lstat(filepath.Join(claudeMount.Source, ".claude", "settings.json")); err != nil {
		t.Fatalf("Lstat(runtime settings copy) error = %v", err)
	} else if info, _ := os.Lstat(filepath.Join(claudeMount.Source, ".claude", "settings.json")); info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("runtime settings copy is symlink, want regular file")
	}
}

func TestResolveCredentialMountsSkipsClaudeRootWithoutClaudeFiles(t *testing.T) {
	home := t.TempDir()
	runtimeDir := t.TempDir()

	mounts := resolveCredentialMountsFromHome(home, runtimeDir)

	if mount := findCredentialMount(mounts, "/root"); mount != nil {
		t.Fatalf("claude root mount = %#v, want nil", mount)
	}
}

func TestResolveCredentialMountsUsesWritableCodexHome(t *testing.T) {
	home := t.TempDir()
	runtimeDir := t.TempDir()

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.codex) error = %v", err)
	}
	authPath := filepath.Join(codexDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(auth.json) error = %v", err)
	}
	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"test\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config.toml) error = %v", err)
	}

	mounts := resolveCredentialMountsFromHome(home, runtimeDir)

	codexMount := findCredentialMount(mounts, "/root/.codex")
	if codexMount == nil {
		t.Fatalf("codex home mount not found in %#v", mounts)
	}
	if codexMount.ReadOnly {
		t.Fatalf("codex home mount ReadOnly = true, want false")
	}
	if codexMount.Source == codexDir {
		t.Fatalf("codex home mount source points at host dir, want runtime copy")
	}

	data, err := os.ReadFile(filepath.Join(codexMount.Source, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(runtime config copy) error = %v", err)
	}
	if string(data) != "model = \"test\"\n" {
		t.Fatalf("runtime config copy = %q", data)
	}

	data, err = os.ReadFile(filepath.Join(codexMount.Source, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile(runtime auth copy) error = %v", err)
	}
	if string(data) != `{"token":"test"}` {
		t.Fatalf("runtime auth copy = %q", data)
	}
}

func TestResolveCredentialMountsCreatesWritableCodexHomeWithoutHostConfig(t *testing.T) {
	home := t.TempDir()
	runtimeDir := t.TempDir()

	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(.codex) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(auth.json) error = %v", err)
	}

	mounts := resolveCredentialMountsFromHome(home, runtimeDir)

	codexMount := findCredentialMount(mounts, "/root/.codex")
	if codexMount == nil {
		t.Fatalf("codex home mount not found in %#v", mounts)
	}
	if codexMount.ReadOnly {
		t.Fatalf("codex home mount ReadOnly = true, want false")
	}
	if _, err := os.Stat(filepath.Join(codexMount.Source, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("runtime config stat error = %v, want not exist", err)
	}
}

func TestSanitizeAgentsForCredentialMountsRemovesMountedClaudeCredentialEnv(t *testing.T) {
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"token":"test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(runtime auth.json) error = %v", err)
	}

	agents := []config.AgentConfig{
		{
			ID:      "claude",
			Command: "npx",
			Args:    []string{"@agentclientprotocol/claude-agent-acp@0.30.0"},
			Env: map[string]string{
				"ANTHROPIC_AUTH_TOKEN": "bad-token",
				"ANTHROPIC_BASE_URL":   "https://example.test",
			},
		},
		{
			ID:      "codex",
			Command: "npx",
			Args:    []string{"@zed-industries/codex-acp"},
			Env: map[string]string{
				"OPENAI_API_KEY":  "bad-token",
				"OPENAI_BASE_URL": "https://example.test/v1",
			},
		},
	}
	mounts := []sandboxdocker.CredentialMount{
		{Target: "/root"},
		{Source: codexHome, Target: "/root/.codex"},
	}

	got := sanitizeAgentsForCredentialMounts(agents, mounts)
	if _, ok := got[0].Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatalf("claude token env was not removed")
	}
	if got[0].Env["ANTHROPIC_BASE_URL"] == "" {
		t.Fatalf("claude base URL env should be preserved")
	}
	if got[1].Env["OPENAI_API_KEY"] != "bad-token" {
		t.Fatalf("codex API key should be preserved")
	}
	if got[1].Env["OPENAI_BASE_URL"] == "" {
		t.Fatalf("codex base URL env should be preserved")
	}
}

func TestSanitizeAgentsForCredentialMountsKeepsCodexAPIKeyWithMountedAuth(t *testing.T) {
	agents := []config.AgentConfig{
		{
			ID:      "codex",
			Command: "npx",
			Args:    []string{"@zed-industries/codex-acp"},
			Env: map[string]string{
				"OPENAI_API_KEY":  "api-key",
				"OPENAI_BASE_URL": "https://example.test/v1",
			},
		},
	}
	mounts := []sandboxdocker.CredentialMount{
		{Source: t.TempDir(), Target: "/root/.codex"},
	}

	got := sanitizeAgentsForCredentialMounts(agents, mounts)
	if got[0].Env["OPENAI_API_KEY"] != "api-key" {
		t.Fatalf("codex API key should be preserved")
	}
	if got[0].Env["OPENAI_BASE_URL"] == "" {
		t.Fatalf("codex base URL should be preserved")
	}
}

func findCredentialMount(mounts []sandboxdocker.CredentialMount, target string) *sandboxdocker.CredentialMount {
	for i := range mounts {
		if mounts[i].Target == target {
			return &mounts[i]
		}
	}
	return nil
}
