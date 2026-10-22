package lsp

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// bufferLines is Buffer::new(text).lines.
func bufferLines(text string) [][]rune { return splitLines(text) }

// parse is json!(...) over a literal: a decoded message as a server sends it.
func parse(t *testing.T, text string) any {
	t.Helper()
	value, err := unmarshal([]byte(text))
	if err != nil {
		t.Fatalf("parse %s: %v", text, err)
	}
	return value
}

// recv is `rx.recv().unwrap()` on a queue that must already hold a message.
func recv(t *testing.T, tx *outbox) any {
	t.Helper()
	select {
	case value := <-tx.messages:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("no message was sent")
		return nil
	}
}

// nothingSent is `rx.try_recv().is_err()`.
func nothingSent(tx *outbox) bool {
	select {
	case <-tx.messages:
		return false
	default:
		return true
	}
}

func tryEvent(events chan output) (output, bool) {
	select {
	case event := <-events:
		return event, true
	default:
		return nil, false
	}
}

func mustEvent(t *testing.T, events chan output) output {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("no event was produced")
		return nil
	}
}

func expectJSON(t *testing.T, got any, want string) {
	t.Helper()
	if !equalJSON(got, parse(t, want)) {
		t.Fatalf("got %s, want %s", marshal(got), want)
	}
}

func semanticCapabilities(t *testing.T) any {
	return parse(t, `{"semanticTokensProvider":{"full":{"delta":true},"legend":{"tokenTypes":["function"],"tokenModifiers":[]}}}`)
}

func doc(text string, version int32) *document {
	return &document{
		uri:        "file:///tmp/test.rs",
		generation: 0,
		version:    version,
		saved:      0,
		saveEvent:  false,
		language:   languageRust,
		text:       text,
	}
}

func testPublication(uri string, version *int32, message string) publication {
	p := publication{uri: uri, diagnostics: []wireDiagnostic{{
		rng:         wireRange{start: wirePosition{0, 1}, end: wirePosition{0, 3}},
		severity:    2,
		hasSeverity: true,
		message:     message,
	}}}
	if version != nil {
		p.version, p.hasVersion = *version, true
	}
	return p
}

func ptr(v int32) *int32 { return &v }

// Rust: lsp::tests::semantic_requests_negotiate_full_cancel_stale_and_refresh_without_reinitializing
func TestSemanticRequestsNegotiateFullCancelStaleAndRefreshWithoutReinitializing(t *testing.T) {
	tx := newOutbox(64)
	events := make(chan output, 64)
	var tokens tokenRequests
	first := doc("fn name() {}", 1)
	if err := tokens.request(first, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) {
		t.Fatal("requested tokens before the server announced a legend")
	}
	tokens.configure(semanticCapabilities(t))
	if err := tokens.request(first, tx, events); err != nil {
		t.Fatal(err)
	}
	request := recv(t, tx)
	expectJSON(t, index(request, "method"), `"textDocument/semanticTokens/full"`)
	expectJSON(t, index(request, "id"), `3`)
	if err := tokens.request(first, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) {
		t.Fatal("second request while one is pending")
	}
	second := doc("fn other() {}", 2)
	if err := tokens.request(second, tx, events); err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(recv(t, tx), "method"), `"$/cancelRequest"`)
	expectJSON(t, index(recv(t, tx), "id"), `4`)
	tokens.response(parse(t, `{"id":3,"result":{"data":[0,3,4,0,0]}}`), events)
	if _, ok := tryEvent(events); ok {
		t.Fatal("a response to the cancelled request was published")
	}
	tokens.response(parse(t, `{"id":4,"result":{"data":[0,3,5,0,0]}}`), events)
	published, ok := mustEvent(t, events).(outputTokens)
	if !ok {
		t.Fatal("expected a tokens event")
	}
	if published.document.version != 2 {
		t.Fatalf("document version = %d, want 2", published.document.version)
	}
	if !published.hasData || len(published.data) != 5 || published.data[2] != 5 {
		t.Fatalf("data = %v", published.data)
	}
	if err := tokens.request(second, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) {
		t.Fatal("requested again without a refresh")
	}
	if err := tokens.refresh(tx); err != nil {
		t.Fatal(err)
	}
	if err := tokens.request(second, tx, events); err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(recv(t, tx), "id"), `5`)
	markdown := doc("# title", 3)
	markdown.language = languageMarkdown
	if err := tokens.request(markdown, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) {
		t.Fatal("requested tokens for markdown")
	}
	proto := &protocol{gopls: true}
	err := proto.reply(parse(t, `{"id":9,"method":"workspace/configuration","params":{"items":[{"section":"gopls"},{"section":"gopls.semanticTokens"},{"section":"other"}]}}`), "file:///tmp", tx)
	if err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(recv(t, tx), "result"), `[{"semanticTokens":true},true,null]`)
	proto.tokens = tokens
	err = proto.reply(parse(t, `{"id":"refresh","method":"workspace/semanticTokens/refresh"}`), "file:///tmp", tx)
	if err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(recv(t, tx), "method"), `"$/cancelRequest"`)
	expectJSON(t, index(recv(t, tx), "result"), `null`)
	if proto.tokens.requested {
		t.Fatal("refresh left the request flag set")
	}
}

