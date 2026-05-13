package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/device"
	workspacepreview "github.com/pengmide/lumi/internal/workspace"
)

const (
	maxUploadSize = 10 << 20 // 10MB
	uploadDir     = ".lumi-uploads"
)

// FileInfo represents a file in the workspace
type FileInfo struct {
	Path  string `json:"path"`  // Relative path from workspace root
	Name  string `json:"name"`  // File name
	IsDir bool   `json:"isDir"` // Is directory
}

func (s *Server) handleWorkspaceChanges(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), r.URL.Query().Get("workspaceId"), r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Changes []workspacepreview.Change `json:"changes"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceChanges, device.WorkspaceRequestPayload{}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		if response.Changes == nil {
			response.Changes = []workspacepreview.Change{}
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, map[string]any{"changes": response.Changes})
		return
	}

	changes, err := s.workspaceDiffs.ListChanges(runtimeInfo.WorkspacePath)
	if err != nil {
		writeError(w, "Failed to load workspace changes", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"changes": changes})
}

func (s *Server) handleWorkspaceDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), r.URL.Query().Get("workspaceId"), r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceDiff, device.WorkspaceRequestPayload{Path: r.URL.Query().Get("path")}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, response)
		return
	}

	relativePath := r.URL.Query().Get("path")
	diff, err := s.workspaceDiffs.UnifiedDiff(runtimeInfo.WorkspacePath, relativePath)
	if err != nil {
		writeWorkspacePreviewError(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"path":    relativePath,
		"content": diff,
	})
}

func (s *Server) handleWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), r.URL.Query().Get("workspaceId"), r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Tree []workspacepreview.TreeNode `json:"tree"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceTree, device.WorkspaceRequestPayload{Path: r.URL.Query().Get("path")}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		if response.Tree == nil {
			response.Tree = []workspacepreview.TreeNode{}
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, map[string]any{"tree": response.Tree})
		return
	}

	tree, err := s.workspaceSvc.ListTreeDirectory(runtimeInfo.WorkspacePath, r.URL.Query().Get("path"))
	if err != nil {
		writeWorkspacePreviewError(w, err)
		return
	}

	writeJSON(w, map[string]any{"tree": tree})
}

func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), r.URL.Query().Get("workspaceId"), r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var textFile workspacepreview.TextFile
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceText, device.WorkspaceRequestPayload{Path: r.URL.Query().Get("path")}, &textFile); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, map[string]any{
			"meta":      textFile.Meta,
			"content":   textFile.Content,
			"truncated": textFile.Truncated,
		})
		return
	}

	textFile, err := s.workspaceSvc.ReadTextFile(runtimeInfo.WorkspacePath, r.URL.Query().Get("path"))
	if err != nil {
		writeWorkspacePreviewError(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"meta":      textFile.Meta,
		"content":   textFile.Content,
		"truncated": textFile.Truncated,
	})
}

