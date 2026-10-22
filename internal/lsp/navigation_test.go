package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decoded turns a value built in Go into what the decoder would produce from
// its JSON text, as a server's message would be.
func decoded(t *testing.T, v any) any {
	t.Helper()
	return parse(t, string(marshal(v)))
}

func navRange(line, character int) any {
	return object{
		"start": object{"line": line, "character": character},
		"end":   object{"line": line, "character": character + 1},
	}
}

func navDoc() *document {
	return &document{
		uri:        "file:///tmp/coigue-navigation.rs",
		generation: 7,
		version:    2,
		language:   languageRust,
		text:       "a😀target\nfn local() {}",
	}
}

func navRequest(kind NavigationKind, serial uint64) navigationRequest {
	return navigationRequest{
		serial:    serial,
		kind:      kind,
		query:     "Target",
		document:  tokenDocumentOf(navDoc()),
		row:       0,
		character: 3,
	}
}

// Rust: lsp::navigation::tests::parses_links_flat_and_nested_symbols_with_unsaved_utf16_text
func TestParsesLinksFlatAndNestedSymbolsWithUnsavedUTF16Text(t *testing.T) {
	d := navDoc()
	links := decoded(t, []any{object{"targetUri": d.uri, "targetRange": navRange(0, 0), "targetSelectionRange": navRange(0, 3)}})
	hits, err := parseTargets(links, d.uri, d.text)
	if err != nil {
		t.Fatal(err)
	}
	if hits[0].Location.Row != 0 || hits[0].Location.Col != 2 {
		t.Fatalf("link at (%d, %d), want (0, 2)", hits[0].Location.Row, hits[0].Location.Col)
	}
	symbols := decoded(t, []any{object{"name": "Outer", "selectionRange": navRange(0, 3), "children": []any{
		object{"name": "local", "selectionRange": navRange(1, 3)},
	}}})
	hits, err = parseTargets(symbols, d.uri, d.text)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("%d hits, want 2", len(hits))
	}
	if !strings.Contains(hits[1].Label, "Outer / local") {
		t.Fatalf("label %q", hits[1].Label)
	}
	if hits[1].Location.Row != 1 || hits[1].Location.Col != 3 {
		t.Fatalf("nested symbol at (%d, %d), want (1, 3)", hits[1].Location.Row, hits[1].Location.Col)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "space café #界.rs")
	if err := os.WriteFile(path, []byte("😀 target"), 0o666); err != nil {
		t.Fatal(err)
	}
	pathURI, _ := fileURI(path)
	flat := decoded(t, []any{object{"name": "Target", "location": object{"uri": pathURI, "range": navRange(0, 3)}}})
	hits, err = parseTargets(flat, d.uri, d.text)
	if err != nil || hits[0].Location.Col != 2 {
		t.Fatalf("flat symbol: %v, %v", hits, err)
	}
	if _, err := parseTargets(decoded(t, []any{object{"uri": "https://invalid.example/", "range": navRange(0, 0)}}), d.uri, d.text); err == nil {
		t.Error("a non-file URL was accepted")
	}
	if _, err := parseTargets(decoded(t, []any{object{"range": navRange(0, 0)}}), d.uri, d.text); err == nil {
		t.Error("a location without a URI was accepted")
	}
	hits, err = parseTargets(nil, d.uri, d.text)
	if err != nil || len(hits) != 0 {
		t.Errorf("null result: %v, %v", hits, err)
	}
}

