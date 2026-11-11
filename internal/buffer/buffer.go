// Package buffer is the editable text of one file: lines of Unicode scalars, a
// cursor, and undo history. Every text mutation goes through a method of
// Buffer, and every mutation first calls change, which is the single place that
// captures the pre-edit snapshot and records which rows the edit may alter
// (see track.go). BeginEdit and FinishEdit bracket an
// edit-transaction: all changes made between them become one undo step.
//
// The lines are kept in a rope (see package rope): a persistent tree, so an
// undo snapshot is the old tree itself rather than a copy of the text, and an
// edit rebuilds only the path to the lines it changes. Lines are never
// modified in place, because snapshots share them; every edit makes new ones.
package buffer

import (
	"strings"

	"github.com/fclaude/coigue/internal/rope"
	"github.com/fclaude/coigue/internal/ucd"
)

// HistoryLimit bounds the undo stack (and, through it, the redo stack).
const HistoryLimit = 100

type snapshot struct {
	text rope.Rope
	row  int
	col  int
}

// Buffer holds lines of rune slices in a rope. The cursor (Row, Col) may be
// moved freely by the editor; text only changes through the methods below.
type Buffer struct {
	text       rope.Rope // at least one line
	Row        int
	Col        int
	revision   uint64
	undo       []snapshot
	redo       []snapshot
	editOrigin *[2]int
	beforeEdit *snapshot
	damage     [damageLog]damage
}

func splitLines(text string) [][]rune {
	parts := strings.Split(text, "\n")
	lines := make([][]rune, len(parts))
	for i, part := range parts {
		lines[i] = []rune(part)
	}
	return lines
}

// New creates a buffer holding text. A trailing newline yields a final empty line,
// so that Text returns exactly what was given.
func New(text string) *Buffer {
	return &Buffer{text: rope.FromLines(splitLines(text))}
}

// Line returns line i (0 ≤ i < LineCount) for reading. Callers must not
// modify it: undo history shares it.
func (b *Buffer) Line(i int) []rune { return b.text.Line(i) }

// Lines returns every line, for reading, in a new slice: it costs one entry
// per line, so code that runs on every key or frame uses Line and LineCount.
// Callers must not modify the lines.
func (b *Buffer) Lines() [][]rune { return b.text.Lines() }

// LineCount returns the number of lines (at least one).
func (b *Buffer) LineCount() int { return b.text.Len() }

// Revision increases on every change, including undo and redo, so caches keyed
// on it (highlighting, dirty status) are invalidated even when old text returns.
func (b *Buffer) Revision() uint64 { return b.revision }

// CloneLines deep-copies lines (Rust's to_vec on Vec<Vec<char>>), so registers
// and snapshots never share backing arrays with the live buffer.
func CloneLines(lines [][]rune) [][]rune {
	out := make([][]rune, len(lines))
	for i, line := range lines {
		out[i] = append([]rune(nil), line...)
	}
	return out
}

// BeginEdit starts (or continues) an edit-transaction, remembering where the
// cursor was so undo returns there.
func (b *Buffer) BeginEdit() {
	if b.editOrigin == nil {
		b.editOrigin = &[2]int{b.Row, b.Col}
	}
}

func (b *Buffer) snapshot() snapshot {
	return snapshot{text: b.text, row: b.Row, col: b.Col}
}

// change is called before every mutation, with the first row the mutation may
// alter (rows above it keep their text and position). It captures lazily:
// navigation, copying and search never take a snapshot.
func (b *Buffer) change(row int) {
	if b.beforeEdit == nil {
		s := b.snapshot()
		if b.editOrigin != nil {
			s.row, s.col = b.editOrigin[0], b.editOrigin[1]
		}
		b.beforeEdit = &s
	}
	b.revision++
	b.noteDamage(row)
}

// FinishEdit closes the transaction. A transaction whose net effect is no
// change leaves no undo step and keeps the redo history.
func (b *Buffer) FinishEdit() {
	b.editOrigin = nil
	s := b.beforeEdit
	b.beforeEdit = nil
	if s != nil && !rope.Equal(s.text, b.text) {
		b.undo = append(b.undo, *s)
		if len(b.undo) > HistoryLimit {
			b.undo = b.undo[1:]
		}
		b.redo = nil
	}
}