func (s *Server) handleWorkspaceFileMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), r.URL.Query().Get("workspaceId"), r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Meta workspacepreview.FileMeta `json:"meta"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceMeta, device.WorkspaceRequestPayload{Path: r.URL.Query().Get("path")}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, map[string]any{"meta": response.Meta})
		return
	}

	meta, err := s.workspaceSvc.StatFile(runtimeInfo.WorkspacePath, r.URL.Query().Get("path"))
	if err != nil {
		writeWorkspacePreviewError(w, err)
		return
	}

	writeJSON(w, map[string]any{"meta": meta})
}

func (s *Server) handleWorkspaceFileBuffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), r.URL.Query().Get("workspaceId"), r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		meta, data, err := s.deviceWorkspaceBuffer(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, r.URL.Query().Get("path"))
		if err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		s.touchResolvedRuntime(runtimeInfo)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if meta.MIME != "" {
			w.Header().Set("Content-Type", meta.MIME)
		}
		http.ServeContent(w, r, meta.Name, time.UnixMilli(meta.ModifiedAt), bytes.NewReader(data))
		return
	}

	resolvedFile, err := s.workspaceSvc.ResolveFile(runtimeInfo.WorkspacePath, r.URL.Query().Get("path"))
	if err != nil {
		writeWorkspacePreviewError(w, err)
		return
	}
	if resolvedFile.Info.IsDir() {
		writeWorkspacePreviewError(w, workspacepreview.ErrIsDirectory)
		return
	}

	meta, err := s.workspaceSvc.StatFile(runtimeInfo.WorkspacePath, resolvedFile.RelativePath)
	if err != nil {
		writeWorkspacePreviewError(w, err)
		return
	}

	file, err := os.Open(resolvedFile.AbsolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeWorkspacePreviewError(w, workspacepreview.ErrNotFound)
			return
		}
		writeError(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if meta.MIME != "" {
		w.Header().Set("Content-Type", meta.MIME)
	}

	http.ServeContent(w, r, meta.Name, resolvedFile.Info.ModTime(), file)
}

// handleWorkspaceFiles returns files in the current workspace
func (s *Server) handleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceID := r.URL.Query().Get("workspaceId")
	query := r.URL.Query().Get("q")
	limitStr := r.URL.Query().Get("limit")

	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), workspaceID, r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Files []FileInfo `json:"files"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceFiles, device.WorkspaceRequestPayload{
			Query: query,
			Limit: parseLimitOrDefault(limitStr, 50),
		}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		if response.Files == nil {
			response.Files = []FileInfo{}
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, map[string]any{"files": response.Files})
		return
	}

	limit := 50 // Default limit
	if limitStr != "" {
		if n, err := parseInt(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	workspacePath := runtimeInfo.WorkspacePath
	if workspacePath == "" || workspacePath == "." {
		writeJSON(w, map[string]any{"files": []FileInfo{}})
		return
	}

	files := listWorkspaceFiles(workspacePath, query, limit)
	writeJSON(w, map[string]any{"files": files})
}

// listWorkspaceFiles walks the workspace and returns matching files
func listWorkspaceFiles(root, query string, limit int) []FileInfo {
	var files []FileInfo
	query = strings.ToLower(query)

	// Skip these directories
	skipDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		".idea":        true,
		".vscode":      true,
		"vendor":       true,
		"dist":         true,
		"build":        true,
		"__pycache__":  true,
		".next":        true,
		".nuxt":        true,
		"coverage":     true,
		".cache":       true,
	}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Get relative path
		relPath, err := filepath.Rel(root, path)
		if err != nil || relPath == "." {
			return nil
		}

		// Skip hidden files/dirs (except query matches)
		name := info.Name()
		if strings.HasPrefix(name, ".") && !strings.Contains(strings.ToLower(name), query) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip certain directories
		if info.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil // Don't add directories to results
		}

		// Check limit
		if len(files) >= limit {
			return filepath.SkipAll
		}

		// Match query (case insensitive)
		if query != "" {
			lowerPath := strings.ToLower(relPath)
			lowerName := strings.ToLower(name)
			if !strings.Contains(lowerPath, query) && !strings.Contains(lowerName, query) {
				return nil
			}
		}

		// Use forward slashes for consistency
		relPath = filepath.ToSlash(relPath)

		files = append(files, FileInfo{
			Path:  relPath,
			Name:  name,
			IsDir: info.IsDir(),
		})

		return nil
	})

	return files
}

func parseInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &pathError{msg: "invalid number"}
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func parseLimitOrDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := parseInt(s)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func writeWorkspacePreviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workspacepreview.ErrInvalidPath), errors.Is(err, workspacepreview.ErrPathEscape):
		writeError(w, "Invalid workspace path", http.StatusBadRequest)
	case errors.Is(err, workspacepreview.ErrNotFound):
		writeError(w, "File not found", http.StatusNotFound)
	case errors.Is(err, workspacepreview.ErrIsDirectory):
		writeError(w, "Directories cannot be previewed as files", http.StatusBadRequest)
	case errors.Is(err, workspacepreview.ErrUnsupportedTextFile):
		writeError(w, "File does not support text preview", http.StatusUnsupportedMediaType)
	default:
		writeError(w, "Failed to read workspace file", http.StatusInternalServerError)
	}
}

