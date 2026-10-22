package lsp

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/syntax"
)

// Client scripts: random sequences of Sync, Poll, Navigate, CancelNavigation,
// Restart and worker events against a Client with a synthetic session (a
// mailbox nobody serves and an event channel the script feeds), run through
// the real Client in src/lsp.rs by a scratch harness. After every step the
// whole visible state must agree: diagnostics, semantic tokens and revision,
// status and failure, navigation result and pending request, stamp, URI,
// document version, key, session and retired counts, the answers of Counts,
// LineSeverity and AtCursor, and every command the worker would have received.
// Paths are in the testdata/tree_spec.json tree; the language server programs
// are not on PATH, so a session the client starts fails at once.

func styleName(s syntax.Style) string {
	return [...]string{"Plain", "Keyword", "Comment", "String", "Number", "Macro", "Punctuation", "Lifetime", "Type",
		"Function", "Variable", "Property", "Constant", "Namespace", "Heading", "Emphasis", "Strong", "Link"}[s]
}

func commandJSON(c input) any {
	switch c := c.(type) {
	case inputDocument:
		d := c.document
		return []any{"document", d.uri, d.generation, d.version, d.saved, d.saveEvent, d.language.id(), d.text}
	case inputNavigate:
		r := c.request
		return []any{"navigate", r.serial, uint8(r.kind), r.query, r.document.uri, r.document.generation, r.document.version, r.row, r.character}
	case inputCancelNavigation:
		return []any{"cancel"}
	}
	return []any{"stop"}
}

func clientState(c *Client) object {
	diagnostics := []any{}
	for _, d := range c.Diagnostics {
		diagnostics = append(diagnostics, []any{d.Start.Row, d.Start.Col, d.End.Row, d.End.Col, d.Severity, d.Message})
	}
	tokens := []any{}
	for _, t := range c.SemanticTokens {
		tokens = append(tokens, []any{t.Row, t.Start, t.End, styleName(t.Style)})
	}
	state := object{
		"diagnostics": diagnostics, "tokens": tokens, "semantic_revision": c.SemanticRevision,
		"status": c.Status, "failure": c.failure, "failure_text": nil, "result": nil, "pending": nil,
		"serial": c.navigationSerial, "stamp": nil, "uri": c.uri, "version": c.version, "key": nil,
		"session": c.session != nil, "retired": len(c.retired), "enabled": c.enabled,
	}
	if text, failed := c.Failure(); failed {
		state["failure_text"] = text
	}
	if r := c.NavigationResult; r != nil {
		if r.Err != nil {
			state["result"] = []any{uint8(r.Kind), object{"err": r.Err.Error()}}
		} else {
			list := []any{}
			for _, target := range r.Targets {
				list = append(list, []any{target.Label, target.Location.Row, target.Location.Col})
			}
			state["result"] = []any{uint8(r.Kind), object{"ok": list}}
		}
	}
	if c.navigationPending != nil {
		state["pending"] = c.navigationPending.request.serial
	}
	if c.stamp != nil {
		state["stamp"] = []any{c.stamp.path, c.stamp.generation, c.stamp.revision, c.stamp.saved}
	}
	if c.key != nil {
		state["key"] = []any{c.key.language.id(), c.key.root}
	}
	errors, warnings := c.Counts()
	state["counts"] = []any{errors, warnings}
	severity := []any{}
	for row := 0; row < 4; row++ {
		if s, ok := c.LineSeverity(row); ok {
			severity = append(severity, s)
		} else {
			severity = append(severity, nil)
		}
	}
	state["severity"] = severity
	at := []any{}
	for _, pos := range [][2]int{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {1, 0}, {1, 1}, {2, 0}, {2, 2}, {3, 0}} {
		if m, ok := c.AtCursor(pos[0], pos[1]); ok {
			at = append(at, m)
		} else {
			at = append(at, nil)
		}
	}
	state["at"] = at
	return state
}

