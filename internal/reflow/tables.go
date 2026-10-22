package reflow

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// pulldownTables is goldmark's table extension (parsing of rows and cells, rendering) with
// pulldown-cmark's rule for when a paragraph is a table: see startsTable.
type pulldownTables struct{}

func (pulldownTables) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithParagraphTransformers(util.Prioritized(&pulldownTableTransformer{inner: extension.NewTableParagraphTransformer()}, 200)),
		parser.WithASTTransformers(util.Prioritized(extension.NewTableASTTransformer(), 0)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(extension.NewTableHTMLRenderer(), 500)))
}

// pulldownTableTransformer lets goldmark's transformer turn a paragraph into a table only where
// pulldown-cmark does: when the paragraph's first line is a table header, or when a later line
// starting with a pipe is (such a "heavy" table interrupts the paragraph, which keeps the lines
// before it). goldmark itself would accept any line followed by a delimiter row.
type pulldownTableTransformer struct {
	inner parser.ParagraphTransformer
	// text is the source as a string, converted once per parse.
	source []byte
	text   string
}

func (t *pulldownTableTransformer) Transform(node *ast.Paragraph, reader gtext.Reader, pc parser.Context) {
	lines := node.Lines()
	n := lines.Len()
	if n < 2 {
		return
	}
	src := reader.Source()
	if len(t.source) != len(src) || len(src) == 0 || &t.source[0] != &src[0] {
		t.source, t.text = src, string(src)
	}
	text := t.text
	first := lines.At(0)
	lineStart := strings.LastIndexByte(text[:first.Start], '\n') + 1
	continuation := continuationOf(containerPrefix(text[lineStart:first.Start]))
	for j := 0; j+1 < n; j++ {
		header := lines.At(j).Start
		if j == 0 {
			_, isItem := node.Parent().(*ast.ListItem)
			if isItem && node.Parent().FirstChild() == node {
				// A task-list marker is its own event for pulldown-cmark; the header follows it.
				if start, ok := textStart(src, lines, true); ok {
					header = start
				}
			}
			if !startsTable(text, header, continuation, true) {
				continue
			}
		} else if !interruptsParagraph(text, header, continuation) {
			continue
		}
		t.split(node, j, reader, pc)
		return
	}
}

// split makes goldmark build the table whose header is line j of node, keeping lines 0..j-1 as a
// paragraph before it.
func (t *pulldownTableTransformer) split(node *ast.Paragraph, j int, reader gtext.Reader, pc parser.Context) {
	lines := node.Lines()
	all := append([]gtext.Segment(nil), lines.Sliced(0, lines.Len())...)
	parent, prev := node.Parent(), node.PreviousSibling()
	lines.SetSliced(j, len(all))
	t.inner.Transform(node, reader, pc)
	if node.Parent() != nil {
		// goldmark does not agree that this is a table: put the paragraph back as it was.
		restored := gtext.NewSegments()
		restored.AppendAll(all)
		node.SetLines(restored)
		return
	}
	if j == 0 {
		return
	}
	table := parent.FirstChild()
	if prev != nil {
		table = prev.NextSibling()
	}
	before := ast.NewParagraph()
	before.SetBlankPreviousLines(node.HasBlankPreviousLines())
	segments := gtext.NewSegments()
	segments.AppendAll(all[:j])
	last := segments.At(j - 1)
	last.Stop-- // the newline that ended the line, as goldmark's own transformer trims it
	segments.Set(j-1, last)
	before.SetLines(segments)
	parent.InsertBefore(parent, table, before)
}

// interruptsParagraph reports whether the line of a paragraph starting at headerStart begins a
// "heavy" table, one whose header row starts with a pipe, which pulldown-cmark lets interrupt a
// paragraph.
func interruptsParagraph(text string, headerStart int, continuation string) bool {
	lineStart := strings.LastIndexByte(text[:headerStart], '\n') + 1
	indent, found := strings.CutPrefix(text[lineStart:headerStart], continuation)
	if !found || columns(indent) > 3 || text[headerStart] != '|' {
		return false
	}
	return startsTable(text, headerStart, continuation, false)
}

// columns is the width of leading white space made of spaces and tabs (tab stops of four).
func columns(space string) int {
	n := 0
	for i := 0; i < len(space); i++ {
		if space[i] == '\t' {
			n += 4 - n%4
		} else {
			n++
		}
	}
	return n
}

// containerPrefix is the part of the prefix of a paragraph's first line that belongs to its
// containers: block quote markers (with the one space that may follow) and list markers with the
// white space after them. What follows the last of those is only the paragraph's own indentation.
func containerPrefix(prefix string) string {
	isSpace := func(i int) bool { return i < len(prefix) && (prefix[i] == ' ' || prefix[i] == '\t') }
	end := 0
	for i := 0; ; {
		j := i
		for isSpace(j) {
			j++
		}
		if j < len(prefix) && prefix[j] == '>' {
			j++
			if isSpace(j) {
				j++
			}
			i, end = j, j
			continue
		}
		// A list marker, which must be followed by white space.
		k := j
		if k < len(prefix) && strings.IndexByte("-+*", prefix[k]) >= 0 {
			k++
		} else {
			for k < len(prefix) && prefix[k] >= '0' && prefix[k] <= '9' {
				k++
			}
			if k == j || k >= len(prefix) || (prefix[k] != '.' && prefix[k] != ')') {
				return prefix[:end]
			}
			k++
		}
		if !isSpace(k) {
			return prefix[:end]
		}
		for isSpace(k) {
			k++
		}
		i, end = k, k
	}
}

