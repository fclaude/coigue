package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/actions"
	"github.com/fclaude/coigue/internal/buffer"
	"github.com/fclaude/coigue/internal/editor"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/lsp"
	"github.com/fclaude/coigue/internal/picker"
	"github.com/fclaude/coigue/internal/semantic"
	"github.com/fclaude/coigue/internal/syntax"
	"github.com/fclaude/coigue/internal/term"
	"github.com/fclaude/coigue/internal/theme"
)

func newEditor(t *testing.T, path, text string) *editor.Editor {
	t.Helper()
	f, _, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := editor.New(t.Context(), f, text)
	t.Cleanup(e.Close)
	return e
}

// shortDir is a fresh directory whose path is as short as the system allows.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "c")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func newWelcome(t *testing.T, dir string) *editor.Editor {
	t.Helper()
	e := editor.NewWelcome(t.Context(), dir)
	t.Cleanup(e.Close)
	return e
}

// draw renders one frame and returns the bytes written.
func draw(t *testing.T, v *View, e *editor.Editor, w, h int) string {
	t.Helper()
	var out bytes.Buffer
	if err := v.DrawTo(e, w, h, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func typeKeys(e *editor.Editor, rows int, keys string) {
	for _, c := range keys {
		e.Key(key.Char(c), rows)
	}
}

func row(v *View, i int) string { return string(v.previous[i]) }

func waitLoaded(t *testing.T, e *editor.Editor) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for e.Explorer.Loading {
		e.Explorer.Poll()
		if time.Now().After(deadline) {
			t.Fatal("explorer did not finish loading")
		}
		time.Sleep(time.Millisecond)
	}
}

// Rust: render::tests::visual_selection_repaints_empty_lines_scrolls_and_cancels
// Parity: VIS, REN.
func TestVisualSelectionRepaintsEmptyLinesScrollsAndCancels(t *testing.T) {
	dir := t.TempDir()
	e := newEditor(t, filepath.Join(dir, "selection.md"), "a界\n\nlast\nfour\nfive\nsix\nseven")
	var v View
	draw(t, &v, e, 14, 5)
	typeKeys(e, 3, "v2jl")
	draw(t, &v, e, 14, 5)
	for i := 0; i < 3; i++ {
		if !strings.Contains(row(&v, i), term.Reverse) {
			t.Fatalf("row %d lacks the selection: %q", i, row(&v, i))
		}
	}
	// The selected empty line has an actual highlighted cell, retained after clearing.
	if !strings.Contains(row(&v, 1), term.Reverse+" ") {
		t.Fatalf("empty selected line: %q", row(&v, 1))
	}
	e.Key(key.Char('G'), 3)
	draw(t, &v, e, 14, 5)
	if v.top == 0 || !strings.Contains(row(&v, 0), term.Reverse) {
		t.Fatalf("top %d row0 %q", v.top, row(&v, 0))
	}
	e.Key(key.Press(key.Esc), 3)
	draw(t, &v, e, 14, 5)
	for i, r := range v.previous {
		if strings.Contains(string(r), term.Reverse) {
			t.Fatalf("row %d kept the selection: %q", i, r)
		}
	}
	typeKeys(e, 3, "Vgg")
	draw(t, &v, e, 5, 3)
	draw(t, &v, e, 1, 1)
	if e.Buffer.Text() != "a界\n\nlast\nfour\nfive\nsix\nseven" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: render::tests::visual_plain_text_horizontal_scroll_and_theme_reset
func TestVisualPlainTextHorizontalScrollAndThemeReset(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "selection.txt"), "abcdefghi\t界éxyz")
	var v View
	typeKeys(e, 2, "v$")
	for _, th := range []theme.Theme{theme.Terminal, theme.Monochrome, theme.CatppuccinMocha} {
		e.Theme = th
		draw(t, &v, e, 9, 4)
		if v.left == 0 || !strings.Contains(row(&v, 0), term.Reverse) {
			t.Fatalf("%v: left %d row %q", th, v.left, row(&v, 0))
		}
	}
	e.Key(key.Press(key.Esc), 2)
	draw(t, &v, e, 9, 4)
	if strings.Contains(row(&v, 0), term.Reverse) || v.styles != nil {
		t.Fatalf("selection survived Esc: %q styles %v", row(&v, 0), v.styles)
	}
}

