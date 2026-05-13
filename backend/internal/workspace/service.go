package workspace

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type PreviewKind string

const (
	PreviewKindCode        PreviewKind = "code"
	PreviewKindMarkdown    PreviewKind = "markdown"
	PreviewKindImage       PreviewKind = "image"
	PreviewKindPDF         PreviewKind = "pdf"
	PreviewKindHTML        PreviewKind = "html"
	PreviewKindUnsupported PreviewKind = "unsupported"
)

const maxTextPreviewBytes = 1024 * 1024

var (
	ErrWorkspaceUnavailable = errors.New("workspace unavailable")
	ErrInvalidPath          = errors.New("invalid workspace path")
	ErrPathEscape           = errors.New("workspace path escapes root")
	ErrNotFound             = errors.New("workspace file not found")
	ErrIsDirectory          = errors.New("workspace path is a directory")
	ErrUnsupportedTextFile  = errors.New("workspace file does not support text preview")

	skippedWorkspaceDirs = map[string]struct{}{
		"vendor": {}, "build": {}, "dist": {}, "coverage": {}, "node_modules": {}, ".git": {}, ".idea": {}, ".vscode": {},
		"__pycache__": {}, ".next": {}, ".nuxt": {}, ".cache": {}, ".lumi-uploads": {},
	}
	codePreviewExtensions = map[string]struct{}{
		".txt": {}, ".log": {}, ".sh": {}, ".bash": {}, ".zsh": {}, ".go": {}, ".rs": {}, ".py": {}, ".java": {}, ".kt": {},
		".js": {}, ".jsx": {}, ".ts": {}, ".tsx": {}, ".json": {}, ".jsonc": {}, ".yaml": {}, ".yml": {}, ".toml": {},
		".ini": {}, ".cfg": {}, ".conf": {}, ".css": {}, ".scss": {}, ".less": {}, ".xml": {},
		".svg": {}, ".sql": {}, ".c": {}, ".cc": {}, ".cpp": {}, ".h": {}, ".hpp": {}, ".m": {}, ".mm": {}, ".swift": {},
		".rb": {}, ".php": {}, ".dart": {}, ".vue": {}, ".svelte": {}, ".lock": {},
	}
	markdownExtensions = map[string]struct{}{
		".md": {}, ".markdown": {}, ".mdown": {}, ".mkd": {}, ".mdx": {},
	}
	imagePreviewExtensions = map[string]struct{}{
		".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".bmp": {}, ".ico": {}, ".svg": {}, ".avif": {}, ".tif": {}, ".tiff": {},
	}
)

type Service struct{}

type TreeNode struct {
	Path        string      `json:"path"`
	Name        string      `json:"name"`
	IsDir       bool        `json:"isDir"`
	PreviewKind PreviewKind `json:"previewKind,omitempty"`
	Children    []TreeNode  `json:"children,omitempty"`
	Loaded      bool        `json:"loaded,omitempty"`
}

type FileMeta struct {
	Path        string      `json:"path"`
	Name        string      `json:"name"`
	Size        int64       `json:"size"`
	ModifiedAt  int64       `json:"modifiedAt"`
	MIME        string      `json:"mime,omitempty"`
	PreviewKind PreviewKind `json:"previewKind"`
}

type TextFile struct {
	Meta      FileMeta `json:"meta"`
	Content   string   `json:"content"`
	Truncated bool     `json:"truncated,omitempty"`
}

type ResolvedFile struct {
	RootPath     string
	RootRealPath string
	AbsolutePath string
	RelativePath string
	Name         string
	Info         os.FileInfo
}

func NewService() *Service {
	return &Service{}
}

func (s *Service) ListTree(root string) ([]TreeNode, error) {
	return s.ListTreeDirectory(root, "")
}

func (s *Service) ListTreeDirectory(root string, relativePath string) ([]TreeNode, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrWorkspaceUnavailable
	}

	info, err := os.Stat(rootAbs)
	if err != nil || !info.IsDir() {
		return nil, ErrWorkspaceUnavailable
	}

	resolvedPath := rootAbs
	normalized := ""
	if strings.TrimSpace(relativePath) != "" {
		resolved, err := s.ResolveFile(root, relativePath)
		if err != nil {
			return nil, err
		}
		if !resolved.Info.IsDir() {
			return nil, ErrIsDirectory
		}
		resolvedPath = resolved.AbsolutePath
		normalized = resolved.RelativePath
	}

	return s.listDirectory(rootAbs, resolvedPath, normalized)
}

