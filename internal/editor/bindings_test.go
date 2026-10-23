package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
)

// initEditor opens an editor on text and loads program as its init file,
// returning the path so a test can rewrite the file and :reload.
func initEditor(t *testing.T, text, program string) (*Editor, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coigue", "init.scm")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, path, program)
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, text)
	e.LoadInit(path)
	if e.Message != "" {
		t.Fatalf("loading the init file reported %q", e.Message)
	}
	return e, path
}

func alt(e *Editor, r rune) { e.Key(key.Key{Code: key.Code(r), Mods: key.Alt}, 22) }

const bindInit = `
(define (mark) (insert-text "*") (message "marked"))
(define (shout)
  (replace-selection (string-upcase (selection-text)))
  (message (string-append (mode) " " (number->string (cursor-column)))))
(bind-key "normal" "X" mark "mark the cursor")
(bind-key "normal" "<C-d>" (lambda () (insert-text "ctrl")) "ctrl")
(bind-key "normal" "<A-j>" (lambda () (insert-text "alt")) "alt")
(bind-key "visual" "U" shout "uppercase the selection")
`

func TestBindingsRunInNormalMode(t *testing.T) {
	e, _ := initEditor(t, "abc\ndef", bindInit)
	send(e, "lX")
	if e.Buffer.Text() != "a*bc\ndef" || cursor(e) != [2]int{0, 1} || e.Message != "marked" || e.Mode != ModeNormal {
		t.Fatalf("text %q cursor %v message %q", e.Buffer.Text(), cursor(e), e.Message)
	}
	// Ctrl and Alt keys, and a capital arriving with the Shift modifier, find their bindings too.
	ctrl(e, 'd')
	if e.Buffer.Text() != "actrl*bc\ndef" {
		t.Fatalf("Ctrl-D: %q", e.Buffer.Text())
	}
	alt(e, 'j')
	if e.Buffer.Text() != "aaltctrl*bc\ndef" {
		t.Fatalf("Alt-J: %q", e.Buffer.Text())
	}
	e.Key(key.Key{Code: 'X', Mods: key.Shift}, 22)
	if e.Buffer.Text() != "a*altctrl*bc\ndef" {
		t.Fatalf("shifted X: %q", e.Buffer.Text())
	}
	// Each binding was one undo step, and redo applies it again.
	before := e.Buffer.Text()
	undo(e)
	if e.Buffer.Text() == before || strings.Count(e.Buffer.Text(), "*") != 1 {
		t.Fatalf("undo: %q", e.Buffer.Text())
	}
	redo(e)
	if e.Buffer.Text() != before {
		t.Fatalf("redo: %q", e.Buffer.Text())
	}
}

func TestBindingWithoutMessageSaysWhichKeyRan(t *testing.T) {
	e, _ := initEditor(t, "abc", `(bind-key "normal" "X" (lambda () (line-count)))`)
	send(e, "X")
	if e.Message != "Ran binding X" {
		t.Fatalf("message %q", e.Message)
	}
	if text, _, _, revision := state(e); text != "abc" || revision != 0 {
		t.Fatalf("a binding that edits nothing changed the buffer: %q %d", text, revision)
	}
}

func TestVisualBindingGetsTheSelectionAndEndsIt(t *testing.T) {
	e, _ := initEditor(t, "héllo wörld\nsecond", bindInit)
	send(e, "lv3l")
	send(e, "U")
	if e.Buffer.Text() != "hÉLLO wörld\nsecond" || e.Mode != ModeNormal || e.Message != "visual 2" {
		t.Fatalf("text %q mode %v message %q", e.Buffer.Text(), e.Mode, e.Message)
	}
	if _, _, ok := e.Selection(0); ok {
		t.Fatal("the selection survived the binding")
	}
	undo(e)
	if e.Buffer.Text() != "héllo wörld\nsecond" {
		t.Fatalf("one undo should restore the text: %q", e.Buffer.Text())
	}
	// A linewise selection is whole lines, and the mode says so.
	e, _ = initEditor(t, "one\ntwo\nthree", bindInit+`(bind-key "visual" "I" (lambda () (message (format "~a ~s" (mode) (selection-range)))))`)
	send(e, "jVjI")
	if e.Message != "visual-line (2 1 3 5)" {
		t.Fatalf("message %q", e.Message)
	}
}

