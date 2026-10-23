package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/theme"
)

func TestStartProcedureRunsOnceWhenTheFirstFileIsOpen(t *testing.T) {
	e, path := initEditor(t, "abc", `(on-start (lambda () (insert-text (string-append (mode) ":")) (message "started")))`)
	e.RunStart()
	if e.Buffer.Text() != "normal:abc" || e.Message != "started" {
		t.Fatalf("text %q message %q", e.Buffer.Text(), e.Message)
	}
	undo(e)
	if e.Buffer.Text() != "abc" {
		t.Fatalf("the start procedure is not one undo step: %q", e.Buffer.Text())
	}
	e.RunStart() // a second call, as app would never make, does nothing
	if e.Buffer.Text() != "abc" {
		t.Fatalf("ran twice: %q", e.Buffer.Text())
	}
	// :reload does not run it again, whatever the new file says.
	write(t, path, `(on-start (lambda () (insert-text "again")))`)
	command(e, "reload")
	e.RunStart()
	if e.Buffer.Text() != "abc" || !strings.HasPrefix(e.Message, "Reloaded init.scm") {
		t.Fatalf("text %q message %q", e.Buffer.Text(), e.Message)
	}
}

func TestStartProcedureWaitsForAFileOnTheWelcomeScreen(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.txt"), filepath.Join(dir, "second.txt")
	write(t, first, "one")
	write(t, second, "two")
	e := newWelcome(t, dir)
	initPath := filepath.Join(t.TempDir(), "init.scm")
	write(t, initPath, `(on-start (lambda () (insert-text (string-append (buffer-path) " "))))`)
	e.LoadInit(initPath)
	e.RunStart()
	if !e.IsWelcome() || e.startDone {
		t.Fatal("the start procedure ran with no file open")
	}
	e.openPath(first)
	if want := canonical(t, first) + " one"; e.Buffer.Text() != want {
		t.Fatalf("text %q, want %q", e.Buffer.Text(), want)
	}
	e.openPath(second)
	if e.Buffer.Text() != "two" {
		t.Fatalf("it ran again for the second file: %q", e.Buffer.Text())
	}
}

func TestStartProcedureFailureIsReportedAndBlocksNothing(t *testing.T) {
	e, _ := initEditor(t, "abc", `(on-start (lambda () (insert-text "x") (error "no luck")))`)
	e.RunStart()
	if e.Message != "on-start failed: no luck" || e.Buffer.Text() != "abc" {
		t.Fatalf("message %q text %q", e.Buffer.Text(), e.Message)
	}
	send(e, "ix")
	if e.Buffer.Text() != "xabc" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	// A start procedure with nothing to say leaves what init.scm reported alone.
	e = newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	path := filepath.Join(t.TempDir(), "init.scm")
	write(t, path, `(on-start (lambda () (line-count))) (car 1)`)
	e.LoadInit(path)
	first := e.Message
	e.RunStart()
	if first == "" || e.Message != first {
		t.Fatalf("message %q then %q", first, e.Message)
	}
}

func TestStartProcedureHonoursTheDeadline(t *testing.T) {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	e.extDeadline = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "init.scm")
	write(t, path, `(on-start (lambda () (insert-text "x") (let loop () (loop))))`)
	e.LoadInit(path)
	e.RunStart()
	if e.Message != "on-start stopped after 50ms; no changes made" || e.Buffer.Text() != "abc" {
		t.Fatalf("message %q text %q", e.Message, e.Buffer.Text())
	}
}

