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
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/render"
	"github.com/fclaude/coigue/internal/term"
	"github.com/fclaude/coigue/internal/version"
)

const usage = "Usage: coigue [FILE]\n\nWithout a file, welcome opens in the current directory.\nCtrl-O / - / :explore browse; Ctrl-P / :open find files; F1 / :help guide; :q quit.\n\n  --version   Show source version, package build, and commit\n  --help, -h  Show this help\n"

// pollInterval is how long the loop waits for input before redrawing and
// polling background work.
const pollInterval = 50 * time.Millisecond

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
	return loop(ed, t)
}

func loop(ed *editor.Editor, t *term.Terminal) error {
	var view render.View
	view.SetNoColor(os.Getenv("NO_COLOR") != "")
	events := t.Events()
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()
	for {
		ed.Tick(view.PageRows())
		if err := view.Draw(ed, t, os.Stdout); err != nil {
			return err
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(pollInterval)
		select {
		case <-timer.C:
		case event, ok := <-events:
			if !ok {
				return term.ErrInputClosed
			}
			if event.Type != term.KeyEvent || event.Kind == term.Release {
				continue
			}
			// Resize notifications can arrive after a queued key. Page by
			// the current terminal height rather than the last drawn frame.
			_, rows, err := t.Size()
			if err != nil {
				return err
			}
			if ed.Key(event.Key, max(rows-2, 1)) {
				return nil
			}
		}
	}
}
