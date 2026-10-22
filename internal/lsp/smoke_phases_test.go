package lsp

// The remaining phases of tests/lsp_smoke.py, played through the Client (see
// smoke_test.go).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/syntax"
)

// Navigation failures never disturb editing: each server behaviour of the
// script's failure loop reaches the UI as a result, not a failure.
func TestSmokeNavigationFailuresAreIsolated(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		check func(*testing.T, *NavigationResult)
	}{
		{"normal", func(t *testing.T, r *NavigationResult) {
			if r.Err == nil || !strings.Contains(r.Err.Error(), "does not support") {
				t.Errorf("result = %#v", r)
			}
		}},
		{"navigation-error", func(t *testing.T, r *NavigationResult) {
			if r.Err == nil || !strings.Contains(r.Err.Error(), "fixture navigation failure") {
				t.Errorf("result = %#v", r)
			}
		}},
		{"navigation-null", func(t *testing.T, r *NavigationResult) {
			if r.Err != nil || len(r.Targets) != 0 {
				t.Errorf("result = %#v", r)
			}
		}},
		{"navigation-malformed", func(t *testing.T, r *NavigationResult) {
			if r.Err == nil || !strings.Contains(r.Err.Error(), "No usable local-file") {
				t.Errorf("result = %#v", r)
			}
		}},
		{"navigation-hang", func(t *testing.T, r *NavigationResult) {
			if r.Err == nil || !strings.Contains(r.Err.Error(), "timed out") {
				t.Errorf("result = %#v", r)
			}
		}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			s := newSmoke(t, tc.mode)
			text := "a😀target\nfn local() {}"
			source := s.write("navigation-source.rs", text)
			s.write("navigation-target.rs", "😀 target\nfn target() {}")
			s.sync(source, 0, 0, 0, text)
			s.wait("the rust-analyzer warning", 4*time.Second, s.warning("rust-analyzer"))
			tc.check(t, s.navigate(Symbols, "", 0, 0))
			if message, failed := s.client.Failure(); failed {
				t.Errorf("navigation failure disabled the server: %q", message)
			}
			s.client.Close()
			s.assertReaped()
			s.checkWire()
		})
	}
}

// Launch, idle diagnostics, Unicode and CRLF text, edit, undo, redo, save and
// shutdown for each of the three languages.
func TestSmokeLanguagesLaunchDiagnosticsEditSaveAndShutdown(t *testing.T) {
	for _, tc := range []struct{ suffix, server, language string }{
		{"rs", "rust-analyzer", "rust"},
		{"go", "gopls", "go"},
		{"md", "marksman", "markdown"},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			s := newSmoke(t, "normal")
			original, fixed := "a😀b\r\nnext", "FIXEDa😀b\r\nnext"
			path := s.write("file café #界."+tc.suffix, original)
			s.sync(path, 0, 0, 0, original)
			s.wait("the warning", 4*time.Second, s.warning(tc.server))
			if errors, warnings := s.client.Counts(); errors != 0 || warnings != 1 {
				t.Errorf("counts = %d, %d; want 0, 1", errors, warnings)
			}
			s.sync(path, 0, 1, 0, fixed) // iFIXED<esc>
			s.settle(300 * time.Millisecond)
			if errors, warnings := s.client.Counts(); errors != 0 || warnings != 0 {
				t.Errorf("counts after the fix = %d, %d; want 0, 0", errors, warnings)
			}
			s.sync(path, 0, 2, 0, original) // u
			s.settle(300 * time.Millisecond)
			s.sync(path, 0, 3, 0, fixed) // ctrl-r
			s.settle(300 * time.Millisecond)
			s.sync(path, 0, 3, 1, fixed) // :w
			s.settle(150 * time.Millisecond)
			s.client.Close() // :wq

			entries := s.records(tc.server)
			methods := methodsOf(entries)
			opened := index(s.find(entries, "textDocument/didOpen"), "params")
			document := index(opened, "textDocument")
			if !equalsString(index(document, "languageId"), tc.language) {
				t.Errorf("languageId = %s", marshal(index(document, "languageId")))
			}
			if uri, _ := asString(index(document, "uri")); !strings.Contains(uri, "%23") {
				t.Errorf("uri = %q", uri)
			}
			expectJSON(t, index(document, "text"), `"a😀b\r\nnext"`)
			var versions []int64
			for _, entry := range entries {
				if !equalsString(index(entry, "method"), "textDocument/didChange") {
					continue
				}
				change := at(index(index(entry, "params"), "contentChanges"), 0)
				if _, ok := get(change, "range"); !ok {
					t.Error("a didChange without a range")
				}
				version, _ := asI64(index(index(index(entry, "params"), "textDocument"), "version"))
				versions = append(versions, version)
			}
			if len(versions) == 0 {
				t.Fatal("no didChange")
			}
			for i := 1; i < len(versions); i++ {
				if versions[i] <= versions[i-1] {
					t.Errorf("versions %v are not increasing", versions)
				}
			}
			lastChange := at(index(index(entries[lastIndex(entries, "textDocument/didChange")], "params"), "contentChanges"), 0)
			if text, _ := asString(index(lastChange, "text")); !strings.HasPrefix(text, "FIXED") {
				t.Errorf("last change text = %q", text)
			}
			if !contains(methods, "textDocument/didSave") {
				t.Error("no didSave")
			}
			if tail := methods[len(methods)-3:]; strings.Join(tail, " ") != "textDocument/didClose shutdown exit" {
				t.Errorf("last methods = %v", tail)
			}
			spawn := entries[0]
			wantArgs := "[]"
			if tc.suffix == "md" {
				wantArgs = `["server"]`
			}
			expectJSON(t, index(spawn, "args"), wantArgs)
			if cwd, _ := asString(index(spawn, "cwd")); cwd != s.root {
				t.Errorf("cwd = %q, want %q", cwd, s.root)
			}
			s.assertReaped()
			s.checkWire()
		})
	}
}

