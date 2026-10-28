package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fclaude/coigue/internal/ext"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedHostProfileMatchesRuntime(t *testing.T) {
	data, err := json.MarshalIndent(ext.HostProfile(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(data, '\n'), coigueProfile) {
		t.Fatal("host profile is stale: go generate ./internal/lsp")
	}
}
func TestSchemeServerAndConfigProfileIsolation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	config := filepath.Join(root, "coigue")
	for _, path := range []string{filepath.Join(config, "init.scm"), filepath.Join(config, "modules", "tools.scm")} {
		l, ok := languageForPath(path)
		if !ok || l != languageScheme {
			t.Fatal("not Scheme", path)
		}
		if l.root(path) != config {
			t.Fatal("wrong config root", path)
		}
		program, args := l.command()
		if program != "brote-lsp" || len(args) != 0 || l.id() != "scheme" {
			t.Fatal(program, args)
		}
		uri, _ := directoryURI(l.root(path))
		if initializationOptionsForRoot(program, uri) == nil {
			t.Fatal("missing host profile")
		}
	}
	other := filepath.Join(root, "other", "init.scm")
	uri, _ := directoryURI(languageScheme.root(other))
	if initializationOptionsForRoot("brote-lsp", uri) != nil {
		t.Fatal("profile leaked to unrelated Scheme")
	}
	if _, ok := configRoot(filepath.Join(root, "coigue-other", "init.scm")); ok {
		t.Fatal("sibling accepted")
	}
}

// Set COIGUE_BROTE_LSP to exercise the real packaged server through the editor's
// client, rather than a protocol fixture. Ordinary unit runs need no install.
func TestRealBroteServer(t *testing.T) {
	binary := os.Getenv("COIGUE_BROTE_LSP")
	if binary == "" {
		t.Skip("COIGUE_BROTE_LSP not set")
	}
	dir := t.TempDir()
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", dir)
	root := filepath.Join(dir, "coigue")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	var client Client
	client.Enable(context.Background())
	defer client.Close()
	text := "(define value 42) value"
	lines := [][]rune{[]rune(text)}
	client.Sync(filepath.Join(root, "init.scm"), 1, 1, 0, func() string { return text })
	wait := func(ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			client.Poll(lines)
			if msg, failed := client.Failure(); failed {
				t.Fatal(msg)
			}
			if ready() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("server timeout", client.Status)
	}
	wait(func() bool { return client.Status == "brote-lsp" })
	if err := client.Navigate(Definition, "", 0, 18, lines); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return client.NavigationResult != nil })
	result := client.NavigationResult
	if result.Err != nil || len(result.Targets) != 1 || result.Targets[0].Location.Col != 8 {
		t.Fatal(result)
	}
	text = "(define value"
	lines = [][]rune{[]rune(text)}
	client.Sync(filepath.Join(root, "init.scm"), 1, 2, 0, func() string { return text })
	wait(func() bool { return len(client.Diagnostics) == 1 })
}
