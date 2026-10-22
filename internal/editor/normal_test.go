package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/key"
)

// Rust: editor::tests::counts_move_jump_cancel_and_do_not_leak_between_modes
// Parity: NRM-01..05, NRM-12, NRM-13, NRM-15, NRM-20, INS-04.
func TestCountsMoveJumpCancelAndDoNotLeakBetweenModes(t *testing.T) {
	_, e := editingFixture(t, strings.Repeat("abcdef\n", 30))
	send(e, "12j3l")
	if cursor(e) != [2]int{12, 3} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, "0")
	if e.Buffer.Col != 0 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	send(e, "10gg")
	if e.Buffer.Row != 9 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	send(e, "1G")
	if e.Buffer.Row != 0 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	send(e, "999999999999999999999999999j")
	if e.Buffer.Row != 30 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	send(e, "gg12d3")
	if e.PendingInput() != "12d3" {
		t.Fatalf("pending = %q", e.PendingInput())
	}
	press(e, key.Esc)
	send(e, "j")
	if e.Buffer.Row != 1 || e.PendingInput() != "" {
		t.Fatalf("row %d pending %q", e.Buffer.Row, e.PendingInput())
	}
	send(e, "20")
	ctrl(e, 'p')
	press(e, key.Esc)
	send(e, "j")
	if e.Buffer.Row != 2 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	send(e, "9i123")
	press(e, key.Esc)
	if !contains(e.Buffer.Text(), "123abcdef") {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "/12")
	if e.Search != "12" {
		t.Fatalf("search = %q", e.Search)
	}
	press(e, key.Esc)
	send(e, ":123")
	if e.Command != "123" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Esc)
	send(e, "4")
	press(e, key.Down)
	if e.Buffer.Row != 6 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	send(e, "2")
	ctrl(e, 'f')
	if e.Buffer.Row != 30 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
}

// Rust: editor::tests::counted_line_registers_and_indentation_undo_as_one_edit
// Parity: NRM-08, NRM-10, REG-02, UND-01, UND-13.
func TestCountedLineRegistersAndIndentationUndoAsOneEdit(t *testing.T) {
	const original = "a\nb\nc\nd\ne"
	_, e := editingFixture(t, original)
	check := func(want string) {
		t.Helper()
		if got := e.Buffer.Text(); got != want {
			t.Fatalf("text = %q, want %q", got, want)
		}
	}
	send(e, "2dd")
	check("c\nd\ne")
	send(e, "p")
	check("c\na\nb\nd\ne")
	send(e, "uu")
	check(original)
	send(e, "2yyG2p")
	check("a\nb\nc\nd\ne\na\nb\na\nb")
	send(e, "u")
	check(original)
	send(e, "gg4>>")
	check("    a\n    b\n    c\n    d\ne")
	send(e, "u")
	check(original)
	redo(e)
	send(e, "2<2<")
	check(original)
	send(e, "G999dd")
	check("a\nb\nc\nd")
	send(e, "u")
	check(original)
	send(e, "gg999ddP")
	check(original)
}

// Rust: editor::tests::counted_words_characters_replacement_and_change_preserve_registers
// Parity: NRM-06, NRM-07, NRM-09, NRM-11, REG-01.
func TestCountedWordsCharactersReplacementAndChangePreserveRegisters(t *testing.T) {
	const original = "one two three four five six seven"
	_, e := editingFixture(t, original)
	check := func(want string) {
		t.Helper()
		if got := e.Buffer.Text(); got != want {
			t.Fatalf("text = %q, want %q", got, want)
		}
	}
	send(e, "2d3w")
	check("seven")
	send(e, "P")
	check(original)
	send(e, "uu2w")
	if e.Buffer.Col != 8 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	send(e, "2b2ywG$p")
	check(original + "one two ")
	send(e, "u0")
	send(e, "2cwNEW")
	press(e, key.Esc)
	check("NEW three four five six seven")
	send(e, "u0")
	check(original)
	send(e, "3xP")
	check(original)
	send(e, "uu2r3")
	check("33e two three four five six seven")
	send(e, "u0$999x")
	check("one two three four five six seve")
	send(e, "u")
	check(original)
	_, empty := editingFixture(t, "")
	send(empty, "999x999dw999p")
	if empty.Buffer.Text() != "" || empty.Buffer.Undo() {
		t.Fatalf("empty buffer edited: %q", empty.Buffer.Text())
	}
}

