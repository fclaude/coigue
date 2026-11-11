package buffer

import (
	"fmt"
	"testing"

	"github.com/fclaude/coigue/internal/rope"
)

// The flatten-and-split implementations these operations had before they
// were rewritten on splice and walker, kept as the reference the rewrites must
// agree with. Each builds the whole text as one array, edits it and splits it
// back into lines.

func (b *Buffer) refFlatChars() []rune {
	var out []rune
	for i, line := range b.Lines() {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, line...)
	}
	return out
}

func refNextWord(chars []rune, at int) int {
	if at < len(chars) {
		class := wordClass(chars[at])
		for at < len(chars) && wordClass(chars[at]) == class {
			at++
		}
		for at < len(chars) && wordClass(chars[at]) == 0 {
			at++
		}
	}
	return at
}

func (b *Buffer) refPasteChars(chars []rune, after bool) {
	if len(chars) == 0 {
		return
	}
	start := b.offset()
	if after && b.Col < len(b.Line(b.Row)) {
		start++
	}
	b.change(b.Row)
	text := b.refFlatChars()
	spliced := make([]rune, 0, len(text)+len(chars))
	spliced = append(append(append(spliced, text[:start]...), chars...), text[start:]...)
	b.text = rope.FromLines(splitChars(spliced))
	b.setOffset(start + len(chars) - 1)
	b.Clamp(false)
}

func (b *Buffer) refInsertText(chars []rune) {
	if len(chars) == 0 {
		return
	}
	start := b.offset()
	b.change(b.Row)
	text := b.refFlatChars()
	spliced := make([]rune, 0, len(text)+len(chars))
	spliced = append(append(append(spliced, text[:start]...), chars...), text[start:]...)
	b.text = rope.FromLines(splitChars(spliced))
	b.setOffset(start + len(chars))
}

func (b *Buffer) refReplaceSpan(start, end int, text []rune) {
	chars := b.refFlatChars()
	start = max(min(start, len(chars)), 0)
	end = max(min(end, len(chars)), start)
	b.change(b.rowAt(start))
	spliced := make([]rune, 0, len(chars)-(end-start)+len(text))
	spliced = append(append(append(spliced, chars[:start]...), text...), chars[end:]...)
	b.text = rope.FromLines(splitChars(spliced))
	b.setOffset(start)
	b.Clamp(false)
}

func (b *Buffer) refSelectionChars(anchor [2]int, cut bool) []rune {
	start, end := b.SelectionSpan(anchor)
	chars := b.refFlatChars()
	selected := append([]rune(nil), chars[start:end]...)
	if cut && start < end {
		b.change(min(anchor[0], b.Row))
		chars = append(chars[:start], chars[end:]...)
		b.text = rope.FromLines(splitChars(chars))
	}
	b.setOffset(start)
	b.Clamp(false)
	return selected
}

func (b *Buffer) refWordMotion(motion rune) {
	chars := b.refFlatChars()
	at := b.offset()
	switch motion {
	case 'w':
		at = refNextWord(chars, at)
	case 'b':
		if at > 0 {
			at--
		}
		for at > 0 && wordClass(chars[at]) == 0 {
			at--
		}
		for at > 0 && wordClass(chars[at-1]) == wordClass(chars[at]) {
			at--
		}
	case 'e':
		if at < len(chars) {
			at++
		}
		for at < len(chars) && wordClass(chars[at]) == 0 {
			at++
		}
		for at+1 < len(chars) && wordClass(chars[at+1]) == wordClass(chars[at]) {
			at++
		}
	default:
		return
	}
	b.setOffset(at)
	b.Clamp(false)
}

func (b *Buffer) refWordRange(cut, change bool, count int) []rune {
	chars := b.refFlatChars()
	start := b.offset()
	end := start
	for i := 0; i < count; i++ {
		next := refNextWord(chars, end)
		if next == end {
			break
		}
		end = next
	}
	if change && start < len(chars) && wordClass(chars[start]) != 0 {
		// cw behaves like ce: retain whitespace after the last selected run.
		for end > start && wordClass(chars[end-1]) == 0 {
			end--
		}
	}
	copied := append([]rune(nil), chars[start:end]...)
	if cut && start < end {
		b.change(b.Row)
		chars = append(chars[:start], chars[end:]...)
		b.text = rope.FromLines(splitChars(chars))
		b.setOffset(start)
	}
	return copied
}

// TestSplicedEditsAgreeWithFlattenAndSplit runs each rewritten operation and
// its reference on copies of the same random buffer and cursor and compares
// the text, the cursor, the size and what they return.
func TestSplicedEditsAgreeWithFlattenAndSplit(t *testing.T) {
	for seed := range 400 {
		r := rng(seed + 7)
		text := string(r.runes(40))
		for step := range 40 {
			got, want := New(text), New(text)
			row := r.intn(got.LineCount())
			col := r.intn(len(got.Lines()[row]) + 1)
			got.Row, got.Col, want.Row, want.Col = row, col, row, col
			if r.intn(2) == 0 {
				got.Clamp(false)
				want.Clamp(false)
			}
			chars := r.runes(8)
			var name string
			var gotOut, wantOut []rune
			switch r.intn(9) {
			case 0:
				name = "PasteChars after"
				got.PasteChars(chars, true)
				want.refPasteChars(chars, true)
			case 1:
				name = "PasteChars before"
				got.PasteChars(chars, false)
				want.refPasteChars(chars, false)
			case 2:
				name = "InsertText"
				got.InsertText(chars)
				want.refInsertText(chars)
			case 3:
				name = "ReplaceSpan"
				start := r.intn(got.length() + 2)
				end := start + r.intn(8) - 2
				got.ReplaceSpan(start, end, chars)
				want.refReplaceSpan(start, end, chars)
			case 4, 5:
				cut := r.intn(2) == 0
				name = fmt.Sprintf("SelectionChars cut=%v", cut)
				anchor := [2]int{r.intn(got.LineCount() + 1), r.intn(6)}
				gotOut = got.SelectionChars(anchor, cut)
				wantOut = want.refSelectionChars(anchor, cut)
			case 6:
				motion := []rune{'w', 'b', 'e', 'x'}[r.intn(4)]
				name = fmt.Sprintf("WordMotion %c", motion)
				got.WordMotion(motion)
				want.refWordMotion(motion)
			default:
				cut, change, count := r.intn(2) == 0, r.intn(2) == 0, 1+r.intn(3)
				name = fmt.Sprintf("WordRange cut=%v change=%v count=%d", cut, change, count)
				gotOut = got.WordRange(cut, change, count)
				wantOut = want.refWordRange(cut, change, count)
			}
			if got.Text() != want.Text() || got.Row != want.Row || got.Col != want.Col ||
				string(gotOut) != string(wantOut) || got.Len() != len(got.Text()) {
				t.Fatalf("seed %d step %d %s at %d:%d on %q\n  got  %q cursor %d:%d returned %q len %d\n  want %q cursor %d:%d returned %q",
					seed, step, name, row, col, text, got.Text(), got.Row, got.Col, string(gotOut), got.Len(),
					want.Text(), want.Row, want.Col, string(wantOut))
			}
			text = got.Text()
		}
	}
}
