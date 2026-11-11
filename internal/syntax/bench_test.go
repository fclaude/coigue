package syntax

import (
	"fmt"
	"testing"

	"github.com/fclaude/coigue/internal/benchdata"
)

// sliceLines is a LineSource over a slice.
type sliceLines [][]rune

func (s sliceLines) LineCount() int    { return len(s) }
func (s sliceLines) Line(i int) []rune { return s[i] }

func benchLines(text string) [][]rune {
	var lines [][]rune
	start := 0
	for i := range len(text) {
		if text[i] == '\n' {
			lines = append(lines, []rune(text[start:i]))
			start = i + 1
		}
	}
	return append(lines, []rune(text[start:]))
}

// BenchmarkHighlightFull relexes a whole Go file, which is what one edit
// cost the renderer before highlighting became incremental.
func BenchmarkHighlightFull(b *testing.B) {
	for _, n := range benchdata.Sizes {
		lines := benchLines(benchdata.GoSource(n))
		b.Run(fmt.Sprintf("lines=%d", n), func(b *testing.B) {
			for range b.N {
				_ = ForPath("x.go", lines)
			}
		})
	}
}

// BenchmarkHighlightAfterEdit is what one edit on the middle line costs the
// renderer with incremental highlighting: relex from that line to the bottom
// of a 40-row screen.
func BenchmarkHighlightAfterEdit(b *testing.B) {
	for _, n := range benchdata.Sizes {
		lines := benchLines(benchdata.GoSource(n))
		b.Run(fmt.Sprintf("lines=%d", n), func(b *testing.B) {
			h, _ := NewIncremental("x.go")
			mid := len(lines) / 2
			h.Lines(sliceLines(lines), mid+40)
			b.ResetTimer()
			for range b.N {
				h.Invalidate(mid)
				_ = h.Lines(sliceLines(lines), mid+40)
			}
		})
	}
}
