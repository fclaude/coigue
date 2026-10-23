package buffer

import (
	"testing"
)

func TestWordBoundariesAndMultilinePaste(t *testing.T) {
	// Rust: buffer::tests::word_boundaries_and_multiline_paste
	// Parity: BUF-11, BUF-12, BUF-13, BUF-14, BUF-15, BUF-16, BUF-17, BUF-18, BUF-24.
	b := New("é_2!! \n\n界9")
	for _, step := range []struct {
		motion   rune
		row, col int
	}{
		{'e', 0, 2},
		{'w', 0, 3},
		{'e', 0, 4},
		{'w', 2, 0},
		{'e', 2, 1},
		{'w', 2, 1},
		{'b', 2, 0},
		{'b', 0, 3},
		{'b', 0, 0},
	} {
		b.WordMotion(step.motion)
		if b.Row != step.row || b.Col != step.col {
			t.Fatalf("after %q: (%d,%d), want (%d,%d)", step.motion, b.Row, b.Col, step.row, step.col)
		}
	}
	if b.Revision() != 0 {
		t.Fatalf("motions changed revision to %d", b.Revision())
	}
	b.Col = 3
	cut := b.WordRange(true, false, 1)
	if got := string(cut); got != "!! \n\n" {
		t.Fatalf("cut = %q", got)
	}
	b.Clamp(false)
	b.Col = 2
	b.PasteChars(cut, true)
	if got := b.Text(); got != "é_2!! \n\n界9" {
		t.Fatalf("text = %q", got)
	}
	empty := New("")
	if got := empty.WordRange(true, false, 1); len(got) != 0 {
		t.Fatalf("empty range = %q", string(got))
	}
	if empty.Revision() != 0 {
		t.Fatal("empty range changed revision")
	}
}

func TestHistoryLimitAndMonotonicRevisions(t *testing.T) {
	// Rust: buffer::tests::history_limit_and_monotonic_revisions
	// Parity: UND-07, BUF-24.
	b := New("")
	for i := 0; i < HistoryLimit+5; i++ {
		b.BeginEdit()
		b.Insert('界')
		b.FinishEdit()
	}
	revision := b.Revision()
	for i := 0; i < HistoryLimit; i++ {
		if !b.Undo() {
			t.Fatalf("undo %d refused", i)
		}
		if b.Revision() <= revision {
			t.Fatalf("revision did not increase on undo %d", i)
		}
		revision = b.Revision()
	}
	if b.Undo() {
		t.Fatal("undo beyond the history limit succeeded")
	}
	if got := len(b.Lines()[0]); got != 5 {
		t.Fatalf("line length after undo = %d, want 5", got)
	}
	for i := 0; i < HistoryLimit; i++ {
		if !b.Redo() {
			t.Fatalf("redo %d refused", i)
		}
	}
	if b.Redo() {
		t.Fatal("redo beyond history succeeded")
	}
	if got := len(b.Lines()[0]); got != HistoryLimit+5 {
		t.Fatalf("line length after redo = %d", got)
	}
}

func TestUnicodeSplitJoin(t *testing.T) {
	// Rust: buffer::tests::unicode_split_join
	// Parity: BUF-03, BUF-05, BUF-06, BUF-07.
	b := New("é🦀\n")
	b.Col = 1
	b.Insert('\n')
	if got := b.Text(); got != "é\n🦀\n" {
		t.Fatalf("after split: %q", got)
	}
	b.Backspace()
	if got := b.Text(); got != "é🦀\n" {
		t.Fatalf("after backspace: %q", got)
	}
	b.Delete()
	if got := b.Text(); got != "é\n" {
		t.Fatalf("after delete: %q", got)
	}
}

func TestEmptyAndFinalNewlines(t *testing.T) {
	// Rust: buffer::tests::empty_and_final_newlines
	// Parity: BUF-01, BUF-06, BUF-07, BUF-10.
	for _, s := range []string{"", "\n", "a\n\n", "a"} {
		if got := New(s).Text(); got != s {
			t.Errorf("New(%q).Text() = %q", s, got)
		}
	}
	b := New("")
	b.DeleteLine()
	b.Backspace()
	b.Delete()
	if got := b.Text(); got != "" {
		t.Fatalf("text = %q", got)
	}
}

