package ext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/key"
)

func writeModules(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	mod := filepath.Join(dir, "modules")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(mod, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestModulesKeepTheirOwnCopyOfTheLanguage(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"a.scm": `(define-module a (export mine theirs))
(set! car (lambda (x) "stolen"))
(define (mine) (car '("one" "two")))
(define (theirs) (assoc-ref '((k . "ok")) 'k))
`,
		"b.scm": `(define-module b (export mine))
(define (mine) (car '("one" "two")))
`,
		"c.scm": `(define-module c (export seen via))
(define filter "shadow")
(define (car x) "local")
(define (seen) filter)
(define (via) (assoc-ref '((k . "ok")) 'k))
`,
		"d.scm": `(define-module d (export seen))
(define (seen) (filter (lambda (x) #t) '(1)))
`,
	})
	r := loaded(t, Options{ConfigDir: dir}, `
(require a)
(require b (prefix b:))
(require c (prefix c:))
(require d (prefix d:))
(define-command "show" (lambda ()
  (message (format "~a ~a ~a ~a ~a ~a" (mine) (theirs) (b:mine) (c:seen) (c:via)
    (if (equal? (d:seen) '(1)) "yes" "no")))))
`)
	s := session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "show", nil, s); err != nil {
		t.Fatal(err)
	}
	if s.Message() != "stolen ok one shadow ok yes" {
		t.Fatalf("message %q", s.Message())
	}
	if v, ok := r.interp.Global().Lookup("car"); !ok || v.Kind != scheme.KPrim {
		t.Fatalf("the global car changed: %+v %v", v, ok)
	}
	if v, ok := r.interp.Global().Lookup("filter"); !ok || v.Kind != scheme.KPrim {
		t.Fatalf("the global filter changed: %+v %v", v, ok)
	}
	var note string
	for _, m := range r.Modules() {
		if m.Name == "c" {
			note = strings.Join(m.Notes, "\n")
		}
	}
	for _, want := range []string{"defines car", "defines filter", "inside c"} {
		if !strings.Contains(note, want) {
			t.Errorf("notes %q, want %q", note, want)
		}
	}
}

func TestAnExportedProcedureKeepsItsPrivateNames(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"text-tools.scm": `(define-module text-tools (export wrap))
(define (surround open close)
  (string-append open close))
(define (wrap open close)
  (surround open close))
`,
	})
	r := loaded(t, Options{ConfigDir: dir}, `
(require text-tools)
(define (surround open close) "init")
(define-command "use" (lambda (open close) (message (wrap open close))))
`)
	s := session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "use", []string{"(", ")"}, s); err != nil || s.Message() != "()" {
		t.Fatalf("%v %q", err, s.Message())
	}
	err := New(Options{ConfigDir: dir}).Load(context.Background(), `
(require text-tools)
(define-command "nope" surround)
`)
	if err == nil || !strings.Contains(err.Error(), "unbound variable: surround") {
		t.Fatalf("a private name was visible: %v", err)
	}
}

func TestAnImportIsACopy(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"box.scm": `(define-module box (export n bump peek))
(define n 1)
(define (bump) (set! n (+ n 1)) n)
(define (peek) n)
`,
	})
	r := loaded(t, Options{ConfigDir: dir}, `
(require box)
(define-command "show" (lambda ()
  (define seen n)
  (define after (bump))
  (set! n 9)
  (message (format "~a ~a ~a ~a" seen after n (peek)))))
`)
	s := session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "show", nil, s); err != nil || s.Message() != "1 2 9 2" {
		t.Fatalf("%v %q", err, s.Message())
	}
}

