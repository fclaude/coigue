package editor

import (
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/key"
)

func TestPasteInInsertModeIsTypedTextWithNormalizedLineBreaks(t *testing.T) {
	_, e := editingFixture(t, "ab\ncd\n")
	send(e, "li")
	if !e.AcceptsPaste() {
		t.Fatal("Insert mode refused a paste")
	}
	e.Paste("1\r\n2\r3")
	if got := e.Buffer.Text(); got != "a1\n2\n3b\ncd\n" {
		t.Fatalf("text = %q", got)
	}
	if e.Buffer.Row != 2 || e.Buffer.Col != 1 || e.Mode != ModeInsert {
		t.Fatalf("cursor %d:%d mode %v, want 2:1 in Insert mode", e.Buffer.Row, e.Buffer.Col, e.Mode)
	}
	// Typing after the paste continues the same Insert session: one undo.
	send(e, "x")
	press(e, key.Esc)
	send(e, "u")
	if got := e.Buffer.Text(); got != "ab\ncd\n" {
		t.Fatalf("after undo = %q", got)
	}
}

func TestPasteInNormalModeIsLikePAndKeepsTheRegister(t *testing.T) {
	_, e := editingFixture(t, "abc\nnext\n")
	send(e, "yy") // the register holds the line "abc"
	e.Buffer.Row, e.Buffer.Col = 0, 1
	if !e.AcceptsPaste() {
		t.Fatal("Normal mode refused a paste")
	}
	e.Paste("XY")
	if got := e.Buffer.Text(); got != "aXYbc\nnext\n" {
		t.Fatalf("text = %q", got)
	}
	if e.Buffer.Row != 0 || e.Buffer.Col != 2 || e.Mode != ModeNormal {
		t.Fatalf("cursor %d:%d mode %v, want 0:2 in Normal mode", e.Buffer.Row, e.Buffer.Col, e.Mode)
	}
	send(e, "u")
	if got := e.Buffer.Text(); got != "abc\nnext\n" {
		t.Fatalf("after undo = %q", got)
	}
	send(e, "p")
	if got := e.Buffer.Text(); got != "abc\nabc\nnext\n" {
		t.Fatalf("the paste changed the register: p gives %q", got)
	}
}

func TestPasteEndingInANewlineLeavesTheCursorOnItsLastCharacter(t *testing.T) {
	_, e := editingFixture(t, "tail\n")
	e.Paste("one\ntwo\n")
	if got := e.Buffer.Text(); got != "one\ntwo\ntail\n" {
		t.Fatalf("text = %q", got)
	}
	if e.Buffer.Row != 1 || e.Buffer.Col != 2 {
		t.Fatalf("cursor %d:%d, want 1:2", e.Buffer.Row, e.Buffer.Col)
	}
}

func TestPasteIsRefusedWhereKeysMeanSomethingElse(t *testing.T) {
	_, e := editingFixture(t, "abc\n")
	for _, tc := range []struct {
		name  string
		setup string
	}{
		{"pending operator", "d"},
		{"count", "3"},
		{"Visual mode", "v"},
		{"command line", ":"},
		{"search", "/"},
	} {
		send(e, tc.setup)
		if e.AcceptsPaste() {
			t.Errorf("%s: AcceptsPaste is true", tc.name)
		}
		press(e, key.Esc)
		if !e.AcceptsPaste() {
			t.Fatalf("after %s and Esc, Normal mode refused a paste", tc.name)
		}
	}
	e.Paste("")
	if got := e.Buffer.Text(); got != "abc\n" {
		t.Fatalf("an empty paste changed the text to %q", got)
	}
}

func TestEditsReachTheLanguageServerOncePausedAndSavesAtOnce(t *testing.T) {
	_, e := editingFixture(t, "abc\n")
	send(e, "ix")
	if !e.LSPWaiting() {
		t.Fatal("an edit was sent at once instead of when typing pauses")
	}
	e.Tick(22)
	if !e.LSPWaiting() {
		t.Fatal("the edit was sent before typing paused")
	}
	time.Sleep(lspQuiet + 10*time.Millisecond)
	e.Tick(22)
	if e.LSPWaiting() {
		t.Fatal("the edit was not sent after typing paused")
	}
	send(e, "y")
	press(e, key.Esc)
	send(e, ":w")
	press(e, key.Enter)
	if e.LSPWaiting() {
		t.Fatal("saving left an edit unsent")
	}
	if e.Dirty() {
		t.Fatal("dirty after saving")
	}
}

func TestDirtyFollowsEditsUndoAndSameSizeChanges(t *testing.T) {
	_, e := editingFixture(t, "abc\n")
	if e.Dirty() {
		t.Fatal("dirty when just opened")
	}
	send(e, "rz") // same size, different text
	if !e.Dirty() {
		t.Fatal("a same-size change is not dirty")
	}
	send(e, "u")
	if e.Dirty() {
		t.Fatal("dirty after undoing back to the saved text")
	}
}