// Rust: lsp::tests::semantic_errors_retry_only_when_appropriate_and_timeouts_keep_diagnostics_alive
func TestSemanticErrorsRetryOnlyWhenAppropriateAndTimeoutsKeepDiagnosticsAlive(t *testing.T) {
	tx := newOutbox(64)
	events := make(chan output, 64)
	var tokens tokenRequests
	tokens.configure(semanticCapabilities(t))
	document := doc("text", 1)
	for id := 3; id <= 5; id++ {
		tokens.retryAt = time.Time{}
		if err := tokens.request(document, tx, events); err != nil {
			t.Fatal(err)
		}
		got := recv(t, tx)
		if !equalsInt(index(got, "id"), int64(id)) && !equalJSON(index(got, "id"), uint64(id)) {
			t.Fatalf("request id = %s, want %d", marshal(index(got, "id")), id)
		}
		tokens.response(parse(t, `{"id":`+itoa(id)+`,"error":{"code":-32801,"message":"modified"}}`), events)
		if event, ok := mustEvent(t, events).(outputTokens); !ok || event.hasData {
			t.Fatalf("expected a tokens event without data, got %#v", event)
		}
	}
	tokens.retryAt = time.Time{}
	if err := tokens.request(document, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) { // At most two retries per document/refresh.
		t.Fatal("a third retry was sent")
	}
	if err := tokens.refresh(tx); err != nil {
		t.Fatal(err)
	}
	if err := tokens.request(document, tx, events); err != nil {
		t.Fatal(err)
	}
	id, _ := asU64(index(recv(t, tx), "id"))
	tokens.response(parse(t, `{"id":`+itoa(int(id))+`,"error":{"code":-32601,"message":"unsupported"}}`), events)
	mustEvent(t, events)
	if err := tokens.request(document, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) {
		t.Fatal("retried after an unsupported error")
	}
	if err := tokens.refresh(tx); err != nil {
		t.Fatal(err)
	}
	if err := tokens.request(document, tx, events); err != nil {
		t.Fatal(err)
	}
	recv(t, tx)
	tokens.pending.started = time.Now().Add(-tokenTimeout)
	if err := tokens.request(document, tx, events); err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(recv(t, tx), "method"), `"$/cancelRequest"`)
	if event, ok := mustEvent(t, events).(outputTokens); !ok || event.hasData {
		t.Fatalf("expected a tokens event without data, got %#v", event)
	}
	if err := tokens.request(document, tx, events); err != nil {
		t.Fatal(err)
	}
	if !nothingSent(tx) {
		t.Fatal("requested again right after the timeout")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// at is value[i] for an array: Null when out of range.
func at(v any, i int) any {
	if items, ok := asArray(v); ok && i < len(items) {
		return items[i]
	}
	return nil
}

// Rust: lsp::tests::framing_handles_utf8_split_reads_and_rejects_bad_frames
func TestFramingHandlesUTF8SplitReadsAndRejectsBadFrames(t *testing.T) {
	value := object{"text": "界😀\r\n"}
	var buffer bytes.Buffer
	if err := writeMessage(&buffer, value); err != nil {
		t.Fatal(err)
	}
	if err := writeMessage(&buffer, nil); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(iotest.OneByteReader(&buffer))
	got, err := readMessage(reader)
	if err != nil || !equalJSON(got, value) {
		t.Fatalf("first message = %v, %v", got, err)
	}
	got, err = readMessage(reader)
	if err != nil || got != nil {
		t.Fatalf("second message = %v, %v", got, err)
	}
	for _, data := range []string{
		"Content-Length: 999999999\r\n\r\n",
		"Bad header\r\n\r\n",
		"Content-Length: 10\r\n\r\n{}",
		"Content-Length: 1\r\n\r\nx",
		"Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}",
		"\r\n{}",
	} {
		if message, err := readMessage(bufio.NewReader(strings.NewReader(data))); err == nil {
			t.Errorf("readMessage(%q) = %v, want an error", data, message)
		}
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader(strings.Repeat("x", 9000)))); err == nil {
		t.Error("an oversized header line was accepted")
	}
}

