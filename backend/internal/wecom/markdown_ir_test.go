package wecom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownParserLiveDefersIncompleteTable(t *testing.T) {
	parser := NewMarkdownParser()
	if got := parser.PushFullText("| 项目 | 内容 |"); len(got) != 0 {
		t.Fatalf("header only blocks = %+v, want none", got)
	}
	if got := parser.PushFullText("| 项目 | 内容 |\n| --- | --- |\n| 毛利"); len(got) != 0 {
		t.Fatalf("incomplete row blocks = %+v, want none", got)
	}
	got := parser.PushFullText("| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |\n")
	if len(got) != 1 || got[0].Type != MarkdownBlockTable {
		t.Fatalf("complete table blocks = %+v, want one table", got)
	}
}

func TestMarkdownParserDoesNotTreatPlainPipeOrCodeFenceAsTable(t *testing.T) {
	parser := NewMarkdownParser()
	got := parser.PushFullText("普通文本 a | b 不是表格\n")
	if len(got) != 1 || got[0].Type != MarkdownBlockParagraph {
		t.Fatalf("plain pipe blocks = %+v, want paragraph", got)
	}
	got = parser.PushFullText("```text\n| a | b |\n| --- | --- |\n```\n")
	if len(got) != 1 || got[0].Type != MarkdownBlockCode {
		t.Fatalf("fenced pipe blocks = %+v, want code", got)
	}
}

func TestMarkdownParserLiveDefersUnclosedFenceAndFlushReturnsRaw(t *testing.T) {
	parser := NewMarkdownParser()
	if got := parser.PushFullText("before\n\n```go\nfmt.Println(1)"); len(got) != 1 || got[0].Text != "before" {
		t.Fatalf("live unclosed fence blocks = %+v, want preceding paragraph only", got)
	}
	got := parser.Flush()
	if len(got) != 2 || got[1].Type != MarkdownBlockRaw {
		t.Fatalf("final unclosed fence blocks = %+v, want raw tail", got)
	}
}

func TestWeComMarkdownRendererTables(t *testing.T) {
	renderer := NewWeComMarkdownRenderer(t.TempDir())

	headerOnly := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: "| 项目 | 内容 |\n| --- | --- |"}})
	if got := headerOnly.Text(); !strings.Contains(got, "| 项目 | 内容 |") || !strings.Contains(got, "| --- | --- |") || strings.Contains(got, "```") {
		t.Fatalf("header-only table rendered = %q, want markdown table", got)
	}

	small := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: "| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |"}})
	if got := small.Text(); !strings.Contains(got, "| 项目 | 内容 |") || !strings.Contains(got, "| --- | --- |") || !strings.Contains(got, "| 毛利 | 10 |") || strings.Contains(got, "```") {
		t.Fatalf("small table rendered = %q, want markdown table", got)
	}

	rows := []string{"| 区域 | 收入 |", "| --- | --- |"}
	for i := 0; i < wecomMediumTableMaxRows; i++ {
		rows = append(rows, "| 华东 | 10 |")
	}
	medium := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: strings.Join(rows, "\n")}})
	if got := medium.Text(); !strings.Contains(got, "| 区域 | 收入 |") || !strings.Contains(got, "| --- | --- |") || !strings.Contains(got, "| 华东 | 10 |") || strings.Contains(got, "```") || strings.Contains(got, "- 区域: 华东") {
		t.Fatalf("medium table rendered = %q, want markdown table", got)
	}

	rows = []string{"| 区域 | 收入 |", "| --- | --- |"}
	for i := 0; i < wecomMediumTableMaxRows+1; i++ {
		rows = append(rows, "| 华东 | 10 |")
	}
	large := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: strings.Join(rows, "\n")}})
	if len(large.Units) != 1 || large.Units[0].Kind != "table_markdown" || large.Units[0].Action != nil {
		t.Fatalf("large table units = %+v, want table_markdown text unit", large.Units)
	}
	if got := large.Text(); !strings.Contains(got, "| 区域 | 收入 |") || !strings.Contains(got, "| --- | --- |") || strings.Contains(got, "```") || strings.Contains(got, "CSV") {
		t.Fatalf("large table rendered = %q, want markdown table without CSV fallback", got)
	}
}

