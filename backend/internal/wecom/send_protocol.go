package wecom

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pengmide/lumi/internal/imfile"
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
	Segments    []ParsedSendSegment
}

type ParsedSendSegment struct {
	Text    string
	Action  *SendAction
	Failure string
}

func ParseSendProtocol(content, workspaceRoot string) ParsedSendProtocol {
	actions := make([]SendAction, 0)
	failures := make([]string, 0)
	segments := make([]ParsedSendSegment, 0)

	var visibleBuilder strings.Builder
	last := 0
	for _, loc := range wecomSendBlockRE.FindAllStringSubmatchIndex(content, -1) {
		if len(loc) < 4 {
			continue
		}
		text := normalizeVisibleText(content[last:loc[0]])
		if text != "" {
			if visibleBuilder.Len() > 0 {
				visibleBuilder.WriteString("\n\n")
			}
			visibleBuilder.WriteString(text)
			segments = append(segments, ParsedSendSegment{Text: text})
		}

		action, failure := parseAndResolveSendAction(content[loc[2]:loc[3]], workspaceRoot)
		if failure != "" {
			failures = append(failures, failure)
			segments = append(segments, ParsedSendSegment{Failure: failure})
		}
		if action != nil {
			actions = append(actions, *action)
			actionCopy := *action
			segments = append(segments, ParsedSendSegment{Action: &actionCopy})
		}
		last = loc[1]
	}
	text := normalizeVisibleText(content[last:])
	if text != "" {
		if visibleBuilder.Len() > 0 {
			visibleBuilder.WriteString("\n\n")
		}
		visibleBuilder.WriteString(text)
		segments = append(segments, ParsedSendSegment{Text: text})
	}

	return ParsedSendProtocol{
		VisibleText: normalizeVisibleText(visibleBuilder.String()),
		Actions:     actions,
		Failures:    failures,
		Segments:    segments,
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
	if resolved.Info.Size() <= 5 {
		return nil, failureText(raw.Path, "file must be larger than 5 bytes")
	}
	if resolved.Info.Size() > maxMediaBytes {
		return nil, failureText(raw.Path, "file exceeds 20MB")
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