// Rust: lsp::tests::utf16_positions_handle_emoji_combining_marks_crlf_and_eof
func TestUTF16PositionsHandleEmojiCombiningMarksCRLFAndEOF(t *testing.T) {
	lines := bufferLines("a😀é\r\n界\r\n")
	for _, tc := range [][2]uint64{{0, 0}, {1, 1}, {2, 1}, {3, 2}, {4, 3}, {5, 4}, {99, 4}} {
		got, ok := scalarPosition(lines, wirePosition{line: 0, character: tc[0]})
		if !ok || got != (Position{0, int(tc[1])}) {
			t.Errorf("units %d: got %v, %v; want column %d", tc[0], got, ok, tc[1])
		}
	}
	if got, ok := scalarPosition(lines, wirePosition{line: 2, character: 0}); !ok || got != (Position{2, 0}) {
		t.Errorf("line 2 = %v, %v", got, ok)
	}
	if _, ok := scalarPosition(lines, wirePosition{line: 3, character: 0}); ok {
		t.Error("line 3 exists")
	}
	expectJSON(t, endPosition("a😀\r\n界😀"), `{"line":1,"character":3}`)
	expectJSON(t, endPosition("a😀\r\n"), `{"line":1,"character":0}`)
	expectJSON(t, endPosition(""), `{"line":0,"character":0}`)
}

