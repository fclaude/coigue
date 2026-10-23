// Package buffer is the editable text of one file: lines of Unicode scalars, a
// cursor, and undo history. Every text mutation goes through a method of
// Buffer, and every mutation first calls change, which is the single place that
// captures the pre-edit snapshot. BeginEdit and FinishEdit bracket an
// edit-transaction: all changes made between them become one undo step.
package buffer

import (
	"strings"

	"github.com/fclaude/coigue/internal/ucd"
)

// HistoryLimit bounds the undo stack (and, through it, the redo stack).
const HistoryLimit = 100

type snapshot struct {
	lines [][]rune
	row   int
	col   int
}

// Buffer holds lines as rune slices (Rust's Vec<Vec<char>>). The cursor
// (Row, Col) may be moved freely by the editor; text only changes through the
// methods below.
type Buffer struct {
	lines      [][]rune
	Row        int
	Col        int
	revision   uint64
	undo       []snapshot
	redo       []snapshot
	editOrigin *[2]int
	beforeEdit *snapshot
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
	return &Buffer{lines: splitLines(text)}
}

// Lines returns the buffer's lines for reading. Callers must not modify them:
// all edits go through Buffer methods so history stays consistent.
func (b *Buffer) Lines() [][]rune { return b.lines }

// LineCount returns the number of lines (at least one).
func (b *Buffer) LineCount() int { return len(b.lines) }

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

func equalLines(a, b [][]rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// BeginEdit starts (or continues) an edit-transaction, remembering where the
// cursor was so undo returns there.
func (b *Buffer) BeginEdit() {
	if b.editOrigin == nil {
		b.editOrigin = &[2]int{b.Row, b.Col}
	}
}

func (b *Buffer) snapshot() snapshot {
	return snapshot{lines: CloneLines(b.lines), row: b.Row, col: b.Col}
}

// change is called before every mutation. It captures lazily: navigation,
// copying and search never copy the buffer.
func (b *Buffer) change() {
	if b.beforeEdit == nil {
		s := b.snapshot()
		if b.editOrigin != nil {
			s.row, s.col = b.editOrigin[0], b.editOrigin[1]
		}
		b.beforeEdit = &s
	}
	b.revision++
}

// FinishEdit closes the transaction. A transaction whose net effect is no
// change leaves no undo step and keeps the redo history.
func (b *Buffer) FinishEdit() {
	b.editOrigin = nil
	s := b.beforeEdit
	b.beforeEdit = nil
	if s != nil && !equalLines(s.lines, b.lines) {
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
	b.lines = s.lines
	b.Row = s.row
	b.Col = s.col
	// Revisions must increase even when restoring older contents, so the
	// renderer invalidates highlighting and its cached dirty status.
	b.revision++
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
	for _, line := range b.lines[:b.Row] {
		cursor += runeBytes(line) + 1
	}
	cursor += runeBytes(b.lines[b.Row][:b.Col])
	start, end, replacement, ok := fill(text, cursor, width)
	if !ok {
		return false
	}
	if text[start:end] == replacement {
		return false
	}
	b.change()
	updated := text[:start] + replacement + text[end:]
	b.lines = splitLines(updated)
	b.Row = strings.Count(text[:start], "\n")
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

// OpenLine inserts an empty line below the cursor and moves onto it.
func (b *Buffer) OpenLine() {
	b.change()
	b.Row++
	b.lines = append(b.lines, nil)
	copy(b.lines[b.Row+1:], b.lines[b.Row:])
	b.lines[b.Row] = []rune{}
	b.Col = 0
}

// Text joins the lines with "\n".
func (b *Buffer) Text() string {
	var sb strings.Builder
	for i, line := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(line))
	}
	return sb.String()
}

// Clamp keeps the cursor inside the text: one past the last character is
// allowed only in insert mode.
func (b *Buffer) Clamp(insert bool) {
	b.Row = min(b.Row, len(b.lines)-1)
	limit := len(b.lines[b.Row])
	if !insert && limit > 0 {
		limit--
	}
	b.Col = min(b.Col, limit)
}

func insertRune(line []rune, at int, r rune) []rune {
	line = append(line, 0)
	copy(line[at+1:], line[at:])
	line[at] = r
	return line
}

func removeRune(line []rune, at int) []rune {
	return append(line[:at], line[at+1:]...)
}

// Insert types a character at the cursor; '\n' splits the line.
func (b *Buffer) Insert(ch rune) {
	b.change()
	if ch == '\n' {
		tail := append([]rune(nil), b.lines[b.Row][b.Col:]...)
		b.lines[b.Row] = b.lines[b.Row][:b.Col]
		b.Row++
		b.lines = append(b.lines, nil)
		copy(b.lines[b.Row+1:], b.lines[b.Row:])
		b.lines[b.Row] = tail
		b.Col = 0
		return
	}
	b.lines[b.Row] = insertRune(b.lines[b.Row], b.Col, ch)
	b.Col++
}

// Backspace deletes before the cursor, joining with the previous line at the
// start of a line.
func (b *Buffer) Backspace() {
	if b.Col > 0 {
		b.change()
		b.Col--
		b.lines[b.Row] = removeRune(b.lines[b.Row], b.Col)
	} else if b.Row > 0 {
		b.change()
		line := b.lines[b.Row]
		b.lines = append(b.lines[:b.Row], b.lines[b.Row+1:]...)
		b.Row--
		b.Col = len(b.lines[b.Row])
		b.lines[b.Row] = append(b.lines[b.Row], line...)
	}
}

// Delete removes the character under the cursor.
func (b *Buffer) Delete() {
	if b.Col < len(b.lines[b.Row]) {
		b.change()
		b.lines[b.Row] = removeRune(b.lines[b.Row], b.Col)
	}
	b.Clamp(false)
}

// Replace overwrites the character under the cursor; an identical character
// is not a change.
func (b *Buffer) Replace(ch rune) {
	line := b.lines[b.Row]
	if b.Col < len(line) && line[b.Col] != ch {
		b.change()
		b.lines[b.Row][b.Col] = ch
	}
}

// PasteLines inserts whole lines above or below the cursor line.
func (b *Buffer) PasteLines(lines [][]rune, after, terminated bool) {
	b.change()
	if len(b.lines) == 1 && len(b.lines[0]) == 0 {
		b.lines = CloneLines(lines)
		// An empty buffer is represented by one placeholder line. Restore
		// the copied terminator as well when replacing that placeholder.
		if terminated {
			b.lines = append(b.lines, []rune{})
		}
	} else {
		if after {
			b.Row++
		}
		inserted := CloneLines(lines)
		tail := append([][]rune(nil), b.lines[b.Row:]...)
		b.lines = append(append(b.lines[:b.Row], inserted...), tail...)
	}
	b.Col = 0
}

func (b *Buffer) flatChars() []rune {
	var out []rune
	for i, line := range b.lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, line...)
	}
	return out
}

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

