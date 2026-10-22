package syntax

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// splitLines is crate::buffer::Buffer::new(text).lines: split on '\n' only, so
// a '\r' stays at the end of its line.
func splitLines(text string) [][]rune {
	parts := strings.Split(text, "\n")
	lines := make([][]rune, len(parts))
	for i, part := range parts {
		lines[i] = []rune(part)
	}
	return lines
}

// lex is the lex helper of syntax.rs's tests: the Rust lexer over text.
func lex(text string) [][]Style {
	return Rust(splitLines(text))
}

func allStyle(styles []Style, want Style) bool {
	for _, s := range styles {
		if s != want {
			return false
		}
	}
	return true
}

func repeated(style Style, n int) []Style {
	styles := make([]Style, n)
	for i := range styles {
		styles[i] = style
	}
	return styles
}

// styleCodes spells a row of styles with one letter per character, the format
// of the golden file: . plain, k keyword, c comment, s string, n number,
// m macro, p punctuation, l lifetime, t type, f function, v variable,
// r property, o constant, a namespace, h heading, e emphasis, b strong, i link.
func styleCodes(styles []Style) string {
	codes := map[Style]byte{
		Plain: '.', Keyword: 'k', Comment: 'c', String: 's', Number: 'n', Macro: 'm',
		Punctuation: 'p', Lifetime: 'l', Type: 't', Function: 'f', Variable: 'v',
		Property: 'r', Constant: 'o', Namespace: 'a', Heading: 'h', Emphasis: 'e',
		Strong: 'b', Link: 'i',
	}
	out := make([]byte, len(styles))
	for i, s := range styles {
		out[i] = codes[s]
	}
	return string(out)
}

// Rust: syntax::tests::multiline_nested_comments_and_edit
func TestMultilineNestedCommentsAndEdit(t *testing.T) {
	s := "/* outer\n/* nested */ still\n*/ fn main() {}"
	styles := lex(s)
	if !allStyle(styles[0], Comment) {
		t.Errorf("row 0 = %v", styleCodes(styles[0]))
	}
	if !allStyle(styles[1], Comment) {
		t.Errorf("row 1 = %v", styleCodes(styles[1]))
	}
	if styles[2][3] != Keyword {
		t.Errorf("styles[2][3] = %v, want Keyword", styles[2][3])
	}
	edited := lex(strings.Replace(s, "/* outer", "// outer", 1))
	if edited[1][13] != Plain {
		t.Errorf("edited[1][13] = %v, want Plain", edited[1][13])
	}
}

// Rust: syntax::tests::quotes_raw_strings_and_escapes
func TestQuotesRawStringsAndEscapes(t *testing.T) {
	styles := lex("br##\"a\n\"# /*\n\"## fn\n\"a\\\"b\nend\" let")
	if !allStyle(styles[1], String) {
		t.Errorf("row 1 = %v", styleCodes(styles[1]))
	}
	if styles[2][4] != Keyword {
		t.Errorf("styles[2][4] = %v, want Keyword", styles[2][4])
	}
	if !allStyle(styles[3], String) {
		t.Errorf("row 3 = %v", styleCodes(styles[3]))
	}
	if styles[4][5] != Keyword {
		t.Errorf("styles[4][5] = %v, want Keyword", styles[4][5])
	}
	if got := lex("c\"hello\"")[0]; !reflect.DeepEqual(got, repeated(String, 8)) {
		t.Errorf("c\"hello\" = %v", styleCodes(got))
	}
}

// Rust: syntax::tests::characters_lifetimes_unicode_and_numbers
func TestCharactersLifetimesUnicodeAndNumbers(t *testing.T) {
	s := "'a 'static '界' '\\n' b'X' '\\u{1f980}' 0xff_u8 1.2e-3 println! r#type"
	styles := lex(s)
	for _, c := range []struct {
		needle   string
		expected Style
	}{
		{"'a", Lifetime},
		{"'static", Lifetime},
		{"'界'", String},
		{"'\\n'", String},
		{"b'X'", String},
		{"'\\u{1f980}'", String},
		{"0xff_u8", Number},
		{"1.2e-3", Number},
		{"println!", Macro},
		{"r#type", Plain},
	} {
		start := utf8.RuneCountInString(s[:strings.Index(s, c.needle)])
		n := utf8.RuneCountInString(c.needle)
		if !allStyle(styles[0][start:start+n], c.expected) {
			t.Errorf("%s = %v, want %v", c.needle, styleCodes(styles[0][start:start+n]), c.expected)
		}
	}
	if got := lex("let café = 1..2;")[0][0]; got != Keyword {
		t.Errorf("let = %v", got)
	}
	if got := lex("let café = 1..2;")[0][12]; got != Punctuation {
		t.Errorf("'.' = %v", got)
	}
}

