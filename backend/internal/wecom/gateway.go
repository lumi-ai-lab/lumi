package wecom

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/imagent"
)

const (
	busyReplyText             = "上一条消息还在处理中，请稍后再发。"
	attachmentFailedReplyText = "附件处理失败，请重新发送。"
	fallbackDoneText          = "已完成。"
	uploadsTTL                = 72 * time.Hour
	maxMediaBytes             = 20 << 20
)

var invalidFileNameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

const wecomSourceInstruction = `You are replying to a WeCom user through Lumi.

If the user sent files, they are listed in a [WeCom attachments] block using workspace-relative paths. Read and use those files directly from the bound workspace.

If you need to send an image or file back to WeCom, emit one or more protocol blocks in this exact format:
[LUMI_WECOM_SEND]
{"type":"image"|"file","path":"workspace/relative/or/absolute/path","fileName":"optional","caption":"optional"}
[/LUMI_WECOM_SEND]

Only emit one JSON object per block. The path must point to a file inside the current workspace.`

type WeComAttachment struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Data     []byte `json:"-"`
	MimeType string `json:"mimeType,omitempty"`
}

type replyContext struct {
	ReqID    string `json:"reqId,omitempty"`
	ChatID   string `json:"chatId"`
	ChatType string `json:"chatType,omitempty"`
	UserID   string `json:"userId"`
}

type WeComInboundMessage struct {
	ConversationKey string            `json:"conversationKey"`
	MessageID       string            `json:"messageId"`
	ChatID          string            `json:"chatId"`
	UserID          string            `json:"userId"`
	Text            string            `json:"text"`
	Attachments     []WeComAttachment `json:"attachments"`
	ReplyContext    replyContext      `json:"replyContext"`
	ReceivedAt      int64             `json:"receivedAt"`
}

type wsMessageSender interface {
	Reply(ctx context.Context, rctx replyContext, content string) error
	Send(ctx context.Context, rctx replyContext, content string) error
	ReplyMedia(ctx context.Context, rctx replyContext, action SendAction) error
	SendMedia(ctx context.Context, rctx replyContext, action SendAction) error
}

type conversationLocks struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

