package syntax

import (
	"fmt"
	"testing"

	"github.com/fclaude/coigue/internal/buffer"
)

// Pieces of each language that open or close multi-line state, so random
// edits keep moving comment and string boundaries around.
var incrementalPieces = map[string][]string{
	"x.go":  {"/*", "*/", "`", "\"", "'", "//", "\n", "func", " x ", "0x1f", "\\", "if"},
	"x.rs":  {"/*", "*/", "r#\"", "\"#", "\"", "\\", "\n", "'a", "fn", "b\"", "//", "1e+3"},
	"x.scm": {"#|", "|#", "\"\"\"", "\"", "\\", ";", "\n", "(define", " x", "#t", "1.5"},
	"x.md":  {"```go", "```", "\n", "# ", "> ", "- ", "*", "`", "<!--", "-->", "text", "===", "    "},
}

func TestIncrementalHighlightingMatchesRelexingEverything(t *testing.T) {
	for path, pieces := range incrementalPieces {
		for seed := range 60 {
			r := uint64(seed + 1)
			next := func(n int) int {
				r = r*6364136223846793005 + 1442695040888963407
				return int(r>>33) % n
			}
			var text string
			for range 40 {
				text += pieces[next(len(pieces))]
			}
			b := buffer.New(text)
			h, ok := NewIncremental(path)
			if !ok {
				t.Fatalf("%s: no incremental highlighter", path)
			}
			seen := b.Revision()
			for step := range 60 {
				b.Row = next(b.LineCount())
				b.Col = next(len(b.Lines()[b.Row]) + 1)
				switch next(3) {
				case 0:
					b.InsertText([]rune(pieces[next(len(pieces))]))
				case 1:
					b.Backspace()
				default:
					b.Undo()
				}
				row, known := b.FirstChangedRow(seen)
				if !known {
					row = 0
				}
				h.Invalidate(row)
				seen = b.Revision()
				lines := b.Lines()
				through := next(len(lines) + 2)
				got := h.Lines(b, through)
				want := ForPath(path, lines)
				for i := 0; i <= min(through, len(lines)-1); i++ {
					if fmt.Sprint(got[i]) != fmt.Sprint(want[i]) {
						t.Fatalf("%s seed %d step %d: row %d\n  incremental %v\n  full        %v\n  line %q",
							path, seed, step, i, got[i], want[i], string(lines[i]))
					}
				}
			}
		}
	}
}

func TestIncrementalHasNoHighlighterWhereForPathHasNone(t *testing.T) {
	for _, path := range []string{"notes.txt", "Makefile", "x.py"} {
		if _, ok := NewIncremental(path); ok {
			t.Errorf("%s: got a highlighter", path)
		}
	}
	for _, path := range []string{"A.GO", "lib.rs", "init.scm", "README.md", "x.markdown"} {
		if _, ok := NewIncremental(path); !ok {
			t.Errorf("%s: no highlighter", path)
		}
	}
}
