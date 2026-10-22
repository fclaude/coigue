package lsp

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTree creates the directory tree of testdata/tree_spec.json (project
// roots with their markers, symlinks, a broken link, source files for
// navigation targets, files at the 2 MiB limit) under a fresh directory, and
// returns the directory with symlinks resolved.
func buildTree(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "tree")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/tree_spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec [][]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, op := range spec {
		path := filepath.Join(root, op[1].(string))
		switch op[0] {
		case "mkdir":
			err = os.MkdirAll(path, 0o777)
		case "write":
			err = os.WriteFile(path, []byte(op[2].(string)), 0o666)
		case "writehex":
			var content []byte
			content, err = hex.DecodeString(op[2].(string))
			if err == nil {
				err = os.WriteFile(path, content, 0o666)
			}
		case "symlink":
			err = os.Symlink(op[2].(string), path)
		case "big":
			err = os.WriteFile(path, bytes.Repeat([]byte("a"), int(op[2].(float64))), 0o666)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func checkFSCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	root := buildTree(t)
	t.Chdir(root)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<26)
	unroot := func(s string) string { return strings.ReplaceAll(s, root, "<R>") }
	unrootHex := func(b string) string {
		return hex.EncodeToString(bytes.ReplaceAll([]byte(b), []byte(root), []byte("<R>")))
	}
	substitute := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "<RU>", root), "<R>", root) }
	// The corpus was recorded on Linux, where /tmp is a directory. absolute_path
	// canonicalizes in the Rust editor and in this one, so where /tmp is a symlink
	// (macOS: /private/tmp) both answer the real path and the recorded bytes for a
	// literal /tmp input cannot apply.
	tmpIsLink := false
	if real, err := filepath.EvalSymlinks("/tmp"); err == nil && real != "/tmp" {
		tmpIsLink = true
	}
	for scanner.Scan() {
		var entry struct {
			In  map[string]json.RawMessage `json:"in"`
			Out json.RawMessage            `json:"out"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		var op string
		json.Unmarshal(entry.In["op"], &op)
		if op == "absolute" && tmpIsLink {
			var literal string
			json.Unmarshal(entry.In["t"], &literal)
			if strings.HasPrefix(literal, "/tmp") {
				continue
			}
		}
		var got any
		switch op {
		case "absolute":
			var template string
			json.Unmarshal(entry.In["t"], &template)
			if p, err := absolutePath(substitute(template)); err != nil {
				got = object{"err": ioErrorText(err)}
			} else {
				got = object{"ok": unrootHex(p)}
			}
		case "root":
			var template, name string
			json.Unmarshal(entry.In["t"], &template)
			json.Unmarshal(entry.In["language"], &name)
			l := map[string]language{"rust": languageRust, "go": languageGo, "markdown": languageMarkdown}[name]
			got = unrootHex(l.root(substitute(template)))
		case "targets":
			var uri, text string
			json.Unmarshal(entry.In["uri"], &uri)
			json.Unmarshal(entry.In["text"], &text)
			result, err := unmarshal([]byte(substitute(string(entry.In["result"]))))
			if err != nil {
				t.Fatal(err)
			}
			targets, err := parseTargets(result, substitute(uri), text)
			if err != nil {
				got = object{"err": err.Error()}
			} else {
				list := []any{}
				for _, target := range targets {
					list = append(list, []any{unrootHex(target.Location.Path), target.Location.Row, target.Location.Col, unroot(target.Label)})
				}
				got = object{"ok": list}
			}
		default:
			t.Fatalf("unknown op %q", op)
		}
		checked++
		want := canonicalText(t, string(entry.Out))
		if have := canonicalText(t, string(marshal(got))); have != want {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("%.300s: got %.300s, Rust %.300s", scanner.Text(), have, want)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checked, mismatches
}

func TestPathsRootsAndNavigationTargetsAgreeWithRustGolden(t *testing.T) {
	if checked, _ := checkFSCorpus(t, "testdata/fs_golden.jsonl"); checked < 150 {
		t.Fatalf("only %d golden cases", checked)
	}
}

func TestPathsRootsAndNavigationTargetsAgreeWithRustCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_FS_CORPUS")
	if path == "" {
		t.Skip("COIGUE_FS_CORPUS not set")
	}
	checked, mismatches := checkFSCorpus(t, path)
	t.Logf("%d cases, %d mismatches", checked, mismatches)
}