func TestNormalBindingDoesNotSeeAVisualSelectionAndTheOtherWayRound(t *testing.T) {
	e, _ := initEditor(t, "abc", bindInit)
	send(e, "vlX") // X is bound in normal mode only: in Visual it is just an unknown key
	if e.Mode != ModeVisual || e.Buffer.Text() != "abc" {
		t.Fatalf("a normal-mode binding ran in Visual mode: mode %v text %q", e.Mode, e.Buffer.Text())
	}
	press(e, key.Esc)
	send(e, "U")
	if e.Buffer.Text() != "abc" {
		t.Fatalf("a visual-mode binding ran in Normal mode: %q", e.Buffer.Text())
	}
}

func TestBindingsStayOutOfTheWayOfTypingAndPendingKeys(t *testing.T) {
	e, _ := initEditor(t, "abc", `(bind-key "normal" "X" (lambda () (insert-text "*")))`)
	// Insert mode types the character.
	send(e, "iX")
	press(e, key.Esc)
	if e.Buffer.Text() != "Xabc" {
		t.Fatalf("Insert mode: %q", e.Buffer.Text())
	}
	// The search prompt and the command line take it as text.
	send(e, "/X")
	if e.Search != "X" {
		t.Fatalf("search %q", e.Search)
	}
	press(e, key.Esc)
	send(e, ":X")
	if e.Command != "X" || e.Buffer.Text() != "Xabc" {
		t.Fatalf("command %q text %q", e.Command, e.Buffer.Text())
	}
	press(e, key.Esc)
	// A pending operator or replacement keeps its meaning: the key cancels the operator, or is the replacement.
	send(e, "dX")
	if e.Buffer.Text() != "Xabc" || e.pending != 0 {
		t.Fatalf("after dX: %q pending %q", e.Buffer.Text(), e.pending)
	}
	send(e, "rX")
	if e.Buffer.Text() != "Xabc" {
		t.Fatalf("after rX: %q", e.Buffer.Text())
	}
	send(e, "lrX")
	if e.Buffer.Text() != "XXbc" {
		t.Fatalf("rX replaces with X, it does not run the binding: %q", e.Buffer.Text())
	}
	// A typed count is dropped, and the binding runs once.
	send(e, "3X")
	if e.Buffer.Text() != "X*Xbc" || e.count != 0 {
		t.Fatalf("after 3X: %q count %d", e.Buffer.Text(), e.count)
	}
}

func TestBindingsDoNotRunOnTheWelcomeScreenOrInOverlays(t *testing.T) {
	e := newWelcome(t, t.TempDir())
	path := filepath.Join(t.TempDir(), "init.scm")
	write(t, path, `(bind-key "normal" "X" (lambda () (insert-text "*")))`)
	e.LoadInit(path)
	send(e, "X")
	if e.Message != "" || !e.IsWelcome() {
		t.Fatalf("message %q", e.Message)
	}
	e, _ = initEditor(t, "abc", `(bind-key "normal" "X" (lambda () (insert-text "*")))`)
	press(e, key.F(1))
	send(e, "X")
	press(e, key.Esc)
	if e.Buffer.Text() != "abc" {
		t.Fatalf("a binding ran under the help overlay: %q", e.Buffer.Text())
	}
}

