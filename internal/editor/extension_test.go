package editor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/actions"
	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
)

const wrapInit = `
(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))
(define-command "wrap" wrap "Wrap the selection in OPEN and CLOSE")
`

// loadInit writes program as an init file and loads it into e.
func loadInit(t *testing.T, e *Editor, program string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coigue", "init.scm")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, path, program)
	e.LoadInit(path)
}

func extensionEditor(t *testing.T, text, program string) *Editor {
	t.Helper()
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, text)
	loadInit(t, e, program)
	if e.Message != "" {
		t.Fatalf("loading the init file reported %q", e.Message)
	}
	return e
}

// command types a command line and Enter: ":" in Normal mode, "!" in Visual
// mode, where ":" is ignored (as in the Rust editor) and "!" opens the same
// command line with the selection kept.
func command(e *Editor, line string) {
	opener := ":"
	if e.Mode == ModeVisual || e.Mode == ModeVisualLine {
		opener = "!"
	}
	send(e, opener+line)
	press(e, key.Enter)
}

func state(e *Editor) (text string, row, col int, revision uint64) {
	return e.Buffer.Text(), e.Buffer.Row, e.Buffer.Col, e.Buffer.Revision()
}

func TestExampleInit(t *testing.T) {
	program, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "init.scm"))
	if err != nil {
		t.Fatal(err)
	}
	e := extensionEditor(t, "héllo 界 wörld", string(program))
	send(e, "v4l")
	command(e, "wrap ( )")
	if e.Buffer.Text() != "(héllo) 界 wörld" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
}

func TestWrapCommand(t *testing.T) {
	e := extensionEditor(t, "héllo 界 wörld\nsecond", wrapInit)
	send(e, "6lv$") // 界 wörld, the multibyte tail of the line
	command(e, "wrap 「 」")
	if got := e.Buffer.Text(); got != "héllo 「界 wörld」\nsecond" {
		// $ in Visual mode selects up to the last character of the line.
		t.Fatalf("text %q", got)
	}
	if cursor(e) != [2]int{0, 6} || e.Mode != ModeNormal || e.Message != "Ran :wrap" {
		t.Fatalf("cursor %v mode %v message %q", cursor(e), e.Mode, e.Message)
	}
	if _, _, ok := e.Selection(0); ok {
		t.Fatal("the selection survived the command")
	}
	// The selection does not leak into the next command.
	command(e, "wrap ( )")
	if !strings.HasPrefix(e.Message, ":wrap failed: selection-text: no selection") || e.Buffer.Text() != "héllo 「界 wörld」\nsecond" {
		t.Fatalf("second wrap: %q, text %q", e.Message, e.Buffer.Text())
	}
}

func TestWrapLinewise(t *testing.T) {
	e := extensionEditor(t, "one\ntwo\nthree\nfour", `
(define (shout) (replace-selection (string-upcase (selection-text))))
(define-command "shout" shout)`)
	send(e, "jVj")
	command(e, "shout")
	if e.Buffer.Text() != "one\nTWO\nTHREE\nfour" || cursor(e) != [2]int{1, 0} {
		t.Fatalf("text %q cursor %v", e.Buffer.Text(), cursor(e))
	}
}

