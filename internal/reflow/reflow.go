// Package reflow fills (re-wraps) the Markdown paragraph under the cursor while preserving the
// source text around it. The parser supplies block and inline boundaries.
//
// The Rust editor asks pulldown-cmark for event byte ranges; this port asks goldmark (with the
// table and task-list extensions only) for the equivalent block structure and then follows the
// Rust algorithm line for line. See paragraphSpans for how goldmark nodes map to the paragraph
// ranges pulldown-cmark reports.
package reflow

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer/html"
	gtext "github.com/yuin/goldmark/text"

	"github.com/fclaude/coigue/internal/ucd"
	uwidth "github.com/fclaude/coigue/internal/width"
)

// newMarkdown is Parser::new_ext(source, ENABLE_TABLES | ENABLE_TASKLISTS) for goldmark: no
// strikethrough, footnotes, smart punctuation or heading attributes. It parses the block structure
// and renders the HTML for the comparison; raw HTML is passed through (WithUnsafe) so that the
// comparison sees it instead of a placeholder. The tables are goldmark's own behind
// pulldown-cmark's rules for when a paragraph is a table (pulldownTables).
func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(pulldownTables{}, extension.TaskList),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
}

// span is the byte range of a Start(Paragraph) event, and whether the events inside it contain a
// HardBreak or InlineHtml.
type span struct {
	start, end int
	// headerStart is where the paragraph text proper begins: start, or after a task-list marker.
	headerStart int
	// containerEnd is the end of the container prefix of the first line (the start of a
	// task-list marker, which start may be past).
	containerEnd int
	unsafe       bool
}

// Paragraph returns the byte range [start, end) of the paragraph under cursor and the text that
// should replace it when the paragraph is re-wrapped to width columns. ok is false when the
// cursor is not in a paragraph or when filling it could change what the Markdown means.
func Paragraph(text string, cursor int, width int) (start, end int, replacement string, ok bool) {
	md := newMarkdown()
	spans := paragraphSpans(md, text)
	// The line after a table header is the delimiter row. goldmark may make something else of
	// it (a list item, for instance) but for pulldown-cmark it belongs to the table.
	delimiterRows := map[int]bool{}
	for _, p := range spans {
		lineStart := strings.LastIndexByte(text[:p.start], '\n') + 1
		if startsTable(text, p.headerStart, continuationOf(containerPrefix(text[lineStart:p.containerEnd])), true) {
			delimiterRows[nextLine(text, p.headerStart)] = true
		}
	}
	for _, p := range spans {
		lineStart := strings.LastIndexByte(text[:p.start], '\n') + 1
		if cursor < lineStart || cursor >= p.end {
			continue
		}
		firstPrefix := text[lineStart:p.start]
		continuation := continuationOf(firstPrefix)
		if delimiterRows[lineStart] ||
			startsTable(text, p.headerStart, continuationOf(containerPrefix(text[lineStart:p.containerEnd])), true) {
			// pulldown-cmark makes this a table, which has no Paragraph event.
			continue
		}
		if p.unsafe {
			return 0, 0, "", false
		}
		start := lineStart
		end := p.end
		if strings.HasSuffix(text[:end], "\n") {
			end--
		}
		if strings.HasSuffix(text[:end], "\r") {
			end--
		}
		source := text[p.start:end]
		var joinedBuilder strings.Builder
		for n, line := range strings.Split(source, "\n") {
			line = strings.TrimSuffix(line, "\r")
			if n > 0 {
				var found bool
				line, found = strings.CutPrefix(line, continuation)
				if !found {
					return 0, 0, "", false
				}
				joinedBuilder.WriteByte(' ')
			}
			joinedBuilder.WriteString(line)
		}
		joined := joinedBuilder.String()
		tokens, balanced := tokenize(joined)
		if !balanced {
			return 0, 0, "", false
		}
		newline := "\n"
		if strings.Contains(text[start:p.end], "\r\n") {
			newline = "\r\n"
		}
		var output strings.Builder
		output.WriteString(firstPrefix)
		used := uwidth.String(firstPrefix)
		hasWord := false
		for _, token := range tokens {
			size := uwidth.String(token)
			// Do not introduce a Markdown block marker at the beginning of a source line.
			first, _ := utf8.DecodeRuneInString(token)
			safeStart := !strings.ContainsRune("#>-+*`~[", first) && !ucd.IsASCIIDigit(first)
			if hasWord && used+1+size > width && safeStart {
				output.WriteString(newline)
				output.WriteString(continuation)
				used = uwidth.String(continuation)
				hasWord = false
			}
			if hasWord {
				output.WriteByte(' ')
				used++
			}
			output.WriteString(token)
			used += size
			hasWord = true
		}
		// Refuse transformations that change Markdown rendering (apart from whitespace).
		// This also catches ambiguous lazy continuations and nested container prefixes.
		replacement := output.String()
		candidate := text[:start] + replacement + text[end:]
		before, okBefore := render(md, text)
		after, okAfter := render(md, candidate)
		if !okBefore || !okAfter || before != after {
			return 0, 0, "", false
		}
		return start, end, replacement, true
	}
	return 0, 0, "", false
}

