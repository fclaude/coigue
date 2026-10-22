package lsp

// LSP 3.17 definition, references, document-symbol and workspace-symbol requests.
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/navigation"
)

const navigationTimeout = 5 * time.Second

// NavigationKind says which navigation request to make.
type NavigationKind uint8

// The four navigation requests.
const (
	Definition NavigationKind = iota
	References
	Symbols
	Workspace
)

// Title is the heading of the result list.
func (k NavigationKind) Title() string {
	switch k {
	case Definition:
		return "Definitions"
	case References:
		return "References"
	case Symbols:
		return "File symbols"
	}
	return "Workspace symbols"
}

func (k NavigationKind) method() string {
	switch k {
	case Definition:
		return "textDocument/definition"
	case References:
		return "textDocument/references"
	case Symbols:
		return "textDocument/documentSymbol"
	}
	return "workspace/symbol"
}

func (k NavigationKind) capability() string {
	switch k {
	case Definition:
		return "definitionProvider"
	case References:
		return "referencesProvider"
	case Symbols:
		return "documentSymbolProvider"
	}
	return "workspaceSymbolProvider"
}

type navigationRequest struct {
	serial    uint64
	kind      NavigationKind
	query     string
	document  tokenDocument
	row       int
	character int
}

func (r navigationRequest) id() string { return "navigation-" + strconv.FormatUint(r.serial, 10) }

type navigationPendingRequest struct {
	request navigationRequest
	text    string
	started time.Time
}

type navigationRequests struct {
	capabilities any
	queued       *navigationRequest
	pending      *navigationPendingRequest
}

func (n *navigationRequests) cancel(tx *outbox) error {
	n.queued = nil
	if old := n.pending; old != nil {
		n.pending = nil
		return send(tx, notify("$/cancelRequest", object{"id": old.request.id()}))
	}
	return nil
}

func (n *navigationRequests) queue(request navigationRequest, tx *outbox) error {
	if err := n.cancel(tx); err != nil {
		return err
	}
	n.queued = &request
	return nil
}

func publishNavigation(request navigationRequest, targets []navigation.Target, err error, events chan<- output) {
	// The UI also owns a deadline, so a saturated event channel cannot leave
	// an interactive request waiting indefinitely.
	trySend(events, outputNavigation{request: request, targets: targets, err: err})
}

func (n *navigationRequests) tick(doc *document, tx *outbox, events chan<- output) error {
	if n.pending != nil && time.Since(n.pending.started) >= navigationTimeout {
		pending := n.pending
		n.pending = nil
		if err := send(tx, notify("$/cancelRequest", object{"id": pending.request.id()})); err != nil {
			return err
		}
		publishNavigation(pending.request, nil,
			errors.New("Language-server navigation timed out; editing and project search remain available"), events)
	}
	if doc == nil {
		return nil
	}
	request := n.queued
	if request == nil {
		return nil
	}
	n.queued = nil
	if request.document != tokenDocumentOf(doc) {
		return nil
	}
	if supported := index(n.capabilities, request.kind.capability()); supported != true && !isObject(supported) {
		publishNavigation(*request, nil, fmt.Errorf(
			"The language server does not support %s; use project search instead",
			strings.ToLower(request.kind.Title())), events)
		return nil
	}
	params := object{"textDocument": object{"uri": request.document.uri}}
	switch request.kind {
	case Definition, References:
		params["position"] = object{"line": request.row, "character": request.character}
		if request.kind == References {
			params["context"] = object{"includeDeclaration": true}
		}
	case Workspace:
		params = object{"query": request.query}
	case Symbols:
	}
	err := send(tx, object{
		"jsonrpc": "2.0",
		"id":      request.id(),
		"method":  request.kind.method(),
		"params":  params,
	})
	if err != nil {
		return err
	}
	n.pending = &navigationPendingRequest{request: *request, text: doc.text, started: time.Now()}
	return nil
}

func (n *navigationRequests) response(value any, events chan<- output) {
	if n.pending == nil || !equalsString(index(value, "id"), n.pending.request.id()) {
		return
	}
	pending := n.pending
	n.pending = nil
	var targets []navigation.Target
	var err error
	if failure, ok := get(value, "error"); ok {
		message, ok := asString(index(failure, "message"))
		if !ok {
			message = "request rejected"
		}
		err = errors.New("Language-server navigation failed: " + message)
	} else if result, ok := get(value, "result"); ok {
		targets, err = parseTargets(result, pending.request.document.uri, pending.text)
	} else {
		err = errors.New("Invalid language-server navigation response")
	}
	publishNavigation(pending.request, targets, err, events)
}

const maxTargetFile = 2 * 1024 * 1024

func splitLines(text string) [][]rune {
	parts := strings.Split(text, "\n")
	lines := make([][]rune, len(parts))
	for i, part := range parts {
		lines[i] = []rune(part)
	}
	return lines
}