// Rust: render::tests::visual_highlight_clips_tabs_unicode_and_resets_attributes
func TestVisualHighlightClipsTabsUnicodeAndResetsAttributes(t *testing.T) {
	var out []byte
	line := []rune("\t界é")
	styles := make([]syntax.Style, len(line))
	for i := range styles {
		styles[i] = syntax.Keyword
	}
	styledLine(&out, line, styles, 5, 2, theme.Terminal.Palette(), &selectionRange{1, 3}, false)
	rendered := string(out)
	if !strings.Contains(rendered, term.Reverse) || !strings.Contains(rendered, " é") ||
		!strings.Contains(rendered, term.NoReverse) || !strings.HasSuffix(rendered, term.ResetForeground) {
		t.Fatalf("rendered = %q", rendered)
	}
}

// Rust: render::tests::history_restores_viewport_without_resetting_same_file_syntax
// Parity: NAV, REN.
func TestHistoryRestoresViewportWithoutResettingSameFileSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.rs")
	text := strings.TrimSuffix(strings.Repeat("fn long_function_name_with_more_text() {}\n", 60), "\n")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, path, text)
	e.Buffer.Row = 30
	e.Buffer.Col = 25
	var v View
	draw(t, &v, e, 18, 8)
	original := [2]int{v.top, v.left}
	e.Viewport = editor.Viewport{Top: original[0], Left: original[1]}
	typeKeys(e, 6, ":1")
	e.Key(key.Press(key.Enter), 6)
	draw(t, &v, e, 18, 8)
	e.Viewport = editor.Viewport{Top: v.top, Left: v.left}
	e.Key(key.New(key.Left, key.Alt), 6)
	draw(t, &v, e, 18, 8)
	if e.Buffer.Row != 30 || e.Buffer.Col != 25 || [2]int{v.top, v.left} != original {
		t.Fatalf("cursor (%d,%d) view %v, want (30,25) %v", e.Buffer.Row, e.Buffer.Col, [2]int{v.top, v.left}, original)
	}
}

// Rust: render::tests::action_strip_and_menu_render_across_surfaces_and_resizes
// Parity: ACT, REN, TRM-10.
func TestActionStripAndMenuRenderAcrossSurfacesAndResizes(t *testing.T) {
	e := newWelcome(t, t.TempDir())
	var v View
	draw(t, &v, e, 100, 12)
	if !strings.Contains(row(&v, 0), "F2 Files") {
		t.Fatalf("strip = %q", row(&v, 0))
	}
	e.Menu = actions.NewMenu(actions.Code)
	for _, size := range [][2]int{{100, 12}, {1, 1}, {2, 2}, {80, 8}} {
		draw(t, &v, e, size[0], size[1])
	}
	var all []string
	for _, r := range v.previous {
		all = append(all, string(r))
	}
	text := strings.Join(all, "\n")
	if !strings.Contains(text, "Go to definition") || !strings.Contains(text, "F3 Code") {
		t.Fatalf("menu text = %q", text)
	}
	e.Menu.Query = "no such action"
	draw(t, &v, e, 80, 8)
	if !strings.Contains(row(&v, 1), "No matching actions") {
		t.Fatalf("row 1 = %q", row(&v, 1))
	}
}

// Rust: render::tests::help_returns_to_scrolled_file_and_survives_tiny_resizes
func TestHelpReturnsToScrolledFileAndSurvivesTinyResizes(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat("a very long line with text\n", 40), "\n")
	e := newEditor(t, filepath.Join(t.TempDir(), "guide"), text)
	e.Buffer.Row = 30
	e.Buffer.Col = 20
	var v View
	draw(t, &v, e, 16, 8)
	position := [2]int{v.top, v.left}
	e.Key(key.Press(key.F(1)), 6)
	for _, size := range [][2]int{{80, 24}, {1, 1}, {2, 2}, {16, 8}} {
		draw(t, &v, e, size[0], size[1])
	}
	e.Key(key.Press(key.Esc), 6)
	draw(t, &v, e, 16, 8)
	if [2]int{v.top, v.left} != position || e.Buffer.Row != 30 || e.Buffer.Col != 20 {
		t.Fatalf("view %v cursor (%d,%d), want %v (30,20)", [2]int{v.top, v.left}, e.Buffer.Row, e.Buffer.Col, position)
	}
}

var allThemes = []theme.Theme{
	theme.CatppuccinMocha, theme.GruvboxDark, theme.CatppuccinLatte, theme.Monochrome, theme.Terminal,
}