func TestBuiltInKeysNeedOverrideAndNeverBoundKeysCannotBeOverridden(t *testing.T) {
	load := func(program string) string {
		e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
		path := filepath.Join(t.TempDir(), "init.scm")
		write(t, path, program)
		e.LoadInit(path)
		return e.Message
	}
	for program, want := range map[string]string{
		`(bind-key "normal" "x" car)`:                                         `"x" already has a meaning in normal mode (it is a Normal command); pass #t as the override argument`,
		`(bind-key "normal" "3" car)`:                                         "digits are counts",
		`(bind-key "normal" "w" car)`:                                         "it is a motion",
		`(bind-key "normal" "<C-r>" car)`:                                     "Ctrl-R redoes",
		`(bind-key "normal" "<C-a>" car)`:                                     "Ctrl-A moves like Home",
		`(bind-key "visual" "<C-f>" car)`:                                     "Ctrl-F moves like",
		`(bind-key "visual" "y" car)`:                                         `"y" already has a meaning in visual mode (it is a Visual command)`,
		`(bind-key "visual" "d" car)`:                                         "it is a Visual command",
		`(bind-key "visual" "j" car)`:                                         "it is a motion",
		`(bind-key "normal" ":" car "" #t)`:                                   "cannot be bound in normal mode: it opens the command line",
		`(bind-key "visual" ":" car "" #t)`:                                   "cannot be bound in visual mode: it opens the command line",
		`(bind-key "visual" "!" car "" #t)`:                                   "cannot be bound in visual mode: it opens the command line with the selection",
		`(bind-key "normal" "<C-o>" car "" #t)`:                               "Ctrl-O opens the file explorer",
		`(bind-key "normal" "<C-p>" car "" #t)`:                               "Ctrl-P opens the file picker",
		`(bind-key "visual" "<C-o>" car "" #t)`:                               "Ctrl-O opens the file explorer",
		`(bind-key "normal" "<Esc>" car "" #t)`:                               "Escape cannot be bound",
		`(bind-key "normal" "<C-c>" car "" #t)`:                               "Ctrl-C interrupts",
		`(bind-key "normal" "<C-s>" car "" #t)`:                               "stop and start",
		`(bind-key "normal" "x" car "" #t) (bind-key "normal" "x" cdr "" #t)`: "normal x is already bound by binding 1",
	} {
		got := load(program)
		if !strings.Contains(got, want) {
			t.Errorf("%s:\nmessage %q, want one containing %q", program, got, want)
		}
	}
	// Free keys need no override, and the keys that have a meaning only in the other mode are free here.
	for _, program := range []string{
		`(bind-key "normal" "Q" car)`, `(bind-key "normal" "<C-g>" car)`, `(bind-key "normal" "<A-x>" car)`,
		`(bind-key "visual" "x" car)`, `(bind-key "visual" "<C-r>" car)`, `(bind-key "visual" "s" car)`,
		`(bind-key "normal" "Y" car)`, `(bind-key "normal" "界" car)`, `(bind-key "normal" "?" car)`,
	} {
		if got := load(program); got != "" {
			t.Errorf("%s: message %q", program, got)
		}
	}
}

