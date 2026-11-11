package syntax

import "github.com/fclaude/coigue/internal/fsx"

// State is what a line lexer carries from the end of one line to the start of
// the next: whether a block comment or a string is still open, and for Rust
// how deeply comments nest and how many hashes close a raw string. The zero
// State is the start of a file.
type State struct {
	depth  int
	hashes int
	flags  uint8
}

const (
	goBlockComment = 1 << iota
	goRawString
)

const (
	rustQuoted = 1 << iota
	rustRaw
)

const (
	schemeBlock = 1 << iota
	schemeQuoted
	schemeRaw
)

// lineLexer styles one line given the State the previous line ended in, and
// returns the State this line ends in.
type lineLexer func(line []rune, st State) ([]Style, State)

func lexAll(lines [][]rune, lex lineLexer) [][]Style {
	result := make([][]Style, 0, len(lines))
	var st State
	for _, line := range lines {
		var styles []Style
		styles, st = lex(line, st)
		result = append(result, styles)
	}
	return result
}

// Incremental highlights a file whose lexer carries only a State from line
// to line (Go, Rust and Brote), so that after an edit only the lines from the
// first changed one need lexing again, and only as far down as is shown. For
// other files (Markdown, whose fences look ahead) Lines lexes everything
// after each Invalidate, as ForPath does.
type Incremental struct {
	path   string
	lex    lineLexer // nil: relex everything with ForPath
	styles [][]Style // styles of the first len(styles) lines
	ends   []State   // ends[i] is the State line i ends in
	full   bool      // lex == nil and styles holds the whole file
}

// NewIncremental returns a highlighter for path. ok is false when ForPath has
// no lexer for it either, and nothing is highlighted.
func NewIncremental(path string) (h *Incremental, ok bool) {
	ext, found := fsx.Extension(path)
	if !found {
		return nil, false
	}
	h = &Incremental{path: path}
	switch lowerASCII(ext) {
	case "go":
		h.lex = goLine
	case "rs":
		h.lex = rustLine
	case "scm":
		h.lex = schemeLine
	case "md", "markdown":
	default:
		return nil, false
	}
	return h, true
}

// Invalidate forgets the styles of row and every row after it, because the
// text from row on may have changed.
func (h *Incremental) Invalidate(row int) {
	row = max(row, 0)
	if h.lex == nil {
		h.full = false
		h.styles = nil
		return
	}
	if row < len(h.styles) {
		// The forgotten slots are overwritten by later appends, never read.
		h.styles = h.styles[:row]
		h.ends = h.ends[:row]
	}
}

// LineSource is text read one line at a time, as a buffer provides it.
type LineSource interface {
	LineCount() int
	Line(i int) []rune
}

// Lines returns styles for the lines of src: every row up to and including
// through is styled (when there are that many), rows after it may be
// missing. The result must not be modified, and is valid until the next call.
func (h *Incremental) Lines(src LineSource, through int) [][]Style {
	if h.lex == nil {
		if !h.full {
			lines := make([][]rune, src.LineCount())
			for i := range lines {
				lines[i] = src.Line(i)
			}
			h.styles = ForPath(h.path, lines)
			h.full = true
		}
		return h.styles
	}
	through = min(through, src.LineCount()-1)
	for row := len(h.styles); row <= through; row++ {
		var st State
		if row > 0 {
			st = h.ends[row-1]
		}
		styles, end := h.lex(src.Line(row), st)
		h.styles = append(h.styles, styles)
		h.ends = append(h.ends, end)
	}
	return h.styles
}