// Rust: syntax::go_tests::go_fallback_handles_multiline_raw_strings_comments_runes_and_keywords
func TestGoFallbackHandlesMultilineRawStringsCommentsRunesAndKeywords(t *testing.T) {
	text := "package main\n/* func\nstill */ func main() {\ns := `// raw\nfunc` // comment\nr := '\\''; n := 0x1.fp+2; f := .5\nvar 界 = \"a\\\"b\"\npackageName := 1\n"
	lines := splitLines(text)
	styles := Go(lines)
	if !allStyle(styles[0][:7], Keyword) {
		t.Errorf("row 0 = %v", styleCodes(styles[0]))
	}
	if !allStyle(styles[1], Comment) {
		t.Errorf("row 1 = %v", styleCodes(styles[1]))
	}
	if styles[2][9] != Keyword {
		t.Errorf("styles[2][9] = %v", styles[2][9])
	}
	if styles[3][5] != String {
		t.Errorf("styles[3][5] = %v", styles[3][5])
	}
	if !allStyle(styles[4][:5], String) {
		t.Errorf("row 4 = %v", styleCodes(styles[4]))
	}
	if !allStyle(styles[4][6:], Comment) {
		t.Errorf("row 4 = %v", styleCodes(styles[4]))
	}
	if styles[5][5] != String {
		t.Errorf("styles[5][5] = %v", styles[5][5])
	}
	if styles[5][17] != Number {
		t.Errorf("styles[5][17] = %v", styles[5][17])
	}
	if styles[6][0] != Keyword {
		t.Errorf("styles[6][0] = %v", styles[6][0])
	}
	if styles[7][0] != Plain {
		t.Errorf("styles[7][0] = %v", styles[7][0])
	}
	if got := ForPath("test.GO", lines); !reflect.DeepEqual(got, styles) {
		t.Errorf("ForPath(test.GO) differs from Go()")
	}
}

func TestForPathPicksLexersByExtension(t *testing.T) {
	// Expected extensions were checked against Path::extension in Rust.
	rust := splitLines("fn main() {} // c")
	for _, c := range []struct {
		path string
		want string // "rust", "go", "md" or "" for no lexer
	}{
		{"main.rs", "rust"}, {"MAIN.RS", "rust"}, {"dir/a.b.Rs", "rust"}, {"/abs/dir.go/x.rs", "rust"},
		{"x.go", "go"}, {"x.GO", "go"}, {"x.Go", "go"},
		{"x.md", "md"}, {"x.MD", "md"}, {"x.markdown", "md"}, {"x.MarkDown", "md"}, {"./docs/x.md", "md"},
		{"dir/x.rs/", "rust"}, // a trailing slash does not change the file name
		{"x.txt", ""}, {"x", ""}, {"Makefile", ""}, {"x.", ""}, {".rs", ""}, {".md", ""}, {"", ""},
		{"x.rs.bak", ""}, {"x.mdx", ""}, {"x.r", ""}, {"x.rss", ""}, {"x.gom", ""}, {"x.rs/", "rust"},
		{"dir.rs/file", ""}, {"dir.go/", "go"}, {"..", ""}, {"x.rs/..", ""}, {".", ""}, {"./", ""},
		{"dir//x.rs", "rust"}, {"x.rs//", "rust"}, {"a/./x.go", "go"}, {"x.rs/.", "rust"}, {"..rs", "rust"}, {"...rs", "rust"},
		// to_ascii_lowercase leaves every non-ASCII character alone: U+212A is
		// not a K, and U+017F is not an S.
		{"x.marKdown", ""}, {"x.rſ", ""},
		{"x.rs\x00", ""}, {"x.\xffrs", ""},
	} {
		got := ForPath(c.path, rust)
		var want [][]Style
		switch c.want {
		case "rust":
			want = Rust(rust)
		case "go":
			want = Go(rust)
		case "md":
			want = highlightMarkdown(rust)
		}
		if (got == nil) != (want == nil) || !reflect.DeepEqual(got, want) {
			t.Errorf("ForPath(%q) = %v, want the %q lexer (%v)", c.path, got, c.want, want)
		}
	}
	// No lines is not "no lexer": the result is an empty list, not nil.
	if got := ForPath("a.rs", nil); got == nil || len(got) != 0 {
		t.Errorf("ForPath(a.rs, nil) = %#v", got)
	}
	if ForPath("a.txt", nil) != nil {
		t.Error("ForPath(a.txt, nil) must be nil")
	}
	for _, lex := range []func([][]rune) [][]Style{Rust, Go, highlightMarkdown} {
		if got := lex(nil); got == nil || len(got) != 0 {
			t.Errorf("lexer on no lines = %#v", got)
		}
	}
}

func TestLexersKeepOneStylePerCharacter(t *testing.T) {
	texts := []string{
		"", "\r", "a", "'", "\"", "`", "/", "/*", "r#", "r#\"", "b'", "'\\u{", "[a](", "<!--", "```",
		"let x = '界'; // é\r\n/* \U0001f980 */\nlet y = r#\"\U0001f980\n\"#;",
		"# 界 *é* `\U0001f980`\n```go\nx := \"\U0001f980\"\n```\n<!-- \U0001f980 -->\n[l](u)",
	}
	for _, text := range texts {
		lines := splitLines(text)
		for name, lexer := range map[string]func([][]rune) [][]Style{"rust": Rust, "go": Go, "markdown": highlightMarkdown} {
			styles := lexer(lines)
			if len(styles) != len(lines) {
				t.Fatalf("%s(%q): %d rows for %d lines", name, text, len(styles), len(lines))
			}
			for i := range lines {
				if len(styles[i]) != len(lines[i]) {
					t.Errorf("%s(%q) row %d: %d styles for %d characters", name, text, i, len(styles[i]), len(lines[i]))
				}
			}
		}
	}
}