// Rust: render::tests::markdown_highlighting_repaints_themes_and_tracks_fence_edits_and_history
// Parity: MDH, THM.
func TestMarkdownHighlightingRepaintsThemesAndTracksFenceEditsAndHistory(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "test.MD"), "# Heading\n*italic* plain **bold**\n```rust\nfn main() {}\n```")
	var v View
	for _, th := range allThemes {
		e.Theme = th
		rendered := draw(t, &v, e, 80, 8)
		for _, attribute := range []string{term.Bold, term.NormalIntensity, term.Italic, term.NoItalic} {
			if !strings.Contains(rendered, attribute) {
				t.Fatalf("%v: missing %q", th, attribute)
			}
		}
		want := foreground(th.Palette().Syntax(syntax.Heading)) + term.Bold + "# Heading"
		if !strings.Contains(rendered, want) {
			t.Fatalf("%v: heading not rendered as %q in %q", th, want, rendered)
		}
		if v.styles[0][0] != syntax.Heading || v.styles[1][0] != syntax.Emphasis || v.styles[3][0] != syntax.Keyword {
			t.Fatalf("%v: styles %v", th, v.styles[:4])
		}
		if again := draw(t, &v, e, 80, 8); again != "" {
			t.Fatalf("%v: unchanged frame wrote %q", th, again)
		}
	}
	e.Buffer.Row = 2
	typeKeys(e, 6, "dd")
	draw(t, &v, e, 80, 8)
	for _, s := range v.styles[2] {
		if s != syntax.Plain {
			t.Fatalf("row 2 styles %v", v.styles[2])
		}
	}
	e.Key(key.Char('u'), 6)
	draw(t, &v, e, 80, 8)
	if v.styles[3][0] != syntax.Keyword {
		t.Fatalf("after undo: %v", v.styles[3])
	}
	e.Key(key.WithCtrl('r'), 6)
	draw(t, &v, e, 80, 8)
	if v.styles[2][0] != syntax.Plain {
		t.Fatalf("after redo: %v", v.styles[2])
	}
}

// Rust: render::tests::semantic_highlighting_uses_every_theme_and_reverts_to_lexical_colors
// Parity: SEM, THM.
func TestSemanticHighlightingUsesEveryThemeAndRevertsToLexicalColors(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		extension, text string
		start, end      int
	}{{"rs", "fn main() {}", 3, 7}, {"go", "func main() {}", 5, 9}} {
		e := newEditor(t, filepath.Join(dir, "test."+c.extension), c.text)
		var v View
		draw(t, &v, e, 100, 6)
		if v.styles[0][0] != syntax.Keyword || v.styles[0][c.start] != syntax.Plain {
			t.Fatalf("%s lexical styles: %v", c.extension, v.styles[0])
		}
		e.LSP.SemanticTokens = []semantic.Token{{Row: 0, Start: c.start, End: c.end, Style: syntax.Function}}
		e.LSP.SemanticRevision++
		for _, th := range allThemes {
			e.Theme = th
			rendered := draw(t, &v, e, 100, 6)
			if v.styles[0][c.start] != syntax.Function || v.styles[0][0] != syntax.Keyword {
				t.Fatalf("%v: styles %v", th, v.styles[0])
			}
			if want := foreground(th.Palette().Syntax(syntax.Function)) + "main"; !strings.Contains(rendered, want) {
				t.Fatalf("%v: %q missing in %q", th, want, rendered)
			}
			if th == theme.Terminal && (strings.Contains(rendered, "38;2;") || strings.Contains(rendered, "48;2;")) {
				t.Fatalf("terminal theme leaked RGB: %q", rendered)
			}
			if th == theme.Monochrome {
				if !strings.Contains(rendered, term.Bold) || !strings.Contains(rendered, term.NormalIntensity) ||
					th.Palette().Bold(syntax.Function) {
					t.Fatalf("monochrome bold handling: %q", rendered)
				}
			}
			if again := draw(t, &v, e, 100, 6); again != "" {
				t.Fatalf("%v: idle frame wrote %q", th, again)
			}
		}
		e.LSP.SemanticTokens = nil
		e.LSP.SemanticRevision++
		draw(t, &v, e, 100, 6)
		if v.styles[0][c.start] != syntax.Plain || v.styles[0][0] != syntax.Keyword {
			t.Fatalf("reverted styles: %v", v.styles[0])
		}
		for _, size := range [][2]int{{1, 1}, {2, 3}, {4, 5}, {100, 6}} {
			draw(t, &v, e, size[0], size[1])
		}
	}
}