func TestWrapIsOneUndoStep(t *testing.T) {
	e := extensionEditor(t, "héllo 界 wörld", wrapInit)
	send(e, "x") // an earlier edit
	send(e, "v3l")
	command(e, "wrap ( )")
	if e.Buffer.Text() != "(éllo) 界 wörld" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "éllo 界 wörld" || cursor(e) != [2]int{0, 3} {
		t.Fatalf("one undo gave %q at %v", e.Buffer.Text(), cursor(e))
	}
	redo(e)
	if e.Buffer.Text() != "(éllo) 界 wörld" {
		t.Fatalf("one redo gave %q", e.Buffer.Text())
	}
	send(e, "uu")
	if e.Buffer.Text() != "héllo 界 wörld" {
		t.Fatalf("two undos gave %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Message != "Nothing to undo" {
		t.Fatalf("a third undo: %q", e.Message)
	}
}

func TestSeveralEditsOfOneCommandAreOneUndoStep(t *testing.T) {
	e := extensionEditor(t, "alpha\nbeta", `
(define (stamp)
  (insert-text "# ")
  (insert-text "one\n")
  (insert-text (format "~a lines" (line-count))))
(define-command "stamp" stamp)`)
	command(e, "stamp")
	// Each insert goes before the cursor character, and the cursor stays on
	// the first character inserted, so later inserts land in front of earlier.
	const after = "3 linesone\n# alpha\nbeta"
	if e.Buffer.Text() != after || cursor(e) != [2]int{0, 0} {
		t.Fatalf("text %q cursor %v", e.Buffer.Text(), cursor(e))
	}
	send(e, "u")
	if e.Buffer.Text() != "alpha\nbeta" {
		t.Fatalf("one undo gave %q", e.Buffer.Text())
	}
	redo(e)
	if e.Buffer.Text() != after {
		t.Fatalf("redo gave %q, want %q", e.Buffer.Text(), after)
	}
	send(e, "u")
	send(e, "u")
	if e.Message != "Nothing to undo" {
		t.Fatalf("the command took more than one undo step: %q", e.Message)
	}
}

func TestUnchangedResultLeavesNoUndoStep(t *testing.T) {
	e := extensionEditor(t, "abc", `
(define (same) (replace-selection (selection-text)))
(define-command "same" same)`)
	send(e, "xu") // leaves one redo entry
	send(e, "vl")
	command(e, "same")
	if e.Buffer.Text() != "abc" {
		t.Fatalf("text %q", e.Buffer.Text())
	}
	redo(e)
	if e.Message != "Redone" || e.Buffer.Text() != "bc" {
		t.Fatalf("redo after a no-op command: %q %q", e.Message, e.Buffer.Text())
	}
	send(e, "u")
	send(e, "u")
	if e.Message != "Nothing to undo" {
		t.Fatalf("the no-op command left an undo step: %q", e.Message)
	}
}

func TestFailuresChangeNothing(t *testing.T) {
	long := strings.Repeat("x", ext.MaxRegion+1)
	program := wrapInit + `
(define (boom) (error "boom" 1))
(define (unbound) (no-such-procedure))
(define (bad-utf8) (replace-selection "\xff"))
(define (wrong-type) (replace-selection 42))
(define (too-big) (replace-selection (selection-text)) (selection-text))
(define (edit-then-fail) (replace-selection "changed") (error "late failure"))
(define (loader) (load "/etc/hostname"))
(define-command "boom" boom)
(define-command "unbound" unbound)
(define-command "bad-utf8" bad-utf8)
(define-command "wrong-type" wrong-type)
(define-command "edit-then-fail" edit-then-fail)
(define-command "loader" loader)
`
	for _, c := range []struct {
		name, text, keys, line, message string
	}{
		{"scheme error", "héllo wörld", "v2l", "boom", ":boom failed: boom 1"},
		{"unbound variable", "héllo wörld", "v2l", "unbound", ":unbound failed: eval: unbound variable: no-such-procedure"},
		{"no selection", "héllo wörld", "", "wrap ( )", ":wrap failed: selection-text: no selection; select text with v or V, then press !"},
		{"wrong argument count", "héllo wörld", "v2l", "wrap (", ":wrap failed: wrap: expected 2 argument(s), got 1"},
		{"oversized selection", long, "v$", "wrap ( )", ":wrap failed: selection-text: selection is 65537 characters; the limit is 65536"},
		{"rejected edit", "héllo wörld", "v2l", "bad-utf8", ":bad-utf8 failed: replace-selection: text is not valid UTF-8"},
		{"wrong type", "héllo wörld", "v2l", "wrong-type", ":wrong-type failed: replace-selection: argument 1: expected string, got integer 42"},
		{"edits made before the failure", "héllo wörld", "v2l", "edit-then-fail", ":edit-then-fail failed: late failure"},
		{"load", "héllo wörld", "v2l", "loader", ":loader failed: load: denied: editor extensions cannot use operating-system operations"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := extensionEditor(t, c.text, program)
			send(e, "xu") // an undone edit: redo history must survive the failure
			text, row, col, revision := state(e)
			send(e, c.keys)
			cursorBefore := cursor(e)
			command(e, c.line)
			if e.Message != c.message {
				t.Fatalf("message %q, want %q", e.Message, c.message)
			}
			gotText, gotRow, gotCol, gotRevision := state(e)
			if gotText != text || gotRevision != revision {
				t.Fatalf("the live buffer was touched: text %q revision %d, want %q %d", gotText, gotRevision, text, revision)
			}
			if c.keys == "" && (gotRow != row || gotCol != col) {
				t.Fatalf("cursor moved to (%d,%d)", gotRow, gotCol)
			}
			if cursor(e) != cursorBefore || e.Mode != ModeNormal {
				t.Fatalf("cursor %v mode %v", cursor(e), e.Mode)
			}
			if _, _, ok := e.Selection(0); ok {
				t.Fatal("the selection survived the failed command")
			}
			redo(e)
			if e.Message != "Redone" || e.Buffer.Text() == text {
				t.Fatalf("the failure discarded the redo history: %q %q", e.Message, e.Buffer.Text())
			}
			// The editor is responsive: undo, then a command that works.
			send(e, "u")
			if e.Buffer.Text() != text {
				t.Fatalf("undo gave %q, want %q", e.Buffer.Text(), text)
			}
			send(e, "v")
			command(e, "wrap ( )")
			if e.Message != "Ran :wrap" || utf8.RuneCountInString(e.Buffer.Text()) != utf8.RuneCountInString(text)+2 {
				t.Fatalf("after the failure: %q, %d characters", e.Message, utf8.RuneCountInString(e.Buffer.Text()))
			}
		})
	}
}