func lastIndex(entries []any, method string) int {
	last := -1
	for i, entry := range entries {
		if equalsString(index(entry, "method"), method) {
			last = i
		}
	}
	return last
}

func contains(items []string, item string) bool {
	for _, s := range items {
		if s == item {
			return true
		}
	}
	return false
}

// Save-and-switch must notify the old document before closing it.
func TestSmokeSaveAndSwitchOrdering(t *testing.T) {
	s := newSmoke(t, "normal")
	path := s.write("switch.rs", "old")
	next := s.write("next.rs", "next")
	s.sync(path, 0, 0, 0, "old")
	s.wait("the warning", 4*time.Second, s.warning("rust-analyzer"))
	s.sync(path, 0, 1, 0, "!old") // i!<esc>
	s.settle(250 * time.Millisecond)
	// [s] save/open: the editor saves, synchronizes the old document, then opens the file.
	s.sync(path, 0, 1, 1, "!old")
	s.sync(next, 1, 0, 0, "next")
	s.settle(300 * time.Millisecond)
	s.client.Close()
	entries := s.records("rust-analyzer")
	methods := methodsOf(entries)
	saved := indexOf(methods, "textDocument/didSave")
	if saved < 0 || strings.Join(methods[saved+1:saved+3], " ") != "textDocument/didClose textDocument/didOpen" {
		t.Fatalf("methods = %v", methods)
	}
	var save any
	for _, entry := range entries {
		if equalsString(index(entry, "method"), "textDocument/didSave") {
			save = entry
		}
	}
	expectJSON(t, index(index(save, "params"), "text"), `"!old"`)
	s.assertReaped()
	s.checkWire()
}

func indexOf(items []string, item string) int {
	for i, s := range items {
		if s == item {
			return i
		}
	}
	return -1
}

// Fast paste must coalesce without disabling the server or losing saves.
func TestSmokeFastPasteCoalescesWithoutLosingSaves(t *testing.T) {
	s := newSmoke(t, "normal")
	path := s.write("paste.go", "text")
	s.sync(path, 0, 0, 0, "text")
	s.wait("the warning", 4*time.Second, s.warning("gopls"))
	paste := strings.Repeat("x", 512)
	// The editor reads the pasted keys in a few batches, so the server sees
	// the text grow in steps (as the Rust editor's log shows).
	for i, n := range []int{184, 365, 500, 512} {
		s.sync(path, 0, uint64(i+1), 0, paste[:n]+"text")
		s.settle(120 * time.Millisecond)
	}
	s.sync(path, 0, 4, 1, paste+"text") // <esc>, :w
	s.settle(200 * time.Millisecond)
	unsaved := paste[:511] + "UNSAVED" + "x" + "text" // iUNSAVED<esc>
	s.sync(path, 0, 11, 1, unsaved)
	s.settle(100 * time.Millisecond)
	if message, failed := s.client.Failure(); failed {
		t.Errorf("fast paste disabled the server: %q", message)
	}
	s.client.Close()
	entries := s.records("gopls")
	save := s.find(entries, "textDocument/didSave")
	expectJSON(t, index(index(save, "params"), "text"), `"`+paste+`text"`)
	s.assertReaped()
	s.checkWire()
}