// Rust: render::tests::theme_switch_repaints_all_rows_and_tails_without_resetting_viewport_or_syntax
// Parity: THM, REN.
func TestThemeSwitchRepaintsAllRowsAndTailsWithoutResettingViewportOrSyntax(t *testing.T) {
	for _, extension := range []string{"txt", "rs"} {
		text := strings.TrimSuffix(strings.Repeat("fn main() { let x = 42; }\n", 10), "\n")
		e := newEditor(t, filepath.Join(t.TempDir(), "test."+extension), text)
		e.Buffer.Row = 9
		e.Buffer.Col = 20
		e.LSP.Diagnostics = []lsp.Diagnostic{{
			Start: lsp.Position{Row: 9, Col: 0}, End: lsp.Position{Row: 9, Col: 1}, Severity: 2, Message: "warning",
		}}
		v := View{top: 9}
		draw(t, &v, e, 18, 6)
		position := [3]int{v.top, v.left}
		position[2] = v.cursor[0]*1000 + v.cursor[1]
		revision := *v.revision
		styles := v.styles
		for _, th := range []theme.Theme{theme.GruvboxDark, theme.CatppuccinLatte, theme.Monochrome, theme.Terminal, theme.CatppuccinMocha} {
			e.Theme = th
			palette := th.Palette()
			rendered := draw(t, &v, e, 18, 6)
			for r := 0; r < 6; r++ {
				if !strings.Contains(rendered, term.MoveTo(0, r)) {
					t.Fatalf("%s %v: stale row %d", extension, th, r)
				}
			}
			for _, want := range []string{background(palette.Background), background(palette.Surface),
				foreground(palette.Diagnostic(2)), term.ClearUntilNewLine} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("%s %v: missing %q", extension, th, want)
				}
			}
			if strings.Contains(rendered, term.ClearAll) {
				t.Fatalf("%s %v: cleared the whole screen", extension, th)
			}
			if th == theme.Terminal && strings.Contains(rendered, ";2;") {
				t.Fatalf("terminal theme leaked RGB colors: %q", rendered)
			}
			if got := [3]int{v.top, v.left, v.cursor[0]*1000 + v.cursor[1]}; got != position {
				t.Fatalf("%s %v: viewport %v, want %v", extension, th, got, position)
			}
			if *v.revision != revision || !equalStyles(v.styles, styles) {
				t.Fatalf("%s %v: highlighting cache was reset", extension, th)
			}
			if idle := draw(t, &v, e, 18, 6); idle != "" {
				t.Fatalf("%s %v: idle frame wrote %q", extension, th, idle)
			}
		}
		if e.Buffer.Text() != text {
			t.Fatal("text changed")
		}
	}
}

func equalStyles(a, b [][]syntax.Style) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// Rust: render::tests::welcome_and_browsers_repaint_themes_including_selected_rows_after_resize
func TestWelcomeAndBrowsersRepaintThemesIncludingSelectedRowsAfterResize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newWelcome(t, dir)
	var v View
	draw(t, &v, e, 80, 8)
	e.Theme = theme.CatppuccinLatte
	rendered := draw(t, &v, e, 80, 8)
	if !strings.Contains(rendered, background(e.Theme.Palette().Background)) || !strings.Contains(rendered, term.MoveTo(0, 0)) {
		t.Fatalf("welcome repaint: %q", rendered)
	}
	e.Key(key.Char('-'), 6)
	waitLoaded(t, e)
	for _, withPicker := range []bool{false, true} {
		if withPicker {
			e.Picker = picker.New(t.Context(), dir)
		}
		for _, th := range []theme.Theme{theme.GruvboxDark, theme.Monochrome, theme.Terminal, theme.CatppuccinMocha} {
			e.Theme = th
			for _, size := range [][2]int{{0, 0}, {1, 1}, {80, 8}} {
				draw(t, &v, e, size[0], size[1])
			}
			e.Theme = theme.CatppuccinLatte
			rendered := draw(t, &v, e, 80, 8)
			for r := 0; r < 8; r++ {
				if !strings.Contains(rendered, term.MoveTo(0, r)) {
					t.Fatalf("picker=%v %v: stale row %d", withPicker, th, r)
				}
			}
			if !strings.Contains(rendered, background(e.Theme.Palette().Surface)) {
				t.Fatalf("picker=%v %v: no surface background", withPicker, th)
			}
			if idle := draw(t, &v, e, 80, 8); idle != "" {
				t.Fatalf("picker=%v %v: idle frame wrote %q", withPicker, th, idle)
			}
		}
	}
}

