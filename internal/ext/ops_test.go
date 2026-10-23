package ext

import (
	"context"
	"testing"
)

func TestSessionPathAndMode(t *testing.T) {
	r := loaded(t, Options{}, `(define-command "info" (lambda () (message (string-append (mode) " [" (buffer-path) "]"))))`)
	for _, c := range []struct {
		anchor   *[2]int
		linewise bool
		path     string
		want     string
	}{
		{nil, false, "", "normal []"},
		{nil, false, "/a/b.txt", "normal [/a/b.txt]"},
		{&[2]int{0, 0}, false, "", "visual []"},
		{&[2]int{0, 0}, true, "", "visual-line []"},
	} {
		s := session("ab", 0, 1, c.anchor, c.linewise)
		s.Path = c.path
		if err := r.Run(context.Background(), "info", nil, s); err != nil || s.Message() != c.want {
			t.Errorf("%v: %v, message %q, want %q", c, err, s.Message(), c.want)
		}
	}
}

func TestOnlySetCursorLeavesTheTextAlone(t *testing.T) {
	r := loaded(t, Options{}, `(define-command "go" (lambda () (set-cursor 2 1)))
(define-command "nothing" (lambda () (line-count)))`)
	s := session("ab\ncd", 0, 0, nil, false)
	if err := r.Run(context.Background(), "go", nil, s); err != nil || !s.Edited() || s.Buffer.Row != 1 {
		t.Fatalf("%v edited %v row %d", err, s.Edited(), s.Buffer.Row)
	}
	s = session("ab\ncd", 0, 0, nil, false)
	if err := r.Run(context.Background(), "nothing", nil, s); err != nil || s.Edited() {
		t.Fatalf("%v edited %v", err, s.Edited())
	}
}

func TestSeveralEditOperationsInOneCommand(t *testing.T) {
	r := loaded(t, Options{}, `
(define-command "tidy"
  (lambda ()
    (replace-lines 2 2 "TWO")
    (insert-at 1 1 "> ")
    (delete-lines 3 3)
    (insert-at (line-count) 1 "end: ")
    (set-cursor 1 1)))
`)
	s := session("one\ntwo\nthree\nfour", 3, 0, nil, false)
	if err := r.Run(context.Background(), "tidy", nil, s); err != nil {
		t.Fatal(err)
	}
	if got := s.Buffer.Text(); got != "> one\nTWO\nend: four" || s.Buffer.Row != 0 || s.Buffer.Col != 0 {
		t.Fatalf("text %q cursor (%d,%d)", got, s.Buffer.Row, s.Buffer.Col)
	}
}
