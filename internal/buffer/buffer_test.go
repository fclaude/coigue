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
