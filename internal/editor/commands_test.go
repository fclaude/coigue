package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/theme"
)

// Rust: editor::tests::justify_commands_width_history_and_completion
// Parity: RFL, CMD :justify/:textwidth.
func TestJustifyCommandsWidthHistoryAndCompletion(t *testing.T) {
	_, e := editingFixture(t, "one two three four\n\nother paragraph")
	send(e, ":textwidth 10")
	press(e, key.Enter)
	send(e, "gq")
	if e.Buffer.Text() != "one two\nthree four\n\nother paragraph" {
		t.Fatalf("filled = %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "one two three four\n\nother paragraph" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	e.Key(key.WithCtrl('r'), 22)
	if e.Buffer.Text() != "one two\nthree four\n\nother paragraph" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	send(e, ":justify 80")
	press(e, key.Enter)
	if e.Buffer.Text() != "one two three four\n\nother paragraph" {
		t.Fatalf("justify 80 = %q", e.Buffer.Text())
	}
	for _, bad := range []string{"textwidth 0", "justify nope", "justify 10 extra", "textwidth 10001"} {
		e.execute(bad)
		if !strings.HasPrefix(e.Message, "Usage:") {
			t.Errorf("%q: message %q", bad, e.Message)
		}
	}
	if e.textwidth != 10 {
		t.Fatalf("textwidth = %d", e.textwidth)
	}
	send(e, ":ju")
	press(e, key.Tab)
	if e.Command != "justify" {
		t.Fatalf("command = %q", e.Command)
	}
}

// Rust: editor::tests::command_completion_cycles_both_directions_without_executing
func TestCommandCompletionCyclesBothDirectionsWithoutExecuting(t *testing.T) {
	_, e := editingFixture(t, "original")
	send(e, "i!")
	press(e, key.Esc)
	revision := e.Buffer.Revision()
	send(e, ":w")
	for _, expected := range []string{"w", "wq", "workspace-symbols", "w", "wq"} {
		if e.Key(key.Press(key.Tab), 22) {
			t.Fatal("Tab quit the editor")
		}
		if e.Command != expected {
			t.Fatalf("command = %q, want %q", e.Command, expected)
		}
	}
	e.Key(key.New(key.BackTab, key.Shift), 22)
	if e.Command != "w" {
		t.Fatalf("command = %q", e.Command)
	}
	e.Key(key.New(key.Tab, key.Shift), 22)
	if e.Command != "workspace-symbols" {
		t.Fatalf("command = %q", e.Command)
	}
	if e.Mode != ModeCommand || e.Buffer.Revision() != revision || !e.File.Dirty(e.Buffer.Text()) {
		t.Fatalf("mode %v revision %d dirty %v", e.Mode, e.Buffer.Revision(), e.File.Dirty(e.Buffer.Text()))
	}
	press(e, key.Esc)
	send(e, ":q")
	for i := 0; i < 2; i++ {
		if e.Key(key.Press(key.Tab), 22) {
			t.Fatal("Tab quit the editor")
		}
	}
	if e.Command != "q!" {
		t.Fatalf("command = %q", e.Command)
	}
	if !e.Key(key.Press(key.Enter), 22) {
		t.Fatal("Enter on :q! did not quit")
	}
}

// Rust: editor::tests::command_completion_covers_commands_and_resets_after_input_or_cancel
func TestCommandCompletionCoversCommandsAndResetsAfterInputOrCancel(t *testing.T) {
	e := newWelcome(t, t.TempDir())
	send(e, ":")
	expected := []string{
		"actions", "back", "clipboard-copy", "clipboard-paste", "clipboard-paste-before", "code",
		"definition", "explore", "files", "forward", "help", "justify", "lsp-restart", "open",
		"project-search", "q", "q!", "recent", "references", "symbols", "textwidth", "theme",
		"w", "wq", "workspace-symbols", "actions",
	}
	for _, want := range expected {
		press(e, key.Tab)
		if e.Command != want {
			t.Fatalf("command = %q, want %q", e.Command, want)
		}
	}
	press(e, key.Esc)
	send(e, ":")
	press(e, key.BackTab)
	if e.Command != "workspace-symbols" {
		t.Fatalf("command = %q", e.Command)
	}
	e.Command = "wq"
	e.commandCompletion = nil
	press(e, key.Backspace)
	press(e, key.Tab)
	if e.Command != "w" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Tab)
	if e.Command != "wq" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Esc)
	send(e, ":the")
	press(e, key.Tab)
	if e.Command != "theme" {
		t.Fatalf("command = %q", e.Command)
	}
	send(e, " lat")
	press(e, key.Tab)
	if e.Command != "theme latte" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Enter)
	if e.Theme != theme.CatppuccinLatte {
		t.Fatalf("theme = %v", e.Theme)
	}
	send(e, ":exp")
	press(e, key.Tab)
	if e.Command != "explore" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Enter)
	if e.Explorer == nil || !e.IsWelcome() {
		t.Fatal("explore on welcome did not open the explorer")
	}
}