// Rust: lsp::navigation::tests::requests_negotiate_capabilities_cancel_superseded_work_and_time_out
func TestRequestsNegotiateCapabilitiesCancelSupersededWorkAndTimeOut(t *testing.T) {
	tx := newOutbox(16)
	events := make(chan output, 16)
	var requests navigationRequests
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	isErr := func(event output, ok bool) bool {
		n, isNav := event.(outputNavigation)
		return ok && isNav && n.err != nil
	}
	must(requests.queue(navRequest(Definition, 1), tx))
	must(requests.tick(navDoc(), tx, events))
	if event, ok := tryEvent(events); !isErr(event, ok) {
		t.Fatalf("expected an unsupported-capability error, got %#v", event)
	}
	if !nothingSent(tx) {
		t.Fatal("sent a request the server does not support")
	}
	requests.capabilities = parse(t, `{"definitionProvider":{},"referencesProvider":true,"documentSymbolProvider":true,"workspaceSymbolProvider":true}`)
	must(requests.queue(navRequest(Definition, 2), tx))
	must(requests.tick(navDoc(), tx, events))
	value := recv(t, tx)
	expectJSON(t, index(value, "id"), `"navigation-2"`)
	expectJSON(t, index(index(index(value, "params"), "position"), "character"), `3`)
	must(requests.queue(navRequest(References, 3), tx))
	expectJSON(t, index(recv(t, tx), "method"), `"$/cancelRequest"`)
	requests.response(parse(t, `{"id":"navigation-2","result":null}`), events)
	if _, ok := tryEvent(events); ok {
		t.Fatal("a cancelled request was answered")
	}
	must(requests.tick(navDoc(), tx, events))
	expectJSON(t, index(index(index(recv(t, tx), "params"), "context"), "includeDeclaration"), `true`)
	requests.pending.started = time.Now().Add(-navigationTimeout)
	must(requests.tick(navDoc(), tx, events))
	if event, ok := tryEvent(events); !isErr(event, ok) {
		t.Fatalf("expected a timeout error, got %#v", event)
	}
	expectJSON(t, index(recv(t, tx), "method"), `"$/cancelRequest"`)
	must(requests.queue(navRequest(Workspace, 4), tx))
	must(requests.tick(navDoc(), tx, events))
	expectJSON(t, index(index(recv(t, tx), "params"), "query"), `"Target"`)
	requests.response(parse(t, `{"id":"navigation-4","error":{"code":-32603,"message":"fixture failure"}}`), events)
	event, ok := tryEvent(events)
	if n, isNav := event.(outputNavigation); !ok || !isNav || n.err == nil || !strings.Contains(n.err.Error(), "fixture failure") {
		t.Fatalf("expected the server's failure message, got %#v", event)
	}
}

// Rust: lsp::navigation::tests::client_rejects_stale_replies_and_converts_outgoing_unicode_position
func TestClientRejectsStaleRepliesAndConvertsOutgoingUnicodePosition(t *testing.T) {
	d := navDoc()
	input, in := mailbox()
	events := make(chan output, 16)
	finished := make(chan struct{})
	var client Client
	client.session = &session{input: input, output: events, finished: finished}
	client.stamp = &stamp{path: "/tmp/coigue-navigation.rs", generation: 7, revision: 1, saved: 0}
	client.uri = d.uri
	client.version = 2
	lines := bufferLines(d.text)
	if err := client.Navigate(Definition, "", 0, 2, lines); err != nil {
		t.Fatal(err)
	}
	command, ok := in.recvTimeout(time.Second)
	sent, isNav := command.(inputNavigate)
	if !ok || !isNav {
		t.Fatalf("missing request: %v", command)
	}
	if sent.request.character != 3 {
		t.Fatalf("character = %d, want 3", sent.request.character)
	}
	for variant := 0; variant < 4; variant++ {
		stale := sent.request
		switch variant {
		case 0:
			stale.document.version = 1
		case 1:
			stale.document.generation++
		case 2:
			stale.document.uri += ".other"
		default:
			stale.serial++
		}
		events <- outputNavigation{request: stale, targets: nil}
		client.Poll(lines)
		if client.NavigationResult != nil {
			t.Fatalf("variant %d: a stale reply was accepted", variant)
		}
	}
	events <- outputNavigation{request: sent.request}
	client.Poll(lines)
	result := client.NavigationResult
	client.NavigationResult = nil
	if result == nil || result.Kind != Definition || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	if err := client.Navigate(Symbols, "", 0, 0, lines); err != nil {
		t.Fatal(err)
	}
	client.navigationPending.started = time.Now().Add(-navigationTimeout - 2*time.Second)
	client.Poll(lines)
	result = client.NavigationResult
	client.NavigationResult = nil
	if result == nil || result.Kind != Symbols || result.Err == nil {
		t.Fatalf("result = %#v", result)
	}
}