// Rust: editor::tests::go_to_line_command_jumps_clamps_cancels_and_preserves_history
// Parity: CMD :N, NAV.
func TestGoToLineCommandJumpsClampsCancelsAndPreservesHistory(t *testing.T) {
	_, e := editingFixture(t, "first\n界 second\n\nlast\n")
	send(e, "i!")
	press(e, key.Esc)
	send(e, ":2")
	if e.Buffer.Row != 0 {
		t.Fatalf("row = %d before Enter", e.Buffer.Row)
	}
	press(e, key.Enter)
	if e.Mode != ModeNormal || cursor(e) != [2]int{1, 0} {
		t.Fatalf("mode %v cursor %v", e.Mode, cursor(e))
	}
	send(e, ":3")
	press(e, key.Enter)
	if cursor(e) != [2]int{2, 0} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, ":999999")
	press(e, key.Enter)
	if cursor(e) != [2]int{4, 0} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, ":1")
	press(e, key.Esc)
	if e.Buffer.Row != 4 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	for _, invalid := range []string{"0", "9999999999999999999999999999999999999999", "-1", "2x"} {
		if e.execute(invalid) || cursor(e) != [2]int{4, 0} || e.Message == "" {
			t.Fatalf("%q: cursor %v message %q", invalid, cursor(e), e.Message)
		}
	}
	e.execute("  0002  ")
	if cursor(e) != [2]int{1, 0} || e.Message != "" {
		t.Fatalf("padded: cursor %v message %q", cursor(e), e.Message)
	}
	if e.Buffer.Text() != "!first\n界 second\n\nlast\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "first\n界 second\n\nlast\n" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	redo(e)
	if e.Buffer.Text() != "!first\n界 second\n\nlast\n" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	_, empty := editingFixture(t, "")
	empty.execute("100")
	if cursor(empty) != [2]int{0, 0} {
		t.Fatalf("empty cursor = %v", cursor(empty))
	}
	welcome := newWelcome(t, t.TempDir())
	send(welcome, ":2")
	press(welcome, key.Enter)
	if welcome.Message != "No file open" || !welcome.IsWelcome() {
		t.Fatalf("welcome: %q", welcome.Message)
	}
}

// Rust: editor::tests::counted_undo_redo_and_search_repeat_without_editing
// Parity: UND-08, SRC.
func TestCountedUndoRedoAndSearchRepeatWithoutEditing(t *testing.T) {
	_, e := editingFixture(t, "a a a a")
	send(e, "xx2u")
	if e.Buffer.Text() != "a a a a" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "2")
	ctrl(e, 'r')
	if e.Buffer.Text() != "a a a" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	send(e, "2u/a")
	press(e, key.Enter)
	if e.Buffer.Col != 2 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	send(e, "2n")
	if e.Buffer.Col != 6 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	send(e, "3N")
	if e.Buffer.Col != 0 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	if e.Buffer.Text() != "a a a a" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::counted_unicode_ranges_roundtrip_crlf_and_multiple_lines
// Parity: BUF-02, REG-03.
func TestCountedUnicodeRangesRoundtripCrlfAndMultipleLines(t *testing.T) {
	const original = "café 界\r\n\nlast end"
	_, e := editingFixture(t, original)
	send(e, "3dwP")
	if e.Buffer.Text() != original {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "uu2yyG2p")
	if want := "café 界\r\n\nlast end\ncafé 界\r\n\ncafé 界\r\n"; e.Buffer.Text() != want {
		t.Fatalf("text = %q, want %q", e.Buffer.Text(), want)
	}
	send(e, "u")
	if e.Buffer.Text() != original {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::word_edits_register_history_and_modes
// Parity: BUF-13..17, NRM-09, UND-02.
func TestWordEditsRegisterHistoryAndModes(t *testing.T) {
	_, e := editingFixture(t, "café_2!! \t\r\n\n界 end")
	send(e, "we")
	if cursor(e) != [2]int{0, 7} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, "wb")
	if cursor(e) != [2]int{0, 6} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, "ggyw")
	if text, ok := regChars(e); !ok || text != "café_2" {
		t.Fatalf("register = %q", text)
	}
	send(e, "u")
	if !contains(e.Message, "Nothing to undo") {
		t.Fatalf("message = %q", e.Message)
	}
	send(e, "wwdw")
	if e.Buffer.Text() != "café_2!! \t\r\n\nend" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	if text, _ := regChars(e); text != "界 " {
		t.Fatalf("register = %q", text)
	}
	send(e, "u")
	send(e, "ggwdw")
	if text, _ := regChars(e); text != "!! \t\r\n\n" {
		t.Fatalf("register = %q", text)
	}
	send(e, "P")
	if e.Buffer.Text() != "café_2!! \t\r\n\n界 end" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "ggcwnew")
	press(e, key.Esc)
	if e.Buffer.Text() != "new!! \t\r\n\n界 end" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "café_2!! \t\r\n\n界 end" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	redo(e)
	if e.Buffer.Text() != "new!! \t\r\n\n界 end" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	send(e, "G$dw")
	if e.Buffer.Text() != "new!! \t\r\n\n界 en" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "icwbywe")
	press(e, key.Esc)
	if !contains(e.Buffer.Text(), "cwbywe") {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, ":dwcwbywe")
	if e.Command != "dwcwbywe" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Esc)
	send(e, "/dwcwbywe")
	if e.Search != "dwcwbywe" {
		t.Fatalf("search = %q", e.Search)
	}
}