// Edit runs fn as one edit-transaction.
func (b *Buffer) Edit(fn func()) {
	b.BeginEdit()
	defer b.FinishEdit()
	fn()
}

func (b *Buffer) restore(s snapshot) {
	row := rope.FirstDifference(b.text, s.text)
	b.text = s.text
	b.Row = s.row
	b.Col = s.col
	// Revisions must increase even when restoring older contents, so the
	// renderer invalidates highlighting and its cached dirty status.
	b.revision++
	b.noteDamage(row)
	b.Clamp(false)
}

// Undo reverts the last edit-transaction.
func (b *Buffer) Undo() bool {
	b.FinishEdit()
	if len(b.undo) == 0 {
		return false
	}
	s := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.redo = append(b.redo, b.snapshot())
	b.restore(s)
	return true
}

// Redo reapplies the last undone edit-transaction.
func (b *Buffer) Redo() bool {
	b.FinishEdit()
	if len(b.redo) == 0 {
		return false
	}
	s := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	b.undo = append(b.undo, b.snapshot())
	b.restore(s)
	return true
}

// Reflow finds the paragraph around the cursor (byte offset cursor into the
// text) and returns the replaced byte range and new text; ok is false when
// the paragraph cannot or need not be filled.
type Reflow func(text string, cursor, width int) (start, end int, replacement string, ok bool)

// Justify fills the paragraph under the cursor to width columns using fill.
func (b *Buffer) Justify(width int, fill Reflow) bool {
	text := b.Text()
	cursor := 0
	b.text.Each(0, func(i int, line []rune) bool {
		if i == b.Row {
			cursor += runeBytes(line[:b.Col])
			return false
		}
		cursor += runeBytes(line) + 1
		return true
	})
	start, end, replacement, ok := fill(text, cursor, width)
	if !ok {
		return false
	}
	if text[start:end] == replacement {
		return false
	}
	row := strings.Count(text[:start], "\n")
	b.change(row)
	b.text = rope.FromLines(splitLines(text[:start] + replacement + text[end:]))
	b.Row = row
	b.Col = 0
	return true
}

func runeBytes(line []rune) int {
	n := 0
	for _, r := range line {
		switch {
		case r < 0x80:
			n++
		case r < 0x800:
			n += 2
		case r < 0x10000:
			n += 3
		default:
			n += 4
		}
	}
	return n
}

// setLine replaces row with line.
func (b *Buffer) setLine(row int, line []rune) {
	b.text = b.text.Replace(row, row+1, [][]rune{line})
}

// OpenLine inserts an empty line below the cursor and moves onto it.
func (b *Buffer) OpenLine() {
	b.change(b.Row + 1)
	b.Row++
	b.text = b.text.Replace(b.Row, b.Row, [][]rune{{}})
	b.Col = 0
}

// Text joins the lines with "\n".
func (b *Buffer) Text() string {
	var sb strings.Builder
	sb.Grow(b.text.Bytes())
	b.text.Each(0, func(i int, line []rune) bool {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(line))
		return true
	})
	return sb.String()
}

// Clamp keeps the cursor inside the text: one past the last character is
// allowed only in insert mode.
func (b *Buffer) Clamp(insert bool) {
	b.Row = min(b.Row, b.text.Len()-1)
	limit := len(b.Line(b.Row))
	if !insert && limit > 0 {
		limit--
	}
	b.Col = min(b.Col, limit)
}

// insertRune returns a new line: line with r inserted at at.
func insertRune(line []rune, at int, r rune) []rune {
	out := make([]rune, 0, len(line)+1)
	return append(append(append(out, line[:at]...), r), line[at:]...)
}

// removeRune returns a new line: line without the character at at.
func removeRune(line []rune, at int) []rune {
	out := make([]rune, 0, len(line)-1)
	return append(append(out, line[:at]...), line[at+1:]...)
}

// Insert types a character at the cursor; '\n' splits the line.
func (b *Buffer) Insert(ch rune) {
	b.change(b.Row)
	line := b.Line(b.Row)
	if ch == '\n' {
		head := line[:b.Col:b.Col]
		tail := line[b.Col:]
		b.text = b.text.Replace(b.Row, b.Row+1, [][]rune{head, tail})
		b.Row++
		b.Col = 0
		return
	}
	b.setLine(b.Row, insertRune(line, b.Col, ch))
	b.Col++
}