func (b *Buffer) offset() int {
	n := b.Col
	for _, line := range b.lines[:b.Row] {
		n += len(line) + 1
	}
	return n
}

func (b *Buffer) setOffset(offset int) {
	b.Row = 0
	for b.Row+1 < len(b.lines) && offset > len(b.lines[b.Row]) {
		offset -= len(b.lines[b.Row]) + 1
		b.Row++
	}
	b.Col = min(offset, len(b.lines[b.Row]))
}

// PasteChars inserts text characterwise after or before the cursor.
func (b *Buffer) PasteChars(chars []rune, after bool) {
	if len(chars) == 0 {
		return
	}
	start := b.offset()
	if after && b.Col < len(b.lines[b.Row]) {
		start++
	}
	b.change()
	text := b.flatChars()
	spliced := make([]rune, 0, len(text)+len(chars))
	spliced = append(append(append(spliced, text[:start]...), chars...), text[start:]...)
	b.lines = splitChars(spliced)
	b.setOffset(start + len(chars) - 1)
	b.Clamp(false)
}

// Offset is the cursor's character offset in the text, counting each newline.
func (b *Buffer) Offset() int { return b.offset() }

// SelectionSpan returns the half-open range [start, end) of character offsets
// (a newline counts as one character) covered by the inclusive selection
// between anchor (row, col) and the cursor, clamped to the text. It moves
// nothing.
func (b *Buffer) SelectionSpan(anchor [2]int) (start, end int) {
	row := min(anchor[0], len(b.lines))
	a := anchor[1]
	for _, line := range b.lines[:row] {
		a += len(line) + 1
	}
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
	first := max(min(min(rowA, rowB), len(b.lines)-1), 0)
	last := max(min(max(rowA, rowB), len(b.lines)-1), 0)
	for i, line := range b.lines[:last+1] {
		if i < first {
			start += len(line) + 1
		}
		end += len(line) + 1
	}
	return start, end - 1
}

// length is the number of characters Text would have, counting each newline.
func (b *Buffer) length() int {
	n := len(b.lines) - 1
	for _, line := range b.lines {
		n += len(line)
	}
	return n
}

// Span returns a copy of the characters in [start, end) (see SelectionSpan),
// clamped to the text.
func (b *Buffer) Span(start, end int) []rune {
	start = max(start, 0)
	var out []rune
	at := 0
	for i, line := range b.lines {
		if i > 0 {
			if at >= start && at < end {
				out = append(out, '\n')
			}
			at++
		}
		from, to := max(start-at, 0), min(end-at, len(line))
		if from < to {
			out = append(out, line[from:to]...)
		}
		at += len(line)
		if at >= end {
			break
		}
	}
	return out
}

