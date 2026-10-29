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

const wordInit = `
(define m #f)
(define target "")
(define inbox (make-channel 4))
(define (swap text) (replace-at-mark m target text))
(define-command "rename"
  (lambda ()
    (let ((r (word-range)))
      (set! target (word-at-cursor))
      (set! m (mark-at (car r) (cadr r)))
      (on-channel (prompt "Replace with" target) swap))))
(define-command "arm"
  (lambda ()
    (let ((r (word-range)))
      (set! target (word-at-cursor))
      (set! m (mark-at (car r) (cadr r)))
      (on-channel inbox swap))))
(define-command "fire" (lambda (w) (channel-send! inbox w)))
`

func TestAPromptCanReplaceTheWordUnderTheCursor(t *testing.T) {
	e := extensionEditor(t, "one two three", wordInit)
	send(e, "w") // onto "two"
	command(e, "rename")
	if e.Window == nil || e.Window.Kind != WindowPrompt || e.Window.Query != "two" {
		t.Fatalf("window %+v", e.Window)
	}
	ctrl(e, 'u')
	send(e, "2")
	press(e, key.Enter)
	tickUntil(t, e, "the replacement", func() bool { return e.Buffer.Text() != "one two three" })
	if e.Buffer.Text() != "one 2 three" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	undo(e)
	if e.Buffer.Text() != "one two three" {
		t.Fatalf("one undo step should restore it: %q", e.Buffer.Text())
	}
}

func TestTheWordIsReplacedInTheRightPlaceAfterTheTextAboveChanged(t *testing.T) {
	e := extensionEditor(t, "one two three", wordInit)
	send(e, "w")
	command(e, "arm")
	// Type a new first line: at the start of the line, not at the mark.
	send(e, "i")
	press(e, key.Home)
	send(e, "above")
	press(e, key.Enter)
	press(e, key.Esc)
	if e.Buffer.Text() != "above\none two three" {
		t.Fatalf("setup: %q", e.Buffer.Text())
	}
	command(e, "fire 2")
	tickUntil(t, e, "the replacement", func() bool { return strings.Contains(e.Buffer.Text(), "2") })
	if e.Buffer.Text() != "above\none 2 three" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
}

func TestTypingAtTheStartOfTheWordIsRefusedNotMisreplaced(t *testing.T) {
	// Text typed exactly at a mark goes after it, so the mark now sits before
	// the typed text and not on the word. The check catches that: nothing is
	// replaced, and the message shows what is there.
	e := extensionEditor(t, "one two three", wordInit)
	send(e, "w")
	command(e, "arm")
	send(e, "iabove")
	press(e, key.Esc)
	if e.Buffer.Text() != "one abovetwo three" {
		t.Fatalf("setup: %q", e.Buffer.Text())
	}
	command(e, "fire 2")
	tickUntil(t, e, "the refusal", func() bool { return strings.Contains(e.Message, "the text at the mark is") })
	if e.Buffer.Text() != "one abovetwo three" {
		t.Fatalf("the buffer changed: %q", e.Buffer.Text())
	}
}

func TestAWordEditedAwayIsLeftAloneAndSaysSo(t *testing.T) {
	e := extensionEditor(t, "one two three", wordInit)
	send(e, "w")
	command(e, "arm")
	// Delete the first character of "two" (the cursor is on it).
	send(e, "x")
	if e.Buffer.Text() != "one wo three" {
		t.Fatalf("setup: %q", e.Buffer.Text())
	}
	command(e, "fire 2")
	tickUntil(t, e, "the failure", func() bool { return strings.Contains(e.Message, "the text at the mark is") })
	if e.Buffer.Text() != "one wo three" {
		t.Fatalf("the buffer changed: %q", e.Buffer.Text())
	}
	if !strings.Contains(e.Message, `not "two"`) {
		t.Fatalf("message %q", e.Message)
	}
}

func TestTheDocumentedReplaceExampleRuns(t *testing.T) {
	e := extensionEditor(t, "one two three", `
(define-command "shout"
  (lambda ()
    (let ((range (word-range)) (word (word-at-cursor)))
      (when (not (equal? word ""))
        (let ((here (mark-at (car range) (cadr range))))
          (on-channel (prompt "Replace with" word)
                      (lambda (new) (replace-at-mark here word new))))))))
`)
	send(e, "w")
	command(e, "shout")
	ctrl(e, 'u')
	send(e, "TWO")
	press(e, key.Enter)
	tickUntil(t, e, "the replacement", func() bool { return e.Buffer.Text() != "one two three" })
	if e.Buffer.Text() != "one TWO three" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	// Off a word, it says nothing and opens nothing.
	e2 := extensionEditor(t, "a  b", `
(define-command "shout"
  (lambda ()
    (let ((range (word-range)) (word (word-at-cursor)))
      (when (not (equal? word ""))
        (let ((here (mark-at (car range) (cadr range))))
          (on-channel (prompt "Replace with" word)
                      (lambda (new) (replace-at-mark here word new))))))))
`)
	send(e2, "l")
	send(e2, "l")
	command(e2, "shout")
	if e2.Window != nil {
		t.Fatalf("a window opened off a word: %+v", e2.Window)
	}
}