// Rust: lsp::tests::languages_roots_and_new_file_uris
func TestLanguagesRootsAndNewFileURIs(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub space 界")
	if err := os.Mkdir(sub, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"Cargo.toml", "go.mod", ".marksman.toml"} {
		if err := os.WriteFile(filepath.Join(dir, marker), nil, 0o666); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		extension string
		language  language
		command   string
	}{
		{"rs", languageRust, "rust-analyzer"},
		{"go", languageGo, "gopls"},
		{"md", languageMarkdown, "marksman"},
		{"markdown", languageMarkdown, "marksman"},
		{"MD", languageMarkdown, "marksman"},
	} {
		path := filepath.Join(sub, "new #?界."+tc.extension)
		if got, ok := languageForPath(path); !ok || got != tc.language {
			t.Errorf("languageForPath(%q) = %v, %v", path, got, ok)
		}
		if program, _ := tc.language.command(); program != tc.command {
			t.Errorf("command = %q, want %q", program, tc.command)
		}
		if got := tc.language.root(path); got != dir {
			t.Errorf("root(%q) = %q, want %q", path, got, dir)
		}
		absolute, err := absolutePath(path)
		if err != nil {
			t.Fatal(err)
		}
		uri, ok := fileURI(absolute)
		if !ok || !strings.Contains(uri, "%23%3F") {
			t.Errorf("uri = %q, %v", uri, ok)
		}
		if back, ok := uriToPath(uri); !ok || back != absolute {
			t.Errorf("uriToPath(%q) = %q, %v; want %q", uri, back, ok, absolute)
		}
	}
	if _, ok := languageForPath("file.txt"); ok {
		t.Error("file.txt has a language")
	}
	if got := languageGo.root(filepath.Join(sub, "a.go")); got != dir {
		t.Errorf("go root = %q, want %q", got, dir)
	}
	if err := os.WriteFile(filepath.Join(sub, "go.work"), nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if got := languageGo.root(filepath.Join(sub, "a.go")); got != sub {
		t.Errorf("go.work root = %q, want %q", got, sub)
	}
}

// Rust: lsp::tests::typing_coalesces_but_preserves_saves_switches_and_stop
func TestTypingCoalescesButPreservesSavesSwitchesAndStop(t *testing.T) {
	commands, in := mailbox()
	for version := int32(1); version < 10000; version++ {
		if err := commands.trySend(inputDocument{*doc("typing", version)}); err != nil {
			t.Fatal(err)
		}
	}
	commands.queue.mu.Lock()
	queued := len(commands.queue.items)
	commands.queue.mu.Unlock()
	if queued != 1 {
		t.Fatalf("%d queued documents, want 1", queued)
	}
	saved := doc("saved", 10000)
	saved.saved = 1
	saved.saveEvent = true
	commands.trySend(inputDocument{*saved})
	afterSave := doc("unsaved again", 10001)
	afterSave.saved = 1
	commands.trySend(inputDocument{*afterSave})
	next := doc("next", 10002)
	next.generation = 1
	commands.trySend(inputDocument{*next})
	commands.trySend(inputStop{})
	for _, want := range [][2]uint64{{9999, 0}, {10000, 1}, {10001, 1}, {10002, 0}} {
		command, ok := in.recvTimeout(tick)
		if !ok {
			t.Fatal("mailbox ran dry")
		}
		got, isDoc := command.(inputDocument)
		if !isDoc {
			t.Fatalf("got %T, want a document", command)
		}
		if uint64(got.document.version) != want[0] || got.document.saved != want[1] {
			t.Fatalf("got version %d saved %d, want %v", got.document.version, got.document.saved, want)
		}
	}
	if command, ok := in.recvTimeout(tick); !ok || command != (inputStop{}) {
		t.Fatalf("got %v, %v; want stop", command, ok)
	}
}

// Rust: lsp::tests::synchronization_negotiates_full_incremental_and_disabled_changes
func TestSynchronizationNegotiatesFullIncrementalAndDisabledChanges(t *testing.T) {
	for kind := 0; kind <= 2; kind++ {
		tx := newOutbox(64)
		p := &protocol{desired: doc("a😀\r\n界", 1)}
		if err := p.flush(tx); err != nil {
			t.Fatal(err)
		}
		if !nothingSent(tx) { // No notification before initialize response.
			t.Fatal("flushed before the initialize response")
		}
		result := parse(t, `{"capabilities":{"textDocumentSync":{"openClose":true,"change":`+itoa(kind)+`,"save":{"includeText":true}}}}`)
		if err := p.initialize(result, tx); err != nil {
			t.Fatal(err)
		}
		expectJSON(t, index(recv(t, tx), "method"), `"initialized"`)
		expectJSON(t, index(index(index(recv(t, tx), "params"), "textDocument"), "text"), `"a😀\r\n界"`)
		changed := doc("changed", 2)
		changed.saved = 1
		p.desired = changed
		if err := p.flush(tx); err != nil {
			t.Fatal(err)
		}
		if kind != 0 {
			change := recv(t, tx)
			expectJSON(t, index(change, "method"), `"textDocument/didChange"`)
			params := index(change, "params")
			expectJSON(t, index(index(params, "textDocument"), "version"), `2`)
			first := at(index(params, "contentChanges"), 0)
			expectJSON(t, index(first, "text"), `"changed"`)
			if kind == 2 {
				expectJSON(t, index(index(first, "range"), "end"), `{"line":1,"character":1}`)
			} else if _, ok := get(first, "range"); ok {
				t.Error("full sync carries a range")
			}
		}
		save := recv(t, tx)
		expectJSON(t, index(save, "method"), `"textDocument/didSave"`)
		expectJSON(t, index(index(save, "params"), "text"), `"changed"`)
		renamed := doc("next", 3)
		renamed.uri = "file:///tmp/renamed.rs"
		renamed.generation = 1
		p.desired = renamed
		if err := p.flush(tx); err != nil {
			t.Fatal(err)
		}
		expectJSON(t, index(recv(t, tx), "method"), `"textDocument/didClose"`)
		expectJSON(t, index(index(index(recv(t, tx), "params"), "textDocument"), "uri"), `"file:///tmp/renamed.rs"`)
		if err := p.close(tx); err != nil {
			t.Fatal(err)
		}
		expectJSON(t, index(recv(t, tx), "method"), `"textDocument/didClose"`)
		if !nothingSent(tx) {
			t.Fatal("extra messages after close")
		}
	}
	tx := newOutbox(64)
	p := &protocol{}
	if err := p.initialize(parse(t, `{"capabilities":{"positionEncoding":"utf-8"}}`), tx); err == nil {
		t.Fatal("accepted a utf-8 position encoding")
	}
	if err := p.initialize(parse(t, `{"capabilities":{"textDocumentSync":2}}`), tx); err != nil {
		t.Fatal(err)
	}
	if p.change != 2 || !p.openClose || p.save != nil {
		t.Fatalf("change=%d openClose=%v save=%v", p.change, p.openClose, p.save)
	}
	recv(t, tx)
	err := p.reply(parse(t, `{"id":"config","method":"workspace/configuration","params":{"items":[{},{}]}}`), "file:///tmp", tx)
	if err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(recv(t, tx), "result"), `[null,null]`)
	if err := p.reply(parse(t, `{"id":9,"method":"unknown"}`), "file:///tmp", tx); err != nil {
		t.Fatal(err)
	}
	expectJSON(t, index(index(recv(t, tx), "error"), "code"), `-32601`)
}

