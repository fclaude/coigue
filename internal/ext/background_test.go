package ext

import (
	"context"
	"strings"
	"testing"
	"time"
)

const backgroundProgram = `
(define saved #f)
(define-command "start"
  (lambda ()
    (on-done (spawn-detached (lambda () (* 6 7)))
             (lambda (t) (insert-text (number->string (task-wait t)))))))
(define-command "fail"
  (lambda ()
    (on-done (spawn-detached (lambda () (error "boom")))
             (lambda (t) (guard (e (#t (insert-text "caught"))) (task-wait t))))))
(define-command "fail-raw"
  (lambda ()
    (on-done (spawn-detached (lambda () (error "kaboom")))
             (lambda (t) (task-wait t)))))
(define-command "unwatched" (lambda () (spawn-detached (lambda () (error "nobody listens")))))
(define-command "hold" (lambda () (set! saved (spawn-detached (lambda () (* 3 3))))))
(define-command "wait-later" (lambda () (insert-text (number->string (task-wait saved)))))
(define-command "forever"
  (lambda ()
    (set! saved (spawn-detached (lambda () (let loop () (loop)))))
    (on-done saved (lambda (t) (insert-text "ran")))))
(define-command "cancel" (lambda () (task-cancel! saved)))
(define-command "toucher"
  (lambda () (on-done (spawn-detached (lambda () (insert-text "x"))) (lambda (t) (task-wait t)))))
(define-command "bad-task" (lambda () (on-done 42 (lambda (t) t))))
(define-command "twice"
  (lambda ()
    (let ((t (spawn-detached (lambda () 1))))
      (on-done t (lambda (x) x))
      (on-done t (lambda (x) x)))))
`

// events waits for n events to be ready.
func events(t *testing.T, r *Runtime, n int) []Event {
	t.Helper()
	var got []Event
	deadline := time.Now().Add(3 * time.Second)
	for len(got) < n {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d events, got %d", n, len(got))
		}
		got = append(got, r.PollEvents(10)...)
		time.Sleep(2 * time.Millisecond)
	}
	return got
}

func TestADetachedTaskOutlivesItsCommandAndOnDoneDeliversIt(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "start")
	if h, _ := r.Waiting(); h != 1 {
		t.Fatalf("waiting %d", h)
	}
	evs := events(t, r, 1)
	if got := deliver(t, r, evs[0]).Buffer.Text(); got != "42" {
		t.Fatalf("handler inserted %q", got)
	}
	if h, _ := r.Waiting(); h != 0 {
		t.Fatalf("a finished task keeps its handler: %d", h)
	}
}

func TestAFailedTaskIsReportedToItsHandlerWhichCanGuardIt(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "fail")
	evs := events(t, r, 1)
	if got := deliver(t, r, evs[0]).Buffer.Text(); got != "caught" {
		t.Fatalf("handler inserted %q", got)
	}
	// The handler heard of it, so the host does not report it a second time.
	if errs := r.TakeFailures(); len(errs) != 0 {
		t.Fatalf("a watched failure was also reported to the host: %v", errs)
	}
}

func TestAHandlerThatDoesNotGuardFailsLikeAnyCommand(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "fail-raw")
	evs := events(t, r, 1)
	err := r.RunEvent(context.Background(), evs[0], session("", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("got %v", err)
	}
}

func TestAFailureNobodyWatchesGoesToTheHost(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "unwatched")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if errs := r.TakeFailures(); len(errs) > 0 {
			if !strings.Contains(errs[0].Error(), "nobody listens") {
				t.Fatalf("got %v", errs[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the failure never reached the host")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestALaterCommandCanAwaitATaskAnEarlierOneStarted(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "hold")
	s := session("", 0, 0, nil, false)
	if err := r.Run(context.Background(), "wait-later", nil, s); err != nil || s.Buffer.Text() != "9" {
		t.Fatalf("got %q, %v", s.Buffer.Text(), err)
	}
}

func TestACancelledTaskDoesNotRunItsHandler(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "forever")
	if evs := r.PollEvents(10); len(evs) != 0 {
		t.Fatalf("a running task fired: %d", len(evs))
	}
	run(t, r, "cancel")
	time.Sleep(50 * time.Millisecond)
	if evs := r.PollEvents(10); len(evs) != 0 {
		t.Fatalf("a cancelled task ran its handler: %d events", len(evs))
	}
	if h, _ := r.Waiting(); h != 0 {
		t.Fatalf("a cancelled task keeps its handler: %d", h)
	}
}

func TestShutdownEndsATaskThatNeverFinishes(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "forever")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if evs := r.PollEvents(10); len(evs) != 0 {
		t.Fatalf("a task ended by Shutdown ran its handler: %d", len(evs))
	}
}

func TestABackgroundTaskCannotTouchTheBufferAndSaysWhy(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	run(t, r, "toucher")
	evs := events(t, r, 1)
	err := r.RunEvent(context.Background(), evs[0], session("", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "a task cannot read or edit the buffer") {
		t.Fatalf("got %v", err)
	}
}

func TestOnDoneChecksItsArguments(t *testing.T) {
	r := loaded(t, Options{}, backgroundProgram)
	for name, want := range map[string]string{
		"bad-task": "must be a task",
		"twice":    "already has a handler",
	} {
		err := r.Run(context.Background(), name, nil, session("", 0, 0, nil, false))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v; want %q", name, err, want)
		}
	}
}