// readTargetFile reads another file the server pointed at: a regular UTF-8
// file of at most 2 MiB, or nothing.
func readTargetFile(path string) ([][]rune, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxTargetFile {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxTargetFile+1))
	if err != nil || len(data) > maxTargetFile || !utf8.Valid(data) {
		return nil, false
	}
	return splitLines(string(data)), true
}

type targetText struct {
	lines [][]rune
	ok    bool
}

type pendingValue struct {
	value  any
	parent string
	depth  int
}

// parseTargets normalizes Location, LocationLink, flat SymbolInformation and
// hierarchical DocumentSymbol responses to scalar positions. This runs on the
// worker, with the exact unsaved source text attached to the request, never on
// the UI thread.
func parseTargets(result any, currentURI, currentText string) ([]navigation.Target, error) {
	if result == nil {
		return []navigation.Target{}, nil
	}
	var queue []pendingValue
	if values, ok := asArray(result); ok {
		for _, value := range values {
			queue = append(queue, pendingValue{value: value})
		}
	} else if isObject(result) {
		queue = append(queue, pendingValue{value: result})
	} else {
		return nil, errors.New("Invalid navigation result")
	}
	texts := map[string]targetText{currentURI: {lines: splitLines(currentText), ok: true}}
	targets := []navigation.Target{}
	rejected := 0
	visited := 0
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		visited++
		if len(targets) == 1000 || visited > 10000 {
			break
		}
		name, _ := asString(index(item.value, "name"))
		title := name
		if item.parent != "" {
			title = item.parent + " / " + name
		}
		if item.depth < 32 {
			if children, ok := asArray(index(item.value, "children")); ok {
				for _, child := range children[:min(len(children), 1000)] {
					queue = append(queue, pendingValue{value: child, parent: title, depth: item.depth + 1})
				}
			}
		}
		location := item.value
		if inner, ok := get(item.value, "location"); ok {
			location = inner
		}
		var uri string
		if u, ok := asString(index(location, "uri")); ok {
			uri = u
		} else if u, ok := asString(index(location, "targetUri")); ok {
			uri = u
		} else if _, ok := get(location, "selectionRange"); ok && name != "" {
			uri = currentURI
		} else {
			rejected++
			continue
		}
		var start any
		hasStart := false
		for _, key := range []string{"targetSelectionRange", "selectionRange", "range"} {
			if r, ok := get(location, key); ok {
				start, hasStart = get(r, "start")
				break
			}
		}
		if !hasStart {
			rejected++
			continue
		}
		position, ok := decodePosition(start)
		if !ok {
			rejected++
			continue
		}
		path, ok := uriToPath(uri)
		if !ok {
			rejected++
			continue
		}
		text, loaded := texts[uri]
		if !loaded {
			text.lines, text.ok = readTargetFile(path)
			texts[uri] = text
		}
		var scalar Position
		if text.ok {
			scalar, ok = scalarPosition(text.lines, position)
		}
		if !text.ok || !ok {
			rejected++
			continue
		}
		loc := navigation.NewLocation(path)
		loc.Row = scalar.Row
		loc.Col = scalar.Col
		duplicate := false
		for _, t := range targets {
			if t.Location == loc && strings.HasPrefix(t.Label, title) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		prefix := ""
		if title != "" {
			prefix = title + "  "
		}
		label := fmt.Sprintf("%s%s:%d:%d", prefix, displayPath(loc.Path), scalar.Row+1, scalar.Col+1)
		targets = append(targets, navigation.Target{Location: loc, Label: label})
	}
	if len(targets) == 0 && rejected > 0 {
		return nil, errors.New("No usable local-file locations in the server response")
	}
	return targets, nil
}

// displayPath is Path::display: invalid UTF-8 shows as U+FFFD, one per
// maximal invalid subsequence (str::from_utf8_lossy).
func displayPath(path string) string {
	if utf8.ValidString(path) {
		return path
	}
	var b strings.Builder
	for len(path) > 0 {
		r, size := utf8.DecodeRuneInString(path)
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
			path = path[invalidPrefixLength(path):]
			continue
		}
		b.WriteString(path[:size])
		path = path[size:]
	}
	return b.String()
}

// invalidPrefixLength is the length of the longest prefix of s that could
// start a valid UTF-8 sequence (at least 1), for s that does not decode.
func invalidPrefixLength(s string) int {
	lead := s[0]
	var lo, hi byte = 0x80, 0xbf
	need := 0
	switch {
	case lead >= 0xc2 && lead <= 0xdf:
		need = 1
	case lead == 0xe0:
		need, lo = 2, 0xa0
	case lead >= 0xe1 && lead <= 0xec, lead == 0xee, lead == 0xef:
		need = 2
	case lead == 0xed:
		need, hi = 2, 0x9f
	case lead == 0xf0:
		need, lo = 3, 0x90
	case lead >= 0xf1 && lead <= 0xf3:
		need = 3
	case lead == 0xf4:
		need, hi = 3, 0x8f
	default:
		return 1
	}
	n := 1
	for i := 1; i <= need && i < len(s); i++ {
		if s[i] < lo || s[i] > hi {
			break
		}
		lo, hi = 0x80, 0xbf
		n++
	}
	return n
}
