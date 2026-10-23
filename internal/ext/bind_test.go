package ext

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/key"
)

func TestBindKeyRecordsBindingsInOrder(t *testing.T) {
	r := loaded(t, Options{}, `
(define (up) (insert-text "UP"))
(bind-key "normal" "x" up "insert UP")
(bind-key "visual" "x" up)
(bind-key "normal" "<C-d>" up "ctrl" #f)
(bind-key "normal" "<A-j>" up "alt" #t)
`)
	got := r.Bindings()
	if len(got) != 4 {
		t.Fatalf("%d bindings", len(got))
	}
	want := []struct {
		mode, spec, doc string
		k               key.Key
		override        bool
	}{
		{"normal", "x", "insert UP", key.Char('x'), false},
		{"visual", "x", "", key.Char('x'), false},
		{"normal", "<C-d>", "ctrl", key.WithCtrl('d'), false},
		{"normal", "<A-j>", "alt", key.Key{Code: 'j', Mods: key.Alt}, true},
	}
	for i, w := range want {
		b := got[i]
		if b.Mode != w.mode || b.Spec != w.spec || b.Doc != w.doc || b.Key != w.k || b.Override != w.override || b.Number != i+1 {
			t.Errorf("binding %d: %+v, want %+v", i+1, b, w)
		}
	}
	if _, ok := r.Binding("normal", key.Key{Code: 'x', Mods: key.Shift}); !ok {
		t.Error("the Shift modifier made a binding unreachable")
	}
	if _, ok := r.Binding("visual", key.WithCtrl('d')); ok {
		t.Error("a normal-mode binding answered in visual mode")
	}
	if _, ok := r.Binding("normal", key.Char('y')); ok {
		t.Error("an unbound key answered")
	}
}

func TestBindKeyRules(t *testing.T) {
	seen := ""
	opts := Options{Bind: func(mode string, k key.Key, override bool) error {
		seen = mode + " " + k.Code.String()
		if k.Code == 'q' {
			return errors.New("q is kept by the editor")
		}
		return nil
	}}
	for program, want := range map[string]string{
		`(bind-key "insert" "x" car)`:                                              `"insert" is not a mode`,
		`(bind-key "normal" "xy" car)`:                                             `"xy" is not a key`,
		`(bind-key "normal" "<Esc>" car)`:                                          "Escape cannot be bound",
		`(bind-key "normal" "<C-c>" car)`:                                          "Ctrl-C interrupts",
		`(bind-key "normal" "x" 42)`:                                               "expected procedure",
		`(bind-key "normal" 'x car)`:                                               "expected string",
		`(bind-key "normal" "x" car 7)`:                                            "expected string",
		`(bind-key "normal" "x" car "doc" 1)`:                                      "override is #t or #f",
		`(bind-key "normal" "x")`:                                                  "expected 3 to 5 argument(s), got 2",
		`(bind-key "normal" "q" car)`:                                              "q is kept by the editor",
		`(bind-key "normal" "x" car "first") (bind-key "normal" "x" cdr "second")`: `normal x is already bound by binding 1 ("first"); binding 2 ("second") cannot also use it`,
		`(bind-key "visual" "<C-d>" car) (bind-key "visual" "<C-d>" cdr)`:          `visual <C-d> is already bound by binding 1 (no description); binding 2 (no description)`,
	} {
		err := New(opts).Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", program, err, want)
		}
	}
	// The same key in the other mode is not a conflict, and a refused binding is not recorded.
	r := New(opts)
	if err := r.Load(context.Background(), `(bind-key "normal" "x" car) (bind-key "visual" "x" car)`); err != nil || len(r.Bindings()) != 2 {
		t.Fatalf("both modes: %v, %d bindings", err, len(r.Bindings()))
	}
	if seen != "visual 'x'" {
		t.Errorf("the editor was asked about %q", seen)
	}
	r = New(opts)
	if err := r.Load(context.Background(), `(guard (e (#t #f)) (bind-key "normal" "q" car)) (bind-key "normal" "q" car "again")`); err == nil {
		t.Fatal("a refused binding was remembered as a success")
	}
	if len(r.Bindings()) != 0 {
		t.Errorf("a refused binding was recorded")
	}
}

func TestBindKeyLimit(t *testing.T) {
	var program strings.Builder
	for i := 0; i <= MaxBindings; i++ {
		program.WriteString(`(bind-key "normal" "` + string(rune(0x4e00+i)) + `" car)` + "\n")
	}
	r := New(Options{})
	err := r.Load(context.Background(), program.String())
	if err == nil || !strings.Contains(err.Error(), "at most 100 keys can be bound") {
		t.Fatalf("error %v", err)
	}
	if len(r.Bindings()) != MaxBindings {
		t.Fatalf("%d bindings", len(r.Bindings()))
	}
}

func TestBindingsRunAsACommandDoes(t *testing.T) {
	r := loaded(t, Options{}, `
(define (shout) (replace-selection (string-upcase (selection-text))) (message (mode)))
(define (add) (insert-text "+"))
(define (boom) (insert-text "partial") (error "boom"))
(bind-key "visual" "U" shout)
(bind-key "normal" "<C-d>" add)
(bind-key "normal" "<A-b>" boom)
`)
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	if err := r.RunBinding(context.Background(), "visual", key.Char('U'), s); err != nil {
		t.Fatal(err)
	}
	if s.Buffer.Text() != "ABc" || s.Message() != "visual" || !s.Edited() {
		t.Fatalf("text %q message %q edited %v", s.Buffer.Text(), s.Message(), s.Edited())
	}
	s = session("abc", 0, 1, nil, false)
	if err := r.RunBinding(context.Background(), "normal", key.WithCtrl('d'), s); err != nil || s.Buffer.Text() != "a+bc" {
		t.Fatalf("%v %q", err, s.Buffer.Text())
	}
	// A failing binding fails like a command: the error is the program's, and the editor drops the session.
	s = session("abc", 0, 1, nil, false)
	err := r.RunBinding(context.Background(), "normal", key.Key{Code: 'b', Mods: key.Alt}, s)
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Failed || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %v", err)
	}
	if err := r.RunBinding(context.Background(), "normal", key.Char('z'), session("abc", 0, 0, nil, false)); err == nil || !strings.Contains(err.Error(), "no binding") {
		t.Fatalf("an unbound key ran: %v", err)
	}
}

func TestBindKeyIsDeniedOutsideLoading(t *testing.T) {
	r := loaded(t, Options{}, `
(define (late) (bind-key "normal" "x" car))
(define-command "late" late)
`)
	err := r.Run(context.Background(), "late", nil, session("ab", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "only allowed while init.scm loads") {
		t.Errorf("error %v", err)
	}
}
