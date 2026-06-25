package wecom

import (
	"crypto/sha1"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	wecomMediumTableMaxRows = 20
	wecomLongCodeMaxBytes   = 3800
	wecomLongJSONMaxBytes   = 3800
)

type WeComMarkdownRenderer struct {
	WorkspacePath     string
	Preview           bool
	TableMode         string
	CoverableTextOnly bool
}

func NewWeComMarkdownRenderer(workspacePath string) WeComMarkdownRenderer {
	return WeComMarkdownRenderer{WorkspacePath: workspacePath, TableMode: defaultWeComRuntimeConfig.MarkdownTableMode}
}

func NewWeComMarkdownPreviewRenderer(workspacePath string) WeComMarkdownRenderer {
	return WeComMarkdownRenderer{WorkspacePath: workspacePath, Preview: true, TableMode: defaultWeComRuntimeConfig.MarkdownTableMode}
}

func (r WeComMarkdownRenderer) Render(blocks []MarkdownBlock) RenderedMessage {
	msg := RenderedMessage{Units: make([]RenderedUnit, 0, len(blocks))}
	for _, block := range blocks {
		switch block.Type {
		case MarkdownBlockTable:
			msg.Units = append(msg.Units, r.renderTable(block)...)
		case MarkdownBlockCode:
			msg.Units = append(msg.Units, r.renderCode(block))
		case MarkdownBlockJSON:
			msg.Units = append(msg.Units, r.renderJSON(block))
		case MarkdownBlockHeading:
			msg.Units = append(msg.Units, RenderedUnit{Kind: "text", Text: normalizeWeComMarkdown(block.Text), SourceType: string(MarkdownBlockHeading)})
		case MarkdownBlockList:
			msg.Units = append(msg.Units, RenderedUnit{Kind: "text", Text: normalizeWeComMarkdown(block.Text), SourceType: string(MarkdownBlockList)})
		case MarkdownBlockQuote:
			msg.Units = append(msg.Units, RenderedUnit{Kind: "text", Text: normalizeWeComMarkdown(block.Text), SourceType: string(MarkdownBlockQuote)})
		case MarkdownBlockRaw:
			msg.Units = append(msg.Units, RenderedUnit{Kind: "raw", Text: normalizeWeComMarkdown(block.Text), SourceType: string(MarkdownBlockRaw)})
		default:
			msg.Units = append(msg.Units, RenderedUnit{Kind: "text", Text: normalizeWeComMarkdown(block.Text), SourceType: "answer"})
		}
	}
	return compactRenderedMessage(msg)
}

func (r WeComMarkdownRenderer) renderTable(block MarkdownBlock) []RenderedUnit {
	if r.CoverableTextOnly {
		return []RenderedUnit{{Kind: "table_markdown", Text: markdownTableAsMarkdown(block.Text), SourceType: "answer"}}
	}
	rows := markdownTableRows(block.Text)
	if len(rows) == 0 {
		return []RenderedUnit{{Kind: "raw", Text: normalizeWeComMarkdown(block.Text), SourceType: "answer"}}
	}
	dataRows := len(rows) - 1
	mode := strings.ToLower(strings.TrimSpace(r.TableMode))
	switch mode {
	case "", "auto", "markdown", "original":
		return []RenderedUnit{{Kind: "table_markdown", Text: markdownTableAsMarkdown(block.Text), SourceType: "answer"}}
	case "code":
		return []RenderedUnit{{Kind: "table_code", Text: fencedCode(markdownTableAsAlignedText(rows), ""), SourceType: "answer"}}
	case "bullets":
		if len(rows) < 2 {
			return []RenderedUnit{{Kind: "table_header", Text: markdownTableHeaderText(rows), SourceType: "answer"}}
		}
		return []RenderedUnit{{Kind: "table_bullets", Text: markdownTableAsBullets(rows), SourceType: "answer"}}
	case "csv", "file":
		if r.Preview {
			return []RenderedUnit{{Kind: "table_summary", Text: fmt.Sprintf("表格共 %d 行，最终结果将以 CSV 文件发送。", dataRows), SourceType: "answer"}}
		}
		if action, err := r.writeTableCSV(rows); err == nil {
			return []RenderedUnit{
				{Kind: "table_summary", Text: fmt.Sprintf("表格共 %d 行，已转为 CSV 文件发送。", dataRows), SourceType: "answer"},
				{Kind: "table_csv", Action: action, SourceType: "answer"},
			}
		}
		return []RenderedUnit{{Kind: "table_bullets", Text: markdownTableAsBullets(rows), SourceType: "answer"}}
	}
	return []RenderedUnit{{Kind: "table_markdown", Text: markdownTableAsMarkdown(block.Text), SourceType: "answer"}}
}

func markdownTableAsMarkdown(text string) string {
	return strings.TrimSpace(normalizeWeComMarkdown(text))
}

func (r WeComMarkdownRenderer) renderCode(block MarkdownBlock) RenderedUnit {
	text := strings.TrimSpace(block.Text)
	if r.CoverableTextOnly {
		return RenderedUnit{Kind: "code", Text: text, SourceType: "answer"}
	}
	if len(text) <= wecomLongCodeMaxBytes {
		return RenderedUnit{Kind: "code", Text: text, SourceType: "answer"}
	}
	body := stripMarkdownFence(text)
	if action, err := r.writeTextFile(body, "code", ".txt"); err == nil {
		return RenderedUnit{
			Kind:       "code_file",
			Text:       "代码块较长，已转为文件发送。",
			Action:     action,
			SourceType: "answer",
		}
	}
	return RenderedUnit{Kind: "code", Text: text, SourceType: "answer"}
}

