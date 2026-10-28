//go:build windows

package term

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fclaude/coigue/internal/guard"
)

// watchTermination restores the console when Windows asks the process to stop
// (the console window closing, or Ctrl+Break). Ctrl+C is not one of these:
// raw mode turns it into a key, which is how the editor cancels a running
// extension. After restoring, the process exits; Windows has no signal to
// re-raise the way Unix does.
func watchTermination(t *Terminal) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		t.Close()
		os.Exit(1)
	}()
}

// notifyResize calls f when the console window changes size. Windows has no
// SIGWINCH, so the size is polled. The first sample only records the size.
func notifyResize(f func()) {
	guard.Go(func() {
		width, height := -1, -1
		for {
			w, h, err := Size(os.Stdout)
			if err == nil && (w != width || h != height) {
				if width >= 0 {
					f()
				}
				width, height = w, h
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}
