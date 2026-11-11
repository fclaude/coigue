package buffer

import "github.com/fclaude/coigue/internal/rope"

// Edits that span lines work on the lines they touch: splice replaces a range
// given as rows and columns, and walker steps through the text one character
// at a time across line ends. Neither builds the whole text as one array, so
// a paste or a word motion costs what it touches, not the size of the file.

// splitChars splits chars at '\n' into the lines they form (one more than
// the number of line breaks), copying them.
func splitChars(chars []rune) [][]rune {
	var lines [][]rune
	start := 0
	for i, c := range chars {
		if c == '\n' {
			lines = append(lines, append([]rune(nil), chars[start:i]...))
			start = i + 1
		}
	}
	return append(lines, append([]rune(nil), chars[start:]...))
}

// posAt is where character offset (see Offset) is: a row, and a column that
// is the row's length when the offset is at its line break. Offsets past the
// end are the end of the last row.
func (b *Buffer) posAt(offset int) (row, col int) {
	row = b.text.RowAt(offset)
	return row, max(min(offset-b.text.Offset(row), len(b.Line(row))), 0)
}

// between copies the characters from (r1, c1) up to (r2, c2), with a '\n'
// for each line break.
func (b *Buffer) between(r1, c1, r2, c2 int) []rune {
	if r1 == r2 {
		return append([]rune(nil), b.Line(r1)[c1:c2]...)
	}
	out := append(append([]rune(nil), b.Line(r1)[c1:]...), '\n')
	b.text.Each(r1+1, func(i int, line []rune) bool {
		if i == r2 {
			out = append(out, line[:c2]...)
			return false
		}
		out = append(append(out, line...), '\n')
		return true
	})
	return out
}

// splice replaces the characters from (r1, c1) up to (r2, c2) with chars and
// returns the position just after them. The caller has called change.
func (b *Buffer) splice(r1, c1, r2, c2 int, chars []rune) (row, col int) {
	segments := splitChars(chars)
	n := len(segments)
	head, tail := b.Line(r1)[:c1], b.Line(r2)[c2:]
	replacement := make([][]rune, n)
	if n == 1 {
		line := make([]rune, 0, len(head)+len(segments[0])+len(tail))
		replacement[0] = append(append(append(line, head...), segments[0]...), tail...)
		row, col = r1, c1+len(segments[0])
	} else {
		first := make([]rune, 0, len(head)+len(segments[0]))
		replacement[0] = append(append(first, head...), segments[0]...)
		copy(replacement[1:n-1], segments[1:n-1])
		row, col = r1+n-1, len(segments[n-1])
		replacement[n-1] = append(segments[n-1], tail...)
	}
	b.text = b.text.Replace(r1, r2+1, replacement)
	return row, col
}

// walker is a position that moves one character at a time over the text as
// Text spells it: each line break is one character, at its row's end. It
// keeps the line it is on, so stepping within a line does not search the
// rope.
type walker struct {
	text     rope.Rope
	line     []rune
	row, col int
}

func (b *Buffer) walkerAt(row, col int) walker {
	line := b.Line(row)
	return walker{text: b.text, line: line, row: row, col: min(col, len(line))}
}

func (w *walker) atEnd() bool {
	return w.row == w.text.Len()-1 && w.col >= len(w.line)
}

func (w *walker) atStart() bool { return w.row == 0 && w.col == 0 }

// at reports whether w and o are at the same position.
func (w walker) at(o walker) bool { return w.row == o.row && w.col == o.col }

// char is the character at the position; the caller checks atEnd.
func (w *walker) char() rune {
	if w.col < len(w.line) {
		return w.line[w.col]
	}
	return '\n'
}

func (w *walker) next() {
	if w.col < len(w.line) {
		w.col++
	} else if w.row+1 < w.text.Len() {
		w.row++
		w.line = w.text.Line(w.row)
		w.col = 0
	}
}

func (w *walker) prev() {
	if w.col > 0 {
		w.col--
	} else if w.row > 0 {
		w.row--
		w.line = w.text.Line(w.row)
		w.col = len(w.line)
	}
}

// peekPrev is the character before the position; the caller checks atStart.
func (w walker) peekPrev() rune {
	w.prev()
	return w.char()
}

// peekNext is the character after the one at the position, and whether there
// is one.
func (w walker) peekNext() (rune, bool) {
	w.next()
	if w.atEnd() {
		return 0, false
	}
	return w.char(), true
}

// nextWord moves past the run the position is in and the whitespace after
// it: the start of the next word.
func (w *walker) nextWord() {
	if w.atEnd() {
		return
	}
	class := wordClass(w.char())
	for !w.atEnd() && wordClass(w.char()) == class {
		w.next()
	}
	for !w.atEnd() && wordClass(w.char()) == 0 {
		w.next()
	}
}
