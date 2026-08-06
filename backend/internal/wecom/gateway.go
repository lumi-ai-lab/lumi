package wecom

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/imagent"
	"github.com/pengmide/lumi/internal/imdebug"
	"github.com/pengmide/lumi/internal/requestercontext"
	"github.com/pengmide/lumi/internal/storage"
)

const (
	busyReplyText             = "上一条消息还在处理中，请稍后再发。"
	attachmentFailedReplyText = "附件处理失败，请重新发送。"
	fallbackDoneText          = "已完成。"
	lengthLimitNoticeText     = "模型回复达到单次长度上限，回答“继续”可补全"
	incompleteReplyNoticeText = "回复疑似未完成，可发送“继续”补全"
	continueReplyPrompt       = "继续上一条回答，从中断处后面开始，不要重复已经输出的内容。"
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
	ConversationKey  string                     `json:"conversationKey"`
	MessageID        string                     `json:"messageId"`
	ChatID           string                     `json:"chatId"`
	UserID           string                     `json:"userId"`
	Text             string                     `json:"text"`
	Attachments      []WeComAttachment          `json:"attachments"`
	ReplyContext     replyContext               `json:"replyContext"`
	ReceivedAt       int64                      `json:"receivedAt"`
	RequesterContext *requestercontext.Context  `json:"-"`
	HostAuth         *requestercontext.HostAuth `json:"-"`
}

type wsMessageSender interface {
	Reply(ctx context.Context, rctx replyContext, content string) error
	ReplyStream(ctx context.Context, rctx replyContext, streamID, content string, finish bool) error
	ReplyStreamFinal(ctx context.Context, rctx replyContext, streamID, content string) error
	NewStreamID() string
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
	buffer               *imdebug.Buffer
	sendSegment          func(imdebug.Segment) error
	textUpdate           func(string)
	lastError            string
	debug                storage.IMDebugSettings
	finalTextBuilder     strings.Builder
	finalTextAccumulated string
	stopReason           string
}

func (s *gatewayEventSink) Emit(event ChatEvent) error {
	if s.buffer == nil {
		s.buffer = imdebug.NewBuffer(s.debug)
	}
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
		kind, _ := update["sessionUpdate"].(string)
		switch kind {
		case "agent_message_chunk", "agent_thought_chunk":
			content, _ := update["content"].(map[string]any)
			if contentType, _ := content["type"].(string); contentType == "text" {
				if text, _ := content["text"].(string); text != "" {
					if kind == "agent_thought_chunk" {
						s.buffer.AddThinkingChunk(text)
						if err := s.flushReadySegments(); err != nil {
							return err
						}
					} else {
						s.addFinalMessageChunk(text)
						s.buffer.AddMessageChunk(text)
						if s.textUpdate != nil {
							s.textUpdate(s.FinalText())
						}
						if err := s.flushReadySegments(); err != nil {
							return err
						}
					}
				}
			}
		case "tool_call", "tool_call_update":
			s.buffer.AddTool(update)
			if err := s.flushReadySegments(); err != nil {
				return err
			}
		}
	case "thinking":
		s.buffer.AddThinkingEvent(event.Data)
		if imdebug.IsThinkingDone(event.Data) {
			s.buffer.FlushThinking()
		}
		if err := s.flushReadySegments(); err != nil {
			return err
		}
	case "tool_call", "tool_call_update":
		s.buffer.AddTool(event.Data)
		if err := s.flushReadySegments(); err != nil {
			return err
		}
	case "done":
		s.stopReason = eventStopReason(event.Data)
		if err := s.flushDebugSegments(); err != nil {
			return err
		}
	case "error":
		if payload, ok := event.Data.(map[string]string); ok {
			s.lastError = payload["message"]
		}
	}
	return nil
}

func (s *gatewayEventSink) FinalText() string {
	if text := strings.TrimSpace(s.finalTextBuilder.String()); text != "" {
		return text
	}
	if s.buffer == nil {
		return ""
	}
	return s.buffer.Text()
}