// Rust: editor::tests::theme_completion_handles_aliases_whitespace_and_ambiguous_prefixes
func TestThemeCompletionHandlesAliasesWhitespaceAndAmbiguousPrefixes(t *testing.T) {
	_, e := editingFixture(t, "original")
	for _, c := range []struct{ input, expected string }{
		{":theme ", "theme mocha"},
		{":theme catppuccin-l", "theme catppuccin-latte"},
		{":theme gruvbox-d", "theme gruvbox-dark"},
		{":  theme  term", "  theme  terminal"},
		{": theme lat", " theme latte"},
	} {
		send(e, c.input)
		press(e, key.Tab)
		if e.Command != c.expected {
			t.Fatalf("%q completed to %q, want %q", c.input, e.Command, c.expected)
		}
		press(e, key.Esc)
	}
	send(e, ":theme m")
	for _, expected := range []string{"theme mocha", "theme monochrome", "theme mocha"} {
		press(e, key.Tab)
		if e.Command != expected {
			t.Fatalf("command = %q, want %q", e.Command, expected)
		}
	}
	press(e, key.BackTab)
	if e.Command != "theme monochrome" {
		t.Fatalf("command = %q", e.Command)
	}
	// Completion does not apply a theme until Enter.
	if e.Theme != theme.CatppuccinMocha {
		t.Fatalf("theme applied early: %v", e.Theme)
	}
	press(e, key.Enter)
	if e.Theme != theme.Monochrome {
		t.Fatalf("theme = %v", e.Theme)
	}
	for _, name := range theme.Completions() {
		if _, ok := theme.Parse(name); !ok {
			t.Errorf("completion %q does not parse", name)
		}
	}
}

// Rust: editor::tests::completion_leaves_unmatched_commands_numbers_and_other_modes_alone
func TestCompletionLeavesUnmatchedCommandsNumbersAndOtherModesAlone(t *testing.T) {
	_, e := editingFixture(t, "original")
	for _, input := range []string{":unknown", ":42", ":thé", ":theme nope", ":theme mocha extra", ":open path"} {
		send(e, input)
		press(e, key.Tab)
		press(e, key.BackTab)
		if e.Command != input[1:] {
			t.Fatalf("%q changed the command line to %q", input, e.Command)
		}
		press(e, key.Esc)
	}
	send(e, "/the")
	press(e, key.Tab)
	if e.Search != "the" {
		t.Fatalf("search = %q", e.Search)
	}
	press(e, key.Esc)
	send(e, "i")
	press(e, key.Tab)
	if e.Buffer.Text() != "\toriginal" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	press(e, key.Esc)
	send(e, "u")
	if e.Buffer.Text() != "original" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::theme_commands_preserve_edits_history_and_selection_across_files
func TestThemeCommandsPreserveEditsHistoryAndSelectionAcrossFiles(t *testing.T) {
	dir, e := editingFixture(t, "first\nsecond")
	if e.Theme != theme.CatppuccinMocha {
		t.Fatalf("default theme = %v", e.Theme)
	}
	send(e, "jA!")
	press(e, key.Esc)
	position := cursor(e)
	revision := e.Buffer.Revision()
	for _, c := range []struct {
		name string
		want theme.Theme
	}{
		{"gruvbox-dark", theme.GruvboxDark},
		{"gruvbox", theme.GruvboxDark},
		{"catppuccin-latte", theme.CatppuccinLatte},
		{"latte", theme.CatppuccinLatte},
		{"monochrome", theme.Monochrome},
		{"terminal", theme.Terminal},
		{"catppuccin-mocha", theme.CatppuccinMocha},
		{"mocha", theme.CatppuccinMocha},
	} {
		send(e, ":theme "+c.name)
		if e.Key(key.Press(key.Enter), 22) {
			t.Fatal("theme command quit")
		}
		if e.Theme != c.want || e.Message != "Theme: "+c.want.Name() || e.Mode != ModeNormal ||
			cursor(e) != position || e.Buffer.Revision() != revision || !e.File.Dirty(e.Buffer.Text()) {
			t.Fatalf("after :theme %s: theme %v message %q mode %v", c.name, e.Theme, e.Message, e.Mode)
		}
	}
	send(e, ":theme latte")
	press(e, key.Esc)
	if e.Theme != theme.CatppuccinMocha {
		t.Fatalf("cancelled command changed the theme to %v", e.Theme)
	}
	send(e, "u")
	if e.Buffer.Text() != "first\nsecond" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	e.execute("theme latte")
	second := filepath.Join(dir, "other")
	write(t, second, "other")
	e.requestOpen(second)
	if e.Theme != theme.CatppuccinLatte {
		t.Fatalf("theme after switching files = %v", e.Theme)
	}
	if got := read(t, filepath.Join(dir, "fixture")); got != "first\nsecond" {
		t.Fatalf("fixture = %q", got)
	}
}

// Rust: editor::tests::themes_work_on_welcome_reject_bad_arguments_and_reset_on_new_sessions
func TestThemesWorkOnWelcomeRejectBadArgumentsAndResetOnNewSessions(t *testing.T) {
	dir := t.TempDir()
	e := newWelcome(t, dir)
	send(e, ":theme gruvbox")
	press(e, key.Enter)
	if e.Theme != theme.GruvboxDark {
		t.Fatalf("theme = %v", e.Theme)
	}
	e.execute("theme")
	for _, name := range []string{"gruvbox-dark", "mocha", "gruvbox", "latte", "monochrome", "terminal"} {
		if !contains(e.Message, name) {
			t.Errorf("message %q lacks %q", e.Message, name)
		}
	}
	for _, command := range []string{"theme unknown", "theme latte extra", "themefoo latte"} {
		e.execute(command)
		if e.Theme != theme.GruvboxDark || !(strings.HasPrefix(e.Message, "Unknown") || strings.HasPrefix(e.Message, "Usage:")) {
			t.Errorf("%q: theme %v message %q", command, e.Theme, e.Message)
		}
	}
	e.execute("  theme\tlatte  ")
	if e.Theme != theme.CatppuccinLatte {
		t.Fatalf("theme = %v", e.Theme)
	}
	if e.Buffer.Text() != "" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("theme command created files: %v", entries)
	}
	if got := newWelcome(t, dir).Theme; got != theme.CatppuccinMocha {
		t.Fatalf("new session theme = %v", got)
	}
}