func (r WeComMarkdownRenderer) renderJSON(block MarkdownBlock) RenderedUnit {
	text := strings.TrimSpace(block.Text)
	body := stripMarkdownFence(text)
	if r.CoverableTextOnly {
		if strings.HasPrefix(text, "```") || strings.HasPrefix(text, "~~~") {
			return RenderedUnit{Kind: "json_code", Text: text, SourceType: string(MarkdownBlockJSON)}
		}
		return RenderedUnit{Kind: "json_code", Text: fencedCode(body, "json"), SourceType: string(MarkdownBlockJSON)}
	}
	if len(body) <= wecomLongJSONMaxBytes {
		if strings.HasPrefix(text, "```") || strings.HasPrefix(text, "~~~") {
			return RenderedUnit{Kind: "json_code", Text: text, SourceType: string(MarkdownBlockJSON)}
		}
		return RenderedUnit{Kind: "json_code", Text: fencedCode(body, "json"), SourceType: string(MarkdownBlockJSON)}
	}
	if action, err := r.writeTextFile(body, "json", ".json"); err == nil {
		return RenderedUnit{
			Kind:       "json_file",
			Text:       "JSON 内容较长，已转为文件发送。",
			Action:     action,
			SourceType: string(MarkdownBlockJSON),
		}
	}
	return RenderedUnit{Kind: "json_code", Text: fencedCode(body, "json"), SourceType: string(MarkdownBlockJSON)}
}

func compactRenderedMessage(msg RenderedMessage) RenderedMessage {
	out := RenderedMessage{Units: make([]RenderedUnit, 0, len(msg.Units))}
	for _, unit := range msg.Units {
		unit.Text = strings.TrimSpace(unit.Text)
		if unit.Text == "" && unit.Action == nil {
			continue
		}
		out.Units = append(out.Units, unit)
	}
	return out
}

func markdownTableRows(text string) [][]string {
	lines := splitWeComMarkdownLines(text)
	if len(lines) < 2 || !isWeComMarkdownTableDelimiter(lines[1].text) {
		return nil
	}
	rows := [][]string{markdownTableCells(lines[0].text)}
	for _, line := range lines[2:] {
		if !isWeComMarkdownTableLine(line.text) {
			continue
		}
		rows = append(rows, markdownTableCells(line.text))
	}
	return rows
}

func markdownTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func markdownTableAsAlignedText(rows [][]string) string {
	widths := make([]int, 0)
	for _, row := range rows {
		for len(widths) < len(row) {
			widths = append(widths, 0)
		}
		for i, cell := range row {
			if len([]rune(cell)) > widths[i] {
				widths[i] = len([]rune(cell))
			}
		}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := make([]string, 0, len(row))
		for i, cell := range row {
			cells = append(cells, padRightRunes(cell, widths[i]))
		}
		lines = append(lines, strings.Join(cells, "  "))
	}
	return strings.Join(lines, "\n")
}

func markdownTableHeaderText(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	cells := make([]string, 0, len(rows[0]))
	for _, cell := range rows[0] {
		if trimmed := strings.TrimSpace(cell); trimmed != "" {
			cells = append(cells, trimmed)
		}
	}
	return strings.Join(cells, "；")
}

func markdownTableAsBullets(rows [][]string) string {
	if len(rows) < 2 {
		return ""
	}
	header := rows[0]
	out := make([]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		parts := make([]string, 0, len(row))
		for i, cell := range row {
			key := fmt.Sprintf("列%d", i+1)
			if i < len(header) && strings.TrimSpace(header[i]) != "" {
				key = strings.TrimSpace(header[i])
			}
			parts = append(parts, key+": "+strings.TrimSpace(cell))
		}
		out = append(out, "- "+strings.Join(parts, "；"))
	}
	return strings.Join(out, "\n")
}

func padRightRunes(text string, width int) string {
	if width <= 0 {
		return text
	}
	n := len([]rune(text))
	if n >= width {
		return text
	}
	return text + strings.Repeat(" ", width-n)
}

func fencedCode(body, lang string) string {
	if lang != "" {
		return "```" + lang + "\n" + body + "\n```"
	}
	return "```\n" + body + "\n```"
}

func stripMarkdownFence(text string) string {
	lines := splitWeComMarkdownLines(text)
	if len(lines) >= 2 && wecomMarkdownFenceRE.MatchString(lines[0].text) {
		last := strings.TrimSpace(lines[len(lines)-1].text)
		if strings.HasPrefix(last, "```") || strings.HasPrefix(last, "~~~") {
			body := make([]string, 0, len(lines)-2)
			for _, line := range lines[1 : len(lines)-1] {
				body = append(body, line.text)
			}
			return strings.Join(body, "\n")
		}
	}
	return text
}

func (r WeComMarkdownRenderer) writeTableCSV(rows [][]string) (*SendAction, error) {
	if r.WorkspacePath == "" {
		return nil, fmt.Errorf("workspace path is empty")
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.WriteAll(rows); err != nil {
		return nil, err
	}
	return r.writeTextFile(b.String(), "table", ".csv")
}

func (r WeComMarkdownRenderer) writeTextFile(content, prefix, ext string) (*SendAction, error) {
	if r.WorkspacePath == "" {
		return nil, fmt.Errorf("workspace path is empty")
	}
	sum := sha1.Sum([]byte(content))
	name := fmt.Sprintf("%s-%x%s", prefix, sum[:6], ext)
	rel := filepath.Join(".lumi-wecom", "exports", name)
	abs := filepath.Join(r.WorkspacePath, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return &SendAction{Type: "file", Path: rel, ResolvedPath: abs, FileName: name}, nil
}