func TestReloadReplacesCommandsThemesAndBindingsAsAUnit(t *testing.T) {
	e, path := initEditor(t, "abc", `
(define-command "one" (lambda () (message "one")))
(define-command "two" (lambda () (message "two")))
(define-theme "dusk" '(("keyword" . "#ff79c6")))
(set-theme "dusk")
(bind-key "normal" "X" (lambda () (message "old X")))
(bind-key "normal" "Y" (lambda () (message "old Y")))
`)
	if e.Themes.Name(e.Theme) != "dusk" {
		t.Fatalf("theme %q", e.Themes.Name(e.Theme))
	}
	write(t, path, `
(define-command "two" (lambda () (message "new two")))
(define-command "three" (lambda () (message "three")))
(define-theme "dawn" '(("base" . "latte")))
(bind-key "normal" "X" (lambda () (message "new X")))
(bind-key "normal" "Z" (lambda () (message "new Z")))
`)
	command(e, "reload")
	if e.Message != "Reloaded init.scm: 2 commands, 2 key bindings; theme dusk is gone, using catppuccin-mocha" {
		t.Fatalf("message %q", e.Message)
	}
	if _, ok := e.commands.lookup("one"); ok {
		t.Error(":one is still there")
	}
	for name, want := range map[string]string{"two": "new two", "three": "three"} {
		command(e, name)
		if e.Message != want {
			t.Errorf(":%s says %q, want %q", name, e.Message, want)
		}
	}
	for k, want := range map[string]string{"X": "new X", "Z": "new Z"} {
		send(e, k)
		if e.Message != want {
			t.Errorf("%s says %q, want %q", k, e.Message, want)
		}
	}
	send(e, "Y")
	if e.Message == "old Y" {
		t.Error("Y kept its old binding")
	}
	if _, ok := e.Themes.Parse("dusk"); ok {
		t.Error("the old theme is still defined")
	}
	if _, ok := e.Themes.Parse("dawn"); !ok {
		t.Error("the new theme is missing")
	}
	// One command, once: the table has no stale or duplicate entries.
	count := 0
	for _, spec := range e.commands.specs {
		if spec.name == "two" {
			count++
		}
	}
	if count != 1 {
		t.Errorf(":two is in the table %d times", count)
	}
}

func TestReloadKeepsTheThemeYouSwitchedToWhenItStillExists(t *testing.T) {
	e, path := initEditor(t, "abc", `(define-theme "dusk" '())`)
	command(e, "theme dusk")
	write(t, path, `(define-theme "dusk" '(("keyword" . "#ff79c6"))) (define-command "x" car)`)
	command(e, "reload")
	if e.Themes.Name(e.Theme) != "dusk" || e.Palette() == theme.CatppuccinMocha.Palette() {
		t.Fatalf("theme %q", e.Themes.Name(e.Theme))
	}
	command(e, "theme gruvbox")
	write(t, path, `(define-command "x" car)`)
	command(e, "reload")
	if e.Themes.Name(e.Theme) != "gruvbox-dark" {
		t.Fatalf("a built-in theme was lost: %q", e.Themes.Name(e.Theme))
	}
	// set-theme in the file wins over a :theme typed earlier: the file is read as at start-up.
	write(t, path, `(set-theme "latte")`)
	command(e, "reload")
	if e.Themes.Name(e.Theme) != "catppuccin-latte" {
		t.Fatalf("theme %q", e.Themes.Name(e.Theme))
	}
}

func TestFailedReloadLeavesEverythingAsItWas(t *testing.T) {
	program := `
(define-command "keep" (lambda () (message "kept")))
(define-theme "dusk" '())
(set-theme "dusk")
(bind-key "normal" "X" (lambda () (message "old X")))
`
	e, path := initEditor(t, "abc", program)
	for name, broken := range map[string]string{
		"a syntax error":       `(define-command "new" (lambda () 1)`,
		"a scheme error":       `(define-command "new" (lambda () 1)) (car 1)`,
		"a built-in command":   `(define-command "new" car) (define-command "w" car)`,
		"a refused key":        `(define-command "new" car) (bind-key "normal" "x" car)`,
		"a duplicate binding":  `(bind-key "normal" "Q" car "a") (bind-key "normal" "Q" cdr "b")`,
		"a bad theme":          `(define-theme "other" '(("keyword" . "pink")))`,
		"a second start":       `(on-start car) (on-start cdr)`,
		"the load command":     `(load "other.scm")`,
		"a redefined theme":    `(define-theme "dusk2" '()) (define-theme "dusk2" '())`,
		"too many definitions": strings.Repeat(`(define-command "same" car)`, 2),
	} {
		write(t, path, broken)
		text, row, col, revision := state(e)
		themeBefore := e.Theme
		command(e, "reload")
		if !strings.HasPrefix(e.Message, "init.scm: ") || !strings.HasSuffix(e.Message, "; nothing changed") {
			t.Errorf("%s: message %q", name, e.Message)
		}
		if t2, r2, c2, rev2 := state(e); t2 != text || r2 != row || c2 != col || rev2 != revision || e.Theme != themeBefore {
			t.Errorf("%s: the editor changed", name)
		}
		command(e, "keep")
		send(e, "X")
		if e.Message != "old X" {
			t.Errorf("%s: the old binding is gone: %q", name, e.Message)
		}
		command(e, "keep")
		if e.Message != "kept" {
			t.Errorf("%s: the old command is gone: %q", name, e.Message)
		}
		if _, ok := e.commands.lookup("new"); ok {
			t.Errorf("%s: a command of the failed file leaked in", name)
		}
		if _, ok := e.Themes.Parse("dusk"); !ok || e.Themes.Name(e.Theme) != "dusk" {
			t.Errorf("%s: the old theme is gone", name)
		}
	}
	// And the file, once fixed, loads: the failed attempts did not poison the editor.
	write(t, path, program+`(define-command "added" (lambda () (message "added")))`)
	command(e, "reload")
	command(e, "added")
	if e.Message != "added" {
		t.Fatalf("message %q", e.Message)
	}
}

