package imdebug

import (
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/storage"
)

type thinkingPayload struct {
	Content string
	Status  string
}

func TestAppendThinkingEventSupportsMapAndStructPointer(t *testing.T) {
	var fromMap strings.Builder
	AppendThinkingEvent(&fromMap, map[string]any{"content": "map thought"})
	if got := fromMap.String(); got != "🤔\nmap thought" {
		t.Fatalf("map thinking = %q", got)
	}

	var fromStruct strings.Builder
	AppendThinkingEvent(&fromStruct, &thinkingPayload{Content: "struct thought", Status: "done"})
	if got := fromStruct.String(); got != "🤔\nstruct thought" {
		t.Fatalf("struct thinking = %q", got)
	}
}

func TestToolSummaryTruncatesLongDetails(t *testing.T) {
	got := ToolSummary(map[string]any{
		"toolName": "Bash",
		"status":   "completed",
		"output":   strings.Repeat("x", 400),
	})
	if !strings.HasPrefix(got, "🪄 Bash completed: ") {
		t.Fatalf("ToolSummary() = %q", got)
	}
	if len([]rune(got)) != MaxSummaryRunes {
		t.Fatalf("ToolSummary length = %d, want %d", len([]rune(got)), MaxSummaryRunes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("ToolSummary() = %q, want ellipsis", got)
	}
}

func TestBufferBatchesThinkingChunksUntilBoundary(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Thinking: true})
	buf.AddThinkingChunk("first ")
	buf.AddThinkingChunk("second")
	if segments := buf.PopSegments(); len(segments) != 0 {
		t.Fatalf("PopSegments() before boundary = %v", segments)
	}
	buf.AddMessageChunk("reply")
	segments := buf.PopSegments()
	if len(segments) != 1 || segments[0].Kind != SegmentDebug || segments[0].Text != "🤔\nfirst second" {
		t.Fatalf("PopSegments() = %v", segments)
	}
	if got := buf.Text(); got != "reply" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestBufferBatchesMessageChunksUntilBoundary(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Thinking: true})
	buf.AddMessageChunk("hello ")
	buf.AddMessageChunk("world")
	if segments := buf.PopSegments(); len(segments) != 0 {
		t.Fatalf("PopSegments() before boundary = %v", segments)
	}
	buf.AddThinkingChunk("next thought")
	segments := buf.PopSegments()
	if len(segments) != 1 || segments[0].Kind != SegmentText || segments[0].Text != "hello world" {
		t.Fatalf("PopSegments() = %v", segments)
	}
	buf.PopAllSegments()
}

func TestBufferPreservesThoughtMessageOrder(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Thinking: true})
	buf.AddThinkingChunk("first ")
	buf.AddThinkingChunk("thought")
	buf.AddMessageChunk("reply ")
	buf.AddMessageChunk("text")
	segments := buf.PopAllSegments()
	if len(segments) != 2 {
		t.Fatalf("PopAllSegments() = %v, want thinking and text", segments)
	}
	if segments[0].Kind != SegmentDebug || segments[0].Text != "🤔\nfirst thought" {
		t.Fatalf("thinking segment = %+v", segments[0])
	}
	if segments[1].Kind != SegmentText || segments[1].Text != "reply text" {
		t.Fatalf("text segment = %+v", segments[1])
	}
}

func TestBufferMergesToolUpdatesAndFlushesOnCompleted(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Tools: true})
	buf.AddTool(map[string]any{
		"toolCallId": "tool-1",
		"toolName":   "Bash",
		"status":     "pending",
		"input":      "go test ./...",
	})
	if segments := buf.PopSegments(); len(segments) != 0 {
		t.Fatalf("PopSegments() flushed pending tool early = %v", segments)
	}
	if messages := buf.PopAllMessages(); len(messages) != 1 {
		t.Fatalf("PopAllMessages() flushed pending tool at turn end = %v", messages)
	}

	buf = NewBuffer(storage.IMDebugSettings{Tools: true})
	buf.AddTool(map[string]any{
		"toolCallId": "tool-1",
		"toolName":   "Bash",
		"status":     "pending",
		"input":      "go test ./...",
	})
	buf.AddTool(map[string]any{
		"toolCallId": "tool-1",
		"status":     "completed",
		"output":     "ok",
	})
	messages := buf.DebugMessages()
	if len(messages) != 1 || messages[0] != "🪄 Bash completed: ok" {
		t.Fatalf("DebugMessages() = %v", messages)
	}
}

func TestBufferDeduplicatesCumulativeMessageChunks(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Thinking: true})
	buf.AddMessageChunk("The")
	buf.AddMessageChunk("The user")
	buf.AddMessageChunk("The user said \"hi\"")
	buf.AddThinkingChunk("flush boundary")
	segments := buf.PopSegments()
	if len(segments) != 1 || segments[0].Kind != SegmentText || segments[0].Text != "The user said \"hi\"" {
		t.Fatalf("PopSegments() = %v", segments)
	}
	buf.PopAllSegments()
}

func TestBufferDeduplicatesCumulativeThinkingChunks(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Thinking: true})
	buf.AddThinkingChunk("The")
	buf.AddThinkingChunk("The user")
	buf.AddThinkingChunk("The user said \"hi\"")
	buf.AddMessageChunk("reply")
	segments := buf.PopSegments()
	if len(segments) != 1 || segments[0].Kind != SegmentDebug || segments[0].Text != "🤔\nThe user said \"hi\"" {
		t.Fatalf("PopSegments() = %v", segments)
	}
	buf.PopAllSegments()
}

func TestBufferIgnoresExactRepeatMessageChunk(t *testing.T) {
	buf := NewBuffer(storage.IMDebugSettings{Thinking: true})
	buf.AddMessageChunk("hi")
	buf.AddMessageChunk("hi")
	buf.AddThinkingChunk("boundary")
	segments := buf.PopSegments()
	if len(segments) != 1 || segments[0].Kind != SegmentText || segments[0].Text != "hi" {
		t.Fatalf("PopSegments() = %v", segments)
	}
	buf.PopAllSegments()
}