func TestFailuresLeaveNoUndoStep(t *testing.T) {
	e := extensionEditor(t, "abc", wrapInit+`(define (boom) (insert-text "x") (error "boom")) (define-command "boom" boom)`)
	send(e, "v")
	command(e, "boom")
	send(e, "u")
	if e.Message != "Nothing to undo" || e.Buffer.Text() != "abc" {
		t.Fatalf("a failed command left an undo step: %q %q", e.Message, e.Buffer.Text())
	}
}

func TestBangInVisualModeKeepsTheSelectionForOneCommand(t *testing.T) {
	e := extensionEditor(t, "alpha beta", wrapInit)
	send(e, "v2l!")
	if e.Mode != ModeCommand {
		t.Fatalf("mode %v", e.Mode)
	}
	if start, end, ok := e.Selection(0); !ok || start != 0 || end != 3 {
		t.Fatalf("the highlight did not stay: %d %d %v", start, end, ok)
	}
	press(e, key.Esc)
	if e.Mode != ModeNormal || e.selectionAnchor != nil || e.commandVisual != 0 || e.Message != "" {
		t.Fatalf("Esc left mode %v anchor %v visual %v message %q", e.Mode, e.selectionAnchor, e.commandVisual, e.Message)
	}
	// A built-in command run from a selection drops the selection too.
	send(e, "v2l")
	command(e, "theme latte")
	if e.Message != "Theme: catppuccin-latte" || e.selectionAnchor != nil {
		t.Fatalf("message %q anchor %v", e.Message, e.selectionAnchor)
	}
	command(e, "wrap ( )")
	if !strings.Contains(e.Message, "no selection") {
		t.Fatalf("a dropped selection reached a later command: %q", e.Message)
	}
	// ":" is still ignored in Visual mode, as in the Rust editor.
	send(e, "v2l:")
	if e.Mode != ModeVisual {
		t.Fatalf("':' changed the mode to %v", e.Mode)
	}
	press(e, key.Esc)
	// Running a menu action from the suspended prompt also ends it.
	send(e, "v2l!")
	e.Key(key.Press(key.F(2)), 22)
	e.activate(actions.Action{Kind: actions.FirstLine})
	if e.commandVisual != 0 || e.selectionAnchor != nil || e.Mode != ModeNormal {
		t.Fatal("the menu action left the selection behind")
	}
}

func TestCompletionAndHelpTreatExtensionCommandsLikeBuiltIns(t *testing.T) {
	e := extensionEditor(t, "abc", wrapInit+`(define-command "wrapper" car) (define-command "zzz" car)`)
	send(e, ":wr")
	press(e, key.Tab)
	if e.Command != "wrap" {
		t.Fatalf("Tab completed to %q", e.Command)
	}
	press(e, key.Tab)
	if e.Command != "wrapper" {
		t.Fatalf("second Tab completed to %q", e.Command)
	}
	press(e, key.Esc)
	send(e, ":z")
	press(e, key.Tab)
	if e.Command != "zzz" {
		t.Fatalf("Tab completed to %q", e.Command)
	}
	press(e, key.Esc)
	cmds := e.ExtensionCommands()
	if len(cmds) != 3 || cmds[0].Name != "wrap" || cmds[0].Doc != "Wrap the selection in OPEN and CLOSE" {
		t.Fatalf("ExtensionCommands() = %+v", cmds)
	}
}

