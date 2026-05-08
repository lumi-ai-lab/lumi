package wechat

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	lumicron "github.com/pengmide/lumi/internal/cron"
)

const (
	busyReplyText             = "上一条消息还在处理中，请稍后再发。"
	attachmentFailedReplyText = "附件处理失败，请重新发送。"
	fallbackDoneText          = "已完成。"
	uploadsTTL                = 72 * time.Hour
)

var invalidFileNameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

const wechatSourceInstruction = `You are replying to a WeChat user through Lumi.

If the user sent files, they are listed in a [WeChat attachments] block using workspace-relative paths. Read and use those files directly from the bound workspace.

If you need to send an image or file back to WeChat, emit one or more protocol blocks in this exact format:
[LUMI_WECHAT_SEND]
{"type":"image"|"file","path":"workspace/relative/or/absolute/path","fileName":"optional","caption":"optional"}
[/LUMI_WECHAT_SEND]

Only emit one JSON object per block. The path must point to a file inside the current workspace.`

type WeChatAttachment struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`

	downloadQuery string
	aesKeyHex     string
	aesKeyBase64  string
}

type WeChatInboundMessage struct {
	ConversationKey string             `json:"conversationKey"`
	MessageID       string             `json:"messageId"`
	ContextToken    string             `json:"contextToken"`
	Text            string             `json:"text"`
	Attachments     []WeChatAttachment `json:"attachments"`
	ReceivedAt      int64              `json:"receivedAt"`
}

type conversationLocks struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func newConversationLocks() *conversationLocks {
	return &conversationLocks{
		locks: make(map[string]chan struct{}),
	}
}

func (l *conversationLocks) TryLock(id string) (func(), bool) {
	l.mu.Lock()
	ch, ok := l.locks[id]
	if !ok {
		ch = make(chan struct{}, 1)
		l.locks[id] = ch
	}
	l.mu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

type gatewayEventSink struct {
	textBuilder strings.Builder
	lastError   string
}

func (s *gatewayEventSink) Emit(event ChatEvent) error {
	switch event.Name {
	case "update":
		payload, ok := event.Data.(map[string]any)
		if !ok {
			return nil
		}
		update, ok := payload["update"].(map[string]any)
		if !ok {
			return nil
		}
		if kind, _ := update["sessionUpdate"].(string); kind == "agent_message_chunk" || kind == "agent_thought_chunk" {
			content, _ := update["content"].(map[string]any)
			if contentType, _ := content["type"].(string); contentType == "text" {
				if text, _ := content["text"].(string); text != "" {
					s.textBuilder.WriteString(text)
				}
			}
		}
	case "error":
		if payload, ok := event.Data.(map[string]string); ok {
			s.lastError = payload["message"]
		}
	}
	return nil
}

func (s *gatewayEventSink) FinalText() string {
	return strings.TrimSpace(s.textBuilder.String())
}

func (s *Service) handleInboundMessage(ctx context.Context, cfg Config, msg WeChatInboundMessage) error {
	if strings.TrimSpace(msg.ConversationKey) == "" {
		return nil
	}

	workspace := s.config.FindWorkspace(cfg.WorkspaceID)
	if workspace == nil {
		return errors.New("workspace not found")
	}
	if workspace.Kind != "" && workspace.Kind != "local" {
		return errors.New("workspace must be local")
	}
	if s.config.FindAgent(cfg.AgentID) == nil {
		return errors.New("agent not found")
	}

	conversationID := deriveConversationID(msg.ConversationKey)
	client := NewClient(cfg)
	unlock, ok := s.locks.TryLock(conversationID)
	if !ok {
		return client.SendText(ctx, msg.ConversationKey, busyReplyText, msg.ContextToken)
	}
	defer unlock()

	messageWithAttachments, files, fatalAttachmentFailure, err := s.prepareMessageWithAttachments(ctx, client, workspace.Path, conversationID, msg)
	if err != nil {
		return client.SendText(ctx, msg.ConversationKey, err.Error(), msg.ContextToken)
	}
	if fatalAttachmentFailure {
		return client.SendText(ctx, msg.ConversationKey, attachmentFailedReplyText, msg.ContextToken)
	}

	stopTyping := s.typing.Start(ctx, cfg, msg.ConversationKey, msg.ContextToken)
	defer stopTyping()

	sink := &gatewayEventSink{}
	runErr := s.runner.RunWeChatChat(ctx, ChatRunInput{
		Message:             messageWithAttachments,
		ConversationID:      conversationID,
		WorkspaceID:         workspace.ID,
		WorkspacePath:       workspace.Path,
		AgentID:             cfg.AgentID,
		Files:               files,
		PromptPrefix:        wechatSourceInstruction,
		SessionModeOverride: deriveSessionMode(cfg.AgentID),
		ConversationStore:   s.convStore,
		CronTarget: lumicron.Target{WeChat: &lumicron.WeChatTarget{
			ConversationKey: msg.ConversationKey,
			ContextToken:    msg.ContextToken,
		}},
	}, sink)
	if runErr != nil && ctx.Err() != nil {
		return runErr
	}

	finalText := sink.FinalText()
	if sink.lastError != "" && finalText == "" {
		return client.SendText(ctx, msg.ConversationKey, sink.lastError, msg.ContextToken)
	}

	parsed := ParseSendProtocol(finalText, workspace.Path)
	sentMedia := false
	failures := append([]string(nil), parsed.Failures...)
	for _, action := range parsed.Actions {
		if action.Caption != "" {
			if err := client.SendText(ctx, msg.ConversationKey, action.Caption, msg.ContextToken); err != nil {
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
		}
		if err := client.UploadAndSendMedia(ctx, msg.ConversationKey, action, msg.ContextToken); err != nil {
			failures = append(failures, failureText(action.Path, err.Error()))
			continue
		}
		sentMedia = true
	}

	visibleText := parsed.VisibleText
	if len(failures) > 0 {
		failureTextBlock := strings.Join(failures, "\n")
		if visibleText == "" {
			visibleText = failureTextBlock
		} else {
			visibleText += "\n\n" + failureTextBlock
		}
	}
	if visibleText == "" && !sentMedia {
		visibleText = fallbackDoneText
	}
	return client.SendText(ctx, msg.ConversationKey, visibleText, msg.ContextToken)
}

func deriveConversationID(conversationKey string) string {
	sum := sha1.Sum([]byte(conversationKey))
	return "wx_" + fmt.Sprintf("%x", sum[:])[:16]
}

func deriveSessionMode(agentID string) string {
	switch {
	case agentID == "codex":
		return "auto"
	case strings.HasPrefix(agentID, "claude"):
		return "bypassPermissions"
	default:
		return "default"
	}
}

func (s *Service) prepareMessageWithAttachments(ctx context.Context, client *Client, workspacePath, conversationID string, msg WeChatInboundMessage) (string, []ChatFileInfo, bool, error) {
	if len(msg.Attachments) == 0 {
		return strings.TrimSpace(msg.Text), nil, false, nil
	}

	lines := make([]string, 0, len(msg.Attachments)+2)
	lines = append(lines, "[WeChat attachments]")
	files := make([]ChatFileInfo, 0, len(msg.Attachments))
	successCount := 0
	for _, attachment := range msg.Attachments {
		if attachment.Size > maxMediaBytes && attachment.Size > 0 {
			lines = append(lines, fmt.Sprintf("- failed: %s (download failed: file exceeds 200MB)", attachment.Name))
			continue
		}

		data, err := client.DownloadAttachment(ctx, attachment)
		if err != nil {
			lines = append(lines, fmt.Sprintf("- failed: %s (download failed: %s)", attachment.Name, err.Error()))
			continue
		}
		kind := detectAttachmentKind(data, attachment.Name)
		relativePath, absolutePath, err := writeInboundAttachment(workspacePath, conversationID, attachment.Name, data)
		if err != nil {
			lines = append(lines, fmt.Sprintf("- failed: %s (download failed: %s)", attachment.Name, err.Error()))
			continue
		}
		files = append(files, ChatFileInfo{
			Name: attachment.Name,
			Path: relativePath,
			Size: int64(len(data)),
		})
		lines = append(lines, fmt.Sprintf("- %s: %s", kind, relativePath))
		_ = absolutePath
		successCount++
	}
	lines = append(lines, "[/WeChat attachments]")

	if successCount > 0 {
		_ = cleanupUploadRoot(filepath.Join(workspacePath, ".lumi-uploads", "wechat"))
	}

	attachmentBlock := strings.Join(lines, "\n")
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		if successCount == 0 {
			return "", nil, true, nil
		}
		return attachmentBlock, files, false, nil
	}
	return attachmentBlock + "\n\n" + text, files, false, nil
}

func writeInboundAttachment(workspacePath, conversationID, originalName string, data []byte) (string, string, error) {
	baseName := sanitizeAttachmentBaseName(originalName)
	targetDir := filepath.Join(workspacePath, ".lumi-uploads", "wechat", conversationID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", "", err
	}
	fileName := fmt.Sprintf("%d-%s", time.Now().UnixMilli(), baseName)
	absolutePath := filepath.Join(targetDir, fileName)
	if err := os.WriteFile(absolutePath, data, 0o644); err != nil {
		return "", "", err
	}
	relativePath, err := filepath.Rel(workspacePath, absolutePath)
	if err != nil {
		return "", "", err
	}
	return filepath.ToSlash(relativePath), absolutePath, nil
}

func sanitizeAttachmentBaseName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "/" || name == "" {
		name = "file"
	}
	name = invalidFileNameChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		return "file"
	}
	return name
}

func cleanupUploadRoot(root string) error {
	type fileEntry struct {
		path    string
		modTime time.Time
		size    int64
	}

	files := make([]fileEntry, 0)
	var totalSize int64
	now := time.Now()
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if now.Sub(info.ModTime()) > uploadsTTL {
			_ = os.Remove(path)
			return nil
		}
		totalSize += info.Size()
		files = append(files, fileEntry{path: path, modTime: info.ModTime(), size: info.Size()})
		return nil
	})
	if err != nil {
		return err
	}
	if totalSize <= maxMediaBytes {
		return nil
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, entry := range files {
		if totalSize <= maxMediaBytes {
			break
		}
		_ = os.Remove(entry.path)
		totalSize -= entry.size
	}
	return nil
}
