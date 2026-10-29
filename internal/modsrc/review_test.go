package modsrc

import (
	"strings"
	"testing"
)

func TestPrintableWritesOutWhatCouldHideCode(t *testing.T) {
	cases := map[string]string{
		"plain (define x 1)":    "plain (define x 1)",
		"tab\there":             "tab\there",
		"héllo ✓ 日本":            "héllo ✓ 日本",
		"esc\x1b[31m":           "esc<U+001B>[31m",
		"cr\rhere":              "cr<U+000D>here",
		"nul\x00":               "nul<U+0000>",
		"del\x7f c1\u0085":      "del<U+007F> c1<U+0085>",
		"bidi \u202e reversed":  "bidi <U+202E> reversed",
		"isolate \u2066 x":      "isolate <U+2066> x",
		"zero\u200bwidth":       "zero<U+200B>width",
		"bom\ufeff":             "bom<U+FEFF>",
		"sep\u2028x\u2029":      "sep<U+2028>x<U+2029>",
		"filler\u3164":          "filler<U+3164>",
		"bad \xff\xfe bytes":    "bad <0xFF><0xFE> bytes",
		"truncated \xe2\x82":    "truncated <0xE2><0x82>",
		"nbsp\u00a0stays":       "nbsp\u00a0stays",
		"emoji \U0001F600 stay": "emoji \U0001F600 stay",
	}
	for in, want := range cases {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestDiff(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	cases := []struct {
		name, old, new string
		want           []string
	}{
		{"nothing", old, old, nil},
		{"a change in the middle", old, "a\nb\nc\nd\nE\nf\ng\nh\ni\nj\n", []string{
			"@@ -2,7 +2,7 @@", "  b", "  c", "  d", "- e", "+ E", "  f", "  g", "  h",
		}},
		{"two far apart changes make two hunks", old, "A\nb\nc\nd\ne\nf\ng\nh\ni\nJ\n", []string{
			"@@ -1,4 +1,4 @@", "- a", "+ A", "  b", "  c", "  d",
			"@@ -7,4 +7,4 @@", "  g", "  h", "  i", "- j", "+ J",
		}},
		{"an added line", "a\nb\n", "a\nx\nb\n", []string{"@@ -1,2 +1,3 @@", "  a", "+ x", "  b"}},
		{"a removed line", "a\nx\nb\n", "a\nb\n", []string{"@@ -1,3 +1,2 @@", "  a", "- x", "  b"}},
		{"from nothing", "", "a\nb\n", []string{"@@ -1,0 +1,2 @@", "+ a", "+ b"}},
		{"to nothing", "a\n", "", []string{"@@ -1,1 +1,0 @@", "- a"}},
		{"no trailing newline is the same text", "a\nb", "a\nb\n", nil},
	}
	for _, c := range cases {
		got, ok := Diff([]byte(c.old), []byte(c.new))
		if !ok {
			t.Errorf("%s: too large", c.name)
			continue
		}
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestDiffOfHugeFilesGivesUpInsteadOfStalling(t *testing.T) {
	big := []byte(strings.Repeat("x\n", 3000))
	if _, ok := Diff(big, big); ok {
		t.Fatal("a 3000 by 3000 line comparison should be refused")
	}
}

func TestReviewShowsEverythingASafeDecisionNeeds(t *testing.T) {
	newFile := File{Module: "alpha", Data: []byte("(define-module alpha)\n(display \"hi\u202e\")\x1b[2J\n")}
	changed := Change{Module: "beta", Kind: Update, Old: []byte("(define-module beta)\n(x)\n"), New: []byte("(define-module beta)\n(y)\n")}
	plan := &Plan{
		Source: Source{Owner: "o", Repo: "r", Ref: "v1"},
		Commit: commitA,
		Changes: []Change{
			{Module: "alpha", Kind: Add, New: newFile.Data},
			changed,
			{Module: "gamma", Kind: Same, New: []byte("same")},
			{Module: "delta", Blocked: "modules/delta.scm exists and was not installed from a repository"},
		},
		Notes: []string{"alpha requires zeta, which is not installed"},
	}
	text := strings.Join(Review([]*Plan{plan}), "\n")
	for _, want := range []string{
		"o/r@v1 at commit " + commitA,
		"add           alpha",
		"sha256 " + Sum(newFile.Data),
		"update        beta",
		"unchanged     gamma",
		"CANNOT APPLY  delta: modules/delta.scm exists",
		"note: alpha requires zeta",
		"=== modules/alpha.scm: new file ===",
		"+ (define-module alpha)",
		"=== modules/beta.scm: changes ===",
		"- (x)",
		"+ (y)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the review lacks %q:\n%s", want, text)
		}
	}
	if strings.ContainsRune(text, 0x1b) || strings.ContainsRune(text, 0x202e) {
		t.Errorf("the review holds a character that could repaint it:\n%q", text)
	}
	if !strings.Contains(text, "hi<U+202E>") || !strings.Contains(text, "<U+001B>[2J") {
		t.Errorf("the escape sequence should be written out:\n%q", text)
	}
	if strings.Contains(text, "modules/gamma.scm") || strings.Contains(text, "modules/delta.scm: ") {
		t.Errorf("an unchanged or blocked module should have no body:\n%s", text)
	}
}
