package editor

import (
	"errors"
	"testing"

	"github.com/fclaude/coigue/internal/key"
)

// Rust: editor::tests::clipboard_register_serialization_and_recoverable_copy_errors
// Parity: CLP, REG-01, VIS yank serialization.
func TestClipboardRegisterSerializationAndRecoverableCopyErrors(t *testing.T) {
	_, e := editingFixture(t, "é界\nnext")
	send(e, "vly")
	e.copyClipboardUsing(func(text string) error {
		if text != "é界" {
			t.Errorf("copied %q", text)
		}
		return errors.New("backend unavailable")
	})
	if !contains(e.Message, "internal yank kept") {
		t.Fatalf("message = %q", e.Message)
	}
	if e.clipboardExport != nil {
		t.Fatal("a failed copy recorded an export")
	}
	if text, ok := regChars(e); !ok || text != "é界" {
		t.Fatalf("register = %q, %v", text, ok)
	}
	if e.Buffer.Undo() {
		t.Fatal("selection and yank added history")
	}
	send(e, "Vy")
	e.copyClipboardUsing(func(text string) error {
		if text != "é界\n" {
			t.Errorf("copied %q", text)
		}
		return nil
	})
	if e.clipboardExport == nil || e.clipboardExport.text != "é界\n" {
		t.Fatalf("export = %+v", e.clipboardExport)
	}
	send(e, "GVy")
	e.copyClipboardUsing(func(text string) error {
		if text != "next" {
			t.Errorf("copied %q", text)
		}
		return nil
	})
	if e.Buffer.Text() != "é界\nnext" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	_, empty := editingFixture(t, "")
	empty.copyClipboardUsing(func(string) error {
		t.Fatal("No register must not contact a backend")
		return nil
	})
	send(empty, "vY") // Empty visual Y must not export a previous register.
	if !contains(empty.Message, "Empty selection") {
		t.Fatalf("message = %q", empty.Message)
	}
}

// Rust: editor::tests::clipboard_paste_unicode_multiline_linewise_empty_errors_and_undo
func TestClipboardPasteUnicodeMultilineLinewiseEmptyErrorsAndUndo(t *testing.T) {
	_, e := editingFixture(t, "abc\nlast")
	send(e, "yy")
	e.copyClipboardUsing(func(string) error { return nil })
	e.Buffer.BeginEdit()
	e.pasteClipboardResult("abc\n", nil, true)
	e.Buffer.FinishEdit()
	if e.Buffer.Text() != "abc\nabc\nlast" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u")
	e.Buffer.BeginEdit()
	e.pasteClipboardResult("é界\r\nnext\n", nil, false)
	e.Buffer.FinishEdit()
	if e.Buffer.Text() != "é界\r\nnext\nabc\nlast" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	if e.register == nil || !e.register.isLine || !e.register.terminated || len(e.register.lines) != 1 {
		t.Fatalf("register = %+v", e.register)
	}
	send(e, "u")
	before := cursor(e)
	e.pasteClipboardResult("", nil, true)
	e.pasteClipboardResult("", errors.New("denied"), true)
	if e.Buffer.Text() != "abc\nlast" || cursor(e) != before {
		t.Fatalf("text %q at %v", e.Buffer.Text(), cursor(e))
	}
	e.Key(key.WithCtrl('r'), 22)
	if e.Buffer.Text() != "é界\r\nnext\nabc\nlast" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
	if !contains(e.Message, "Redone") {
		t.Fatalf("message = %q", e.Message)
	}
}

// Rust: editor::tests::visual_yank_cancel_preserve_redo_and_empty_yank_keeps_register
func TestVisualYankCancelPreserveRedoAndEmptyYankKeepsRegister(t *testing.T) {
	_, e := editingFixture(t, "abc\n")
	send(e, "vldu")
	if e.Buffer.Text() != "abc\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "gg0vly")
	send(e, "Gvy")
	if text, ok := regChars(e); !ok || text != "ab" {
		t.Fatalf("register = %q, %v", text, ok)
	}
	send(e, "v")
	press(e, key.Esc)
	e.Key(key.WithCtrl('r'), 22)
	if e.Buffer.Text() != "c\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	if e.Mode != ModeNormal || e.selectionAnchor != nil {
		t.Fatalf("mode %v anchor %v", e.Mode, e.selectionAnchor)
	}
}