func newConversationLocks() *conversationLocks {
	return &conversationLocks{locks: make(map[string]chan struct{})}
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

func (s *Service) handleInboundMessage(ctx context.Context, cfg Config, msg WeComInboundMessage, sender wsMessageSender) error {
	if strings.TrimSpace(msg.ConversationKey) == "" {
		return nil
	}

	workspace := s.config.FindWorkspace(cfg.WorkspaceID)
	if workspace == nil {
		return errors.New("workspace not found")
	}
	if !isSupportedWorkspaceKind(workspace.Kind) {
		return errors.New("workspace must be local or sandbox")
	}
	conversationID := deriveConversationID(msg.ConversationKey)
	unlock, ok := s.locks.TryLock(conversationID)
	if !ok {
		return sender.Reply(ctx, msg.ReplyContext, busyReplyText)
	}
	defer unlock()

	if reply, handled, err := imagent.HandleCommand(msg.Text, conversationID, workspace.ID, cfg.AgentID, s.config, workspace, s.convStore); handled {
		if err != nil {
			return err
		}
		return sender.Reply(ctx, msg.ReplyContext, reply)
	}

	agentID, err := imagent.ResolveActiveAgent(s.convStore, conversationID, workspace.ID, cfg.AgentID, s.config, workspace)
	if err != nil {
		return err
	}

	messageWithAttachments, files, fatalAttachmentFailure, err := prepareMessageWithAttachments(workspace.Path, conversationID, msg)
	if err != nil {
		return sender.Reply(ctx, msg.ReplyContext, err.Error())
	}
	if fatalAttachmentFailure {
		return sender.Reply(ctx, msg.ReplyContext, attachmentFailedReplyText)
	}

	sink := &gatewayEventSink{}
	runErr := s.runner.RunWeComChat(ctx, ChatRunInput{
		Message:             messageWithAttachments,
		ConversationID:      conversationID,
		WorkspaceID:         workspace.ID,
		WorkspacePath:       workspace.Path,
		AgentID:             agentID,
		Files:               files,
		PromptPrefix:        wecomSourceInstruction,
		SessionModeOverride: deriveSessionMode(agentID),
		ConversationStore:   s.convStore,
		CronTarget: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ReqID:    msg.ReplyContext.ReqID,
			ChatID:   msg.ReplyContext.ChatID,
			ChatType: msg.ReplyContext.ChatType,
			UserID:   msg.ReplyContext.UserID,
		}},
	}, sink)
	if runErr != nil && ctx.Err() != nil {
		return runErr
	}

	finalText := sink.FinalText()
	if sink.lastError != "" && finalText == "" {
		return sender.Reply(ctx, msg.ReplyContext, sink.lastError)
	}

	parsed := ParseSendProtocol(finalText, workspace.Path)
	sentMedia := false
	failures := append([]string(nil), parsed.Failures...)
	for _, action := range parsed.Actions {
		if action.Caption != "" {
			if err := sender.Reply(ctx, msg.ReplyContext, action.Caption); err != nil {
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
		}
		if err := sender.ReplyMedia(ctx, msg.ReplyContext, action); err != nil {
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
	if visibleText == "" {
		return nil
	}
	return sender.Reply(ctx, msg.ReplyContext, visibleText)
}

func deriveConversationID(conversationKey string) string {
	sum := sha1.Sum([]byte(conversationKey))
	return "wecom_" + fmt.Sprintf("%x", sum[:])[:16]
}

func deriveSessionMode(agentID string) string {
	switch {
	case agentID == "codex":
		return "auto"
	default:
		return "default"
	}
}

func prepareMessageWithAttachments(workspacePath, conversationID string, msg WeComInboundMessage) (string, []ChatFileInfo, bool, error) {
	if len(msg.Attachments) == 0 {
		return strings.TrimSpace(msg.Text), nil, false, nil
	}

	lines := make([]string, 0, len(msg.Attachments)+2)
	lines = append(lines, "[WeCom attachments]")
	files := make([]ChatFileInfo, 0, len(msg.Attachments))
	successCount := 0
	for _, attachment := range msg.Attachments {
		if len(attachment.Data) == 0 {
			lines = append(lines, fmt.Sprintf("- failed: %s (download failed: attachment is empty)", attachment.Name))
			continue
		}
		if int64(len(attachment.Data)) > maxMediaBytes {
			lines = append(lines, fmt.Sprintf("- failed: %s (download failed: file exceeds 20MB)", attachment.Name))
			continue
		}

		kind := detectAttachmentKind(attachment.Data, attachment.Name)
		relativePath, _, err := writeInboundAttachment(workspacePath, conversationID, attachment.Name, attachment.Data)
		if err != nil {
			lines = append(lines, fmt.Sprintf("- failed: %s (download failed: %s)", attachment.Name, err.Error()))
			continue
		}
		files = append(files, ChatFileInfo{
			Name: attachment.Name,
			Path: relativePath,
			Size: int64(len(attachment.Data)),
		})
		lines = append(lines, fmt.Sprintf("- %s: %s", kind, relativePath))
		successCount++
	}
	lines = append(lines, "[/WeCom attachments]")

	if successCount > 0 {
		_ = cleanupUploadRoot(filepath.Join(workspacePath, ".lumi-uploads", "wecom"))
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
	targetDir := filepath.Join(workspacePath, ".lumi-uploads", "wecom", conversationID)
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
		files = append(files, fileEntry{path: path, modTime: info.ModTime(), size: info.Size()})
		totalSize += info.Size()
		return nil
	})
	if err != nil {
		return err
	}

	for _, file := range files {
		if now.Sub(file.modTime) > uploadsTTL {
			totalSize -= file.size
			_ = os.Remove(file.path)
		}
	}

	if totalSize <= 512<<20 {
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, file := range files {
		if totalSize <= 512<<20 {
			break
		}
		if err := os.Remove(file.path); err == nil {
			totalSize -= file.size
		}
	}
	return nil
}

func detectAttachmentKind(data []byte, fileName string) string {
	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "image/") {
		return "image"
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext != "" {
		if byExt := mime.TypeByExtension(ext); strings.HasPrefix(byExt, "image/") {
			return "image"
		}
	}
	return "file"
}
