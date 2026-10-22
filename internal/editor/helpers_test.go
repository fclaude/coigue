package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/picker"
)

// editingFixture writes text to <tempdir>/fixture and opens an editor on it,
// like the Rust editing_fixture helper.
func editingFixture(t *testing.T, text string) (string, *Editor) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture")
	write(t, path, text)
	f, text, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e := New(t.Context(), f, text)
	t.Cleanup(e.Close)
	return dir, e
}

func newEditor(t *testing.T, f *file.File, text string) *Editor {
	t.Helper()
	e := New(t.Context(), f, text)
	t.Cleanup(e.Close)
	return e
}

func newWelcome(t *testing.T, dir string) *Editor {
	t.Helper()
	e := NewWelcome(t.Context(), dir)
	t.Cleanup(e.Close)
	return e
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := fsx.Canonicalize(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func send(e *Editor, s string) {
	for _, c := range s {
		e.Key(key.Char(c), 22)
	}
}

func press(e *Editor, code key.Code) { e.Key(key.Press(code), 22) }

func redo(e *Editor) { e.Key(key.WithCtrl('r'), 22) }

func ctrl(e *Editor, c rune) { e.Key(key.WithCtrl(c), 22) }

func cursor(e *Editor) [2]int { return [2]int{e.Buffer.Row, e.Buffer.Col} }

func readyExplorer(t *testing.T, e *Editor) {
	t.Helper()
	x := e.Explorer
	deadline := time.Now().Add(3 * time.Second)
	for x.Loading {
		x.Poll()
		if time.Now().After(deadline) {
			t.Fatal("explorer did not finish loading")
		}
		time.Sleep(time.Millisecond)
	}
	x.Fit(4)
}

func explorerSelect(e *Editor, query string) {
	send(e, "/")
	send(e, query)
	press(e, key.Tab)
}

func readyPicker(t *testing.T, e *Editor, root, query string) {
	t.Helper()
	p := picker.New(t.Context(), root)
	deadline := time.Now().Add(3 * time.Second)
	for p.Scanning {
		p.Poll()
		if time.Now().After(deadline) {
			t.Fatal("picker did not finish scanning")
		}
		time.Sleep(time.Millisecond)
	}
	p.Query = query
	p.QueryChanged()
	e.dropPicker()
	e.Picker = p
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// regChars reports the character register's text and whether one is set.
func regChars(e *Editor) (string, bool) {
	if e.register == nil || e.register.isLine {
		return "", false
	}
	return string(e.register.chars), true
}

func fsStat(path string) (os.FileInfo, error) { return os.Stat(path) }

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
