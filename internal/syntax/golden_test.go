package syntax

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenRowsMatchTheRustLexers checks the lexers against testdata/golden.jsonl,
// a few hundred texts together with the rows that the real src/syntax.rs and
// src/syntax/markdown.rs produced for them (one letter per character, see
// styleCodes). The cases are the hand-written snippets of every construct the
// lexers handle plus seeded random documents; testdata/regenerate.py rebuilds
// the file from the Rust sources and can also write the whole 37,000-case
// corpus for a full differential run.
func TestGoldenRowsMatchTheRustLexers(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "golden.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	lexers := map[string]func([][]rune) [][]Style{
		"rust": Rust,
		"go":   Go,
		"md":   func(lines [][]rune) [][]Style { return ForPath("x.md", lines) },
	}
	counts := map[string]int{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for number := 1; scanner.Scan(); number++ {
		var c struct {
			Lang   string   `json:"lang"`
			Text   string   `json:"text"`
			Styles []string `json:"styles"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatalf("golden.jsonl:%d: %v", number, err)
		}
		lexer := lexers[c.Lang]
		if lexer == nil {
			t.Fatalf("golden.jsonl:%d: unknown lexer %q", number, c.Lang)
		}
		counts[c.Lang]++
		styles := lexer(splitLines(c.Text))
		rows := make([]string, len(styles))
		for i, row := range styles {
			rows[i] = styleCodes(row)
		}
		if got, want := strings.Join(rows, "|"), strings.Join(c.Styles, "|"); got != want {
			t.Errorf("golden.jsonl:%d %s %q:\n got %s\nwant %s", number, c.Lang, c.Text, got, want)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	total := 0
	for lang := range lexers {
		if counts[lang] < 50 {
			t.Errorf("golden.jsonl has only %d %s cases", counts[lang], lang)
		}
		total += counts[lang]
	}
	if total < 300 {
		t.Errorf("golden.jsonl has only %d cases", total)
	}
}