// Rust: editor::tests::visual_characters_yank_paste_delete_backwards_and_undo
func TestVisualCharactersYankPasteDeleteBackwardsAndUndo(t *testing.T) {
	_, e := editingFixture(t, "aé界z\n\nend\r\nlast")
	send(e, "v2ly")
	if e.Mode != ModeNormal {
		t.Fatalf("mode = %v", e.Mode)
	}
	if text, ok := regChars(e); !ok || text != "aé界" {
		t.Fatalf("register = %q", text)
	}
	if e.Buffer.Undo() { // Selection/yank add no history.
		t.Fatal("yank added history")
	}
	send(e, "$p")
	if e.Buffer.Text() != "aé界zaé界\n\nend\r\nlast" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u$v")
	send(e, "2hy")
	if text, _ := regChars(e); text != "é界z" {
		t.Fatalf("register = %q", text)
	}
	send(e, "gg0v2ld")
	if e.Buffer.Text() != "z\n\nend\r\nlast" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "aé界z\n\nend\r\nlast" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	e.Key(key.WithCtrl('r'), 22)
	if e.Buffer.Text() != "z\n\nend\r\nlast" {
		t.Fatalf("redo = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::visual_multiline_empty_lines_crlf_and_whole_buffer
func TestVisualMultilineEmptyLinesCrlfAndWholeBuffer(t *testing.T) {
	_, e := editingFixture(t, "ab\r\n\n界z\nlast")
	send(e, "v2jly")
	if text, ok := regChars(e); !ok || text != "ab\r\n\n界z" {
		t.Fatalf("register = %q", text)
	}
	send(e, "v2jld")
	if e.Buffer.Text() != "\nlast" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "uG$vgg0d")
	if e.Buffer.Text() != "" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "u")
	if e.Buffer.Text() != "ab\r\n\n界z\nlast" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	send(e, "j0vyd") // 'd' starts an ordinary operator after yanking newline.
	if text, ok := regChars(e); !ok || text != "\n" {
		t.Fatalf("register = %q", text)
	}
	press(e, key.Esc)
	_, empty := editingFixture(t, "")
	send(empty, "vydVd")
	if empty.Buffer.Text() != "" || empty.Mode != ModeNormal {
		t.Fatalf("empty: %q mode %v", empty.Buffer.Text(), empty.Mode)
	}
}

// Rust: editor::tests::visual_lines_switch_cancel_swap_and_motion_counts
func TestVisualLinesSwitchCancelSwapAndMotionCounts(t *testing.T) {
	_, e := editingFixture(t, "one\ntwo\n\nlast")
	send(e, "V2jy")
	if e.register == nil || !e.register.isLine || !e.register.terminated || len(e.register.lines) != 3 {
		t.Fatalf("register = %+v", e.register)
	}
	send(e, "Gp")
	if e.Buffer.Text() != "one\ntwo\n\nlast\none\ntwo\n" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "uggVjd")
	if e.Buffer.Text() != "\nlast" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "uGVggd")
	if e.Buffer.Text() != "" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, "uggv2jov")
	if e.Mode != ModeNormal {
		t.Fatalf("mode = %v", e.Mode)
	}
	if _, _, ok := e.Selection(0); ok {
		t.Fatal("selection survived leaving visual mode")
	}
	send(e, "vV")
	if e.Mode != ModeVisualLine {
		t.Fatalf("mode = %v", e.Mode)
	}
	press(e, key.Esc)
	if e.selectionAnchor != nil {
		t.Fatal("anchor survived Esc")
	}
	text := e.Buffer.Text()
	send(e, "vixp:q") // Editing and command entry are ignored while selecting.
	if e.Buffer.Text() != text {
		t.Fatalf("text changed to %q", e.Buffer.Text())
	}
	if e.Mode != ModeVisual {
		t.Fatalf("mode = %v", e.Mode)
	}
	press(e, key.Esc)
}
