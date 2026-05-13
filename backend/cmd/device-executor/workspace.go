package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/device"
	workspacepreview "github.com/pengmide/lumi/internal/workspace"
)

const remoteWorkspaceBufferLimit = 10 << 20

type workspaceFileInfo struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

type workspaceUploadedFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (c *Client) handleWorkspaceRequest(ctx context.Context, env Envelope) {
	payload, err := decodePayload[WorkspaceRequestPayload](env)
	if err != nil {
		c.sendWorkspaceError(env, "invalid_payload", err.Error())
		return
	}
	if strings.TrimSpace(payload.WorkspacePath) == "" {
		c.sendWorkspaceError(env, "invalid_path", "workspacePath is required")
		return
	}

	result, err := c.executeWorkspaceRequest(ctx, env.Type, payload)
	if err != nil {
		code, message := workspaceError(err)
		c.sendWorkspaceError(env, code, message)
		return
	}
	c.sendWorkspaceOK(env, result)
}

func (c *Client) executeWorkspaceRequest(ctx context.Context, typ MessageType, payload WorkspaceRequestPayload) (any, error) {
	switch typ {
	case MsgWorkspaceTree:
		tree, err := c.files.ListTreeDirectory(payload.WorkspacePath, payload.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tree": tree}, nil
	case MsgWorkspaceFiles:
		limit := payload.Limit
		if limit <= 0 {
			limit = 50
		}
		files, err := listRemoteWorkspaceFiles(payload.WorkspacePath, payload.Query, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"files": files}, nil
	case MsgWorkspaceMeta:
		meta, err := c.files.StatFile(payload.WorkspacePath, payload.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"meta": meta}, nil
	case MsgWorkspaceText:
		textFile, err := c.files.ReadTextFile(payload.WorkspacePath, payload.Path)
		if err != nil {
			return nil, err
		}
		return textFile, nil
	case MsgWorkspaceBuffer:
		return readRemoteWorkspaceBuffer(c.files, payload.WorkspacePath, payload.Path)
	case MsgWorkspaceChanges:
		changes, err := c.diffs.ListChanges(payload.WorkspacePath)
		if err != nil {
			return nil, err
		}
		return map[string]any{"changes": changes}, nil
	case MsgWorkspaceDiff:
		diff, err := c.diffs.UnifiedDiff(payload.WorkspacePath, payload.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": payload.Path, "content": diff}, nil
	case MsgWorkspaceUpload:
		return writeRemoteWorkspaceUploads(ctx, c.files, payload.WorkspacePath, payload.Files)
	case MsgWorkspaceCleanup:
		if err := os.RemoveAll(filepath.Join(payload.WorkspacePath, ".lumi-uploads")); err != nil {
			return nil, err
		}
		return map[string]any{"success": true}, nil
	default:
		return nil, fmt.Errorf("unsupported workspace request: %s", typ)
	}
}

func readRemoteWorkspaceBuffer(files *workspacepreview.Service, root string, relativePath string) (any, error) {
	resolved, err := files.ResolveFile(root, relativePath)
	if err != nil {
		return nil, err
	}
	if resolved.Info.IsDir() {
		return nil, workspacepreview.ErrIsDirectory
	}
	if resolved.Info.Size() > remoteWorkspaceBufferLimit {
		return nil, workspacepreview.ErrUnsupportedTextFile
	}

	meta, err := files.StatFile(root, resolved.RelativePath)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(resolved.AbsolutePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, workspacepreview.ErrNotFound
		}
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, remoteWorkspaceBufferLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > remoteWorkspaceBufferLimit {
		return nil, workspacepreview.ErrUnsupportedTextFile
	}

	return map[string]any{
		"meta":    meta,
		"content": base64.StdEncoding.EncodeToString(data),
	}, nil
}

func writeRemoteWorkspaceUploads(ctx context.Context, files *workspacepreview.Service, root string, uploadFiles []WorkspaceUploadFile) (any, error) {
	if len(uploadFiles) == 0 {
		return nil, errors.New("no files uploaded")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, workspacepreview.ErrWorkspaceUnavailable
	}
	if info, err := os.Stat(rootAbs); err != nil || !info.IsDir() {
		return nil, workspacepreview.ErrWorkspaceUnavailable
	}

	uploadPath := filepath.Join(rootAbs, ".lumi-uploads")
	if err := os.MkdirAll(uploadPath, 0755); err != nil {
		return nil, err
	}

	uploaded := make([]workspaceUploadedFile, 0, len(uploadFiles))
	for _, upload := range uploadFiles {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		name := filepath.Base(upload.Name)
		if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
			name = "upload"
		}
		data, err := base64.StdEncoding.DecodeString(upload.Content)
		if err != nil {
			return nil, fmt.Errorf("invalid upload content: %w", err)
		}

		ext := filepath.Ext(name)
		baseName := strings.TrimSuffix(name, ext)
		uniqueName := fmt.Sprintf("%s_%d%s", baseName, time.Now().UnixNano(), ext)
		destPath := filepath.Join(uploadPath, uniqueName)
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return nil, err
		}
		resolved, err := files.ResolveFile(rootAbs, filepath.ToSlash(filepath.Join(".lumi-uploads", uniqueName)))
		if err != nil {
			return nil, err
		}
		uploaded = append(uploaded, workspaceUploadedFile{
			Name: name,
			Path: resolved.AbsolutePath,
			Size: int64(len(data)),
		})
	}

	return map[string]any{"success": true, "files": uploaded}, nil
}

