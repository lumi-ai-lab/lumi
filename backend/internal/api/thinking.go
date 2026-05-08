package api

import (
	"regexp"
	"strings"
	"time"
)

var thinkBlockRegex = regexp.MustCompile(`(?is)<\s*(think|thinking)\s*>(.*?)<\s*/\s*(think|thinking)\s*>`)
var thinkTagRegex = regexp.MustCompile(`(?is)<\s*/?\s*(think|thinking)\s*>`)
var orphanThinkClosePrefixRegex = regexp.MustCompile(`(?is)^[\s\S]*?<\s*/\s*think(?:ing)?\s*>`)
var excessiveNewlineRegex = regexp.MustCompile(`\n{3,}`)

type thinkingData struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Duration int64  `json:"duration,omitempty"`
}

type streamAccumulator struct {
	currentText       string
	currentThinking   string
	thinkingStartedAt int64
}

func (a *streamAccumulator) AddMessageChunk(text string, streamItems *[]streamItem) (visibleText string, thinking []streamItem) {
	segments := splitThinkSegments(text)
	for _, segment := range segments {
		if segment.thinking {
			a.flushText(streamItems)
			if segment.text != "" {
				a.ensureThinkingStarted()
				a.currentThinking += segment.text
			}
			continue
		}

		if segment.text != "" {
			if item, ok := a.flushThinking(); ok {
				*streamItems = append(*streamItems, item)
				thinking = append(thinking, item)
			}
			a.currentText += segment.text
			visibleText += segment.text
		}
	}
	return visibleText, thinking
}

func (a *streamAccumulator) AddThinkingChunk(text string) streamItem {
	if text != "" {
		a.ensureThinkingStarted()
		a.currentThinking += stripThinkTags(text)
	}
	return streamItem{Type: "thinking", Thinking: &thinkingData{Content: a.currentThinking, Status: "thinking"}}
}

func (a *streamAccumulator) FlushText(streamItems *[]streamItem) {
	a.flushText(streamItems)
}

func (a *streamAccumulator) FlushThinking(streamItems *[]streamItem) {
	if item, ok := a.flushThinking(); ok {
		*streamItems = append(*streamItems, item)
	}
}

func (a *streamAccumulator) Finish(streamItems *[]streamItem) {
	a.flushText(streamItems)
	a.FlushThinking(streamItems)
}

func (a *streamAccumulator) Text() string {
	return a.currentText
}

func (a *streamAccumulator) SetText(text string) {
	a.currentText = text
}

func (a *streamAccumulator) flushText(streamItems *[]streamItem) {
	if a.currentText == "" {
		return
	}
	*streamItems = append(*streamItems, streamItem{Type: "text", Text: a.currentText})
	a.currentText = ""
}

func (a *streamAccumulator) flushThinking() (streamItem, bool) {
	if a.currentThinking == "" {
		return streamItem{}, false
	}
	duration := int64(0)
	if a.thinkingStartedAt > 0 {
		duration = time.Now().UnixMilli() - a.thinkingStartedAt
	}
	item := streamItem{
		Type:     "thinking",
		Thinking: &thinkingData{Content: a.currentThinking, Status: "done", Duration: duration},
	}
	a.currentThinking = ""
	a.thinkingStartedAt = 0
	return item, true
}

func (a *streamAccumulator) ensureThinkingStarted() {
	if a.thinkingStartedAt == 0 {
		a.thinkingStartedAt = time.Now().UnixMilli()
	}
}

type thinkSegment struct {
	text     string
	thinking bool
}

func splitThinkSegments(text string) []thinkSegment {
	if text == "" {
		return nil
	}

	matches := thinkBlockRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		cleaned := stripThinkTags(text)
		if cleaned == "" {
			return nil
		}
		return []thinkSegment{{text: cleaned}}
	}

	segments := make([]thinkSegment, 0, len(matches)*2+1)
	pos := 0
	for _, match := range matches {
		if match[0] > pos {
			if cleaned := stripThinkTags(text[pos:match[0]]); cleaned != "" {
				segments = append(segments, thinkSegment{text: cleaned})
			}
		}
		if match[4] >= 0 && match[5] >= 0 {
			segments = append(segments, thinkSegment{text: text[match[4]:match[5]], thinking: true})
		}
		pos = match[1]
	}
	if pos < len(text) {
		if cleaned := stripThinkTags(text[pos:]); cleaned != "" {
			segments = append(segments, thinkSegment{text: cleaned})
		}
	}
	return segments
}

func stripThinkTags(text string) string {
	if text == "" {
		return ""
	}
	withoutBlocks := thinkBlockRegex.ReplaceAllString(text, "")
	withoutOrphanPrefix := orphanThinkClosePrefixRegex.ReplaceAllString(withoutBlocks, "")
	cleaned := thinkTagRegex.ReplaceAllString(withoutOrphanPrefix, "")
	cleaned = excessiveNewlineRegex.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimLeft(cleaned, "\r\n")
}