// Rust: editor::tests::word_bindings_stay_out_of_explorer_and_prompts
func TestWordBindingsStayOutOfExplorerAndPrompts(t *testing.T) {
	_, e := editingFixture(t, "original words")
	ctrl(e, 'o')
	readyExplorer(t, e)
	send(e, "wbecw")
	if e.Buffer.Text() != "original words" || e.Mode != ModeNormal {
		t.Fatalf("text %q mode %v", e.Buffer.Text(), e.Mode)
	}
	send(e, "/dwcwbywe")
	if e.Explorer.Query != "dwcwbywe" {
		t.Fatalf("explorer query = %q", e.Explorer.Query)
	}
	press(e, key.Esc)
	press(e, key.Esc)
	e.FilesystemPrompt = &FilesystemPrompt{Action: CreateFile}
	send(e, "dwcwbywe")
	if e.FilesystemPrompt.Input != "dwcwbywe" || e.Buffer.Text() != "original words" ||
		e.register != nil || e.Buffer.Revision() != 0 {
		t.Fatalf("prompt %q text %q register %v revision %d", e.FilesystemPrompt.Input, e.Buffer.Text(), e.register, e.Buffer.Revision())
	}
}

// Rust: editor::tests::word_empty_whitespace_and_change_spacing
func TestWordEmptyWhitespaceAndChangeSpacing(t *testing.T) {
	for _, text := range []string{"", "\n", "  \n\n", "é", "!!"} {
		_, e := editingFixture(t, text)
		send(e, "webwebggdw")
		send(e, "u")
		if e.Buffer.Text() != text {
			t.Errorf("%q became %q", text, e.Buffer.Text())
		}
	}
	_, e := editingFixture(t, "one  two")
	send(e, "cwX")
	press(e, key.Esc)
	if e.Buffer.Text() != "X  two" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "lcwY")
	press(e, key.Esc)
	if e.Buffer.Text() != "XYtwo" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::undo_groups_insert_sessions_including_open_append_and_newlines