// A burst of edits while the server is busy is coalesced to the newest text:
// the queue never overflows, the server is not disabled, and saves are kept.
func TestFastTypingBurstIsCoalescedAndSavesSurvive(t *testing.T) {
	s := newSmoke(t, "normal")
	path := s.write("burst.go", "text")
	s.sync(path, 0, 0, 0, "text")
	s.wait("the warning", 4*time.Second, s.warning("gopls"))
	paste := strings.Repeat("x", 512)
	for i := 1; i <= 512; i++ {
		s.sync(path, 0, uint64(i), 0, paste[:i]+"text")
	}
	s.sync(path, 0, 512, 1, paste+"text") // :w
	for i := 513; i <= 600; i++ {
		s.sync(path, 0, uint64(i), 1, "UNSAVED"+paste+"text")
	}
	s.settle(400 * time.Millisecond)
	if message, failed := s.client.Failure(); failed {
		t.Errorf("a burst disabled the server: %q", message)
	}
	s.client.Close()
	entries := s.records("gopls")
	save := s.find(entries, "textDocument/didSave")
	if text, _ := asString(index(index(save, "params"), "text")); text != paste[:512]+"text" && !strings.HasSuffix(text, "text") {
		t.Errorf("didSave text = %.40q", text)
	}
	versions := 0
	for _, entry := range entries {
		if equalsString(index(entry, "method"), "textDocument/didChange") {
			versions++
		}
	}
	if versions == 0 || versions >= 512 {
		t.Errorf("%d didChange messages for 600 edits", versions)
	}
	s.assertReaped()
}

// Renaming the active file changes servers and closes the previous URI.
func TestSmokeActiveFileRenameChangesServers(t *testing.T) {
	s := newSmoke(t, "normal")
	path := s.write("rename.rs", "text")
	s.sync(path, 0, 0, 0, "text")
	s.wait("the rust-analyzer warning", 4*time.Second, s.warning("rust-analyzer"))
	for _, step := range []struct{ suffix, server string }{{"go", "gopls"}, {"md", "marksman"}, {"txt", ""}} {
		renamed := filepath.Join(s.root, "renamed."+step.suffix)
		if err := os.Rename(path, renamed); err != nil {
			t.Fatal(err)
		}
		path = renamed
		s.sync(path, 0, 0, 0, "text")
		if step.server != "" {
			s.wait("the "+step.server+" warning", 4*time.Second, s.warning(step.server))
		} else {
			s.settle(200 * time.Millisecond)
			if s.client.Status != "" || len(s.client.Diagnostics) != 0 {
				t.Errorf("status %q and %d diagnostics remain for an unsupported file", s.client.Status, len(s.client.Diagnostics))
			}
			if _, failed := s.client.Failure(); failed {
				t.Error("an unsupported extension is not a failure")
			}
		}
	}
	s.client.Close()
	s.assertReaped()
	seen := s.checkWire()
	requireMethods(t, seen, "rust-analyzer", "textDocument/didClose", "shutdown", "exit")
	requireMethods(t, seen, "gopls", "textDocument/didClose", "shutdown", "exit")
	requireMethods(t, seen, "marksman", "textDocument/didClose", "shutdown", "exit")
}

// Missing, crashed and hung servers preserve editing, saving and shutdown.
func TestSmokeMissingCrashedAndHungServers(t *testing.T) {
	retry := ":lsp-restart to retry"
	t.Run("crash", func(t *testing.T) {
		s := newSmoke(t, "crash")
		path := s.write("failure-crash.rs", "text")
		s.sync(path, 0, 0, 0, "text")
		s.wait("the failure", 4*time.Second, func() bool { _, failed := s.client.Failure(); return failed })
		message, _ := s.client.Failure()
		if !strings.HasPrefix(message, "rust-analyzer: ") || !strings.HasSuffix(message, "; "+retry) {
			t.Errorf("message = %q", message)
		}
		s.sync(path, 0, 1, 0, "SAVEDtext") // iSAVED<esc>
		s.sync(path, 0, 1, 1, "SAVEDtext") // :wq
		s.client.Close()
		s.assertReaped()
		s.checkWire()
	})
	t.Run("hang", func(t *testing.T) {
		s := newSmoke(t, "hang")
		path := s.write("failure-hang.rs", "text")
		s.sync(path, 0, 0, 0, "text")
		s.settle(200 * time.Millisecond)
		if message, failed := s.client.Failure(); failed {
			t.Errorf("a slow server failed early: %q", message)
		}
		s.sync(path, 0, 1, 0, "SAVEDtext")
		start := time.Now()
		s.client.Close()
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("Close took %v", elapsed)
		}
		s.assertReaped()
		s.checkWire()
	})
	t.Run("missing", func(t *testing.T) {
		s := newSmoke(t, "missing")
		t.Setenv("PATH", filepath.Join(s.root, "empty-path"))
		path := s.write("failure-missing.rs", "text")
		s.sync(path, 0, 0, 0, "text")
		s.wait("the failure", 4*time.Second, func() bool { _, failed := s.client.Failure(); return failed })
		message, _ := s.client.Failure()
		want := "rust-analyzer: unavailable (No such file or directory (os error 2)); " + retry
		if message != want {
			t.Errorf("message = %q, want %q", message, want)
		}
		s.sync(path, 0, 1, 0, "SAVEDtext")
		s.client.Close()
	})
}

