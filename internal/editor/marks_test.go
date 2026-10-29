package editor

import (
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/key"
)

const markInit = `
(define m #f)
(define inbox (make-channel 4))
(define (put text) (insert-at-mark m text))
(define-command "arm"
  (lambda ()
    (set! m (mark-at (line-count) 1))
    (on-channel inbox put)))
(define-command "fire" (lambda (w) (channel-send! inbox w)))
(define-command "where" (lambda () (message (string-append (number->string (mark-line m)) ":" (number->string (mark-column m))))))
`

func TestAMarkFollowsTheTextWhileThePersonTypesAboveIt(t *testing.T) {
	e := extensionEditor(t, "one\ntwo\nthree", markInit)
	command(e, "arm")
	// Type a new first line, then leave Insert mode.
	send(e, "inew")
	press(e, key.Enter)
	press(e, key.Esc)
	if e.Buffer.Text() != "new\none\ntwo\nthree" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	command(e, "where")
	if e.Message != "4:1" {
		t.Fatalf("the mark is at %q", e.Message)
	}
	command(e, "fire hello")
	tickUntil(t, e, "the handler", func() bool { return strings.Contains(e.Buffer.Text(), "hello") })
	if e.Buffer.Text() != "new\none\ntwo\nhellothree" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	// The handler's insert is one undo step of its own.
	undo(e)
	if e.Buffer.Text() != "new\none\ntwo\nthree" {
		t.Fatalf("after undo: %q", e.Buffer.Text())
	}
	// And the mark is still where the text it was on is.
	command(e, "where")
	if e.Message != "4:1" {
		t.Fatalf("after undo the mark is at %q", e.Message)
	}
}

func TestAMarkKeepsUpThroughUndoOfTheTyping(t *testing.T) {
	e := extensionEditor(t, "one\ntwo", markInit)
	command(e, "arm")
	send(e, "iabc")
	press(e, key.Enter)
	press(e, key.Esc)
	command(e, "where")
	if e.Message != "3:1" {
		t.Fatalf("after typing: %q", e.Message)
	}
	undo(e)
	command(e, "where")
	if e.Message != "2:1" {
		t.Fatalf("after undo: %q", e.Message)
	}
}