// Rust: render::tests::diagnostics_redraw_while_idle_and_preserve_gutter_width_and_prompt_priority
// Parity: DIA, REN.
func TestDiagnosticsRedrawWhileIdleAndPreserveGutterWidthAndPromptPriority(t *testing.T) {
	// t.TempDir() names the test, which on macOS (a long TMPDIR) pushes the language
	// server status off a 200-column status line; the Rust test used a short path.
	e := newEditor(t, filepath.Join(shortDir(t), "test.rs"), "a😀b\nsecond\nthird")
	var v View
	draw(t, &v, e, 200, 6)
	e.LSP.Status = "rust-analyzer"
	e.LSP.Diagnostics = []lsp.Diagnostic{{
		Start: lsp.Position{Row: 0, Col: 1}, End: lsp.Position{Row: 1, Col: 0}, Severity: 2,
		Message: "Unicode warning\nwith control\x1b",
	}}
	out := draw(t, &v, e, 200, 6)
	for _, c := range []struct {
		row  int
		want string
	}{{0, "W"}, {1, "2 "}, {4, "rust-analyzer E:0 W:1"}, {5, "Unicode warning�with control�"}} {
		if !strings.Contains(row(&v, c.row), c.want) {
			t.Fatalf("row %d = %q, want %q", c.row, row(&v, c.row), c.want)
		}
	}
	if !strings.Contains(out, term.MoveTo(2, 0)) {
		t.Fatalf("cursor not placed after the gutter: %q", out)
	}
	if idle := draw(t, &v, e, 200, 6); idle != "" {
		t.Fatalf("idle polling wrote an unchanged frame: %q", idle)
	}
	e.Mode = editor.ModeCommand
	e.Command = "w"
	draw(t, &v, e, 200, 6)
	if row(&v, 5) != ":w" {
		t.Fatalf("row 5 = %q", row(&v, 5))
	}
	e.Mode = editor.ModeNormal
	e.Message = "Save failed"
	draw(t, &v, e, 200, 6)
	if row(&v, 5) != "Save failed" {
		t.Fatalf("row 5 = %q", row(&v, 5))
	}
	e.LSP.Diagnostics = nil
	for _, size := range [][2]int{{1, 1}, {2, 3}, {3, 4}, {200, 6}} {
		draw(t, &v, e, size[0], size[1])
	}
	if !strings.Contains(row(&v, 0), "1 ") {
		t.Fatalf("row 0 = %q", row(&v, 0))
	}
}

// Rust: render::tests::line_numbers_track_scroll_cursor_and_decimal_width_changes
// Parity: REN gutter.
func TestLineNumbersTrackScrollCursorAndDecimalWidthChanges(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "numbers"), strings.TrimSuffix(strings.Repeat("text\n", 9), "\n"))
	var v View
	out := draw(t, &v, e, 12, 5)
	if !strings.Contains(row(&v, 0), "1 ") || !strings.Contains(out, term.MoveTo(2, 0)) {
		t.Fatalf("row 0 %q out %q", row(&v, 0), out)
	}
	e.Buffer.Row = 8
	e.Buffer.OpenLine()
	out = draw(t, &v, e, 12, 5)
	if v.top != 7 || !strings.Contains(row(&v, 0), " 8 ") || !strings.Contains(row(&v, 2), "10 ") ||
		!strings.Contains(out, term.MoveTo(3, 2)) {
		t.Fatalf("top %d row0 %q row2 %q out %q", v.top, row(&v, 0), row(&v, 2), out)
	}
	e.Buffer.DeleteLine()
	draw(t, &v, e, 12, 5)
	if !strings.Contains(row(&v, 0), "8 ") || row(&v, 2) != "~" {
		t.Fatalf("row0 %q row2 %q", row(&v, 0), row(&v, 2))
	}
	// Neither status nor command prompts inherit the number gutter.
	e.Mode = editor.ModeCommand
	e.Command = "w"
	out = draw(t, &v, e, 12, 5)
	if row(&v, 4) != ":w" || !strings.Contains(out, term.MoveTo(2, 4)) {
		t.Fatalf("row 4 %q out %q", row(&v, 4), out)
	}
}

// Rust: render::tests::line_number_gutter_stays_fixed_with_unicode_horizontal_scroll_and_resize
func TestLineNumberGutterStaysFixedWithUnicodeHorizontalScrollAndResize(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "numbers.rs"), "a\t界xyz")
	e.Buffer.Col = 4
	var v View
	out := draw(t, &v, e, 10, 4)
	if v.left != 1 {
		t.Fatalf("left = %d", v.left)
	}
	if r := row(&v, 0); !strings.Contains(r, "1 ") || !strings.Contains(r, "    界xy") {
		t.Fatalf("row 0 = %q", r)
	}
	if !strings.Contains(out, term.MoveTo(9, 0)) {
		t.Fatalf("cursor: %q", out)
	}
	for _, size := range [][2]int{{1, 4}, {2, 4}, {3, 4}, {0, 0}, {80, 4}} {
		draw(t, &v, e, size[0], size[1])
	}
	e.Buffer.Col = 0
	draw(t, &v, e, 80, 4)
	if v.left != 0 || !strings.Contains(row(&v, 0), "1 ") {
		t.Fatalf("left %d row %q", v.left, row(&v, 0))
	}
	if out := draw(t, &v, e, 80, 4); strings.Contains(out, "1 ") {
		t.Fatalf("idle frame rewrote the gutter: %q", out)
	}
}