func eventStopReason(data any) string {
	switch payload := data.(type) {
	case map[string]any:
		reason, _ := payload["stopReason"].(string)
		return reason
	case map[string]string:
		return payload["stopReason"]
	default:
		return ""
	}
}

func appendLengthLimitNotice(text string) string {
	if text == "" {
		return text
	}
	separator := ""
	var last rune
	for _, r := range text {
		last = r
	}
	if !unicode.IsSpace(last) {
		separator = "\n\n"
	}
	return text + separator + "> " + lengthLimitNoticeText
}

func appendIncompleteReplyNotice(text string) string {
	if text == "" {
		return incompleteReplyNoticeText
	}
	if strings.Contains(text, incompleteReplyNoticeText) {
		return text
	}
	separator := ""
	var last rune
	for _, r := range text {
		last = r
	}
	if !unicode.IsSpace(last) {
		separator = "\n\n"
	}
	return text + separator + "> " + incompleteReplyNoticeText
}

func looksLikeIncompleteEndTurn(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if hasUnclosedWeComCodeFence(text) {
		return true
	}
	if strings.Count(text, "**")%2 == 1 {
		return true
	}
	lastLine := strings.TrimSpace(text)
	if idx := strings.LastIndex(lastLine, "\n"); idx >= 0 {
		lastLine = strings.TrimSpace(lastLine[idx+1:])
	}
	if lastLine == "" {
		return false
	}
	switch lastLine {
	case "-", "*", "+", ">", "`", "```", "**":
		return true
	}
	if strings.HasPrefix(lastLine, "#") && strings.Trim(strings.TrimSpace(lastLine), "#") == "" {
		return true
	}
	if strings.HasSuffix(lastLine, "**") || strings.HasSuffix(lastLine, "`") {
		return true
	}
	if strings.HasSuffix(lastLine, "-") {
		before := strings.TrimSpace(strings.TrimSuffix(lastLine, "-"))
		return before == "" || strings.HasSuffix(before, "：") || strings.HasSuffix(before, ":")
	}
	return false
}

func hasUnclosedWeComCodeFence(text string) bool {
	open := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			open = !open
		}
	}
	return open
}

func tailRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[len(runes)-maxRunes:])
}

func (s *gatewayEventSink) addFinalMessageChunk(text string) {
	delta := deltaAgainstText(s.finalTextAccumulated, text)
	if delta == "" {
		return
	}
	s.finalTextBuilder.WriteString(delta)
	s.finalTextAccumulated += delta
}

func deltaAgainstText(accumulated, chunk string) string {
	if chunk == "" {
		return ""
	}
	if accumulated == "" {
		return chunk
	}
	if chunk == accumulated {
		return ""
	}
	if strings.HasPrefix(chunk, accumulated) {
		return chunk[len(accumulated):]
	}
	return chunk
}

func (s *gatewayEventSink) DebugMessages() []string {
	if s.buffer == nil {
		return nil
	}
	return s.buffer.DebugMessages()
}

