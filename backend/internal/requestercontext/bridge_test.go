package requestercontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionDir(t *testing.T) {
	root := t.TempDir()
	got, err := SessionDir(root, "workspace-1", "claude")
	if err != nil {
		t.Fatalf("SessionDir() error = %v", err)
	}
	want := filepath.Join(root, "workspace-1", "claude")
	if got != want {
		t.Fatalf("SessionDir() = %q, want %q", got, want)
	}
}

func TestSessionDirRejectsUnsafeSegments(t *testing.T) {
	tests := []struct {
		name        string
		workspaceID string
		agentID     string
	}{
		{name: "empty workspace", workspaceID: "", agentID: "claude"},
		{name: "dot workspace", workspaceID: ".", agentID: "claude"},
		{name: "parent workspace", workspaceID: "..", agentID: "claude"},
		{name: "workspace slash", workspaceID: "one/two", agentID: "claude"},
		{name: "workspace backslash", workspaceID: `one\two`, agentID: "claude"},
		{name: "absolute workspace", workspaceID: filepath.Join(string(filepath.Separator), "tmp"), agentID: "claude"},
		{name: "empty agent", workspaceID: "workspace", agentID: ""},
		{name: "parent agent", workspaceID: "workspace", agentID: ".."},
		{name: "agent slash", workspaceID: "workspace", agentID: "one/two"},
		{name: "agent backslash", workspaceID: "workspace", agentID: `one\two`},
		{name: "nul agent", workspaceID: "workspace", agentID: "bad\x00agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := SessionDir(t.TempDir(), tt.workspaceID, tt.agentID); err == nil {
				t.Fatal("SessionDir() error = nil, want unsafe segment error")
			}
		})
	}
}

func TestSessionDirRejectsEmptyRoot(t *testing.T) {
	if _, err := SessionDir("", "workspace", "agent"); err == nil {
		t.Fatal("SessionDir() error = nil, want empty root error")
	}
}

func TestSessionFileName(t *testing.T) {
	sessionID := "ACP/session/../1"
	sum := sha256.Sum256([]byte(sessionID))
	want := hex.EncodeToString(sum[:]) + ".json"
	got, err := SessionFileName(sessionID)
	if err != nil {
		t.Fatalf("SessionFileName() error = %v", err)
	}
	if got != want {
		t.Fatalf("SessionFileName() = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, `/\`) {
		t.Fatalf("SessionFileName() = %q, contains a path separator", got)
	}
	if _, err := SessionFileName(""); err == nil {
		t.Fatal("SessionFileName(empty) error = nil")
	}
}

func TestFileBridgeWriteAndCleanup(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 7, 29, 8, 9, 10, 0, time.FixedZone("CST", 8*60*60))
	bridge, err := NewFileBridge(root, "workspace-1", "claude", WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatalf("NewFileBridge() error = %v", err)
	}

	path, cleanup, err := bridge.Write("session-1", testContext())
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	filename, _ := SessionFileName("session-1")
	if want := filepath.Join(root, "workspace-1", "claude", filename); path != want {
		t.Fatalf("Write() path = %q, want %q", path, want)
	}

	dirInfo, err := os.Stat(bridge.Dir())
	if err != nil {
		t.Fatalf("os.Stat(dir) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(file) error = %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if envelope.Version != CurrentEnvelopeVersion || envelope.RequesterContext.Version != CurrentContextVersion || envelope.WorkspaceID != "workspace-1" || envelope.AgentID != "claude" || envelope.SessionID != "session-1" {
		t.Fatalf("envelope identity = %#v", envelope)
	}
	if !envelope.IssuedAt.Equal(fixed) {
		t.Errorf("IssuedAt = %s, want %s", envelope.IssuedAt, fixed)
	}
	if want := fixed.Add(DefaultTTL); !envelope.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %s, want %s", envelope.ExpiresAt, want)
	}
	gotContextJSON, err := json.Marshal(envelope.RequesterContext)
	if err != nil {
		t.Fatalf("json.Marshal(envelope RequesterContext) error = %v", err)
	}
	wantContextJSON, err := json.Marshal(testContext())
	if err != nil {
		t.Fatalf("json.Marshal(want RequesterContext) error = %v", err)
	}
	if string(gotContextJSON) != string(wantContextJSON) {
		t.Errorf("RequesterContext JSON = %s, want %s", gotContextJSON, wantContextJSON)
	}

	entries, err := os.ReadDir(bridge.Dir())
	if err != nil {
		t.Fatalf("os.ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filename {
		t.Fatalf("directory entries = %#v, want only %q", entries, filename)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup() error = %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup() second call error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("os.Stat(cleaned path) error = %v, want not exist", err)
	}
}

func TestFileBridgeInScopeUsesStableDirectoryAndActualWorkspace(t *testing.T) {
	root := t.TempDir()
	first, err := NewFileBridgeInScope(root, "agents", "workspace-a", "claude")
	if err != nil {
		t.Fatalf("NewFileBridgeInScope(first) error = %v", err)
	}
	second, err := NewFileBridgeInScope(root, "agents", "workspace-b", "claude")
	if err != nil {
		t.Fatalf("NewFileBridgeInScope(second) error = %v", err)
	}
	wantDir := filepath.Join(root, "agents", "claude")
	if first.Dir() != wantDir || second.Dir() != wantDir {
		t.Fatalf("scoped bridge dirs = %q and %q, want %q", first.Dir(), second.Dir(), wantDir)
	}

	path, cleanup, err := second.Write("session-b", testContext())
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	defer cleanup()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if envelope.WorkspaceID != "workspace-b" || envelope.AgentID != "claude" {
		t.Fatalf("envelope identity = workspace %q agent %q", envelope.WorkspaceID, envelope.AgentID)
	}
}

func TestFileBridgeInScopeRejectsUnsafeScopeAndWorkspace(t *testing.T) {
	if _, err := NewFileBridgeInScope(t.TempDir(), "../agents", "workspace", "agent"); err == nil {
		t.Fatal("NewFileBridgeInScope(unsafe scope) error = nil")
	}
	if _, err := NewFileBridgeInScope(t.TempDir(), "agents", "../workspace", "agent"); err == nil {
		t.Fatal("NewFileBridgeInScope(unsafe workspace) error = nil")
	}
}

func TestFileBridgeCustomTTL(t *testing.T) {
	fixed := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	bridge, err := NewFileBridge(t.TempDir(), "workspace", "agent", WithTTL(time.Minute), WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatalf("NewFileBridge() error = %v", err)
	}
	path, cleanup, err := bridge.Write("session", testContext())
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	defer cleanup()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if want := fixed.Add(time.Minute); !envelope.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %s, want %s", envelope.ExpiresAt, want)
	}
}