// Parity: UND-01, UND-03, UND-04.
func TestEditTransactionGroupsChangesAndKeepsRedoOnNoOp(t *testing.T) {
	b := New("abc")
	b.Edit(func() {
		b.Insert('x')
		b.Insert('y')
	})
	b.Edit(func() {
		b.Insert('z')
		b.Backspace() // a net-zero transaction leaves no undo step
	})
	if !b.Undo() || b.Text() != "abc" {
		t.Fatalf("one undo should revert the whole first transaction, got %q", b.Text())
	}
	if b.Undo() {
		t.Fatal("no-op transaction created an undo step")
	}
	b.Edit(func() { b.Insert('q') })
	if b.Redo() {
		t.Fatal("a new edit must clear redo history")
	}
}

// Guards the Go-specific hazard that slices share backing arrays; REG-03 and UND-05
// rely on snapshots and registers keeping the text they captured.
func TestSnapshotsAndRegistersDoNotAliasTheLiveBuffer(t *testing.T) {
	b := New("one\ntwo")
	copied := CloneLines(b.Lines()[:1])
	b.Edit(func() { b.Replace('O') })
	if string(copied[0]) != "one" {
		t.Fatalf("clone changed with the buffer: %q", string(copied[0]))
	}
	b.Undo()
	if b.Text() != "one\ntwo" {
		t.Fatalf("undo restored %q", b.Text())
	}
}

// Parity: BUF-23 (the reflow engine itself is tested in package reflow).
func TestJustifyUsesByteOffsetsOfTheCursorAndReplacesTheRange(t *testing.T) {
	b := New("é a\nb")
	b.Row, b.Col = 1, 1
	var gotCursor, gotWidth int
	changed := b.Justify(9, func(text string, cursor, width int) (int, int, string, bool) {
		gotCursor, gotWidth = cursor, width
		return 0, len("é a"), "é a b", true
	})
	if !changed || gotCursor != len("é a\n")+1 || gotWidth != 9 {
		t.Fatalf("changed=%v cursor=%d width=%d", changed, gotCursor, gotWidth)
	}
	if b.Text() != "é a b\nb" || b.Row != 0 || b.Col != 0 {
		t.Fatalf("text %q at (%d,%d)", b.Text(), b.Row, b.Col)
	}
	if b.Justify(9, func(string, int, int) (int, int, string, bool) { return 0, 0, "", false }) {
		t.Fatal("declined reflow reported a change")
	}
	same := func(text string, _, _ int) (int, int, string, bool) { return 0, 1, text[:1], true }
	if b.Justify(9, same) {
		t.Fatal("identical replacement reported a change")
	}
}

func TestSelectionSpanAgreesWithSelectionChars(t *testing.T) {
	text := "héllo 界\n\nthird 🦀 line\n"
	for _, anchor := range [][2]int{{0, 0}, {0, 3}, {1, 0}, {2, 6}, {3, 0}, {9, 40}} {
		for row := 0; row < 4; row++ {
			for col := 0; col < 18; col++ {
				b := New(text)
				b.Row, b.Col = row, col
				b.Clamp(false)
				start, end := b.SelectionSpan(anchor)
				rows, cols, rev := b.Row, b.Col, b.Revision()
				if got := string(b.Span(start, end)); got != string(New(text).selectionCharsAt(anchor, rows, cols)) {
					t.Fatalf("anchor %v cursor (%d,%d): span %q", anchor, rows, cols, got)
				}
				if b.Row != rows || b.Col != cols || b.Revision() != rev {
					t.Fatal("SelectionSpan or Span moved the cursor or changed the buffer")
				}
			}
		}
	}
}

// selectionCharsAt is the reference: the cutting selection the editor has always used.
func (b *Buffer) selectionCharsAt(anchor [2]int, row, col int) []rune {
	b.Row, b.Col = row, col
	b.Clamp(false)
	return b.SelectionChars(anchor, false)
}