func runClientScript(t *testing.T, root string, ops []any) []any {
	t.Helper()
	sub := func(s string) string { return strings.ReplaceAll(s, "<R>", root) }
	subString := func(v any) string { s, _ := asString(v); return sub(s) }
	in, mailboxIn := mailbox()
	events := make(chan output, 64)
	var eventsOpen = true
	finished := make(chan struct{})
	client := &Client{}
	client.Enable(context.Background())
	client.session = &session{input: in, output: events, finished: finished}
	var lines [][]rune
	var records []any
	for _, op := range ops {
		var note any
		switch name, _ := asString(index(op, "o")); name {
		case "setkey":
			l := map[string]language{"rust": languageRust, "go": languageGo, "markdown": languageMarkdown}[index(op, "language").(string)]
			client.key = &languageKey{l, subString(index(op, "root"))}
		case "clearkey":
			client.key = nil
		case "sync":
			text, _ := asString(index(op, "text"))
			called := false
			client.Sync(subString(index(op, "path")), jsonUint(index(op, "gen")), jsonUint(index(op, "rev")), jsonUint(index(op, "saved")),
				func() string { called = true; return text })
			lines = splitLines(text)
			note = object{"text_called": called}
			if strings.HasSuffix(client.Status, ": starting") {
				// A real session was started; the program is not on PATH, so it fails quickly.
				for i := 0; i < 300; i++ {
					client.Poll(lines)
					if client.failure {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		case "event":
			e := index(op, "e")
			version, _ := asI64(index(e, "version"))
			var event output
			switch kind, _ := asString(index(e, "k")); kind {
			case "diag":
				p, ok := decodePublication(object{"uri": subString(index(e, "uri")), "version": index(e, "pubversion"), "diagnostics": index(e, "diagnostics")})
				if !ok {
					t.Fatalf("bad publication %s", marshal(e))
				}
				event = outputDiagnostics{generation: jsonUint(index(e, "generation")), version: int32(version), publication: p}
			case "tokens":
				legend, ok := decodeLegend(index(e, "legend"))
				if !ok {
					t.Fatal("bad legend")
				}
				out := outputTokens{
					document: tokenDocument{uri: subString(index(e, "uri")), generation: jsonUint(index(e, "generation")), version: int32(version)},
					legend:   legend,
				}
				if items, ok := asArray(index(e, "data")); ok {
					out.hasData = true
					out.data = []uint32{}
					for _, item := range items {
						out.data = append(out.data, uint32(jsonUint(item)))
					}
				}
				event = out
			case "nav":
				kind, _ := asU64(index(e, "kind"))
				out := outputNavigation{request: navigationRequest{
					serial: jsonUint(index(e, "serial")), kind: NavigationKind(kind),
					document: tokenDocument{uri: subString(index(e, "uri")), generation: jsonUint(index(e, "generation")), version: int32(version)},
				}}
				if message, ok := asString(index(e, "err")); ok {
					out.err = &stringError{message}
				} else {
					out.targets = []navigation.Target{}
					labels, _ := asArray(index(e, "labels"))
					for _, l := range labels {
						path := subString(l)
						out.targets = append(out.targets, navigation.Target{Location: navigation.NewLocation(path), Label: path})
					}
				}
				event = out
			case "ready":
				event = outputReady{}
			case "failed":
				message, _ := asString(index(e, "message"))
				event = outputFailed{message}
			default:
				t.Fatalf("unknown event %s", marshal(e))
			}
			if eventsOpen {
				trySend(events, event)
			}
		case "disconnect":
			if eventsOpen {
				close(events)
				eventsOpen = false
			}
		case "poll":
			text, _ := asString(index(op, "text"))
			lines = splitLines(text)
			client.Poll(lines)
		case "navigate":
			text, _ := asString(index(op, "text"))
			lines = splitLines(text)
			kind, _ := asU64(index(op, "kind"))
			query, _ := asString(index(op, "query"))
			row, _ := asU64(index(op, "row"))
			col, _ := asU64(index(op, "col"))
			if err := client.Navigate(NavigationKind(kind), query, int(row), int(col), lines); err != nil {
				note = err.Error()
			} else {
				note = "ok"
			}
		case "cancel":
			client.CancelNavigation()
		case "restart":
			client.Restart()
		case "navage":
			if client.navigationPending != nil {
				ms, _ := asU64(index(op, "ms"))
				client.navigationPending.started = time.Now().Add(-time.Duration(ms) * time.Millisecond)
			}
		case "take":
			note = client.NavigationResult != nil
			client.NavigationResult = nil
		case "drain":
		case "fillqueue":
			if client.session != nil {
				n, _ := asU64(index(op, "n"))
				for i := uint64(0); i < n; i++ {
					_ = client.session.input.trySend(inputDocument{document{
						uri: "file:///fill" + string(rune('0'+i%10)) + string(rune('a'+i/10)), generation: i, saveEvent: true, language: languageRust,
					}})
				}
			}
		default:
			t.Fatalf("unknown op %s", marshal(op))
		}
		var commands []any
		fill := 0
		if name, _ := asString(index(op, "o")); name == "drain" {
			for {
				c, ok := mailboxIn.pop()
				if !ok {
					break
				}
				if d, isDoc := c.(inputDocument); isDoc && strings.HasPrefix(d.document.uri, "file:///fill") {
					fill++
					continue
				}
				commands = append(commands, commandJSON(c))
			}
		}
		mailboxIn.queue.mu.Lock()
		queued := len(mailboxIn.queue.items)
		mailboxIn.queue.mu.Unlock()
		state := clientState(client)
		if commands == nil {
			commands = []any{}
		}
		state["commands"] = commands
		state["fill_commands"] = fill
		state["note"] = note
		state["queued"] = queued
		records = append(records, state)
	}
	client.session = nil
	client.retired = nil
	return records
}

type stringError struct{ message string }

func (e *stringError) Error() string { return e.message }

func checkClientCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
	t.Setenv("PATH", "/nonexistent")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var source io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		zipped, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer zipped.Close()
		source = zipped
	}
	root := buildTree(t)
	t.Chdir(root)
	scanner := bufio.NewScanner(source)
	scanner.Buffer(nil, 1<<26)
	for scanner.Scan() {
		var entry struct {
			Ops []json.RawMessage `json:"ops"`
			Out []json.RawMessage `json:"out"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		ops := make([]any, len(entry.Ops))
		for i, raw := range entry.Ops {
			if ops[i], err = unmarshal(raw); err != nil {
				t.Fatal(err)
			}
		}
		records := runClientScript(t, root, ops)
		checked++
		for i, record := range records {
			got := canonicalText(t, strings.ReplaceAll(string(marshal(record)), root, "<R>"))
			want := canonicalText(t, string(entry.Out[i]))
			if got != want {
				mismatches++
				if mismatches <= 10 {
					t.Errorf("script %d step %d (%s):\n got %.1500s\nwant %.1500s", checked, i, marshal(ops[i]), got, want)
				}
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checked, mismatches
}

func TestClientScriptsAgreeWithRustGolden(t *testing.T) {
	if checked, _ := checkClientCorpus(t, "testdata/client_golden.jsonl.gz"); checked < 100 {
		t.Fatalf("only %d golden scripts", checked)
	}
}

func TestClientScriptsAgreeWithRustCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_CLIENT_CORPUS")
	if path == "" {
		t.Skip("COIGUE_CLIENT_CORPUS not set")
	}
	checked, mismatches := checkClientCorpus(t, path)
	t.Logf("%d scripts, %d with a mismatch", checked, mismatches)
}