func TestLexersDoNotModifyTheirInput(t *testing.T) {
	lines := splitLines("# a *b*\n```rust\nfn\n```\n<!-- c\n-->")
	before := make([]string, len(lines))
	for i, l := range lines {
		before[i] = string(l)
	}
	for _, lexer := range []func([][]rune) [][]Style{Rust, Go, highlightMarkdown} {
		lexer(lines)
	}
	for i, l := range lines {
		if string(l) != before[i] {
			t.Errorf("line %d changed to %q", i, string(l))
		}
	}
}

// edgeCase is a text and the rows (joined by "|") that the Rust lexer in
// src/syntax.rs produced for it, spelled with styleCodes.
type edgeCase struct{ text, want string }

func checkEdgeCases(t *testing.T, name string, lexer func([][]rune) [][]Style, cases []edgeCase) {
	t.Helper()
	for _, c := range cases {
		styles := lexer(splitLines(c.text))
		rows := make([]string, len(styles))
		for i, row := range styles {
			rows[i] = styleCodes(row)
		}
		if got := strings.Join(rows, "|"); got != c.want {
			t.Errorf("%s(%q) = %s, want %s", name, c.text, got, c.want)
		}
	}
}

func TestRustLexerEdgeCases(t *testing.T) {
	checkEdgeCases(t, "Rust", Rust, []edgeCase{
		{"if!x", "mmm."},
		{"a!=b", ".pp."},
		{"a! =b", "mm.p."},
		{"r#type!", "......p"},
		{"r#fn", "...."},
		{"r# fn", ".p.kk"},
		{"r#1", ".pn"},
		{"rr#a", "..p."},
		{"1e+5 1E-5 0xE+1 0xe-1 1_e+2", "nnnn.nnnn.nnnnn.nnnnn.nnnnn"},
		{"1.5 1..2 1.a x.0.1", "nnn.nppn.np...pnnn"},
		{"'a' 'a 'ab' 'é 'é'", "sss.ll.lllsss..sss"},
		{"'\\u{1_F980}' '\\u{}' '\\xZZ'", "ssssssssssss.ssssss.pp...p"},
		{"\"a\\", "sss"},
		{"\u2167 \u00aa\u00b2 界 é\u0301", "........."},
		{"b\"x\" c\"y\" cr\"z\" bb\"w\"", "ssss.ssss.sssss...sss"},
		{"r#\"a\"# r##\"b\"#\"## r\"c\"", "ssssss.ssssssssss.ssss"},
		{"/* a /* b */ c */ d", "ccccccccccccccccc.."},
		{"'\\'' '\\\\' '' b'' '\\", "ssss.ssss.pp..psssp"},
		{"9é 1_ 1__ 0b1e+1", "n..nn.nnn.nnnnnn"},
		{"x\u00a0y\u3000z", "....."},
		{"\"a\\\n\" let", "sss|s.kkk"},
		{"\"a\\\\\n\" let", "ssss|s.kkk"},
		{"a_b'c 'a'b\"c\"d", "...ll.sssssss."},
		{"1.e3 1.0.0 1.5.method() 1 .. 2", "np...nnnnn.nnnp......pp.n.pp.n"},
		{"let _ = 'static;'", "kkk...p.lllllllpp"},
		{"0x1F 0b1010_1010 0o77 1.2e-3f64", "nnnn.nnnnnnnnnnn.nnnn.nnnnnnnnn"},
		{"<'a, 'b: 'a>(x: &'a str)", "pllp.llp.llpp.p.pll....p"},
		{"cr#\"x\"# br\"y\" rb\"z\"", "sssssss.sssss...sss"},
		{"/*/ x */ y", "cccccccc.."},
		{"/**/ z", "cccc.."},
		{"*/ stray /* open", "pp.......ccccccc"},
	})
}

func TestGoLexerEdgeCases(t *testing.T) {
	checkEdgeCases(t, "Go", Go, []edgeCase{
		{"0xe+1 0xp-1 1+2 .5 .e5 a.b 1.2.3", "nnnnn.nnnnn.npn.nn.p....p..nnnnn"},
		{"'a' '\\'' ''' `x` \"y\"", "sss.ssss.sssssssssss"},
		{"x /* a */ y // z", "..ccccccc...cccc"},
		{"`a\n// b\n` c", "ss|ssss|s.."},
		{"\"a\\\nb\" c", "sss|.sss"},
		{"func界 界func func_ _func", "......................."},
		{"3i 0x1p-2 1e 1e+e+1", "nn.nnnnnn.nn.nnnnnn"},
		{"/* a\n` b\n*/ ` c", "cccc|ccc|cc.sss"},
	})
}
