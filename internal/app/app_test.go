package app

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/term"
)

func keyEvent(c rune, mods key.Mods) term.Event {
	return term.Event{Type: term.KeyEvent, Key: key.Key{Code: key.Code(c), Mods: mods}}
}

func TestWatcherInterruptsOnCtrlCAndKeepsTheOtherEventsInOrder(t *testing.T) {
	events := make(chan term.Event, 16)
	in := &input{events: events}
	var interrupts atomic.Int32
	interrupted := make(chan struct{}, 4)
	stop := in.watchForInterrupt(func() {
		interrupts.Add(1)
		interrupted <- struct{}{}
	})
	events <- keyEvent('a', 0)
	events <- keyEvent('c', key.Ctrl)
	events <- term.Event{Type: term.ResizeEvent}
	events <- keyEvent('c', 0) // plain c is not an interrupt
	events <- keyEvent('b', key.Alt)
	select {
	case <-interrupted:
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-C did not interrupt")
	}
	// Wait for the watcher to read everything that was queued, then stop it.
	deadline := time.Now().Add(5 * time.Second)
	for len(events) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop()
	if interrupts.Load() != 1 {
		t.Fatalf("interrupted %d times", interrupts.Load())
	}
	if len(in.pending) != 4 || in.pending[0].Key.Code != 'a' || in.pending[1].Type != term.ResizeEvent ||
		in.pending[2].Key.Code != 'c' || in.pending[2].Key.Mods != 0 || in.pending[3].Key.Mods != key.Alt {
		t.Fatalf("pending = %+v", in.pending)
	}
	// After stop the watcher no longer reads: the loop gets the next event.
	events <- keyEvent('z', 0)
	time.Sleep(20 * time.Millisecond)
	if len(events) != 1 {
		t.Fatal("the watcher kept reading after stop")
	}
}

func TestWatcherStopsWhenTheInputCloses(t *testing.T) {
	events := make(chan term.Event)
	in := &input{events: events}
	stop := in.watchForInterrupt(func() {})
	close(events)
	stop() // returns although the watcher already exited
	if _, ok := <-in.events; ok {
		t.Fatal("the stream is not closed for the loop")
	}
}
