package lsp

import (
	"errors"
	"time"

	"github.com/fclaude/coigue/internal/semantic"
)

// errPipeStalled is what a full or dead outgoing queue reports.
var errPipeStalled = errors.New("server pipe stalled")

// outbox is the bounded hand-off from the session worker to the goroutine that
// writes the server's stdin. Sending never blocks; dead is closed when the
// writer has gone, so a stalled or broken pipe is noticed at once.
type outbox struct {
	messages chan any
	dead     chan struct{}
}

func newOutbox(capacity int) *outbox {
	return &outbox{messages: make(chan any, capacity), dead: make(chan struct{})}
}

func send(tx *outbox, value any) error {
	select {
	case <-tx.dead:
		return errPipeStalled
	default:
	}
	select {
	case tx.messages <- value:
		return nil
	default:
		return errPipeStalled
	}
}

type tokenDocument struct {
	uri        string
	generation uint64
	version    int32
}

func tokenDocumentOf(d *document) tokenDocument {
	return tokenDocument{uri: d.uri, generation: d.generation, version: d.version}
}

type tokenRequest struct {
	id       uint64
	document tokenDocument
	started  time.Time
}

// tokenRequests drives textDocument/semanticTokens/full for the document the
// server currently has open: one request at a time, cancelled when the
// document changes, retried only for the server's "content modified" and
// "server cancelled" errors.
type tokenRequests struct {
	legend    *semantic.Legend
	pending   *tokenRequest
	document  *tokenDocument
	requested bool
	nextID    uint64
	retries   uint8
	retryAt   time.Time // zero when no retry is scheduled
}

func decodeLegend(v any) (semantic.Legend, bool) {
	values, _, ok := structFields(v, "tokenTypes", "tokenModifiers")
	if !ok {
		return semantic.Legend{}, false
	}
	types, ok := toStrings(values[0])
	if !ok {
		return semantic.Legend{}, false
	}
	modifiers, ok := toStrings(values[1])
	if !ok {
		return semantic.Legend{}, false
	}
	return semantic.Legend{TokenTypes: types, TokenModifiers: modifiers}, true
}

func (t *tokenRequests) configure(capabilities any) {
	provider := index(capabilities, "semanticTokensProvider")
	if full := index(provider, "full"); full == true || isObject(full) {
		t.legend = nil
		if legend, ok := decodeLegend(index(provider, "legend")); ok {
			t.legend = &legend
		}
	}
}

func (t *tokenRequests) cancel(tx *outbox) error {
	if request := t.pending; request != nil {
		t.pending = nil
		return send(tx, notify("$/cancelRequest", object{"id": request.id}))
	}
	return nil
}

func (t *tokenRequests) refresh(tx *outbox) error {
	if err := t.cancel(tx); err != nil {
		return err
	}
	t.requested = false
	t.retries = 0
	t.retryAt = time.Time{}
	return nil
}

func (t *tokenRequests) publish(request *tokenRequest, data []uint32, hasData bool, events chan<- output) {
	if t.legend != nil {
		trySend(events, outputTokens{document: request.document, legend: *t.legend, data: data, hasData: hasData})
	}
}

func (t *tokenRequests) request(doc *document, tx *outbox, events chan<- output) error {
	if t.legend == nil || (doc.language != languageRust && doc.language != languageGo) {
		return nil
	}
	key := tokenDocumentOf(doc)
	if t.document == nil || *t.document != key {
		if err := t.refresh(tx); err != nil {
			return err
		}
		t.document = &key
	}
	if t.pending != nil && time.Since(t.pending.started) >= tokenTimeout {
		request := t.pending
		t.pending = nil
		if err := send(tx, notify("$/cancelRequest", object{"id": request.id})); err != nil {
			return err
		}
		t.publish(request, nil, false, events)
	}
	if t.pending != nil || t.requested || (!t.retryAt.IsZero() && t.retryAt.After(time.Now())) {
		return nil
	}
	t.nextID = max(t.nextID, 2) + 1
	err := send(tx, object{
		"jsonrpc": "2.0",
		"id":      t.nextID,
		"method":  "textDocument/semanticTokens/full",
		"params":  object{"textDocument": object{"uri": doc.uri}},
	})
	if err != nil {
		return err
	}
	t.pending = &tokenRequest{id: t.nextID, document: key, started: time.Now()}
	t.requested = true
	return nil
}

// decodeTokens is serde_json::from_value::<Tokens>, with Tokens { data: Vec<u32> }.
func decodeTokens(v any) ([]uint32, bool) {
	values, _, ok := structFields(v, "data")
	if !ok {
		return nil, false
	}
	items, ok := asArray(values[0])
	if !ok {
		return nil, false
	}
	data := make([]uint32, 0, len(items))
	for _, item := range items {
		n, ok := toUint(item, 32)
		if !ok {
			return nil, false
		}
		data = append(data, uint32(n))
	}
	return data, true
}

