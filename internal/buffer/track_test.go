package buffer

import (
	"fmt"
	"testing"
)

func equalRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rng is a small deterministic generator, so a failure names a seed that
// reproduces it.
type rng uint64

func (r *rng) intn(n int) int {
	*r = *r*6364136223846793005 + 1442695040888963407
	return int(uint64(*r)>>33) % n
}

var sampleRunes = []rune{'a', 'b', ' ', '\t', 'é', '€', '😀', 'x', '_', '.', '\n', -1, 0xD800, 0x110000}

func (r *rng) runes(n int) []rune {
	out := make([]rune, r.intn(n+1))
	for i := range out {
		out[i] = sampleRunes[r.intn(len(sampleRunes))]
	}
	return out
}

func (r *rng) lineRunes(n int) []rune {
	out := r.runes(n)
	for i, c := range out {
		if c == '\n' {
			out[i] = 'n'
		}
	}
	return out
}

// randomEdit applies one random Buffer operation and names it.
func randomEdit(b *Buffer, r *rng) string {
	b.Row = r.intn(b.LineCount())
	b.Col = r.intn(len(b.Lines()[b.Row]) + 1)
	switch r.intn(17) {
	case 0:
		ch := sampleRunes[r.intn(len(sampleRunes))]
		b.Insert(ch)
		return fmt.Sprintf("Insert(%q)", ch)
	case 1:
		b.Backspace()
		return "Backspace"
	case 2:
		b.Clamp(false)
		b.Delete()
		return "Delete"
	case 3:
		b.Clamp(false)
		b.Replace('€')
		return "Replace"
	case 4:
		b.OpenLine()
		return "OpenLine"
	case 5:
		// Whole lines never contain a line break: registers are split.
		lines := [][]rune{r.lineRunes(4), r.lineRunes(4)}
		b.PasteLines(lines, r.intn(2) == 0, r.intn(2) == 0)
		return "PasteLines"
	case 6:
		b.Clamp(false)
		b.PasteChars(r.runes(8), r.intn(2) == 0)
		return "PasteChars"
	case 7:
		b.InsertText(r.runes(8))
		return "InsertText"
	case 8:
		n := b.length() + 1
		start := r.intn(n)
		b.ReplaceSpan(start, start+r.intn(6), r.runes(5))
		return "ReplaceSpan"
	case 9:
		anchor := [2]int{r.intn(b.LineCount()), r.intn(3)}
		b.Clamp(false)
		b.SelectionChars(anchor, true)
		return "SelectionChars(cut)"
	case 10:
		b.Clamp(false)
		b.WordRange(true, r.intn(2) == 0, 1+r.intn(3))
		return "WordRange(cut)"
	case 11:
		b.Indent(r.intn(2) == 0)
		return "Indent"
	case 12:
		b.DeleteLine()
		return "DeleteLine"
	case 13:
		b.Undo()
		return "Undo"
	case 14:
		b.Redo()
		return "Redo"
	case 15:
		scratch := b.Clone()
		scratch.InsertText(r.runes(5))
		scratch.DeleteLine()
		b.Edit(func() { b.Adopt(scratch) })
		return "Adopt"
	default:
		b.FinishEdit()
		b.BeginEdit()
		return "FinishEdit+BeginEdit"
	}
}

func TestSizeAndChangedRowsFollowEveryEdit(t *testing.T) {
	for seed := range 200 {
		r := rng(seed + 1)
		b := New(string(r.runes(30)))
		b.BeginEdit()
		for step := range 150 {
			before := CloneLines(b.Lines())
			since := b.Revision()
			op := randomEdit(b, &r)
			text := b.Text()
			if b.Len() != len(text) {
				t.Fatalf("seed %d step %d %s: Len %d, Text has %d bytes", seed, step, op, b.Len(), len(text))
			}
			if !b.EqualBytes([]byte(text)) || b.EqualBytes([]byte(text+"x")) {
				t.Fatalf("seed %d step %d %s: EqualBytes disagrees with Text", seed, step, op)
			}
			if len(text) > 0 {
				mutated := []byte(text)
				mutated[len(mutated)/2] ^= 1
				if b.EqualBytes(mutated) {
					t.Fatalf("seed %d step %d %s: EqualBytes missed a changed byte", seed, step, op)
				}
			}
			row, ok := b.FirstChangedRow(since)
			if !ok {
				t.Fatalf("seed %d step %d %s: FirstChangedRow(%d) unknown", seed, step, op, since)
			}
			lines := b.Lines()
			for i := 0; i < row; i++ {
				if i >= len(before) || i >= len(lines) || !equalRunes(before[i], lines[i]) {
					t.Fatalf("seed %d step %d %s: row %d changed but FirstChangedRow is %d", seed, step, op, i, row)
				}
			}
		}
	}
}

func TestFirstChangedRowCoversSeveralRevisionsAndKnowsWhenItForgot(t *testing.T) {
	b := New("a\nb\nc\nd\n")
	start := b.Revision()
	b.Row = 3
	b.Insert('x')
	b.Row = 1
	b.Insert('y')
	if row, ok := b.FirstChangedRow(start); !ok || row != 1 {
		t.Fatalf("FirstChangedRow = %d, %v; want 1", row, ok)
	}
	if row, ok := b.FirstChangedRow(b.Revision()); !ok || row != b.LineCount() {
		t.Fatalf("nothing changed: FirstChangedRow = %d, %v", row, ok)
	}
	for range damageLog + 1 {
		b.Insert('z')
	}
	if _, ok := b.FirstChangedRow(start); ok {
		t.Fatal("FirstChangedRow claimed to know about forgotten revisions")
	}
	if _, ok := b.FirstChangedRow(b.Revision() + 1); ok {
		t.Fatal("FirstChangedRow accepted a revision from the future")
	}
}
