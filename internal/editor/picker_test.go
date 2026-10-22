package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/key"
)

// Rust: editor::tests::file_switch_resets_history_and_picker_cancel_keeps_it
// Parity: UND-11, SWT.
func TestFileSwitchResetsHistoryAndPickerCancelKeepsIt(t *testing.T) {
	dir, e := editingFixture(t, "old")
	write(t, filepath.Join(dir, "second"), "new")
	send(e, "x")
	e.execute("w")
	readyPicker(t, e, dir, "second")
	press(e, key.Esc)
	send(e, "u")
	if e.Buffer.Text() != "old" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	redo(e)
	readyPicker(t, e, dir, "second")
	press(e, key.Enter)
	send(e, "u")
	if e.Buffer.Text() != "new" || !contains(e.Message, "Nothing to undo") {
		t.Fatalf("text %q message %q", e.Buffer.Text(), e.Message)
	}
	redo(e)
	if !contains(e.Message, "Nothing to redo") {
		t.Fatalf("message = %q", e.Message)
	}
	send(e, "ix")
	redo(e) // Ctrl-R has no history action in insert mode.
	press(e, key.Esc)
	send(e, "u")
	if e.Buffer.Text() != "new" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::register_survives_switching_files
// Parity: REG-04.
func TestRegisterSurvivesSwitchingFiles(t *testing.T) {
	dir, e := editingFixture(t, "copied")
	write(t, filepath.Join(dir, "second"), "destination")
	send(e, "yy")
	readyPicker(t, e, dir, "second")
	press(e, key.Enter)
	send(e, "p")
	if e.Buffer.Text() != "destination\ncopied" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::picker_opens_files_and_preserves_buffer_on_cancel_or_failure
// Parity: DSC, SWT, UND-11.
func TestPickerOpensFilesAndPreservesBufferOnCancelOrFailure(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	write(t, old, "old text")
	write(t, filepath.Join(dir, "new.rs"), "fn main() {}")
	if err := os.WriteFile(filepath.Join(dir, "binary"), []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	f, text, err := file.Open(old)
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, text)
	e.Buffer.Col = 3
	e.Mode = ModeInsert
	readyPicker(t, e, dir, "newrs")
	e.Key(key.Press(key.Esc), 22)
	if e.Mode != ModeInsert || e.Buffer.Col != 3 || e.Buffer.Text() != "old text" {
		t.Fatalf("cancel: mode %v col %d text %q", e.Mode, e.Buffer.Col, e.Buffer.Text())
	}
	readyPicker(t, e, dir, "binary")
	e.Key(key.Press(key.Enter), 22)
	if e.Buffer.Text() != "old text" || e.File.Path != canonical(t, old) || !contains(e.Message, "not UTF-8") {
		t.Fatalf("failed open: text %q path %q message %q", e.Buffer.Text(), e.File.Path, e.Message)
	}
	readyPicker(t, e, dir, "newrs")
	e.Key(key.Press(key.Enter), 22)
	if e.Buffer.Text() != "fn main() {}" || e.File.Path != canonical(t, filepath.Join(dir, "new.rs")) ||
		e.Mode != ModeNormal || e.Generation != 1 || e.Picker != nil {
		t.Fatalf("open: text %q path %q mode %v generation %d", e.Buffer.Text(), e.File.Path, e.Mode, e.Generation)
	}
	e.Buffer.Insert('x')
	e.Key(key.WithCtrl('p'), 22)
	if e.Picker == nil {
		t.Fatal("Ctrl-P did not open the picker")
	}
	readyPicker(t, e, dir, "old")
	e.Key(key.Press(key.Enter), 22)
	if e.Buffer.Text() != "xfn main() {}" || e.SwitchPrompt == "" {
		t.Fatalf("dirty open: text %q prompt %q", e.Buffer.Text(), e.SwitchPrompt)
	}
	press(e, key.Esc)
	if e.SwitchPrompt != "" || !fsx.EndsWith(e.File.Path, "new.rs") {
		t.Fatalf("prompt %q path %q", e.SwitchPrompt, e.File.Path)
	}
}

// Rust: editor::tests::picker_root_is_process_working_directory
// Parity: CLI-12, DSC.
func TestPickerRootIsProcessWorkingDirectory(t *testing.T) {
	f, text, err := file.Open(filepath.Join(t.TempDir(), "outside-working-directory"))
	if err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, f, text)
	cwd, _ := os.Getwd()
	e.Key(key.WithCtrl('p'), 22)
	if e.Picker == nil || e.Picker.Root != cwd {
		t.Fatalf("picker = %+v", e.Picker)
	}
	e.Key(key.Press(key.Esc), 22)
	if e.Picker != nil {
		t.Fatal("Esc did not close the picker")
	}
	e.execute("open")
	if e.Picker == nil || e.Picker.Root != cwd {
		t.Fatalf("picker after :open = %+v", e.Picker)
	}
}
