package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestListTreeSkipsHiddenAndBuildsKinds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "src", "main.ts"), "export const ready = true\n")
	mustWriteFile(t, filepath.Join(root, "docs", "readme.md"), "# hello\n")
	mustWriteFile(t, filepath.Join(root, "public", "index.html"), "<!DOCTYPE html><html></html>\n")
	mustWriteFile(t, filepath.Join(root, "assets", "logo.png"), "png")
	mustWriteFile(t, filepath.Join(root, ".env"), "SECRET=1\n")
	mustWriteFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")

	service := NewService()
	tree, err := service.ListTree(root)
	if err != nil {
		t.Fatalf("ListTree() error = %v", err)
	}

	if len(tree) != 4 {
		t.Fatalf("expected 4 top-level nodes, got %d", len(tree))
	}

	srcNode := findTreeNode(tree, "src")
	if srcNode == nil || !srcNode.IsDir {
		t.Fatalf("expected src folder to exist in tree")
	}
	if len(srcNode.Children) != 0 {
		t.Fatalf("expected src folder children to be loaded lazily, got %#v", srcNode.Children)
	}

	srcTree, err := service.ListTreeDirectory(root, "src")
	if err != nil {
		t.Fatalf("ListTreeDirectory(src) error = %v", err)
	}
	mainNode := findTreeNode(srcTree, "src/main.ts")
	if mainNode == nil {
		t.Fatalf("expected src/main.ts to exist in src tree")
	}
	if mainNode.PreviewKind != PreviewKindCode {
		t.Fatalf("expected src/main.ts preview kind %q, got %q", PreviewKindCode, mainNode.PreviewKind)
	}

	docsTree, err := service.ListTreeDirectory(root, "docs")
	if err != nil {
		t.Fatalf("ListTreeDirectory(docs) error = %v", err)
	}
	mdNode := findTreeNode(docsTree, "docs/readme.md")
	if mdNode == nil || mdNode.PreviewKind != PreviewKindMarkdown {
		t.Fatalf("expected docs/readme.md markdown preview kind, got %#v", mdNode)
	}

	publicTree, err := service.ListTreeDirectory(root, "public")
	if err != nil {
		t.Fatalf("ListTreeDirectory(public) error = %v", err)
	}
	htmlNode := findTreeNode(publicTree, "public/index.html")
	if htmlNode == nil || htmlNode.PreviewKind != PreviewKindHTML {
		t.Fatalf("expected public/index.html html preview kind, got %#v", htmlNode)
	}

	if hiddenNode := findTreeNode(tree, ".env"); hiddenNode != nil {
		t.Fatalf("expected hidden file to be skipped")
	}
	if gitNode := findTreeNode(tree, ".git/HEAD"); gitNode != nil {
		t.Fatalf("expected .git contents to be skipped")
	}
}

func TestListTreeDirectorySkipsLargeGeneratedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "src", "main.ts"), "export const ready = true\n")
	mustWriteFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "module.exports = {}\n")
	mustWriteFile(t, filepath.Join(root, "dist", "bundle.js"), "compiled\n")

	service := NewService()
	tree, err := service.ListTreeDirectory(root, "")
	if err != nil {
		t.Fatalf("ListTreeDirectory(root) error = %v", err)
	}

	if node := findTreeNode(tree, "src"); node == nil {
		t.Fatalf("expected src folder to be listed")
	}
	if node := findTreeNode(tree, "node_modules"); node != nil {
		t.Fatalf("expected node_modules to be skipped")
	}
	if node := findTreeNode(tree, "dist"); node != nil {
		t.Fatalf("expected dist to be skipped")
	}
}

func TestResolveFileRejectsTraversal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "src", "main.ts"), "const ready = true\n")

	service := NewService()
	_, err := service.ResolveFile(root, "../outside.txt")
	if err != ErrPathEscape {
		t.Fatalf("ResolveFile() error = %v, want %v", err, ErrPathEscape)
	}
}

func TestReadTextFileTruncatesLargeFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	content := strings.Repeat("a", maxTextPreviewBytes+64)
	mustWriteFile(t, filepath.Join(root, "src", "huge.ts"), content)

	service := NewService()
	textFile, err := service.ReadTextFile(root, "src/huge.ts")
	if err != nil {
		t.Fatalf("ReadTextFile() error = %v", err)
	}
	if !textFile.Truncated {
		t.Fatalf("expected ReadTextFile() to report truncated content")
	}
	if len(textFile.Content) != maxTextPreviewBytes {
		t.Fatalf("expected truncated content length %d, got %d", maxTextPreviewBytes, len(textFile.Content))
	}
}

func TestResolveFileRejectsSymlinkOutsideRoot(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is permission-sensitive on windows")
	}

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.ts")
	mustWriteFile(t, outside, "const outside = true\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked.ts")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	service := NewService()
	_, err := service.ResolveFile(root, "linked.ts")
	if err != ErrPathEscape {
		t.Fatalf("ResolveFile() error = %v, want %v", err, ErrPathEscape)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func findTreeNode(nodes []TreeNode, targetPath string) *TreeNode {
	for i := range nodes {
		node := &nodes[i]
		if node.Path == targetPath {
			return node
		}
		if child := findTreeNode(node.Children, targetPath); child != nil {
			return child
		}
	}

	return nil
}
