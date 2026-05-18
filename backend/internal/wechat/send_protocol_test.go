package wechat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSendProtocolSupportsBlocksAndRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()
	okFile := filepath.Join(root, "out", "chart.png")
	if err := os.MkdirAll(filepath.Dir(okFile), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(okFile, []byte("png"), 0o644); err != nil {
		t.Fatalf("WriteFile(okFile) error = %v", err)
	}

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link-out.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	largeFile := filepath.Join(root, "too-large.bin")
	file, err := os.Create(largeFile)
	if err != nil {
		t.Fatalf("Create(large) error = %v", err)
	}
	if err := file.Truncate(maxMediaBytes + 1); err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}
	file.Close()

	content := strings.Join([]string{
		"done",
		"[LUMI_WECHAT_SEND]",
		`{"type":"image","path":"out/chart.png","caption":"chart"}`,
		"[/LUMI_WECHAT_SEND]",
		"[LUMI_WECHAT_SEND]",
		`{"type":"file","path":"link-out.txt"}`,
		"[/LUMI_WECHAT_SEND]",
		"[LUMI_WECHAT_SEND]",
		`{"type":"file","path":"too-large.bin"}`,
		"[/LUMI_WECHAT_SEND]",
		"[LUMI_WECHAT_SEND]",
		`not-json`,
		"[/LUMI_WECHAT_SEND]",
	}, "\n")

	parsed := ParseSendProtocol(content, root)
	if parsed.VisibleText != "done" {
		t.Fatalf("VisibleText = %q, want done", parsed.VisibleText)
	}
	if len(parsed.Actions) != 1 {
		t.Fatalf("len(Actions) = %d, want 1, failures=%v", len(parsed.Actions), parsed.Failures)
	}
	canonicalOKFile, err := filepath.EvalSymlinks(okFile)
	if err != nil {
		t.Fatalf("EvalSymlinks(okFile) error = %v", err)
	}
	if parsed.Actions[0].ResolvedPath != canonicalOKFile {
		t.Fatalf("ResolvedPath = %q, want %q", parsed.Actions[0].ResolvedPath, canonicalOKFile)
	}
	if len(parsed.Failures) != 3 {
		t.Fatalf("len(Failures) = %d, want 3 (%v)", len(parsed.Failures), parsed.Failures)
	}
}

func TestParseSendProtocolMapsSandboxWorkspacePath(t *testing.T) {
	root := t.TempDir()
	okFile := filepath.Join(root, "cmr-qr.png")
	if err := os.WriteFile(okFile, []byte("png"), 0o644); err != nil {
		t.Fatalf("WriteFile(okFile) error = %v", err)
	}

	content := strings.Join([]string{
		"[LUMI_WECHAT_SEND]",
		`{"type":"image","path":"/workspace/cmr-qr.png"}`,
		"[/LUMI_WECHAT_SEND]",
	}, "\n")

	parsed := ParseSendProtocol(content, root)
	if len(parsed.Failures) != 0 {
		t.Fatalf("Failures = %v, want none", parsed.Failures)
	}
	if len(parsed.Actions) != 1 {
		t.Fatalf("len(Actions) = %d, want 1", len(parsed.Actions))
	}
	canonicalOKFile, err := filepath.EvalSymlinks(okFile)
	if err != nil {
		t.Fatalf("EvalSymlinks(okFile) error = %v", err)
	}
	if parsed.Actions[0].ResolvedPath != canonicalOKFile {
		t.Fatalf("ResolvedPath = %q, want %q", parsed.Actions[0].ResolvedPath, canonicalOKFile)
	}
}
