package wecom

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var wecomSendBlockRE = regexp.MustCompile(`(?s)\[LUMI_WECOM_SEND\]\s*(.*?)\s*\[/LUMI_WECOM_SEND\]`)

type SendAction struct {
	Type         string
	Path         string
	ResolvedPath string
	FileName     string
	Caption      string
}

type ParsedSendProtocol struct {
	VisibleText string
	Actions     []SendAction
	Failures    []string
}

func ParseSendProtocol(content, workspaceRoot string) ParsedSendProtocol {
	actions := make([]SendAction, 0)
	failures := make([]string, 0)

	visibleText := normalizeVisibleText(wecomSendBlockRE.ReplaceAllStringFunc(content, func(block string) string {
		match := wecomSendBlockRE.FindStringSubmatch(block)
		if len(match) < 2 {
			return ""
		}
		action, failure := parseAndResolveSendAction(match[1], workspaceRoot)
		if failure != "" {
			failures = append(failures, failure)
		}
		if action != nil {
			actions = append(actions, *action)
		}
		return ""
	}))

	return ParsedSendProtocol{
		VisibleText: visibleText,
		Actions:     actions,
		Failures:    failures,
	}
}

func normalizeVisibleText(content string) string {
	content = strings.TrimSpace(content)
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")
	return content
}

func parseAndResolveSendAction(jsonText, workspaceRoot string) (*SendAction, string) {
	var raw struct {
		Type     string `json:"type"`
		Path     string `json:"path"`
		FileName string `json:"fileName"`
		Caption  string `json:"caption"`
	}
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		return nil, failureText("协议块", "invalid JSON")
	}
	raw.Type = strings.TrimSpace(raw.Type)
	raw.Path = strings.TrimSpace(raw.Path)
	raw.FileName = strings.TrimSpace(raw.FileName)
	raw.Caption = strings.TrimSpace(raw.Caption)

	if raw.Type != "image" && raw.Type != "file" {
		return nil, failureText(raw.Path, "type must be image or file")
	}
	if raw.Path == "" {
		return nil, failureText("协议块", "path is required")
	}
	if workspaceRoot == "" {
		return nil, failureText(raw.Path, "workspace root is empty")
	}

	resolvedPath := raw.Path
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(workspaceRoot, resolvedPath)
	}

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return nil, failureText(raw.Path, "file does not exist")
	}

	canonicalRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return nil, failureText(raw.Path, "workspace root is invalid")
	}
	canonicalPath, err := filepath.EvalSymlinks(resolvedPath)
	if err != nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, failureText(raw.Path, "symlink target is invalid")
		}
		return nil, failureText(raw.Path, "path is invalid")
	}

	inside, err := isInsideWorkspace(canonicalRoot, canonicalPath)
	if err != nil || !inside {
		return nil, failureText(raw.Path, "path escapes workspace")
	}

	statTarget := info
	if info.Mode()&os.ModeSymlink != 0 {
		if statTarget, err = os.Stat(canonicalPath); err != nil {
			return nil, failureText(raw.Path, "symlink target is invalid")
		}
	}
	if !statTarget.Mode().IsRegular() {
		return nil, failureText(raw.Path, "path is not a regular file")
	}
	if statTarget.Size() <= 5 {
		return nil, failureText(raw.Path, "file must be larger than 5 bytes")
	}
	if statTarget.Size() > maxMediaBytes {
		return nil, failureText(raw.Path, "file exceeds 20MB")
	}

	fileName := raw.FileName
	if fileName == "" {
		fileName = filepath.Base(canonicalPath)
	}
	return &SendAction{
		Type:         raw.Type,
		Path:         raw.Path,
		ResolvedPath: canonicalPath,
		FileName:     fileName,
		Caption:      raw.Caption,
	}, ""
}

func isInsideWorkspace(root, target string) (bool, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return false, nil
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..", nil
}

func failureText(path, reason string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "协议块"
	}
	return fmt.Sprintf("文件回传失败：%s（%s）", path, reason)
}
