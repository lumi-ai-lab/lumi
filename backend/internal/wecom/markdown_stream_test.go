package wecom

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStabilizeWeComMarkdownStreamPlainText(t *testing.T) {
	got := stabilizeWeComMarkdownStream("hello wor")
	if got != "hello wor" {
		t.Fatalf("stabilizeWeComMarkdownStream() = %q, want plain text to keep streaming", got)
	}
}

func TestStabilizeWeComMarkdownStreamDefersIncompleteListItem(t *testing.T) {
	if got := stabilizeWeComMarkdownStream("- ✅ 毛利分析"); got != "" {
		t.Fatalf("incomplete list = %q, want empty", got)
	}
	if got := stabilizeWeComMarkdownStream("- ✅ 毛利分析\n"); got != "- ✅ 毛利分析" {
		t.Fatalf("complete list = %q, want list item", got)
	}
}

func TestStabilizeWeComMarkdownStreamTableBoundaries(t *testing.T) {
	if got := stabilizeWeComMarkdownStream("| 项目 | 内容 |"); got != "" {
		t.Fatalf("table header only = %q, want empty", got)
	}
	headerAndDelimiter := "| 项目 | 内容 |\n| --- | --- |"
	if got := stabilizeWeComMarkdownStream(headerAndDelimiter); got != headerAndDelimiter {
		t.Fatalf("header + delimiter = %q, want %q", got, headerAndDelimiter)
	}
	withIncompleteRow := headerAndDelimiter + "\n| 毛利 |"
	if got := stabilizeWeComMarkdownStream(withIncompleteRow); got != headerAndDelimiter {
		t.Fatalf("incomplete row = %q, want %q", got, headerAndDelimiter)
	}
	withCompleteRow := headerAndDelimiter + "\n| 毛利 | 10 | \n"
	want := headerAndDelimiter + "\n| 毛利 | 10 |"
	if got := stabilizeWeComMarkdownStream(withCompleteRow); got != want {
		t.Fatalf("complete row = %q, want %q", got, want)
	}
}

func TestStabilizeWeComMarkdownStreamHeadingWaitsForFollowingStructure(t *testing.T) {
	content := "## 结论\n| 项目 | 内容 |"
	if got := stabilizeWeComMarkdownStream(content); got != "" {
		t.Fatalf("heading before incomplete table = %q, want empty", got)
	}
	complete := content + "\n| --- | --- |"
	want := "## 结论\n\n| 项目 | 内容 |\n| --- | --- |"
	if got := stabilizeWeComMarkdownStream(complete); got != want {
		t.Fatalf("heading before complete table = %q, want %q", got, want)
	}
}

func TestStabilizeWeComMarkdownStreamDefersUnclosedFence(t *testing.T) {
	content := "before\n\n```json\n{\"a\":1}"
	if got := stabilizeWeComMarkdownStream(content); got != "before" {
		t.Fatalf("unclosed fence = %q, want prefix only", got)
	}
	closed := content + "\n```"
	if got := stabilizeWeComMarkdownStream(closed); got != closed {
		t.Fatalf("closed fence = %q, want %q", got, closed)
	}
}

