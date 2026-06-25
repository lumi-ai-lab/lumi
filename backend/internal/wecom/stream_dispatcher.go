package wecom

import (
	"context"
	crand "crypto/rand"
	"errors"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type WeComStreamDispatcher struct {
	mu              sync.Mutex
	sender          wsMessageSender
	rctx            replyContext
	workspacePath   string
	policy          WeComStreamPolicy
	runtimeConfig   WeComRuntimeConfig
	traceID         string
	streamID        string
	lastSent        string
	pending         string
	failed          bool
	fallbackToSend  bool
	fallbackPreview string
	startedAt       time.Time
	lastFlush       time.Time
	timer           *time.Timer
	lastErr         error
	lastErrClass    WeComStreamErrorClass
	ledger          *DeliveryLedger
	finalUnitIndex  int
	updateCount     int
	deliveredPrefix string
	slots           []provisionalStreamSlot
	activeSlot      int
}

type wecomStreamSender = WeComStreamDispatcher

type wecomStreamCompleteResult struct {
	FullDelivered bool
	Preview       string
	Remaining     string
}

type WeComStreamErrorClass string

type provisionalStreamSlotStatus string

const (
	provisionalStreamSlotActive      provisionalStreamSlotStatus = "active"
	provisionalStreamSlotProvisional provisionalStreamSlotStatus = "provisional"
	provisionalStreamSlotFinalizing  provisionalStreamSlotStatus = "finalizing"
	provisionalStreamSlotFinalized   provisionalStreamSlotStatus = "finalized"
	provisionalStreamSlotFailed      provisionalStreamSlotStatus = "failed"
)

type provisionalStreamSlot struct {
	StreamID    string
	LiveText    string
	FinalText   string
	Status      provisionalStreamSlotStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	UpdateCount int
	ErrorClass  WeComStreamErrorClass
	Error       string
}

const (
	WeComStreamErrorNone         WeComStreamErrorClass = ""
	WeComStreamErrorAckTimeout   WeComStreamErrorClass = "ack_timeout"
	WeComStreamErrorExpired      WeComStreamErrorClass = "stream_expired"
	WeComStreamErrorDisconnected WeComStreamErrorClass = "websocket_disconnected"
	WeComStreamErrorWriteFailed  WeComStreamErrorClass = "write_failed"
)

const (
	advancedRenderingStartNotice = "开始进行回答格式的渲染优化..."
	advancedTableOptimizedNotice = "✅ 回答中的表格内容已完成优化"
	advancedAllOptimizedNotice   = "✅ 回答中全部渲染效果完成优化"
	advancedAnswerCompleteNotice = "✅ 回答完成, 请查看上文结果"
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

func NewWeComStreamDispatcher(sender wsMessageSender, rctx replyContext, workspacePath string, policy WeComStreamPolicy) *WeComStreamDispatcher {
	return newWeComStreamDispatcher(sender, rctx, workspacePath, policy, "")
}

func newWeComStreamDispatcher(sender wsMessageSender, rctx replyContext, workspacePath string, policy WeComStreamPolicy, traceID string) *WeComStreamDispatcher {
	runtimeConfig := loadWeComRuntimeConfigFromEnv()
	if policy.FinalMaxBytes <= 0 || policy.FinalMaxBytes == defaultWeComStreamPolicy.FinalMaxBytes {
		if _, ok := os.LookupEnv("LUMI_WECOM_STREAM_FINAL_MAX_BYTES"); !ok {
			runtimeConfig.StreamPolicy.FinalMaxBytes = wecomMarkdownSendMaxBytes
			if policy.FinalMaxBytes == defaultWeComStreamPolicy.FinalMaxBytes {
				policy.FinalMaxBytes = wecomMarkdownSendMaxBytes
			}
		}
	}
	policy = mergeWeComStreamPolicy(runtimeConfig.StreamPolicy, policy)
	runtimeConfig.StreamPolicy = policy
	if traceID == "" {
		traceID = newWeComTraceID()
	}
	dispatcher := &WeComStreamDispatcher{
		sender:         sender,
		rctx:           rctx,
		workspacePath:  workspacePath,
		policy:         policy,
		runtimeConfig:  runtimeConfig,
		traceID:        traceID,
		streamID:       sender.NewStreamID(),
		startedAt:      time.Now(),
		ledger:         NewDeliveryLedger(),
		finalUnitIndex: -1,
		activeSlot:     -1,
	}
	dispatcher.ensureActiveSlotLocked()
	dispatcher.logStreamEvent("created", "initial", 0, nil)
	return dispatcher
}

func mergeWeComStreamPolicy(base, override WeComStreamPolicy) WeComStreamPolicy {
	if base.FlushInterval <= 0 {
		base.FlushInterval = defaultWeComStreamPolicy.FlushInterval
	}
	if base.SafeDuration <= 0 {
		base.SafeDuration = defaultWeComStreamPolicy.SafeDuration
	}
	if base.MaxBytes <= 0 {
		base.MaxBytes = defaultWeComStreamPolicy.MaxBytes
	}
	if base.LivePreviewMaxBytes <= 0 {
		base.LivePreviewMaxBytes = defaultWeComStreamPolicy.LivePreviewMaxBytes
	}
	if base.FinalMaxBytes <= 0 {
		base.FinalMaxBytes = defaultWeComStreamPolicy.FinalMaxBytes
	}
	if base.MaxAge <= 0 {
		base.MaxAge = defaultWeComStreamPolicy.MaxAge
	}
	if base.MaxUpdates <= 0 {
		base.MaxUpdates = defaultWeComStreamPolicy.MaxUpdates
	}
	if base.MinUpdateGap <= 0 {
		base.MinUpdateGap = base.FlushInterval
	}
	if base.CoalesceGap <= 0 {
		base.CoalesceGap = base.FlushInterval
	}
	if base.LongReplyNotice == "" {
		base.LongReplyNotice = defaultWeComStreamPolicy.LongReplyNotice
	}
	if base.FallbackNotice == "" {
		base.FallbackNotice = defaultWeComStreamPolicy.FallbackNotice
	}
	if override.FlushInterval > 0 {
		base.FlushInterval = override.FlushInterval
	}
	if override.SafeDuration > 0 {
		base.SafeDuration = override.SafeDuration
	}
	if override.MaxBytes > 0 {
		base.MaxBytes = override.MaxBytes
	}
	if override.LivePreviewMaxBytes > 0 {
		base.LivePreviewMaxBytes = override.LivePreviewMaxBytes
	}
	if override.FinalMaxBytes > 0 {
		base.FinalMaxBytes = override.FinalMaxBytes
	}
	if override.MaxAge > 0 {
		base.MaxAge = override.MaxAge
	}
	if override.MaxUpdates > 0 {
		base.MaxUpdates = override.MaxUpdates
	}
	if override.MinUpdateGap > 0 {
		base.MinUpdateGap = override.MinUpdateGap
	}
	if override.CoalesceGap > 0 {
		base.CoalesceGap = override.CoalesceGap
	}
	if override.LongReplyNotice != "" {
		base.LongReplyNotice = override.LongReplyNotice
	}
	if override.FallbackNotice != "" {
		base.FallbackNotice = override.FallbackNotice
	}
	return base
}

func newWeComStreamSender(sender wsMessageSender, rctx replyContext, workspacePath string) *wecomStreamSender {
	return newWeComStreamDispatcher(sender, rctx, workspacePath, WeComStreamPolicy{}, "")
}

func newWeComStreamSenderWithTrace(sender wsMessageSender, rctx replyContext, workspacePath, traceID string) *wecomStreamSender {
	return newWeComStreamDispatcher(sender, rctx, workspacePath, WeComStreamPolicy{}, traceID)
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

func (s *WeComStreamDispatcher) SendPlaceholder(ctx context.Context) {
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

func (s *WeComStreamDispatcher) updateAdvanced(ctx context.Context, fullText string) {
	visible := renderWeComAdvancedLivePreview(fullText, s.workspacePath, s.policy)
	liveLimit := s.policy.LivePreviewMaxBytes
	if liveLimit > s.policy.MaxBytes {
		liveLimit = s.policy.MaxBytes
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.fallbackToSend {
		return
	}
	s.ensureActiveSlotLocked()
	visible, _ = trimDeliveredPrefix(visible, s.deliveredPrefix)
	rotationLimit := s.policy.MaxBytes
	if rotationLimit <= 0 {
		rotationLimit = liveLimit
	}
	for rotationLimit > 0 && len(visible) > rotationLimit {
		chunkLimit := liveLimit
		if chunkLimit <= 0 || chunkLimit > rotationLimit {
			chunkLimit = rotationLimit
		}
		chunk, rest := splitWeComLongReply(visible, chunkLimit)
		if strings.TrimSpace(chunk) == "" {
			cut := utf8SafeIndex(visible, chunkLimit)
			if cut <= 0 {
				break
			}
			chunk, rest = visible[:cut], visible[cut:]
		}
		s.pending = strings.TrimSpace(chunk)
		s.flushLocked(ctx, false)
		if s.failed || s.fallbackToSend {
			return
		}
		s.rotateAdvancedStreamLocked()
		visible = strings.TrimSpace(rest)
	}
	if liveLimit > 0 && len(visible) > liveLimit {
		visible, _ = splitWeComLongReply(visible, liveLimit)
	}
	if visible == "" || visible == s.lastSent {
		return
	}
	if s.shouldRotateBeforeUpdateLocked() {
		previous := s.lastSent
		s.rotateAdvancedStreamLocked()
		if previous != "" && strings.HasPrefix(visible, previous) {
			visible = strings.TrimSpace(visible[len(previous):])
			if visible == "" {
				return
			}
		}
	}
	s.pending = visible
	if time.Since(s.lastFlush) < s.policy.MinUpdateGap {
		s.scheduleFlushLocked(ctx)
		return
	}
	s.stopTimerLocked()
	s.flushLocked(ctx, false)
}

func (s *WeComStreamDispatcher) Update(ctx context.Context, fullText string) {
	if s == nil || s.rctx.ReqID == "" {
		return
	}
	s.updateAdvanced(ctx, fullText)
}

func (s *WeComStreamDispatcher) completeAdvanced(ctx context.Context, fullText string) wecomStreamCompleteResult {
	result := wecomStreamCompleteResult{FullDelivered: true}
	parsed := ParseSendProtocol(fullText, s.workspacePath)
	rendered := renderWeComCoverableFinalMessageWithConfig(parsed.VisibleText, s.workspacePath, s.runtimeConfig)
	finalText := advancedCoverableFinalText(rendered)
	if strings.TrimSpace(finalText) == "" {
		finalText = normalizeWeComMarkdown(parsed.VisibleText)
	}
	chunks := s.splitFinalStreamText(finalText)
	result.Preview = strings.TrimSpace(strings.Join(chunks, "\n\n"))
	result.FullDelivered = true
	result.Remaining = ""

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.fallbackToSend {
		return result
	}
	s.stopTimerLocked()
	s.resetAdvancedFinalLedgerLocked(parsed, rendered, chunks)
	var streamErr error
	for i := range s.ledger.units {
		unit := s.ledger.units[i]
		if unit.DeliveryMethod != DeliveryMethodStream {
			continue
		}
		s.finalUnitIndex = i
		s.streamID = unit.StreamID
		s.markSlotStatusLocked(unit.StreamID, provisionalStreamSlotFinalizing, nil)
		if err := s.sender.ReplyStreamFinal(ctx, s.rctx, unit.StreamID, unit.Text); err != nil {
			streamErr = err
			s.ledger.Mark(i, DeliveryStatusFailed, err)
			s.logDeliveryUnitEvent("failed", i, err)
			s.markSlotStatusLocked(unit.StreamID, provisionalStreamSlotFailed, err)
			s.logStreamEvent("failed", "final_ack", len(unit.Text), err)
			continue
		}
		s.ledger.Mark(i, DeliveryStatusDelivered, nil)
		s.logDeliveryUnitEvent("delivered", i, nil)
		s.markSlotStatusLocked(unit.StreamID, provisionalStreamSlotFinalized, nil)
		s.logStreamEvent("finished", "final_ack", len(unit.Text), nil)
		s.lastSent = unit.Text
		s.lastFlush = time.Now()
	}
	if streamErr != nil {
		result.FullDelivered = false
		s.failed = true
		s.lastErr = streamErr
		s.lastErrClass = classifyWeComStreamError(streamErr)
		return result
	}
	for i := range s.ledger.units {
		if s.ledger.units[i].DeliveryMethod == DeliveryMethodStream {
			continue
		}
		if err := s.deliverAtomicUnitLocked(ctx, i); err != nil {
			result.FullDelivered = false
			return result
		}
	}
	s.logStreamEvent("advanced_complete", "final_ack", len(finalText), nil)
	return result
}

func (s *WeComStreamDispatcher) Complete(ctx context.Context, fullText string) wecomStreamCompleteResult {
	result := wecomStreamCompleteResult{FullDelivered: true}
	if s == nil || s.rctx.ReqID == "" {
		return result
	}
	return s.completeAdvanced(ctx, fullText)
}

func (s *WeComStreamDispatcher) Failed() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

func (s *WeComStreamDispatcher) Failure() (WeComStreamErrorClass, error) {
	if s == nil {
		return WeComStreamErrorNone, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErrClass, s.lastErr
}

func (s *WeComStreamDispatcher) TraceID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.traceID
}

func (s *WeComStreamDispatcher) LedgerSnapshot() []DeliveredUnit {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.Units()
}

func (s *WeComStreamDispatcher) MarkLedgerUnit(id string, status DeliveryStatus, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger.MarkByID(id, status, err)
}

func (s *WeComStreamDispatcher) SendFallbackPreview() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fallbackPreview, s.fallbackToSend
}

func (s *WeComStreamDispatcher) scheduleFlushLocked(ctx context.Context) {
	elapsed := time.Since(s.lastFlush)
	delay := s.policy.CoalesceGap
	if minDelay := s.policy.MinUpdateGap - elapsed; minDelay > delay {
		delay = minDelay
	}
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
		if s.failed || s.fallbackToSend || s.pending == "" {
			return
		}
		if time.Since(s.lastFlush) < s.policy.MinUpdateGap {
			s.scheduleFlushLocked(ctx)
			return
		}
		s.flushLocked(ctx, false)
	})
}

func (s *WeComStreamDispatcher) stopTimerLocked() {
	if s.timer == nil {
		return
	}
	s.timer.Stop()
	s.timer = nil
}

func (s *WeComStreamDispatcher) flushLocked(ctx context.Context, finish bool) {
	if s.pending == "" && !finish {
		return
	}
	if s.failed || s.fallbackToSend {
		return
	}
	content := s.pending
	if finish && content == "" {
		content = s.lastSent
	}
	if content == "" {
		return
	}
	if err := s.sendStreamFrame(ctx, content, finish); err != nil {
		if finish {
			s.ledger.Mark(s.finalUnitIndex, DeliveryStatusFailed, err)
			s.logDeliveryUnitEvent("failed", s.finalUnitIndex, err)
		}
		s.markFailedLocked(err)
		return
	}
	if finish {
		s.ledger.Mark(s.finalUnitIndex, DeliveryStatusDelivered, nil)
		s.logDeliveryUnitEvent("delivered", s.finalUnitIndex, nil)
		s.logStreamEvent("finished", "final_ack", len(content), nil)
	} else {
		s.updateActiveSlotLiveLocked(content)
		s.logStreamEvent("updated", "best_effort", len(content), nil)
	}
	s.lastSent = content
	s.pending = ""
	s.lastFlush = time.Now()
	if !finish {
		s.updateCount++
	}
}

func (s *WeComStreamDispatcher) sendStreamFrame(ctx context.Context, content string, finish bool) error {
	if finish {
		return s.sender.ReplyStreamFinal(ctx, s.rctx, s.streamID, content)
	}
	return s.sender.ReplyStream(ctx, s.rctx, s.streamID, content, false)
}

func (s *WeComStreamDispatcher) deliverAtomicUnitLocked(ctx context.Context, index int) error {
	if index < 0 || index >= len(s.ledger.units) {
		return nil
	}
	unit := s.ledger.units[index]
	switch unit.DeliveryMethod {
	case DeliveryMethodMedia:
		if unit.Action == nil {
			s.ledger.Mark(index, DeliveryStatusSkipped, nil)
			return nil
		}
		if err := s.sender.SendMedia(ctx, s.rctx, *unit.Action); err != nil {
			s.ledger.Mark(index, DeliveryStatusFailed, err)
			s.logDeliveryUnitEvent("failed", index, err)
			return err
		}
		s.ledger.Mark(index, DeliveryStatusDelivered, nil)
		s.logDeliveryUnitEvent("delivered", index, nil)
		s.logStreamEvent("media_delivered", unit.RenderedKind, 0, nil)
	case DeliveryMethodSend:
		text := strings.TrimSpace(unit.Text)
		if text == "" {
			s.ledger.Mark(index, DeliveryStatusSkipped, nil)
			s.logDeliveryUnitEvent("skipped", index, nil)
			return nil
		}
		if err := s.sender.Send(ctx, s.rctx, normalizeWeComMarkdown(text)); err != nil {
			s.ledger.Mark(index, DeliveryStatusFailed, err)
			s.logDeliveryUnitEvent("failed", index, err)
			return err
		}
		s.ledger.Mark(index, DeliveryStatusDelivered, nil)
		s.logDeliveryUnitEvent("delivered", index, nil)
	}
	return nil
}

func (s *WeComStreamDispatcher) shouldRotateBeforeUpdateLocked() bool {
	if s.lastSent == "" {
		return false
	}
	if s.policy.MaxUpdates > 0 && s.updateCount >= s.policy.MaxUpdates {
		return true
	}
	return s.policy.MaxAge > 0 && time.Since(s.startedAt) >= s.policy.MaxAge
}

func (s *WeComStreamDispatcher) finishCurrentStreamForRotationLocked(ctx context.Context) {
	content := s.pending
	if content == "" {
		content = s.lastSent
	}
	if strings.TrimSpace(content) == "" {
		s.rotateStreamLocked()
		return
	}
	if err := s.sender.ReplyStreamFinal(ctx, s.rctx, s.streamID, content); err != nil {
		s.markFailedLocked(err)
		s.pending = ""
		return
	}
	s.deliveredPrefix = appendDeliveredPrefix(s.deliveredPrefix, content)
	s.logStreamEvent("finished", "rotation", len(content), nil)
	s.lastSent = content
	s.pending = ""
	s.lastFlush = time.Now()
	s.rotateStreamLocked()
}

func (s *WeComStreamDispatcher) rotateStreamLocked() bool {
	streamID := s.sender.NewStreamID()
	if streamID == "" {
		err := errors.New("wecom stream rotation failed: empty stream id")
		s.markFailedLocked(err)
		return false
	}
	s.streamID = streamID
	s.startedAt = time.Now()
	s.lastSent = ""
	s.pending = ""
	s.updateCount = 0
	s.logStreamEvent("rotated", "threshold", 0, nil)
	return true
}

func (s *WeComStreamDispatcher) rotateAdvancedStreamLocked() bool {
	s.ensureActiveSlotLocked()
	if s.activeSlot >= 0 && s.activeSlot < len(s.slots) {
		slot := &s.slots[s.activeSlot]
		if strings.TrimSpace(slot.LiveText) != "" {
			slot.Status = provisionalStreamSlotProvisional
			slot.UpdatedAt = time.Now()
			s.deliveredPrefix = appendDeliveredPrefix(s.deliveredPrefix, slot.LiveText)
			s.logSlotEventLocked("slot_rotated_unfinished", s.activeSlot, "rotation", len(slot.LiveText), nil)
		}
	}
	streamID := s.sender.NewStreamID()
	if streamID == "" {
		err := errors.New("wecom stream rotation failed: empty stream id")
		s.markFailedLocked(err)
		return false
	}
	now := time.Now()
	s.streamID = streamID
	s.startedAt = now
	s.lastSent = ""
	s.pending = ""
	s.updateCount = 0
	s.slots = append(s.slots, provisionalStreamSlot{
		StreamID:  streamID,
		Status:    provisionalStreamSlotActive,
		CreatedAt: now,
		UpdatedAt: now,
	})
	s.activeSlot = len(s.slots) - 1
	s.logSlotEventLocked("slot_created", s.activeSlot, "rotation", 0, nil)
	s.logStreamEvent("rotated", "advanced_provisional", 0, nil)
	return true
}

func (s *WeComStreamDispatcher) ensureActiveSlotLocked() {
	if s.activeSlot >= 0 && s.activeSlot < len(s.slots) {
		return
	}
	now := time.Now()
	s.slots = append(s.slots, provisionalStreamSlot{
		StreamID:  s.streamID,
		Status:    provisionalStreamSlotActive,
		CreatedAt: now,
		UpdatedAt: now,
	})
	s.activeSlot = len(s.slots) - 1
	s.logSlotEventLocked("slot_created", s.activeSlot, "initial", 0, nil)
}

func (s *WeComStreamDispatcher) updateActiveSlotLiveLocked(content string) {
	s.ensureActiveSlotLocked()
	if s.activeSlot < 0 || s.activeSlot >= len(s.slots) {
		return
	}
	slot := &s.slots[s.activeSlot]
	slot.StreamID = s.streamID
	slot.LiveText = content
	slot.Status = provisionalStreamSlotActive
	slot.UpdateCount = s.updateCount + 1
	slot.UpdatedAt = time.Now()
	s.logSlotEventLocked("slot_updated", s.activeSlot, "live_update", len(content), nil)
}

func (s *WeComStreamDispatcher) markSlotStatusLocked(streamID string, status provisionalStreamSlotStatus, err error) {
	for i := range s.slots {
		if s.slots[i].StreamID != streamID {
			continue
		}
		s.slots[i].Status = status
		s.slots[i].UpdatedAt = time.Now()
		if err != nil {
			s.slots[i].ErrorClass = classifyWeComStreamError(err)
			s.slots[i].Error = err.Error()
		}
		reason := string(status)
		if status == provisionalStreamSlotFinalizing {
			reason = "finalizing"
		} else if status == provisionalStreamSlotFinalized {
			reason = "final_ack"
		} else if status == provisionalStreamSlotFailed {
			reason = "failed"
		}
		s.logSlotEventLocked("slot_"+reason, i, reason, len(s.slots[i].FinalText), err)
		return
	}
}

func (s *WeComStreamDispatcher) setSlotFinalTextLocked(streamID, text string) {
	for i := range s.slots {
		if s.slots[i].StreamID != streamID {
			continue
		}
		s.slots[i].FinalText = text
		s.slots[i].UpdatedAt = time.Now()
		return
	}
}

func (s *WeComStreamDispatcher) markFailedLocked(err error) {
	log.Printf("wecom stream failed: traceID=%s reqID=%s chatID=%s streamID=%s class=%s err=%v", s.traceID, s.rctx.ReqID, s.rctx.ChatID, s.streamID, classifyWeComStreamError(err), err)
	s.failed = true
	s.lastErr = err
	s.lastErrClass = classifyWeComStreamError(err)
	s.markSlotStatusLocked(s.streamID, provisionalStreamSlotFailed, err)
}

func (s *WeComStreamDispatcher) finishForSendFallbackLocked(ctx context.Context, preview string) {
	content := appendWeComStreamNotice(preview, s.policy.FallbackNotice)
	if err := s.sender.ReplyStreamFinal(ctx, s.rctx, s.streamID, content); err != nil {
		s.ledger.Mark(s.finalUnitIndex, DeliveryStatusFailed, err)
		s.logDeliveryUnitEvent("failed", s.finalUnitIndex, err)
		s.markFailedLocked(err)
		s.pending = ""
		return
	}
	log.Printf("wecom fallback reason=safe_duration traceID=%s reqID=%s chatID=%s streamID=%s unitID=%s method=%s previewBytes=%d", s.traceID, s.rctx.ReqID, s.rctx.ChatID, s.streamID, s.unitID(s.finalUnitIndex), DeliveryMethodStream, len(preview))
	s.fallbackToSend = true
	s.fallbackPreview = strings.TrimSpace(preview)
	s.ledger.Mark(s.finalUnitIndex, DeliveryStatusDelivered, nil)
	s.logDeliveryUnitEvent("delivered", s.finalUnitIndex, nil)
	s.lastSent = content
	s.pending = ""
	s.lastFlush = time.Now()
}

func (s *WeComStreamDispatcher) resetFinalLedgerLocked(parsed ParsedSendProtocol, rendered RenderedMessage) {
	s.ledger = NewDeliveryLedger()
	s.finalUnitIndex = -1
	streamIndex := 0
	sendIndex := 0
	actionIndex := 0
	deliveredPrefix := strings.TrimSpace(s.deliveredPrefix)
	var streamText strings.Builder
	flushStreamText := func() {
		text := strings.TrimSpace(streamText.String())
		streamText.Reset()
		text, deliveredPrefix = trimDeliveredPrefix(text, deliveredPrefix)
		for _, chunk := range s.splitFinalStreamText(text) {
			streamIndex++
			s.ledger.Add(DeliveredUnit{
				ID:             "stream-" + strconv.Itoa(streamIndex),
				SourceType:     "answer",
				RenderedKind:   "text",
				Text:           strings.TrimSpace(chunk),
				StreamID:       s.streamID,
				DeliveryMethod: DeliveryMethodStream,
				Status:         DeliveryStatusPending,
			})
		}
	}
	addRenderedUnit := func(unit RenderedUnit) {
		if renderedUnitCanStream(unit) {
			appendRenderedStreamText(&streamText, unit)
			return
		}
		flushStreamText()
		if strings.TrimSpace(unit.Text) != "" && !renderedUnitCanStream(unit) {
			sendIndex++
			s.ledger.Add(DeliveredUnit{
				ID:             "atomic-text-" + strconv.Itoa(sendIndex),
				SourceType:     defaultRenderedSourceType(unit.SourceType),
				RenderedKind:   unit.Kind,
				Text:           strings.TrimSpace(unit.Text),
				DeliveryMethod: DeliveryMethodSend,
				Status:         DeliveryStatusPending,
			})
		}
		if unit.Action != nil {
			actionIndex++
			actionCopy := *unit.Action
			s.ledger.Add(DeliveredUnit{
				ID:             "rendered-action-" + strconv.Itoa(actionIndex),
				SourceType:     defaultRenderedSourceType(unit.SourceType),
				RenderedKind:   unit.Kind,
				Action:         &actionCopy,
				DeliveryMethod: DeliveryMethodMedia,
				Status:         DeliveryStatusPending,
			})
		}
	}
	addFailure := func(failure string) {
		flushStreamText()
		sendIndex++
		s.ledger.Add(DeliveredUnit{
			ID:             "protocol-failure-" + strconv.Itoa(sendIndex),
			SourceType:     "protocol",
			RenderedKind:   "text",
			Text:           failure,
			DeliveryMethod: DeliveryMethodSend,
			Status:         DeliveryStatusPending,
		})
	}
	addProtocolAction := func(action SendAction) {
		flushStreamText()
		actionCopy := action
		if action.Caption != "" {
			sendIndex++
			s.ledger.Add(DeliveredUnit{
				ID:             "media-caption-" + strconv.Itoa(sendIndex),
				SourceType:     "protocol",
				RenderedKind:   "text",
				Text:           action.Caption,
				DeliveryMethod: DeliveryMethodSend,
				Status:         DeliveryStatusPending,
			})
		}
		actionIndex++
		s.ledger.Add(DeliveredUnit{
			ID:             "media-" + strconv.Itoa(actionIndex),
			SourceType:     "protocol",
			RenderedKind:   action.Type,
			Action:         &actionCopy,
			DeliveryMethod: DeliveryMethodMedia,
			Status:         DeliveryStatusPending,
		})
	}
	if len(parsed.Segments) == 0 {
		for _, unit := range rendered.Units {
			addRenderedUnit(unit)
		}
		flushStreamText()
	} else {
		for _, segment := range parsed.Segments {
			switch {
			case strings.TrimSpace(segment.Text) != "":
				segmentRendered := renderWeComFinalMessageWithConfig(segment.Text, s.workspacePath, s.runtimeConfig)
				for _, unit := range segmentRendered.Units {
					addRenderedUnit(unit)
				}
			case segment.Action != nil:
				addProtocolAction(*segment.Action)
			case strings.TrimSpace(segment.Failure) != "":
				addFailure(segment.Failure)
			}
		}
		flushStreamText()
	}
	for i := range s.ledger.units {
		s.logDeliveryUnitEvent("created", i, nil)
	}
}

func (s *WeComStreamDispatcher) resetAdvancedFinalLedgerLocked(parsed ParsedSendProtocol, rendered RenderedMessage, chunks []string) {
	s.ledger = NewDeliveryLedger()
	s.finalUnitIndex = -1
	s.ensureActiveSlotLocked()
	slots := make([]provisionalStreamSlot, 0, len(s.slots))
	for _, slot := range s.slots {
		if strings.TrimSpace(slot.StreamID) == "" {
			continue
		}
		if strings.TrimSpace(slot.LiveText) != "" || slot.Status == provisionalStreamSlotActive {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		slots = append(slots, provisionalStreamSlot{StreamID: s.streamID})
	}
	for len(slots) < len(chunks) {
		streamID := s.sender.NewStreamID()
		now := time.Now()
		slot := provisionalStreamSlot{
			StreamID:  streamID,
			Status:    provisionalStreamSlotActive,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.slots = append(s.slots, slot)
		slots = append(slots, slot)
	}
	extraNotices := advancedExtraSlotNotices(len(slots) - len(chunks))
	for i, slot := range slots {
		text := ""
		if i < len(chunks) && strings.TrimSpace(chunks[i]) != "" {
			text = chunks[i]
		} else if noticeIndex := i - len(chunks); noticeIndex >= 0 && noticeIndex < len(extraNotices) {
			text = extraNotices[noticeIndex]
		}
		s.ledger.Add(DeliveredUnit{
			ID:             "stream-" + strconv.Itoa(i+1),
			SourceType:     "answer",
			RenderedKind:   "text",
			Text:           strings.TrimSpace(text),
			StreamID:       slot.StreamID,
			DeliveryMethod: DeliveryMethodStream,
			Status:         DeliveryStatusPending,
		})
		s.setSlotFinalTextLocked(slot.StreamID, strings.TrimSpace(text))
	}
	actionIndex := 0
	for _, unit := range rendered.Units {
		if unit.Action == nil {
			continue
		}
		actionIndex++
		actionCopy := *unit.Action
		s.ledger.Add(DeliveredUnit{
			ID:             "rendered-action-" + strconv.Itoa(actionIndex),
			SourceType:     defaultRenderedSourceType(unit.SourceType),
			RenderedKind:   unit.Kind,
			Action:         &actionCopy,
			DeliveryMethod: DeliveryMethodMedia,
			Status:         DeliveryStatusPending,
		})
	}
	for _, failure := range parsed.Failures {
		if strings.TrimSpace(failure) == "" {
			continue
		}
		actionIndex++
		s.ledger.Add(DeliveredUnit{
			ID:             "protocol-failure-" + strconv.Itoa(actionIndex),
			SourceType:     "protocol",
			RenderedKind:   "text",
			Text:           failure,
			DeliveryMethod: DeliveryMethodSend,
			Status:         DeliveryStatusPending,
		})
	}
	finalTextForCaption := strings.Join(chunks, "\n\n")
	for _, action := range parsed.Actions {
		caption := strings.TrimSpace(action.Caption)
		if caption != "" && !containsStandaloneWeComTextBlock(finalTextForCaption, caption) {
			actionIndex++
			s.ledger.Add(DeliveredUnit{
				ID:             "media-caption-" + strconv.Itoa(actionIndex),
				SourceType:     "protocol",
				RenderedKind:   "text",
				Text:           caption,
				DeliveryMethod: DeliveryMethodSend,
				Status:         DeliveryStatusPending,
			})
		}
		actionIndex++
		actionCopy := action
		s.ledger.Add(DeliveredUnit{
			ID:             "media-" + strconv.Itoa(actionIndex),
			SourceType:     "protocol",
			RenderedKind:   action.Type,
			Action:         &actionCopy,
			DeliveryMethod: DeliveryMethodMedia,
			Status:         DeliveryStatusPending,
		})
	}
	for i := range s.ledger.units {
		s.logDeliveryUnitEvent("created", i, nil)
	}
}

func advancedExtraSlotNotices(count int) []string {
	if count <= 0 {
		return nil
	}
	tail := []string{
		advancedTableOptimizedNotice,
		advancedAllOptimizedNotice,
		advancedAnswerCompleteNotice,
	}
	if count < len(tail) {
		tail = tail[len(tail)-count:]
	}
	out := make([]string, 0, count)
	for len(out)+len(tail) < count {
		out = append(out, advancedRenderingStartNotice)
	}
	out = append(out, tail...)
	return out
}

func isAdvancedExtraSlotNotice(text string) bool {
	switch strings.TrimSpace(text) {
	case advancedRenderingStartNotice, advancedTableOptimizedNotice, advancedAllOptimizedNotice, advancedAnswerCompleteNotice:
		return true
	default:
		return false
	}
}

func newWeComTraceID() string {
	var b [8]byte
	if _, err := crand.Read(b[:]); err == nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(uint64(b[0])<<56|uint64(b[1])<<48|uint64(b[2])<<40|uint64(b[3])<<32|uint64(b[4])<<24|uint64(b[5])<<16|uint64(b[6])<<8|uint64(b[7]), 36)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (s *WeComStreamDispatcher) unitID(index int) string {
	if s == nil || index < 0 || s.ledger == nil || index >= len(s.ledger.units) {
		return ""
	}
	return s.ledger.units[index].ID
}

func (s *WeComStreamDispatcher) logStreamEvent(event, reason string, bytes int, err error) {
	if s == nil {
		return
	}
	mode := "advanced"
	log.Printf("wecom stream event=%s mode=%s traceID=%s reqID=%s chatID=%s streamID=%s reason=%s bytes=%d updates=%d err=%v", event, mode, s.traceID, s.rctx.ReqID, s.rctx.ChatID, s.streamID, reason, bytes, s.updateCount, err)
}

func (s *WeComStreamDispatcher) logSlotEventLocked(event string, index int, reason string, bytes int, err error) {
	if s == nil || index < 0 || index >= len(s.slots) {
		return
	}
	slot := s.slots[index]
	log.Printf("wecom stream slot event=%s mode=advanced traceID=%s reqID=%s chatID=%s streamID=%s slotIndex=%d slotStatus=%s reason=%s bytes=%d updates=%d errClass=%s err=%v", event, s.traceID, s.rctx.ReqID, s.rctx.ChatID, slot.StreamID, index, slot.Status, reason, bytes, slot.UpdateCount, classifyWeComStreamError(err), err)
}

func (s *WeComStreamDispatcher) logDeliveryUnitEvent(event string, index int, err error) {
	if s == nil || s.ledger == nil || index < 0 || index >= len(s.ledger.units) {
		return
	}
	unit := s.ledger.units[index]
	log.Printf("wecom delivery unit event=%s traceID=%s reqID=%s chatID=%s unitID=%s method=%s kind=%s status=%s streamID=%s err=%v", event, s.traceID, s.rctx.ReqID, s.rctx.ChatID, unit.ID, unit.DeliveryMethod, unit.RenderedKind, unit.Status, unit.StreamID, err)
}

func appendDeliveredPrefix(prefix, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return strings.TrimSpace(prefix)
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return content
	}
	return prefix + content
}

func trimDeliveredPrefix(text, prefix string) (string, string) {
	text = strings.TrimSpace(text)
	prefix = strings.TrimSpace(prefix)
	if text == "" || prefix == "" {
		return text, prefix
	}
	if strings.HasPrefix(text, prefix) {
		return strings.TrimSpace(text[len(prefix):]), ""
	}
	if strings.HasPrefix(prefix, text) {
		return "", strings.TrimSpace(prefix[len(text):])
	}
	if cut, ok := loosePrefixCut(text, prefix); ok {
		return strings.TrimSpace(text[cut:]), ""
	}
	if cut, ok := loosePrefixCut(prefix, text); ok {
		return "", strings.TrimSpace(prefix[cut:])
	}
	return text, prefix
}

func loosePrefixCut(text, prefix string) (int, bool) {
	i, j := 0, 0
	for {
		for i < len(text) {
			r, size := utf8.DecodeRuneInString(text[i:])
			if !unicode.IsSpace(r) {
				break
			}
			i += size
		}
		for j < len(prefix) {
			r, size := utf8.DecodeRuneInString(prefix[j:])
			if !unicode.IsSpace(r) {
				break
			}
			j += size
		}
		if j >= len(prefix) {
			return i, true
		}
		if i >= len(text) {
			return i, false
		}
		textRune, textSize := utf8.DecodeRuneInString(text[i:])
		prefixRune, prefixSize := utf8.DecodeRuneInString(prefix[j:])
		if textRune != prefixRune {
			return 0, false
		}
		i += textSize
		j += prefixSize
	}
}

func (s *WeComStreamDispatcher) finalStreamChunks(rendered RenderedMessage) []string {
	var text strings.Builder
	for _, unit := range rendered.Units {
		if !renderedUnitCanStream(unit) {
			continue
		}
		appendRenderedStreamText(&text, unit)
	}
	if strings.TrimSpace(text.String()) == "" {
		return nil
	}
	return s.splitFinalStreamText(text.String())
}

func appendRenderedStreamText(dst *strings.Builder, unit RenderedUnit) {
	text := strings.TrimSpace(unit.Text)
	if text == "" {
		return
	}
	if dst.Len() > 0 {
		if unit.SourceType == string(MarkdownBlockList) {
			dst.WriteString("\n")
		} else {
			dst.WriteString("\n\n")
		}
	}
	dst.WriteString(text)
}

func (s *WeComStreamDispatcher) splitFinalStreamText(text string) []string {
	limit := s.policy.FinalMaxBytes
	if limit <= 0 || limit > s.policy.MaxBytes {
		limit = s.policy.MaxBytes
	}
	if limit <= 0 {
		limit = wecomStreamFinalMaxBytes
	}
	return splitWeComStreamChunks(text, limit)
}

func splitWeComStreamChunks(text string, limit int) []string {
	return splitWeComFinalStreamMarkdownChunks(text, limit)
}

func renderedUnitCanStream(unit RenderedUnit) bool {
	if unit.Action != nil {
		return false
	}
	switch unit.Kind {
	case "text", "raw":
		return true
	default:
		return false
	}
}

func appendWeComStreamFallbackNotice(content string) string {
	return appendWeComStreamNotice(content, wecomStreamFallbackNotice)
}

func appendWeComStreamNotice(content, notice string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return notice
	}
	if strings.Contains(content, notice) {
		return content
	}
	separator := "\n\n"
	if strings.HasSuffix(content, "\n\n") {
		separator = ""
	} else if strings.HasSuffix(content, "\n") {
		separator = "\n"
	}
	return content + separator + notice
}

func classifyWeComStreamError(err error) WeComStreamErrorClass {
	if err == nil {
		return WeComStreamErrorNone
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "ack timeout"):
		return WeComStreamErrorAckTimeout
	case strings.Contains(msg, "stream expired"):
		return WeComStreamErrorExpired
	case strings.Contains(msg, "not connected"), strings.Contains(msg, "connection closed"):
		return WeComStreamErrorDisconnected
	default:
		return WeComStreamErrorWriteFailed
	}
}

func renderWeComLivePreview(text, workspacePath string) string {
	return renderWeComLivePreviewWithConfig(text, workspacePath, loadWeComRuntimeConfigFromEnv())
}

func renderWeComLivePreviewWithConfig(text, workspacePath string, cfg WeComRuntimeConfig) string {
	preview, _ := renderWeComLivePreviewStreamPrefixWithConfig(text, workspacePath, cfg, "")
	return preview
}

func renderWeComLivePreviewStreamPrefixWithConfig(text, workspacePath string, cfg WeComRuntimeConfig, traceID string) (string, bool) {
	if !cfg.IRRendererEnabled {
		return stabilizeWeComMarkdownStream(text), false
	}
	parser := NewMarkdownParser()
	blocks := parser.PushFullText(text)
	if len(blocks) == 0 {
		if strings.TrimSpace(text) != "" {
			log.Printf("wecom markdown parser pendingBytes=%d renderer=ir preview=true traceID=%s", len(text), traceID)
		}
		return "", false
	}
	renderer := NewWeComMarkdownPreviewRenderer(workspacePath)
	renderer.TableMode = cfg.MarkdownTableMode
	rendered := renderer.Render(blocks)
	return renderedStreamPrefixText(rendered)
}

func renderWeComAdvancedLivePreview(fullText, workspacePath string, policy WeComStreamPolicy) string {
	visible := strings.TrimSpace(ParseSendProtocol(stripUnclosedWeComSendProtocol(fullText), workspacePath).VisibleText)
	if visible == "" {
		return ""
	}
	return strings.TrimSpace(visible)
}

func stripUnclosedWeComSendProtocol(text string) string {
	open := strings.LastIndex(text, "[LUMI_WECOM_SEND]")
	if open < 0 {
		return text
	}
	close := strings.LastIndex(text, "[/LUMI_WECOM_SEND]")
	if close > open {
		return text
	}
	return text[:open]
}

func renderWeComFinalMessage(text, workspacePath string) RenderedMessage {
	return renderWeComFinalMessageWithConfig(text, workspacePath, loadWeComRuntimeConfigFromEnv())
}

func renderWeComFinalMessageWithConfig(text, workspacePath string, cfg WeComRuntimeConfig) RenderedMessage {
	if !cfg.IRRendererEnabled {
		legacyText := normalizeWeComMarkdown(text)
		if strings.TrimSpace(legacyText) == "" {
			return RenderedMessage{}
		}
		return RenderedMessage{Units: []RenderedUnit{{Kind: "text", Text: legacyText, SourceType: "answer"}}}
	}
	parser := NewMarkdownParser()
	parser.PushFullText(text)
	renderer := NewWeComMarkdownRenderer(workspacePath)
	renderer.TableMode = cfg.MarkdownTableMode
	return renderer.Render(parser.Flush())
}

func renderWeComCoverableFinalMessageWithConfig(text, workspacePath string, cfg WeComRuntimeConfig) RenderedMessage {
	if !cfg.IRRendererEnabled {
		legacyText := normalizeWeComMarkdown(text)
		if strings.TrimSpace(legacyText) == "" {
			return RenderedMessage{}
		}
		return RenderedMessage{Units: []RenderedUnit{{Kind: "text", Text: legacyText, SourceType: "answer"}}}
	}
	parser := NewMarkdownParser()
	parser.PushFullText(text)
	renderer := NewWeComMarkdownRenderer(workspacePath)
	renderer.TableMode = cfg.MarkdownTableMode
	renderer.CoverableTextOnly = true
	return renderer.Render(parser.Flush())
}

func renderedHasAction(rendered RenderedMessage) bool {
	for _, unit := range rendered.Units {
		if unit.Action != nil {
			return true
		}
	}
	return false
}

func advancedCoverableFinalText(rendered RenderedMessage) string {
	var text strings.Builder
	for _, unit := range rendered.Units {
		if strings.TrimSpace(unit.Text) == "" {
			continue
		}
		appendRenderedStreamText(&text, unit)
	}
	return strings.TrimSpace(text.String())
}

func containsStandaloneWeComTextBlock(text, block string) bool {
	block = strings.TrimSpace(block)
	if block == "" {
		return false
	}
	normalizedBlock := normalizeWeComMarkdown(block)
	for _, part := range strings.Split(normalizeWeComMarkdown(text), "\n\n") {
		part = strings.TrimSpace(part)
		if part == normalizedBlock {
			return true
		}
		for _, line := range strings.Split(part, "\n") {
			if strings.TrimSpace(line) == normalizedBlock {
				return true
			}
		}
	}
	return false
}

func renderedStreamPrefixText(rendered RenderedMessage) (string, bool) {
	var text strings.Builder
	for _, unit := range rendered.Units {
		if !renderedUnitCanStream(unit) {
			if strings.TrimSpace(unit.Text) != "" || unit.Action != nil {
				return strings.TrimSpace(text.String()), true
			}
			continue
		}
		appendRenderedStreamText(&text, unit)
	}
	return strings.TrimSpace(text.String()), false
}

func defaultRenderedSourceType(sourceType string) string {
	if strings.TrimSpace(sourceType) == "" {
		return "answer"
	}
	return sourceType
}