// Parity: UND-02, UND-05.
func TestUndoGroupsInsertSessionsIncludingOpenAppendAndNewlines(t *testing.T) {
	_, e := editingFixture(t, "é界\n")
	send(e, "lAhello")
	press(e, key.Enter)
	send(e, "world")
	press(e, key.Backspace)
	press(e, key.Esc)
	if e.Buffer.Text() != "é界hello\nworl\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	edited := e.Buffer.Text()
	send(e, "u")
	if e.Buffer.Text() != "é界\n" || cursor(e) != [2]int{0, 1} || e.File.Dirty(e.Buffer.Text()) {
		t.Fatalf("undo: %q at %v", e.Buffer.Text(), cursor(e))
	}
	redo(e)
	if e.Buffer.Text() != edited {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	send(e, "u")
	send(e, "oopened")
	press(e, key.Esc)
	if e.Buffer.Text() != "é界\nopened\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "é界\n" || cursor(e) != [2]int{0, 1} {
		t.Fatalf("undo: %q at %v", e.Buffer.Text(), cursor(e))
	}
	redo(e)
	if e.Buffer.Text() != "é界\nopened\n" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::undo_redo_each_normal_edit_and_register_remains_usable
// Parity: UND-01, UND-06, UND-08, REG-04.
func TestUndoRedoEachNormalEditAndRegisterRemainsUsable(t *testing.T) {
	_, e := editingFixture(t, "é🦀\nsecond\n")
	states := []string{e.Buffer.Text()}
	for _, command := range []string{"x", "rQ", ">>", "<<", "dd", "P", "p"} {
		send(e, command)
		states = append(states, e.Buffer.Text())
	}
	for i := len(states) - 2; i >= 0; i-- {
		send(e, "u")
		if e.Buffer.Text() != states[i] {
			t.Fatalf("undo to state %d: %q, want %q", i, e.Buffer.Text(), states[i])
		}
	}
	send(e, "u")
	if !contains(e.Message, "Nothing to undo") {
		t.Fatalf("message = %q", e.Message)
	}
	for _, text := range states[1:] {
		redo(e)
		if e.Buffer.Text() != text {
			t.Fatalf("redo: %q, want %q", e.Buffer.Text(), text)
		}
	}
	redo(e)
	if !contains(e.Message, "Nothing to redo") {
		t.Fatalf("message = %q", e.Message)
	}
	// Undo does not discard the cut line in the register.
	send(e, "up")
	if e.Buffer.Text() != states[len(states)-1] {
		t.Fatalf("register after undo: %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::redo_survives_non_edits_but_is_cleared_by_a_new_edit
// Parity: UND-03, UND-04, UND-09.
func TestRedoSurvivesNonEditsButIsClearedByANewEdit(t *testing.T) {
	_, e := editingFixture(t, "abc")
	send(e, "xu")
	send(e, "yyj0/abc")
	press(e, key.Enter)
	send(e, "r")
	press(e, key.Esc)
	send(e, "ra<<") // Replacement and unindent both leave text unchanged.
	send(e, "iZ")
	press(e, key.Backspace) // A net-zero insert session creates no history entry.
	press(e, key.Esc)
	redo(e)
	if e.Buffer.Text() != "bc" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	send(e, "u")
	send(e, "rQ")
	redo(e)
	if e.Buffer.Text() != "Qbc" || !contains(e.Message, "Nothing to redo") {
		t.Fatalf("text %q message %q", e.Buffer.Text(), e.Message)
	}
	// The literal replacement key u must not trigger undo.
	send(e, "ru")
	if e.Buffer.Text() != "ubc" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::save_preserves_history_and_dirty_checks_follow_restored_contents
// Parity: UND-10, FIL.
func TestSavePreservesHistoryAndDirtyChecksFollowRestoredContents(t *testing.T) {
	_, e := editingFixture(t, "original\n")
	send(e, "A!")
	press(e, key.Esc)
	if e.execute("w") || e.File.Dirty(e.Buffer.Text()) {
		t.Fatal("save left the buffer dirty")
	}
	send(e, "u")
	if e.Buffer.Text() != "original\n" || !e.File.Dirty(e.Buffer.Text()) {
		t.Fatalf("undo: %q dirty %v", e.Buffer.Text(), e.File.Dirty(e.Buffer.Text()))
	}
	if e.execute("q") {
		t.Fatal("quit with a buffer that differs from disk")
	}
	if got := read(t, e.File.Path); got != "original!\n" {
		t.Fatalf("disk = %q", got)
	}
	redo(e)
	if e.File.Dirty(e.Buffer.Text()) {
		t.Fatal("redo to the saved text is dirty")
	}
	send(e, "u")
	if e.execute("w") {
		t.Fatal("write quit")
	}
	redo(e)
	if !e.File.Dirty(e.Buffer.Text()) {
		t.Fatal("redo past the saved text is clean")
	}
	send(e, "u")
	if !e.execute("q") {
		t.Fatal("quit at the saved text refused")
	}
}

// Rust: editor::tests::copy_cut_and_repeat_paste_preserve_unicode_and_newlines
// Parity: REG-02, REG-05, NRM-18.
func TestCopyCutAndRepeatPastePreserveUnicodeAndNewlines(t *testing.T) {
	_, e := editingFixture(t, "é界\nbeta\n")
	send(e, "yy")
	if e.Buffer.Revision() != 0 || e.File.Dirty(e.Buffer.Text()) {
		t.Fatal("yank changed the buffer")
	}
	send(e, "p")
	if e.Buffer.Text() != "é界\né界\nbeta\n" || cursor(e) != [2]int{1, 0} {
		t.Fatalf("text %q cursor %v", e.Buffer.Text(), cursor(e))
	}
	send(e, "ddP")
	if e.Buffer.Text() != "é界\né界\nbeta\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "p")
	if e.Buffer.Text() != "é界\né界\né界\nbeta\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "0xP")
	if e.Buffer.Text() != "é界\né界\né界\nbeta\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "xp")
	if got := string(e.Buffer.Lines()[e.Buffer.Row]); got != "界é" {
		t.Fatalf("line = %q", got)
	}
	for _, text := range []string{"only", "only\n", "é界\n"} {
		_, sole := editingFixture(t, text)
		send(sole, "ddp")
		if sole.Buffer.Text() != text || sole.File.Dirty(sole.Buffer.Text()) {
			t.Errorf("ddp on %q gave %q (dirty %v)", text, sole.Buffer.Text(), sole.File.Dirty(sole.Buffer.Text()))
		}
	}
	_, empty := editingFixture(t, "")
	send(empty, "p")
	if empty.Buffer.Text() != "" || !contains(empty.Message, "Nothing to paste") {
		t.Fatalf("empty paste: %q %q", empty.Buffer.Text(), empty.Message)
	}
}

// Rust: editor::tests::replacement_consumes_command_characters_and_can_be_cancelled
// Parity: NRM-11.
func TestReplacementConsumesCommandCharactersAndCanBeCancelled(t *testing.T) {
	_, e := editingFixture(t, "é🦀\n\nlast")
	send(e, "rj")
	if e.Buffer.Text() != "j🦀\n\nlast" || cursor(e) != [2]int{0, 0} {
		t.Fatalf("text %q cursor %v", e.Buffer.Text(), cursor(e))
	}
	send(e, "lr/")
	if e.Mode != ModeNormal || string(e.Buffer.Lines()[0]) != "j/" {
		t.Fatalf("mode %v line %q", e.Mode, string(e.Buffer.Lines()[0]))
	}
	send(e, "r")
	press(e, key.Esc)
	if string(e.Buffer.Lines()[0]) != "j/" {
		t.Fatalf("line = %q", string(e.Buffer.Lines()[0]))
	}
	send(e, "r界")
	if string(e.Buffer.Lines()[0]) != "j界" {
		t.Fatalf("line = %q", string(e.Buffer.Lines()[0]))
	}
	send(e, "r")
	press(e, key.Tab)
	if string(e.Buffer.Lines()[0]) != "j\t" {
		t.Fatalf("line = %q", string(e.Buffer.Lines()[0]))
	}
	send(e, "jrX")
	if len(e.Buffer.Lines()[1]) != 0 || e.Mode != ModeNormal {
		t.Fatalf("empty line replaced: %q mode %v", string(e.Buffer.Lines()[1]), e.Mode)
	}
}

// Rust: editor::tests::uppercase_append_starts_after_last_character_even_on_empty_lines
// Parity: NRM-16.
func TestUppercaseAppendStartsAfterLastCharacterEvenOnEmptyLines(t *testing.T) {
	_, e := editingFixture(t, "abc\n\n界")
	send(e, "lAZ")
	if string(e.Buffer.Lines()[0]) != "abcZ" || e.Mode != ModeInsert {
		t.Fatalf("line %q mode %v", string(e.Buffer.Lines()[0]), e.Mode)
	}
	press(e, key.Esc)
	send(e, "jAempty")
	press(e, key.Esc)
	send(e, "GAé")
	press(e, key.Esc)
	if e.Buffer.Text() != "abcZ\nempty\n界é" || cursor(e) != [2]int{2, 1} {
		t.Fatalf("text %q cursor %v", e.Buffer.Text(), cursor(e))
	}
}

// Rust: editor::tests::indent_unindent_keeps_cursor_on_text_and_handles_mixed_whitespace
// Parity: BUF-21, NRM-10.
func TestIndentUnindentKeepsCursorOnTextAndHandlesMixedWhitespace(t *testing.T) {
	_, e := editingFixture(t, "\t  é\n  x\nplain\n")
	e.Buffer.Col = 3
	send(e, ">>")
	if string(e.Buffer.Lines()[0]) != "    \t  é" || e.Buffer.Col != 7 {
		t.Fatalf("line %q col %d", string(e.Buffer.Lines()[0]), e.Buffer.Col)
	}
	send(e, "<<")
	if e.Buffer.Col != 3 || e.File.Dirty(e.Buffer.Text()) {
		t.Fatalf("col %d dirty %v", e.Buffer.Col, e.File.Dirty(e.Buffer.Text()))
	}
	send(e, "<<")
	if e.Buffer.Col != 2 || string(e.Buffer.Lines()[0]) != "  é" {
		t.Fatalf("col %d line %q", e.Buffer.Col, string(e.Buffer.Lines()[0]))
	}
	send(e, "j<<")
	if string(e.Buffer.Lines()[1]) != "x" {
		t.Fatalf("line = %q", string(e.Buffer.Lines()[1]))
	}
	send(e, "j<<")
	if string(e.Buffer.Lines()[2]) != "plain" {
		t.Fatalf("line = %q", string(e.Buffer.Lines()[2]))
	}
	send(e, "G>><<")
	if len(e.Buffer.Lines()[3]) != 0 || e.Buffer.Col != 0 {
		t.Fatalf("last line %q col %d", string(e.Buffer.Lines()[3]), e.Buffer.Col)
	}
}

// Rust: editor::tests::search_repeats_wraps_cancels_and_leaves_text_unchanged
// Parity: SRC.
func TestSearchRepeatsWrapsCancelsAndLeavesTextUnchanged(t *testing.T) {
	const original = "é界 first\n界.*界\nlast 界"
	_, e := editingFixture(t, original)
	at := func(row, col int) {
		t.Helper()
		if cursor(e) != [2]int{row, col} {
			t.Fatalf("cursor = %v, want (%d,%d)", cursor(e), row, col)
		}
	}
	send(e, "/界")
	if e.Mode != ModeSearch {
		t.Fatalf("mode = %v", e.Mode)
	}
	at(0, 0)
	press(e, key.Enter)
	at(0, 1)
	send(e, "n")
	at(1, 0)
	send(e, "n")
	at(1, 3)
	send(e, "N")
	at(1, 0)
	send(e, "NN")
	at(2, 5)
	if !contains(e.Message, "wrapped") {
		t.Fatalf("message = %q", e.Message)
	}
	send(e, "n")
	at(0, 1)
	if !contains(e.Message, "wrapped") {
		t.Fatalf("message = %q", e.Message)
	}
	send(e, "/absent")
	press(e, key.Esc)
	at(0, 1)
	send(e, "n")
	at(1, 0)
	send(e, "/.*")
	press(e, key.Enter)
	at(1, 1)
	send(e, "/absent")
	press(e, key.Enter)
	at(1, 1)
	if !contains(e.Message, "Not found") {
		t.Fatalf("message = %q", e.Message)
	}
	if e.Buffer.Text() != original || e.Buffer.Revision() != 0 || e.File.Dirty(original) {
		t.Fatal("search edited the buffer")
	}
	_, empty := editingFixture(t, "")
	send(empty, "n")
	if !contains(empty.Message, "No previous search") {
		t.Fatalf("message = %q", empty.Message)
	}
	send(empty, "/")
	press(empty, key.Enter)
	if !contains(empty.Message, "No previous search") {
		t.Fatalf("message = %q", empty.Message)
	}
}

// Rust: editor::tests::line_and_page_navigation_in_both_modes
// Parity: NRM-03, NRM-04, INS-02, KEY-05, TRM-09.
func TestLineAndPageNavigationInBothModes(t *testing.T) {
	dir := t.TempDir()
	f, _, err := file.Open(filepath.Join(dir, "new"))
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, strings.Repeat("é界abc\n", 60))
	for _, mode := range []Mode{ModeNormal, ModeInsert} {
		e.Mode = mode
		end := 4
		if e.Mode == ModeInsert {
			end = 5
		}
		for _, p := range []struct {
			home, end key.Code
			mods      key.Mods
		}{{key.Home, key.End, 0}, {'a', 'e', key.Ctrl}} {
			e.Key(key.New(p.end, p.mods), 22)
			if e.Buffer.Col != end {
				t.Fatalf("mode %v End: col %d, want %d", mode, e.Buffer.Col, end)
			}
			e.Key(key.New(p.home, p.mods), 22)
			if e.Buffer.Col != 0 {
				t.Fatalf("mode %v Home: col %d", mode, e.Buffer.Col)
			}
		}
		for _, p := range []struct {
			down, up key.Code
			mods     key.Mods
		}{{key.PageDown, key.PageUp, 0}, {'f', 'b', key.Ctrl}} {
			e.Buffer.Row = 0
			e.Buffer.Col = 2
			e.Key(key.New(p.down, p.mods), 22)
			if cursor(e) != [2]int{22, 2} {
				t.Fatalf("page down: %v", cursor(e))
			}
			e.Key(key.New(p.up, p.mods), 6)
			if e.Buffer.Row != 16 {
				t.Fatalf("page up by 6: row %d", e.Buffer.Row)
			}
			e.Key(key.New(p.up, p.mods), 22)
			if e.Buffer.Row != 0 {
				t.Fatalf("page up: row %d", e.Buffer.Row)
			}
			e.Key(key.New(p.down, p.mods), maxInt)
			if cursor(e) != [2]int{60, 0} {
				t.Fatalf("page down by MAX: %v", cursor(e))
			}
			e.Key(key.New(p.up, p.mods), 0)
			if e.Buffer.Row != 59 {
				t.Fatalf("page up by 0: row %d", e.Buffer.Row)
			}
			e.Buffer.Row = 0
		}
	}
	original := e.Buffer.Text()
	e.Mode = ModeCommand
	e.Command = "w"
	for _, code := range []key.Code{key.Home, key.End, key.PageDown, key.PageUp} {
		e.Key(key.Press(code), 22)
	}
	if e.Command != "w" || e.Buffer.Text() != original || e.Buffer.Row != 0 {
		t.Fatalf("command mode moved: %q row %d", e.Command, e.Buffer.Row)
	}
}

// Rust: editor::tests::modes_edits_and_quit
func TestModesEditsAndQuit(t *testing.T) {
	dir := t.TempDir()
	f, s, err := file.Open(filepath.Join(dir, "new"))
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, s)
	send(e, "ié🦀")
	if e.Mode != ModeInsert {
		t.Fatalf("mode = %v", e.Mode)
	}
	e.Key(key.Press(key.Esc), 22)
	if e.Buffer.Col != 1 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	if e.execute("q") || !contains(e.Message, "Unsaved") {
		t.Fatalf("q: %q", e.Message)
	}
	send(e, "0x")
	if e.Buffer.Text() != "🦀" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "ohello")
	e.Key(key.Press(key.Esc), 22)
	send(e, "ggdd")
	if e.Buffer.Text() != "hello" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	if !e.execute("wq") || e.File.Dirty("hello") {
		t.Fatal("wq did not save and quit")
	}
}

// Rust: editor::tests::command_and_failed_write_quit
func TestCommandAndFailedWriteQuit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new")
	f, s, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, s)
	send(e, ":w")
	if e.Mode != ModeCommand {
		t.Fatalf("mode = %v", e.Mode)
	}
	e.Key(key.Press(key.Enter), 22)
	if _, err := fsStat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	send(e, "ia")
	write(t, path, "external")
	if e.execute("wq") {
		t.Fatal("wq quit despite an external change")
	}
	if !e.execute("q!") {
		t.Fatal("q! did not quit")
	}
}