func TestOverrideShadowsTheBuiltInMeaning(t *testing.T) {
	e, _ := initEditor(t, "abc\ndef", `
(bind-key "normal" "x" (lambda () (insert-text "<x>")) "x inserts" #t)
(bind-key "normal" "<C-r>" (lambda () (insert-text "<redo>")) "no redo" #t)
(bind-key "visual" "y" (lambda () (message (selection-text))) "show it" #t)
(bind-key "normal" "<C-a>" (lambda () (message "ctrl-a")) "" #t)
`)
	send(e, "x")
	if e.Buffer.Text() != "<x>abc\ndef" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	ctrl(e, 'r')
	if !strings.HasPrefix(e.Buffer.Text(), "<redo>") {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	ctrl(e, 'a')
	if e.Message != "ctrl-a" {
		t.Fatalf("message %q", e.Message)
	}
	// The built-in meaning is gone only where it was overridden: dd still deletes and Visual d still cuts.
	send(e, "dd")
	if e.Buffer.Text() != "def" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	send(e, "vly")
	if e.Message != "de" || e.register != nil && len(e.register.chars) > 0 {
		t.Fatalf("message %q register %v", e.Message, e.register)
	}
}

func TestFailedBindingChangesNothing(t *testing.T) {
	e, _ := initEditor(t, "abc\ndef", `
(bind-key "normal" "X" (lambda () (insert-text "partial") (set-cursor 2 1) (error "boom")))
(bind-key "visual" "B" (lambda () (replace-selection "gone") (error "visual boom")))
`)
	send(e, "l")
	text, row, col, revision := state(e)
	send(e, "X")
	if e.Message != "binding X failed: boom" {
		t.Fatalf("message %q", e.Message)
	}
	if t2, r2, c2, rev2 := state(e); t2 != text || r2 != row || c2 != col || rev2 != revision {
		t.Fatalf("state changed: %q (%d,%d) %d", t2, r2, c2, rev2)
	}
	undo(e)
	if e.Message != "Nothing to undo" {
		t.Fatalf("a failed binding left an undo step: %q", e.Message)
	}
	// A failing visual binding still ends the selection, as a failing command does.
	send(e, "vlB")
	if e.Message != "binding B failed: visual boom" || e.Mode != ModeNormal || e.Buffer.Text() != text {
		t.Fatalf("message %q mode %v text %q", e.Message, e.Mode, e.Buffer.Text())
	}
}

func TestRunawayBindingIsStoppedAndTheEditorStaysUsable(t *testing.T) {
	e2 := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	e2.extDeadline = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "init.scm")
	write(t, path, `(bind-key "normal" "X" (lambda () (insert-text "x") (let loop () (loop))))`)
	e2.LoadInit(path)
	send(e2, "X")
	if e2.Message != "binding X stopped after 50ms; no changes made" || e2.Buffer.Text() != "abc" {
		t.Fatalf("message %q text %q", e2.Message, e2.Buffer.Text())
	}
	send(e2, "ix")
	if e2.Buffer.Text() != "xabc" {
		t.Fatalf("text %q", e2.Buffer.Text())
	}
}

// snapshot is everything a key press could change that this test can see.
type snapshot struct {
	mode                       Mode
	text, message, search, cmd string
	row, col                   int
	pending                    rune
	count                      int
	anchor                     [2]int
	hasAnchor                  bool
	overlay                    bool
	register                   bool
	revision                   uint64
}

func snap(e *Editor) snapshot {
	s := snapshot{
		mode: e.Mode, text: e.Buffer.Text(), message: e.Message, search: e.Search, cmd: e.Command,
		row: e.Buffer.Row, col: e.Buffer.Col, pending: e.pending, count: e.count,
		overlay:  e.Explorer != nil || e.Picker != nil || e.Help != nil || e.Menu != nil || e.Locations != nil || e.SwitchPrompt != "",
		register: e.register != nil, revision: e.Buffer.Revision(),
	}
	if e.selectionAnchor != nil {
		s.anchor, s.hasAnchor = *e.selectionAnchor, true
	}
	return s
}

// keysToTry are all the keys a binding can name: every printable ASCII
// character, a handful of others, Ctrl and a letter, and Alt and a character.
func keysToTry() []key.Key {
	var keys []key.Key
	for r := rune('!'); r <= '~'; r++ {
		keys = append(keys, key.Char(r), key.Key{Code: key.Code(r), Mods: key.Alt})
	}
	for _, r := range "é界" {
		keys = append(keys, key.Char(r), key.Key{Code: key.Code(r), Mods: key.Alt})
	}
	for r := rune('a'); r <= 'z'; r++ {
		keys = append(keys, key.WithCtrl(r))
	}
	return keys
}

// editorIn returns an editor with the cursor in the middle of some text, in
// Visual mode with a selection when visual is set.
func editorIn(t *testing.T, visual bool) *Editor {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "alpha beta gamma\ndelta epsilon zeta\neta theta iota")
	send(e, "j3l")
	if visual {
		send(e, "vll")
	}
	e.Message = ""
	return e
}

