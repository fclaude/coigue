package ext

import (
	"context"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/buffer"
)

func TestOnStart(t *testing.T) {
	r := loaded(t, Options{}, `(on-start (lambda () (insert-text "hello ") (message (buffer-path))))`)
	if !r.HasStart() {
		t.Fatal("no start-up procedure")
	}
	b := buffer.New("world")
	s := NewSession(b, nil)
	s.Path = "/tmp/x.txt"
	if err := r.RunStart(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if b.Text() != "hello world" || s.Message() != "/tmp/x.txt" || s.Mode != "normal" {
		t.Fatalf("text %q message %q mode %q", b.Text(), s.Message(), s.Mode)
	}
	if New(Options{}).HasStart() {
		t.Fatal("a start-up procedure out of nowhere")
	}
	for program, want := range map[string]string{
		`(on-start car) (on-start cdr)`: "on-start was already called",
		`(on-start 42)`:                 "expected procedure",
		`(on-start)`:                    "expected 1 argument(s), got 0",
	} {
		err := New(Options{}).Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", program, err, want)
		}
	}
	// It is for loading time only.
	r = loaded(t, Options{}, `(define-command "late" (lambda () (on-start car)))`)
	if err := r.Run(context.Background(), "late", nil, session("ab", 0, 0, nil, false)); err == nil || !strings.Contains(err.Error(), "only allowed while init.scm loads") {
		t.Errorf("error %v", err)
	}
	// A start-up procedure is a command: it fails like one, and the deadline applies.
	r = loaded(t, Options{}, `(on-start (lambda () (error "no luck")))`)
	if err := r.RunStart(context.Background(), session("a", 0, 0, nil, false)); err == nil || !strings.Contains(err.Error(), "no luck") {
		t.Fatalf("error %v", err)
	}
}
