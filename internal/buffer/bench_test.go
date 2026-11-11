package buffer

// Micro-benchmarks of the operations an edit costs, at the file sizes in
// benchdata.Sizes, with the cursor on the middle line.

import (
	"fmt"
	"testing"

	"github.com/fclaude/coigue/internal/benchdata"
)

func benchBuffer(lines int) *Buffer {
	b := New(benchdata.GoSource(lines))
	b.Row = b.LineCount() / 2
	return b
}

func eachSize(b *testing.B, run func(b *testing.B, lines int, text string)) {
	for _, lines := range benchdata.Sizes {
		text := benchdata.GoSource(lines)
		b.Run(fmt.Sprintf("lines=%d", lines), func(b *testing.B) { run(b, lines, text) })
	}
}

// BenchmarkInsertChar is one typed character inside an Insert session (the
// undo snapshot was already taken by the session's first change), alternating
// with a Backspace so the line keeps its length.
func BenchmarkInsertChar(b *testing.B) {
	eachSize(b, func(b *testing.B, lines int, _ string) {
		buf := benchBuffer(lines)
		buf.Col = 10
		buf.BeginEdit()
		buf.Insert('x')
		b.ResetTimer()
		for i := range b.N {
			if i%2 == 0 {
				buf.Backspace()
			} else {
				buf.Insert('x')
			}
		}
	})
}

// BenchmarkEditStep is one change that is its own undo step, like x in
// Normal mode: snapshot, change, and the check that something changed.
func BenchmarkEditStep(b *testing.B) {
	eachSize(b, func(b *testing.B, lines int, _ string) {
		buf := benchBuffer(lines)
		b.ResetTimer()
		for i := range b.N {
			buf.BeginEdit()
			if i%2 == 0 {
				buf.Insert('x')
			} else {
				buf.Backspace()
			}
			buf.FinishEdit()
		}
	})
}

// BenchmarkPasteChars is p with a 64 KB characterwise register.
func BenchmarkPasteChars(b *testing.B) {
	paste := []rune(benchdata.Prose(64 << 10))
	eachSize(b, func(b *testing.B, _ int, text string) {
		for range b.N {
			b.StopTimer()
			buf := New(text)
			buf.Row = buf.LineCount() / 2
			b.StartTimer()
			buf.PasteChars(paste, true)
		}
	})
}

// BenchmarkUndoRedo undoes and redoes one small change.
func BenchmarkUndoRedo(b *testing.B) {
	eachSize(b, func(b *testing.B, lines int, _ string) {
		buf := benchBuffer(lines)
		buf.Edit(func() { buf.Insert('x') })
		b.ResetTimer()
		for range b.N {
			buf.Undo()
			buf.Redo()
		}
	})
}

// BenchmarkOffset is the cursor's character offset, which selections,
// searches and pastes compute.
func BenchmarkOffset(b *testing.B) {
	eachSize(b, func(b *testing.B, lines int, _ string) {
		buf := benchBuffer(lines)
		b.ResetTimer()
		for range b.N {
			_ = buf.Offset()
		}
	})
}

// BenchmarkText is the whole text as one string, for saving, the dirty check
// and the language server.
func BenchmarkText(b *testing.B) {
	eachSize(b, func(b *testing.B, lines int, _ string) {
		buf := benchBuffer(lines)
		b.ResetTimer()
		for range b.N {
			_ = buf.Text()
		}
	})
}

// BenchmarkNew is building a buffer from a file's text.
func BenchmarkNew(b *testing.B) {
	eachSize(b, func(b *testing.B, _ int, text string) {
		b.SetBytes(int64(len(text)))
		for range b.N {
			_ = New(text)
		}
	})
}