// Rust: lsp::tests::diagnostics_reject_stale_versions_generations_and_other_files_and_clear_on_edits
func TestDiagnosticsRejectStaleVersionsGenerationsAndOtherFilesAndClearOnEdits(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), nil, 0o666); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.rs")
	uri, _ := fileURI(path)
	input, _ := mailbox()
	events := make(chan output, 64)
	finished := make(chan struct{})
	var client Client
	client.Enable(context.Background())
	client.session = &session{input: input, output: events, finished: finished}
	client.key = &languageKey{languageRust, root}
	buffer := "a😀b"
	lines := bufferLines(buffer)
	client.Sync(path, 0, 0, 0, func() string { return buffer })
	for _, tc := range []struct {
		generation uint64
		stamp      int32
		version    *int32
		otherURI   bool
	}{
		{1, 1, ptr(1), false},
		{0, 0, nil, false},
		{0, 1, ptr(0), false},
		{0, 1, ptr(1), true},
	} {
		target := uri
		if tc.otherURI {
			target = "file:///other"
		}
		events <- outputDiagnostics{generation: tc.generation, version: tc.stamp, publication: testPublication(target, tc.version, "stale")}
	}
	client.Poll(lines)
	if len(client.Diagnostics) != 0 {
		t.Fatal("stale diagnostics were accepted")
	}
	events <- outputDiagnostics{generation: 0, version: 1, publication: testPublication(uri, ptr(1), "warning 😀")}
	client.Poll(lines)
	if e, w := client.Counts(); e != 0 || w != 1 {
		t.Fatalf("counts = %d, %d", e, w)
	}
	if client.Diagnostics[0].End != (Position{0, 2}) {
		t.Fatalf("end = %v", client.Diagnostics[0].End)
	}
	if message, ok := client.AtCursor(0, 1); !ok || message != "warning 😀" {
		t.Fatalf("AtCursor = %q, %v", message, ok)
	}
	client.Sync(path, 0, 1, 0, func() string { return "updated" })
	if len(client.Diagnostics) != 0 {
		t.Fatal("an edit left diagnostics in place")
	}
	events <- outputDiagnostics{generation: 0, version: 2, publication: testPublication(uri, nil, "unversioned")}
	client.Poll(lines)
	if len(client.Diagnostics) != 1 {
		t.Fatalf("%d diagnostics, want 1", len(client.Diagnostics))
	}
	events <- outputDiagnostics{generation: 0, version: 2, publication: publication{uri: uri, version: 2, hasVersion: true}}
	client.Poll(lines)
	if len(client.Diagnostics) != 0 {
		t.Fatal("an empty publication did not clear diagnostics")
	}
	legend, ok := decodeLegend(index(semanticCapabilities(t), "semanticTokensProvider").(object)["legend"])
	if !ok {
		t.Fatal("legend")
	}
	data := []uint32{0, 1, 2, 0, 0}
	for _, tc := range []struct {
		generation uint64
		version    int32
		uri        string
	}{
		{1, 2, uri},
		{0, 1, uri},
		{0, 2, "file:///other.rs"},
	} {
		events <- outputTokens{document: tokenDocument{uri: tc.uri, generation: tc.generation, version: tc.version}, legend: legend, data: data, hasData: true}
	}
	client.Poll(lines)
	if len(client.SemanticTokens) != 0 {
		t.Fatal("stale tokens were accepted")
	}
	events <- outputTokens{document: tokenDocument{uri: uri, generation: 0, version: 2}, legend: legend, data: data, hasData: true}
	client.Poll(lines)
	if len(client.SemanticTokens) == 0 || client.SemanticTokens[0].Start != 1 || client.SemanticTokens[0].End != 2 {
		t.Fatalf("tokens = %v", client.SemanticTokens)
	}
	cacheRevision := client.SemanticRevision
	client.Sync(path, 0, 2, 0, func() string { return "edited again" })
	if len(client.SemanticTokens) != 0 || client.SemanticRevision <= cacheRevision {
		t.Fatal("an edit left semantic tokens in place")
	}
	events <- outputTokens{document: tokenDocument{uri: uri, generation: 0, version: 2}, legend: legend, data: data, hasData: true}
	client.Poll(lines)
	if len(client.SemanticTokens) != 0 {
		t.Fatal("tokens for the previous version were accepted")
	}
	events <- outputTokens{document: tokenDocument{uri: uri, generation: 0, version: 3}, legend: legend, data: data, hasData: true}
	client.Poll(lines)
	if len(client.SemanticTokens) != 1 {
		t.Fatalf("tokens = %v", client.SemanticTokens)
	}
	events <- outputFailed{"server crashed"}
	client.Poll(lines)
	if len(client.SemanticTokens) != 0 {
		t.Fatal("a failure left semantic tokens in place")
	}
	if message, ok := client.Failure(); !ok || message != "server crashed" {
		t.Fatalf("Failure = %q, %v", message, ok)
	}
	// Do not wait on a synthetic session with no worker.
	client.session = nil
	client.retired = nil
}