func TestLoadInit(t *testing.T) {
	newE := func(t *testing.T) *Editor {
		return newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	}
	t.Run("a missing file is normal", func(t *testing.T) {
		e := newE(t)
		e.LoadInit(filepath.Join(t.TempDir(), "coigue", "init.scm"))
		if e.Message != "" || e.extensions != nil {
			t.Fatalf("message %q, runtime %v", e.Message, e.extensions)
		}
		command(e, "wrap ( )")
		if e.Message != "Unknown command: wrap ( )" {
			t.Fatalf("message %q", e.Message)
		}
	})
	t.Run("a syntax error is reported", func(t *testing.T) {
		e := newE(t)
		loadInit(t, e, `(define-command "x" car`)
		if !strings.HasPrefix(e.Message, "init.scm: ") {
			t.Fatalf("message %q", e.Message)
		}
		send(e, "ix")
		if e.Buffer.Text() != "xabc" {
			t.Fatal("the editor is not usable")
		}
	})
	t.Run("commands defined before an error stay", func(t *testing.T) {
		e := newE(t)
		loadInit(t, e, wrapInit+`(error "broken after wrap")`+`(define-command "late" car)`)
		if e.Message != "init.scm: failed: broken after wrap" {
			t.Fatalf("message %q", e.Message)
		}
		if cmds := e.ExtensionCommands(); len(cmds) != 1 || cmds[0].Name != "wrap" {
			t.Fatalf("commands %+v", cmds)
		}
		send(e, "v")
		command(e, "wrap ( )")
		if e.Buffer.Text() != "(a)bc" {
			t.Fatalf("text %q", e.Buffer.Text())
		}
	})
	t.Run("a built-in name is refused", func(t *testing.T) {
		e := newE(t)
		loadInit(t, e, `(define-command "theme" car) (define-command "w" car)`)
		if e.Message != `init.scm: failed: define-command: "theme" is a built-in command` {
			t.Fatalf("message %q", e.Message)
		}
	})
	t.Run("load is refused in the init file", func(t *testing.T) {
		e := newE(t)
		other := filepath.Join(t.TempDir(), "other.scm")
		write(t, other, `(define-command "smuggled" car)`)
		loadInit(t, e, `(load "`+other+`")`+`(define-command "after" car)`)
		if !strings.HasPrefix(e.Message, "init.scm: failed: load: denied") {
			t.Fatalf("message %q", e.Message)
		}
		if len(e.ExtensionCommands()) != 0 {
			t.Fatalf("commands %+v", e.ExtensionCommands())
		}
	})
	t.Run("an unreadable path is reported", func(t *testing.T) {
		e := newE(t)
		e.LoadInit(t.TempDir())
		if !strings.HasPrefix(e.Message, "init.scm: not a regular file") {
			t.Fatalf("message %q", e.Message)
		}
	})
	t.Run("a runaway init file does not stop start-up", func(t *testing.T) {
		e := newE(t)
		e.extDeadline = 50 * time.Millisecond
		start := time.Now()
		loadInit(t, e, wrapInit+`(let loop () (loop))`)
		if e.Message != "init.scm: stopped after 50ms" || time.Since(start) > 3*time.Second {
			t.Fatalf("message %q after %v", e.Message, time.Since(start))
		}
		if len(e.ExtensionCommands()) != 1 {
			t.Fatal("the command defined before the loop was lost")
		}
	})
}

func TestWelcomeScreenRefusesExtensionCommands(t *testing.T) {
	e := newWelcome(t, t.TempDir())
	loadInit(t, e, wrapInit)
	command(e, "wrap ( )")
	if e.Message != "No file open" {
		t.Fatalf("message %q", e.Message)
	}
}

func TestCommandsSurviveFileSwitchAndUseTheActiveBuffer(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	write(t, a, "first 界 file")
	write(t, b, "second é file")
	f, text, err := file.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, text)
	loadInit(t, e, wrapInit)
	send(e, "v4l")
	command(e, "wrap [ ]")
	if e.Buffer.Text() != "[first] 界 file" {
		t.Fatalf("a: %q", e.Buffer.Text())
	}
	send(e, "u") // clean again, so switching asks nothing
	e.requestOpen(b)
	if e.File.Path != canonical(t, b) && e.File.Path != b {
		t.Fatalf("still on %q", e.File.Path)
	}
	send(e, "wv$")
	command(e, "wrap { }")
	if e.Buffer.Text() != "second {é file}" || e.Message != "Ran :wrap" {
		t.Fatalf("b: %q %q", e.Buffer.Text(), e.Message)
	}
	send(e, "u")
	if e.Buffer.Text() != "second é file" {
		t.Fatalf("undo in b: %q", e.Buffer.Text())
	}
	if read(t, a) != "first 界 file" {
		t.Fatal("the other file changed")
	}
}