// Backspace deletes before the cursor, joining with the previous line at the
// start of a line.
func (b *Buffer) Backspace() {
	if b.Col > 0 {
		b.change(b.Row)
		b.Col--
		b.setLine(b.Row, removeRune(b.Line(b.Row), b.Col))
	} else if b.Row > 0 {
		b.change(b.Row - 1)
		above, line := b.Line(b.Row-1), b.Line(b.Row)
		joined := make([]rune, 0, len(above)+len(line))
		joined = append(append(joined, above...), line...)
		b.text = b.text.Replace(b.Row-1, b.Row+1, [][]rune{joined})
		b.Row--
		b.Col = len(above)
	}
}

// Delete removes the character under the cursor.
func (b *Buffer) Delete() {
	if line := b.Line(b.Row); b.Col < len(line) {
		b.change(b.Row)
		b.setLine(b.Row, removeRune(line, b.Col))
	}
	b.Clamp(false)
}

// Replace overwrites the character under the cursor; an identical character
// is not a change.
func (b *Buffer) Replace(ch rune) {
	line := b.Line(b.Row)
	if b.Col < len(line) && line[b.Col] != ch {
		b.change(b.Row)
		replaced := append([]rune(nil), line...)
		replaced[b.Col] = ch
		b.setLine(b.Row, replaced)
	}
}

// PasteLines inserts whole lines above or below the cursor line.
func (b *Buffer) PasteLines(lines [][]rune, after, terminated bool) {
	empty := b.text.Len() == 1 && len(b.Line(0)) == 0
	if empty {
		b.change(0)
		inserted := CloneLines(lines)
		// An empty buffer is represented by one placeholder line. Restore
		// the copied terminator as well when replacing that placeholder.
		if terminated {
			inserted = append(inserted, []rune{})
		}
		if len(inserted) == 0 {
			inserted = [][]rune{{}}
		}
		b.text = rope.FromLines(inserted)
	} else {
		row := b.Row
		if after {
			row++
		}
		b.change(row)
		b.Row = row
		b.text = b.text.Replace(b.Row, b.Row, CloneLines(lines))
	}
	b.Col = 0
}

func (b *Buffer) offset() int { return b.text.Offset(b.Row) + b.Col }

// rowAt is the row that setOffset(offset) would move the cursor to.
func (b *Buffer) rowAt(offset int) int { return b.text.RowAt(offset) }

func (b *Buffer) setOffset(offset int) {
	b.Row = b.text.RowAt(offset)
	b.Col = min(offset-b.text.Offset(b.Row), len(b.Line(b.Row)))
}

// PasteChars inserts text characterwise after or before the cursor, leaving
// the cursor on the last character inserted.
func (b *Buffer) PasteChars(chars []rune, after bool) {
	if len(chars) == 0 {
		return
	}
	row, col := b.Row, min(b.Col, len(b.Line(b.Row)))
	if after && col < len(b.Line(row)) {
		col++
	}
	b.change(row)
	b.Row, b.Col = b.splice(row, col, row, col, chars)
	// Onto the last inserted character; a line break is at its row's end.
	if chars[len(chars)-1] == '\n' {
		b.Row--
		b.Col = len(b.Line(b.Row))
	} else {
		b.Col--
	}
	b.Clamp(false)
}

// InsertText inserts chars at the cursor, '\n' splitting lines, and leaves the
// cursor just after them, as if they had been typed in Insert mode.
func (b *Buffer) InsertText(chars []rune) {
	if len(chars) == 0 {
		return
	}
	row, col := b.Row, min(b.Col, len(b.Line(b.Row)))
	b.change(row)
	b.Row, b.Col = b.splice(row, col, row, col, chars)
}

// Offset is the cursor's character offset in the text, counting each newline.
func (b *Buffer) Offset() int { return b.offset() }

// SelectionSpan returns the half-open range [start, end) of character offsets
// (a newline counts as one character) covered by the inclusive selection
// between anchor (row, col) and the cursor, clamped to the text. It moves
// nothing.
func (b *Buffer) SelectionSpan(anchor [2]int) (start, end int) {
	a := anchor[1] + b.text.Offset(min(anchor[0], b.text.Len()))
	c := b.offset()
	total := b.length()
	start = min(min(a, c), total)
	end = min(max(a, c)+1, total)
	return start, end
}

