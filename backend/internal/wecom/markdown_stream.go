package wecom

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	wecomMarkdownListMarkerRE       = regexp.MustCompile(`^(\s{0,3})([-*+]\s+|\d+[.)]\s+)`)
	wecomMarkdownFenceRE            = regexp.MustCompile(`^\s*(` + "```" + `|~~~)`)
	wecomMarkdownHeadingRE          = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	wecomMarkdownInlineHeadingRE    = regexp.MustCompile(`([^#\n])[ \t]*(#{1,6}\s+)`)
	wecomMarkdownInlineListMarkerRE = regexp.MustCompile(`([；;。：:])\s*((?:✅\s*)?[-*+]\s+|(?:[-*+]\s+)(?:✅\s*)?|\d+[.)]\s+)`)
	wecomMarkdownInlineRiskRE       = regexp.MustCompile(`([^\s\n])[ \t]*((?:\x{1F534}|\x{1F7E1}|\x{1F7E2})\s+)`)
	wecomMarkdownBareOrderedItemRE  = regexp.MustCompile(`^\s*\d+[.)]\s*$`)
	wecomMarkdownBoldOrderedItemRE  = regexp.MustCompile(`^(\s{0,3}\d+[.)]\s+\*\*)\s*$`)
	wecomMarkdownRiskOnlyRE         = regexp.MustCompile(`^\s*(\x{1F534}|\x{1F7E1}|\x{1F7E2})\s*$`)
	wecomMarkdownTableDelimiterRE   = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$`)
)

type wecomMarkdownLine struct {
	text  string
	start int
	end   int
	hasNL bool
}

type wecomMarkdownBlock struct {
	kind string
	text string
}

func stabilizeWeComMarkdownStream(content string) string {
	content = normalizeWeComMarkdownStreamInput(content)
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return ""
	}

	stableEnd := 0
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" {
			stableEnd = line.end
			i++
			continue
		}

		if marker := wecomMarkdownFenceRE.FindStringSubmatch(line.text); len(marker) == 2 {
			close := findClosingWeComMarkdownFence(lines, i+1, marker[1])
			if close < 0 {
				break
			}
			stableEnd = lines[close].end
			i = close + 1
			continue
		}

		if isWeComMarkdownTableLine(line.text) {
			if i+1 >= len(lines) || !isWeComMarkdownTableDelimiter(lines[i+1].text) {
				break
			}
			stableEnd = lines[i+1].end
			j := i + 2
			for j < len(lines) && isWeComMarkdownTableLine(lines[j].text) {
				if !lines[j].hasNL {
					break
				}
				stableEnd = lines[j].end
				j++
			}
			if j < len(lines) && isPotentialIncompleteWeComMarkdownTableLine(lines[j].text) {
				break
			}
			i = j
			continue
		}

		if isWeComMarkdownListLine(line.text) {
			j := i
			blockEnd := 0
			for j < len(lines) && isWeComMarkdownListLine(lines[j].text) {
				if !lines[j].hasNL {
					break
				}
				blockEnd = lines[j].end
				j++
			}
			if blockEnd == 0 {
				break
			}
			stableEnd = blockEnd
			i = j
			continue
		}

		if isWeComMarkdownQuoteLine(line.text) {
			j := i
			blockEnd := 0
			for j < len(lines) && isWeComMarkdownQuoteLine(lines[j].text) {
				if !lines[j].hasNL {
					break
				}
				blockEnd = lines[j].end
				j++
			}
			if blockEnd == 0 {
				break
			}
			stableEnd = blockEnd
			i = j
			continue
		}

		if wecomMarkdownHeadingRE.MatchString(line.text) {
			if !line.hasNL {
				break
			}
			next := nextNonblankWeComMarkdownLine(lines, i+1)
			if next >= 0 && startsWeComMarkdownStructure(lines[next].text) {
				i = next
				continue
			}
		}

		stableEnd = line.end
		i++
	}
	if stableEnd == 0 {
		return ""
	}
	return strings.TrimSpace(content[:stableEnd])
}

func nextNonblankWeComMarkdownLine(lines []wecomMarkdownLine, start int) int {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i].text) != "" {
			return i
		}
	}
	return -1
}

func normalizeWeComMarkdown(content string) string {
	return normalizeVisibleText(normalizeWeComMarkdownStreamInput(content))
}

func splitWeComMarkdownMessages(content string, maxBytes int) []string {
	content = normalizeWeComMarkdown(content)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	if maxBytes <= 0 || len(content) <= maxBytes {
		return []string{strings.TrimSpace(content)}
	}
	blocks := scanWeComMarkdownBlocks(content)
	parts := make([]string, 0, len(content)/maxBytes+1)
	var current strings.Builder
	flush := func() {
		part := strings.TrimSpace(current.String())
		if part != "" {
			parts = append(parts, part)
		}
		current.Reset()
	}
	appendBlock := func(block string) {
		block = strings.TrimSpace(block)
		if block == "" {
			return
		}
		if current.Len() == 0 {
			current.WriteString(block)
			return
		}
		current.WriteString("\n\n")
		current.WriteString(block)
	}
	for _, block := range blocks {
		text := strings.TrimSpace(block.text)
		if text == "" {
			continue
		}
		if current.Len() > 0 && len(strings.TrimSpace(current.String()))+len("\n\n")+len(text) <= maxBytes {
			appendBlock(text)
			continue
		}
		if current.Len() > 0 {
			currentText := strings.TrimSpace(current.String())
			firstBudget := maxBytes - len(currentText) - len("\n\n")
			if firstBudget > 0 {
				firstParts := splitOversizedWeComMarkdownBlock(block, firstBudget)
				firstPart := ""
				firstPartIndex := -1
				for i, part := range firstParts {
					part = strings.TrimSpace(part)
					if part == "" {
						continue
					}
					firstPart = part
					firstPartIndex = i
					break
				}
				if firstPart != "" && len(currentText)+len("\n\n")+len(firstPart) <= maxBytes {
					parts = append(parts, currentText+"\n\n"+firstPart)
					if block.kind == "paragraph" {
						remainingText := strings.TrimSpace(text[len(firstParts[firstPartIndex]):])
						if remainingText != "" {
							remainingBlock := block
							remainingBlock.text = remainingText
							for _, part := range splitOversizedWeComMarkdownBlock(remainingBlock, maxBytes) {
								part = strings.TrimSpace(part)
								if part != "" {
									parts = append(parts, part)
								}
							}
						}
					} else {
						for _, part := range firstParts[firstPartIndex+1:] {
							part = strings.TrimSpace(part)
							if part != "" {
								parts = append(parts, part)
							}
						}
					}
					current.Reset()
					continue
				}
			}
		}
		if current.Len() > 0 {
			flush()
		}
		if len(text) <= maxBytes {
			appendBlock(text)
			continue
		}
		for _, part := range splitOversizedWeComMarkdownBlock(block, maxBytes) {
			part = strings.TrimSpace(part)
			if part != "" {
				parts = append(parts, part)
			}
		}
	}
	flush()
	return parts
}

func scanWeComMarkdownBlocks(content string) []wecomMarkdownBlock {
	lines := splitWeComMarkdownLines(content)
	blocks := make([]wecomMarkdownBlock, 0, len(lines))
	for i := 0; i < len(lines); {
		for i < len(lines) && strings.TrimSpace(lines[i].text) == "" {
			i++
		}
		if i >= len(lines) {
			break
		}
		start := i
		kind := "paragraph"
		if marker := wecomMarkdownFenceRE.FindStringSubmatch(lines[i].text); len(marker) == 2 {
			kind = "code"
			close := findClosingWeComMarkdownFence(lines, i+1, marker[1])
			if close < 0 {
				i = len(lines)
			} else {
				i = close + 1
			}
			blocks = append(blocks, joinWeComMarkdownBlock(kind, lines[start:i]))
			continue
		}
		if isWeComMarkdownTableStart(lines, i) {
			kind = "table"
			i += 2
			for i < len(lines) && isWeComMarkdownTableLine(lines[i].text) {
				i++
			}
			blocks = append(blocks, joinWeComMarkdownBlock(kind, lines[start:i]))
			continue
		}
		if isWeComMarkdownListLine(lines[i].text) {
			kind = "list"
			i++
			for i < len(lines) && strings.TrimSpace(lines[i].text) != "" && !startsNewWeComMarkdownBlock(lines, i) {
				i++
			}
			blocks = append(blocks, joinWeComMarkdownBlock(kind, lines[start:i]))
			continue
		}
		if wecomMarkdownHeadingRE.MatchString(lines[i].text) {
			kind = "heading"
			i++
			for i < len(lines) && strings.TrimSpace(lines[i].text) != "" && !wecomMarkdownHeadingRE.MatchString(lines[i].text) {
				i++
			}
			blocks = append(blocks, joinWeComMarkdownBlock(kind, lines[start:i]))
			continue
		}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i].text) != "" && !startsNewWeComMarkdownBlock(lines, i) {
			i++
		}
		blocks = append(blocks, joinWeComMarkdownBlock(kind, lines[start:i]))
	}
	return blocks
}

func joinWeComMarkdownBlock(kind string, lines []wecomMarkdownLine) wecomMarkdownBlock {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, line.text)
	}
	return wecomMarkdownBlock{kind: kind, text: strings.Join(parts, "\n")}
}

func startsNewWeComMarkdownBlock(lines []wecomMarkdownLine, i int) bool {
	return wecomMarkdownFenceRE.MatchString(lines[i].text) ||
		isWeComMarkdownTableStart(lines, i) ||
		isWeComMarkdownListLine(lines[i].text) ||
		wecomMarkdownHeadingRE.MatchString(lines[i].text)
}

func isWeComMarkdownTableStart(lines []wecomMarkdownLine, i int) bool {
	return i+1 < len(lines) && isWeComMarkdownTableLine(lines[i].text) && isWeComMarkdownTableDelimiter(lines[i+1].text)
}

func splitOversizedWeComMarkdownBlock(block wecomMarkdownBlock, maxBytes int) []string {
	switch block.kind {
	case "table":
		return splitOversizedWeComMarkdownTable(block.text, maxBytes)
	case "code":
		return splitOversizedWeComMarkdownCode(block.text, maxBytes)
	default:
		return splitWeComMarkdownTextByBytes(block.text, maxBytes)
	}
}

func splitOversizedWeComMarkdownTable(content string, maxBytes int) []string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) < 3 || !isWeComMarkdownTableDelimiter(lines[1].text) {
		return splitWeComMarkdownTextByBytes(content, maxBytes)
	}
	header := strings.TrimSpace(lines[0].text)
	delimiter := strings.TrimSpace(lines[1].text)
	prefix := header + "\n" + delimiter
	if len(prefix) >= maxBytes {
		return splitWeComMarkdownTextByBytes(content, maxBytes)
	}
	parts := make([]string, 0, len(lines)/4+1)
	current := prefix
	for _, rowLine := range lines[2:] {
		row := strings.TrimSpace(rowLine.text)
		if row == "" {
			continue
		}
		if len(current)+1+len(row) <= maxBytes {
			current += "\n" + row
			continue
		}
		if current != prefix {
			parts = append(parts, current)
		}
		current = prefix
		if len(current)+1+len(row) <= maxBytes {
			current += "\n" + row
			continue
		}
		rowBudget := maxBytes - len(prefix) - 1
		if rowBudget <= 0 {
			parts = append(parts, splitWeComMarkdownTextByBytes(row, maxBytes)...)
			continue
		}
		rowParts := splitWeComMarkdownTextByBytes(row, rowBudget)
		for _, rowPart := range rowParts {
			rowPart = strings.TrimSpace(rowPart)
			if rowPart != "" {
				parts = append(parts, prefix+"\n"+rowPart)
			}
		}
		current = prefix
	}
	if current != prefix || len(parts) == 0 {
		parts = append(parts, current)
	}
	return parts
}

func splitOversizedWeComMarkdownCode(content string, maxBytes int) []string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) < 2 {
		return splitWeComMarkdownTextByBytes(content, maxBytes)
	}
	open := strings.TrimSpace(lines[0].text)
	close := strings.TrimSpace(lines[len(lines)-1].text)
	if !wecomMarkdownFenceRE.MatchString(open) || !strings.HasPrefix(close, open[:3]) {
		return splitWeComMarkdownTextByBytes(content, maxBytes)
	}
	wrapperBytes := len(open) + len(close) + len("\n\n")
	if wrapperBytes >= maxBytes {
		return splitWeComMarkdownTextByBytes(content, maxBytes)
	}
	bodyLines := make([]string, 0, len(lines)-2)
	for _, line := range lines[1 : len(lines)-1] {
		bodyLines = append(bodyLines, line.text)
	}
	bodyParts := splitWeComMarkdownTextByBytesPreserveWhitespace(strings.Join(bodyLines, "\n"), maxBytes-wrapperBytes)
	out := make([]string, 0, len(bodyParts))
	for _, part := range bodyParts {
		out = append(out, open+"\n"+part+"\n"+close)
	}
	return out
}

func splitWeComMarkdownTextByBytes(content string, maxBytes int) []string {
	return splitWeComMarkdownTextByBytesMode(content, maxBytes, true)
}

func splitWeComMarkdownTextByBytesPreserveWhitespace(content string, maxBytes int) []string {
	return splitWeComMarkdownTextByBytesMode(content, maxBytes, false)
}

func splitWeComMarkdownTextByBytesMode(content string, maxBytes int, skipLeadingWhitespace bool) []string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return []string{content}
	}
	parts := make([]string, 0, len(content)/maxBytes+1)
	for start := 0; start < len(content); {
		end := start + maxBytes
		if end >= len(content) {
			parts = append(parts, content[start:])
			break
		}
		cut := bestWeComMarkdownTextCut(content[start:], maxBytes)
		if cut <= 0 {
			cut = utf8SafeIndex(content[start:], maxBytes)
		}
		if cut <= 0 {
			_, size := utf8.DecodeRuneInString(content[start:])
			if size <= 0 {
				break
			}
			cut = size
		}
		parts = append(parts, content[start:start+cut])
		start += cut
		if skipLeadingWhitespace {
			for start < len(content) && (content[start] == '\n' || content[start] == ' ') {
				start++
			}
		}
	}
	return parts
}

func bestWeComMarkdownTextCut(content string, maxBytes int) int {
	limit := utf8SafeIndex(content, maxBytes)
	if limit <= 0 {
		return 0
	}
	best := 0
	for _, marker := range []string{"\n\n", "\n", "。", "；", ";", "，", ","} {
		if idx := strings.LastIndex(content[:limit], marker); idx >= 0 {
			best = idx + len(marker)
			break
		}
	}
	return best
}

func splitWeComLongReply(content string, maxPreviewBytes int) (preview string, remaining string) {
	if maxPreviewBytes <= 0 || len(content) <= maxPreviewBytes {
		return content, ""
	}
	limit := utf8SafeIndex(content, maxPreviewBytes)
	if limit <= 0 {
		return "", content
	}

	lines := splitWeComMarkdownLines(content)
	unsafeRanges := wecomMarkdownUnsafeCutRanges(lines)

	if cut := bestWeComMarkdownParagraphCut(content, limit, unsafeRanges); cut > 0 {
		return content[:cut], content[cut:]
	}
	if cut := bestWeComMarkdownHeadingCut(content, limit, unsafeRanges); cut > 0 {
		return content[:cut], content[cut:]
	}
	if cut := bestWeComMarkdownLineCut(lines, limit, unsafeRanges); cut > 0 {
		return content[:cut], content[cut:]
	}
	return content[:limit], content[limit:]
}

func normalizeWeComMarkdownStreamInput(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}

	var out strings.Builder
	segmentStart := 0
	for i := 0; i < len(lines); {
		marker := wecomMarkdownFenceRE.FindStringSubmatch(lines[i].text)
		if len(marker) != 2 {
			i++
			continue
		}

		fenceStart := lines[i].start
		out.WriteString(normalizeWeComMarkdownFencePrefixSegment(content[segmentStart:fenceStart]))
		close := findClosingWeComMarkdownFence(lines, i+1, marker[1])
		fenceEnd := len(content)
		if close >= 0 {
			fenceEnd = lines[close].end
			i = close + 1
		} else {
			i = len(lines)
		}
		out.WriteString(content[fenceStart:fenceEnd])
		segmentStart = fenceEnd
	}
	out.WriteString(normalizeWeComMarkdownTextSegment(content[segmentStart:]))
	return out.String()
}

func normalizeWeComMarkdownFencePrefixSegment(content string) string {
	normalized := normalizeWeComMarkdownTextSegment(content)
	wantNewlines := trailingWeComMarkdownNewlines(content)
	if wantNewlines > 2 {
		wantNewlines = 2
	}
	gotNewlines := trailingWeComMarkdownNewlines(normalized)
	if gotNewlines < wantNewlines {
		normalized += strings.Repeat("\n", wantNewlines-gotNewlines)
	}
	return normalized
}

func trailingWeComMarkdownNewlines(content string) int {
	count := 0
	for i := len(content) - 1; i >= 0 && content[i] == '\n'; i-- {
		count++
	}
	return count
}

func normalizeWeComMarkdownTextSegment(content string) string {
	endsWithNewline := strings.HasSuffix(content, "\n")
	content = wecomMarkdownInlineHeadingRE.ReplaceAllString(content, "$1\n\n$2")
	content = splitInlineWeComMarkdownRiskMarkers(content)
	content = wecomMarkdownInlineListMarkerRE.ReplaceAllString(content, "$1\n$2")
	content = splitInlineWeComMarkdownTables(content)
	content = mergeSplitWeComMarkdownBoldOrderedItems(content)
	content = convertWeComMarkdownTSVTables(content)
	content = mergeSplitWeComMarkdownTableRows(content)
	content = ensureWeComMarkdownHorizontalRuleSpacing(content)
	content = ensureWeComMarkdownHeadingSpacing(content)
	content = ensureWeComMarkdownTableSpacing(content)
	content = mergeBareWeComMarkdownOrderedItems(content)
	content = regexp.MustCompile(`\n{3,}`).ReplaceAllString(content, "\n\n")
	if endsWithNewline && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content
}

func splitInlineWeComMarkdownRiskMarkers(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line.text, "|") {
			out = append(out, line.text)
			continue
		}
		out = append(out, wecomMarkdownInlineRiskRE.ReplaceAllString(line.text, "$1\n\n$2"))
	}
	return strings.Join(out, "\n")
}

func splitInlineWeComMarkdownTables(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		pipe := strings.Index(line.text, "|")
		if pipe > 0 && strings.TrimSpace(line.text[:pipe]) != "" && isWeComMarkdownTableLine(line.text[pipe:]) {
			out = append(out, strings.TrimSpace(line.text[:pipe]), "", strings.TrimSpace(line.text[pipe:]))
			continue
		}
		out = append(out, line.text)
	}
	return strings.Join(out, "\n")
}

func mergeSplitWeComMarkdownBoldOrderedItems(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		match := wecomMarkdownBoldOrderedItemRE.FindStringSubmatch(lines[i].text)
		if len(match) != 2 {
			out = append(out, lines[i].text)
			continue
		}
		prefix := strings.TrimSpace(match[1])
		parts := make([]string, 0, 2)
		j := i + 1
		for ; j < len(lines); j++ {
			text := strings.TrimSpace(lines[j].text)
			if text == "" {
				break
			}
			parts = append(parts, text)
			if strings.Contains(text, "**") {
				out = append(out, prefix+strings.Join(parts, " "))
				i = j
				break
			}
		}
		if i == j {
			continue
		}
		out = append(out, lines[i].text)
	}
	return strings.Join(out, "\n")
}

func convertWeComMarkdownTSVTables(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines)+2)
	for i := 0; i < len(lines); i++ {
		if !isWeComMarkdownTSVTableLine(lines[i].text) {
			out = append(out, lines[i].text)
			continue
		}
		start := i
		for i < len(lines) && isWeComMarkdownTSVTableLine(lines[i].text) {
			row := wecomMarkdownTSVLineToTableRow(lines[i].text)
			out = append(out, row)
			if i == start {
				out = append(out, wecomMarkdownDelimiterForColumns(wecomMarkdownTableCellCount(row)))
			}
			i++
		}
		i--
	}
	return strings.Join(out, "\n")
}

func mergeSplitWeComMarkdownTableRows(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if isWeComMarkdownTableLine(lines[i].text) && i+1 < len(lines) && isWeComMarkdownTableDelimiter(lines[i+1].text) {
			out = append(out, lines[i].text, lines[i+1].text)
			i += 2
			for i < len(lines) {
				if merged, consumed, ok := mergeSplitWeComMarkdownTableRowAt(lines, i); ok {
					out = append(out, merged)
					i += consumed
					continue
				}
				if strings.TrimSpace(lines[i].text) == "" {
					i--
					break
				}
				if !isWeComMarkdownTableLine(lines[i].text) {
					i--
					break
				}
				out = append(out, lines[i].text)
				i++
			}
			continue
		}
		if merged, consumed, ok := mergeSplitWeComMarkdownTableRowAt(lines, i); ok {
			out = append(out, merged)
			i += consumed - 1
			continue
		}
		if strings.TrimSpace(lines[i].text) == "|" && i+2 < len(lines) {
			firstCell := strings.TrimSpace(lines[i+1].text)
			rest := strings.TrimSpace(lines[i+2].text)
			if firstCell != "" && !strings.Contains(firstCell, "|") && strings.HasPrefix(rest, "|") && isWeComMarkdownTableLine(rest) {
				out = append(out, "| "+firstCell+" "+rest)
				i += 2
				continue
			}
		}
		out = append(out, lines[i].text)
	}
	return strings.Join(out, "\n")
}

func mergeSplitWeComMarkdownTableRowAt(lines []wecomMarkdownLine, i int) (string, int, bool) {
	if i+1 >= len(lines) {
		return "", 0, false
	}
	firstCell, ok := wecomMarkdownTableFirstCellFragment(lines[i].text)
	if ok {
		if i+2 < len(lines) {
			if risk := wecomMarkdownRiskOnlyRE.FindStringSubmatch(lines[i+1].text); len(risk) == 2 {
				rest := strings.TrimSpace(lines[i+2].text)
				if isWeComMarkdownTableRestFragment(rest) {
					return joinWeComMarkdownSplitTableRow(firstCell+" "+risk[1], rest), 3, true
				}
			}
		}
		rest := strings.TrimSpace(lines[i+1].text)
		if isWeComMarkdownTableRestFragment(rest) {
			return joinWeComMarkdownSplitTableRow(firstCell, rest), 2, true
		}
	}
	if strings.TrimSpace(lines[i].text) == "|" && i+2 < len(lines) {
		firstCell := strings.TrimSpace(lines[i+1].text)
		rest := strings.TrimSpace(lines[i+2].text)
		if firstCell != "" && !strings.Contains(firstCell, "|") && isWeComMarkdownTableRestFragment(rest) {
			return joinWeComMarkdownSplitTableRow(firstCell, rest), 3, true
		}
	}
	return "", 0, false
}

func wecomMarkdownTableFirstCellFragment(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") || strings.Count(trimmed, "|") != 1 {
		return "", false
	}
	cell := strings.TrimSpace(strings.TrimPrefix(trimmed, "|"))
	return cell, cell != ""
}

func joinWeComMarkdownSplitTableRow(firstCell string, rest string) string {
	return "| " + strings.TrimSpace(firstCell) + " | " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "|"))
}

func isWeComMarkdownTableRestFragment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && !isWeComMarkdownTableDelimiter(trimmed) && wecomMarkdownTableCellCount(trimmed) >= 1
}

func isWeComMarkdownTSVTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Count(trimmed, "\t") >= 2 && !strings.Contains(trimmed, "|")
}

func wecomMarkdownTSVLineToTableRow(line string) string {
	cells := strings.Split(strings.TrimSpace(line), "\t")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

func wecomMarkdownDelimiterForColumns(columns int) string {
	if columns < 1 {
		columns = 1
	}
	cells := make([]string, columns)
	for i := range cells {
		cells[i] = "---"
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

func ensureWeComMarkdownHorizontalRuleSpacing(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		text := line.text
		trimmed := strings.TrimSpace(text)
		if isWeComMarkdownTableDelimiter(trimmed) || isWeComMarkdownTableLine(trimmed) {
			out = append(out, text)
			continue
		}
		if idx := strings.Index(text, "---"); idx > 0 {
			before := strings.TrimSpace(text[:idx])
			after := strings.TrimSpace(text[idx+len("---"):])
			if before != "" {
				out = append(out, before)
				if len(out) == 0 || strings.TrimSpace(out[len(out)-1]) != "" {
					out = append(out, "")
				}
				out = append(out, "---")
				if after != "" {
					out = append(out, "", after)
				}
				continue
			}
		}
		out = append(out, text)
	}
	return strings.Join(out, "\n")
}

func ensureWeComMarkdownHeadingSpacing(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines)+4)
	for i, line := range lines {
		isHeading := wecomMarkdownHeadingRE.MatchString(line.text)
		if isHeading && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, line.text)
		if isHeading && i+1 < len(lines) && strings.TrimSpace(lines[i+1].text) != "" {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

func mergeBareWeComMarkdownOrderedItems(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if wecomMarkdownBareOrderedItemRE.MatchString(lines[i].text) {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j].text) == "" {
				j++
			}
			if j < len(lines) {
				out = append(out, strings.TrimSpace(lines[i].text)+" "+strings.TrimSpace(lines[j].text))
				i = j
				continue
			}
		}
		out = append(out, lines[i].text)
	}
	return strings.Join(out, "\n")
}

func ensureWeComMarkdownTableSpacing(content string) string {
	lines := splitWeComMarkdownLines(content)
	if len(lines) == 0 {
		return content
	}
	out := make([]string, 0, len(lines)+2)
	for i, line := range lines {
		if isWeComMarkdownTableLine(line.text) && i+1 < len(lines) && isWeComMarkdownTableDelimiter(lines[i+1].text) {
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
		}
		out = append(out, line.text)
	}
	return strings.Join(out, "\n")
}

func splitWeComMarkdownLines(content string) []wecomMarkdownLine {
	if content == "" {
		return nil
	}
	lines := make([]wecomMarkdownLine, 0, strings.Count(content, "\n")+1)
	start := 0
	for start < len(content) {
		next := strings.IndexByte(content[start:], '\n')
		if next < 0 {
			lines = append(lines, wecomMarkdownLine{text: content[start:], start: start, end: len(content), hasNL: false})
			break
		}
		end := start + next
		lines = append(lines, wecomMarkdownLine{text: content[start:end], start: start, end: end + 1, hasNL: true})
		start = end + 1
	}
	return lines
}

func findClosingWeComMarkdownFence(lines []wecomMarkdownLine, start int, marker string) int {
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i].text), marker) {
			return i
		}
	}
	return -1
}

func isWeComMarkdownListLine(line string) bool {
	return wecomMarkdownListMarkerRE.MatchString(line)
}

func isWeComMarkdownQuoteLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), ">")
}

func isWeComMarkdownTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "|") && trimmed != "|" && !isWeComMarkdownTableDelimiter(trimmed) && wecomMarkdownTableCellCount(trimmed) >= 2
}

func isWeComMarkdownTableDelimiter(line string) bool {
	return wecomMarkdownTableDelimiterRE.MatchString(line)
}

func isPotentialIncompleteWeComMarkdownTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && !isWeComMarkdownTableDelimiter(trimmed) && wecomMarkdownTableCellCount(trimmed) < 2
}

func wecomMarkdownTableCellCount(line string) int {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") {
		return 0
	}
	parts := strings.Split(trimmed, "|")
	if strings.HasPrefix(trimmed, "|") && len(parts) > 0 {
		parts = parts[1:]
	}
	if strings.HasSuffix(trimmed, "|") && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	count := 0
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

func startsWeComMarkdownStructure(line string) bool {
	return isWeComMarkdownListLine(line) ||
		isWeComMarkdownTableLine(line) ||
		isWeComMarkdownQuoteLine(line) ||
		wecomMarkdownFenceRE.MatchString(line)
}

type wecomMarkdownRange struct {
	start int
	end   int
}

func wecomMarkdownUnsafeCutRanges(lines []wecomMarkdownLine) []wecomMarkdownRange {
	var ranges []wecomMarkdownRange
	for i := 0; i < len(lines); {
		if marker := wecomMarkdownFenceRE.FindStringSubmatch(lines[i].text); len(marker) == 2 {
			close := findClosingWeComMarkdownFence(lines, i+1, marker[1])
			end := len(lines)
			if close >= 0 {
				end = close + 1
			}
			ranges = append(ranges, wecomMarkdownRange{start: lines[i].start, end: lines[end-1].end})
			i = end
			continue
		}
		if isWeComMarkdownTableLine(lines[i].text) {
			j := i + 1
			for j < len(lines) && (isWeComMarkdownTableDelimiter(lines[j].text) || isWeComMarkdownTableLine(lines[j].text)) {
				j++
			}
			if j > i+1 {
				ranges = append(ranges, wecomMarkdownRange{start: lines[i].start, end: lines[j-1].end})
				i = j
				continue
			}
		}
		if isWeComMarkdownListLine(lines[i].text) {
			j := i + 1
			for j < len(lines) && isWeComMarkdownListLine(lines[j].text) {
				j++
			}
			if j > i+1 {
				ranges = append(ranges, wecomMarkdownRange{start: lines[i].start, end: lines[j-1].end})
				i = j
				continue
			}
		}
		i++
	}
	return ranges
}

func bestWeComMarkdownParagraphCut(content string, limit int, unsafe []wecomMarkdownRange) int {
	best := 0
	for start := 0; start < limit; {
		idx := strings.Index(content[start:limit], "\n\n")
		if idx < 0 {
			break
		}
		cut := start + idx + 2
		if isWeComMarkdownSafeCut(cut, unsafe) {
			best = cut
		}
		start = cut
	}
	return best
}

func bestWeComMarkdownHeadingCut(content string, limit int, unsafe []wecomMarkdownRange) int {
	best := 0
	for _, marker := range []string{"\n# ", "\n## ", "\n### ", "\n#### ", "\n##### ", "\n###### "} {
		for start := 0; start < limit; {
			idx := strings.Index(content[start:limit], marker)
			if idx < 0 {
				break
			}
			cut := start + idx + 1
			if cut > 0 && isWeComMarkdownSafeCut(cut, unsafe) && cut > best {
				best = cut
			}
			start = cut + len(marker) - 1
		}
	}
	return best
}

func bestWeComMarkdownLineCut(lines []wecomMarkdownLine, limit int, unsafe []wecomMarkdownRange) int {
	best := 0
	for _, line := range lines {
		if line.end > limit {
			break
		}
		if line.end > 0 && isWeComMarkdownSafeCut(line.end, unsafe) {
			best = line.end
		}
	}
	return best
}

func isWeComMarkdownSafeCut(cut int, unsafe []wecomMarkdownRange) bool {
	for _, r := range unsafe {
		if cut > r.start && cut < r.end {
			return false
		}
	}
	return true
}

func utf8SafeIndex(s string, maxBytes int) int {
	if maxBytes >= len(s) {
		return len(s)
	}
	if maxBytes <= 0 {
		return 0
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	if end == 0 {
		return 0
	}
	return end
}
