//go:build windows

package term

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/windows"
	xterm "golang.org/x/term"
)

// Terminal is a raw-mode alternate-screen session on a Windows console.
// Close undoes it, and is safe to call more than once and from any goroutine.
// The renderer already writes the VT sequences Windows Terminal and the
// Windows 10 console understand, once virtual-terminal processing is enabled.
type Terminal struct {
	in      *os.File
	ownsIn  bool
	out     *os.File
	inState *xterm.State
	outMode uint32

	mu     sync.Mutex
	closed bool
}

// Enter switches the console to raw VT input and then to the alternate screen.
// Input comes from stdin when it is a console, else from CONIN$. If entering
// the alternate screen fails, the console is restored before the error is
// returned.
func Enter(out *os.File) (*Terminal, error) {
	in, owns := os.Stdin, false
	if !xterm.IsTerminal(int(in.Fd())) {
		tty, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
		if err != nil {
			return nil, err
		}
		in, owns = tty, true
	}
	inState, err := xterm.MakeRaw(int(in.Fd()))
	if err != nil {
		if owns {
			in.Close()
		}
		return nil, err
	}
	outMode, err := enableVirtualTerminal(out)
	if err != nil {
		xterm.Restore(int(in.Fd()), inState)
		if owns {
			in.Close()
		}
		return nil, err
	}
	t := &Terminal{in: in, ownsIn: owns, out: out, inState: inState, outMode: outMode}
	if _, err := io.WriteString(out, EnterAlternateScreen); err != nil {
		t.Close()
		return nil, err
	}
	watchTermination(t)
	return t, nil
}

// enableVirtualTerminal turns on processing of the sequences the renderer
// writes, and returns the previous output mode so Close can put it back.
func enableVirtualTerminal(out *os.File) (uint32, error) {
	var mode uint32
	handle := windows.Handle(out.Fd())
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return 0, err
	}
	if err := windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return 0, err
	}
	return mode, nil
}

// Close leaves the alternate screen, resets colours, shows the cursor and
// restores the original console modes. Errors are ignored: this runs on
// exit paths where there is nobody left to tell.
func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	io.WriteString(t.out, restoreSequence)
	_ = windows.SetConsoleMode(windows.Handle(t.out.Fd()), t.outMode)
	xterm.Restore(int(t.in.Fd()), t.inState)
	if t.ownsIn {
		t.in.Close()
	}
}

// Size returns the console's columns and rows.
func (t *Terminal) Size() (cols, rows int, err error) {
	return Size(t.out)
}

// Size asks out, then stdin, then CONOUT$ for the window size.
func Size(out *os.File) (cols, rows int, err error) {
	var firstErr error
	try := func(fd int) bool {
		width, height, err := xterm.GetSize(fd)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return false
		}
		cols, rows = width, height
		return true
	}
	if try(int(out.Fd())) || try(int(os.Stdin.Fd())) {
		return cols, rows, nil
	}
	if tty, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		if try(int(tty.Fd())) {
			return cols, rows, nil
		}
	}
	return 0, 0, firstErr
}

// Events starts reading the console and returns the decoded events.
func (t *Terminal) Events() <-chan Event {
	return readEvents(t.in, notifyResize)
}
