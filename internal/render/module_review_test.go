package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/editor"
)

func TestWrapLinesKeepsEveryCharacter(t *testing.T) {
	long := strings.Repeat("abcdefghij", 5)
	cases := []struct {
		in    string
		width int
		want  []string
	}{
		{"", 10, []string{""}},
		{"short", 10, []string{"short"}},
		{long, 20, []string{long[:20], long[20:40], long[40:]}},
		{"日本語日本語", 6, []string{"日本語", "日本語"}},
		{"日本語", 5, []string{"日本", "語"}},
		{"a\tb", 4, []string{"a", "\t", "b"}},
		{"x", 0, []string{"x"}},
	}
	for _, c := range cases {
		got := wrapLines([]string{c.in}, c.width)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("wrapLines(%q, %d) = %q; want %q", c.in, c.width, got, c.want)
		}
		if strings.Join(got, "") != c.in {
			t.Errorf("wrapLines(%q, %d) lost text: %q", c.in, c.width, got)
		}
	}
}

func TestModuleReviewPageWrapsScrollsAndNamesItsKeys(t *testing.T) {
	e := newEditor(t, filepath.Join(shortDir(t), "f.txt"), "text")
	tail := "END-OF-THE-LONG-LINE"
	lines := []string{"first", strings.Repeat("x", 100) + tail}
	for i := range 40 {
		lines = append(lines, "line "+strings.Repeat("y", i%5))
	}
	e.ModuleReview = &editor.ModuleReview{Title: "Modules", Lines: lines}
	var v View
	draw(t, &v, e, 40, 12)
	screen := ""
	for i := range 12 {
		screen += row(&v, i) + "\n"
	}
	if !strings.Contains(screen, "Modules") || !strings.Contains(screen, "first") {
		t.Fatalf("title or first line missing:\n%s", screen)
	}
	if !strings.Contains(screen, "Esc close") {
		t.Fatalf("footer missing:\n%s", screen)
	}
	// The long line shows in full, across rows.
	joined := strings.ReplaceAll(screen, "\n", "")
	if !strings.Contains(joined, tail) {
		t.Fatalf("the long line was clipped:\n%s", screen)
	}
	e.ModuleReview.Scroll = 1 << 30
	draw(t, &v, e, 40, 12)
	if e.ModuleReview.Scroll == 1<<30 || e.ModuleReview.Scroll == 0 {
		t.Fatalf("scroll = %d; the view should clamp it", e.ModuleReview.Scroll)
	}
}
