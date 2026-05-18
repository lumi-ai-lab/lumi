package wechat

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pengmide/lumi/internal/imfile"
)

var wechatSendBlockRE = regexp.MustCompile(`(?s)\[LUMI_WECHAT_SEND\]\s*(.*?)\s*\[/LUMI_WECHAT_SEND\]`)

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

	visibleText := normalizeVisibleText(wechatSendBlockRE.ReplaceAllStringFunc(content, func(block string) string {
		match := wechatSendBlockRE.FindStringSubmatch(block)
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
	resolved, reason := imfile.ResolveWorkspaceFile(raw.Path, workspaceRoot)
	if reason != "" {
		return nil, failureText(raw.Path, reason)
	}
	if resolved.Info.Size() > maxMediaBytes {
		return nil, failureText(raw.Path, "file exceeds 200MB")
	}

	fileName := raw.FileName
	if fileName == "" {
		fileName = filepath.Base(resolved.Path)
	}
	return &SendAction{
		Type:         raw.Type,
		Path:         raw.Path,
		ResolvedPath: resolved.Path,
		FileName:     fileName,
		Caption:      raw.Caption,
	}, ""
}

func failureText(path, reason string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "协议块"
	}
	return fmt.Sprintf("文件回传失败：%s（%s）", path, reason)
}