// Rust: render::tests::welcome_renders_directory_hints_and_commands_across_resizes
// Parity: WEL-02, WEL-03.
func TestWelcomeRendersDirectoryHintsAndCommandsAcrossResizes(t *testing.T) {
	e := newWelcome(t, "/tmp/space café 界")
	var v View
	text := draw(t, &v, e, 80, 24)
	for _, want := range []string{"Coigüe", "Welcome", "/tmp/space café 界", "Ctrl-O", "Ctrl-P", ":q"} {
		if !strings.Contains(text, want) {
			t.Errorf("welcome lacks %q", want)
		}
	}
	for _, size := range [][2]int{{0, 0}, {1, 1}, {12, 2}, {80, 24}} {
		draw(t, &v, e, size[0], size[1])
	}
	e.Mode = editor.ModeCommand
	e.Command = "explore"
	if out := draw(t, &v, e, 80, 24); !strings.Contains(out, ":explore") {
		t.Fatalf("command line missing: %q", out)
	}
	if e.Buffer.Text() != "" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: render::tests::explorer_rename_updates_syntax_without_resetting_cursor_or_viewport
func TestExplorerRenameUpdatesSyntaxWithoutResettingCursorOrViewport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain")
	text := strings.Repeat("fn main() {}\n", 12)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, opened, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := editor.New(t.Context(), f, opened)
	t.Cleanup(e.Close)
	e.Buffer.Row = 7
	var v View
	draw(t, &v, e, 20, 4)
	top := v.top
	if v.styles != nil {
		t.Fatal("a plain file was highlighted")
	}
	e.Key(key.WithCtrl('o'), 2)
	waitLoaded(t, e)
	e.Key(key.Char('r'), 2)
	for _, size := range [][2]int{{1, 1}, {0, 0}, {20, 4}} {
		draw(t, &v, e, size[0], size[1])
	}
	e.Key(key.WithCtrl('u'), 2)
	typeKeys(e, 2, "code.rs")
	e.Key(key.Press(key.Enter), 2)
	e.Key(key.Press(key.Esc), 2)
	draw(t, &v, e, 20, 4)
	if e.Buffer.Row != 7 || v.top != top || v.styles == nil || v.styles[0][0] != syntax.Keyword {
		t.Fatalf("row %d top %d (was %d) styles %v", e.Buffer.Row, v.top, top, v.styles)
	}
}

// Rust: render::tests::explorer_resize_confirmation_and_cancel_preserve_file_viewport
func TestExplorerResizeConfirmationAndCancelPreserveFileViewport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current")
	text := strings.Repeat("long line with characters to scroll\n", 12)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, opened, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := editor.New(t.Context(), f, opened)
	t.Cleanup(e.Close)
	e.Buffer.Row = 5
	e.Buffer.Col = 20
	var v View
	draw(t, &v, e, 10, 4)
	position := [2]int{v.top, v.left}
	for _, c := range "rZ-" {
		e.Key(key.Char(c), 2)
	}
	waitLoaded(t, e)
	for _, size := range [][2]int{{0, 0}, {1, 1}, {16, 2}, {80, 6}} {
		if out := draw(t, &v, e, size[0], size[1]); strings.Contains(out, term.ClearAll) {
			t.Fatalf("%v cleared the whole screen", size)
		}
	}
	e.Key(key.Press(key.Enter), 2)
	if out := draw(t, &v, e, 80, 6); !strings.Contains(out, "[s] save/open") {
		t.Fatalf("confirmation missing: %q", out)
	}
	e.Key(key.Char('c'), 2)
	e.Key(key.Press(key.Esc), 2)
	draw(t, &v, e, 10, 4)
	if [2]int{v.top, v.left} != position || e.Buffer.Row != 5 || e.Buffer.Col != 20 || !v.dirty {
		t.Fatalf("view %v cursor (%d,%d) dirty %v, want %v (5,20)", [2]int{v.top, v.left}, e.Buffer.Row, e.Buffer.Col, v.dirty, position)
	}
}