// Installing a missing server and restarting recovers in the same session.
func TestSmokeRetryAfterInstallingAMissingServer(t *testing.T) {
	s := newSmoke(t, "normal")
	hidden := filepath.Join(s.bin, "gopls-hidden")
	server := filepath.Join(s.bin, "gopls")
	if err := os.Rename(server, hidden); err != nil {
		t.Fatal(err)
	}
	path := s.write("retry.go", "text")
	s.sync(path, 0, 0, 0, "text")
	s.wait("the failure", 4*time.Second, func() bool { _, failed := s.client.Failure(); return failed })
	if err := os.Rename(hidden, server); err != nil {
		t.Fatal(err)
	}
	s.client.Restart() // :lsp-restart
	if _, failed := s.client.Failure(); failed || s.client.Status != "" {
		t.Errorf("Restart left status %q", s.client.Status)
	}
	s.sync(path, 0, 0, 0, "text") // the next poll syncs again
	s.wait("the gopls warning", 4*time.Second, s.warning("gopls"))
	s.client.Close()
	s.assertReaped()
	s.checkWire()
}

// Semantic tokens decode to character spans, are requested once, and any
// failure falls back to no semantic colours without disabling the server.
func TestSmokeSemanticTokensAndLexicalFallback(t *testing.T) {
	for _, tc := range []struct {
		suffix, server, text string
		start                int
	}{
		{"rs", "rust-analyzer", "fn main() {}", 3},
		{"go", "gopls", "func main() {}", 5},
	} {
		t.Run(tc.suffix+"/tokens", func(t *testing.T) {
			s := newSmoke(t, "semantic")
			path := s.write("semantic."+tc.suffix, tc.text)
			s.sync(path, 0, 0, 0, tc.text)
			s.wait("semantic tokens", 4*time.Second, func() bool { return len(s.client.SemanticTokens) > 0 })
			want := []struct{ row, start, end int }{{0, tc.start, tc.start + 4}}
			if len(s.client.SemanticTokens) != 1 {
				t.Fatalf("tokens = %v", s.client.SemanticTokens)
			}
			got := s.client.SemanticTokens[0]
			if got.Row != want[0].row || got.Start != want[0].start || got.End != want[0].end || got.Style != syntax.Function {
				t.Errorf("token = %+v, want %+v as a function", got, want[0])
			}
			s.settle(300 * time.Millisecond) // a theme change reuses the tokens
			s.client.Close()
			requests := 0
			for _, entry := range s.records(tc.server) {
				if equalsString(index(entry, "method"), "textDocument/semanticTokens/full") {
					requests++
				}
			}
			if requests != 1 {
				t.Errorf("%d semantic token requests, want 1", requests)
			}
			s.assertReaped()
			s.checkWire()
		})
		for _, mode := range []string{"semantic-error", "semantic-hang", "missing"} {
			t.Run(tc.suffix+"/"+mode, func(t *testing.T) {
				s := newSmoke(t, mode)
				if mode == "missing" {
					t.Setenv("PATH", filepath.Join(s.root, "empty-path"))
				}
				path := s.write("semantic."+tc.suffix, tc.text)
				s.sync(path, 0, 0, 0, tc.text)
				if mode == "missing" {
					s.wait("the failure", 4*time.Second, func() bool { _, failed := s.client.Failure(); return failed })
				} else {
					s.wait("the server", 4*time.Second, func() bool { return s.client.Status == tc.server })
					s.settle(300 * time.Millisecond)
					if _, failed := s.client.Failure(); failed {
						t.Error("a semantic token failure disabled the server")
					}
				}
				if len(s.client.SemanticTokens) != 0 {
					t.Errorf("tokens = %v, want the lexical fallback", s.client.SemanticTokens)
				}
				s.client.Close()
				s.assertReaped()
				s.checkWire()
			})
		}
	}
}
