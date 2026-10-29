package ext

import (
	"context"
	"strings"
	"testing"
	"time"
)

const eventProgram = `
(define inbox (make-channel 8))
(define-command "listen"
  (lambda () (on-channel inbox (lambda (v) (insert-text (string-append "<" (if (string? v) v "?") ">"))))))
(define-command "send"
  (lambda (word) (channel-send! inbox word)))
(define-command "close" (lambda () (channel-close! inbox)))
`

func run(t *testing.T, r *Runtime, name string, args ...string) *Session {
	t.Helper()
	s := session("", 0, 0, nil, false)
	if err := r.Run(context.Background(), name, args, s); err != nil {
		t.Fatalf(":%s: %v", name, err)
	}
	r.Adopt(s)
	return s
}

func poll(r *Runtime, max int) []Event { return r.PollEvents(context.Background(), max) }

func deliver(t *testing.T, r *Runtime, ev Event) *Session {
	t.Helper()
	s := session("", 0, 0, nil, false)
	if err := r.RunEvent(context.Background(), ev, s); err != nil {
		t.Fatalf("handler: %v", err)
	}
	return s
}

func TestAHandlerRunsForEachValueInOrder(t *testing.T) {
	r := loaded(t, Options{}, eventProgram)
	run(t, r, "listen")
	if h, _ := r.Waiting(); h != 1 {
		t.Fatalf("handlers = %d", h)
	}
	run(t, r, "send", "a")
	run(t, r, "send", "b")
	events := poll(r, 10)
	if len(events) != 2 {
		t.Fatalf("%d events", len(events))
	}
	var got []string
	for _, ev := range events {
		got = append(got, deliver(t, r, ev).Buffer.Text())
	}
	if strings.Join(got, "") != "<a><b>" {
		t.Fatalf("handlers inserted %q", got)
	}
	if more := poll(r, 10); len(more) != 0 {
		t.Fatalf("%d events from an empty channel", len(more))
	}
	if h, _ := r.Waiting(); h != 1 {
		t.Fatalf("an empty open channel lost its handler: %d", h)
	}
}

func TestPollTakesNoMoreThanItIsAsked(t *testing.T) {
	r := loaded(t, Options{}, eventProgram)
	run(t, r, "listen")
	for _, w := range []string{"a", "b", "c", "d", "e"} {
		run(t, r, "send", w)
	}
	if n := len(poll(r, 2)); n != 2 {
		t.Fatalf("first poll %d", n)
	}
	if n := len(poll(r, 10)); n != 3 {
		t.Fatalf("second poll %d; the rest should have waited in the channel", n)
	}
}

func TestAClosedChannelIsDrainedThenForgotten(t *testing.T) {
	r := loaded(t, Options{}, eventProgram)
	run(t, r, "listen")
	run(t, r, "send", "last")
	run(t, r, "close")
	events := poll(r, 10)
	if len(events) != 1 || deliver(t, r, events[0]).Buffer.Text() != "<last>" {
		t.Fatalf("events %v", events)
	}
	if h, _ := r.Waiting(); h != 0 {
		t.Fatalf("a closed, drained channel kept its handler: %d", h)
	}
}

func TestACommandThatFailsRegistersNothing(t *testing.T) {
	r := loaded(t, Options{}, `
(define c (make-channel 1))
(define-command "bad" (lambda () (on-channel c (lambda (v) v)) (timer 5) (error "no")))`)
	s := session("", 0, 0, nil, false)
	if err := r.Run(context.Background(), "bad", nil, s); err == nil {
		t.Fatal("want an error")
	}
	// The editor adopts only after a success, so a failed session adds nothing.
	if h, tm := r.Waiting(); h != 0 || tm != 0 {
		t.Fatalf("handlers %d timers %d", h, tm)
	}
}