func TestRequireOptionsAndCollisions(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"text-tools.scm": `(define-module text-tools (export wrap join))
(define (wrap) "wrap")
(define (join) "join")
`,
		"md-tools.scm": `(define-module md-tools (export wrap))
(define (wrap) "md")
`,
	})
	ok := map[string]string{
		"twice": `
(require text-tools)
(require text-tools)
(require 'text-tools)
(define-command "use" (lambda () (message (wrap))))`,
		"prefix and only, only first": `
(require text-tools (only wrap) (prefix t:))
(define-command "use" (lambda () (message (t:wrap))))`,
		"prefix and only, prefix first": `
(require text-tools (prefix t:) (only t:wrap))
(define-command "use" (lambda () (message (t:wrap))))`,
		"except": `
(require text-tools (except wrap))
(define-command "use" (lambda () (message (join))))`,
		"rename": `
(require text-tools (rename wrap surround))
(define-command "use" (lambda () (message (surround))))`,
		"two modules with a prefix": `
(require text-tools)
(require md-tools (prefix md:))
(define-command "use" (lambda () (message (string-append (wrap) (md:wrap)))))`,
	}
	want := map[string]string{
		"twice": "wrap", "prefix and only, only first": "wrap", "prefix and only, prefix first": "wrap",
		"except": "join", "rename": "wrap", "two modules with a prefix": "wrapmd",
	}
	for name, program := range ok {
		r := loaded(t, Options{ConfigDir: dir}, program)
		s := session("x", 0, 0, nil, false)
		if err := r.Run(context.Background(), "use", nil, s); err != nil || s.Message() != want[name] {
			t.Errorf("%s: %v %q", name, err, s.Message())
		}
	}
	bad := map[string]string{
		`(require text-tools) (require md-tools)`:               "wrap is exported by text-tools and md-tools",
		`(require text-tools (rename wrap car))`:                "car is a built-in name",
		`(require text-tools) (define (wrap) 1)`:                "wrap is defined in init.scm and also imported from text-tools",
		`(require text-tools (prefix t:) (only wrap))`:          "does not export wrap",
		`(require text-tools (only gone))`:                      "does not export gone",
		`(require text-tools (except gone))`:                    "does not export gone",
		`(require text-tools (only wrap wrap))`:                 "wrap is listed twice",
		`(require text-tools (only wrap) (except join))`:        "use only or except, not both",
		`(require "text-tools")`:                                "require takes a module name",
		`(require Foo)`:                                         "not a module name",
		`(require ../x)`:                                        "not a module name",
		`(require init)`:                                        "reserved module name",
		`(require coigue)`:                                      "reserved module name",
		`(require text-tools (prefix))`:                         "prefix takes one symbol",
		`(require missing)`:                                     "not found at",
		`(define-module text-tools (export wrap))`:              "cannot start with define-module",
		`(define x 1) (define-module text-tools (export wrap))`: "belongs in modules/NAME.scm",
	}
	for program, want := range bad {
		err := New(Options{ConfigDir: dir}).Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", program, err, want)
		}
	}
	if err := New(Options{}).Load(context.Background(), `(require text-tools)`); err == nil || !strings.Contains(err.Error(), "no modules directory") {
		t.Fatalf("no directory: %v", err)
	}
}

