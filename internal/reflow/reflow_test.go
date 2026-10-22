package reflow

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func fill(s string, cursor, width int) string {
	start, end, replacement, ok := Paragraph(s, cursor, width)
	if !ok {
		return s
	}
	return s[:start] + replacement + s[end:]
}

func assertEq(t *testing.T, got, want string, context ...any) {
	t.Helper()
	if got != want {
		t.Errorf("%v: got %q, want %q", fmt.Sprint(context...), got, want)
	}
}

// Rust: reflow.rs::tests::wraps_only_current_paragraph
func TestWrapsOnlyCurrentParagraph(t *testing.T) {
	assertEq(t, fill("one two three four\n\nother paragraph", 4, 10), "one two\nthree four\n\nother paragraph")
	assertEq(t, fill("one two\nthree four\n", 9, 80), "one two three four\n")
}

// Rust: reflow.rs::tests::protects_markdown_blocks_and_breaks
func TestProtectsMarkdownBlocksAndBreaks(t *testing.T) {
	for _, s := range []string{
		"# one two three",
		"title here\n----------",
		"```\none two three\n```",
		"    one two three",
		"| one | two |\n| --- | --- |\n| three | four |",
		"one two  \nthree four",
		"one two\\\nthree four",
		"<div>\none two three\n</div>",
	} {
		for cursor := 0; cursor < len(s); cursor++ {
			assertEq(t, fill(s, cursor, 5), s, s)
		}
	}
}

// Rust: reflow.rs::tests::containers_and_inline_syntax
func TestContainersAndInlineSyntax(t *testing.T) {
	assertEq(t, fill("- one two three four\n- next item", 5, 12), "- one two\n  three four\n- next item")
	assertEq(t, fill("> one two three four", 5, 12), "> one two\n> three four")
	assertEq(t, fill("  - one two three four", 7, 14), "  - one two\n    three four")
	for _, atom := range []string{
		"`one  two`",
		"[one two](https://example.com/a)",
		"![one two](image.png)",
		"``one ` two``",
	} {
		source := "before " + atom + " after words"
		result := fill(source, 1, 8)
		if !strings.Contains(result, atom) {
			t.Errorf("%q", result)
		}
	}
}

// Rust: reflow.rs::tests::nested_lists_quotes_tasks_and_protected_neighbors
func TestNestedListsQuotesTasksAndProtectedNeighbors(t *testing.T) {
	for _, tc := range []struct {
		source   string
		cursor   int
		expected string
	}{
		{"- parent\n  - one two three four\n  - next", 9, "- parent\n  - one two\n    three four\n  - next"},
		{"12. one two three four\n13. next", 0, "12. one two\n    three four\n13. next"},
		{"> - one two three four", 0, "> - one two\n>   three four"},
		{"- [x] one two three four", 0, "- [x] one two\n  three four"},
	} {
		output := fill(tc.source, tc.cursor, 14)
		assertEq(t, output, tc.expected)
		assertEq(t, fill(output, tc.cursor, 14), output)
	}
	for _, source := range []string{
		"- ```\n  one two three four\n  ```",
		"> ```\n> one two three four\n> ```",
		"[link]: https://example.com/somewhere",
		"- | one | two |\n  | --- | --- |\n  | three | four |",
	} {
		for cursor := 0; cursor < len(source); cursor++ {
			assertEq(t, fill(source, cursor, 5), source, source)
		}
	}
	assertEq(t, fill("one two three four\n\n```\ncode here\n```", 0, 10), "one two\nthree four\n\n```\ncode here\n```")
}

// Rust: reflow.rs::tests::unicode_crlf_and_idempotence
func TestUnicodeCrlfAndIdempotence(t *testing.T) {
	s := "世界 世界 words more\r\n\r\nother\r\n"
	result := fill(s, 0, 10)
	assertEq(t, result, "世界 世界\r\nwords more\r\n\r\nother\r\n")
	assertEq(t, fill(result, 0, 10), result)
}

// TestGoldenAgainstRust checks Paragraph against results recorded from the real src/reflow.rs
// (pulldown-cmark 0.13.4): testdata/golden.jsonl has one {"text","cursor","width","want"} per line,
// want being null or [start, end, replacement]. It covers the classes found while comparing the
// two parsers: tight and loose task items, escapes at the start of a tight item, CRLF, empty list
// items, tables behind list-like delimiter rows and tables interrupting paragraphs, hard breaks made
// of tabs, code spans over line breaks and link reference definitions.
func TestGoldenAgainstRust(t *testing.T) {
	f, err := os.Open("testdata/golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		var g struct {
			Text   string `json:"text"`
			Cursor int    `json:"cursor"`
			Width  int    `json:"width"`
			Want   []any  `json:"want"`
		}
		if err := json.Unmarshal(sc.Bytes(), &g); err != nil {
			t.Fatal(err)
		}
		n++
		start, end, replacement, ok := Paragraph(g.Text, g.Cursor, g.Width)
		if g.Want == nil {
			if ok {
				t.Errorf("Paragraph(%q, %d, %d) = %d, %d, %q; want none", g.Text, g.Cursor, g.Width, start, end, replacement)
			}
			continue
		}
		wantStart, wantEnd, wantReplacement := int(g.Want[0].(float64)), int(g.Want[1].(float64)), g.Want[2].(string)
		if !ok || start != wantStart || end != wantEnd || replacement != wantReplacement {
			t.Errorf("Paragraph(%q, %d, %d) = %d, %d, %q, %v; want %d, %d, %q", g.Text, g.Cursor, g.Width,
				start, end, replacement, ok, wantStart, wantEnd, wantReplacement)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 2000 {
		t.Errorf("only %d golden cases", n)
	}
}

func TestContainerPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "  ": "", "> ": "> ", ">": ">", ">  ": "> ", "- ": "- ", "-   ": "-   ", "  - ": "  - ",
		"> - ": "> - ", "12. ": "12. ", "1) ": "1) ", ">- ": ">- ", "> > ": "> > ", "- [": "- ", "-": "",
		"   > > ": "   > > ", "\t> ": "\t> ", "- - ": "- - ", "1.5 ": "", "a ": "",
	} {
		if got := containerPrefix(in); got != want {
			t.Errorf("containerPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPulldownTableRules(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"| a | b |\n| - | - |", true},
		{"a | b\n--- | ---", true},
		{"a\n:---", false},              // goldmark would make a one-column table
		{"a | b\n--- | ---:| -", false}, // column counts differ
		{"a | b\\\n--- | ---", false},   // the backslash makes a hard break
		{"a | b\n---", false},
	} {
		if got := startsTable(tc.text, 0, "", true); got != tc.want {
			t.Errorf("startsTable(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