func (t *tokenRequests) response(value any, events chan<- output) {
	if t.pending == nil {
		return
	}
	if id, ok := asU64(index(value, "id")); !ok || id != t.pending.id {
		return
	}
	request := t.pending
	t.pending = nil
	var data []uint32
	hasData := false
	if _, failed := get(value, "error"); failed {
		// no data
	} else if result := index(value, "result"); result == nil {
		data, hasData = []uint32{}, true
	} else if tokens, ok := decodeTokens(result); ok {
		data, hasData = tokens, true
	}
	t.publish(request, data, hasData, events)
	if code, ok := asI64(index(index(value, "error"), "code")); ok && (code == -32801 || code == -32802) && t.retries < 2 {
		t.retries++
		t.requested = false
		t.retryAt = time.Now().Add(250 * time.Millisecond)
	}
}

// protocol is the session worker's view of the conversation: what the editor
// wants the server to have open, what it has been told, and what the server
// agreed to during initialize.
type protocol struct {
	desired    *document
	sent       *document
	ready      bool
	change     uint64
	openClose  bool
	save       *bool
	tokens     tokenRequests
	navigation navigationRequests
	// gopls (the Rust field is called go) is true for gopls, which is asked
	// to enable semantic tokens.
	gopls bool
}

func (p *protocol) initialize(result any, tx *outbox) error {
	caps := index(result, "capabilities")
	if encoding, ok := asString(index(caps, "positionEncoding")); ok && encoding != "utf-16" {
		return errors.New("server selected an unsupported position encoding")
	}
	p.tokens.configure(caps)
	p.navigation.capabilities = caps
	sync := index(caps, "textDocumentSync")
	if kind, ok := asU64(sync); ok {
		p.change = kind
		p.openClose = kind != 0
	} else {
		p.change, _ = asU64(index(sync, "change"))
		p.openClose, _ = asBool(index(sync, "openClose"))
		p.save = nil
		switch save := index(sync, "save"); {
		case save == true:
			p.save = new(bool)
		case isObject(save):
			includeText, _ := asBool(index(save, "includeText"))
			p.save = &includeText
		}
	}
	p.ready = true
	if err := send(tx, notify("initialized", object{})); err != nil {
		return err
	}
	return p.flush(tx)
}

func (p *protocol) close(tx *outbox) error {
	if err := p.navigation.cancel(tx); err != nil {
		return err
	}
	if err := p.tokens.refresh(tx); err != nil {
		return err
	}
	p.tokens.document = nil
	old := p.sent
	p.sent = nil
	if old != nil && p.openClose {
		return send(tx, notify("textDocument/didClose", object{"textDocument": object{"uri": old.uri}}))
	}
	return nil
}

func (p *protocol) flush(tx *outbox) error {
	if !p.ready {
		return nil
	}
	next := p.desired
	if next == nil {
		return nil
	}
	p.desired = nil
	if p.sent != nil && (p.sent.uri != next.uri || p.sent.generation != next.generation) {
		if err := p.close(tx); err != nil {
			return err
		}
	}
	if old := p.sent; old != nil {
		if old.version != next.version && p.change != 0 {
			var change object
			if p.change == 2 {
				// A whole-document ranged replacement is valid incremental
				// sync and avoids coupling the protocol to every edit command.
				change = object{
					"range": object{
						"start": object{"line": 0, "character": 0},
						"end":   endPosition(old.text),
					},
					"text": next.text,
				}
			} else {
				change = object{"text": next.text}
			}
			err := send(tx, notify("textDocument/didChange", object{
				"textDocument":   object{"uri": next.uri, "version": next.version},
				"contentChanges": []any{change},
			}))
			if err != nil {
				return err
			}
		}
		if old.saved != next.saved && p.save != nil {
			params := object{"textDocument": object{"uri": next.uri}}
			if *p.save {
				params["text"] = next.text
			}
			if err := send(tx, notify("textDocument/didSave", params)); err != nil {
				return err
			}
		}
	} else if p.openClose {
		err := send(tx, notify("textDocument/didOpen", object{"textDocument": object{
			"uri":        next.uri,
			"languageId": next.language.id(),
			"version":    next.version,
			"text":       next.text,
		}}))
		if err != nil {
			return err
		}
	}
	p.sent = next
	return nil
}

// reply answers a request from the server. We announce only what we handle,
// but servers ask for configuration and progress anyway.
func (p *protocol) reply(value any, root string, tx *outbox) error {
	id := index(value, "id")
	method, _ := asString(index(value, "method"))
	var result any
	switch method {
	case "workspace/configuration":
		items := []any{}
		if requested, ok := asArray(index(index(value, "params"), "items")); ok {
			for _, item := range requested {
				section, _ := asString(index(item, "section"))
				switch {
				case section == "gopls" && p.gopls:
					items = append(items, object{"semanticTokens": true})
				case section == "gopls.semanticTokens" && p.gopls:
					items = append(items, true)
				default:
					items = append(items, nil)
				}
			}
		}
		result = items
	case "workspace/semanticTokens/refresh":
		if err := p.tokens.refresh(tx); err != nil {
			return err
		}
	case "workspace/workspaceFolders":
		result = []any{object{"uri": root, "name": "workspace"}}
	case "window/workDoneProgress/create", "window/showMessageRequest":
	case "workspace/applyEdit":
		result = object{"applied": false, "failureReason": "Coigüe does not apply workspace edits"}
	default:
		return send(tx, object{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   object{"code": -32601, "message": "Unsupported client request"},
		})
	}
	return send(tx, object{"jsonrpc": "2.0", "id": id, "result": result})
}
