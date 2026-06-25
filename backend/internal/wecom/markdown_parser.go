package wecom

import (
	"encoding/json"
	"strings"
)

type MarkdownParser struct {
	text string
}

func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{}
}

func (p *MarkdownParser) PushFullText(text string) []MarkdownBlock {
	if p == nil {
		return nil
	}
	p.text = normalizeWeComMarkdownStreamInput(text)
	return parseMarkdownBlocks(p.text, false)
}

func (p *MarkdownParser) Flush() []MarkdownBlock {
	if p == nil {
		return nil
	}
	return parseMarkdownBlocks(p.text, true)
}

func parseMarkdownBlocks(text string, final bool) []MarkdownBlock {
	lines := splitWeComMarkdownLines(text)
	blocks := make([]MarkdownBlock, 0, len(lines))
	for i := 0; i < len(lines); {
		for i < len(lines) && strings.TrimSpace(lines[i].text) == "" {
			i++
		}
		if i >= len(lines) {
			break
		}
		start := i
		if marker := wecomMarkdownFenceRE.FindStringSubmatch(lines[i].text); len(marker) == 2 {
			close := findClosingWeComMarkdownFence(lines, i+1, marker[1])
			if close < 0 {
				if !final {
					break
				}
				blocks = append(blocks, MarkdownBlock{
					Type:     MarkdownBlockRaw,
					Text:     joinMarkdownIRLines(lines[start:]),
					Language: markdownFenceLanguage(lines[i].text),
				})
				break
			}
			blockType := MarkdownBlockCode
			if strings.EqualFold(markdownFenceLanguage(lines[i].text), "json") {
				blockType = MarkdownBlockJSON
			}
			blocks = append(blocks, MarkdownBlock{
				Type:     blockType,
				Text:     joinMarkdownIRLines(lines[start : close+1]),
				Language: markdownFenceLanguage(lines[i].text),
			})
			i = close + 1
			continue
		}
		if wecomMarkdownHeadingRE.MatchString(lines[i].text) {
			if !final && !lines[i].hasNL {
				break
			}
			blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockHeading, Text: joinMarkdownIRLines(lines[start : start+1])})
			i++
			continue
		}
		if isWeComMarkdownTableStart(lines, i) {
			i += 2
			for i < len(lines) && isWeComMarkdownTableLine(lines[i].text) {
				if !final && !lines[i].hasNL {
					break
				}
				i++
			}
			if !final && i < len(lines) && isPotentialIncompleteWeComMarkdownTableLine(lines[i].text) {
				break
			}
			blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockTable, Text: joinMarkdownIRLines(lines[start:i])})
			continue
		}
		if isPotentialIncompleteWeComMarkdownTableHeader(lines, i, final) {
			break
		}
		if isWeComMarkdownListLine(lines[i].text) {
			i++
			for i < len(lines) && strings.TrimSpace(lines[i].text) != "" && !startsNewMarkdownIRBlock(lines, i) {
				i++
			}
			if !final && markdownIRHasIncompleteTrailingLine(lines[start:i]) {
				break
			}
			blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockList, Text: joinMarkdownIRLines(lines[start:i])})
			continue
		}
		if isWeComMarkdownQuoteLine(lines[i].text) {
			i++
			for i < len(lines) && strings.TrimSpace(lines[i].text) != "" && isWeComMarkdownQuoteLine(lines[i].text) {
				i++
			}
			if !final && markdownIRHasIncompleteTrailingLine(lines[start:i]) {
				break
			}
			blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockQuote, Text: joinMarkdownIRLines(lines[start:i])})
			continue
		}
		if looksLikeBareJSONStart(lines[i].text) {
			i++
			for i < len(lines) && strings.TrimSpace(lines[i].text) != "" && !startsNewMarkdownIRBlock(lines, i) {
				i++
				if json.Valid([]byte(joinMarkdownIRLines(lines[start:i]))) {
					break
				}
			}
			text := joinMarkdownIRLines(lines[start:i])
			if json.Valid([]byte(text)) {
				blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockJSON, Text: text, Language: "json"})
				continue
			}
			if !final {
				break
			}
			blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockRaw, Text: text})
			continue
		}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i].text) != "" {
			if startsNewMarkdownIRBlock(lines, i) || isPotentialIncompleteWeComMarkdownTableHeader(lines, i, final) {
				break
			}
			i++
		}
		blocks = append(blocks, MarkdownBlock{Type: MarkdownBlockParagraph, Text: joinMarkdownIRLines(lines[start:i])})
	}
	return blocks
}

func startsNewMarkdownIRBlock(lines []wecomMarkdownLine, i int) bool {
	return wecomMarkdownFenceRE.MatchString(lines[i].text) ||
		wecomMarkdownHeadingRE.MatchString(lines[i].text) ||
		isWeComMarkdownTableStart(lines, i) ||
		isWeComMarkdownListLine(lines[i].text) ||
		isWeComMarkdownQuoteLine(lines[i].text) ||
		looksLikeBareJSONStart(lines[i].text)
}

func isPotentialIncompleteWeComMarkdownTableHeader(lines []wecomMarkdownLine, i int, final bool) bool {
	if final || i >= len(lines) || !isWeComMarkdownTableLine(lines[i].text) {
		return false
	}
	trimmed := strings.TrimSpace(lines[i].text)
	if !strings.HasPrefix(trimmed, "|") && !strings.HasSuffix(trimmed, "|") {
		return false
	}
	if i+1 >= len(lines) {
		return true
	}
	return strings.TrimSpace(lines[i+1].text) == "" && !lines[i+1].hasNL
}

func joinMarkdownIRLines(lines []wecomMarkdownLine) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, line.text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func markdownFenceLanguage(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return ""
	}
	return strings.TrimSpace(trimmed[3:])
}

func markdownIRHasIncompleteTrailingLine(lines []wecomMarkdownLine) bool {
	if len(lines) == 0 {
		return false
	}
	last := lines[len(lines)-1]
	if !last.hasNL {
		return true
	}
	trimmed := strings.TrimSpace(last.text)
	return trimmed == "-" || trimmed == "*" || trimmed == "+" ||
		strings.HasSuffix(trimmed, "-") ||
		strings.HasSuffix(trimmed, "*") ||
		strings.HasSuffix(trimmed, "+")
}

func looksLikeBareJSONStart(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "{" || trimmed == "[" {
		return true
	}
	if strings.HasPrefix(trimmed, "{") {
		return looksLikeBareJSONObjectStart(strings.TrimSpace(trimmed[1:]))
	}
	if strings.HasPrefix(trimmed, "[") {
		return looksLikeBareJSONArrayStart(strings.TrimSpace(trimmed[1:]))
	}
	return false
}

func looksLikeBareJSONObjectStart(rest string) bool {
	if rest == "" {
		return true
	}
	switch rest[0] {
	case '"', '}':
		return true
	default:
		return false
	}
}

func looksLikeBareJSONArrayStart(rest string) bool {
	if rest == "" {
		return true
	}
	switch rest[0] {
	case '"', '{', '[', ']', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	case 't', 'f', 'n':
		return true
	default:
		return false
	}
}
