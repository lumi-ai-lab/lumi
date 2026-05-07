package wecom

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
	if err := os.WriteFile(okFile, []byte("pngdata"), 0o644); err != nil {
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

	smallFile := filepath.Join(root, "small.txt")
	if err := os.WriteFile(smallFile, []byte("aaaaa"), 0o644); err != nil {
		t.Fatalf("WriteFile(smallFile) error = %v", err)
	}

	content := strings.Join([]string{
		"done",
		"[LUMI_WECOM_SEND]",
		`{"type":"image","path":"out/chart.png","caption":"chart"}`,
		"[/LUMI_WECOM_SEND]",
		"[LUMI_WECOM_SEND]",
		`{"type":"file","path":"link-out.txt"}`,
		"[/LUMI_WECOM_SEND]",
		"[LUMI_WECOM_SEND]",
		`{"type":"file","path":"too-large.bin"}`,
		"[/LUMI_WECOM_SEND]",
		"[LUMI_WECOM_SEND]",
		`{"type":"file","path":"small.txt"}`,
		"[/LUMI_WECOM_SEND]",
		"[LUMI_WECOM_SEND]",
		`not-json`,
		"[/LUMI_WECOM_SEND]",
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
	if len(parsed.Failures) != 4 {
		t.Fatalf("len(Failures) = %d, want 4 (%v)", len(parsed.Failures), parsed.Failures)
	}
	if !strings.Contains(strings.Join(parsed.Failures, "\n"), "small.txt（file must be larger than 5 bytes）") {
		t.Fatalf("Failures missing small file error: %v", parsed.Failures)
	}
}