func TestWeComMarkdownRendererTableModeOverride(t *testing.T) {
	table := "| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |"
	for _, mode := range []string{"", "auto", "markdown", "original"} {
		renderer := NewWeComMarkdownRenderer(t.TempDir())
		renderer.TableMode = mode
		rendered := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: table}})
		if len(rendered.Units) != 1 || rendered.Units[0].Kind != "table_markdown" {
			t.Fatalf("table mode %s units = %+v, want table_markdown", mode, rendered.Units)
		}
		if got := rendered.Text(); got != table || strings.Contains(got, "```") {
			t.Fatalf("table mode %s rendered = %q, want original markdown table", mode, got)
		}
	}

	renderer := NewWeComMarkdownRenderer(t.TempDir())
	renderer.TableMode = "bullets"
	rendered := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: table}})
	if got := rendered.Text(); !strings.Contains(got, "- 项目: 毛利") || strings.Contains(got, "```") {
		t.Fatalf("table mode bullets rendered = %q, want bullet table", got)
	}

	renderer.TableMode = "code"
	rendered = renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: table}})
	if got := rendered.Text(); !strings.Contains(got, "```") || strings.Contains(got, "| --- |") {
		t.Fatalf("table mode code rendered = %q, want safe code table", got)
	}

	headerOnly := "| 项目 | 内容 |\n| --- | --- |"
	rendered = renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: headerOnly}})
	if got := rendered.Text(); !strings.Contains(got, "```") || strings.Contains(got, "| --- |") {
		t.Fatalf("table mode code header-only rendered = %q, want safe code table", got)
	}
}

func TestWeComMarkdownRendererTableModeCSVAndFileOverride(t *testing.T) {
	root := t.TempDir()
	table := "| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |"

	for _, mode := range []string{"csv", "file"} {
		renderer := NewWeComMarkdownRenderer(root)
		renderer.TableMode = mode
		rendered := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: table}})
		if len(rendered.Units) != 2 || rendered.Units[1].Action == nil {
			t.Fatalf("table mode %s units = %+v, want summary + file action", mode, rendered.Units)
		}
		if rendered.Units[1].Action.Type != "file" || !strings.HasSuffix(rendered.Units[1].Action.FileName, ".csv") {
			t.Fatalf("table mode %s action = %+v, want csv file", mode, rendered.Units[1].Action)
		}
		if got := rendered.Text(); strings.Contains(got, "```") || strings.Contains(got, "| --- |") {
			t.Fatalf("table mode %s rendered text = %q, want no code fence or markdown delimiter", mode, got)
		}
	}
}

func TestRenderWeComFinalMessageCanDisableIRRenderer(t *testing.T) {
	cfg := defaultWeComRuntimeConfig
	cfg.IRRendererEnabled = false
	table := "| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |"
	rendered := renderWeComFinalMessageWithConfig(table, t.TempDir(), cfg)
	if len(rendered.Units) != 1 || rendered.Units[0].Kind != "text" {
		t.Fatalf("legacy rendered units = %+v, want one text unit", rendered.Units)
	}
	if got := rendered.Text(); !strings.Contains(got, "| --- |") {
		t.Fatalf("legacy rendered text = %q, want original markdown table path", got)
	}
}

func TestWeComMarkdownRendererLargeTableCreatesCSVFile(t *testing.T) {
	root := t.TempDir()
	renderer := NewWeComMarkdownRenderer(root)
	renderer.TableMode = "csv"
	rows := []string{"| 区域 | 收入 |", "| --- | --- |"}
	for i := 0; i < wecomMediumTableMaxRows+1; i++ {
		rows = append(rows, "| 华东 | 10 |")
	}
	rendered := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: strings.Join(rows, "\n")}})
	if len(rendered.Units) != 2 || rendered.Units[1].Action == nil {
		t.Fatalf("large table units = %+v, want summary + file action", rendered.Units)
	}
	action := rendered.Units[1].Action
	if action.Type != "file" || !strings.HasSuffix(action.FileName, ".csv") {
		t.Fatalf("action = %+v, want csv file", action)
	}
	if action.ResolvedPath == "" || !filepath.IsAbs(action.ResolvedPath) {
		t.Fatalf("ResolvedPath = %q, want absolute path for upload", action.ResolvedPath)
	}
	content, err := os.ReadFile(filepath.Join(root, action.Path))
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	resolvedContent, err := os.ReadFile(action.ResolvedPath)
	if err != nil {
		t.Fatalf("read resolved csv: %v", err)
	}
	if string(resolvedContent) != string(content) {
		t.Fatalf("resolved csv content mismatch")
	}
	if !strings.Contains(string(content), "区域,收入") {
		t.Fatalf("csv content = %q, want header", string(content))
	}
}

func TestWeComMarkdownPreviewRendererDoesNotCreateCSVFile(t *testing.T) {
	root := t.TempDir()
	renderer := NewWeComMarkdownPreviewRenderer(root)
	renderer.TableMode = "csv"
	rows := []string{"| 区域 | 收入 |", "| --- | --- |"}
	for i := 0; i < wecomMediumTableMaxRows+1; i++ {
		rows = append(rows, "| 华东 | 10 |")
	}
	rendered := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockTable, Text: strings.Join(rows, "\n")}})
	if len(rendered.Units) != 1 || rendered.Units[0].Action != nil || !strings.Contains(rendered.Text(), "CSV") {
		t.Fatalf("preview rendered = %+v text=%q, want text-only CSV summary", rendered.Units, rendered.Text())
	}
	if _, err := os.Stat(filepath.Join(root, ".lumi-wecom", "exports")); !os.IsNotExist(err) {
		t.Fatalf("preview created exports dir err=%v, want no file side effect", err)
	}
}

