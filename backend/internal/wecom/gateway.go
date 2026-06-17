package wecom

import (
	"context"
	crand "crypto/rand"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"math/big"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/imagent"
	"github.com/pengmide/lumi/internal/imdebug"
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
	ReplyStream(ctx context.Context, rctx replyContext, streamID, content string, finish bool) error
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

type wecomStreamSender struct {
	mu              sync.Mutex
	sender          wsMessageSender
	rctx            replyContext
	workspacePath   string
	streamID        string
	lastSent        string
	pending         string
	failed          bool
	fallbackToSend  bool
	fallbackPreview string
	startedAt       time.Time
	lastFlush       time.Time
	timer           *time.Timer
}

type wecomStreamCompleteResult struct {
	FullDelivered bool
	Preview       string
	Remaining     string
}

const (
	wecomStreamFlushInterval = 200 * time.Millisecond
	// Keep stream replies inside WeCom's short-lived callback window. After
	// this, finish the preview and send the final answer through aibot_send_msg.
	wecomStreamSafeDuration = 290 * time.Second
	// replyStream.content protocol hard limit for WeCom WebSocket AI Bot.
	wecomStreamMaxBytes            = 20480
	wecomStreamLivePreviewMaxBytes = 16000
	wecomStreamFinalMaxBytes       = 20000
	wecomMarkdownSendMaxBytes      = 4096
	wecomLongReplyNotice           = "（回答较长，以下继续发送剩余内容）"
	wecomStreamFallbackNotice      = "（处理时间较长，最终结果将通过普通消息发送）"
)

var wecomStreamPlaceholders = []string{
	"收到你的消息，我会用最直白、不绕弯、一看就懂的方式回答你，请稍等片刻...\n",
	"我已经收到啦，先快速理清你的问题，再给你一个清楚直接的回答...\n",
	"收到，我会尽量把答案讲清楚、讲明白，不让你来回猜，请稍等...\n",
	"我先看一下你的问题，会用更好理解的方式整理后回复你...\n",
	"收到消息了，我会先抓重点，再给你一个不绕弯的回答...\n",
	"我正在处理你的问题，会尽量用简单清楚的话说明白...\n",
	"稍等一下，我先把问题拆清楚，再给你直接可用的答案...\n",
	"我看到了，接下来会用直白、清楚的方式回答你...\n",
	"收到，我会把重点整理好后回复，尽量让你一眼看懂...\n",
	"我先思考一下怎么讲最清楚，马上给你回复...\n",
	"消息收到，我会尽量少绕弯，直接说重点，请稍等...\n",
	"我正在准备回答，会把复杂的地方尽量讲简单...\n",
	"稍等片刻，我会先确认关键信息，再给你清楚的答复...\n",
	"收到，我会用更容易理解的方式来回答这个问题...\n",
	"我先整理一下思路，马上给你一个清晰的回复...\n",
	"看到了，我会尽快把答案组织成你容易判断的方式...\n",
	"收到你的问题，我会直接说结论，也会说明关键依据...\n",
	"请稍等，我会把答案讲得明确一点，避免含糊不清...\n",
	"我正在处理，会尽量用简洁、直接的方式回复你...\n",
	"收到，我会先抓住核心，再给你一个好理解的回答...\n",
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

func newWeComStreamSender(sender wsMessageSender, rctx replyContext, workspacePath string) *wecomStreamSender {
	return &wecomStreamSender{
		sender:        sender,
		rctx:          rctx,
		workspacePath: workspacePath,
		streamID:      sender.NewStreamID(),
		startedAt:     time.Now(),
	}
}

func randomWeComStreamPlaceholder() string {
	if len(wecomStreamPlaceholders) == 0 {
		return ""
	}
	n, err := crand.Int(crand.Reader, big.NewInt(int64(len(wecomStreamPlaceholders))))
	if err != nil {
		return wecomStreamPlaceholders[int(time.Now().UnixNano()%int64(len(wecomStreamPlaceholders)))]
	}
	return wecomStreamPlaceholders[n.Int64()]
}

func (s *wecomStreamSender) SendPlaceholder(ctx context.Context) {
	if s == nil || s.failed || s.rctx.ReqID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSent != "" {
		return
	}
	s.pending = randomWeComStreamPlaceholder()
	s.flushLocked(ctx, false)
}

func (s *wecomStreamSender) Update(ctx context.Context, fullText string) {
	if s == nil || s.rctx.ReqID == "" {
		return
	}
	visible := ParseSendProtocol(fullText, s.workspacePath).VisibleText
	visible = stabilizeWeComMarkdownStream(visible)
	liveLimit := wecomStreamLivePreviewMaxBytes
	if liveLimit > wecomStreamMaxBytes {
		liveLimit = wecomStreamMaxBytes
	}
	visible, _ = splitWeComLongReply(visible, liveLimit)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.fallbackToSend || visible == "" || visible == s.lastSent {
		return
	}
	s.pending = visible
	if time.Since(s.lastFlush) < wecomStreamFlushInterval {
		s.scheduleFlushLocked(ctx)
		return
	}
	s.stopTimerLocked()
	s.flushLocked(ctx, false)
}

func (s *wecomStreamSender) Complete(ctx context.Context, fullText string) wecomStreamCompleteResult {
	result := wecomStreamCompleteResult{FullDelivered: true}
	if s == nil || s.rctx.ReqID == "" {
		return result
	}
	visible := ParseSendProtocol(fullText, s.workspacePath).VisibleText
	visible = normalizeWeComMarkdown(visible)
	finalLimit := wecomStreamFinalMaxBytes
	if finalLimit > wecomStreamMaxBytes {
		finalLimit = wecomStreamMaxBytes
	}
	previewLimit := finalLimit - len("\n\n") - len(wecomLongReplyNotice)
	if previewLimit <= 0 {
		previewLimit = finalLimit
	}
	preview, remaining := splitWeComLongReply(visible, previewLimit)
	result.Preview = preview
	result.Remaining = remaining
	result.FullDelivered = strings.TrimSpace(remaining) == ""
	finalStreamText := preview
	if !result.FullDelivered {
		separator := "\n\n"
		if strings.HasSuffix(preview, "\n\n") {
			separator = ""
		} else if strings.HasSuffix(preview, "\n") {
			separator = "\n"
		}
		finalStreamText = preview + separator + wecomLongReplyNotice
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.fallbackToSend {
		return result
	}
	s.stopTimerLocked()
	if time.Since(s.startedAt) >= wecomStreamSafeDuration {
		s.finishForSendFallbackLocked(ctx, preview)
		return result
	}
	s.pending = finalStreamText
	s.flushLocked(ctx, true)
	return result
}

func (s *wecomStreamSender) Failed() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func (s *wecomStreamSender) SendFallbackPreview() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fallbackPreview, s.fallbackToSend
}

func (s *wecomStreamSender) scheduleFlushLocked(ctx context.Context) {
	delay := wecomStreamFlushInterval - time.Since(s.lastFlush)
	if delay < 0 {
		delay = 0
	}
	if s.timer != nil {
		s.timer.Reset(delay)
		return
	}
	s.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.timer = nil
		if s.failed || s.fallbackToSend || s.pending == "" || time.Since(s.lastFlush) < wecomStreamFlushInterval {
			return
		}
		s.flushLocked(ctx, false)
	})
}

