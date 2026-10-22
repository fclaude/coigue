package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fclaude/coigue/internal/key"
)

// Rust: editor::tests::welcome_is_inert_and_browses_launch_directory_until_file_opens
// Parity: WEL-01, WEL-04, WEL-05, WEL-08, WEL-09.
func TestWelcomeIsInertAndBrowsesLaunchDirectoryUntilFileOpens(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space café 界")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "hello.txt")
	write(t, path, "hello")
	e := newWelcome(t, root)
	send(e, "iword dwxpu")
	if e.Buffer.Text() != "" || e.Mode != ModeNormal {
		t.Fatalf("welcome edited: %q mode %v", e.Buffer.Text(), e.Mode)
	}
	if e.execute("w") || !contains(e.Message, "No file open") {
		t.Fatalf("write on welcome: %q", e.Message)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("welcome created files: %v", entries)
	}
	e.openExplorer()
	if e.Explorer.Directory != root {
		t.Fatalf("explorer directory = %q", e.Explorer.Directory)
	}
	e.closeExplorer()
	if !e.IsWelcome() {
		t.Fatal("closing the explorer left welcome")
	}
	e.requestOpen(filepath.Join(root, "missing"))
	if !e.IsWelcome() {
		t.Fatal("a failed open left welcome")
	}
	e.requestOpen(path)
	if e.IsWelcome() || e.SwitchPrompt != "" || e.Buffer.Text() != "hello" {
		t.Fatalf("open: welcome %v prompt %q text %q", e.IsWelcome(), e.SwitchPrompt, e.Buffer.Text())
	}
	send(e, "i!")
	e.Key(key.Press(key.Esc), 22)
	if e.execute("q") {
		t.Fatal("quit with unsaved changes")
	}
	if !e.execute("wq") {
		t.Fatal("wq did not quit")
	}
	if got := read(t, path); got != "!hello" {
		t.Fatalf("file = %q", got)
	}
}

// Rust: editor::tests::welcome_commands_cancel_and_quit_without_a_file
// Parity: WEL-05.
func TestWelcomeCommandsCancelAndQuitWithoutAFile(t *testing.T) {
	dir := t.TempDir()
	e := newWelcome(t, dir)
	send(e, ":wq")
	if e.Key(key.Press(key.Enter), 1) {
		t.Fatal(":wq quit without a file")
	}
	if e.Mode != ModeNormal {
		t.Fatalf("mode = %v", e.Mode)
	}
	send(e, ":q")
	e.Key(key.Press(key.Esc), 1)
	if e.Mode != ModeNormal || e.Command != "" {
		t.Fatalf("mode %v command %q", e.Mode, e.Command)
	}
	send(e, ":q")
	if !e.Key(key.Press(key.Enter), 1) {
		t.Fatal(":q did not quit")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files created: %v", entries)
	}
}

// Rust: editor::tests::welcome_filesystem_actions_have_no_active_file_to_protect
// Parity: WEL-09, FOP.
func TestWelcomeFilesystemActionsHaveNoActiveFileToProtect(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "old")
	target := filepath.Join(dir, "new")
	write(t, source, "contents")
	e := newWelcome(t, dir)
	e.openExplorer()
	e.FilesystemPrompt = &FilesystemPrompt{Action: Rename, Source: source, Input: "new"}
	e.applyFilesystemAction()
	if _, err := os.Stat(source); err == nil {
		t.Fatal("source still exists")
	}
	if got := read(t, target); got != "contents" {
		t.Fatalf("target = %q", got)
	}
	if e.File.Path != "" {
		t.Fatalf("file path = %q", e.File.Path)
	}
	e.FilesystemPrompt = &FilesystemPrompt{Action: Delete, Source: target}
	e.applyFilesystemAction()
	if _, err := os.Stat(target); err == nil {
		t.Fatal("target survived the trash")
	}
	e.restoreDeleted()
	if got := read(t, target); got != "contents" {
		t.Fatalf("restored = %q", got)
	}
	if !e.IsWelcome() {
		t.Fatal("left welcome")
	}
}
