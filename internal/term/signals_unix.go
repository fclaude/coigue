//go:build unix

package term

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/fclaude/coigue/internal/guard"
)

// watchTermination restores the terminal when the process is asked to stop
// (SIGHUP when the terminal closes, SIGTERM, SIGINT sent from outside; the
// keyboard cannot raise SIGINT in raw mode) and then re-delivers the signal
// with its default action, so the exit status is the usual one.
func watchTermination(t *Terminal) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-ch
		t.Close()
		signal.Reset(sig)
		syscall.Kill(syscall.Getpid(), sig.(syscall.Signal))
		select {} // the signal's default action ends the process
	}()
}

// notifyResize calls f from a goroutine whenever the window size changes.
func notifyResize(f func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	guard.Go(func() {
		for range ch {
			f()
		}
	})
}
