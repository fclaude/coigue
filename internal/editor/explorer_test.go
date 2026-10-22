package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/key"
)

// Rust: editor::tests::explorer_creates_renames_and_moves_open_file_without_losing_edits
// Parity: EXP, FOP, UND-13.
func TestExplorerCreatesRenamesAndMovesOpenFileWithoutLosingEdits(t *testing.T) {
	dir, e := editingFixture(t, "abc")
	send(e, "rx")
	ctrl(e, 'o')
	readyExplorer(t, e)
	send(e, "aunused")
	press(e, key.Esc)
	if exists(filepath.Join(dir, "unused")) {
		t.Fatal("a cancelled prompt created a file")
	}
	send(e, "aother")
	press(e, key.Enter)
	readyExplorer(t, e)
	if info, err := os.Stat(filepath.Join(dir, "other")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("other: %v", err)
	}
	send(e, "Abucket")
	press(e, key.Enter)
	readyExplorer(t, e)
	if info, err := os.Stat(filepath.Join(dir, "bucket")); err != nil || !info.IsDir() {
		t.Fatalf("bucket: %v", err)
	}
	explorerSelect(e, "fixture")
	send(e, "r")
	ctrl(e, 'u')
	send(e, "bucket/renamed.rs")
	press(e, key.Enter)
	readyExplorer(t, e)
	if !fsx.EndsWith(e.File.Path, "bucket/renamed.rs") {
		t.Fatalf("path = %q", e.File.Path)
	}
	if e.Buffer.Text() != "xbc" || !e.File.Dirty(e.Buffer.Text()) || exists(filepath.Join(dir, "fixture")) {
		t.Fatalf("text %q dirty %v", e.Buffer.Text(), e.File.Dirty(e.Buffer.Text()))
	}
	press(e, key.Esc)
	send(e, "u")
	if e.Buffer.Text() != "abc" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	redo(e)
	e.execute("w")
	if got := read(t, filepath.Join(dir, "bucket/renamed.rs")); got != "xbc" {
		t.Fatalf("saved = %q", got)
	}
	ctrl(e, 'o')
	readyExplorer(t, e)
	send(e, "h")
	readyExplorer(t, e)
	// The parent containing the active buffer can be renamed as well.
	send(e, "r")
	ctrl(e, 'u')
	send(e, "moved")
	press(e, key.Enter)
	readyExplorer(t, e)
	if !fsx.EndsWith(e.File.Path, "moved/renamed.rs") || e.Buffer.Text() != "xbc" {
		t.Fatalf("path %q text %q", e.File.Path, e.Buffer.Text())
	}
	if got := read(t, e.File.Path); got != "xbc" {
		t.Fatalf("disk = %q", got)
	}
}

// Rust: editor::tests::explorer_delete_confirmation_restore_conflict_and_active_file_protection
// Parity: FOP.
func TestExplorerDeleteConfirmationRestoreConflictAndActiveFileProtection(t *testing.T) {
	dir, e := editingFixture(t, "active")
	write(t, filepath.Join(dir, "victim"), "recover me")
	ctrl(e, 'o')
	readyExplorer(t, e)
	send(e, "dy")
	if !contains(e.Message, "Open another file") {
		t.Fatalf("message = %q", e.Message)
	}
	if info, err := os.Stat(e.File.Path); err != nil || !info.Mode().IsRegular() {
		t.Fatal("the active file was deleted")
	}
	press(e, key.Esc)
	explorerSelect(e, "victim")
	send(e, "d")
	press(e, key.Enter) // Enter never confirms deletion.
	if !exists(filepath.Join(dir, "victim")) {
		t.Fatal("Enter confirmed a deletion")
	}
	press(e, key.Esc)
	send(e, "dyn")
	readyExplorer(t, e)
	if exists(filepath.Join(dir, "victim")) {
		t.Fatal("victim was not trashed")
	}
	write(t, filepath.Join(dir, "victim"), "occupied")
	send(e, "u")
	if !contains(e.Message, "Restore failed") {
		t.Fatalf("message = %q", e.Message)
	}
	if got := read(t, filepath.Join(dir, "victim")); got != "occupied" {
		t.Fatalf("victim = %q", got)
	}
	os.Remove(filepath.Join(dir, "victim"))
	send(e, "u")
	readyExplorer(t, e)
	if got := read(t, filepath.Join(dir, "victim")); got != "recover me" {
		t.Fatalf("restored = %q", got)
	}
	if e.Buffer.Text() != "active" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
}

// Rust: editor::tests::explorer_shortcut_keeps_ctrl_e_and_file_operations_cannot_overwrite
func TestExplorerShortcutKeepsCtrlEAndFileOperationsCannotOverwrite(t *testing.T) {
	dir, e := editingFixture(t, "active")
	ctrl(e, 'e')
	if e.Buffer.Col != 5 {
		t.Fatalf("col = %d", e.Buffer.Col)
	}
	send(e, "i")
	ctrl(e, 'o')
	readyExplorer(t, e)
	ctrl(e, 'o')
	if e.Explorer != nil || e.Mode != ModeInsert {
		t.Fatalf("explorer %v mode %v", e.Explorer, e.Mode)
	}
	press(e, key.Esc)
	ctrl(e, 'o')
	readyExplorer(t, e)
	send(e, "afixture")
	press(e, key.Enter)
	if e.FilesystemPrompt == nil || !contains(e.Message, "Operation failed") {
		t.Fatalf("prompt %v message %q", e.FilesystemPrompt, e.Message)
	}
	press(e, key.Esc)
	if got := read(t, filepath.Join(dir, "fixture")); got != "active" {
		t.Fatalf("fixture = %q", got)
	}
	press(e, key.Home)
	send(e, "d")
	if e.FilesystemPrompt != nil { // ../ is not a mutable entry.
		t.Fatal("the parent entry accepted a delete prompt")
	}
}