func TestSwitchingFilesWhileACommandLineHoldsASelection(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	write(t, a, "alpha")
	write(t, b, "beta")
	f, text, err := file.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, text)
	loadInit(t, e, wrapInit)
	send(e, "vl!")
	e.openPath(b)
	if e.commandVisual != 0 || e.selectionAnchor != nil || e.Mode != ModeNormal {
		t.Fatal("the selection of the old file followed the editor to the new one")
	}
}

func TestMessageFromACommand(t *testing.T) {
	e := extensionEditor(t, "abc", `(define (hi) (message (string-append "hello é\n" "界"))) (define-command "hi" hi)`)
	command(e, "hi")
	if e.Message != "hello é 界" || e.Buffer.Revision() != 0 {
		t.Fatalf("message %q revision %d", e.Message, e.Buffer.Revision())
	}
}

func TestDeadlineAndInterruptLeaveTheEditorUsable(t *testing.T) {
	spin := wrapInit + `(define (spin) (let loop () (loop))) (define-command "spin" spin)`
	t.Run("deadline", func(t *testing.T) {
		e := extensionEditor(t, "abc", spin)
		e.extensions = nil
		e.extDeadline = 50 * time.Millisecond
		e.commands = defaultCommands()
		loadInit(t, e, spin)
		text, _, _, revision := state(e)
		start := time.Now()
		send(e, "v")
		command(e, "spin")
		if e.Message != ":spin stopped after 50ms; no changes made" || time.Since(start) > 3*time.Second {
			t.Fatalf("message %q after %v", e.Message, time.Since(start))
		}
		if got, _, _, rev := state(e); got != text || rev != revision {
			t.Fatal("the buffer changed")
		}
		send(e, "v")
		command(e, "wrap ( )")
		if e.Buffer.Text() != "(a)bc" {
			t.Fatalf("text %q", e.Buffer.Text())
		}
	})
	t.Run("interrupt", func(t *testing.T) {
		e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
		var stops sync.WaitGroup
		e.SetInterrupter(func(interrupt func()) func() {
			done := make(chan struct{})
			stops.Add(1)
			go func() {
				defer stops.Done()
				select {
				case <-time.After(30 * time.Millisecond):
					interrupt()
				case <-done:
				}
			}()
			return func() { close(done) }
		})
		loadInit(t, e, spin)
		if e.Message != "" {
			t.Fatalf("message %q", e.Message)
		}
		start := time.Now()
		command(e, "spin")
		if e.Message != ":spin interrupted; no changes made" || time.Since(start) > 3*time.Second {
			t.Fatalf("message %q after %v", e.Message, time.Since(start))
		}
		stops.Wait()
		send(e, "ix")
		if e.Buffer.Text() != "xabc" {
			t.Fatalf("text %q", e.Buffer.Text())
		}
	})
}

func TestHelpListsExtensionCommandsOnlyWhenThereAreSome(t *testing.T) {
	topics := func(e *Editor) []help.Topic {
		press(e, key.F(1))
		defer press(e, key.Esc)
		seen := []help.Topic{e.Help.Topic}
		for i := 0; i < 10; i++ {
			press(e, key.Tab)
			if e.Help.Topic == seen[0] {
				break
			}
			seen = append(seen, e.Help.Topic)
		}
		return seen
	}
	plain := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "abc")
	if got := topics(plain); len(got) != 6 {
		t.Fatalf("without extensions Tab cycles through %v", got)
	}
	e := extensionEditor(t, "abc", wrapInit+`(define-command "a-very-long-command-name" car "Long one")`)
	got := topics(e)
	if len(got) != 7 || got[5] != help.Extensions { // Editing ... Code, Extensions, then Start
		t.Fatalf("with extensions Tab cycles through %v", got)
	}
	press(e, key.F(1))
	for e.Help.Topic != help.Extensions {
		press(e, key.Tab)
	}
	lines := e.Help.Lines()
	if e.Help.Title() != "Your commands" || len(lines) < 4 ||
		lines[2] != ":wrap                Wrap the selection in OPEN and CLOSE" ||
		lines[3] != ":a-very-long-command-name Long one" {
		t.Fatalf("%q: %q", e.Help.Title(), lines)
	}
	press(e, key.BackTab)
	if e.Help.Topic != help.Code {
		t.Fatalf("Shift-Tab went to %v", e.Help.Topic)
	}
	press(e, key.Tab)
	press(e, key.Tab) // wraps to the first topic
	if e.Help.Topic != help.Start {
		t.Fatalf("Tab wrapped to %v", e.Help.Topic)
	}
}