func (s *Service) listDirectory(rootAbs string, dirPath string, relativePath string) ([]TreeNode, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	nodes := make([]TreeNode, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if shouldSkipWorkspaceEntry(name, entry.IsDir()) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		entryRelative := filepath.Join(relativePath, name)
		node := TreeNode{
			Path:  filepath.ToSlash(entryRelative),
			Name:  name,
			IsDir: entry.IsDir(),
		}

		if entry.IsDir() {
			node.Loaded = false
		} else {
			node.PreviewKind = DetectPreviewKind(name)
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

func (s *Service) ResolveFile(root string, relativePath string) (*ResolvedFile, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, ErrWorkspaceUnavailable
	}

	rootInfo, err := os.Stat(rootAbs)
	if err != nil || !rootInfo.IsDir() {
		return nil, ErrWorkspaceUnavailable
	}

	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, ErrWorkspaceUnavailable
	}

	normalized, err := normalizeRelativePath(relativePath)
	if err != nil {
		return nil, err
	}

	absoluteCandidate := filepath.Join(rootAbs, normalized)
	realCandidate, err := filepath.EvalSymlinks(absoluteCandidate)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	if !isWithinRoot(rootReal, realCandidate) {
		return nil, ErrPathEscape
	}

	info, err := os.Stat(realCandidate)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &ResolvedFile{
		RootPath:     rootAbs,
		RootRealPath: rootReal,
		AbsolutePath: realCandidate,
		RelativePath: filepath.ToSlash(normalized),
		Name:         filepath.Base(normalized),
		Info:         info,
	}, nil
}

func (s *Service) StatFile(root string, relativePath string) (FileMeta, error) {
	resolved, err := s.ResolveFile(root, relativePath)
	if err != nil {
		return FileMeta{}, err
	}
	if resolved.Info.IsDir() {
		return FileMeta{}, ErrIsDirectory
	}

	return buildFileMeta(resolved)
}

func (s *Service) ReadTextFile(root string, relativePath string) (TextFile, error) {
	resolved, err := s.ResolveFile(root, relativePath)
	if err != nil {
		return TextFile{}, err
	}
	if resolved.Info.IsDir() {
		return TextFile{}, ErrIsDirectory
	}

	meta, err := buildFileMeta(resolved)
	if err != nil {
		return TextFile{}, err
	}
	if meta.PreviewKind != PreviewKindCode && meta.PreviewKind != PreviewKindMarkdown {
		if meta.PreviewKind != PreviewKindHTML {
			return TextFile{}, ErrUnsupportedTextFile
		}
	}

	file, err := os.Open(resolved.AbsolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return TextFile{}, ErrNotFound
		}
		return TextFile{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxTextPreviewBytes+1))
	if err != nil {
		return TextFile{}, err
	}

	truncated := false
	if len(data) > maxTextPreviewBytes {
		truncated = true
		data = data[:maxTextPreviewBytes]
	}

	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	if !utf8.Valid(data) {
		return TextFile{}, ErrUnsupportedTextFile
	}

	return TextFile{
		Meta:      meta,
		Content:   string(data),
		Truncated: truncated,
	}, nil
}

func DetectPreviewKind(name string) PreviewKind {
	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := markdownExtensions[ext]; ok {
		return PreviewKindMarkdown
	}
	if _, ok := imagePreviewExtensions[ext]; ok {
		return PreviewKindImage
	}
	if ext == ".pdf" {
		return PreviewKindPDF
	}
	if ext == ".html" || ext == ".htm" {
		return PreviewKindHTML
	}
	if _, ok := codePreviewExtensions[ext]; ok {
		return PreviewKindCode
	}

	return PreviewKindUnsupported
}

func buildFileMeta(resolved *ResolvedFile) (FileMeta, error) {
	mimeType, err := detectMimeType(resolved.AbsolutePath, resolved.Name)
	if err != nil {
		return FileMeta{}, err
	}

	return FileMeta{
		Path:        resolved.RelativePath,
		Name:        resolved.Name,
		Size:        resolved.Info.Size(),
		ModifiedAt:  resolved.Info.ModTime().UnixMilli(),
		MIME:        mimeType,
		PreviewKind: DetectPreviewKind(resolved.Name),
	}, nil
}

func detectMimeType(path string, name string) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".markdown", ".mdown", ".mkd", ".mdx":
		return "text/markdown; charset=utf-8", nil
	case ".json":
		return "application/json; charset=utf-8", nil
	case ".yaml", ".yml":
		return "application/yaml; charset=utf-8", nil
	}

	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		if strings.HasPrefix(mimeType, "text/") && !strings.Contains(mimeType, "charset=") {
			return mimeType + "; charset=utf-8", nil
		}
		return mimeType, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	header := make([]byte, 512)
	n, err := file.Read(header)
	if err != nil && err != io.EOF {
		return "", err
	}

	mimeType := http.DetectContentType(header[:n])
	if strings.HasPrefix(mimeType, "text/") && !strings.Contains(mimeType, "charset=") {
		return mimeType + "; charset=utf-8", nil
	}

	return mimeType, nil
}

func normalizeRelativePath(relativePath string) (string, error) {
	if strings.TrimSpace(relativePath) == "" {
		return "", ErrInvalidPath
	}

	normalized := filepath.Clean(filepath.FromSlash(relativePath))
	if normalized == "." || normalized == string(filepath.Separator) {
		return "", ErrInvalidPath
	}
	if strings.HasPrefix(normalized, ".."+string(filepath.Separator)) || normalized == ".." {
		return "", ErrPathEscape
	}

	return normalized, nil
}

func isWithinRoot(rootPath string, candidatePath string) bool {
	relative, err := filepath.Rel(rootPath, candidatePath)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}

	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func shouldSkipWorkspaceEntry(name string, isDir bool) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if isDir {
		_, skipped := skippedWorkspaceDirs[name]
		return skipped
	}

	return false
}