// Rust: editor::tests::movement_insert_newline_and_cancel
func TestMovementInsertNewlineAndCancel(t *testing.T) {
	f, _, err := file.Open(filepath.Join(t.TempDir(), "new"))
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, "abc\n\né界")
	send(e, "$j")
	if cursor(e) != [2]int{1, 0} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, "G$")
	if cursor(e) != [2]int{2, 1} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, "ggllh")
	if cursor(e) != [2]int{0, 1} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	e.Key(key.Press(key.Down), 22)
	if cursor(e) != [2]int{1, 0} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	send(e, "aé")
	e.Key(key.Press(key.Enter), 22)
	send(e, "🦀")
	e.Key(key.Press(key.Backspace), 22)
	e.Key(key.Press(key.Backspace), 22)
	if e.Buffer.Text() != "abc\né\né界" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	e.Key(key.Press(key.Esc), 22)
	send(e, ":q!")
	e.Key(key.Press(key.Esc), 22)
	if e.Mode != ModeNormal || e.Command != "" {
		t.Fatalf("mode %v command %q", e.Mode, e.Command)
	}
	if e.execute("q") || !e.execute("q!") {
		t.Fatal("quit protection broken")
	}
	if _, err := fsStat(e.File.Path); err == nil {
		t.Fatal("quitting created the file")
	}
}
