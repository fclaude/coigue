// Package app wires the editor to a real terminal: argument handling, the
// event loop and the exit paths. Everything with behaviour lives in the
// packages it composes.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fclaude/coigue/internal/editor"
	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/render"
	"github.com/fclaude/coigue/internal/term"
	"github.com/fclaude/coigue/internal/version"
)

const usage = "Usage: coigue [FILE]\n\nWithout a file, welcome opens in the current directory.\nCtrl-O / - / :explore browse; Ctrl-P / :open find files; F1 / :help guide; :q quit.\n\n  --version   Show source version, package build, and commit\n  --help, -h  Show this help\n"

// pollInterval is how long the loop waits for input before redrawing and
// polling background work.
const pollInterval = 50 * time.Millisecond

// While at least burstEvents events are already waiting (a paste in a
// terminal without bracketed paste sends one per character) the loop handles
// them without drawing, but redraws at least every frameBudget. A burst then
// costs a frame per frameBudget instead of one per key. Fewer waiting events,
// such as a few keys arriving in one read, still get a frame each, so every
// prompt and message they pass through is shown; no one types burstEvents
// keys within one frame.
const (
	burstEvents = 16
	frameBudget = 33 * time.Millisecond
)

// Main runs the editor with the process arguments (without the program name)
// and returns the exit status. --help and --version are answered without
// touching the terminal.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		io.WriteString(stdout, usage)
		return 0
	}
	if len(args) > 0 && args[0] == "--version" {
		fmt.Fprintln(stdout, version.Line())
		return 0
	}
	if err := run(args); err != nil {
		fmt.Fprintf(stderr, "coigue: %s\n", fsx.Describe(err))
		return 1
	}
	return 0
}

func run(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: coigue [file]")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ed *editor.Editor
	if len(args) == 1 {
		f, text, err := file.Open(args[0])
		if err != nil {
			return err
		}
		ed = editor.New(ctx, f, text)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		ed = editor.NewWelcome(ctx, cwd)
	}
	// The editor is closed after the terminal is restored, so that waiting for
	// language servers to shut down never leaves the screen in raw mode.
	defer ed.Close()
	t, err := term.Enter(os.Stdout)
	if err != nil {
		return err
	}
	defer t.Close()
	// A panic in a background goroutine ends the process without running the
	// deferred calls above; restore the terminal first.
	guard.OnPanic(t.Close)
	ed.LSP.Enable(ctx)
	in := &input{events: t.Events()}
	ed.SetInterrupter(in.watchForInterrupt)
	// Scheme is loaded from the user's own configuration file only, never
	// from the directory of the file being edited.
	if path, ok := ext.InitPath(os.Getenv, os.UserHomeDir); ok {
		ed.LoadInit(path)
		ed.RunStart()
	}
	return loop(ed, t, os.Stdout, in)
}

// input is the terminal's event stream. While an extension runs the editor is
// busy, so a watcher reads the keyboard for Ctrl-C; the other events it reads
// are kept in pending, in order, for the loop to handle afterwards.
type input struct {
	events  <-chan term.Event
	pending []term.Event
}

func isInterrupt(event term.Event) bool {
	return event.Type == term.KeyEvent && event.Kind != term.Release &&
		event.Key.Mods == key.Ctrl && event.Key.Code == 'c'
}

// watchForInterrupt calls interrupt when Ctrl-C arrives before stop is called.
// stop returns after the watcher goroutine has exited, so the loop and the
// watcher never read the stream at the same time.
func (in *input) watchForInterrupt(interrupt func()) (stop func()) {
	done, exited := make(chan struct{}), make(chan struct{})
	guard.Go(func() {
		defer close(exited)
		for {
			select {
			case <-done:
				return
			case event, ok := <-in.events:
				switch {
				case !ok:
					// The loop sees the closed stream on its next read.
					return
				case isInterrupt(event):
					interrupt()
				default:
					in.pending = append(in.pending, event)
				}
			}
		}
	})
	return func() {
		close(done)
		<-exited
	}
}

// loop draws, waits for input and hands it to the editor until the editor
// quits or the input closes. While a burst of input is waiting it skips the
// frame (and the background polling of Tick), up to frameBudget. screen gives the size to draw at and out receives
// the frames; the real terminal is both, benchmarks use stand-ins.
func loop(ed *editor.Editor, screen render.Screen, out io.Writer, in *input) error {
	var view render.View
	view.SetNoColor(os.Getenv("NO_COLOR") != "")
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	var drawn time.Time
	for {
		burst := len(in.pending)+len(in.events) >= burstEvents
		if !burst || time.Since(drawn) >= frameBudget {
			ed.Tick(view.PageRows())
			if err := view.Draw(ed, screen, out); err != nil {
				return err
			}
			drawn = time.Now()
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(pollInterval)
		var event term.Event
		if len(in.pending) > 0 {
			event, in.pending = in.pending[0], in.pending[1:]
		} else {
			select {
			case <-timer.C:
				continue
			case next, ok := <-in.events:
				if !ok {
					return term.ErrInputClosed
				}
				event = next
			}
		}
		if event.Type == term.PasteEvent {
			if ed.AcceptsPaste() {
				ed.Paste(event.Paste)
			} else {
				// Where text cannot be inserted, a paste is the keys it is
				// made of, as without bracketed paste; they go first.
				parser := term.Parser{Raw: true}
				in.pending = append(parser.Feed([]byte(event.Paste), false), in.pending...)
			}
			continue
		}
		if event.Type != term.KeyEvent || event.Kind == term.Release {
			continue
		}
		// Resize notifications can arrive after a queued key. Page by
		// the current terminal height rather than the last drawn frame.
		_, rows, err := screen.Size()
		if err != nil {
			return err
		}
		if ed.Key(event.Key, max(rows-2, 1)) {
			return nil
		}
	}
}