// continuationOf preserves quote markers and turns list markers into continuation indentation.
func continuationOf(prefix string) string {
	var cont strings.Builder
	for _, c := range prefix {
		if c == '>' || ucd.IsWhitespace(c) {
			cont.WriteRune(c)
		} else {
			cont.WriteByte(' ')
		}
	}
	return cont.String()
}

// tokenize splits joined at whitespace, treating balanced inline constructs as indivisible spans
// that keep their whitespace. It reports false when backticks or brackets are left open.
func tokenize(joined string) (tokens []string, balanced bool) {
	type char struct {
		offset int
		r      rune
		size   int
	}
	chars := make([]char, 0, len(joined))
	for off := 0; off < len(joined); {
		r, size := utf8.DecodeRuneInString(joined[off:])
		chars = append(chars, char{off, r, size})
		off += size
	}
	begin := 0
	brackets := 0
	ticks := 0
	escaped := false
	for i := 0; i < len(chars); {
		offset, c := chars[i].offset, chars[i].r
		if escaped {
			escaped = false
			i++
			continue
		}
		if c == '\\' {
			escaped = true
			i++
			continue
		}
		if c == '`' {
			count := 0
			for _, ch := range chars[i:] {
				if ch.r != '`' {
					break
				}
				count++
			}
			if ticks == 0 {
				ticks = count
			} else if ticks == count {
				ticks = 0
			}
			i += count
			continue
		}
		if ticks == 0 {
			if c == '[' || c == '(' || c == '<' {
				brackets++
			}
			if c == ']' || c == ')' || c == '>' {
				if brackets > 0 {
					brackets--
				}
			}
			if ucd.IsWhitespace(c) && brackets == 0 {
				if begin < offset {
					tokens = append(tokens, joined[begin:offset])
				}
				begin = offset + chars[i].size
			}
		}
		i++
	}
	if ticks != 0 || brackets != 0 {
		return nil, false
	}
	if begin < len(joined) {
		tokens = append(tokens, joined[begin:])
	}
	return tokens, true
}

// render is the whitespace-normalised HTML of source. ok is false if rendering failed.
//
// It renders the text as it is, with its line terminators: goldmark leaves a carriage return in a
// code span that spans lines, and pulldown-cmark turns a CRLF there into two spaces, so that both
// engines see a difference from the single-line form that LF-only text would not show.
func render(md goldmark.Markdown, source string) (rendered string, ok bool) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(source), &buf); err != nil {
		return "", false
	}
	return strings.Join(strings.FieldsFunc(buf.String(), ucd.IsWhitespace), " "), true
}