// TestUnlistedKeysDoNothing keeps builtinMeaning and neverBound honest. A key
// they do not list is one a binding may take without override, so it must
// have no effect of its own in that mode: if a change to the handlers gives
// one a meaning, this fails until the lists say so.
func TestUnlistedKeysDoNothing(t *testing.T) {
	for _, mode := range []string{ext.ModeNormal, ext.ModeVisual} {
		for _, k := range keysToTry() {
			if neverBound(mode, k) != "" || builtinMeaning(mode, k) != "" || strings.Contains(keyName(k), "\x00") {
				continue
			}
			if k.Mods == key.Ctrl && strings.ContainsRune("czsqimjh", rune(k.Code)) {
				continue // key.ParseSpec refuses these, so no binding can name them
			}
			e := editorIn(t, mode == ext.ModeVisual)
			before := snap(e)
			e.Key(k, 22)
			if after := snap(e); after != before {
				t.Errorf("%s mode, %s: has an effect no list mentions:\n before %+v\n after  %+v", mode, keyName(k), before, after)
			}
		}
	}
}

// TestListedKeysDoSomething is the other half: a key the lists claim has a
// meaning must change something, or override would be asked for needlessly.
func TestListedKeysDoSomething(t *testing.T) {
	for _, mode := range []string{ext.ModeNormal, ext.ModeVisual} {
		for _, k := range keysToTry() {
			if neverBound(mode, k) != "" || builtinMeaning(mode, k) == "" {
				continue
			}
			e := editorIn(t, mode == ext.ModeVisual)
			before := snap(e)
			e.Key(k, 22)
			if after := snap(e); after == before {
				t.Errorf("%s mode, %s: listed as meaningful but nothing happened", mode, keyName(k))
			}
		}
	}
}

func TestNeverBoundKeysKeepTheirMeaning(t *testing.T) {
	for _, c := range []struct {
		mode string
		k    key.Key
		want func(*Editor) bool
	}{
		{ext.ModeNormal, key.Char(':'), func(e *Editor) bool { return e.Mode == ModeCommand }},
		{ext.ModeNormal, key.WithCtrl('o'), func(e *Editor) bool { return e.Explorer != nil }},
		{ext.ModeNormal, key.WithCtrl('p'), func(e *Editor) bool { return e.Picker != nil }},
		{ext.ModeVisual, key.Char('!'), func(e *Editor) bool { return e.Mode == ModeCommand && e.commandVisual != 0 }},
	} {
		e := editorIn(t, c.mode == ext.ModeVisual)
		e.Key(c.k, 22)
		if !c.want(e) {
			t.Errorf("%s %s: lost its meaning", c.mode, keyName(c.k))
		}
	}
}

func TestHelpListsBindingsNextToCommands(t *testing.T) {
	e, _ := initEditor(t, "abc", wrapInit+bindInit)
	press(e, key.F(1))
	for e.Help.Topic != help.Extensions {
		press(e, key.Tab)
	}
	text := strings.Join(e.Help.Lines(), "\n")
	for _, want := range []string{
		":wrap                Wrap the selection in OPEN and CLOSE",
		"normal X             mark the cursor",
		"normal <C-d>         ctrl",
		"normal <A-j>         alt",
		"visual U             uppercase the selection",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the guide does not say %q:\n%s", want, text)
		}
	}
	// Bindings alone get a topic too.
	e, _ = initEditor(t, "abc", `(bind-key "normal" "X" car "just a key")`)
	press(e, key.F(1))
	for e.Help.Topic != help.Extensions {
		press(e, key.Tab)
	}
	if text := strings.Join(e.Help.Lines(), "\n"); !strings.Contains(text, "normal X             just a key") || strings.Contains(text, "Commands defined") {
		t.Errorf("the guide says:\n%s", text)
	}
}

// Without an init file nothing in the key bindings is visible: no extension
// runtime, and every key reaches its handler.
func TestBindingsDoNotMoveTheGoldenPaths(t *testing.T) {
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	if e.extensions != nil || e.runBinding(key.Char('x')) {
		t.Fatal("an editor without init.scm has a binding path")
	}
}
