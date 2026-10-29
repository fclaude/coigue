package editor

import (
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/key"
)

const windowInit = `
(define-command "pick"
  (lambda ()
    (on-channel (show-list "Pick one" (list "alpha" "beta" "gamma" "alphabet"))
                (lambda (v) (insert-text v)))))
(define-command "ask"
  (lambda ()
    (on-channel (prompt "Name" "x") (lambda (text) (insert-text text)))))
(define-command "read"
  (lambda ()
    (on-channel (show-text "Help" (list "line one" "line two"))
                (lambda (_) (insert-text "closed")))))
(define-command "two"
  (lambda ()
    (on-channel (show-text "First" "a") (lambda (_) (insert-text "1")))
    (on-channel (show-text "Second" "b") (lambda (_) (insert-text "2")))))
`

// evilInit shows text with a right-to-left override in the title and a line.
const evilInit = "(define-command \"evil\" (lambda () (show-text \"Evil \u202e title\" (list \"a\u202eb\" \"ok\"))))\n"

func TestAListWindowFiltersAndChoosesAndTheHandlerRuns(t *testing.T) {
	e := extensionEditor(t, "", windowInit)
	command(e, "pick")
	w := e.Window
	if w == nil || w.Kind != WindowList || len(w.Matches()) != 4 {
		t.Fatalf("window %+v", w)
	}
	// The window is modal: typing filters the list and never reaches the text.
	send(e, "alp")
	if got := w.Matches(); len(got) != 2 || w.Lines[got[0]] != "alpha" || w.Lines[got[1]] != "alphabet" {
		t.Fatalf("matches %v for %q", got, w.Query)
	}
	press(e, key.Down)
	press(e, key.Enter)
	if e.Window != nil {
		t.Fatal("the window is still open after Enter")
	}
	tickUntil(t, e, "the chosen item", func() bool { return e.Buffer.Text() == "alphabet" })
}

func TestEscCancelsAWindowWithoutRunningItsHandler(t *testing.T) {
	e := extensionEditor(t, "", windowInit)
	for _, name := range []string{"pick", "ask", "read"} {
		command(e, name)
		if e.Window == nil {
			t.Fatalf("%s: no window", name)
		}
		press(e, key.Esc)
		if e.Window != nil {
			t.Fatalf("%s: Esc left the window open", name)
		}
	}
	tickFor(e, 50*time.Millisecond)
	if e.Buffer.Text() != "" {
		t.Fatalf("a handler ran for a cancelled window: %q", e.Buffer.Text())
	}
}

func TestAPromptEditsItsLineAndSendsIt(t *testing.T) {
	e := extensionEditor(t, "", windowInit)
	command(e, "ask")
	if e.Window.Kind != WindowPrompt || e.Window.Query != "x" {
		t.Fatalf("window %+v", e.Window)
	}
	send(e, "yz")
	press(e, key.Backspace)
	if e.Window.Query != "xy" {
		t.Fatalf("query %q", e.Window.Query)
	}
	ctrl(e, 'u')
	send(e, "hello world")
	press(e, key.Enter)
	tickUntil(t, e, "the answer", func() bool { return e.Buffer.Text() == "hello world" })
}

func TestATextWindowIsDismissedByEnterQOrEsc(t *testing.T) {
	for _, k := range []key.Key{key.Press(key.Enter), key.Char('q'), key.Press(key.Esc)} {
		e := extensionEditor(t, "", windowInit)
		command(e, "read")
		if e.Window == nil || e.Window.Kind != WindowText {
			t.Fatalf("window %+v", e.Window)
		}
		send(e, "j")
		e.Key(k, 22)
		if e.Window != nil {
			t.Fatalf("%v left it open", k)
		}
	}
}

func TestWindowsAreShownOneAtATimeInOrder(t *testing.T) {
	e := extensionEditor(t, "", windowInit)
	command(e, "two")
	if e.Window == nil || e.Window.Title != "First" {
		t.Fatalf("window %+v", e.Window)
	}
	press(e, key.Enter)
	if e.Window == nil || e.Window.Title != "Second" {
		t.Fatalf("the second window did not follow: %+v", e.Window)
	}
	press(e, key.Enter)
	if e.Window != nil {
		t.Fatalf("window %+v", e.Window)
	}
}

func TestAWindowGetsEveryKeyAndTheBufferIsUntouched(t *testing.T) {
	e := extensionEditor(t, "keep", windowInit)
	command(e, "read")
	send(e, "i:dd")
	press(e, key.F(1))
	if e.Window == nil || e.Mode != ModeNormal || e.Buffer.Text() != "keep" || e.Help != nil {
		t.Fatalf("a key got through: mode %v help %v text %q", e.Mode, e.Help, e.Buffer.Text())
	}
}

func TestWindowTextCannotHideOrRepaint(t *testing.T) {
	e := extensionEditor(t, "", windowInit+evilInit)
	command(e, "evil")
	w := e.Window
	all := w.Title + "\n" + strings.Join(w.Lines, "\n")
	if strings.ContainsRune(all, 0x202e) || !strings.Contains(all, "<U+202E>") {
		t.Fatalf("bidirectional control not written out: %q", all)
	}
}

func TestWindowKeysStayInRange(t *testing.T) {
	w := &Window{Kind: WindowList, Lines: []string{"a", "b", "c"}}
	for _, k := range []key.Key{key.Press(key.Down), key.Press(key.Down), key.Press(key.Down), key.Press(key.PageDown), key.Press(key.End)} {
		w.Key(k, 2)
	}
	if w.Selected != 2 {
		t.Fatalf("selected %d", w.Selected)
	}
	w.Key(key.Char('z'), 2)
	if finished, _ := w.Key(key.Press(key.Enter), 2); finished {
		t.Fatal("Enter on an empty match list must not finish")
	}
	if w.Selected != 0 {
		t.Fatalf("selected %d with no matches", w.Selected)
	}
}

func TestReloadForgetsQueuedWindows(t *testing.T) {
	e := extensionEditor(t, "", windowInit)
	e.queuedWindows = []*Window{{Kind: WindowText, Title: "stale"}}
	command(e, "reload")
	if len(e.queuedWindows) != 0 {
		t.Fatalf("queued %d after :reload", len(e.queuedWindows))
	}
}
