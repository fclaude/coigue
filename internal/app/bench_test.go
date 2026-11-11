package app

// End-to-end benchmarks: what the person at the keyboard waits for. Each one
// opens a file, turns the bytes a terminal would send into events with the
// real parser, and runs the real event loop (Tick, Draw, Key) until the input
// is used up. Frames are drawn at 120x40 and discarded.
//
// Run them with scripts/bench.sh, or for example:
//
//	go test ./internal/app -run '^$' -bench Session -benchtime 3x

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/benchdata"
	"github.com/fclaude/coigue/internal/editor"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/render"
	"github.com/fclaude/coigue/internal/term"
)

type fixedScreen struct{ cols, rows int }

func (s fixedScreen) Size() (int, int, error) { return s.cols, s.rows, nil }

// bracketedPaste reports whether the editor asks the terminal for bracketed
// paste when it starts. When it does not, a terminal sends a paste as if it
// were typed, one key per character.
func bracketedPaste() bool {
	return strings.Contains(term.EnterSequence, term.EnableBracketedPaste)
}

// terminalPaste is what a terminal sends when text is pasted into it: line
// breaks as carriage returns, wrapped in the paste brackets when the
// application asked for them.
func terminalPaste(text string, bracketed bool) string {
	text = strings.ReplaceAll(text, "\n", "\r")
	if bracketed {
		return "\x1b[200~" + text + "\x1b[201~"
	}
	return text
}

// writeFixture writes text to fixture.go in a fresh directory.
func writeFixture(b *testing.B, text string) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "fixture.go")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		b.Fatal(err)
	}
	return path
}

// openFixture opens an editor on path with the cursor on the middle line,
// where edits are neither cheap nor worst-case.
func openFixture(b *testing.B, path string) *editor.Editor {
	b.Helper()
	f, loaded, err := file.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	ed := editor.New(b.Context(), f, loaded)
	ed.Buffer.Row = ed.Buffer.LineCount() / 2
	return ed
}

// session runs the event loop over input, read in 1024-byte pieces as the
// terminal reader does, and returns once the input is exhausted.
func session(b *testing.B, ed *editor.Editor, keys string) {
	b.Helper()
	parser := term.Parser{Raw: true}
	var events []term.Event
	data := []byte(keys)
	for len(data) > 0 {
		n := min(len(data), 1024)
		events = append(events, parser.Feed(data[:n], n == 1024)...)
		data = data[n:]
	}
	ch := make(chan term.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	if err := loop(ed, fixedScreen{120, 40}, io.Discard, &input{events: ch}); !errors.Is(err, term.ErrInputClosed) {
		b.Fatalf("loop ended with %v", err)
	}
}

// runScenario opens a fresh editor for every iteration, so no run sees
// another's edits, and times only the session.
func runScenario(b *testing.B, text, keys string) {
	path := writeFixture(b, text)
	for range b.N {
		b.StopTimer()
		ed := openFixture(b, path)
		b.StartTimer()
		session(b, ed, keys)
		b.StopTimer()
		ed.Close()
		b.StartTimer()
	}
}

const typed = "fmt.Println(\"typed into the middle of a long file\")"

// BenchmarkSession is the headline suite. Sub-benchmarks:
//
//   - Type: Insert mode, 50 characters typed one by one, then Esc.
//   - NormalX: 20 presses of x in Normal mode; each one is its own undo step.
//   - PasteKeys: 1 KB pasted into a terminal without bracketed paste (or
//     before the editor asked for it), so it arrives as 1024 key presses.
//   - Paste: 1 KB and 64 KB pasted with bracketed paste; skipped while the
//     editor does not enable it.
//   - Open: open the file and draw the first frame.
func BenchmarkSession(b *testing.B) {
	for _, lines := range benchdata.Sizes {
		text := benchdata.GoSource(lines)
		size := fmt.Sprintf("lines=%d", lines)
		b.Run("Type/"+size, func(b *testing.B) {
			runScenario(b, text, "i"+typed+"\x1b")
			b.ReportMetric(float64(b.Elapsed().Milliseconds())/float64(b.N)/float64(len(typed)), "ms/key")
		})
		b.Run("NormalX/"+size, func(b *testing.B) {
			runScenario(b, text, strings.Repeat("x", 20))
			b.ReportMetric(float64(b.Elapsed().Milliseconds())/float64(b.N)/20, "ms/key")
		})
		if lines <= 20_000 {
			// At 100,000 lines the unbracketed baseline takes minutes.
			b.Run("PasteKeys/1KB/"+size, func(b *testing.B) {
				paste := benchdata.Prose(1024)
				runScenario(b, text, "i"+terminalPaste(paste, false)+"\x1b")
			})
		}
		for _, n := range []int{1 << 10, 64 << 10} {
			b.Run(fmt.Sprintf("Paste/%dKB/%s", n>>10, size), func(b *testing.B) {
				if !bracketedPaste() {
					b.Skip("the editor does not enable bracketed paste")
				}
				paste := benchdata.Prose(n)
				runScenario(b, text, "i"+terminalPaste(paste, true)+"\x1b")
			})
		}
		b.Run("Open/"+size, func(b *testing.B) {
			path := writeFixture(b, text)
			for range b.N {
				ed := openFixture(b, path)
				var view render.View
				if err := view.Draw(ed, fixedScreen{120, 40}, io.Discard); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				ed.Close()
				b.StartTimer()
			}
		})
	}
}

// BenchmarkKeystroke is typing the way a person does: one key, then the
// background poll and the frame that shows it, with nothing else waiting, so
// no batching applies. Each operation types 100 characters into the middle
// line in Insert mode; ms/key is the latency of one keystroke.
func BenchmarkKeystroke(b *testing.B) {
	const keys = 100
	for _, lines := range benchdata.Sizes {
		text := benchdata.GoSource(lines)
		b.Run(fmt.Sprintf("lines=%d", lines), func(b *testing.B) {
			path := writeFixture(b, text)
			screen := fixedScreen{120, 40}
			for range b.N {
				b.StopTimer()
				ed := openFixture(b, path)
				var view render.View
				ed.Key(key.Char('i'), 38)
				if err := view.Draw(ed, screen, io.Discard); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				for i := range keys {
					ed.Key(key.Char(rune('a'+i%26)), 38)
					ed.Tick(view.PageRows())
					if err := view.Draw(ed, screen, io.Discard); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				ed.Close()
				b.StartTimer()
			}
			b.ReportMetric(float64(b.Elapsed().Microseconds())/1000/float64(b.N)/keys, "ms/key")
		})
	}
}
