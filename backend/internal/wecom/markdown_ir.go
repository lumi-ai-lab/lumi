package wecom

import "strings"

type MarkdownBlockType string

const (
	MarkdownBlockParagraph MarkdownBlockType = "paragraph"
	MarkdownBlockHeading   MarkdownBlockType = "heading"
	MarkdownBlockList      MarkdownBlockType = "list"
	MarkdownBlockQuote     MarkdownBlockType = "quote"
	MarkdownBlockTable     MarkdownBlockType = "table"
	MarkdownBlockCode      MarkdownBlockType = "code"
	MarkdownBlockJSON      MarkdownBlockType = "json"
	MarkdownBlockRaw       MarkdownBlockType = "raw"
)

type MarkdownBlock struct {
	Type     MarkdownBlockType
	Text     string
	Language string
}

type RenderedMessage struct {
	Units []RenderedUnit
}

type RenderedUnit struct {
	Kind       string
	Text       string
	Action     *SendAction
	SourceType string
}

func (m RenderedMessage) Text() string {
	parts := make([]string, 0, len(m.Units))
	for _, unit := range m.Units {
		if strings.TrimSpace(unit.Text) != "" {
			parts = append(parts, strings.TrimSpace(unit.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