// LineSpan returns the range of character offsets from the start of the first
// of two rows to the end of the later one, excluding that row's newline. Rows
// outside the text are clamped.
func (b *Buffer) LineSpan(rowA, rowB int) (start, end int) {
	first := max(min(min(rowA, rowB), b.text.Len()-1), 0)
	last := max(min(max(rowA, rowB), b.text.Len()-1), 0)
	return b.text.Offset(first), b.text.Offset(last) + len(b.Line(last))
}

// length is the number of characters Text would have, counting each newline.
func (b *Buffer) length() int { return b.text.Chars() }

// Span returns a copy of the characters in [start, end) (see SelectionSpan),
// clamped to the text.
func (b *Buffer) Span(start, end int) []rune {
	start = max(start, 0)
	if start >= end {
		return nil
	}
	r1, c1 := b.posAt(start)
	r2, c2 := b.posAt(end)
	return b.between(r1, c1, r2, c2)
}

// ReplaceSpan replaces the characters in [start, end) with text and leaves the
// cursor on the first inserted character (clamped like any Normal-mode
// cursor). Offsets outside the text are clamped.
func (b *Buffer) ReplaceSpan(start, end int, text []rune) {
	start = max(start, 0)
	end = max(end, start)
	r1, c1 := b.posAt(start)
	r2, c2 := b.posAt(end)
	b.change(r1)
	b.splice(r1, c1, r2, c2, text)
	b.Row, b.Col = r1, c1
	b.Clamp(false)
}

// Clone returns an independent buffer with the same text and cursor and no
// history. Edits to the clone never reach b. It shares b's lines, which
// neither buffer modifies, so it costs nothing however long the text is.
func (b *Buffer) Clone() *Buffer {
	return &Buffer{text: b.text, Row: b.Row, Col: b.Col}
}

// Adopt makes b hold the text and cursor of from as one change, so that the
// surrounding edit-transaction records it as one undo step. It takes over the
// lines of from, which must not be used afterwards.
func (b *Buffer) Adopt(from *Buffer) {
	b.change(rope.FirstDifference(b.text, from.text))
	b.text = from.text
	b.Row, b.Col = from.Row, from.Col
	from.text = rope.Rope{}
	b.Clamp(false)
}

// SelectionChars returns the inclusive character selection between anchor
// (row, col) and the cursor, clamped to real scalars (newlines included).
// With cut it also removes them.
func (b *Buffer) SelectionChars(anchor [2]int, cut bool) []rune {
	start, end := b.SelectionSpan(anchor)
	r1, c1 := b.posAt(start)
	r2, c2 := b.posAt(end)
	selected := b.between(r1, c1, r2, c2)
	if cut && start < end {
		b.change(r1)
		b.splice(r1, c1, r2, c2, nil)
	}
	b.Row, b.Col = r1, c1
	b.Clamp(false)
	return selected
}

func wordClass(c rune) int {
	switch {
	case ucd.IsWhitespace(c):
		return 0
	case ucd.IsAlphanumeric(c) || c == '_':
		return 1
	}
	return 2
}

// WordBounds returns where the word under the cursor is on the cursor's line:
// the columns [start, end), 0-based. ok is false when the cursor is on
// anything but a letter, digit or underscore. It moves nothing.
func (b *Buffer) WordBounds() (start, end int, ok bool) {
	line := b.Line(b.Row)
	if b.Col >= len(line) || wordClass(line[b.Col]) != 1 {
		return 0, 0, false
	}
	start, end = b.Col, b.Col+1
	for start > 0 && wordClass(line[start-1]) == 1 {
		start--
	}
	for end < len(line) && wordClass(line[end]) == 1 {
		end++
	}
	return start, end, true
}

// WordAt returns the word under the cursor: the run of letters, digits and
// underscores on the cursor's line that holds the cursor character. It is
// empty when the cursor is on anything else, and it moves nothing.
func (b *Buffer) WordAt() []rune {
	start, end, ok := b.WordBounds()
	if !ok {
		return nil
	}
	return append([]rune(nil), b.Line(b.Row)[start:end]...)
}

