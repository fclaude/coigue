package ext

import (
	"context"
	"strings"
	"testing"
)

const windowProgram = `
(define-command "pick"
  (lambda ()
    (on-channel (show-list "Pick" (list "alpha" (cons "bee (label)" 'bee) "gamma"))
                (lambda (v) (insert-text (if (symbol? v) (symbol->string v) v))))))
(define-command "ask"
  (lambda ()
    (on-channel (prompt "Name" "start") (lambda (text) (insert-text (string-append "got " text))))))
(define-command "read"
  (lambda () (on-channel (show-text "Notes" "one\ntwo") (lambda (_) (insert-text "unreachable")))))
(define-command "bad-list" (lambda () (show-list "T" (list 1 2))))
(define-command "bad-text" (lambda () (show-text "T" (list "ok" 5))))
(define-command "failing" (lambda () (show-list "T" (list "a")) (error "no")))
(define-command "flood" (lambda ()
  (let loop ((i 0)) (if (< i 20) (begin (show-text "T" "x") (loop (+ i 1)))))))
`

func TestAListWindowAnswersOnItsChannel(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	run(t, r, "pick")
	req, ok := r.TakeWindow()
	if !ok || req.Kind != ListWindow || req.Title != "Pick" || strings.Join(req.Lines, "|") != "alpha|bee (label)|gamma" {
		t.Fatalf("request %+v", req)
	}
	if _, more := r.TakeWindow(); more {
		t.Fatal("one command asked for one window")
	}
	r.Answer(req, 1, "")
	events := poll(r, 5)
	if len(events) != 1 || deliver(t, r, events[0]).Buffer.Text() != "bee" {
		t.Fatalf("a pair sends its value, not its label: %v", events)
	}
	if h, _ := r.Waiting(); h != 0 {
		t.Fatalf("the channel is closed after the answer, so its handler goes: %d", h)
	}
}

func TestAListSendsAStringItemAsItIs(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	run(t, r, "pick")
	req, _ := r.TakeWindow()
	r.Answer(req, 2, "")
	events := poll(r, 5)
	if len(events) != 1 || deliver(t, r, events[0]).Buffer.Text() != "gamma" {
		t.Fatalf("events %v", events)
	}
}

func TestACancelledWindowClosesItsChannelWithNothingSent(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	for _, name := range []string{"pick", "ask", "read"} {
		run(t, r, name)
		req, ok := r.TakeWindow()
		if !ok {
			t.Fatalf("%s: no window", name)
		}
		r.Dismiss(req)
	}
	if events := poll(r, 5); len(events) != 0 {
		t.Fatalf("a cancelled window sent %d values", len(events))
	}
	if h, _ := r.Waiting(); h != 0 {
		t.Fatalf("handlers left: %d", h)
	}
}

func TestAPromptStartsWithTheGivenTextAndSendsWhatWasTyped(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	run(t, r, "ask")
	req, _ := r.TakeWindow()
	if req.Kind != PromptWindow || req.Initial != "start" || req.Title != "Name" {
		t.Fatalf("request %+v", req)
	}
	r.Answer(req, -1, "world")
	events := poll(r, 5)
	if len(events) != 1 || deliver(t, r, events[0]).Buffer.Text() != "got world" {
		t.Fatalf("events %v", events)
	}
}

func TestATextWindowTakesLinesAsAStringOrAList(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	run(t, r, "read")
	req, _ := r.TakeWindow()
	if req.Kind != TextWindow || strings.Join(req.Lines, "|") != "one|two" {
		t.Fatalf("request %+v", req)
	}
}

func TestWindowArgumentsAreChecked(t *testing.T) {
	long := func(n int) string { return `"` + strings.Repeat("a", n) + `"` }
	r := loaded(t, Options{}, windowProgram+`
(define-command "long-title" (lambda () (prompt `+long(300)+`)))
(define-command "long-label" (lambda () (show-list "T" (list `+long(600)+`))))
(define-command "long-line" (lambda () (show-text "T" `+long(3000)+`)))
`)
	for name, want := range map[string]string{
		"bad-list":   "item 1 must be a string",
		"bad-text":   "line 2 must be a string",
		"long-title": "at most 200 characters",
		"long-label": "longer than 500",
		"long-line":  "longer than 2000",
	} {
		err := r.Run(context.Background(), name, nil, session("", 0, 0, nil, false))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v; want %q", name, err, want)
		}
	}
}

func TestAFailedCommandOpensNoWindow(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	if err := r.Run(context.Background(), "failing", nil, session("", 0, 0, nil, false)); err == nil {
		t.Fatal("want an error")
	}
	if r.WindowsWaiting() != 0 {
		t.Fatal("a window was queued by a command that failed")
	}
}

func TestWindowsWaitingAreCapped(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	err := r.Run(context.Background(), "flood", nil, session("", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "at most 8 windows") {
		t.Fatalf("got %v", err)
	}
}

func TestWindowsAreForgottenOnDemand(t *testing.T) {
	r := loaded(t, Options{}, windowProgram)
	run(t, r, "pick")
	run(t, r, "read")
	if r.WindowsWaiting() != 2 {
		t.Fatalf("waiting %d", r.WindowsWaiting())
	}
	r.DropWindows()
	if r.WindowsWaiting() != 0 {
		t.Fatal("not dropped")
	}
}