func TestReloadFailureMessagesSayWhy(t *testing.T) {
	e, path := initEditor(t, "abc", `(define-command "keep" car)`)
	write(t, path, `(define-command "x" car) (error "bad file" 7)`)
	command(e, "reload")
	if e.Message != `init.scm: failed: bad file 7; nothing changed` {
		t.Fatalf("message %q", e.Message)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	command(e, "reload")
	if !strings.Contains(e.Message, "not found at ") || !strings.HasSuffix(e.Message, "; nothing changed") {
		t.Fatalf("message %q", e.Message)
	}
	if _, ok := e.commands.lookup("keep"); !ok {
		t.Error("deleting the file removed the commands")
	}
	write(t, path, strings.Repeat("; padding\n", ext.MaxInitSize/10+1))
	command(e, "reload")
	if !strings.HasPrefix(e.Message, "init.scm: ") || !strings.HasSuffix(e.Message, "; nothing changed") {
		t.Fatalf("message %q", e.Message)
	}
}

func TestReloadDeadlineAndNoLocation(t *testing.T) {
	e, path := initEditor(t, "abc", `(define-command "keep" car)`)
	e.extDeadline = 50 * time.Millisecond
	write(t, path, `(define-command "spin" car) (let loop () (loop))`)
	command(e, "reload")
	if e.Message != "init.scm: stopped after 50ms; nothing changed" {
		t.Fatalf("message %q", e.Message)
	}
	if _, ok := e.commands.lookup("spin"); ok {
		t.Error("a command of the file that timed out leaked in")
	}
	plain := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	command(plain, "reload")
	if !strings.HasPrefix(plain.Message, "init.scm: no place for it") {
		t.Fatalf("message %q", plain.Message)
	}
}

func TestReloadCreatesTheFirstInit(t *testing.T) {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	path := filepath.Join(t.TempDir(), "coigue", "init.scm")
	e.LoadInit(path) // no file yet: normal, and silent
	if e.Message != "" {
		t.Fatalf("message %q", e.Message)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, path, `(define-command "hello" (lambda () (message "hi"))) (bind-key "normal" "X" (lambda () (message "X!")))`)
	command(e, "reload")
	if e.Message != "Reloaded init.scm: 1 command, 1 key binding" {
		t.Fatalf("message %q", e.Message)
	}
	command(e, "hello")
	send(e, "X")
	if e.Message != "X!" {
		t.Fatalf("message %q", e.Message)
	}
}

func TestReloadCompletesOnlyOnceThereIsAnInit(t *testing.T) {
	plain := newWelcome(t, t.TempDir())
	send(plain, ":rel")
	press(plain, key.Tab)
	if plain.Command != "rel" {
		t.Fatalf("an editor without an init file completes :reload: %q", plain.Command)
	}
	for _, c := range plain.commands.specs {
		if c.name == "reload" && !c.unlisted {
			t.Fatal("reload is listed without an init file")
		}
	}
	e, _ := initEditor(t, "abc", `(define-command "x" car)`)
	send(e, ":rel")
	press(e, key.Tab)
	if e.Command != "reload" {
		t.Fatalf("command %q", e.Command)
	}
}