// WordMotion moves the cursor by one word: 'w' next start, 'b' previous
// start, 'e' end. Words are runs of alphanumerics, runs of punctuation, and
// line separators count as whitespace.
func (b *Buffer) WordMotion(motion rune) {
	w := b.walkerAt(b.Row, b.Col)
	switch motion {
	case 'w':
		w.nextWord()
	case 'b':
		if !w.atStart() {
			w.prev()
		}
		for !w.atStart() && wordClass(w.char()) == 0 {
			w.prev()
		}
		for !w.atStart() && wordClass(w.peekPrev()) == wordClass(w.char()) {
			w.prev()
		}
	case 'e':
		if !w.atEnd() {
			w.next()
		}
		for !w.atEnd() && wordClass(w.char()) == 0 {
			w.next()
		}
		for {
			next, ok := w.peekNext()
			if !ok || wordClass(next) != wordClass(w.char()) {
				break
			}
			w.next()
		}
	default:
		return
	}
	b.Row, b.Col = w.row, w.col
	b.Clamp(false)
}

// WordRange returns the characterwise range from the cursor over count
// words, including literal line separators. Change retains trailing
// whitespace when starting on a non-whitespace run. With cut the range is
// removed.
func (b *Buffer) WordRange(cut, change bool, count int) []rune {
	start := b.walkerAt(b.Row, b.Col)
	end := start
	for range count {
		before := end
		end.nextWord()
		if end.at(before) {
			break
		}
	}
	if change && !start.atEnd() && wordClass(start.char()) != 0 {
		// cw behaves like ce: retain whitespace after the last selected run.
		for !end.at(start) && wordClass(end.peekPrev()) == 0 {
			end.prev()
		}
	}
	copied := b.between(start.row, start.col, end.row, end.col)
	if cut && !end.at(start) {
		b.change(start.row)
		b.splice(start.row, start.col, end.row, end.col, nil)
		b.Row, b.Col = start.row, start.col
	}
	return copied
}

// Indent shifts the cursor line one level (four spaces) right, or removes one
// level (a tab, or up to four spaces) left.
func (b *Buffer) Indent(right bool) {
	line := b.Line(b.Row)
	if right {
		b.change(b.Row)
		indented := make([]rune, 0, len(line)+4)
		indented = append(append(indented, ' ', ' ', ' ', ' '), line...)
		b.setLine(b.Row, indented)
		b.Col += 4
	} else {
		count := 0
		if len(line) > 0 && line[0] == '\t' {
			count = 1
		} else {
			for count < len(line) && count < 4 && line[count] == ' ' {
				count++
			}
		}
		if count > 0 {
			b.change(b.Row)
			b.setLine(b.Row, append([]rune(nil), line[count:]...))
			b.Col = max(b.Col-count, 0)
		}
	}
	b.Clamp(false)
}

// Find does a literal, case-sensitive search in character coordinates and
// wraps once. Forward finds the next match after the cursor, backward the
// previous one.
func (b *Buffer) Find(query []rune, forward bool) (row, col int, ok bool) {
	if len(query) == 0 {
		return 0, 0, false
	}
	cursor := [2]int{b.Row, b.Col}
	var before, after, next *[2]int
	less := func(a, c [2]int) bool { return a[0] < c[0] || (a[0] == c[0] && a[1] < c[1]) }
	b.text.Each(0, func(r int, line []rune) bool {
		for c := 0; c+len(query) <= len(line); c++ {
			if !runesEqual(line[c:c+len(query)], query) {
				continue
			}
			position := [2]int{r, c}
			if forward {
				if less(cursor, position) {
					next = &position
					return false
				}
				if before == nil {
					p := position
					before = &p
				}
			} else {
				if less(position, cursor) {
					p := position
					before = &p
				}
				p := position
				after = &p
			}
		}
		return true
	})
	var found *[2]int
	switch {
	case next != nil:
		found = next
	case forward:
		found = before
	case before != nil:
		found = before
	default:
		found = after
	}
	if found == nil {
		return 0, 0, false
	}
	return found[0], found[1], true
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DeleteLine removes the cursor line, leaving one empty line in an empty buffer.
func (b *Buffer) DeleteLine() {
	b.change(b.Row)
	if b.text.Len() == 1 {
		b.text = rope.FromLines([][]rune{{}})
	} else {
		b.text = b.text.Replace(b.Row, b.Row+1, nil)
	}
	b.Clamp(false)
}
