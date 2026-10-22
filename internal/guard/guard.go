// Package guard makes a panic in a background goroutine leave the terminal
// usable. Go ends the whole process when any goroutine panics, without running
// the deferred functions of the main goroutine, so the terminal restore that
// protects the main goroutine would be skipped. Goroutines started with Go run
// the registered cleanups before the panic continues.
//
// This is process-wide infrastructure, like a signal handler, not editor state:
// the cleanups list is only appended to during start-up.
package guard

import "sync"

var (
	mu       sync.Mutex
	cleanups []func()
)

// OnPanic registers fn to run when a goroutine started with Go panics.
func OnPanic(fn func()) {
	mu.Lock()
	defer mu.Unlock()
	cleanups = append(cleanups, fn)
}

func runCleanups() {
	mu.Lock()
	fns := append([]func(){}, cleanups...)
	mu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}

// Go runs f in a new goroutine. If f panics, the cleanups registered with
// OnPanic run (most recent first) and the panic then proceeds as usual.
func Go(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				runCleanups()
				panic(r)
			}
		}()
		f()
	}()
}
