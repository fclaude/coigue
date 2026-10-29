package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/key"
)

// tickUntil drives Tick until done reports true or a few seconds pass.
func tickUntil(t *testing.T, e *Editor, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (message %q)", what, e.Message)
		}
		e.Tick(22)
		time.Sleep(2 * time.Millisecond)
	}
}

func tickFor(e *Editor, d time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		e.Tick(22)
		time.Sleep(2 * time.Millisecond)
	}
}

const breakProgram = `
(define-command "break"
  (lambda ()
    (message "waiting")
    (on-channel (timer 0.03) (lambda (_) (message "time to stand up")))))
`

func TestATimerHandlerRunsAsACommandAfterTheDelay(t *testing.T) {
	e := extensionEditor(t, "text", breakProgram)
	command(e, "break")
	if e.Message != "waiting" {
		t.Fatalf("message %q", e.Message)
	}
	tickUntil(t, e, "the timer", func() bool { return e.Message == "time to stand up" })
}

func TestAHandlerWaitsUntilTheEditorIsIdle(t *testing.T) {
	e := extensionEditor(t, "text", `
(define inbox (make-channel 8))
(define-command "listen" (lambda () (on-channel inbox (lambda (v) (insert-text v)))))
(define-command "put" (lambda (w) (channel-send! inbox w)))
`)
	command(e, "listen")
	command(e, "put X")
	cases := []struct {
		name  string
		enter func()
		leave func()
	}{
		{"Insert mode", func() { send(e, "i") }, func() { press(e, key.Esc) }},
		{"Visual mode", func() { send(e, "v") }, func() { press(e, key.Esc) }},
		{"a half-typed operator", func() { send(e, "d") }, func() { press(e, key.Esc) }},
		{"a count", func() { send(e, "3") }, func() { press(e, key.Esc) }},
		{"the command line", func() { send(e, ":") }, func() { press(e, key.Esc) }},
		{"the guide", func() { press(e, key.F(1)) }, func() { press(e, key.Esc) }},
	}
	for _, c := range cases {
		c.enter()
		before := e.Buffer.Text()
		tickFor(e, 30*time.Millisecond)
		if got := e.Buffer.Text(); got != before {
			t.Fatalf("%s: a handler ran and changed %q to %q", c.name, before, got)
		}
		c.leave()
	}
	tickUntil(t, e, "the handler once idle", func() bool { return strings.Contains(e.Buffer.Text(), "X") })
	if strings.Count(e.Buffer.Text(), "X") != 1 {
		t.Fatalf("buffer %q; the value must be handled exactly once", e.Buffer.Text())
	}
	undo(e)
	if strings.Contains(e.Buffer.Text(), "X") {
		t.Fatalf("a handler's edit is not one undo step: %q", e.Buffer.Text())
	}
}

func TestAHandlerThatFailsSaysSoAndStaysRegistered(t *testing.T) {
	e := extensionEditor(t, "text", `
(define inbox (make-channel 8))
(define-command "listen" (lambda () (on-channel inbox (lambda (v) (if (equal? v "bad") (error "nope") (message v))))))
(define-command "put" (lambda (w) (channel-send! inbox w)))
`)
	command(e, "listen")
	command(e, "put bad")
	tickUntil(t, e, "the failure", func() bool { return strings.Contains(e.Message, "failed") })
	if !strings.HasPrefix(e.Message, "on-channel from :listen failed:") || !strings.Contains(e.Message, "nope") {
		t.Fatalf("message %q", e.Message)
	}
	command(e, "put good")
	tickUntil(t, e, "the next value", func() bool { return e.Message == "good" })
}

func TestAHandlerWithNothingToSayLeavesTheStatusLineAlone(t *testing.T) {
	e := extensionEditor(t, "text", `
(define inbox (make-channel 8))
(define-command "listen" (lambda () (on-channel inbox (lambda (v) (insert-text v)))))
(define-command "put" (lambda (w) (channel-send! inbox w)))
`)
	command(e, "listen")
	command(e, "put Z")
	e.Message = "keep me"
	tickUntil(t, e, "the handler", func() bool { return strings.Contains(e.Buffer.Text(), "Z") })
	if e.Message != "keep me" {
		t.Fatalf("message %q", e.Message)
	}
}

func TestReloadDropsHandlersAndTimers(t *testing.T) {
	e := extensionEditor(t, "text", breakProgram)
	command(e, "break")
	command(e, "reload")
	if !strings.HasPrefix(e.Message, "Reloaded") {
		t.Fatalf("reload: %q", e.Message)
	}
	tickFor(e, 150*time.Millisecond)
	if e.Message == "time to stand up" {
		t.Fatal("a handler registered before :reload ran after it")
	}
}

func TestAFailedCommandLeavesNoTimerBehind(t *testing.T) {
	e := extensionEditor(t, "text", `
(define-command "oops" (lambda () (on-channel (timer 0.02) (lambda (_) (message "ghost"))) (error "no")))
`)
	command(e, "oops")
	tickFor(e, 120*time.Millisecond)
	if strings.Contains(e.Message, "ghost") {
		t.Fatal("a command that failed still got its timer")
	}
}

func TestDocumentedExamplesRun(t *testing.T) {
	// The queue example of the documentation, with the registration in a command.
	e := extensionEditor(t, "one", `
(define notes (make-channel 16))
(define (append-line text)
  (let ((n (line-count)))
    (insert-at n (+ (string-length (buffer-text n)) 1) (string-append "\n" text))))
(define-command "listen" (lambda () (on-channel notes append-line)))
(define-command "note" (lambda (word) (channel-send! notes word)))
(define (line-widths)
  (let ((lines (map (lambda (i) (buffer-text (+ i 1))) (iota (line-count)))))
    (pmap (lambda (l) (string-length l)) lines)))
(define-command "widths" (lambda () (message (number->string (apply + (line-widths))))))
`)
	command(e, "listen")
	command(e, "note two")
	tickUntil(t, e, "the note", func() bool { return e.Buffer.Text() == "one\ntwo" })
	command(e, "widths")
	if e.Message != "6" {
		t.Fatalf("widths: %q", e.Message)
	}
}