func TestModuleHeadersAndCycles(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"text-tools.scm": `(define-module other (export wrap))
(define (wrap) 1)
`,
		"bare.scm": `(define (wrap) 1)
`,
		"a.scm": `(define-module a (export x))
(require b)
(define x 1)
`,
		"b.scm": `(define-module b (export y))
(require a)
(define y 1)
`,
		"c.scm": `(define-module c (export x))
(require d)
(define x 1)
`,
		"d.scm": `(define-module d (export y))
(require e)
(define y 1)
`,
		"e.scm": `(define-module e (export z))
(require c)
(define z 1)
`,
		"dangling.scm": `(define-module dangling (export wrap missing))
(define (wrap) 1)
`,
		"twice.scm": `(define-module twice (export wrap wrap))
(define (wrap) 1)
`,
	})
	for program, want := range map[string]string{
		`(require text-tools)`: "defines the module other, but the file is text-tools.scm",
		`(require bare)`:       "must start with (define-module bare ...)",
		`(require a)`:          "module cycle: a -> b -> a",
		`(require c)`:          "module cycle: c -> d -> e -> c",
		`(require dangling)`:   "dangling exports missing, which it does not define",
		`(require twice)`:      "wrap is exported twice",
	} {
		err := New(Options{ConfigDir: dir}).Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", program, err, want)
		}
	}
	missing := filepath.Join(dir, "modules", "nope.scm")
	err := New(Options{ConfigDir: dir}).Load(context.Background(), `(require nope)`)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "modules", "dir.scm"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = New(Options{ConfigDir: dir}).Load(context.Background(), `(require dir)`)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory: %v", err)
	}
	big := filepath.Join(dir, "modules", "big.scm")
	if err := os.WriteFile(big, []byte(strings.Repeat(";", MaxInitSize+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	err = New(Options{ConfigDir: dir}).Load(context.Background(), `(require big)`)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("size: %v", err)
	}
}

func TestAtMostSixtyFourModules(t *testing.T) {
	files := map[string]string{}
	var requires strings.Builder
	for i := 0; i <= MaxModules; i++ {
		name := "m" + strings.Repeat("a", i%20) + string(rune('a'+i/20))
		files[name+".scm"] = "(define-module " + name + ")\n(define x 1)\n"
		requires.WriteString("(require " + name + ")\n")
	}
	dir := writeModules(t, files)
	err := New(Options{ConfigDir: dir}).Load(context.Background(), requires.String())
	if err == nil || !strings.Contains(err.Error(), "at most 64 modules") {
		t.Fatalf("error %v", err)
	}
	// One fewer loads.
	all := strings.Split(strings.TrimRight(requires.String(), "\n"), "\n")
	if err := New(Options{ConfigDir: dir}).Load(context.Background(), strings.Join(all[:MaxModules], "\n")); err != nil {
		t.Fatal(err)
	}
}

func TestCommandsAndKeysResolveAcrossModules(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"text-tools.scm": `(define-module text-tools (export wrap))
(define (wrap) "text")
(define-command "wrap" (lambda () (message "text")) "from text")
(define-command "only-text" (lambda () (message "only text")))
(bind-key "visual" "<A-w>" (lambda () (message "text key")) "parenthesize")
(bind-key "normal" "Z" (lambda () (message "text z")))
`,
		"md-tools.scm": `(define-module md-tools (export wrap))
(define (wrap) "md")
(define-command "wrap" (lambda () (message "md")) "from md")
(bind-key "visual" "<A-w>" (lambda () (message "md key")) "bold")
(define-theme "dusk" '(("keyword" . "#ff79c6")))
`,
		"chooser.scm": `(define-module chooser (export x))
(define x 1)
(set-theme "mocha")
`,
		"over.scm": `(define-module over (export x))
(define x 1)
(bind-key "normal" "x" (lambda () 1) "doc" #t)
`,
		"twice.scm": `(define-module twice)
(define-command "wrap" (lambda () 1))
(define-command "wrap" (lambda () 2))
`,
	})
	// Both define wrap and <A-w>. init.scm defines neither, so the command is
	// ambiguous and the key fails the load. The key is left unbound.
	err := New(Options{ConfigDir: dir}).Load(context.Background(), "(require text-tools) (require md-tools (prefix md:))")
	if err == nil || !strings.Contains(err.Error(), "visual <A-w> is bound by") || !strings.Contains(err.Error(), "left unbound") {
		t.Fatalf("clash: %v", err)
	}
	if !strings.Contains(err.Error(), "md-tools") || !strings.Contains(err.Error(), "text-tools") {
		t.Fatalf("clash should name both modules: %v", err)
	}
	r := New(Options{ConfigDir: dir})
	_ = r.Load(context.Background(), "(require text-tools) (require md-tools (prefix md:))")
	if _, ok := r.Binding("visual", key.Key{Code: 'w', Mods: key.Alt}); ok {
		t.Fatal("the clashing key was installed")
	}
	s := session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "wrap", nil, s); err == nil || !strings.Contains(err.Error(), "wrap is ambiguous: md-tools/wrap, text-tools/wrap") {
		t.Fatalf("ambiguous: %v", err)
	}
	s = session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "text-tools/wrap", nil, s); err != nil || s.Message() != "text" {
		t.Fatalf("qualified: %v %q", err, s.Message())
	}
	if err := r.Run(context.Background(), "only-text", nil, session("x", 0, 0, nil, false)); err != nil {
		t.Fatalf("a unique module command did not get the short name: %v", err)
	}

	// init.scm wins the short name and the key, and only it may override or choose the theme.
	r = loaded(t, Options{ConfigDir: dir}, `
(require text-tools)
(require md-tools (prefix md:))
(define-command "wrap" (lambda () (message "init")) "from init")
(bind-key "visual" "<A-w>" (lambda () (message "init key")) "mine")
(set-theme "md-tools/dusk")
`)
	s = session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "wrap", nil, s); err != nil || s.Message() != "init" {
		t.Fatalf("init's wrap: %v %q", err, s.Message())
	}
	s = session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "md-tools/wrap", nil, s); err != nil || s.Message() != "md" {
		t.Fatalf("displaced command: %v %q", err, s.Message())
	}
	s = session("x", 0, 0, nil, false)
	if err := r.Run(context.Background(), "init/wrap", nil, s); err != nil || s.Message() != "init" {
		t.Fatalf("init/wrap: %v %q", err, s.Message())
	}
	if _, ok := r.Binding("visual", key.Key{Code: 'w', Mods: key.Alt}); !ok {
		t.Fatal("init's key was not installed")
	}
	if _, ok := r.Binding("normal", key.Char('Z')); !ok {
		t.Fatal("a module key with no conflict was dropped")
	}
	if _, ok := r.themes.Parse("md-tools/dusk"); !ok {
		t.Fatal("the module theme was not qualified")
	}
	var notes string
	for _, m := range r.Modules() {
		if m.Name == "text-tools" {
			notes = strings.Join(m.Notes, "\n")
		}
	}
	for _, want := range []string{"command wrap is displaced by init.scm", "visual <A-w> is displaced by init.scm"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes %q, want %q", notes, want)
		}
	}
	for program, want := range map[string]string{
		`(require over)`:    "only init.scm may override a built-in key",
		`(require chooser)`: "only init.scm may choose the starting theme",
		`(require twice)`:   `the command "wrap" is already defined in twice`,
	} {
		err := New(Options{ConfigDir: dir}).Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", program, err, want)
		}
	}
}

