//go:build unix

// Package term owns the terminal: raw mode and the alternate screen with
// restoration on every exit path, size queries, and the stream of decoded
// input events. It deliberately stays below any widget or screen model: the
// renderer writes bytes, this package only guarantees the terminal is left as
// it was found.
package term

import (
	"errors"
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"

	"github.com/fclaude/coigue/internal/guard"
)

// readSize is crossterm's TTY buffer size. A read that fills it signals that
// more input is probably pending, which keeps a trailing ESC unresolved.
const readSize = 1024

// Terminal is a raw-mode alternate-screen session. Close undoes it, and is
// safe to call more than once and from any goroutine.
type Terminal struct {
	in     *os.File
	ownsIn bool
	out    *os.File
	state  *xterm.State

	mu     sync.Mutex
	closed bool
}

// Enter switches the terminal to raw mode and then to the alternate screen.
// Input comes from stdin when it is a terminal, else from /dev/tty. If entering
// the alternate screen fails (for example stdout is a full disk) the terminal
// is restored before the error is returned.
func Enter(out *os.File) (*Terminal, error) {
	in, owns := os.Stdin, false
	if !xterm.IsTerminal(int(in.Fd())) {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		in, owns = tty, true
	}
	state, err := xterm.MakeRaw(int(in.Fd()))
	if err != nil {
		if owns {
			in.Close()
		}
		return nil, err
	}
	t := &Terminal{in: in, ownsIn: owns, out: out, state: state}
	if _, err := io.WriteString(out, EnterAlternateScreen); err != nil {
		t.Close()
		return nil, err
	}
	watchTermination(t)
	return t, nil
}

// Close leaves the alternate screen, resets colours, shows the cursor and
// restores the original terminal modes. Errors are ignored: this runs on
// exit paths where there is nobody left to tell.
func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	io.WriteString(t.out, restoreSequence)
	xterm.Restore(int(t.in.Fd()), t.state)
	if t.ownsIn {
		t.in.Close()
	}
}

// Size returns the terminal's columns and rows. The terminal being drawn on
// (stdout) is asked first; stdin and the controlling terminal are fallbacks.
func (t *Terminal) Size() (cols, rows int, err error) {
	return Size(t.out)
}

// Size asks out, then stdin, then /dev/tty for the window size.
func Size(out *os.File) (cols, rows int, err error) {
	var firstErr error
	try := func(fd int) bool {
		ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return false
		}
		cols, rows = int(ws.Col), int(ws.Row)
		return true
	}
	if try(int(out.Fd())) || try(int(os.Stdin.Fd())) {
		return cols, rows, nil
	}
	if tty, err := os.Open("/dev/tty"); err == nil {
		defer tty.Close()
		if try(int(tty.Fd())) {
			return cols, rows, nil
		}
	}
	return 0, 0, firstErr
}

// ErrInputClosed is delivered when the terminal can no longer be read.
var ErrInputClosed = errors.New("terminal input closed")

// Events starts reading the terminal and returns the decoded events. A
// ResizeEvent is sent when the window changes size so the caller redraws at
// once, and a nil event with the read error closes the stream.
func (t *Terminal) Events() <-chan Event {
	events := make(chan Event, 64)
	guard.Go(func() {
		defer close(events)
		parser := Parser{Raw: true}
		buf := make([]byte, readSize)
		for {
			n, err := t.in.Read(buf)
			for _, event := range parser.Feed(buf[:n], n == readSize) {
				events <- event
			}
			if err != nil {
				return
			}
		}
	})
	notifyResize(func() {
		select {
		case events <- Event{Type: ResizeEvent}:
		default:
		}
	})
	return events
}
