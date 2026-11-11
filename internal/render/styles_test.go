package render

import (
	"github.com/fclaude/coigue/internal/editor"
	"github.com/fclaude/coigue/internal/syntax"
)

// drawnStyles is what the last frame highlighted: every row the lexer has
// styled so far, with the semantic tokens laid over each as it is drawn. It
// is nil when the file has no lexer, and lexes nothing new.
func (v *View) drawnStyles(e *editor.Editor) [][]syntax.Style {
	h := &v.syntax
	if h.lexer == nil {
		return nil
	}
	lexed := h.lexer.Lines(e.Buffer, -1)
	out := make([][]syntax.Style, len(lexed))
	for i := range lexed {
		out[i] = append([]syntax.Style(nil), h.row(e, lexed, i)...)
	}
	return out
}