func TestSpanLineSpanAndReplaceSpan(t *testing.T) {
	b := New("ab\ncd\n界e")
	if got := string(b.Span(1, 7)); got != "b\ncd\n界" {
		t.Fatalf("Span = %q", got)
	}
	if got := string(b.Span(-5, 100)); got != b.Text() {
		t.Fatalf("clamped Span = %q", got)
	}
	if got := b.Span(4, 4); len(got) != 0 {
		t.Fatalf("empty Span = %q", string(got))
	}
	for _, c := range []struct {
		a, z       int
		start, end int
	}{{0, 0, 0, 2}, {1, 1, 3, 5}, {2, 1, 3, 8}, {1, 2, 3, 8}, {0, 99, 0, 8}} {
		if s, e := b.LineSpan(c.a, c.z); s != c.start || e != c.end {
			t.Fatalf("LineSpan(%d,%d) = %d,%d, want %d,%d", c.a, c.z, s, e, c.start, c.end)
		}
	}
	b.ReplaceSpan(3, 5, []rune("(界\nx)"))
	if b.Text() != "ab\n(界\nx)\n界e" || b.Row != 1 || b.Col != 0 {
		t.Fatalf("text %q at (%d,%d)", b.Text(), b.Row, b.Col)
	}
	b.ReplaceSpan(0, 0, []rune("\n"))
	if b.Text() != "\nab\n(界\nx)\n界e" {
		t.Fatalf("insert at start: %q", b.Text())
	}
	b.ReplaceSpan(-3, 1000, nil)
	if b.Text() != "" || b.LineCount() != 1 {
		t.Fatalf("replace everything: %q (%d lines)", b.Text(), b.LineCount())
	}
}

func TestCloneIsIndependentAndAdoptIsOneUndoStep(t *testing.T) {
	b := New("one\ntwo")
	b.Row, b.Col = 1, 1
	scratch := b.Clone()
	scratch.ReplaceSpan(0, 3, []rune("1"))
	scratch.ReplaceSpan(0, 0, []rune("# "))
	if b.Text() != "one\ntwo" || b.Revision() != 0 {
		t.Fatalf("a clone's edits reached the original: %q rev %d", b.Text(), b.Revision())
	}
	if len(scratch.undo) != 0 || len(scratch.redo) != 0 {
		t.Fatal("a clone carries history")
	}
	b.Edit(func() { b.Adopt(scratch) })
	if b.Text() != "# 1\ntwo" {
		t.Fatalf("adopted %q", b.Text())
	}
	if !b.Undo() || b.Text() != "one\ntwo" || b.Row != 1 || b.Col != 1 {
		t.Fatalf("undo gave %q at (%d,%d)", b.Text(), b.Row, b.Col)
	}
	if b.Undo() {
		t.Fatal("the adoption took more than one undo step")
	}
	if !b.Redo() || b.Text() != "# 1\ntwo" {
		t.Fatalf("redo gave %q", b.Text())
	}
	// Adopting identical text records no step and keeps redo history.
	b.Undo()
	same := b.Clone()
	b.Edit(func() { b.Adopt(same) })
	if !b.Redo() || b.Text() != "# 1\ntwo" {
		t.Fatal("adopting identical text discarded the redo history")
	}
}

func TestWordAtIsTheWordRunUnderTheCursor(t *testing.T) {
	b := New("x foo_bar9 y\n界 é2\n\n.")
	for _, c := range []struct {
		row, col int
		want     string
	}{
		{0, 0, "x"},
		{0, 1, ""},
		{0, 2, "foo_bar9"},
		{0, 6, "foo_bar9"},
		{0, 9, "foo_bar9"},
		{0, 10, ""},
		{0, 11, "y"},
		{0, 12, ""}, // one past the end of the line
		{1, 0, "界"},
		{1, 3, "é2"},
		{2, 0, ""}, // an empty line
		{3, 0, ""}, // punctuation
	} {
		b.Row, b.Col = c.row, c.col
		if got := string(b.WordAt()); got != c.want {
			t.Errorf("(%d,%d): %q, want %q", c.row, c.col, got, c.want)
		}
	}
	if b.Row != 3 || b.Col != 0 {
		t.Errorf("the cursor moved to (%d,%d)", b.Row, b.Col)
	}
	// A copy: changing it does not touch the buffer.
	b.Row, b.Col = 0, 2
	w := b.WordAt()
	w[0] = 'X'
	if string(b.Lines()[0]) != "x foo_bar9 y" {
		t.Errorf("WordAt aliases the line: %q", string(b.Lines()[0]))
	}
}
