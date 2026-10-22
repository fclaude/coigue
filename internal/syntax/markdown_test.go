package syntax

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func lexMarkdown(text string) [][]Style {
	return highlightMarkdown(splitLines(text))
}

func markdownSpan(t *testing.T, text, needle string, style Style) {
	t.Helper()
	start := utf8.RuneCountInString(text[:strings.Index(text, needle)])
	n := utf8.RuneCountInString(needle)
	if got := lexMarkdown(text)[0][start : start+n]; !allStyle(got, style) {
		t.Errorf("%s: %s, want all %v", needle, styleCodes(got), style)
	}
}

// Rust: syntax::markdown::tests::extensions_headings_and_plain_text
func TestExtensionsHeadingsAndPlainText(t *testing.T) {
	for _, extension := range []string{"md", "MD", "markdown", "MarkDown"} {
		lines := splitLines("# Heading")
		got := ForPath("file."+extension, lines)
		if want := [][]Style{repeated(Heading, 9)}; !reflect.DeepEqual(got, want) {
			t.Errorf("file.%s = %v, want %v", extension, got, want)
		}
	}
	styles := lexMarkdown("# Title\n\nSubtitle\n===\n\n---\n####### plain\n#hashtag\nplain_text_here\nordinary prose")
	for _, row := range []int{0, 2, 3} {
		if !allStyle(styles[row], Heading) {
			t.Errorf("row %d = %s", row, styleCodes(styles[row]))
		}
	}
	if !allStyle(styles[5], Macro) {
		t.Errorf("row 5 = %s", styleCodes(styles[5]))
	}
	for _, row := range []int{6, 7, 8, 9} {
		if !allStyle(styles[row], Plain) {
			t.Errorf("row %d = %s", row, styleCodes(styles[row]))
		}
	}
	styles = lexMarkdown("text\n-\n\n* * *\n\n---")
	if !reflect.DeepEqual(styles[0], repeated(Heading, 4)) {
		t.Errorf("row 0 = %s", styleCodes(styles[0]))
	}
	if !reflect.DeepEqual(styles[1], []Style{Heading}) {
		t.Errorf("row 1 = %s", styleCodes(styles[1]))
	}
	if !allStyle(styles[3], Macro) {
		t.Errorf("row 3 = %s", styleCodes(styles[3]))
	}
	if !allStyle(styles[5], Macro) {
		t.Errorf("row 5 = %s", styleCodes(styles[5]))
	}
}

// Rust: syntax::markdown::tests::inline_markup_links_and_unicode_offsets
func TestInlineMarkupLinksAndUnicodeOffsets(t *testing.T) {
	text := "界 *café* **bold** _italic_ __strong__ `**code**` ``a ` b`` [link](path_(one)) ![image](pic.png) [ref][id] <https://example.com>"
	for _, c := range []struct {
		needle string
		style  Style
	}{
		{"界", Plain},
		{"*café*", Emphasis},
		{"**bold**", Strong},
		{"_italic_", Emphasis},
		{"__strong__", Strong},
		{"`**code**`", String},
		{"``a ` b``", String},
		{"[link]", Link},
		{"(path_(one))", String},
		{"![image]", Link},
		{"[ref]", Link},
		{"[id]", String},
		{"<https://example.com>", Link},
	} {
		markdownSpan(t, text, c.needle, c.style)
	}
	for _, text := range []string{
		"unmatched *markup",
		"escaped \\*literal*",
		"snake_case_word",
		"a_b_c",
		"[unclosed](link",
		"`unclosed",
		"text * spaced *",
	} {
		if got := lexMarkdown(text)[0]; !allStyle(got, Plain) {
			t.Errorf("%s: %s", text, styleCodes(got))
		}
	}
	markdownSpan(t, "[id]: https://example.com", "[id]:", Link)
	markdownSpan(t, "[id]: https://example.com", "https://example.com", String)
}

