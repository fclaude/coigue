// Package term owns the terminal: raw mode and the alternate screen with
// restoration on every exit path, size queries, and the stream of decoded
// input events. It deliberately stays below any widget or screen model: the
// renderer writes bytes, this package only guarantees the terminal is left as
// it was found.
package term

import (
	"errors"
	"os"

	"github.com/fclaude/coigue/internal/guard"
)

// ErrInputClosed is delivered when the terminal can no longer be read.
var ErrInputClosed = errors.New("terminal input closed")

// readSize is crossterm's TTY buffer size. A read that fills it signals that
// more input is probably pending, which keeps a trailing ESC unresolved.
const readSize = 1024

// readEvents reads in and decodes it with the crossterm parser. notify is
// asked to call its function when the window changes size; that send never
// blocks, so a burst of resizes cannot stall the reader.
func readEvents(in *os.File, notify func(func())) <-chan Event {
	events := make(chan Event, 64)
	guard.Go(func() {
		defer close(events)
		parser := Parser{Raw: true}
		buf := make([]byte, readSize)
		for {
			n, err := in.Read(buf)
			for _, event := range parser.Feed(buf[:n], n == readSize) {
				events <- event
			}
			if err != nil {
				return
			}
		}
	})
	notify(func() {
		select {
		case events <- Event{Type: ResizeEvent}:
		default:
		}
	})
	return events
}