func listRemoteWorkspaceFiles(root, query string, limit int) ([]workspaceFileInfo, error) {
	var files []workspaceFileInfo
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, workspacepreview.ErrWorkspaceUnavailable
	}
	query = strings.ToLower(query)
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, ".idea": true, ".vscode": true, "vendor": true,
		"dist": true, "build": true, "__pycache__": true, ".next": true, ".nuxt": true,
		"coverage": true, ".cache": true,
	}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil || relPath == "." {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") && !strings.Contains(strings.ToLower(name), query) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= limit {
			return filepath.SkipAll
		}
		if query != "" {
			lowerPath := strings.ToLower(relPath)
			lowerName := strings.ToLower(name)
			if !strings.Contains(lowerPath, query) && !strings.Contains(lowerName, query) {
				return nil
			}
		}
		files = append(files, workspaceFileInfo{
			Path:  filepath.ToSlash(relPath),
			Name:  name,
			IsDir: false,
		})
		return nil
	})

	return files, nil
}

func (c *Client) sendWorkspaceOK(env Envelope, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		c.sendWorkspaceError(env, "internal", err.Error())
		return
	}
	c.sendWorkspaceResponse(env, WorkspaceResponsePayload{OK: true, Payload: raw})
}

func (c *Client) sendWorkspaceError(env Envelope, code string, message string) {
	c.sendWorkspaceResponse(env, WorkspaceResponsePayload{
		OK:    false,
		Error: &device.ErrorPayload{Code: code, Message: message},
	})
}

func (c *Client) sendWorkspaceResponse(env Envelope, payload WorkspaceResponsePayload) {
	resp, err := device.NewEnvelope(MsgWorkspaceResponse, env.ID, c.cfg.DeviceID, "", payload)
	if err != nil {
		log.Printf("failed to encode workspace response: %v", err)
		return
	}
	if err := c.writeEnvelope(resp); err != nil {
		log.Printf("failed to send workspace response: %v", err)
	}
}

func workspaceError(err error) (string, string) {
	switch {
	case errors.Is(err, workspacepreview.ErrInvalidPath):
		return "invalid_path", "Invalid workspace path"
	case errors.Is(err, workspacepreview.ErrPathEscape):
		return "path_escape", "Invalid workspace path"
	case errors.Is(err, workspacepreview.ErrNotFound):
		return "not_found", "File not found"
	case errors.Is(err, workspacepreview.ErrIsDirectory):
		return "is_directory", "Directories cannot be previewed as files"
	case errors.Is(err, workspacepreview.ErrUnsupportedTextFile):
		return "unsupported", "File does not support preview"
	case errors.Is(err, workspacepreview.ErrWorkspaceUnavailable):
		return "unavailable", "Workspace unavailable"
	default:
		return "internal", err.Error()
	}
}