// Rust: syntax::markdown::tests::lists_quotes_tasks_and_indented_code
func TestListsQuotesTasksAndIndentedCode(t *testing.T) {
	for _, c := range []struct {
		text, needle string
		style        Style
	}{
		{"- [x] done", "-", Macro},
		{"- [x] done", "[x]", Constant},
		{"12. item", "12.", Macro},
		{"> > # Title", ">", Macro},
		{"> > # Title", "# Title", Heading},
		{"    *code*", "*code*", String},
	} {
		markdownSpan(t, c.text, c.needle, c.style)
	}
	if got := lexMarkdown("1.5 and -not-a-list")[0]; !allStyle(got, Plain) {
		t.Errorf("1.5 and -not-a-list: %s", styleCodes(got))
	}
}

// Rust: syntax::markdown::tests::fenced_code_reuses_rust_and_go_multiline_lexers
func TestFencedCodeReusesRustAndGoMultilineLexers(t *testing.T) {
	styles := lexMarkdown("````rust\n/* comment\nstill */ fn main() {}\n```\n```` trailing\n~~~~\n`````\n# Heading\n~~~go\npackage main\ns := `raw\nfunc`\n~~~\nordinary prose")
	if !allStyle(styles[0], Macro) {
		t.Errorf("row 0 = %s", styleCodes(styles[0]))
	}
	if !allStyle(styles[1], Comment) {
		t.Errorf("row 1 = %s", styleCodes(styles[1]))
	}
	if styles[2][9] != Keyword {
		t.Errorf("styles[2][9] = %v", styles[2][9])
	}
	if !allStyle(styles[6], Macro) {
		t.Errorf("row 6 = %s", styleCodes(styles[6]))
	}
	if !allStyle(styles[7], Heading) {
		t.Errorf("row 7 = %s", styleCodes(styles[7]))
	}
	if styles[9][0] != Keyword {
		t.Errorf("styles[9][0] = %v", styles[9][0])
	}
	if !allStyle(styles[11], String) {
		t.Errorf("row 11 = %s", styleCodes(styles[11]))
	}
	if !allStyle(styles[13], Plain) {
		t.Errorf("row 13 = %s", styleCodes(styles[13]))
	}
}

// Rust: syntax::markdown::tests::unknown_and_unclosed_fences_hide_markdown_markup
func TestUnknownAndUnclosedFencesHideMarkdownMarkup(t *testing.T) {
	text := "  ~~~python\r\n# code\r\n**code**\r\n  ~~~~\r\n# Heading\r\n```\r\n*code*"
	styles := lexMarkdown(text)
	for _, row := range []int{1, 2, 6} {
		if !allStyle(styles[row], String) {
			t.Errorf("row %d = %s", row, styleCodes(styles[row]))
		}
	}
	for _, row := range []int{0, 3, 5} {
		if !allStyle(styles[row], Macro) {
			t.Errorf("row %d = %s", row, styleCodes(styles[row]))
		}
	}
	if styles[4][0] != Heading {
		t.Errorf("styles[4][0] = %v", styles[4][0])
	}
	if got := lexMarkdown("```bad`info")[0]; !allStyle(got, Plain) {
		t.Errorf("```bad`info: %s", styleCodes(got))
	}
}

// Rust: syntax::markdown::tests::html_comments_continue_across_lines_and_resume_inline_markup
func TestHTMLCommentsContinueAcrossLinesAndResumeInlineMarkup(t *testing.T) {
	styles := lexMarkdown("<!-- # hidden\n   **hidden**\n--> *visible*\n`<!--` **visible**\ntext <!-- hidden --> text")
	for _, row := range []int{0, 1} {
		if !allStyle(styles[row], Comment) {
			t.Errorf("row %d = %s", row, styleCodes(styles[row]))
		}
	}
	if !allStyle(styles[2][:3], Comment) {
		t.Errorf("row 2 = %s", styleCodes(styles[2]))
	}
	if !allStyle(styles[2][4:], Emphasis) {
		t.Errorf("row 2 = %s", styleCodes(styles[2]))
	}
	if !allStyle(styles[3][:6], String) {
		t.Errorf("row 3 = %s", styleCodes(styles[3]))
	}
	if !allStyle(styles[3][7:], Strong) {
		t.Errorf("row 3 = %s", styleCodes(styles[3]))
	}
	if !allStyle(styles[4][5:20], Comment) {
		t.Errorf("row 4 = %s", styleCodes(styles[4]))
	}
	if styles[4][21] != Plain {
		t.Errorf("styles[4][21] = %v", styles[4][21])
	}
}