func TestMarkdownParserJSONBlocks(t *testing.T) {
	parser := NewMarkdownParser()
	got := parser.PushFullText("```json\n{\"a\":1}\n```\n")
	if len(got) != 1 || got[0].Type != MarkdownBlockJSON {
		t.Fatalf("fenced json blocks = %+v, want json", got)
	}

	got = parser.PushFullText("{\n  \"a\": 1\n}\n\nnext\n")
	if len(got) != 2 || got[0].Type != MarkdownBlockJSON || got[1].Type != MarkdownBlockParagraph {
		t.Fatalf("bare json blocks = %+v, want json then paragraph", got)
	}

	if got := parser.PushFullText("{\n  \"a\":"); len(got) != 0 {
		t.Fatalf("incomplete bare json blocks = %+v, want none", got)
	}
	got = parser.Flush()
	if len(got) != 1 || got[0].Type != MarkdownBlockRaw {
		t.Fatalf("final incomplete json blocks = %+v, want raw fallback", got)
	}
}

func TestWeComMarkdownRendererJSON(t *testing.T) {
	root := t.TempDir()
	renderer := NewWeComMarkdownRenderer(root)
	short := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockJSON, Text: "{\"a\":1}"}})
	if got := short.Text(); !strings.Contains(got, "```json") || !strings.Contains(got, "{\"a\":1}") {
		t.Fatalf("short json rendered = %q, want json code block", got)
	}

	longBody := `{"items":[` + strings.Repeat(`{"name":"华东","value":123},`, 260) + `{"name":"end","value":0}]}`
	rendered := renderer.Render([]MarkdownBlock{{Type: MarkdownBlockJSON, Text: longBody}})
	if len(rendered.Units) != 1 || rendered.Units[0].Action == nil {
		t.Fatalf("long json units = %+v, want file action", rendered.Units)
	}
	action := rendered.Units[0].Action
	if action.Type != "file" || !strings.HasSuffix(action.FileName, ".json") {
		t.Fatalf("json action = %+v, want json file", action)
	}
	if action.ResolvedPath == "" || !filepath.IsAbs(action.ResolvedPath) {
		t.Fatalf("json ResolvedPath = %q, want absolute path for upload", action.ResolvedPath)
	}
	content, err := os.ReadFile(filepath.Join(root, action.Path))
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	if !strings.Contains(string(content), `"items"`) {
		t.Fatalf("json file content missing payload")
	}
}

func TestMarkdownParserLiveDefersIncompleteListHeadingAndQuote(t *testing.T) {
	parser := NewMarkdownParser()
	if got := parser.PushFullText("- "); len(got) != 0 {
		t.Fatalf("bare list marker blocks = %+v, want none", got)
	}
	got := parser.PushFullText("- 第一项\n")
	if len(got) != 1 || got[0].Type != MarkdownBlockList {
		t.Fatalf("complete list blocks = %+v, want list", got)
	}

	if got := parser.PushFullText("## 标题"); len(got) != 0 {
		t.Fatalf("incomplete heading blocks = %+v, want none", got)
	}
	got = parser.PushFullText("## 标题\n正文")
	if len(got) != 2 || got[0].Type != MarkdownBlockHeading || got[1].Type != MarkdownBlockParagraph {
		t.Fatalf("heading blocks = %+v, want heading then paragraph", got)
	}

	if got := parser.PushFullText("> 引用"); len(got) != 0 {
		t.Fatalf("incomplete quote blocks = %+v, want none", got)
	}
	got = parser.PushFullText("> 引用\n\n正文\n")
	if len(got) != 2 || got[0].Type != MarkdownBlockQuote || got[1].Type != MarkdownBlockParagraph {
		t.Fatalf("quote blocks = %+v, want quote then paragraph", got)
	}
}

func TestMarkdownParserTableFalsePositiveSamples(t *testing.T) {
	parser := NewMarkdownParser()
	samples := []string{
		"这里有 A | B 但不是表格\n",
		"路径 /tmp/a|b 也不是表格\n",
		"比率 10 | 20 | 30 缺少 delimiter\n",
		"> 引用里 a | b 不应当误判为表格\n",
		"[重点] 本周完成\n",
		"{待确认} 正文\n",
	}
	for _, sample := range samples {
		got := parser.PushFullText(sample)
		if len(got) != 1 || got[0].Type == MarkdownBlockTable || got[0].Type == MarkdownBlockJSON {
			t.Fatalf("sample %q blocks = %+v, want non-table non-json", sample, got)
		}
	}
}