// Rust: render::tests::undo_redo_invalidates_multiline_highlighting_and_dirty_status
func TestUndoRedoInvalidatesMultilineHighlightingAndDirtyStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.rs")
	if err := os.WriteFile(path, []byte("/*\nfn main() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, opened, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := editor.New(t.Context(), f, opened)
	t.Cleanup(e.Close)
	var v View
	draw(t, &v, e, 80, 4)
	if v.dirty {
		t.Fatal("fresh file is dirty")
	}
	e.Key(key.Char('x'), 2)
	draw(t, &v, e, 80, 4)
	if v.styles[1][0] != syntax.Keyword || !v.dirty {
		t.Fatalf("after x: %v dirty %v", v.styles[1], v.dirty)
	}
	e.Key(key.Char('u'), 2)
	draw(t, &v, e, 80, 4)
	if v.styles[1][0] != syntax.Comment || v.dirty {
		t.Fatalf("after undo: %v dirty %v", v.styles[1], v.dirty)
	}
	e.Key(key.WithCtrl('r'), 2)
	draw(t, &v, e, 80, 4)
	if v.styles[1][0] != syntax.Keyword || !v.dirty {
		t.Fatalf("after redo: %v dirty %v", v.styles[1], v.dirty)
	}
}

// Rust: render::tests::long_unicode_search_prompt_scrolls_and_cancel_restores_message_row
func TestLongUnicodeSearchPromptScrollsAndCancelRestoresMessageRow(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "fixture"), "text")
	e.Mode = editor.ModeSearch
	e.Search = "界éabcdefghijkl"
	var v View
	out := draw(t, &v, e, 6, 4)
	if !strings.Contains(out, "hijkl") || !strings.Contains(out, term.MoveTo(5, 3)) {
		t.Fatalf("prompt: %q", out)
	}
	e.Key(key.Press(key.Esc), 2)
	out = draw(t, &v, e, 6, 4)
	if !strings.Contains(out, term.ClearUntilNewLine) || e.Mode != editor.ModeNormal || e.Buffer.Text() != "text" {
		t.Fatalf("after cancel: %q mode %v", out, e.Mode)
	}
}

// Rust: render::tests::picker_cancel_and_file_switch_reset_rendering_correctly
func TestPickerCancelAndFileSwitchResetRenderingCorrectly(t *testing.T) {
	dir := t.TempDir()
	e := newEditor(t, filepath.Join(dir, "old.rs"), "/*\ncomment\nstill comment")
	e.Buffer.Row = 2
	var v View
	draw(t, &v, e, 80, 4)
	if v.top != 1 {
		t.Fatalf("top = %d", v.top)
	}
	e.Picker = picker.New(t.Context(), dir)
	e.Picker.Query = "absent"
	for _, size := range [][2]int{{80, 4}, {1, 1}, {16, 2}, {0, 0}, {80, 4}} {
		draw(t, &v, e, size[0], size[1])
	}
	e.Picker.Close()
	e.Picker = nil
	out := draw(t, &v, e, 80, 4)
	if v.top != 1 || !strings.Contains(out, "still comment") {
		t.Fatalf("after cancel: top %d out %q", v.top, out)
	}
	e.Generation++
	e.Buffer = buffer.New("fn new() {}")
	out = draw(t, &v, e, 80, 4)
	if v.top != 0 || v.styles[0][0] != syntax.Keyword || !strings.Contains(out, "new()") {
		t.Fatalf("after switch: top %d styles %v out %q", v.top, v.styles[0], out)
	}
}

// Rust: render::tests::redraws_only_changed_rows_and_clears_stale_tails
// Parity: TRM-11, TRM-13.
func TestRedrawsOnlyChangedRowsAndClearsStaleTails(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "new"), "abcdef\nsecond\nthird")
	var v View
	out := draw(t, &v, e, 80, 4)
	if v.PageRows() != 2 || strings.Contains(out, term.ClearAll) {
		t.Fatalf("page rows %d out %q", v.PageRows(), out)
	}
	e.Buffer.Col = 1
	out = draw(t, &v, e, 80, 4)
	if strings.Contains(out, "abcdef") || strings.Contains(out, "second") {
		t.Fatalf("unchanged rows rewritten: %q", out)
	}
	e.Buffer.Col = 0
	e.Buffer.Delete()
	out = draw(t, &v, e, 80, 4)
	if !strings.Contains(out, "bcdef") || !strings.Contains(out, term.ClearUntilNewLine) || !v.dirty {
		t.Fatalf("after delete: %q dirty %v", out, v.dirty)
	}
	if err := e.File.Save(e.Buffer.Text()); err != nil {
		t.Fatal(err)
	}
	draw(t, &v, e, 80, 4)
	if v.dirty {
		t.Fatal("still dirty after save")
	}
	e.Buffer.Row = 2
	out = draw(t, &v, e, 80, 4)
	if v.top != 1 || !strings.Contains(out, "third") {
		t.Fatalf("top %d out %q", v.top, out)
	}
	for _, size := range [][2]int{{1, 1}, {0, 0}, {16, 8}, {80, 4}} {
		draw(t, &v, e, size[0], size[1])
	}
	if v.PageRows() != 2 {
		t.Fatalf("page rows = %d", v.PageRows())
	}
}