func TestTimerSendsOnceWhenDue(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "wait"
  (lambda () (on-channel (timer 0.05) (lambda (v) (insert-text (if (eq? v #t) "ding" "?"))))))`)
	run(t, r, "wait")
	if h, tm := r.Waiting(); h != 1 || tm != 1 {
		t.Fatalf("handlers %d timers %d", h, tm)
	}
	r.FireTimers(context.Background(), time.Now())
	if n := len(poll(r, 10)); n != 0 {
		t.Fatalf("the timer fired early: %d events", n)
	}
	r.FireTimers(context.Background(), time.Now().Add(time.Second))
	events := poll(r, 10)
	if len(events) != 1 || deliver(t, r, events[0]).Buffer.Text() != "ding" {
		t.Fatalf("events %v", events)
	}
	if _, tm := r.Waiting(); tm != 0 {
		t.Fatalf("timers left: %d", tm)
	}
	r.FireTimers(context.Background(), time.Now().Add(time.Hour))
	if n := len(poll(r, 10)); n != 0 {
		t.Fatalf("a timer fired twice: %d", n)
	}
}

func TestTimerAndOnChannelCheckTheirArguments(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "t" (lambda (x) (timer (string->number x))))
(define-command "c" (lambda () (on-channel 42 (lambda (v) v))))
(define-command "p" (lambda () (on-channel (make-channel 1) 7)))
(define-command "twice" (lambda ()
  (let ((c (make-channel 1)))
    (on-channel c (lambda (v) v))
    (on-channel c (lambda (v) v)))))`)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"early":   {[]string{"t", "0.001"}, "0.01"},
		"late":    {[]string{"t", "99999999"}, "604800"},
		"channel": {[]string{"c"}, "must be a channel"},
		"proc":    {[]string{"p"}, "procedure"},
		"twice":   {[]string{"twice"}, "already has a handler"},
	} {
		err := r.Run(context.Background(), tc.args[0], tc.args[1:], session("", 0, 0, nil, false))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v; want it to mention %q", name, err, tc.want)
		}
	}
}

func TestHandlersAndTimersAreCapped(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "many" (lambda ()
  (let loop ((i 0))
    (if (< i 100) (begin (on-channel (make-channel 1) (lambda (v) v)) (loop (+ i 1)))))))
(define-command "timers" (lambda ()
  (let loop ((i 0))
    (if (< i 100) (begin (timer 60) (loop (+ i 1)))))))`)
	for name, want := range map[string]string{"many": "at most 64 channels", "timers": "at most 64 timers"} {
		err := r.Run(context.Background(), name, nil, session("", 0, 0, nil, false))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestTheEditorNeverWaitsForAChannel(t *testing.T) {
	// A send on an unbuffered channel waits for a receiver. The host never
	// receives by waiting, so Brote reports a deadlock the command can see.
	r := loaded(t, Options{}, `
(define c (make-channel))
(define-command "listen" (lambda () (on-channel c (lambda (v) v))))
(define-command "send" (lambda () (channel-send! c 1)))`)
	run(t, r, "listen")
	err := r.Run(context.Background(), "send", nil, session("", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "deadlock") {
		t.Fatalf("got %v", err)
	}
}

func TestATaskCannotTouchTheBuffer(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "bad"
  (lambda () (task-wait (spawn (lambda () (insert-text "x"))))))
(define-command "good"
  (lambda () (insert-text (task-wait (spawn (lambda () "from a task"))))))`)
	s := session("", 0, 0, nil, false)
	err := r.Run(context.Background(), "bad", nil, s)
	if err == nil || !strings.Contains(err.Error(), "a task cannot read or edit the buffer") {
		t.Fatalf("got %v", err)
	}
	if s.Buffer.Text() != "" {
		t.Fatalf("the task edited the buffer: %q", s.Buffer.Text())
	}
	s = session("", 0, 0, nil, false)
	if err := r.Run(context.Background(), "good", nil, s); err != nil || s.Buffer.Text() != "from a task" {
		t.Fatalf("a task's value is usable in the command: %v %q", err, s.Buffer.Text())
	}
}

func TestTasksEndWithTheirCommand(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "leak" (lambda () (spawn (lambda () (let loop () (loop))))))`)
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background(), "leak", nil, session("", 0, 0, nil, false)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("the command reports the stopped task: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a command that spawned a task that never ends did not return")
	}
}

func TestTaskProceduresAreAvailable(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "par"
  (lambda () (insert-text (number->string (apply + (pmap (lambda (x) (* x x)) (list 1 2 3)))))))`)
	s := session("", 0, 0, nil, false)
	if err := r.Run(context.Background(), "par", nil, s); err != nil || s.Buffer.Text() != "14" {
		t.Fatalf("pmap: %v %q", err, s.Buffer.Text())
	}
}