func TestStartProceduresRunInLoadOrder(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"early.scm": `(define-module early)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "e")))
`,
		"late.scm": `(define-module late)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "l")))
`,
	})
	r := loaded(t, Options{ConfigDir: dir}, `
(require early)
(require late)
(on-start (lambda () (insert-at 1 (+ (string-length (buffer-text 1)) 1) "i")))
`)
	if got := strings.Join(r.StartOwners(), " "); got != "early late init" {
		t.Fatalf("owners %q", got)
	}
	// Each owner is its own invocation. Running them in order on one session
	// shows the order; the editor gives each its own session.
	s := session("x", 0, 0, nil, false)
	for _, owner := range r.StartOwners() {
		if err := r.RunStartOwner(context.Background(), owner, s); err != nil {
			t.Fatal(err)
		}
	}
	if s.Buffer.Text() != "xeli" {
		t.Fatalf("text %q", s.Buffer.Text())
	}
}

func TestAModuleErrorNamesTheFileAndEarlierCommandsStay(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"early.scm": `(define-module early (export x))
(define x 1)
(define-command "early" (lambda () (message "early")))
`,
		"bad.scm": `(define-module bad (export x))
(define x unbound-name)
`,
	})
	r := New(Options{ConfigDir: dir})
	err := r.Load(context.Background(), `(require early) (require bad) (define-command "late" car)`)
	if err == nil || !strings.Contains(err.Error(), "bad.scm: unbound variable: unbound-name") {
		t.Fatalf("error %v", err)
	}
	if cmds := r.Commands(); len(cmds) != 1 || cmds[0].Name != "early" {
		t.Fatalf("commands %+v", cmds)
	}
}

func TestTheDeadlineAndInterruptCoverARequiredModule(t *testing.T) {
	dir := writeModules(t, map[string]string{
		"early.scm": `(define-module early (export x))
(define x 1)
(define-command "early" (lambda () (message "early")))
`,
		"slow.scm": `(define-module slow (export x))
(define x 1)
(let loop () (loop))
`,
	})
	program := `(require early) (require slow) (define-command "late" car)`
	r := New(Options{ConfigDir: dir, Deadline: 50 * time.Millisecond})
	err := r.Load(context.Background(), program)
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Deadline {
		t.Fatalf("deadline: %v", err)
	}
	if cmds := r.Commands(); len(cmds) != 1 || cmds[0].Name != "early" {
		t.Fatalf("commands %+v", cmds)
	}
	r = New(Options{ConfigDir: dir, Watch: watcher(20 * time.Millisecond)})
	err = r.Load(context.Background(), program)
	if !errors.As(err, &re) || re.Stop != Interrupted {
		t.Fatalf("interrupt: %v", err)
	}
	if cmds := r.Commands(); len(cmds) != 1 || cmds[0].Name != "early" {
		t.Fatalf("after interrupt: %+v", cmds)
	}
}