// The expected rows below were produced by running markdown::highlight from
// src/syntax/markdown.rs; they pin behaviour that is easy to "fix" by accident.
func TestMarkdownEdgeCases(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"Title\r\n===\r\n", "hhhhhh|hhhh|"},
		{"text\n-", "hhhh|h"},
		{"text\n\n---", "....||mmm"},
		{"- a\n---", "m..|mmm"},
		{"# h\n---", "hhh|mmm"},
		{"[ref]: x\n---", "iiiiiiss|mmm"},
		{"para\n    cont", "....|ssssssss"},
		{"a  \n---", "hhh|hhh"},
		{"```rust\nfn x() {}\n```x", "mmmmmmm|kk..pp.pp|ppp."},
		{"``` rust extra\nfn x() {}\n```", "mmmmmmmmmmmmmm|kk..pp.pp|mmm"},
		{"~~~\ncode\n```", "mmm|ssss|sss"},
		{"```\ncode\n   ```\nmore", "mmm|ssss|mmmmmm|...."},
		{"```\ncode\n    ```\nmore", "mmm|ssss|sssssss|ssss"},
		{"```\r\ncode\r\n```\r\n", "mmmm|sssss|mmmm|"},
		{"```rs\n'a 'b\n```", "mmmmm|ll.ll|mmm"},
		{"> a\n===", "m..|..."},
		{"- [ ] t", "m.ooo.."},
		{"1.\ta", "mm.."},
		{"1234567890. a", "............."},
		{"- ---", "mmmmm"},
		{"    # not heading", "sssssssssssssssss"},
		{"\t# tab", "ssssss"},
		{"  \tcode", "......."},
		{"[a](b(c))", "iiissssss"},
		{"[a](b", "....."},
		{"[a][b]", "iiisss"},
		{"![a](b)", "iiiisss"},
		{"[a]: <u> \"t\"", "iiiissssssss"},
		{"[a\\]: b", "......."},
		{"*a **b** c*", "eeeeeeeeeee"},
		{"***a***", "bbbbbbb"},
		{"_a_b_", "eeeee"},
		{"*a*b*", "eee.."},
		{"__a__b", "......"},
		{"*\u00a0a*", "m..."},
		{"**a*", "...."},
		{"<https://a.b>", "iiiiiiiiiiiii"},
		{"<a@b.c>", "iiiiiii"},
		{"<https://a b>", "............."},
		{"`a``b`", "ssssss"},
		{"``a ` b``", "sssssssss"},
		{"\\*x\\*", "....."},
		{"*a\\*b*", "eeeeee"},
		{"<!-- a\n# H\n-->\n# H", "cccccc|ccc|ccc|hhh"},
		{"x <!-- y --> *z*", "..cccccccccc.eee"},
		{"<!-->", "ccccc"},
		{"<!---->", "ccccccc"},
		{"-->", "..."},
		{"- <!-- a\n  b -->\n- c", "m.cccccc|ccccccc|m.."},
		{"text\n<!-- x -->\n---", "....|hhhhhhhhhh|hhh"},
		{"# *a* `b`", "hheeehsss"},
		{"> # h", "m.hhh"},
		{"####### x", "........."},
		{"# 界 *é*", "hhhheee"},
		{"*e\u0301*e\u0301", "eeee.."},
		{"界_a_界", "....."},
	} {
		styles := lexMarkdown(c.text)
		rows := make([]string, len(styles))
		for i, row := range styles {
			rows[i] = styleCodes(row)
		}
		if got := strings.Join(rows, "|"); got != c.want {
			t.Errorf("markdown(%q) = %s, want %s", c.text, got, c.want)
		}
	}
}
