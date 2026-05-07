package wecom

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultWSEndpoint = "wss://openws.work.weixin.qq.com"
	wsMaxBackoff      = 30 * time.Second
	wsMaxMissedPong   = 2
	wsChunkSize       = 512 * 1024
)

var wsEndpoint = defaultWSEndpoint
var wsDialer = websocket.DefaultDialer
var processStartTime = time.Now()

type wsFrame struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers wsFrameHeaders  `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	ErrCode *int            `json:"errcode,omitempty"`
	ErrMsg  string          `json:"errmsg,omitempty"`
}

type wsFrameHeaders struct {
	ReqID string `json:"req_id"`
}

type wsMsgCallbackBody struct {
	MsgID    string `json:"msgid"`
	AibotID  string `json:"aibotid"`
	ChatID   string `json:"chatid"`
	ChatType string `json:"chattype"`
	From     struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
	Voice struct {
		Text    string `json:"text,omitempty"`
		Content string `json:"content,omitempty"`
	} `json:"voice"`
	Image *struct {
		URL    string `json:"url"`
		Aeskey string `json:"aeskey"`
	} `json:"image,omitempty"`
	File *struct {
		URL    string `json:"url"`
		Aeskey string `json:"aeskey"`
	} `json:"file,omitempty"`
	Mixed      *wsMixedBlock `json:"mixed,omitempty"`
	Quote      *wsQuoteBlock `json:"quote,omitempty"`
	CreateTime int64         `json:"create_time"`
}

type uploadMediaInitBody struct {
	Type        string `json:"type"`
	Filename    string `json:"filename"`
	TotalSize   int    `json:"total_size"`
	TotalChunks int    `json:"total_chunks"`
	MD5         string `json:"md5,omitempty"`
}

type uploadMediaInitResult struct {
	UploadID string `json:"upload_id"`
}

type uploadMediaChunkBody struct {
	UploadID   string `json:"upload_id"`
	ChunkIndex int    `json:"chunk_index"`
	Base64Data string `json:"base64_data"`
}

type uploadMediaFinishBody struct {
	UploadID string `json:"upload_id"`
}

type uploadMediaFinishResult struct {
	Type    string `json:"type"`
	MediaID string `json:"media_id"`
}

type messageDedup struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (d *messageDedup) IsDuplicate(msgID string) bool {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = make(map[string]time.Time)
	}
	now := time.Now()
	for k, t := range d.seen {
		if now.Sub(t) > 2*time.Minute {
			delete(d.seen, k)
		}
	}
	if _, exists := d.seen[msgID]; exists {
		return true
	}
	d.seen[msgID] = now
	return false
}

type wsRuntime struct {
	service *Service
	cfg     Config

	conn         *websocket.Conn
	writeMu      sync.Mutex
	connMu       sync.RWMutex
	processedMu  sync.Mutex
	processedSet map[string]struct{}
	processedIDs []string

	reqSeq      atomic.Int64
	missedPong  atomic.Int32
	pendingAcks sync.Map
	dedup       messageDedup
}

func newWebSocketRuntime(service *Service, cfg Config) *wsRuntime {
	rt := &wsRuntime{
		service:      service,
		cfg:          cfg,
		processedSet: make(map[string]struct{}),
	}
	if state, err := service.runtimeStore.Load(); err == nil {
		for _, id := range state.ProcessedMessageIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			rt.processedIDs = append(rt.processedIDs, id)
			rt.processedSet[id] = struct{}{}
		}
	}
	return rt
}

func (rt *wsRuntime) TestConnection(ctx context.Context) error {
	conn, err := rt.dialAndSubscribe(ctx)
	if err != nil {
		return err
	}
	return conn.Close()
}

func (s *Service) runWebSocketLoop(ctx context.Context, cfg Config, done chan struct{}) {
	defer close(done)
	defer func() {
		_ = s.updateRuntime(func(state *RuntimeState) {
			state.Running = false
		})
	}()

	rt := newWebSocketRuntime(s, cfg)
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			_ = rt.close()
			return
		default:
		}

		start := time.Now()
		err := rt.runConnection(ctx)
		if ctx.Err() != nil {
			return
		}
		_ = s.updateRuntime(func(state *RuntimeState) {
			state.LastError = err.Error()
			state.Running = true
		})

		if time.Since(start) > 2*time.Duration(cfg.HeartbeatIntervalMs)*time.Millisecond {
			backoff = time.Second
		}
		if err := sleepContext(ctx, backoff); err != nil {
			return
		}
		backoff *= 2
		if backoff > wsMaxBackoff {
			backoff = wsMaxBackoff
		}
	}
}

func (rt *wsRuntime) runConnection(ctx context.Context) error {
	conn, err := rt.dialAndSubscribe(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rt.closePendingAcks(fmt.Errorf("wecom-ws: connection closed"))
		_ = conn.Close()
		rt.connMu.Lock()
		if rt.conn == conn {
			rt.conn = nil
		}
		rt.connMu.Unlock()
	}()

	rt.connMu.Lock()
	rt.conn = conn
	rt.connMu.Unlock()
	rt.missedPong.Store(0)
	_ = rt.service.updateRuntime(func(state *RuntimeState) {
		state.LastError = ""
		state.LastConnectedAt = time.Now().UnixMilli()
	})

	heartCtx, heartCancel := context.WithCancel(ctx)
	defer heartCancel()
	go rt.heartbeat(heartCtx, conn)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("wecom-ws: read: %w", err)
		}

		var frame wsFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			continue
		}
		rt.handleFrame(ctx, frame)
	}
}

func (rt *wsRuntime) dialAndSubscribe(ctx context.Context) (*websocket.Conn, error) {
	connectTimeout := time.Duration(rt.cfg.ConnectTimeoutMs) * time.Millisecond
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, _, err := wsDialer.DialContext(dialCtx, wsEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("wecom-ws: dial: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.Close()
		}
	}()

	subReqID := rt.generateReqID("aibot_subscribe")
	subFrame := map[string]any{
		"cmd":     "aibot_subscribe",
		"headers": map[string]string{"req_id": subReqID},
		"body": map[string]string{
			"bot_id": rt.cfg.BotID,
			"secret": rt.cfg.BotSecret,
		},
	}
	if err := conn.SetWriteDeadline(time.Now().Add(connectTimeout)); err != nil {
		return nil, fmt.Errorf("wecom-ws: subscribe write deadline: %w", err)
	}
	if err := conn.WriteJSON(subFrame); err != nil {
		return nil, fmt.Errorf("wecom-ws: subscribe: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})

	var subResp wsFrame
	if err := conn.SetReadDeadline(time.Now().Add(connectTimeout)); err != nil {
		return nil, fmt.Errorf("wecom-ws: subscribe read deadline: %w", err)
	}
	if err := conn.ReadJSON(&subResp); err != nil {
		return nil, fmt.Errorf("wecom-ws: subscribe response: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if subResp.ErrCode == nil || *subResp.ErrCode != 0 {
		errCode := 0
		if subResp.ErrCode != nil {
			errCode = *subResp.ErrCode
		}
		return nil, fmt.Errorf("wecom-ws: subscribe failed: errcode=%d errmsg=%s", errCode, subResp.ErrMsg)
	}
	closeOnError = false
	return conn, nil
}

func (rt *wsRuntime) handleFrame(ctx context.Context, frame wsFrame) {
	switch frame.Cmd {
	case "aibot_msg_callback":
		rt.handleMsgCallback(ctx, frame)
	case "":
		reqID := frame.Headers.ReqID
		switch {
		case strings.HasPrefix(reqID, "ping"):
			rt.missedPong.Store(0)
		default:
			var ackErr error
			if frame.ErrCode != nil && *frame.ErrCode != 0 {
				ackErr = fmt.Errorf("wecom-ws: ack error: errcode=%d errmsg=%s", *frame.ErrCode, frame.ErrMsg)
			}
			if ch, ok := rt.pendingAcks.LoadAndDelete(reqID); ok {
				waiter := ch.(ackWaiter)
				if waiter.respCh != nil {
					waiter.respCh <- &frame
				}
				waiter.errCh <- ackErr
			}
		}
	}
}

func (rt *wsRuntime) heartbeat(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(time.Duration(rt.cfg.HeartbeatIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if int(rt.missedPong.Load()) >= wsMaxMissedPong {
				_ = conn.Close()
				return
			}
			rt.missedPong.Add(1)
			pingFrame := map[string]any{
				"cmd":     "ping",
				"headers": map[string]string{"req_id": rt.generateReqID("ping")},
			}
			if err := rt.writeJSON(pingFrame); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func (rt *wsRuntime) handleMsgCallback(ctx context.Context, frame wsFrame) {
	var body wsMsgCallbackBody
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		return
	}
	if rt.dedup.IsDuplicate(body.MsgID) {
		return
	}
	if rt.hasProcessed(body.MsgID) {
		return
	}
	if body.CreateTime > 0 && isOldMessage(time.Unix(body.CreateTime, 0)) {
		return
	}

	userID := strings.TrimSpace(body.From.UserID)
	if !allowUser(rt.cfg.AllowFrom, userID) {
		return
	}

	chatID := strings.TrimSpace(body.ChatID)
	if chatID == "" {
		chatID = userID
	}
	sessionKey := fmt.Sprintf("wecom:%s:%s", chatID, userID)
	rctx := replyContext{
		ReqID:    frame.Headers.ReqID,
		ChatID:   chatID,
		ChatType: body.ChatType,
		UserID:   userID,
	}

	texts, imgRefs, fileRefs := wsCollectInboundParts(&body)
	if body.MsgType == "voice" {
		vt := stripWeComAtMentions(wsVoiceText(body.Voice), rt.cfg.BotID, body.AibotID)
		if vt == "" && len(imgRefs) == 0 && len(fileRefs) == 0 {
			return
		}
		if vt != "" {
			texts = append([]string{vt}, texts...)
		}
	}

	go func() {
		msg := WeComInboundMessage{
			ConversationKey: sessionKey,
			MessageID:       body.MsgID,
			ChatID:          chatID,
			UserID:          userID,
			Text:            stripWeComAtMentions(strings.Join(texts, "\n"), rt.cfg.BotID, body.AibotID),
			ReplyContext:    rctx,
			ReceivedAt:      time.Now().UnixMilli(),
		}
		if len(imgRefs) > 0 || len(fileRefs) > 0 {
			attachments := rt.downloadAttachments(context.Background(), imgRefs, fileRefs)
			msg.Attachments = attachments
		}
		handleErr := rt.service.handleInboundMessage(ctx, rt.cfg, msg, rt)
		rt.markProcessed(body.MsgID)
		_ = rt.service.updateRuntime(func(state *RuntimeState) {
			state.LastMessageAt = time.Now().UnixMilli()
			state.ProcessedMessageIDs = append(state.ProcessedMessageIDs, body.MsgID)
			if handleErr != nil {
				state.LastError = handleErr.Error()
			} else {
				state.LastError = ""
			}
		})
	}()
}

func (rt *wsRuntime) downloadAttachments(parent context.Context, imgRefs, fileRefs []wsMediaRef) []WeComAttachment {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()

	attachments := make([]WeComAttachment, 0, len(imgRefs)+len(fileRefs))
	for _, im := range imgRefs {
		buf, fn, err := downloadWeComWSMedia(ctx, im.URL, im.Aeskey)
		if err != nil {
			continue
		}
		base := filepath.Base(strings.TrimSpace(fn))
		if base == "" || base == "." {
			base = "image.bin"
		}
		mt := wecomInboundFileMime(base, buf)
		attachments = append(attachments, WeComAttachment{
			Kind:     "image",
			Name:     base,
			Size:     int64(len(buf)),
			Data:     buf,
			MimeType: mt,
		})
	}
	for _, f := range fileRefs {
		buf, fn, err := downloadWeComWSMedia(ctx, f.URL, f.Aeskey)
		if err != nil {
			continue
		}
		base := filepath.Base(strings.TrimSpace(fn))
		if base == "" || base == "." {
			base = "attachment"
		}
		mt := wecomInboundFileMime(base, buf)
		attachments = append(attachments, WeComAttachment{
			Kind:     "file",
			Name:     base,
			Size:     int64(len(buf)),
			Data:     buf,
			MimeType: mt,
		})
	}
	return attachments
}

func (rt *wsRuntime) Reply(ctx context.Context, rctx replyContext, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if rctx.ReqID == "" {
		return rt.Send(ctx, rctx, content)
	}
	streamID := rt.generateReqID("stream")
	frame := map[string]any{
		"cmd":     "aibot_respond_msg",
		"headers": map[string]string{"req_id": rctx.ReqID},
		"body": map[string]any{
			"msgtype": "stream",
			"stream": map[string]any{
				"id":      streamID,
				"finish":  true,
				"content": content,
			},
		},
	}
	return rt.writeJSON(frame)
}

func (rt *wsRuntime) Send(ctx context.Context, rctx replyContext, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if rctx.ChatID == "" {
		return fmt.Errorf("wecom-ws: chatID is empty")
	}
	chunks := splitByBytes(content, 2000)
	for _, chunk := range chunks {
		reqID := rt.generateReqID("aibot_send_msg")
		frame := map[string]any{
			"cmd":     "aibot_send_msg",
			"headers": map[string]string{"req_id": reqID},
			"body": map[string]any{
				"chatid":  rctx.ChatID,
				"msgtype": "markdown",
				"markdown": map[string]string{
					"content": chunk,
				},
			},
		}
		if err := rt.writeAndWaitAck(ctx, frame, reqID); err != nil {
			return err
		}
	}
	return nil
}

func (rt *wsRuntime) ReplyMedia(ctx context.Context, rctx replyContext, action SendAction) error {
	if rctx.ReqID == "" {
		return rt.SendMedia(ctx, rctx, action)
	}
	mediaID, err := rt.uploadMedia(ctx, action)
	if err != nil {
		return err
	}
	frame := map[string]any{
		"cmd":     "aibot_respond_msg",
		"headers": map[string]string{"req_id": rctx.ReqID},
		"body":    buildMediaMsgBody(action.Type, mediaID),
	}
	return rt.writeJSON(frame)
}

func (rt *wsRuntime) SendMedia(ctx context.Context, rctx replyContext, action SendAction) error {
	if rctx.ChatID == "" {
		return fmt.Errorf("wecom-ws: chatID is empty")
	}
	mediaID, err := rt.uploadMedia(ctx, action)
	if err != nil {
		return err
	}
	reqID := rt.generateReqID("aibot_send_msg")
	frame := map[string]any{
		"cmd":     "aibot_send_msg",
		"headers": map[string]string{"req_id": reqID},
		"body":    withChatID(rctx.ChatID, buildMediaMsgBody(action.Type, mediaID)),
	}
	return rt.writeAndWaitAck(ctx, frame, reqID)
}

func withChatID(chatID string, body map[string]any) map[string]any {
	out := make(map[string]any, len(body)+1)
	out["chatid"] = chatID
	for k, v := range body {
		out[k] = v
	}
	return out
}

func buildMediaMsgBody(kind, mediaID string) map[string]any {
	body := map[string]any{"msgtype": kind}
	switch kind {
	case "image":
		body["image"] = map[string]string{"media_id": mediaID}
	default:
		body["file"] = map[string]string{"media_id": mediaID}
	}
	return body
}

func (rt *wsRuntime) uploadMedia(ctx context.Context, action SendAction) (string, error) {
	data, err := os.ReadFile(action.ResolvedPath)
	if err != nil {
		return "", err
	}
	if len(data) <= 5 {
		return "", fmt.Errorf("file must be larger than 5 bytes")
	}
	totalChunks := (len(data) + wsChunkSize - 1) / wsChunkSize
	if totalChunks > 100 {
		return "", fmt.Errorf("file too large: exceeds 100 chunks")
	}
	initReqID := rt.generateReqID("aibot_upload_media_init")
	initFrame := map[string]any{
		"cmd":     "aibot_upload_media_init",
		"headers": map[string]string{"req_id": initReqID},
		"body": uploadMediaInitBody{
			Type:        action.Type,
			Filename:    filepath.Base(action.FileName),
			TotalSize:   len(data),
			TotalChunks: totalChunks,
			MD5:         fmt.Sprintf("%x", md5.Sum(data)),
		},
	}
	initResp, err := rt.writeAndReadAck(ctx, initFrame, initReqID)
	if err != nil {
		return "", err
	}
	var initResult uploadMediaInitResult
	if err := json.Unmarshal(initResp.Body, &initResult); err != nil {
		return "", err
	}
	if initResult.UploadID == "" {
		return "", fmt.Errorf("wecom-ws: upload init missing upload_id")
	}

	for i := 0; i < totalChunks; i++ {
		start := i * wsChunkSize
		end := start + wsChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunkReqID := rt.generateReqID("aibot_upload_media_chunk")
		chunkFrame := map[string]any{
			"cmd":     "aibot_upload_media_chunk",
			"headers": map[string]string{"req_id": chunkReqID},
			"body": uploadMediaChunkBody{
				UploadID:   initResult.UploadID,
				ChunkIndex: i,
				Base64Data: base64.StdEncoding.EncodeToString(data[start:end]),
			},
		}
		if _, err := rt.writeAndReadAck(ctx, chunkFrame, chunkReqID); err != nil {
			return "", err
		}
	}

	finishReqID := rt.generateReqID("aibot_upload_media_finish")
	finishFrame := map[string]any{
		"cmd":     "aibot_upload_media_finish",
		"headers": map[string]string{"req_id": finishReqID},
		"body": uploadMediaFinishBody{
			UploadID: initResult.UploadID,
		},
	}
	finishResp, err := rt.writeAndReadAck(ctx, finishFrame, finishReqID)
	if err != nil {
		return "", err
	}
	var finishResult uploadMediaFinishResult
	if err := json.Unmarshal(finishResp.Body, &finishResult); err != nil {
		return "", err
	}
	if finishResult.MediaID == "" {
		return "", fmt.Errorf("wecom-ws: upload finish missing media_id")
	}
	return finishResult.MediaID, nil
}

func (rt *wsRuntime) writeAndReadAck(ctx context.Context, frame map[string]any, reqID string) (*wsFrame, error) {
	ch := make(chan error, 1)
	respCh := make(chan *wsFrame, 1)
	rt.pendingAcks.Store(reqID, ackWaiter{errCh: ch, respCh: respCh})
	if err := rt.writeJSON(frame); err != nil {
		rt.pendingAcks.Delete(reqID)
		return nil, err
	}

	timeout := time.Duration(rt.cfg.MessageAckTimeoutMs) * time.Millisecond
	select {
	case err := <-ch:
		if err != nil {
			return nil, err
		}
		select {
		case resp := <-respCh:
			return resp, nil
		default:
			return &wsFrame{}, nil
		}
	case <-ctx.Done():
		rt.pendingAcks.Delete(reqID)
		return nil, ctx.Err()
	case <-time.After(timeout):
		rt.pendingAcks.Delete(reqID)
		return nil, fmt.Errorf("wecom-ws: ack timeout")
	}
}

type ackWaiter struct {
	errCh  chan error
	respCh chan *wsFrame
}

func (rt *wsRuntime) writeJSON(v any) error {
	rt.writeMu.Lock()
	defer rt.writeMu.Unlock()
	rt.connMu.RLock()
	conn := rt.conn
	rt.connMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("wecom-ws: not connected")
	}
	return conn.WriteJSON(v)
}

func (rt *wsRuntime) writeAndWaitAck(ctx context.Context, frame map[string]any, reqID string) error {
	ch := make(chan error, 1)
	rt.pendingAcks.Store(reqID, ackWaiter{errCh: ch, respCh: nil})
	if err := rt.writeJSON(frame); err != nil {
		rt.pendingAcks.Delete(reqID)
		return err
	}

	timeout := time.Duration(rt.cfg.MessageAckTimeoutMs) * time.Millisecond
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		rt.pendingAcks.Delete(reqID)
		return ctx.Err()
	case <-time.After(timeout):
		rt.pendingAcks.Delete(reqID)
		return nil
	}
}

func (rt *wsRuntime) generateReqID(prefix string) string {
	seq := rt.reqSeq.Add(1)
	return fmt.Sprintf("%s_%d", prefix, seq)
}

func (rt *wsRuntime) closePendingAcks(err error) {
	var staleKeys []any
	rt.pendingAcks.Range(func(key, value any) bool {
		waiter := value.(ackWaiter)
		select {
		case waiter.errCh <- err:
		default:
		}
		staleKeys = append(staleKeys, key)
		return true
	})
	for _, k := range staleKeys {
		rt.pendingAcks.Delete(k)
	}
}

func (rt *wsRuntime) close() error {
	rt.connMu.Lock()
	conn := rt.conn
	rt.conn = nil
	rt.connMu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func allowUser(allowFrom, userID string) bool {
	allowFrom = strings.TrimSpace(allowFrom)
	if allowFrom == "" || allowFrom == "*" {
		return true
	}
	for _, part := range strings.Split(allowFrom, ",") {
		if strings.TrimSpace(part) == userID {
			return true
		}
	}
	return false
}

func (rt *wsRuntime) hasProcessed(msgID string) bool {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return false
	}
	rt.processedMu.Lock()
	defer rt.processedMu.Unlock()
	_, ok := rt.processedSet[msgID]
	return ok
}

func (rt *wsRuntime) markProcessed(msgID string) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return
	}
	rt.processedMu.Lock()
	defer rt.processedMu.Unlock()
	if _, exists := rt.processedSet[msgID]; exists {
		return
	}
	rt.processedSet[msgID] = struct{}{}
	rt.processedIDs = append(rt.processedIDs, msgID)
	if len(rt.processedIDs) > maxProcessedMessageIDs {
		evict := rt.processedIDs[0]
		rt.processedIDs = rt.processedIDs[1:]
		delete(rt.processedSet, evict)
	}
}

func isOldMessage(msgTime time.Time) bool {
	return msgTime.Before(processStartTime.Add(-2 * time.Second))
}

func wsVoiceText(v struct {
	Text    string `json:"text,omitempty"`
	Content string `json:"content,omitempty"`
}) string {
	if s := strings.TrimSpace(v.Content); s != "" {
		return s
	}
	return strings.TrimSpace(v.Text)
}

func splitByBytes(s string, maxBytes int) []string {
	if maxBytes <= 0 {
		return []string{s}
	}
	if len(s) == 0 {
		return []string{""}
	}
	if len(s) <= maxBytes {
		return []string{s}
	}
	out := make([]string, 0, len(s)/maxBytes+1)
	for start := 0; start < len(s); {
		end := start + maxBytes
		if end >= len(s) {
			out = append(out, s[start:])
			break
		}
		for end > start && (s[end]&0xC0) == 0x80 {
			end--
		}
		if end == start {
			end = start + maxBytes
		}
		out = append(out, s[start:end])
		start = end
	}
	return out
}