// Rust: editor::tests::explorer_browses_dirty_buffer_and_cancel_preserves_cursor_history_and_cwd
// Parity: EXP, CLI-12, SWT.
func TestExplorerBrowsesDirtyBufferAndCancelPreservesCursorHistoryAndCwd(t *testing.T) {
	_, e := editingFixture(t, "abc\nnext")
	cwd, _ := os.Getwd()
	send(e, "lrx-")
	readyExplorer(t, e)
	if e.Buffer.Text() != "axc\nnext" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	if entry, ok := e.Explorer.Entry(e.Explorer.Selected); !ok || entry.Path != e.File.Path {
		t.Fatalf("selected entry = %+v", entry)
	}
	press(e, key.Enter)
	if e.SwitchPrompt == "" {
		t.Fatal("opening over a dirty buffer did not prompt")
	}
	send(e, "c")
	if e.Explorer == nil {
		t.Fatal("cancel closed the explorer")
	}
	send(e, "/no-match")
	if e.Explorer.Len() != 0 {
		t.Fatalf("matches = %d", e.Explorer.Len())
	}
	press(e, key.Esc)
	if e.Explorer == nil {
		t.Fatal("Esc left the explorer while filtering")
	}
	press(e, key.Esc)
	if e.Explorer != nil {
		t.Fatal("Esc did not close the explorer")
	}
	if cursor(e) != [2]int{0, 1} {
		t.Fatalf("cursor = %v", cursor(e))
	}
	if now, _ := os.Getwd(); now != cwd {
		t.Fatalf("cwd changed to %q", now)
	}
	send(e, "u")
	if e.Buffer.Text() != "abc\nnext" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	e.execute("explore")
	readyExplorer(t, e)
	e.Key(key.WithCtrl('p'), 22)
	if e.Picker == nil || e.Picker.Root != cwd {
		t.Fatalf("picker %v", e.Picker)
	}
	press(e, key.Esc)
	if e.Explorer == nil || e.Picker != nil {
		t.Fatal("Esc from the picker did not return to the explorer")
	}
}

// Rust: editor::tests::explorer_saves_or_discards_only_after_explicit_choice
// Parity: SWT.
func TestExplorerSavesOrDiscardsOnlyAfterExplicitChoice(t *testing.T) {
	for _, choice := range []string{"s", "d"} {
		dir, e := editingFixture(t, "old")
		source := e.File.Path
		write(t, filepath.Join(dir, "destination"), "new")
		send(e, "rx-")
		readyExplorer(t, e)
		send(e, "/destination")
		press(e, key.Enter)
		if e.Buffer.Text() != "xld" || e.SwitchPrompt == "" {
			t.Fatalf("%s: text %q prompt %q", choice, e.Buffer.Text(), e.SwitchPrompt)
		}
		if got := read(t, source); got != "old" {
			t.Fatalf("%s: source changed early: %q", choice, got)
		}
		send(e, choice)
		if e.Buffer.Text() != "new" {
			t.Fatalf("%s: text = %q", choice, e.Buffer.Text())
		}
		want := "old"
		if choice == "s" {
			want = "xld"
		}
		if got := read(t, source); got != want {
			t.Fatalf("%s: source = %q, want %q", choice, got, want)
		}
		if e.Explorer != nil || e.SwitchPrompt != "" {
			t.Fatalf("%s: explorer %v prompt %q", choice, e.Explorer, e.SwitchPrompt)
		}
		send(e, "u")
		if e.Buffer.Text() != "new" {
			t.Fatalf("%s: undo crossed files: %q", choice, e.Buffer.Text())
		}
	}
}

// Rust: editor::tests::failed_save_or_discard_open_keeps_original_edits_and_history
func TestFailedSaveOrDiscardOpenKeepsOriginalEditsAndHistory(t *testing.T) {
	dir, e := editingFixture(t, "old")
	destination := filepath.Join(dir, "binary")
	if err := os.WriteFile(destination, []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	send(e, "rx-")
	readyExplorer(t, e)
	send(e, "/binary")
	press(e, key.Enter)
	write(t, e.File.Path, "external")
	send(e, "s")
	if e.SwitchPrompt == "" || !contains(e.Message, "Save failed") || e.Buffer.Text() != "xld" {
		t.Fatalf("save: prompt %q message %q text %q", e.SwitchPrompt, e.Message, e.Buffer.Text())
	}
	send(e, "d")
	if !contains(e.Message, "not UTF-8") || e.Explorer == nil || e.Buffer.Text() != "xld" {
		t.Fatalf("discard: message %q explorer %v text %q", e.Message, e.Explorer, e.Buffer.Text())
	}
	press(e, key.Esc) // Leave filter.
	press(e, key.Esc) // Return to file.
	send(e, "u")
	if e.Buffer.Text() != "old" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	if got := read(t, e.File.Path); got != "external" {
		t.Fatalf("disk = %q", got)
	}
}