// UploadedFile represents an uploaded file
type UploadedFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeError(w, "File too large (max 10MB)", http.StatusBadRequest)
		return
	}

	// Get workspace ID from form
	workspaceID := r.FormValue("workspaceId")
	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), workspaceID, r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		fileHeaders := r.MultipartForm.File["files"]
		if len(fileHeaders) == 0 {
			writeError(w, "No files uploaded", http.StatusBadRequest)
			return
		}
		uploadFiles := make([]device.WorkspaceUploadFile, 0, len(fileHeaders))
		for _, fileHeader := range fileHeaders {
			file, err := fileHeader.Open()
			if err != nil {
				writeError(w, "Failed to read uploaded file", http.StatusInternalServerError)
				return
			}
			data, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil {
				writeError(w, "Failed to read uploaded file", http.StatusInternalServerError)
				return
			}
			uploadFiles = append(uploadFiles, device.WorkspaceUploadFile{
				Name:    fileHeader.Filename,
				Content: base64.StdEncoding.EncodeToString(data),
			})
		}
		var response struct {
			Success bool           `json:"success"`
			Files   []UploadedFile `json:"files"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceUpload, device.WorkspaceRequestPayload{Files: uploadFiles}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, response)
		return
	}

	workspacePath := runtimeInfo.WorkspacePath

	// Create upload directory
	uploadPath := filepath.Join(workspacePath, uploadDir)
	if err := os.MkdirAll(uploadPath, 0755); err != nil {
		writeError(w, "Failed to create upload directory", http.StatusInternalServerError)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeError(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	uploadedFiles := make([]UploadedFile, 0, len(files))

	for _, fileHeader := range files {
		// Open uploaded file
		file, err := fileHeader.Open()
		if err != nil {
			writeError(w, "Failed to read uploaded file", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Generate unique filename to avoid conflicts
		ext := filepath.Ext(fileHeader.Filename)
		baseName := strings.TrimSuffix(fileHeader.Filename, ext)
		uniqueName := fmt.Sprintf("%s_%d%s", baseName, time.Now().UnixNano(), ext)
		destPath := filepath.Join(uploadPath, uniqueName)

		// Create destination file
		dst, err := os.Create(destPath)
		if err != nil {
			writeError(w, "Failed to save file", http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		// Copy file content
		size, err := io.Copy(dst, file)
		if err != nil {
			writeError(w, "Failed to save file", http.StatusInternalServerError)
			return
		}

		uploadedFiles = append(uploadedFiles, UploadedFile{
			Name: fileHeader.Filename,
			Path: destPath,
			Size: size,
		})
	}

	writeJSON(w, map[string]any{
		"success": true,
		"files":   uploadedFiles,
	})
}

func (s *Server) handleFileCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeError(w, "Invalid request", http.StatusBadRequest)
		return
	}
	runtimeInfo, err := s.resolveWorkspaceRuntime(r.Context(), data.WorkspaceID, r)
	if err != nil {
		writeResolvedRuntimeError(w, err)
		return
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Success bool `json:"success"`
		}
		if err := s.deviceWorkspacePayload(r.Context(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceCleanup, device.WorkspaceRequestPayload{}, &response); err != nil {
			writeRuntimeWorkspaceError(w, runtimeInfo, err)
			return
		}
		s.touchResolvedRuntime(runtimeInfo)
		writeJSON(w, response)
		return
	}

	workspacePath := runtimeInfo.WorkspacePath
	uploadPath := filepath.Join(workspacePath, uploadDir)

	// Remove upload directory and all contents
	if err := os.RemoveAll(uploadPath); err != nil && !os.IsNotExist(err) {
		writeError(w, "Failed to cleanup files", http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"success": true})
}

// CleanupUploads removes the upload directory for a workspace
func (s *Server) CleanupUploads(workspaceID string) error {
	runtimeInfo, err := s.resolveWorkspaceRuntime(context.Background(), workspaceID, nil)
	if err != nil {
		return err
	}
	if runtimeInfo.Mode != "local" {
		var response struct {
			Success bool `json:"success"`
		}
		if err := s.deviceWorkspacePayload(context.Background(), runtimeInfo.DeviceID, runtimeInfo.WorkspacePath, device.MsgWorkspaceCleanup, device.WorkspaceRequestPayload{}, &response); err != nil {
			return err
		}
		s.touchResolvedRuntime(runtimeInfo)
		return nil
	}
	workspacePath := runtimeInfo.WorkspacePath
	uploadPath := filepath.Join(workspacePath, uploadDir)
	return os.RemoveAll(uploadPath)
}
