package imdebug

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/pengmide/lumi/internal/conversation"
	"github.com/pengmide/lumi/internal/storage"
)

const MaxSummaryRunes = 300

const (
	ThinkingMarker = "🤔"
	ToolMarker     = "🪄"
)

type Buffer struct {
	debug               storage.IMDebugSettings
	textBuilder         strings.Builder
	thinkingBuilder     strings.Builder
	textAccumulated     string
	thinkingAccumulated string
	segments            []Segment
	tools               map[string]map[string]any
	toolSeq             int
}

type SegmentKind string

const (
	SegmentText  SegmentKind = "text"
	SegmentDebug SegmentKind = "debug"
)

type Segment struct {
	Kind SegmentKind
	Text string
}

func NewBuffer(debug storage.IMDebugSettings) *Buffer {
	return &Buffer{debug: debug}
}

func (b *Buffer) AddMessageChunk(text string) {
	b.FlushThinking()
	delta := deltaAgainst(b.textAccumulated, text)
	if delta == "" {
		return
	}
	b.textBuilder.WriteString(delta)
	b.textAccumulated += delta
}

func (b *Buffer) AddThinkingChunk(text string) {
	b.FlushText()
	if !b.debug.Thinking || strings.TrimSpace(text) == "" {
		return
	}
	delta := deltaAgainst(b.thinkingAccumulated, text)
	if delta == "" {
		return
	}
	b.thinkingBuilder.WriteString(delta)
	b.thinkingAccumulated += delta
}

// deltaAgainst returns the portion of chunk that is new relative to the text
// already accumulated. It tolerates both delta streams (each chunk is purely
// new text) and cumulative-snapshot streams (each chunk contains the full text
// produced so far).
func deltaAgainst(accumulated, chunk string) string {
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

func (b *Buffer) AddThinkingEvent(data any) {
	b.AddThinkingChunk(ThinkingText(data))
}

// IsThinkingDone returns true if the thinking event data indicates the thinking
// phase has completed (status == "done").
func IsThinkingDone(data any) bool {
	switch v := data.(type) {
	case map[string]any:
		status, _ := v["status"].(string)
		return status == "done"
	default:
		value := reflect.ValueOf(data)
		if !value.IsValid() {
			return false
		}
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return false
			}
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			return false
		}
		field := value.FieldByName("Status")
		return field.IsValid() && field.Kind() == reflect.String && field.String() == "done"
	}
}

func (b *Buffer) AddTool(data any) {
	b.FlushThinking()
	b.FlushText()
	if !b.debug.Tools {
		return
	}
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	id := firstString(m, "toolCallId", "id")
	if id == "" {
		b.toolSeq++
		id = fmt.Sprintf("_tool_%d", b.toolSeq)
	}
	if b.tools == nil {
		b.tools = make(map[string]map[string]any)
	}
	current := b.tools[id]
	if current == nil {
		current = make(map[string]any)
		b.tools[id] = current
	}
	for key, value := range m {
		if isEmptyValue(value) {
			continue
		}
		current[key] = value
	}
	if isTerminalToolStatus(firstString(current, "status")) {
		b.flushTool(id)
	}
}

func (b *Buffer) FlushThinking() {
	text := strings.TrimSpace(b.thinkingBuilder.String())
	if text == "" {
		b.thinkingBuilder.Reset()
		b.thinkingAccumulated = ""
		return
	}
	b.segments = append(b.segments, Segment{Kind: SegmentDebug, Text: ThinkingMarker + "\n" + text})
	b.thinkingBuilder.Reset()
	b.thinkingAccumulated = ""
}

func (b *Buffer) FlushText() {
	text := strings.TrimSpace(b.textBuilder.String())
	if text == "" {
		b.textBuilder.Reset()
		b.textAccumulated = ""
		return
	}
	b.segments = append(b.segments, Segment{Kind: SegmentText, Text: text})
	b.textBuilder.Reset()
	b.textAccumulated = ""
}

func (b *Buffer) FlushTools() {
	if len(b.tools) == 0 {
		return
	}
	for id := range b.tools {
		b.flushTool(id)
	}
}

func (b *Buffer) FlushDebug() {
	b.FlushThinking()
	b.FlushTools()
}

func (b *Buffer) FlushAll() {
	b.FlushThinking()
	b.FlushTools()
	b.FlushText()
}