func (s *wecomStreamSender) stopTimerLocked() {
	if s.timer == nil {
		return
	}
	s.timer.Stop()
	s.timer = nil
}

func (s *wecomStreamSender) flushLocked(ctx context.Context, finish bool) {
	if s.pending == "" && !finish {
		return
	}
	if s.failed || s.fallbackToSend {
		return
	}
	if time.Since(s.startedAt) >= wecomStreamSafeDuration && !finish {
		s.finishForSendFallbackLocked(ctx, s.pending)
		return
	}
	content := s.pending
	if finish && content == "" {
		content = s.lastSent
	}
	if content == "" {
		return
	}
	if err := s.sender.ReplyStream(ctx, s.rctx, s.streamID, content, finish); err != nil {
		log.Printf("wecom stream write failed: reqID=%s chatID=%s streamID=%s finish=%v err=%v", s.rctx.ReqID, s.rctx.ChatID, s.streamID, finish, err)
		s.failed = true
		return
	}
	s.lastSent = content
	s.pending = ""
	s.lastFlush = time.Now()
}

func (s *wecomStreamSender) finishForSendFallbackLocked(ctx context.Context, preview string) {
	content := appendWeComStreamFallbackNotice(preview)
	if err := s.sender.ReplyStream(ctx, s.rctx, s.streamID, content, true); err != nil {
		log.Printf("wecom stream fallback finish failed: reqID=%s chatID=%s streamID=%s err=%v", s.rctx.ReqID, s.rctx.ChatID, s.streamID, err)
		s.failed = true
		s.pending = ""
		return
	}
	log.Printf("wecom stream switched to send fallback: reqID=%s chatID=%s streamID=%s previewBytes=%d", s.rctx.ReqID, s.rctx.ChatID, s.streamID, len(preview))
	s.fallbackToSend = true
	s.fallbackPreview = strings.TrimSpace(preview)
	s.lastSent = content
	s.pending = ""
	s.lastFlush = time.Now()
}