// fakeServer is the Python server of the Rust unit tests.
const fakeServer = `
import json, os, sys, time
mode, log = sys.argv[1:]
def send(value):
    data = json.dumps(value, ensure_ascii=False).encode()
    sys.stdout.buffer.write(('Content-Length: %d\r\n\r\n' % len(data)).encode() + data)
    sys.stdout.buffer.flush()
while True:
    headers = {}
    while True:
        line = sys.stdin.buffer.readline()
        if not line: sys.exit(0)
        if line == b'\r\n': break
        k, v = line.decode().split(':', 1)
        headers[k.lower()] = v.strip()
    msg = json.loads(sys.stdin.buffer.read(int(headers['content-length'])))
    with open(log, 'a') as f: f.write(json.dumps(msg) + '\n')
    method = msg.get('method')
    if method == 'initialize':
        if mode == 'hang': time.sleep(30)
        if mode == 'reject': send({'jsonrpc':'2.0','id':1,'error':{'code':-1,'message':'rejected'}}); continue
        if mode == 'malformed': sys.stdout.buffer.write(b'bad header\r\n\r\n'); sys.stdout.buffer.flush(); continue
        send({'jsonrpc':'2.0','id':1,'result':{'capabilities':{'textDocumentSync':{'openClose':True,'change':2,'save':True}}}})
        send({'jsonrpc':'2.0','id':'configuration','method':'workspace/configuration','params':{'items':[{'section':'gopls'}]}})
    elif method in ('textDocument/didOpen','textDocument/didChange'):
        doc = msg['params']['textDocument']
        send({'jsonrpc':'2.0','method':'textDocument/publishDiagnostics','params':{'uri':doc['uri'],'version':doc['version'],'diagnostics':[{'range':{'start':{'line':0,'character':0},'end':{'line':0,'character':1}},'severity':1,'message':'fake error'}]}})
        if mode == 'crash': os._exit(7)
    elif method == 'shutdown':
        if mode == 'stuck': time.sleep(30)
        send({'jsonrpc':'2.0','id':msg['id'],'result':None})
    elif method == 'exit': sys.exit(0)
`

