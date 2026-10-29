package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/key"
)

// backgroundInit starts a task that waits until "release" lets it go, so a test
// controls when the work finishes without any clock.
const backgroundInit = `
(define gate (make-channel 1))
(define-command "slow"
  (lambda ()
    (message "working")
    (on-done (spawn-detached (lambda () (channel-recv gate) "finished"))
             (lambda (t) (insert-text (task-wait t))))))
(define-command "release" (lambda () (channel-send! gate 1)))
(define-command "boom" (lambda () (spawn-detached (lambda () (error "kaboom")))))
`

func TestABackgroundTaskDeliversToItsHandlerWhileTheEditorStaysUsable(t *testing.T) {
	e := extensionEditor(t, "", backgroundInit)
	command(e, "slow")
	if e.Message != "working" {
		t.Fatalf("message %q", e.Message)
	}
	// While the task waits, the editor works as usual.
	send(e, "iab")
	press(e, key.Esc)
	if e.Buffer.Text() != "ab" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	tickFor(e, 30*time.Millisecond)
	if strings.Contains(e.Buffer.Text(), "finished") {
		t.Fatal("the handler ran before the task finished")
	}
	command(e, "release")
	tickUntil(t, e, "the handler", func() bool { return strings.Contains(e.Buffer.Text(), "finished") })
	undo(e)
	if strings.Contains(e.Buffer.Text(), "finished") || e.Buffer.Text() != "ab" {
		t.Fatalf("the handler's edit is not one undo step: %q", e.Buffer.Text())
	}
}

func TestAFailureNobodyWatchesAppearsInTheStatusLine(t *testing.T) {
	e := extensionEditor(t, "text", backgroundInit)
	command(e, "boom")
	tickUntil(t, e, "the failure", func() bool { return strings.Contains(e.Message, "background task failed") })
	if !strings.Contains(e.Message, "kaboom") {
		t.Fatalf("message %q", e.Message)
	}
}

func TestReloadEndsTheOldCodesBackgroundTasks(t *testing.T) {
	e := extensionEditor(t, "text", backgroundInit)
	command(e, "slow")
	old := e.extensions
	if h, _ := old.Waiting(); h != 1 {
		t.Fatalf("waiting %d", h)
	}
	command(e, "reload")
	if !strings.HasPrefix(e.Message, "Reloaded") {
		t.Fatalf("reload: %q", e.Message)
	}
	// The old runtime's task is cancelled: its handler is forgotten, not run.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if evs := old.PollEvents(10); len(evs) != 0 {
			t.Fatalf("a task from before :reload ran its handler")
		}
		if h, _ := old.Waiting(); h == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the old task was not ended by :reload")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.Contains(e.Buffer.Text(), "finished") {
		t.Fatalf("text %q", e.Buffer.Text())
	}
}

func TestAFailedReloadEndsWhatTheStagedCodeStarted(t *testing.T) {
	e := extensionEditor(t, "text", backgroundInit)
	before := e.extensions
	write(t, e.initPath, `
(define t (spawn-detached (lambda () (let loop () (loop)))))
(error "stop here")
`)
	command(e, "reload")
	if !strings.Contains(e.Message, "nothing changed") || e.extensions != before {
		t.Fatalf("message %q", e.Message)
	}
}

func TestClosingTheEditorEndsBackgroundTasks(t *testing.T) {
	e := extensionEditor(t, "text", backgroundInit)
	command(e, "slow")
	rt := e.extensions
	e.Close()
	if evs := rt.PollEvents(10); len(evs) != 0 {
		t.Fatalf("a task ended by Close ran its handler")
	}
	if h, _ := rt.Waiting(); h != 0 {
		t.Fatalf("handlers left after Close: %d", h)
	}
}

func TestTheDocumentedBackgroundExamplesRun(t *testing.T) {
	e := extensionEditor(t, "one\ntwo\nthree", `
(define-command "stats"
  (lambda ()
    ;; Read what the task needs now, in the command: a task cannot touch the buffer.
    (let ((lines (map (lambda (i) (buffer-text (+ i 1))) (iota (line-count)))))
      (message "Counting...")
      (on-done (spawn-detached (lambda () (apply + (pmap string-length lines))))
               (lambda (task)
                 (message (string-append (number->string (task-wait task)) " characters")))))))
(define-command "guarded"
  (lambda ()
    (on-done (spawn-detached (lambda () (error "no")))
             (lambda (task)
               (guard (e (#t (message "Could not count")))
                 (message (number->string (task-wait task))))))))
`)
	command(e, "stats")
	if e.Message != "Counting..." {
		t.Fatalf("message %q", e.Message)
	}
	tickUntil(t, e, "the count", func() bool { return strings.HasSuffix(e.Message, "characters") })
	if e.Message != "11 characters" {
		t.Fatalf("message %q", e.Message)
	}
	command(e, "guarded")
	tickUntil(t, e, "the guarded failure", func() bool { return e.Message == "Could not count" })
}
