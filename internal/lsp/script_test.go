package lsp

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Protocol scripts: sequences of operations on the worker's Protocol
// (initialize, document flushes, closes, replies to server requests, semantic
// token and navigation requests and their responses, with timeouts forced by
// ageing the pending request) were generated at random and run through the
// real src/lsp.rs by a scratch harness. Every step records the messages sent
// to the server, the events handed to the UI and a summary of the protocol
// state; the Go port must reproduce all three, step by step.

func jsonUint(v any) uint64 {
	u, _ := asU64(v)
	return u
}

func scriptDocument(v any) *document {
	d := &document{
		uri:        index(v, "uri").(string),
		generation: jsonUint(index(v, "generation")),
		saved:      jsonUint(index(v, "saved")),
		text:       index(v, "text").(string),
	}
	version, _ := asI64(index(v, "version"))
	d.version = int32(version)
	d.saveEvent, _ = asBool(index(v, "save_event"))
	switch index(v, "language") {
	case "rust":
		d.language = languageRust
	case "go":
		d.language = languageGo
	default:
		d.language = languageMarkdown
	}
	return d
}

func scriptState(p *protocol) object {
	state := object{
		"ready": p.ready, "change": p.change, "open_close": p.openClose, "go": p.gopls,
		"save": nil, "desired": nil, "sent": nil,
	}
	if p.save != nil {
		state["save"] = *p.save
	}
	if p.desired != nil {
		state["desired"] = p.desired.version
	}
	if p.sent != nil {
		state["sent"] = []any{p.sent.uri, p.sent.version, p.sent.generation, p.sent.saved}
	}
	tokens := object{
		"legend": p.tokens.legend != nil, "pending": nil, "document": nil,
		"requested": p.tokens.requested, "next_id": p.tokens.nextID, "retries": p.tokens.retries,
		"retry_pending": !p.tokens.retryAt.IsZero(),
	}
	if p.tokens.pending != nil {
		tokens["pending"] = p.tokens.pending.id
	}
	if d := p.tokens.document; d != nil {
		tokens["document"] = []any{d.uri, d.generation, d.version}
	}
	state["tokens"] = tokens
	navigation := object{"queued": nil, "pending": nil}
	if p.navigation.queued != nil {
		navigation["queued"] = p.navigation.queued.serial
	}
	if p.navigation.pending != nil {
		navigation["pending"] = p.navigation.pending.request.serial
	}
	state["navigation"] = navigation
	caps := p.navigation.capabilities
	state["caps"] = []any{
		index(caps, "definitionProvider"), index(caps, "referencesProvider"),
		index(caps, "documentSymbolProvider"), index(caps, "workspaceSymbolProvider"),
	}
	return state
}

func scriptEvent(event output) any {
	switch e := event.(type) {
	case outputNavigation:
		result := object{}
		if e.err != nil {
			result["err"] = e.err.Error()
		} else {
			labels := []any{}
			for _, target := range e.targets {
				labels = append(labels, target.Label)
			}
			result["ok"] = labels
		}
		return object{"nav": object{"serial": e.request.serial, "uri": e.request.document.uri, "result": result}}
	case outputTokens:
		var data any
		if e.hasData {
			list := []any{}
			for _, n := range e.data {
				list = append(list, n)
			}
			data = list
		}
		return object{"tokens": object{
			"version": e.document.version, "uri": e.document.uri, "generation": e.document.generation,
			"types": e.legend.TokenTypes, "modifiers": e.legend.TokenModifiers, "data": data,
		}}
	case outputReady:
		return "ready"
	}
	return "unexpected"
}