type fakeRun struct {
	dir      string
	commands *commands
	events   chan output
	done     chan error
}

// fake runs serve against the Python server in the given mode.
func fake(t *testing.T, mode string, timeout time.Duration) *fakeRun {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake.py")
	if err := os.WriteFile(script, []byte(fakeServer), 0o666); err != nil {
		t.Fatal(err)
	}
	args := []string{"-u", script, mode, filepath.Join(dir, "log")}
	commands, in := mailbox()
	events := make(chan output, 64)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		done <- serve(ctx, "python3", args, dir, "0.1.0", in, events, timeout)
	}()
	return &fakeRun{dir: dir, commands: commands, events: events, done: done}
}

func (f *fakeRun) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-f.done:
		return err
	case <-time.After(timeout):
		t.Fatal("serve did not finish")
		return nil
	}
}

func receiveDiagnostic(t *testing.T, events chan output, version int32) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if d, ok := event.(outputDiagnostics); ok && d.publication.hasVersion && d.publication.version == version {
				if d.publication.diagnostics[0].message != "fake error" {
					t.Fatalf("message = %q", d.publication.diagnostics[0].message)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no diagnostic for version %d", version)
		}
	}
}

// Rust: lsp::tests::fake_process_roundtrip_saves_switches_and_graceful_shutdown
func TestFakeProcessRoundtripSavesSwitchesAndGracefulShutdown(t *testing.T) {
	run := fake(t, "normal", 2*time.Second)
	run.commands.trySend(inputDocument{*doc("a😀\r\n", 1)})
	receiveDiagnostic(t, run.events, 1)
	saved := doc("new", 2)
	saved.saved = 1
	run.commands.trySend(inputDocument{*saved})
	receiveDiagnostic(t, run.events, 2)
	next := doc("next", 3)
	next.generation = 1
	run.commands.trySend(inputDocument{*next})
	receiveDiagnostic(t, run.events, 3)
	run.commands.trySend(inputStop{})
	if err := run.wait(t, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	log := readLog(t, filepath.Join(run.dir, "log"))
	var methods []string
	for _, entry := range log {
		if method, ok := asString(index(entry, "method")); ok {
			methods = append(methods, method)
		}
	}
	want := []string{
		"initialize", "initialized", "textDocument/didOpen", "textDocument/didChange",
		"textDocument/didSave", "textDocument/didClose", "textDocument/didOpen",
		"textDocument/didClose", "shutdown", "exit",
	}
	if strings.Join(methods, " ") != strings.Join(want, " ") {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	found := false
	for _, entry := range log {
		if equalsString(index(entry, "id"), "configuration") && equalJSON(index(entry, "result"), []any{nil}) {
			found = true
		}
	}
	if !found {
		t.Fatal("the configuration reply was not recorded")
	}
}

// readLog parses the fake servers' JSON lines log.
func readLog(t *testing.T, path string) []any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		entries = append(entries, parse(t, line))
	}
	return entries
}

// Rust: lsp::tests::missing_crashed_rejected_malformed_and_stalled_servers_are_bounded
func TestMissingCrashedRejectedMalformedAndStalledServersAreBounded(t *testing.T) {
	dir := t.TempDir()
	_, in := mailbox()
	events := make(chan output, 64)
	err := serve(context.Background(), filepath.Join(dir, "absent"), nil, dir, "0.1.0", in, events, time.Second)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing server: %v", err)
	}
	for _, tc := range []struct{ mode, expected string }{
		{"crash", ""},
		{"reject", "initialize failed"},
		{"malformed", "invalid LSP header"},
		{"hang", "timed out"},
	} {
		run := fake(t, tc.mode, 300*time.Millisecond)
		run.commands.trySend(inputDocument{*doc("a", 1)})
		err := run.wait(t, 3*time.Second)
		if err == nil || !strings.Contains(err.Error(), tc.expected) {
			t.Errorf("%s: %v", tc.mode, err)
		}
	}
	run := fake(t, "stuck", 2*time.Second)
	run.commands.trySend(inputDocument{*doc("a", 1)})
	receiveDiagnostic(t, run.events, 1)
	run.commands.trySend(inputStop{})
	if err := run.wait(t, 2*time.Second); err != nil {
		t.Fatal(err)
	}
}
