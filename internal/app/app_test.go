package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/editor"
	"github.com/fclaude/coigue/internal/file"
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

// runLoop feeds events to the real loop on an editor opened on text and
// returns the text afterwards.
func runLoop(t *testing.T, text string, events ...term.Event) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f, loaded, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ed := editor.New(t.Context(), f, loaded)
	defer ed.Close()
	ch := make(chan term.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	if err := loop(ed, fixedScreen{80, 24}, io.Discard, &input{events: ch}); !errors.Is(err, term.ErrInputClosed) {
		t.Fatalf("loop: %v", err)
	}
	return ed.Buffer.Text()
}

func TestLoopInsertsABracketedPasteAsText(t *testing.T) {
	got := runLoop(t, "end\n", keyEvent('i', 0), term.Event{Type: term.PasteEvent, Paste: "a\rb:q\r"})
	if got != "a\nb:q\nend\n" {
		t.Fatalf("text = %q", got)
	}
}

func TestLoopReplaysAPasteAsKeysWhereTextCannotGo(t *testing.T) {
	// After d, a pasted "d" completes dd, as it would without bracketed paste.
	got := runLoop(t, "one\ntwo\n", keyEvent('d', 0), term.Event{Type: term.PasteEvent, Paste: "d"})
	if got != "two\n" {
		t.Fatalf("text = %q", got)
	}
}

func TestLoopDrawsQueuedInputInOneFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("end\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, loaded, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ed := editor.New(t.Context(), f, loaded)
	defer ed.Close()
	typed := strings.Repeat("queued keys arrive faster than frames ", 6)
	events := []term.Event{keyEvent('i', 0)}
	for _, c := range typed {
		events = append(events, keyEvent(c, 0))
	}
	ch := make(chan term.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	var out strings.Builder
	if err := loop(ed, fixedScreen{80, 24}, &out, &input{events: ch}); !errors.Is(err, term.ErrInputClosed) {
		t.Fatalf("loop: %v", err)
	}
	frames := strings.Count(out.String(), term.BeginSynchronizedUpdate)
	// The first frame, one for each of the last burstEvents keys (fewer are
	// waiting by then), and on a slow machine one per frameBudget.
	if frames > burstEvents+4 {
		t.Fatalf("%d frames for %d queued keys", frames, len(events))
	}
	if !strings.Contains(out.String(), strings.TrimSpace(typed)[len(typed)-60:]) {
		t.Fatal("the last frame does not show the queued keys")
	}
}

func TestLoopDrawsEveryFrameForAFewKeysInOneRead(t *testing.T) {
	// ":w" arriving in one read still shows the prompt before it runs.
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, loaded, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ed := editor.New(t.Context(), f, loaded)
	defer ed.Close()
	ch := make(chan term.Event, 2)
	ch <- keyEvent(':', 0)
	ch <- keyEvent('w', 0)
	close(ch)
	var out strings.Builder
	if err := loop(ed, fixedScreen{80, 24}, &out, &input{events: ch}); !errors.Is(err, term.ErrInputClosed) {
		t.Fatalf("loop: %v", err)
	}
	if !strings.Contains(out.String(), ":w") {
		t.Fatal("the prompt was never drawn")
	}
}