func runScript(t *testing.T, ops []any) []any {
	t.Helper()
	tx := newOutbox(64)
	events := make(chan output, 64)
	p := &protocol{}
	var records []any
	for _, op := range ops {
		var err error
		switch name, _ := asString(index(op, "o")); name {
		case "gopls":
			p.gopls, _ = asBool(index(op, "on"))
		case "init":
			err = p.initialize(index(op, "result"), tx)
		case "desired":
			p.desired = scriptDocument(index(op, "doc"))
		case "flush":
			err = p.flush(tx)
		case "close":
			err = p.close(tx)
		case "reply":
			root, _ := asString(index(op, "root"))
			err = p.reply(index(op, "value"), root, tx)
		case "tokreq":
			if p.sent != nil {
				err = p.tokens.request(p.sent, tx, events)
			}
		case "tokresp":
			p.tokens.response(index(op, "value"), events)
		case "tokrespp":
			value := index(op, "value").(object)
			if p.tokens.pending != nil {
				delta, _ := asI64(index(op, "delta"))
				value["id"] = json.Number(strconv.FormatInt(int64(p.tokens.pending.id)+delta, 10))
			}
			p.tokens.response(value, events)
		case "tokage":
			if p.tokens.pending != nil {
				ms, _ := asU64(index(op, "ms"))
				p.tokens.pending.started = time.Now().Add(-time.Duration(ms) * time.Millisecond)
			}
		case "retryclear":
			p.tokens.retryAt = time.Time{}
		case "navqueue":
			r := index(op, "request")
			kind, _ := asU64(index(r, "kind"))
			version, _ := asI64(index(r, "version"))
			row, _ := asU64(index(r, "row"))
			character, _ := asU64(index(r, "character"))
			query, _ := asString(index(r, "query"))
			uri, _ := asString(index(r, "uri"))
			err = p.navigation.queue(navigationRequest{
				serial: jsonUint(index(r, "serial")), kind: NavigationKind(kind), query: query,
				document: tokenDocument{uri: uri, generation: jsonUint(index(r, "generation")), version: int32(version)},
				row:      int(row), character: int(character),
			}, tx)
		case "navtick":
			err = p.navigation.tick(p.sent, tx, events)
		case "navresp":
			p.navigation.response(index(op, "value"), events)
		case "navcancel":
			err = p.navigation.cancel(tx)
		case "navage":
			if p.navigation.pending != nil {
				ms, _ := asU64(index(op, "ms"))
				p.navigation.pending.started = time.Now().Add(-time.Duration(ms) * time.Millisecond)
			}
		default:
			t.Fatalf("unknown op %s", marshal(op))
		}
		record := object{"r": "ok", "state": scriptState(p)}
		if err != nil {
			record["r"] = "err: " + err.Error()
		}
		sent := []any{}
		for drained := false; !drained; {
			select {
			case m := <-tx.messages:
				sent = append(sent, m)
			default:
				drained = true
			}
		}
		record["sent"] = sent
		emitted := []any{}
		for drained := false; !drained; {
			select {
			case e := <-events:
				emitted = append(emitted, scriptEvent(e))
			default:
				drained = true
			}
		}
		record["events"] = emitted
		records = append(records, record)
	}
	return records
}

func checkScriptCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
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
		records := runScript(t, ops)
		checked++
		for i, record := range records {
			got := canonicalText(t, string(marshal(record)))
			want := canonicalText(t, string(entry.Out[i]))
			if got != want {
				mismatches++
				if mismatches <= 10 {
					t.Errorf("script %d step %d (%s):\n got %.800s\nwant %.800s", checked, i, marshal(ops[i]), got, want)
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

func TestProtocolScriptsAgreeWithRustGolden(t *testing.T) {
	if checked, _ := checkScriptCorpus(t, "testdata/script_golden.jsonl.gz"); checked < 100 {
		t.Fatalf("only %d golden scripts", checked)
	}
}

func TestProtocolScriptsAgreeWithRustCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_SCRIPT_CORPUS")
	if path == "" {
		t.Skip("COIGUE_SCRIPT_CORPUS not set")
	}
	checked, mismatches := checkScriptCorpus(t, path)
	t.Logf("%d scripts, %d with a mismatch", checked, mismatches)
}