// startsTable reports whether pulldown-cmark reads the paragraph text beginning at headerStart
// and the line after it as a table header and delimiter row. Such a paragraph is a table and has
// no Paragraph event. continuation is the container prefix of the lines after the first.
//
// pulldown-cmark only looks at the first line of a paragraph: it must contain a pipe that is not
// backslash-escaped, the next line (inside the same containers) must be a delimiter row that also
// has a pipe, and both must have the same number of columns.
//
// firstLine selects the rules of a paragraph's first line, where a header that ends in a
// backslash is a hard break and no table; a line that interrupts a paragraph has no such rule.
func startsTable(text string, headerStart int, continuation string, firstLine bool) bool {
	if headerStart >= len(text) {
		return false
	}
	eol := strings.IndexAny(text[headerStart:], "\r\n")
	if eol < 0 {
		return false
	}
	eol += headerStart
	header := text[headerStart:eol]
	next := nextLine(text, headerStart)

	pipes, lastPipe := 0, 0
	for i := 0; i < len(header); i++ {
		if header[i] == '|' && (headerStart+i == 0 || text[headerStart+i-1] != '\\') {
			pipes++
			lastPipe = i
		}
	}
	if pipes == 0 {
		return false
	}
	// A backslash before the line break is a hard break, not the end of a table header.
	trailing := len(header) - len(strings.TrimRight(header, "\\"))
	if firstLine && trailing%2 == 1 && next < len(text) {
		return false
	}
	if next >= len(text) {
		return false
	}
	rest, found := strings.CutPrefix(text[next:], continuation)
	if !found {
		return false
	}
	if end := strings.IndexByte(rest, '\n'); end >= 0 {
		rest = rest[:end+1]
	}
	columns, ok := scanTableHead([]byte(rest))
	if !ok {
		return false
	}
	return columns == countHeaderCols(header, pipes, lastPipe)
}

// nextLine is the offset of the line after the one containing from (len(text) for the last line).
func nextLine(text string, from int) int {
	eol := strings.IndexAny(text[from:], "\r\n")
	if eol < 0 {
		return len(text)
	}
	eol += from
	if text[eol] == '\r' && eol+1 < len(text) && text[eol+1] == '\n' {
		return eol + 2
	}
	return eol + 1
}

// countHeaderCols is count_header_cols: the pipes of the header row minus the one before the
// first cell and plus one for a last cell that is not closed by a pipe.
func countHeaderCols(header string, pipes, lastPipe int) int {
	if strings.TrimLeft(header, " \t\v\f")[:1] == "|" {
		pipes--
	}
	if strings.Trim(header[lastPipe+1:], " \t\v\f") == "" {
		return pipes
	}
	return pipes + 1
}

// scanTableHead is scan_table_head: whether data starts with a table delimiter row, and its
// number of columns.
func scanTableHead(data []byte) (columns int, ok bool) {
	i, spaces := calcIndent(data, 4)
	if spaces > 3 || i == len(data) {
		return 0, false
	}
	startCol := true
	foundPipe, foundHyphen, foundHyphenInCol := false, false, false
	if data[i] == '|' {
		i++
		foundPipe = true
	}
	for ; i < len(data); i++ {
		if data[i] == '\n' || data[i] == '\r' {
			break
		}
		switch data[i] {
		case ' ':
		case ':':
			startCol = false
		case '-':
			startCol = false
			foundHyphen = true
			foundHyphenInCol = true
		case '|':
			startCol = true
			foundPipe = true
			columns++
			if !foundHyphenInCol {
				// It isn't a table head if it has back-to-back pipes.
				return 0, false
			}
			foundHyphenInCol = false
		default:
			// It isn't a table head if it has characters outside the allowed set.
			return 0, false
		}
	}
	if !startCol {
		columns++
	}
	if !foundPipe || !foundHyphen {
		// It isn't a table head if it doesn't have a least one pipe or hyphen.
		// It's a list, a header, or a thematic break.
		return 0, false
	}
	return columns, true
}

// calcIndent is calc_indent: the offset where the indentation of text ends and the number of
// columns it spans, looking at no more than max columns.
//
// Rust oddity preserved: when text ends inside the indentation the offset is that of the last byte.
func calcIndent(text []byte, max int) (offset, spaces int) {
	for i, b := range text {
		offset = i
		switch b {
		case ' ':
			spaces++
			if spaces == max {
				return offset, spaces
			}
		case '\t':
			newSpaces := spaces + 4 - (spaces & 3)
			if newSpaces > max {
				return offset, spaces
			}
			spaces = newSpaces
		default:
			return offset, spaces
		}
	}
	return offset, spaces
}