func TestNormalizeWeComMarkdownRepairsInlineListAndTableSpacing(t *testing.T) {
	got := normalizeWeComMarkdown("变化；- ✅ 毛利分析")
	if want := "变化；\n- ✅ 毛利分析"; got != want {
		t.Fatalf("inline list normalized = %q, want %q", got, want)
	}

	got = normalizeWeComMarkdown("📌 口径说明| 项目 | 内容 |")
	want := "📌 口径说明\n\n| 项目 | 内容 |"
	if got != want {
		t.Fatalf("inline table normalized = %q, want %q", got, want)
	}

	got = normalizeWeComMarkdown("📌 口径说明\n| 项目 | 内容 |\n| --- | --- |")
	want = "📌 口径说明\n\n| 项目 | 内容 |\n| --- | --- |"
	if got != want {
		t.Fatalf("table spacing normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownRepairsSplitTableFirstCell(t *testing.T) {
	content := strings.Join([]string{
		"| 区域 | 销售额 |",
		"| --- | --- |",
		"|",
		"🟢 粤东区",
		"| 2,807 |",
	}, "\n")
	want := strings.Join([]string{
		"| 区域 | 销售额 |",
		"| --- | --- |",
		"| 🟢 粤东区 | 2,807 |",
	}, "\n")
	if got := normalizeWeComMarkdown(content); got != want {
		t.Fatalf("split table first cell normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownRepairsSplitRiskTableFirstCell(t *testing.T) {
	content := strings.Join([]string{
		"| 区域 | 时段折扣率 | 促销折扣率 |",
		"| --- | --- | --- |",
		"| 成都区",
		"🔴",
		"| 13.87% | 8.26% |",
	}, "\n")
	want := strings.Join([]string{
		"| 区域 | 时段折扣率 | 促销折扣率 |",
		"| --- | --- | --- |",
		"| 成都区 🔴 | 13.87% | 8.26% |",
	}, "\n")
	if got := normalizeWeComMarkdown(content); got != want {
		t.Fatalf("split risk table row normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownRepairsSplitBoldOrderedListItem(t *testing.T) {
	got := normalizeWeComMarkdown("1. **\n🔴 利润额和毛利率同比下降**")
	want := "1. **🔴 利润额和毛利率同比下降**"
	if got != want {
		t.Fatalf("split bold ordered item normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownConvertsTSVTable(t *testing.T) {
	content := strings.Join([]string{
		"区域\t时段折扣率\t促销折扣率",
		"成都区\t13.87%\t8.26%",
	}, "\n")
	want := strings.Join([]string{
		"| 区域 | 时段折扣率 | 促销折扣率 |",
		"| --- | --- | --- |",
		"| 成都区 | 13.87% | 8.26% |",
	}, "\n")
	if got := normalizeWeComMarkdown(content); got != want {
		t.Fatalf("tsv table normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownRepairsMixedBrokenTable(t *testing.T) {
	content := strings.Join([]string{
		"| 区域 | 时段折扣率 | 促销折扣率 |",
		"| --- | --- | --- |",
		"| 成都区",
		"🔴",
		"| 13.87% | 8.26% |",
		"| 武汉区 🟡 | 10.10% | 6.22% |",
		"| 杭州区",
		"🟢",
		"| 9.12% | 4.55% |",
	}, "\n")
	got := normalizeWeComMarkdown(content)
	lines := strings.Split(got, "\n")
	if len(lines) != 5 {
		t.Fatalf("normalized line count = %d, want 5: %q", len(lines), got)
	}
	wantColumns := wecomMarkdownTableCellCount(lines[0])
	for i, line := range lines {
		if wecomMarkdownRiskOnlyRE.MatchString(line) {
			t.Fatalf("line %d is standalone risk emoji after normalization: %q", i, got)
		}
		if i == 1 {
			continue
		}
		if gotColumns := wecomMarkdownTableCellCount(line); gotColumns != wantColumns {
			t.Fatalf("line %d columns = %d, want %d: %q", i, gotColumns, wantColumns, got)
		}
	}
}

func TestNormalizeWeComMarkdownLeavesValidTableDelimiterAlone(t *testing.T) {
	content := strings.Join([]string{
		"| 区域 | 时段折扣率 | 促销折扣率 |",
		"| --- | --- | --- |",
		"| 成都区 🔴 | 13.87% | 8.26% |",
		"| 武汉区 🟡 | 10.10% | 6.22% |",
	}, "\n")
	if got := normalizeWeComMarkdown(content); got != content {
		t.Fatalf("valid markdown table changed = %q, want %q", got, content)
	}
}

func TestNormalizeWeComMarkdownRepairsInlineHorizontalRule(t *testing.T) {
	got := normalizeWeComMarkdown("动作---")
	if want := "动作\n\n---"; got != want {
		t.Fatalf("inline horizontal rule normalized = %q, want %q", got, want)
	}

	table := "| 项目 | 内容 |\n| --- | --- |"
	if got := normalizeWeComMarkdown(table); got != table {
		t.Fatalf("table delimiter changed = %q, want %q", got, table)
	}
}

func TestNormalizeWeComMarkdownRepairsInlineHeadingsAndRiskEmoji(t *testing.T) {
	content := strings.Join([]string{
		"折扣侵蚀风险判断🔴 折扣可能侵蚀毛利空间：",
		"📦 进货与订购### 投入转化效率分析",
		"行动建议### 行动一：运营中心直管紧急利润下钻",
	}, "\n")
	want := strings.Join([]string{
		"折扣侵蚀风险判断",
		"",
		"🔴 折扣可能侵蚀毛利空间：",
		"📦 进货与订购",
		"",
		"### 投入转化效率分析",
		"",
		"行动建议",
		"",
		"### 行动一：运营中心直管紧急利润下钻",
	}, "\n")
	if got := normalizeWeComMarkdown(content); got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownPreservesFencedCodeContent(t *testing.T) {
	content := strings.Join([]string{
		"外部x### y",
		"变化；- item",
		"说明| A | B |",
		"",
		"```text",
		"x### y",
		"变化；- item",
		"说明| A | B |",
		"```",
		"",
		"外部说明| A | B |",
	}, "\n")
	want := strings.Join([]string{
		"外部x",
		"",
		"### y",
		"",
		"变化；",
		"- item",
		"说明",
		"",
		"| A | B |",
		"",
		"```text",
		"x### y",
		"变化；- item",
		"说明| A | B |",
		"```",
		"",
		"外部说明",
		"",
		"| A | B |",
	}, "\n")
	if got := normalizeWeComMarkdownStreamInput(content); got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizeWeComMarkdownMergesBareOrderedListMarker(t *testing.T) {
	got := normalizeWeComMarkdown("8. 已完成\n9. \n### 行动建议")
	if strings.Contains(got, "\n9.\n") || strings.Contains(got, "\n9. \n") {
		t.Fatalf("bare ordered marker was not merged: %q", got)
	}
	if !strings.Contains(got, "9. ### 行动建议") {
		t.Fatalf("normalized marker = %q, want merged heading text", got)
	}
}

func TestNormalizeWeComMarkdownLeavesValidBlocksAlone(t *testing.T) {
	content := "变化；\n- ✅ 毛利分析\n\n| 项目 | 内容 |\n| --- | --- |"
	if got := normalizeWeComMarkdown(content); got != content {
		t.Fatalf("valid markdown changed = %q, want %q", got, content)
	}
}

func TestSplitWeComMarkdownMessagesShortAndLongText(t *testing.T) {
	short := strings.Repeat("一", 1000)
	parts := splitWeComMarkdownMessages(short, wecomMarkdownSendMaxBytes)
	if len(parts) != 1 || parts[0] != short {
		t.Fatalf("short parts = %d, want one unchanged part", len(parts))
	}

	long := strings.Repeat("长文内容。", 500)
	parts = splitWeComMarkdownMessages(long, wecomMarkdownSendMaxBytes)
	if len(parts) != 2 {
		t.Fatalf("long parts = %d, want 2", len(parts))
	}
	for _, part := range parts {
		if len(part) > wecomMarkdownSendMaxBytes {
			t.Fatalf("part bytes = %d, want <= %d", len(part), wecomMarkdownSendMaxBytes)
		}
		if !utf8.ValidString(part) {
			t.Fatalf("part is invalid utf8: %q", part)
		}
	}
}

func TestSplitWeComMarkdownMessagesSmallLimitKeepsUTF8Valid(t *testing.T) {
	parts := splitWeComMarkdownMessages("你好", 1)
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want one part per rune", len(parts))
	}
	for _, part := range parts {
		if !utf8.ValidString(part) {
			t.Fatalf("part is invalid utf8: %q", part)
		}
	}
	if strings.Join(parts, "") != "你好" {
		t.Fatalf("parts = %q, want reconstruct original text", parts)
	}
}

func TestSplitWeComMarkdownMessagesRepeatsOversizedTableHeader(t *testing.T) {
	rows := make([]string, 0, 20)
	rows = append(rows, "| 区域 | 值 | 变化 |", "| --- | ---: | ---: |")
	for i := 0; i < 20; i++ {
		rows = append(rows, "| 澳门区 | 2.22 | -8.26% |")
	}
	parts := splitWeComMarkdownMessages(strings.Join(rows, "\n"), 130)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want oversized table split", len(parts))
	}
	for _, part := range parts {
		if len(part) > 130 {
			t.Fatalf("table part bytes = %d, want <= 130: %q", len(part), part)
		}
		if !strings.HasPrefix(part, "| 区域 | 值 | 变化 |\n| --- | ---: | ---: |\n") {
			t.Fatalf("table part missing header/delimiter: %q", part)
		}
	}
}

func TestSplitWeComMarkdownMessagesOversizedTableDoesNotEmitEmptyBodyPart(t *testing.T) {
	content := strings.Join([]string{
		"| H | V |",
		"| --- | --- |",
		"| row | " + strings.Repeat("x", 40) + " |",
	}, "\n")
	parts := splitWeComMarkdownMessages(content, 30)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want split long row", len(parts))
	}
	for _, part := range parts {
		lines := strings.Split(part, "\n")
		if len(lines) < 3 {
			t.Fatalf("table part has no body row content: %q", part)
		}
		if lines[0] != "| H | V |" || lines[1] != "| --- | --- |" {
			t.Fatalf("table part missing header/delimiter: %q", part)
		}
		if strings.TrimSpace(lines[2]) == "" {
			t.Fatalf("table part has empty body row content: %q", part)
		}
		if len(part) > 30 {
			t.Fatalf("table part bytes = %d, want <= 30: %q", len(part), part)
		}
	}
}

func TestSplitWeComMarkdownMessagesKeepsFencedCodeClosed(t *testing.T) {
	body := strings.Repeat("fmt.Println(\"hello\")\n", 20)
	parts := splitWeComMarkdownMessages("```go\n"+body+"```", 120)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want code split", len(parts))
	}
	for _, part := range parts {
		if len(part) > 120 {
			t.Fatalf("code part bytes = %d, want <= 120", len(part))
		}
		if !strings.HasPrefix(part, "```go\n") || !strings.HasSuffix(part, "\n```") {
			t.Fatalf("code part is not closed: %q", part)
		}
	}
}

func TestSplitWeComMarkdownMessagesPreservesFencedCodeBody(t *testing.T) {
	body := strings.Join([]string{
		"func main() {",
		"    fmt.Println(\"hello\")",
		"",
		"    fmt.Println(\"again\")",
		"}",
		"    // keep trailing indentation",
	}, "\n")
	parts := splitWeComMarkdownMessages("```go\n"+body+"\n```", 70)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want code split", len(parts))
	}
	var rebuilt strings.Builder
	for _, part := range parts {
		if !strings.HasPrefix(part, "```go\n") || !strings.HasSuffix(part, "\n```") {
			t.Fatalf("code part is not fenced: %q", part)
		}
		rebuilt.WriteString(strings.TrimSuffix(strings.TrimPrefix(part, "```go\n"), "\n```"))
	}
	if rebuilt.String() != body {
		t.Fatalf("rebuilt code body = %q, want %q", rebuilt.String(), body)
	}
}

func TestSplitWeComMarkdownMessagesContinuationPrefixOnlyFirstPart(t *testing.T) {
	content := "续上：\n\n" + strings.Repeat("后续内容。", 80)
	parts := splitWeComMarkdownMessages(content, 180)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want continuation split", len(parts))
	}
	for i, part := range parts {
		hasPrefix := strings.Contains(part, "续上：")
		if i == 0 && !hasPrefix {
			t.Fatalf("first part missing continuation prefix: %q", part)
		}
		if i == 0 && !strings.Contains(part, "后续内容") {
			t.Fatalf("first part contains only continuation prefix: %q", part)
		}
		if i > 0 && hasPrefix {
			t.Fatalf("part %d repeated continuation prefix: %q", i, part)
		}
	}
}

func TestSplitWeComMarkdownMessagesContinuationPrefixIncludesFirstTableChunk(t *testing.T) {
	rows := []string{
		"续上：",
		"",
		"| 区域 | 销售额 | 同比 |",
		"| --- | ---: | ---: |",
	}
	for i := 0; i < 12; i++ {
		rows = append(rows, "| 华南大区 | 12345 | 9.8% |")
	}
	parts := splitWeComMarkdownMessages(strings.Join(rows, "\n"), 160)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want continuation table split", len(parts))
	}
	if !strings.HasPrefix(parts[0], "续上：\n\n| 区域 | 销售额 | 同比 |\n| --- | ---: | ---: |\n") {
		t.Fatalf("first part did not include table header/delimiter: %q", parts[0])
	}
	if !strings.Contains(parts[0], "| 华南大区 |") {
		t.Fatalf("first part did not include first table row: %q", parts[0])
	}
	for i, part := range parts {
		if len(part) > 160 {
			t.Fatalf("part %d bytes = %d, want <= 160: %q", i, len(part), part)
		}
		if i > 0 && !strings.HasPrefix(part, "| 区域 | 销售额 | 同比 |\n| --- | ---: | ---: |\n") {
			t.Fatalf("table continuation part missing header/delimiter: %q", part)
		}
	}
}

func TestSplitWeComLongReplyShortText(t *testing.T) {
	content := "短回答"
	preview, remaining := splitWeComLongReply(content, 100)
	if preview != content || remaining != "" {
		t.Fatalf("split = %q / %q, want full preview", preview, remaining)
	}
}

func TestSplitWeComLongReplyUsesByteLimit(t *testing.T) {
	content := "你好你好你好"
	preview, remaining := splitWeComLongReply(content, 3)
	if preview+remaining != content {
		t.Fatalf("split did not preserve content: %q + %q", preview, remaining)
	}
	if !utf8.ValidString(preview) || !utf8.ValidString(remaining) {
		t.Fatalf("split produced invalid utf8: %q / %q", preview, remaining)
	}
	if got := utf8.RuneCountInString(preview); got != 1 {
		t.Fatalf("preview runes = %d, want 1", got)
	}
	if len(preview) != 3 {
		t.Fatalf("preview bytes = %d, want 3 for one Chinese rune", len(preview))
	}
}

func TestSplitWeComLongReplyDoesNotCutChineseAtByteThreshold(t *testing.T) {
	content := strings.Repeat("中", 9000)
	preview, remaining := splitWeComLongReply(content, 9000)
	if preview+remaining != content {
		t.Fatalf("split did not preserve content")
	}
	if len(preview) > 9000 {
		t.Fatalf("preview bytes = %d, want <= 9000", len(preview))
	}
	if got := utf8.RuneCountInString(preview); got != 3000 {
		t.Fatalf("preview runes = %d, want 3000", got)
	}
}

func TestSplitWeComLongReplyPrefersParagraphAndHeadingBoundaries(t *testing.T) {
	content := strings.Join([]string{
		"第一段内容",
		"",
		"第二段内容",
		"",
		"## 后续标题",
		"后续内容",
	}, "\n")
	preview, remaining := splitWeComLongReply(content, len("第一段内容\n\n第二段内容\n"))
	if preview+remaining != content {
		t.Fatalf("split did not preserve content: %q + %q", preview, remaining)
	}
	if preview != "第一段内容\n\n" {
		t.Fatalf("preview = %q, want first paragraph", preview)
	}

	preview, remaining = splitWeComLongReply(content, len("第一段内容\n\n第二段内容\n\n## 后续"))
	if preview+remaining != content {
		t.Fatalf("heading split did not preserve content: %q + %q", preview, remaining)
	}
	if preview != "第一段内容\n\n第二段内容\n\n" {
		t.Fatalf("preview before heading = %q", preview)
	}
}

func TestSplitWeComLongReplyAvoidsMarkdownBlocks(t *testing.T) {
	content := "intro\n\n| 项目 | 内容 |\n| --- | --- |\n| 毛利 | 10 |\n\noutro"
	limitInsideTable := len("intro\n\n| 项目 | 内容 |\n| --- |")
	preview, remaining := splitWeComLongReply(content, limitInsideTable)
	if preview+remaining != content {
		t.Fatalf("split did not preserve table content: %q + %q", preview, remaining)
	}
	if preview != "intro\n\n" {
		t.Fatalf("table preview = %q, want prefix before table", preview)
	}

	content = "intro\n\n```json\n{\"a\":1}\n```\n\noutro"
	limitInsideFence := len("intro\n\n```json\n{\"a\"")
	preview, remaining = splitWeComLongReply(content, limitInsideFence)
	if preview+remaining != content {
		t.Fatalf("split did not preserve fence content: %q + %q", preview, remaining)
	}
	if preview != "intro\n\n" {
		t.Fatalf("fence preview = %q, want prefix before fence", preview)
	}
}