func (b *Buffer) Text() string {
	var parts []string
	for _, segment := range b.segments {
		if segment.Kind == SegmentText {
			parts = append(parts, segment.Text)
		}
	}
	if text := strings.TrimSpace(b.textBuilder.String()); text != "" {
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (b *Buffer) DebugMessages() []string {
	b.FlushDebug()
	var messages []string
	for _, segment := range b.segments {
		if segment.Kind == SegmentDebug {
			messages = append(messages, segment.Text)
		}
	}
	return messages
}

func (b *Buffer) PopSegments() []Segment {
	if len(b.segments) == 0 {
		return nil
	}
	segments := append([]Segment(nil), b.segments...)
	b.segments = nil
	return segments
}

func (b *Buffer) PopAllSegments() []Segment {
	b.FlushAll()
	return b.PopSegments()
}

func (b *Buffer) PopMessages() []string {
	return segmentTexts(b.PopSegments())
}

func (b *Buffer) PopAllMessages() []string {
	return segmentTexts(b.PopAllSegments())
}

func (b *Buffer) flushTool(id string) {
	tool := b.tools[id]
	if tool == nil {
		return
	}
	if line := ToolSummary(tool); line != "" {
		b.segments = append(b.segments, Segment{Kind: SegmentDebug, Text: line})
	}
	delete(b.tools, id)
}

func segmentTexts(segments []Segment) []string {
	if len(segments) == 0 {
		return nil
	}
	messages := make([]string, 0, len(segments))
	for _, segment := range segments {
		messages = append(messages, segment.Text)
	}
	return messages
}

func AppendThinking(reply *strings.Builder, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	appendBlock(reply, ThinkingMarker+"\n"+text)
}

func AppendThinkingEvent(reply *strings.Builder, data any) {
	if text := ThinkingText(data); text != "" {
		AppendThinking(reply, text)
	}
}

func ThinkingText(data any) string {
	switch v := data.(type) {
	case string:
		return v
	case map[string]any:
		text, _ := v["content"].(string)
		return text
	default:
		value := reflect.ValueOf(data)
		if !value.IsValid() {
			return ""
		}
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return ""
			}
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct {
			return ""
		}
		field := value.FieldByName("Content")
		if field.IsValid() && field.Kind() == reflect.String {
			return field.String()
		}
		return ""
	}
}

func AppendTool(reply *strings.Builder, data any) {
	if line := ToolSummary(data); line != "" {
		appendBlock(reply, line)
	}
}

func AppendToolInfo(reply *strings.Builder, tool *conversation.ToolCallInfo) {
	if line := ToolInfoSummary(tool); line != "" {
		appendBlock(reply, line)
	}
}

func ToolSummary(data any) string {
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	toolName := firstString(m, "toolName", "kind", "title")
	if toolName == "" {
		toolName = "Tool"
	}
	status := firstString(m, "status")
	if status == "" {
		status = "pending"
	}
	detail := firstString(m, "error", "output", "input", "description", "rawInput")
	return toolLine(toolName, status, detail)
}

func ToolInfoSummary(tool *conversation.ToolCallInfo) string {
	if tool == nil {
		return ""
	}
	toolName := tool.ToolName
	if toolName == "" {
		toolName = tool.Kind
	}
	if toolName == "" {
		toolName = tool.Title
	}
	if toolName == "" {
		toolName = "Tool"
	}
	status := tool.Status
	if status == "" {
		status = "pending"
	}
	detail := firstNonEmpty(tool.Error, tool.Output, tool.Input, tool.Description, tool.RawInput)
	return toolLine(toolName, status, detail)
}

func ToolDebugEnabled(store interface {
	Load(id string) (*storage.StoredSession, error)
}, conversationID string) storage.IMDebugSettings {
	if store == nil {
		return storage.IMDebugSettings{}
	}
	session, err := store.Load(conversationID)
	if err != nil || session == nil {
		return storage.IMDebugSettings{}
	}
	return session.IMDebug
}

func appendBlock(reply *strings.Builder, text string) {
	if reply.Len() > 0 {
		reply.WriteString("\n\n")
	}
	reply.WriteString(text)
}

func toolLine(toolName, status, detail string) string {
	line := fmt.Sprintf("%s %s %s", ToolMarker, toolName, status)
	if strings.TrimSpace(detail) != "" {
		line += ": " + strings.TrimSpace(detail)
	}
	return truncateRunes(line, MaxSummaryRunes)
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		value := m[key]
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v
			}
		case map[string]any:
			if len(v) > 0 {
				if data, err := json.Marshal(v); err == nil {
					return string(data)
				}
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func isTerminalToolStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "error", "failed", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func isEmptyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}
