// Package benchdata builds the deterministic texts the performance benchmarks
// edit, so that every package measures against the same kind of file. It is
// imported only by tests.
package benchdata

import (
	"fmt"
	"strings"
)

// Sizes are the file lengths, in lines, the benchmarks are run at: a typical
// source file, a long one and a very long one (generated code, logs, data).
var Sizes = []int{2_000, 20_000, 100_000}

// GoSource returns lines lines of Go-like source, about 80 bytes a line. It
// mixes the constructs that carry lexer state from line to line (block
// comments and raw strings) with ordinary code, so highlighting does real
// work, and ends with a newline like a file on disk.
func GoSource(lines int) string {
	var sb strings.Builder
	sb.Grow(lines * 82)
	for i := range lines {
		switch i % 40 {
		case 0:
			fmt.Fprintf(&sb, "// Section %d: helpers for the numbered handlers below.\n", i/40)
		case 10:
			sb.WriteString("/* A block comment that spans two lines,\n")
		case 11:
			sb.WriteString("   so the lexer carries state across them. */\n")
		case 20:
			fmt.Fprintf(&sb, "var template%d = `first line of a raw string %d\n", i, i)
		case 21:
			sb.WriteString("second line of the raw string, still a string`\n")
		case 39:
			sb.WriteString("\n")
		default:
			fmt.Fprintf(&sb, "\tif v%d := compute(%d, \"label-%d\"); v%d > 0x%x { total += v%d }\n", i, i, i, i, i, i)
		}
	}
	return sb.String()
}

// Prose returns about n bytes of plain text in lines of up to 72 bytes, the
// shape of something copied from a document or a log into the editor. It
// contains no characters the editor treats as commands in Insert mode.
func Prose(n int) string {
	words := []string{"the", "editor", "keeps", "pasted", "text", "exactly", "as", "it",
		"arrived", "and", "redraws", "once", "per", "burst", "of", "input", "ñandú", "naïve"}
	var sb strings.Builder
	sb.Grow(n + 80)
	line := 0
	for i := 0; sb.Len() < n; i++ {
		w := words[i%len(words)]
		if line+len(w)+1 > 72 {
			sb.WriteByte('\n')
			line = 0
		} else if line > 0 {
			sb.WriteByte(' ')
			line++
		}
		sb.WriteString(w)
		line += len(w)
	}
	sb.WriteByte('\n')
	return sb.String()
}