func TestFileBridgeCleanupDoesNotDeleteNewerSessionFile(t *testing.T) {
	bridge, err := NewFileBridge(t.TempDir(), "workspace", "agent")
	if err != nil {
		t.Fatalf("NewFileBridge() error = %v", err)
	}
	path, oldCleanup, err := bridge.Write("same-session", testContext())
	if err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	_, newCleanup, err := bridge.Write("same-session", testContext())
	if err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	defer newCleanup()

	if err := oldCleanup(); err != nil {
		t.Fatalf("old cleanup() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("newer session file removed by stale cleanup: %v", err)
	}
}

func TestNewFileBridgeRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name   string
		option FileBridgeOption
	}{
		{name: "zero TTL", option: WithTTL(0)},
		{name: "negative TTL", option: WithTTL(-time.Second)},
		{name: "nil clock", option: WithClock(nil)},
		{name: "root reader GID", option: WithReaderGID(0)},
		{name: "nil option", option: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewFileBridge(t.TempDir(), "workspace", "agent", tt.option); err == nil {
				t.Fatal("NewFileBridge() error = nil, want invalid option error")
			}
		})
	}
}

func TestFileBridgeWriteRejectsEmptySession(t *testing.T) {
	bridge, err := NewFileBridge(t.TempDir(), "workspace", "agent")
	if err != nil {
		t.Fatalf("NewFileBridge() error = %v", err)
	}
	if _, _, err := bridge.Write("", testContext()); err == nil {
		t.Fatal("Write(empty session) error = nil")
	}
}

func TestPrivateFileBridgePreservesLegacyModeRepair(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewFileBridge(root, "workspace", "agent")
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := bridge.Write("session", testContext())
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	defer cleanup()
	after, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != 0o700 {
		t.Fatalf("legacy private root mode = %o, want 700", after.Mode().Perm())
	}
}

func TestFileBridgeReusesExactExistingDirectories(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "requester-context")
	agentDir := filepath.Join(root, "workspace", "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, filepath.Dir(agentDir), agentDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bridge, err := NewFileBridge(root, "workspace", "agent")
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := bridge.Write("session", testContext())
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	defer cleanup()
}

func TestFileBridgeRejectsSymlinkAtEveryManagedDirectoryLevel(t *testing.T) {
	for _, level := range []string{"root", "workspace", "agent"} {
		t.Run(level, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "requester-context")
			workspaceDir := filepath.Join(root, "workspace")
			agentDir := filepath.Join(workspaceDir, "agent")
			switch level {
			case "root":
				if err := os.Symlink(target, root); err != nil {
					t.Skipf("symlink is unavailable: %v", err)
				}
			case "workspace":
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, workspaceDir); err != nil {
					t.Skipf("symlink is unavailable: %v", err)
				}
			case "agent":
				if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, agentDir); err != nil {
					t.Skipf("symlink is unavailable: %v", err)
				}
			}
			bridge, err := NewFileBridge(root, "workspace", "agent")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := bridge.Write("session", testContext()); err == nil || !strings.Contains(err.Error(), "real directory") {
				t.Fatalf("Write() error = %v, want symlink rejection", err)
			}
		})
	}
}