// Rust: render::tests::highlighting_cache_tracks_edits_in_multiline_state
func TestHighlightingCacheTracksEditsInMultilineState(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "new.rs"), "/*\nfn main() {}\n*/")
	var v View
	draw(t, &v, e, 80, 4)
	if v.styles[1][0] != syntax.Comment {
		t.Fatalf("styles = %v", v.styles[1])
	}
	e.Buffer.Row = 1
	draw(t, &v, e, 80, 4)
	if *v.revision != [2]uint64{0, 0} {
		t.Fatalf("revision = %v", *v.revision)
	}
	e.Buffer.Row = 0
	e.Buffer.DeleteLine()
	draw(t, &v, e, 80, 4)
	if v.styles[0][0] != syntax.Keyword {
		t.Fatalf("styles = %v", v.styles[0])
	}
}

// Rust: render::tests::clipping_controls_and_wide
func TestClippingControlsAndWide(t *testing.T) {
	if got := clipped([]rune("a界b"), 2, 2); got != " b" {
		t.Fatalf("clipped = %q", got)
	}
	if got := clipped([]rune("\x1b"), 0, 10); got != "�" {
		t.Fatalf("clipped = %q", got)
	}
}

// Rust: render::styled_tests::clipped_markdown_emphasis_resets_attributes_before_the_next_row
func TestClippedMarkdownEmphasisResetsAttributesBeforeTheNextRow(t *testing.T) {
	for _, c := range []struct {
		style          syntax.Style
		enabled, reset string
	}{
		{syntax.Emphasis, term.Italic, term.NoItalic},
		{syntax.Strong, term.Bold, term.NormalIntensity},
	} {
		line := []rune("\t界é")
		styles := make([]syntax.Style, len(line))
		for i := range styles {
			styles[i] = c.style
		}
		var out []byte
		styledLine(&out, line, styles, 5, 2, theme.Terminal.Palette(), nil, false)
		rendered := string(out)
		if !strings.Contains(rendered, c.enabled+" é"+c.reset) || !strings.HasSuffix(rendered, term.ResetForeground) {
			t.Fatalf("style %v: %q", c.style, rendered)
		}
	}
}

// Rust: render::styled_tests::styled_clipping_preserves_cells_and_resets
func TestStyledClippingPreservesCellsAndResets(t *testing.T) {
	line := []rune("\t界é\x1b")
	styles := make([]syntax.Style, len(line))
	for i := range styles {
		styles[i] = syntax.Keyword
	}
	var out []byte
	styledLine(&out, line, styles, 5, 4, theme.Terminal.Palette(), nil, false)
	rendered := string(out)
	if !strings.Contains(rendered, " é�") || !strings.HasSuffix(rendered, term.ResetForeground) {
		t.Fatalf("rendered = %q", rendered)
	}
}

// Parity: THM-10 / ODD-19. With NO_COLOR set, crossterm turned every colour
// command into a bare "ESC[m" while still sending attribute commands.
func TestNoColorReducesColourCommandsToABareReset(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "t.rs"), "fn main() {}")
	var v View
	v.SetNoColor(true)
	out := draw(t, &v, e, 40, 4)
	if strings.Contains(out, "38;2;") || strings.Contains(out, "48;2;") || strings.Contains(out, "38;5;") {
		t.Fatalf("colour leaked with NO_COLOR: %q", out)
	}
	if !strings.Contains(out, "\x1b[m") {
		t.Fatalf("no bare reset in %q", out)
	}
	typeKeys(e, 4, "v")
	if out := draw(t, &v, e, 40, 4); !strings.Contains(out, term.Reverse) {
		t.Fatalf("attributes are still sent: %q", out)
	}
}

func TestSchemeConfigColoursRenderWithoutLanguageServer(t *testing.T) {
	e := newEditor(t, filepath.Join(t.TempDir(), "init.scm"), "; Configuration\n(define title \"hello 界\")\n(set-theme \"mocha\")")
	var view View
	e.Theme = theme.CatppuccinMocha
	rendered := draw(t, &view, e, 80, 8)
	if view.styles[0][0] != syntax.Comment || view.styles[1][1] != syntax.Keyword || view.styles[1][14] != syntax.String {
		t.Fatal("Scheme lexer was not used", view.styles)
	}
	want := foreground(e.Theme.Palette().Syntax(syntax.String)) + "\"hello 界\""
	if !strings.Contains(rendered, want) {
		t.Fatalf("missing rendered Scheme string colour %q in %q", want, rendered)
	}
	e.Buffer.Row = 0
	typeKeys(e, 6, "dd")
	draw(t, &view, e, 80, 8)
	if view.styles[0][1] != syntax.Keyword {
		t.Fatal("highlighting did not follow edit")
	}
	e.Key(key.Char('u'), 6)
	draw(t, &view, e, 80, 8)
	if view.styles[0][0] != syntax.Comment {
		t.Fatal("highlighting did not follow undo")
	}
}