func (s *gatewayEventSink) flushReadySegments() error {
	if s.buffer == nil || s.sendSegment == nil {
		return nil
	}
	for _, segment := range s.buffer.PopSegments() {
		if err := s.sendSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func (s *gatewayEventSink) flushAllSegments() error {
	if s.buffer == nil || s.sendSegment == nil {
		return nil
	}
	for _, segment := range s.buffer.PopAllSegments() {
		if err := s.sendSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func (s *gatewayEventSink) flushDebugSegments() error {
	if s.buffer == nil || s.sendSegment == nil {
		return nil
	}
	s.buffer.FlushDebug()
	for _, segment := range s.buffer.PopSegments() {
		if segment.Kind != imdebug.SegmentDebug {
			continue
		}
		if err := s.sendSegment(segment); err != nil {
			return err
		}
	}
	return nil
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
	if imagent.IsStopCommand(msg.Text) {
		if s.runs.Stop(conversationID) {
			return sender.Reply(ctx, msg.ReplyContext, "已请求停止当前任务。")
		}
		return sender.Reply(ctx, msg.ReplyContext, "当前没有正在处理的任务。")
	}

	unlock, ok := s.locks.TryLock(conversationID)
	if !ok {
		return sender.Reply(ctx, msg.ReplyContext, busyReplyText)
	}
	defer unlock()

	if result, err := imagent.HandleCommandWithIntent(msg.Text, conversationID, workspace.ID, cfg.AgentID, s.config, workspace, s.convStore); result.Handled {
		if err != nil {
			return err
		}
		return sender.Reply(ctx, msg.ReplyContext, result.Reply)
	}

	agentID, err := imagent.ResolveActiveAgent(s.convStore, conversationID, workspace.ID, cfg.AgentID, s.config, workspace)
	if err != nil {
		return err
	}
	newSession, err := imagent.PendingNewSession(s.convStore, conversationID)
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

	sentVisible := false
	sink := &gatewayEventSink{debug: imdebug.ToolDebugEnabled(s.convStore, conversationID)}
	streamSender := (*wecomStreamSender)(nil)
	traceID := newWeComTraceID()
	if cfg.Stream && msg.ReplyContext.ReqID != "" {
		streamSender = newWeComStreamSenderWithTrace(sender, msg.ReplyContext, workspace.Path, traceID)
		streamSender.SendPlaceholder(ctx)
		sink.textUpdate = func(text string) {
			streamSender.Update(ctx, text)
		}
	}
	sink.sendSegment = func(segment imdebug.Segment) error {
		if segment.Kind == imdebug.SegmentDebug {
			return sender.Reply(ctx, msg.ReplyContext, segment.Text)
		}
		if streamSender != nil && streamSender.Failed() {
			return nil
		}
		if streamSender != nil && !streamSender.Failed() {
			return nil
		}
		var sent bool
		var err error
		if streamSender != nil && streamSender.Failed() {
			sent, err = s.sendTextSegmentViaSend(ctx, sender, msg, workspace.Path, segment.Text)
		} else {
			sent, err = s.sendTextSegment(ctx, sender, msg, workspace.Path, segment.Text)
		}
		if sent {
			sentVisible = true
		}
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	runToken := s.runs.Register(conversationID, cancel)
	defer s.runs.Unregister(conversationID, runToken)
	defer cancel()

	chatInput := ChatRunInput{
		Message:             messageWithAttachments,
		ConversationID:      conversationID,
		WorkspaceID:         workspace.ID,
		WorkspacePath:       workspace.Path,
		AgentID:             agentID,
		Files:               files,
		PromptPrefix:        wecomSourceInstruction,
		SessionModeOverride: deriveSessionMode(agentID),
		NewSession:          newSession,
		ConversationStore:   s.convStore,
		RequesterContext:    msg.RequesterContext,
		HostAuth:            msg.HostAuth,
		CronTarget: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ReqID:    msg.ReplyContext.ReqID,
			ChatID:   msg.ReplyContext.ChatID,
			ChatType: msg.ReplyContext.ChatType,
			UserID:   msg.ReplyContext.UserID,
		}},
	}
	log.Printf("wecom task start: traceID=%s conversationID=%s workspaceID=%s agentID=%s stream=%v", traceID, conversationID, workspace.ID, agentID, streamSender != nil)
	runErr := s.runner.RunWeComChat(runCtx, chatInput, sink)
	if runCtx.Err() != nil {
		return runCtx.Err()
	}
	if runErr != nil && ctx.Err() != nil {
		return runErr
	}
	if runErr == nil && newSession {
		if err := imagent.ClearPendingNewSession(s.convStore, conversationID); err != nil {
			return err
		}
	}

	finalText := sink.FinalText()
	continuedText := ""
	if sink.stopReason == "end_turn" && looksLikeIncompleteEndTurn(ParseSendProtocol(finalText, workspace.Path).VisibleText) {
		beforeContinue := finalText
		continueInput := chatInput
		continueInput.Message = continueReplyPrompt
		continueInput.Files = nil
		continueInput.NewSession = false
		log.Printf("wecom final appears incomplete, requesting continuation: traceID=%s conversationID=%s bytes=%d chars=%d", traceID, conversationID, len(beforeContinue), len([]rune(beforeContinue)))
		continueErr := s.runner.RunWeComChat(runCtx, continueInput, sink)
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		afterContinue := sink.FinalText()
		if strings.HasPrefix(afterContinue, beforeContinue) {
			continuedText = strings.TrimSpace(afterContinue[len(beforeContinue):])
		}
		if continueErr != nil || continuedText == "" {
			log.Printf("wecom continuation fallback failed: conversationID=%s err=%v continuedChars=%d", conversationID, continueErr, len([]rune(continuedText)))
			finalText = appendIncompleteReplyNotice(beforeContinue)
			continuedText = ""
		} else {
			finalText = afterContinue
		}
	}
	log.Printf("wecom final: traceID=%s conversationID=%s stopReason=%s bytes=%d chars=%d", traceID, conversationID, sink.stopReason, len(finalText), len([]rune(finalText)))
	lengthLimitReached := sink.stopReason == "length" && finalText != ""
	if lengthLimitReached {
		finalText = appendLengthLimitNotice(finalText)
		if continuedText != "" {
			continuedText = appendLengthLimitNotice(continuedText)
		}
	}
	if sink.lastError != "" && finalText == "" {
		return sender.Reply(ctx, msg.ReplyContext, sink.lastError)
	}
	if sentVisible {
		if err := sink.flushAllSegments(); err != nil {
			return err
		}
		if lengthLimitReached {
			return sender.Send(ctx, msg.ReplyContext, "> "+lengthLimitNoticeText)
		}
		return nil
	}
	if err := sink.flushDebugSegments(); err != nil {
		return err
	}

	if finalText == "" {
		return sender.Reply(ctx, msg.ReplyContext, fallbackDoneText)
	}
	if streamSender != nil && !streamSender.Failed() {
		streamFinalText := finalText
		streamSender.Complete(ctx, streamFinalText)
		if _, fallback := streamSender.SendFallbackPreview(); !streamSender.Failed() && !fallback {
			sent, err := s.sendTextSegmentAfterStreamWithLedger(ctx, sender, msg, workspace.Path, streamFinalText, streamSender)
			if err != nil {
				return err
			}
			if sent {
				return nil
			}
			return nil
		}
	}
	if streamSender != nil {
		if preview, fallback := streamSender.SendFallbackPreview(); fallback {
			sent, err := s.sendTextSegmentAfterStreamFallbackWithLedgerAndTrace(ctx, sender, msg, workspace.Path, finalText, preview, streamSender.LedgerSnapshot(), streamSender.TraceID())
			if err != nil {
				return err
			}
			if sent {
				return nil
			}
			return nil
		}
	}
	if streamSender != nil && streamSender.Failed() {
		class, err := streamSender.Failure()
		log.Printf("wecom advanced stream failed without ordinary answer fallback: traceID=%s reqID=%s chatID=%s class=%s err=%v", streamSender.TraceID(), msg.ReplyContext.ReqID, msg.ReplyContext.ChatID, class, err)
		return nil
	}
	sent, err := s.sendTextSegment(ctx, sender, msg, workspace.Path, finalText)
	if err != nil {
		return err
	}
	if sent {
		return nil
	}
	return sender.Reply(ctx, msg.ReplyContext, fallbackDoneText)
}

func (s *Service) sendTextSegmentAfterStream(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text string) (bool, error) {
	return s.sendTextSegmentAfterStreamWithLedger(ctx, sender, msg, workspacePath, text, nil)
}

func (s *Service) sendTextSegmentAfterStreamWithLedger(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text string, streamSender *WeComStreamDispatcher) (bool, error) {
	if streamSender != nil {
		return false, nil
	}
	parsed := ParseSendProtocol(text, workspacePath)
	if len(parsed.Actions) == 0 && len(parsed.Failures) == 0 {
		return parsed.VisibleText != "", nil
	}
	sent := false
	failures := append([]string(nil), parsed.Failures...)
	for i, action := range parsed.Actions {
		if action.Caption != "" {
			if err := sender.Reply(ctx, msg.ReplyContext, action.Caption); err != nil {
				if streamSender != nil {
					streamSender.MarkLedgerUnit("media-caption-"+strconv.Itoa(i+1), DeliveryStatusFailed, err)
				}
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
			if streamSender != nil {
				streamSender.MarkLedgerUnit("media-caption-"+strconv.Itoa(i+1), DeliveryStatusDelivered, nil)
			}
			sent = true
		}
		if err := sender.ReplyMedia(ctx, msg.ReplyContext, action); err != nil {
			if streamSender != nil {
				streamSender.MarkLedgerUnit("media-"+strconv.Itoa(i+1), DeliveryStatusFailed, err)
			}
			failures = append(failures, failureText(action.Path, err.Error()))
			continue
		}
		if streamSender != nil {
			streamSender.MarkLedgerUnit("media-"+strconv.Itoa(i+1), DeliveryStatusDelivered, nil)
		}
		sent = true
	}
	if len(failures) > 0 {
		if err := sender.Reply(ctx, msg.ReplyContext, strings.Join(failures, "\n")); err != nil {
			return sent, err
		}
		sent = true
	}
	if parsed.VisibleText != "" {
		sent = true
	}
	return sent, nil
}

func (s *Service) sendTextSegmentAfterStreamFallback(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text, preview string) (bool, error) {
	return s.sendTextSegmentAfterStreamFallbackLegacy(ctx, sender, msg, workspacePath, text, preview)
}

func (s *Service) sendTextSegmentAfterStreamFallbackWithLedger(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text, preview string, units []DeliveredUnit) (bool, error) {
	return s.sendTextSegmentAfterStreamFallbackWithLedgerAndTrace(ctx, sender, msg, workspacePath, text, preview, units, "")
}

func (s *Service) sendTextSegmentAfterStreamFallbackWithLedgerAndTrace(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text, preview string, units []DeliveredUnit, traceID string) (bool, error) {
	ledger := &DeliveryLedger{units: units}
	if ledger.Valid() {
		log.Printf("wecom fallback reason=stream_delivery_ledger traceID=%s reqID=%s chatID=%s pendingUnits=%d", traceID, msg.ReplyContext.ReqID, msg.ReplyContext.ChatID, len(ledger.PendingOrFailedIndexes()))
		return s.sendDeliveryUnitsViaSendWithTrace(ctx, sender, msg, ledger, traceID)
	}
	log.Printf("wecom fallback reason=ledger_invalid traceID=%s reqID=%s chatID=%s unitCount=%d", traceID, msg.ReplyContext.ReqID, msg.ReplyContext.ChatID, len(units))
	return s.sendCompleteTextSegmentViaSend(ctx, sender, msg, workspacePath, text)
}

func (s *Service) sendTextSegmentAfterStreamFallbackLegacy(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text, preview string) (bool, error) {
	parsed := ParseSendProtocol(text, workspacePath)
	visibleText := normalizeWeComMarkdown(parsed.VisibleText)
	fallbackText := ""
	if visibleText != "" {
		if remaining, ok := remainingWeComTextAfterStreamPreview(visibleText, preview); ok {
			if remaining != "" {
				fallbackText = "续上：\n\n" + remaining
			}
		} else {
			fallbackText = "完整回答：\n\n" + visibleText
		}
	}
	return s.sendParsedTextViaSend(ctx, sender, msg, parsed, fallbackText)
}

func (s *Service) sendCompleteTextSegmentViaSend(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text string) (bool, error) {
	parsed := ParseSendProtocol(text, workspacePath)
	rendered := renderWeComFinalMessage(parsed.VisibleText, workspacePath)
	visibleText := rendered.Text()
	if visibleText != "" {
		visibleText = "完整回答：\n\n" + visibleText
	}
	sent, err := s.sendParsedTextViaSend(ctx, sender, msg, parsed, visibleText)
	if err != nil {
		return sent, err
	}
	sentRendered, err := s.sendRenderedActionsViaSend(ctx, sender, msg, rendered)
	if err != nil {
		return sent || sentRendered, err
	}
	return sent || sentRendered, nil
}

func (s *Service) sendDeliveryUnitsViaSend(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, ledger *DeliveryLedger) (bool, error) {
	return s.sendDeliveryUnitsViaSendWithTrace(ctx, sender, msg, ledger, "")
}

func (s *Service) sendDeliveryUnitsViaSendWithMarker(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, ledger *DeliveryLedger, markExternal func(DeliveredUnit, DeliveryStatus, error)) (bool, error) {
	return s.sendDeliveryUnitsViaSendWithTraceAndMarker(ctx, sender, msg, ledger, "", markExternal)
}

func (s *Service) sendDeliveryUnitsViaSendWithTrace(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, ledger *DeliveryLedger, traceID string) (bool, error) {
	return s.sendDeliveryUnitsViaSendWithTraceAndMarker(ctx, sender, msg, ledger, traceID, nil)
}

func (s *Service) sendDeliveryUnitsViaSendWithTraceAndMarker(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, ledger *DeliveryLedger, traceID string, markExternal func(DeliveredUnit, DeliveryStatus, error)) (bool, error) {
	sent := false
	indexes := ledger.PendingOrFailedIndexes()
	mark := func(index int, status DeliveryStatus, err error) {
		unit := ledger.units[index]
		ledger.Mark(index, status, err)
		log.Printf("wecom delivery unit event=%s traceID=%s reqID=%s chatID=%s unitID=%s method=%s kind=%s status=%s err=%v", deliveryLogEvent(status), traceID, msg.ReplyContext.ReqID, msg.ReplyContext.ChatID, unit.ID, unit.DeliveryMethod, unit.RenderedKind, status, err)
		if markExternal != nil {
			markExternal(unit, status, err)
		}
	}
	sendUnit := func(index int) error {
		unit := ledger.units[index]
		switch unit.DeliveryMethod {
		case DeliveryMethodMedia:
			if unit.Action == nil {
				mark(index, DeliveryStatusSkipped, nil)
				return nil
			}
			if err := sender.SendMedia(ctx, msg.ReplyContext, *unit.Action); err != nil {
				mark(index, DeliveryStatusFailed, err)
				return err
			}
			mark(index, DeliveryStatusDelivered, nil)
			sent = true
		case DeliveryMethodSend, DeliveryMethodStream:
			text := strings.TrimSpace(unit.Text)
			if text == "" {
				mark(index, DeliveryStatusSkipped, nil)
				return nil
			}
			if unit.ID == "text-remaining" {
				text = "续上：\n\n" + text
			}
			if err := sender.Send(ctx, msg.ReplyContext, normalizeWeComMarkdown(text)); err != nil {
				mark(index, DeliveryStatusFailed, err)
				return err
			}
			mark(index, DeliveryStatusDelivered, nil)
			sent = true
		}
		return nil
	}
	for _, index := range indexes {
		if err := sendUnit(index); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

func deliveryLogEvent(status DeliveryStatus) string {
	switch status {
	case DeliveryStatusDelivered:
		return "delivered"
	case DeliveryStatusFailed:
		return "failed"
	case DeliveryStatusSkipped:
		return "skipped"
	default:
		return "updated"
	}
}

func remainingWeComTextAfterStreamPreview(visibleText, preview string) (string, bool) {
	visibleText = strings.TrimSpace(visibleText)
	preview = strings.TrimSpace(preview)
	if visibleText == "" {
		return "", true
	}
	if preview == "" {
		return visibleText, true
	}
	if strings.HasPrefix(visibleText, preview) {
		return strings.TrimSpace(visibleText[len(preview):]), true
	}
	normalizedPreview := normalizeWeComMarkdown(preview)
	if normalizedPreview != preview && strings.HasPrefix(visibleText, normalizedPreview) {
		return strings.TrimSpace(visibleText[len(normalizedPreview):]), true
	}
	return "", false
}

func (s *Service) sendTextSegment(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text string) (bool, error) {
	parsed := ParseSendProtocol(text, workspacePath)
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

	rendered := renderWeComFinalMessage(parsed.VisibleText, workspacePath)
	visibleText := rendered.Text()
	if len(failures) > 0 {
		failureTextBlock := strings.Join(failures, "\n")
		if visibleText == "" {
			visibleText = failureTextBlock
		} else {
			visibleText += "\n\n" + failureTextBlock
		}
	}
	visibleText = normalizeWeComMarkdown(visibleText)
	if visibleText == "" {
		if msg.ReplyContext.ChatID == "" {
			sentRendered, err := s.sendRenderedActionsViaReply(ctx, sender, msg, rendered)
			return sentMedia || sentRendered, err
		}
		sentRendered, err := s.sendRenderedActionsViaSend(ctx, sender, msg, rendered)
		return sentMedia || sentRendered, err
	}
	if msg.ReplyContext.ChatID == "" {
		if err := sender.Reply(ctx, msg.ReplyContext, visibleText); err != nil {
			return true, err
		}
		_, err := s.sendRenderedActionsViaReply(ctx, sender, msg, rendered)
		return true, err
	}
	if err := sender.Send(ctx, msg.ReplyContext, visibleText); err != nil {
		return true, err
	}
	_, err := s.sendRenderedActionsViaSend(ctx, sender, msg, rendered)
	return true, err
}

func (s *Service) sendTextSegmentViaSend(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text string) (bool, error) {
	parsed := ParseSendProtocol(text, workspacePath)
	rendered := renderWeComFinalMessage(parsed.VisibleText, workspacePath)
	sent, err := s.sendParsedTextViaSend(ctx, sender, msg, parsed, rendered.Text())
	if err != nil {
		return sent, err
	}
	sentRendered, err := s.sendRenderedActionsViaSend(ctx, sender, msg, rendered)
	if err != nil {
		return sent || sentRendered, err
	}
	return sent || sentRendered, nil
}

func (s *Service) sendRenderedActionsViaSend(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, rendered RenderedMessage) (bool, error) {
	sent := false
	for _, unit := range rendered.Units {
		if unit.Action == nil {
			continue
		}
		if err := sender.SendMedia(ctx, msg.ReplyContext, *unit.Action); err != nil {
			return sent, err
		}
		sent = true
	}
	return sent, nil
}

func (s *Service) sendRenderedActionsViaReply(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, rendered RenderedMessage) (bool, error) {
	sent := false
	for _, unit := range rendered.Units {
		if unit.Action == nil {
			continue
		}
		if err := sender.ReplyMedia(ctx, msg.ReplyContext, *unit.Action); err != nil {
			return sent, err
		}
		sent = true
	}
	return sent, nil
}

func (s *Service) sendParsedTextViaSend(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, parsed ParsedSendProtocol, visibleText string) (bool, error) {
	sent := false
	failures := append([]string(nil), parsed.Failures...)
	for _, action := range parsed.Actions {
		if action.Caption != "" {
			if err := sender.Send(ctx, msg.ReplyContext, action.Caption); err != nil {
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
			sent = true
		}
		if err := sender.SendMedia(ctx, msg.ReplyContext, action); err != nil {
			failures = append(failures, failureText(action.Path, err.Error()))
			continue
		}
		sent = true
	}

	if len(failures) > 0 {
		failureTextBlock := strings.Join(failures, "\n")
		if visibleText == "" {
			visibleText = failureTextBlock
		} else {
			visibleText += "\n\n" + failureTextBlock
		}
	}
	visibleText = normalizeWeComMarkdown(visibleText)
	if visibleText == "" {
		return sent, nil
	}
	if err := sender.Send(ctx, msg.ReplyContext, visibleText); err != nil {
		return sent, err
	}
	return true, nil
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