// ReplaceSpan replaces the characters in [start, end) with text and leaves the
// cursor on the first inserted character (clamped like any Normal-mode
// cursor). Offsets outside the text are clamped.
func (b *Buffer) ReplaceSpan(start, end int, text []rune) {
	b.change()
	chars := b.flatChars()
	start = max(min(start, len(chars)), 0)
	end = max(min(end, len(chars)), start)
	spliced := make([]rune, 0, len(chars)-(end-start)+len(text))
	spliced = append(append(append(spliced, chars[:start]...), text...), chars[end:]...)
	b.lines = splitChars(spliced)
	b.setOffset(start)
	b.Clamp(false)
}

// Clone returns an independent buffer with the same text and cursor and no
// history. Edits to the clone never reach b.
func (b *Buffer) Clone() *Buffer {
	return &Buffer{lines: CloneLines(b.lines), Row: b.Row, Col: b.Col}
}

// Adopt makes b hold the text and cursor of from as one change, so that the
// surrounding edit-transaction records it as one undo step. It takes over the
// lines of from, which must not be used afterwards.
func (b *Buffer) Adopt(from *Buffer) {
	b.change()
	b.lines = from.lines
	b.Row, b.Col = from.Row, from.Col
	from.lines = nil
	b.Clamp(false)
}

// SelectionChars returns the inclusive character selection between anchor
// (row, col) and the cursor, clamped to real scalars (newlines included).
// With cut it also removes them.
func (b *Buffer) SelectionChars(anchor [2]int, cut bool) []rune {
	start, end := b.SelectionSpan(anchor)
	chars := b.flatChars()
	selected := append([]rune(nil), chars[start:end]...)
	if cut && start < end {
		b.change()
		chars = append(chars[:start], chars[end:]...)
		b.lines = splitChars(chars)
	}
	b.setOffset(start)
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

// WordAt returns the word under the cursor: the run of letters, digits and
// underscores on the cursor's line that holds the cursor character. It is
// empty when the cursor is on anything else, and it moves nothing.
func (b *Buffer) WordAt() []rune {
	line := b.lines[b.Row]
	if b.Col >= len(line) || wordClass(line[b.Col]) != 1 {
		return nil
	}
	start, end := b.Col, b.Col+1
	for start > 0 && wordClass(line[start-1]) == 1 {
		start--
	}
	for end < len(line) && wordClass(line[end]) == 1 {
		end++
	}
	return append([]rune(nil), line[start:end]...)
}

func nextWord(chars []rune, at int) int {
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

// WordMotion moves the cursor by one word: 'w' next start, 'b' previous
// start, 'e' end. Words are runs of alphanumerics, runs of punctuation, and
// line separators count as whitespace.
func (b *Buffer) WordMotion(motion rune) {
	chars := b.flatChars()
	at := b.offset()
	switch motion {
	case 'w':
		at = nextWord(chars, at)
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

// WordRange returns the characterwise range from the cursor over count
// words, including literal line separators. Change retains trailing
// whitespace when starting on a non-whitespace run. With cut the range is
// removed.
func (b *Buffer) WordRange(cut, change bool, count int) []rune {
	chars := b.flatChars()
	start := b.offset()
	end := start
	for i := 0; i < count; i++ {
		next := nextWord(chars, end)
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
		b.change()
		chars = append(chars[:start], chars[end:]...)
		b.lines = splitChars(chars)
		b.setOffset(start)
	}
	return copied
}

// Indent shifts the cursor line one level (four spaces) right, or removes one
// level (a tab, or up to four spaces) left.
func (b *Buffer) Indent(right bool) {
	if right {
		b.change()
		line := make([]rune, 0, len(b.lines[b.Row])+4)
		line = append(append(line, ' ', ' ', ' ', ' '), b.lines[b.Row]...)
		b.lines[b.Row] = line
		b.Col += 4
	} else {
		line := b.lines[b.Row]
		count := 0
		if len(line) > 0 && line[0] == '\t' {
			count = 1
		} else {
			for count < len(line) && count < 4 && line[count] == ' ' {
				count++
			}
		}
		if count > 0 {
			b.change()
			b.lines[b.Row] = append(line[:0], line[count:]...)
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
	var before, after *[2]int
	less := func(a, c [2]int) bool { return a[0] < c[0] || (a[0] == c[0] && a[1] < c[1]) }
	for r, line := range b.lines {
		for c := 0; c+len(query) <= len(line); c++ {
			if !runesEqual(line[c:c+len(query)], query) {
				continue
			}
			position := [2]int{r, c}
			if forward {
				if less(cursor, position) {
					return r, c, true
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
	}
	var found *[2]int
	if forward {
		found = before
	} else if before != nil {
		found = before
	} else {
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
	b.change()
	b.lines = append(b.lines[:b.Row], b.lines[b.Row+1:]...)
	if len(b.lines) == 0 {
		b.lines = append(b.lines, []rune{})
	}
	b.Clamp(false)
}
