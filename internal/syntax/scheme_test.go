package syntax

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSchemeTokensAndWholeIdentifiers(t *testing.T) {
	source := "(define café (lambda (x) (if #t -1.5e+2 +.5))) ; 文\n(define-value -name 1name foo#t) '(a . b) `(,c ,@d) #f"
	styles := Scheme(splitLines(source))
	for _, c := range []struct {
		row   int
		token string
		style Style
	}{
		{0, "define", Keyword}, {0, "lambda", Keyword}, {0, "if", Keyword}, {0, "#t", Constant},
		{0, "-1.5e+2", Number}, {0, "+.5", Number}, {0, "; 文", Comment}, {0, "café", Plain},
		{1, "define-value", Plain}, {1, "-name", Plain}, {1, "1name", Plain}, {1, "foo#t", Plain}, {1, "#f", Constant},
	} {
		line := strings.Split(source, "\n")[c.row]
		at := utf8.RuneCountInString(line[:strings.Index(line, c.token)])
		end := at + utf8.RuneCountInString(c.token)
		if !allStyle(styles[c.row][at:end], c.style) {
			t.Fatalf("%q: %s", c.token, styleCodes(styles[c.row]))
		}
	}
	for _, ext := range []string{"init.scm", "INIT.SCM"} {
		if got := ForPath(ext, splitLines(source)); got == nil || got[0][1] != Keyword {
			t.Fatal(ext, got)
		}
	}
	if got := Scheme(nil); got == nil || len(got) != 0 {
		t.Fatal(got)
	}
}
func TestSchemeMultilineCommentsStringsAndRecovery(t *testing.T) {
	source := "#| comment \"\n#| still |# (define x 1)\n\"hello \\\" ; #|\n界\" #t\n\"\"\"raw \\\"\n; #| words\n\"\"\" (lambda (x) x)"
	styles := Scheme(splitLines(source))
	if !allStyle(styles[0], Comment) || styles[1][13] != Keyword {
		t.Fatalf("comments: %s", styleCodes(styles[1]))
	}
	if !allStyle(styles[2], String) || styles[3][0] != String || styles[3][3] != Constant {
		t.Fatalf("strings: %s", styleCodes(styles[3]))
	}
	if !allStyle(styles[4], String) || !allStyle(styles[5], String) || styles[6][5] != Keyword {
		t.Fatalf("raw strings: %s", styleCodes(styles[6]))
	}
	edited := Scheme(splitLines(strings.Replace(source, "#| comment", "; comment", 1)))
	if edited[1][0] != Comment {
		t.Fatal("second block opener lost")
	}
	quoted := Scheme(splitLines("\"open\n(define x 1)"))
	if !allStyle(quoted[1], String) {
		t.Fatal("unfinished string lost state")
	}
	plain := Scheme(splitLines("; open\n(define x 1)"))
	if plain[1][1] != Keyword {
		t.Fatal("edit did not reset string state")
	}
}
func TestStarterConfigHasSchemeColours(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "sample.scm"))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[Style]int{}
	for _, line := range ForPath("init.scm", splitLines(string(data))) {
		for _, style := range line {
			counts[style]++
		}
	}
	for _, style := range []Style{Keyword, Comment, String, Number, Punctuation} {
		if counts[style] == 0 {
			t.Fatal("starter config has no style", style)
		}
	}
}
