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
	e.Window = &editor.Window{Kind: editor.WindowText, Title: "Modules", Lines: lines}
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
	e.Window.Scroll = 1 << 30
	draw(t, &v, e, 40, 12)
	if e.Window.Scroll == 1<<30 || e.Window.Scroll == 0 {
		t.Fatalf("scroll = %d; the view should clamp it", e.Window.Scroll)
	}
}

func screenOf(v *View, rows int) string {
	var b strings.Builder
	for i := range rows {
		b.WriteString(row(v, i))
		b.WriteString("\n")
	}
	return b.String()
}

func TestListWindowShowsTheSelectionTheFilterAndTheCount(t *testing.T) {
	e := newEditor(t, filepath.Join(shortDir(t), "f.txt"), "text")
	w := &editor.Window{Kind: editor.WindowList, Title: "Pick one", Lines: []string{"alpha", "beta", "alphabet"}}
	e.Window = w
	var v View
	draw(t, &v, e, 60, 8)
	screen := screenOf(&v, 8)
	for _, want := range []string{"Pick one 3/3", "> alpha", "  beta", "  alphabet", "Filter: "} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	w.Query = "alp"
	w.Selected = 1
	draw(t, &v, e, 60, 8)
	screen = screenOf(&v, 8)
	if !strings.Contains(screen, "Pick one 2/3") || !strings.Contains(screen, "> alphabet") ||
		strings.Contains(screen, "beta") || !strings.Contains(screen, "Filter: alp") {
		t.Fatalf("filtered list:\n%s", screen)
	}
	w.Query = "zzz"
	draw(t, &v, e, 60, 8)
	if screen = screenOf(&v, 8); !strings.Contains(screen, "No matches") {
		t.Fatalf("no matches:\n%s", screen)
	}
}

func TestPromptWindowShowsWhatIsTypedOnTheLastRow(t *testing.T) {
	e := newEditor(t, filepath.Join(shortDir(t), "f.txt"), "text")
	e.Window = &editor.Window{Kind: editor.WindowPrompt, Title: "Rename to", Query: "hello"}
	var v View
	draw(t, &v, e, 60, 6)
	screen := screenOf(&v, 6)
	if !strings.Contains(screen, "Rename to") || !strings.HasSuffix(strings.TrimRight(screen, "\n"), "Input: hello") {
		t.Fatalf("prompt:\n%s", screen)
	}
}

func TestConfirmWindowNamesWhatYAndNDo(t *testing.T) {
	e := newEditor(t, filepath.Join(shortDir(t), "f.txt"), "text")
	e.Window = &editor.Window{Kind: editor.WindowConfirm, Title: "Install", Lines: []string{"code"}}
	var v View
	draw(t, &v, e, 70, 6)
	if screen := screenOf(&v, 6); !strings.Contains(screen, "y apply | n/Esc cancel") {
		t.Fatalf("confirm:\n%s", screen)
	}
}