// paragraphSpans lists, in document order, the paragraphs pulldown-cmark would report with
// byte ranges, as src/reflow.rs sees them after its tight-list expansion:
//
//   - every ast.Paragraph (a Start(Paragraph) event). Its range starts at the first text byte
//     (after container prefixes and leading spaces) and ends after the line terminator of its
//     last line, trailing spaces included, or at the end of the text. In a loose list item the
//     range starts after a task-list marker, which pulldown-cmark emits as its own event.
//   - the leading inline run of a tight list item, which pulldown-cmark reports without a
//     Paragraph event and which reflow.rs wraps in a synthetic one. goldmark makes it the first
//     child of the item, an ast.TextBlock. Its range starts at the first inline event (the task
//     marker included) and ends after the last content byte, without trailing spaces or newline.
func paragraphSpans(md goldmark.Markdown, text string) []span {
	le := normalizeLineEnds(text)
	src := le.norm
	doc := md.Parser().Parse(gtext.NewReader(src))
	var spans []span
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindParagraph:
			if s, ok := looseSpan(src, n); ok {
				spans = append(spans, le.original(s))
			}
		case ast.KindTextBlock:
			if item, isItem := n.Parent().(*ast.ListItem); isItem && item.FirstChild() == n {
				if s, ok := tightSpan(src, n); ok {
					spans = append(spans, le.original(s))
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return spans
}

// lineEnds is the text with every CRLF reduced to LF and every lone CR turned into LF, which is
// how pulldown-cmark reads line endings (goldmark trips over CRLF in places, for instance it does
// not see an empty list item "-\r\n"), and the map from offsets in it back to offsets in the text.
type lineEnds struct {
	norm []byte
	// back[i] is the offset in the text where norm[i] begins (that of the CR for the LF of a CRLF);
	// back[len(norm)] is len(text).
	back []int
}

func normalizeLineEnds(text string) lineEnds {
	le := lineEnds{
		norm: make([]byte, 0, len(text)),
		back: make([]int, 0, len(text)+1),
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\r' {
			c = '\n'
			if i+1 < len(text) && text[i+1] == '\n' {
				i++ // the pair is one LF that begins at the CR
				le.norm = append(le.norm, c)
				le.back = append(le.back, i-1)
				continue
			}
		}
		le.norm = append(le.norm, c)
		le.back = append(le.back, i)
	}
	le.back = append(le.back, len(text))
	return le
}

// original converts the offsets of a span of the normalised text.
func (le lineEnds) original(s span) span {
	return span{
		start:        le.back[s.start],
		end:          le.back[s.end],
		headerStart:  le.back[s.headerStart],
		containerEnd: le.back[s.containerEnd],
		unsafe:       s.unsafe,
	}
}

func looseSpan(src []byte, n ast.Node) (span, bool) {
	lines := n.Lines()
	if lines.Len() == 0 {
		return span{}, false
	}
	_, isTask := n.FirstChild().(*extast.TaskCheckBox)
	start, ok := textStart(src, lines, isTask)
	if !ok {
		// Only a task-list marker. pulldown-cmark emits no Paragraph for it, so the marker is
		// a run of its own, as in a tight item.
		return tightSpan(src, n)
	}
	end := lines.At(lines.Len() - 1).Stop
	for end < len(src) && src[end] != '\n' {
		end++
	}
	if end < len(src) {
		end++
	}
	if start > end {
		return span{}, false
	}
	containerEnd := lines.At(0).Start
	if start > containerEnd && bytes.IndexByte(src[containerEnd:start], '\n') >= 0 {
		// The text begins on a later line than the task-list marker.
		containerEnd = start
	}
	return span{start, end, start, containerEnd, hasBreakOrHTML(src, n)}, true
}

func tightSpan(src []byte, n ast.Node) (span, bool) {
	lines := n.Lines()
	if lines.Len() == 0 {
		return span{}, false
	}
	containerEnd := lines.At(0).Start
	start := containerEnd
	end := lines.At(lines.Len() - 1).Stop
	if start >= end {
		return span{}, false
	}
	_, isTask := n.FirstChild().(*extast.TaskCheckBox)
	if !isTask || !taskMarkerAt(src, start) {
		// A backslash escape is not part of the first event's range in pulldown-cmark: the text
		// event begins at the escaped character.
		if src[start] == '\\' && start+1 < len(src) && isASCIIPunctuation(src[start+1]) {
			start++
		}
	}
	headerStart, ok := textStart(src, lines, isTask)
	if !ok {
		// Only a task-list marker: there is no text that could be a table header.
		headerStart = start
	}
	return span{start, end, headerStart, containerEnd, hasBreakOrHTML(src, n)}, true
}

// textStart is where the text of a paragraph or tight item run with the given lines begins as pulldown-cmark
// sees it: the first line's start, or, after a task-list marker, the first byte after the
// whitespace that follows the marker (on the next line when the rest of the line is blank).
func textStart(src []byte, lines *gtext.Segments, hasCheckBox bool) (int, bool) {
	start := lines.At(0).Start
	if !hasCheckBox || !taskMarkerAt(src, start) {
		return start, true
	}
	i := start + 3
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	switch {
	case i < len(src) && src[i] != '\n' && src[i] != '\r':
		return i, true
	case lines.Len() > 1:
		return lines.At(1).Start, true
	}
	return 0, false
}

func isASCIIPunctuation(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

// taskMarkerAt reports whether pulldown-cmark reads a task-list marker at src[i:]: "[ ]", "[x]"
// or "[X]" followed by an ASCII whitespace byte. goldmark is more permissive (it accepts "[ ]x"
// and any whitespace inside the brackets), so its TaskCheckBox node alone does not decide.
func taskMarkerAt(src []byte, i int) bool {
	if i+3 >= len(src) || src[i] != '[' || src[i+2] != ']' {
		return false
	}
	switch c := src[i+1]; c {
	case ' ', '\t', 0x0b, 0x0c, 'x', 'X':
	default:
		return false
	}
	c := src[i+3]
	return c == ' ' || (c >= 0x09 && c <= 0x0d)
}

// hasBreakOrHTML reports whether a HardBreak or InlineHtml event occurs inside the paragraph.
func hasBreakOrHTML(src []byte, n ast.Node) bool {
	found := false
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.RawHTML:
			found = true
		case *ast.Text:
			if c.HardLineBreak() || endsInBreakWhitespace(src, c) {
				found = true
			}
		}
		return ast.WalkContinue, nil
	})
	return found
}

// endsInBreakWhitespace reports whether t ends a line followed by two or more spaces or tabs,
// which pulldown-cmark takes for a hard break; CommonMark (and goldmark) only count spaces.
func endsInBreakWhitespace(src []byte, t *ast.Text) bool {
	if !t.SoftLineBreak() {
		return false
	}
	n := 0
	for i := t.Segment.Stop; i < len(src) && src[i] != '\n'; i++ {
		switch src[i] {
		case ' ', '\t', 0x0b, 0x0c:
			n++
		}
	}
	return n >= 2
}