func appendWeComStreamFallbackNotice(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return wecomStreamFallbackNotice
	}
	if strings.Contains(content, wecomStreamFallbackNotice) {
		return content
	}
	separator := "\n\n"
	if strings.HasSuffix(content, "\n\n") {
		separator = ""
	} else if strings.HasSuffix(content, "\n") {
		separator = "\n"
	}
	return content + separator + wecomStreamFallbackNotice
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
	if cfg.Stream && msg.ReplyContext.ReqID != "" {
		streamSender = newWeComStreamSender(sender, msg.ReplyContext, workspace.Path)
		streamSender.SendPlaceholder(ctx)
		sink.textUpdate = func(text string) {
			streamSender.Update(ctx, text)
		}
	}
	sink.sendSegment = func(segment imdebug.Segment) error {
		if segment.Kind == imdebug.SegmentDebug {
			return sender.Reply(ctx, msg.ReplyContext, segment.Text)
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
		CronTarget: lumicron.Target{WeCom: &lumicron.WeComTarget{
			ReqID:    msg.ReplyContext.ReqID,
			ChatID:   msg.ReplyContext.ChatID,
			ChatType: msg.ReplyContext.ChatType,
			UserID:   msg.ReplyContext.UserID,
		}},
	}
	log.Printf("wecom task start: conversationID=%s workspaceID=%s agentID=%s stream=%v", conversationID, workspace.ID, agentID, streamSender != nil)
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
	streamOriginalText := ""
	if sink.stopReason == "end_turn" && looksLikeIncompleteEndTurn(ParseSendProtocol(finalText, workspace.Path).VisibleText) {
		beforeContinue := finalText
		continueInput := chatInput
		continueInput.Message = continueReplyPrompt
		continueInput.Files = nil
		continueInput.NewSession = false
		log.Printf("wecom final appears incomplete, requesting continuation: conversationID=%s chars=%d tail=%q", conversationID, len([]rune(beforeContinue)), tailRunes(beforeContinue, 200))
		continueErr := func() error {
			previousTextUpdate := sink.textUpdate
			if streamSender != nil {
				sink.textUpdate = nil
				defer func() {
					sink.textUpdate = previousTextUpdate
				}()
			}
			return s.runner.RunWeComChat(runCtx, continueInput, sink)
		}()
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
			streamOriginalText = beforeContinue
		}
	}
	log.Printf("wecom final: conversationID=%s stopReason=%s bytes=%d chars=%d tail=%q", conversationID, sink.stopReason, len(finalText), len([]rune(finalText)), tailRunes(finalText, 200))
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
		if continuedText != "" && streamOriginalText != "" {
			streamFinalText = streamOriginalText
		} else if continuedText != "" && strings.HasSuffix(finalText, continuedText) {
			streamFinalText = strings.TrimSpace(finalText[:len(finalText)-len(continuedText)])
		}
		result := streamSender.Complete(ctx, streamFinalText)
		if _, fallback := streamSender.SendFallbackPreview(); !streamSender.Failed() && !fallback {
			sent, err := s.sendTextSegmentAfterStream(ctx, sender, msg, workspace.Path, streamFinalText)
			if err != nil {
				return err
			}
			if !result.FullDelivered && strings.TrimSpace(result.Remaining) != "" {
				if err := sender.Send(ctx, msg.ReplyContext, "续上：\n\n"+result.Remaining); err != nil {
					return err
				}
				if continuedText != "" {
					_, err := s.sendTextSegmentViaSend(ctx, sender, msg, workspace.Path, continuedText)
					return err
				}
				return nil
			}
			if continuedText != "" {
				sentContinuation, err := s.sendTextSegmentViaSend(ctx, sender, msg, workspace.Path, continuedText)
				if err != nil {
					return err
				}
				if sentContinuation {
					return nil
				}
			}
			if sent {
				return nil
			}
			return nil
		}
	}
	if streamSender != nil {
		if preview, fallback := streamSender.SendFallbackPreview(); fallback {
			sent, err := s.sendTextSegmentAfterStreamFallback(ctx, sender, msg, workspace.Path, finalText, preview)
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
		sent, err := s.sendTextSegmentViaSend(ctx, sender, msg, workspace.Path, finalText)
		if err != nil {
			return err
		}
		if sent {
			return nil
		}
		return sender.Send(ctx, msg.ReplyContext, fallbackDoneText)
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
	parsed := ParseSendProtocol(text, workspacePath)
	if len(parsed.Actions) == 0 && len(parsed.Failures) == 0 {
		return parsed.VisibleText != "", nil
	}
	sent := false
	failures := append([]string(nil), parsed.Failures...)
	for _, action := range parsed.Actions {
		if action.Caption != "" {
			if err := sender.Reply(ctx, msg.ReplyContext, action.Caption); err != nil {
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
			sent = true
		}
		if err := sender.ReplyMedia(ctx, msg.ReplyContext, action); err != nil {
			failures = append(failures, failureText(action.Path, err.Error()))
			continue
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

	visibleText := parsed.VisibleText
	if len(failures) > 0 {
		failureTextBlock := strings.Join(failures, "\n")
		if visibleText == "" {
			visibleText = failureTextBlock
		} else {
			visibleText += "\n\n" + failureTextBlock
		}
	}
	visibleText = normalizeWeComMarkdown(visibleText)
	if visibleText == "" && !sentMedia {
		return false, nil
	}
	if visibleText == "" {
		return sentMedia, nil
	}
	if msg.ReplyContext.ChatID == "" {
		return true, sender.Reply(ctx, msg.ReplyContext, visibleText)
	}
	return true, sender.Send(ctx, msg.ReplyContext, visibleText)
}

func (s *Service) sendTextSegmentViaSend(ctx context.Context, sender wsMessageSender, msg WeComInboundMessage, workspacePath, text string) (bool, error) {
	parsed := ParseSendProtocol(text, workspacePath)
	return s.sendParsedTextViaSend(ctx, sender, msg, parsed, parsed.VisibleText)
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
