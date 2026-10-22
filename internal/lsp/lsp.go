// Package lsp talks to optional language servers (rust-analyzer, gopls,
// marksman): diagnostics, semantic tokens and navigation. The UI never touches
// server pipes. This deliberately models only our small protocol subset,
// rather than an entire LSP SDK.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/semantic"
)

const (
	tick            = 20 * time.Millisecond
	changeInterval  = 75 * time.Millisecond
	startupTimeout  = 20 * time.Second
	shutdownTimeout = 500 * time.Millisecond
	tokenTimeout    = 5 * time.Second

	// pollBound is how many server events one Poll handles, so a flood of
	// notifications cannot stall the UI.
	pollBound = 64
	// queueLimit is the capacity of the command mailbox (Stop is exempt).
	queueLimit = 32
	// channelCapacity is the capacity of the event, outgoing and incoming
	// channels of a session.
	channelCapacity = 64
	// defaultVersion is clientInfo.version when the editor sets none.
	defaultVersion = "0.1.0"
)

type language uint8

const (
	languageRust language = iota
	languageGo
	languageMarkdown
)

func languageForPath(path string) (language, bool) {
	ext, ok := fsx.Extension(path)
	if !ok || !utf8.ValidString(ext) {
		return 0, false
	}
	switch asciiLower(ext) {
	case "rs":
		return languageRust, true
	case "go":
		return languageGo, true
	case "md", "markdown":
		return languageMarkdown, true
	}
	return 0, false
}

// asciiLower is str::to_ascii_lowercase.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func (l language) id() string {
	switch l {
	case languageRust:
		return "rust"
	case languageGo:
		return "go"
	}
	return "markdown"
}

func (l language) command() (string, []string) {
	switch l {
	case languageRust:
		return "rust-analyzer", nil
	case languageGo:
		return "gopls", nil
	}
	return "marksman", []string{"server"}
}

// root is the nearest ancestor directory holding a project marker, or the
// file's own directory.
func (l language) root(path string) string {
	parent, ok := fsx.Parent(path)
	if !ok {
		parent = "/"
	}
	var markers []string
	switch l {
	case languageRust:
		markers = []string{"Cargo.toml"}
	case languageGo:
		markers = []string{"go.work", "go.mod"}
	default:
		markers = []string{".marksman.toml", ".git"}
	}
	for dir := parent; ; {
		for _, name := range markers {
			if fsx.Exists(fsx.Join(dir, name)) {
				return dir
			}
		}
		next, ok := fsx.Parent(dir)
		if !ok {
			return parent
		}
		dir = next
	}
}

// wirePosition is the protocol's Position: a line and a UTF-16 offset.
type wirePosition struct {
	line      uint64
	character uint64
}

type wireRange struct {
	start wirePosition
	end   wirePosition
}

type wireDiagnostic struct {
	rng         wireRange
	severity    uint8
	hasSeverity bool
	message     string
}

type publication struct {
	uri         string
	version     int32
	hasVersion  bool
	diagnostics []wireDiagnostic
}

// decodePosition reads a Position as serde would derive it.
func decodePosition(v any) (wirePosition, bool) {
	values, _, ok := structFields(v, "line", "character")
	if !ok {
		return wirePosition{}, false
	}
	line, ok := toUint(values[0], 64)
	if !ok {
		return wirePosition{}, false
	}
	character, ok := toUint(values[1], 64)
	if !ok {
		return wirePosition{}, false
	}
	return wirePosition{line, character}, true
}

func decodeRange(v any) (wireRange, bool) {
	values, _, ok := structFields(v, "start", "end")
	if !ok {
		return wireRange{}, false
	}
	start, ok := decodePosition(values[0])
	if !ok {
		return wireRange{}, false
	}
	end, ok := decodePosition(values[1])
	if !ok {
		return wireRange{}, false
	}
	return wireRange{start, end}, true
}

func decodeWireDiagnostic(v any) (wireDiagnostic, bool) {
	values, _, ok := structFields(v, "range", "severity", "message")
	if !ok {
		return wireDiagnostic{}, false
	}
	var d wireDiagnostic
	if d.rng, ok = decodeRange(values[0]); !ok {
		return wireDiagnostic{}, false
	}
	if values[1] != nil {
		severity, ok := toUint(values[1], 8)
		if !ok {
			return wireDiagnostic{}, false
		}
		d.severity, d.hasSeverity = uint8(severity), true
	}
	if d.message, ok = asString(values[2]); !ok {
		return wireDiagnostic{}, false
	}
	return d, true
}

// decodePublication is serde_json::from_value::<Publication>.
func decodePublication(v any) (publication, bool) {
	values, _, ok := structFields(v, "uri", "version", "diagnostics")
	if !ok {
		return publication{}, false
	}
	var p publication
	if p.uri, ok = asString(values[0]); !ok {
		return publication{}, false
	}
	if values[1] != nil {
		version, ok := toInt32(values[1])
		if !ok {
			return publication{}, false
		}
		p.version, p.hasVersion = version, true
	}
	items, ok := asArray(values[2])
	if !ok {
		return publication{}, false
	}
	for _, item := range items {
		d, ok := decodeWireDiagnostic(item)
		if !ok {
			return publication{}, false
		}
		p.diagnostics = append(p.diagnostics, d)
	}
	return p, true
}

// Position is a (row, column) pair in characters. Positions order by row,
// then column, like the Rust tuples they were ported from.
type Position struct {
	Row int
	Col int
}

// Less reports whether p comes before q.
func (p Position) Less(q Position) bool {
	return p.Row < q.Row || (p.Row == q.Row && p.Col < q.Col)
}

// Diagnostic is one server diagnostic in character positions. Severity is 1
// (error) to 4 (hint).
type Diagnostic struct {
	Start    Position
	End      Position
	Severity uint8
	Message  string
}

func (d Diagnostic) coversLine(row int) bool {
	return row >= d.Start.Row &&
		(row < d.End.Row || (row == d.End.Row && (d.End.Col > 0 || d.Start == d.End)))
}

func (d Diagnostic) contains(row, col int) bool {
	pos := Position{row, col}
	return !pos.Less(d.Start) && (pos.Less(d.End) || (d.Start == d.End && pos == d.Start))
}

type document struct {
	uri        string
	generation uint64
	version    int32
	saved      uint64
	saveEvent  bool
	language   language
	text       string
}

type stamp struct {
	path       string
	generation uint64
	revision   uint64
	saved      uint64
}

// input is a command for the session worker.
type input interface{ isInput() }

type (
	inputDocument         struct{ document document }
	inputNavigate         struct{ request navigationRequest }
	inputCancelNavigation struct{}
	inputStop             struct{}
)

func (inputDocument) isInput()         {}
func (inputNavigate) isInput()         {}
func (inputCancelNavigation) isInput() {}
func (inputStop) isInput()             {}

// output is an event from the session worker.
type output interface{ isOutput() }

type (
	outputNavigation struct {
		request navigationRequest
		targets []navigation.Target
		err     error
	}
	outputReady       struct{}
	outputDiagnostics struct {
		generation  uint64
		version     int32
		publication publication
	}
	outputFailed struct{ message string }
	outputTokens struct {
		document tokenDocument
		legend   semantic.Legend
		data     []uint32
		hasData  bool
	}
)

func (outputNavigation) isOutput()  {}
func (outputReady) isOutput()       {}
func (outputDiagnostics) isOutput() {}
func (outputFailed) isOutput()      {}
func (outputTokens) isOutput()      {}

// trySend is a non-blocking send on a bounded event channel: when it is
// full the event is dropped (events are replaceable, and the UI keeps its own
// deadlines).
func trySend(events chan<- output, event output) {
	select {
	case events <- event:
	default:
	}
}

type session struct {
	input    *commands
	output   <-chan output
	finished <-chan struct{}
}

// Coalesce typing while retaining save and file-switch boundaries. A slow or
// initializing server must not accumulate one whole-buffer copy per keystroke.
type commandQueue struct {
	mu    sync.Mutex
	items []input
}

type commands struct {
	queue *commandQueue
	wake  chan struct{}
}

type inbox struct {
	queue *commandQueue
	wake  chan struct{}
}

func mailbox() (*commands, *inbox) {
	queue := &commandQueue{}
	wake := make(chan struct{}, 1)
	return &commands{queue: queue, wake: wake}, &inbox{queue: queue, wake: wake}
}

var errQueueFull = errors.New("LSP command queue full")

func (c *commands) trySend(in input) error {
	c.queue.mu.Lock()
	defer c.queue.mu.Unlock()
	items := c.queue.items
	if next, ok := in.(inputDocument); ok && len(items) > 0 {
		if old, ok := items[len(items)-1].(inputDocument); ok &&
			old.document.uri == next.document.uri &&
			old.document.generation == next.document.generation &&
			old.document.saved == next.document.saved &&
			!old.document.saveEvent && !next.document.saveEvent {
			items[len(items)-1] = next
			c.wakeUp()
			return nil
		}
	}
	if _, stop := in.(inputStop); len(items) >= queueLimit && !stop {
		return errQueueFull
	}
	c.queue.items = append(items, in)
	c.wakeUp()
	return nil
}

func (c *commands) wakeUp() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (b *inbox) pop() (input, bool) {
	b.queue.mu.Lock()
	defer b.queue.mu.Unlock()
	if len(b.queue.items) == 0 {
		return nil, false
	}
	in := b.queue.items[0]
	b.queue.items[0] = nil
	b.queue.items = b.queue.items[1:]
	return in, true
}

// recvTimeout waits up to timeout for a command. A false result is the
// RecvTimeoutError::Timeout case.
func (b *inbox) recvTimeout(timeout time.Duration) (input, bool) {
	if in, ok := b.pop(); ok {
		return in, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-b.wake:
	case <-timer.C:
		return nil, false
	}
	return b.pop()
}

// pendingNavigation is the client's outstanding request and when it started.
type pendingNavigation struct {
	request navigationRequest
	started time.Time
}

// NavigationResult is a finished navigation request: the targets, or the
// message to show.
type NavigationResult struct {
	Kind    NavigationKind
	Targets []navigation.Target
	Err     error
}

type languageKey struct {
	language language
	root     string
}

// Client is the UI-side handle on the language server: Sync tells it which
// document is open, Poll collects what the server said. The zero value is
// ready to use and does nothing until Enable. It is not safe for concurrent
// use.
type Client struct {
	// Version is the clientInfo.version announced to servers ("0.1.0" when
	// empty).
	Version string

	// NavigationResult is the finished navigation request, if any; the editor
	// takes it.
	NavigationResult *NavigationResult
	Diagnostics      []Diagnostic
	SemanticTokens   []semantic.Token
	SemanticRevision uint64
	// Status is the server name, or a progress or failure message.
	Status string

	ctx               context.Context
	navigationSerial  uint64
	navigationPending *pendingNavigation
	enabled           bool
	session           *session
	retired           []*session
	key               *languageKey
	stamp             *stamp
	uri               string
	// version is the version of the open document, not clientInfo.version.
	version int32
	failure bool
}

func (c *Client) CancelNavigation() {
	pending := c.navigationPending != nil
	c.navigationPending = nil
	c.NavigationResult = nil
	if pending && c.session != nil {
		_ = c.session.input.trySend(inputCancelNavigation{})
	}
}

// Navigate asks the server for definitions, references or symbols at the
// cursor. The error text is meant for the status line.
func (c *Client) Navigate(kind NavigationKind, query string, row, col int, lines [][]rune) error {
	c.CancelNavigation()
	if c.session == nil {
		return errors.New("No language server available; use project search or install the server for this file")
	}
	if c.stamp == nil {
		return errors.New("No document open")
	}
	c.navigationSerial++
	character := 0
	if row < len(lines) {
		for _, r := range lines[row][:min(col, len(lines[row]))] {
			character += lenUTF16(r)
		}
	}
	request := navigationRequest{
		serial: c.navigationSerial,
		kind:   kind,
		query:  query,
		document: tokenDocument{
			uri:        c.uri,
			generation: c.stamp.generation,
			version:    c.version,
		},
		row:       row,
		character: character,
	}
	if err := c.session.input.trySend(inputNavigate{request}); err != nil {
		return errors.New("Language server busy; retry navigation")
	}
	c.navigationPending = &pendingNavigation{request: request, started: time.Now()}
	return nil
}

// Enable lets the client start language servers; ctx bounds every session it
// starts. Call Close when done.
func (c *Client) Enable(ctx context.Context) {
	c.enabled = true
	c.ctx = ctx
}

// Restart drops the current server and all its results; the next Sync starts
// afresh (the :lsp-restart command).
func (c *Client) Restart() {
	c.CancelNavigation()
	c.retire()
	c.key = nil
	c.stamp = nil
	c.Diagnostics = nil
	c.clearTokens()
	c.Status = ""
	c.failure = false
}

// Failure is the status line text when the server could not be used.
func (c *Client) Failure() (string, bool) {
	if c.failure {
		return c.Status, true
	}
	return "", false
}

// Counts returns the number of error and warning diagnostics.
func (c *Client) Counts() (errorCount, warningCount int) {
	for _, d := range c.Diagnostics {
		switch d.Severity {
		case 1:
			errorCount++
		case 2:
			warningCount++
		}
	}
	return errorCount, warningCount
}

// LineSeverity is the most severe diagnostic touching a line.
func (c *Client) LineSeverity(row int) (uint8, bool) {
	var best uint8
	found := false
	for _, d := range c.Diagnostics {
		if d.coversLine(row) && (!found || d.Severity < best) {
			best, found = d.Severity, true
		}
	}
	return best, found
}

// AtCursor is the message of the most severe diagnostic at the cursor, or
// failing that on its line.
func (c *Client) AtCursor(row, col int) (string, bool) {
	if d, ok := mostSevere(c.Diagnostics, func(d Diagnostic) bool { return d.contains(row, col) }); ok {
		return d.Message, true
	}
	if d, ok := mostSevere(c.Diagnostics, func(d Diagnostic) bool { return d.coversLine(row) }); ok {
		return d.Message, true
	}
	return "", false
}

// mostSevere is `.filter(keep).min_by_key(|d| d.severity)`: the first of the
// lowest severity.
func mostSevere(diagnostics []Diagnostic, keep func(Diagnostic) bool) (Diagnostic, bool) {
	var best Diagnostic
	found := false
	for _, d := range diagnostics {
		if keep(d) && (!found || d.Severity < best.Severity) {
			best, found = d, true
		}
	}
	return best, found
}

func (c *Client) fail(message string) {
	if c.navigationPending != nil {
		c.NavigationResult = &NavigationResult{Kind: c.navigationPending.request.kind, Err: errors.New(message)}
		c.navigationPending = nil
	}
	c.retire()
	c.Diagnostics = nil
	c.clearTokens()
	c.Status = message
	c.failure = true
}

// Sync tells the client which document is open. generation identifies the
// buffer (a new file or a reload), revision its contents and saved the last
// save; text is only called when the server needs the whole document.
func (c *Client) Sync(path string, generation, revision, saved uint64, text func() string) {
	if !c.enabled {
		return
	}
	next := stamp{path: path, generation: generation, revision: revision, saved: saved}
	if c.stamp != nil && *c.stamp == next {
		return
	}
	identityChanged := c.stamp == nil || c.stamp.path != path || c.stamp.generation != generation
	contentsChanged := identityChanged || c.stamp == nil || c.stamp.revision != revision
	saveEvent := !identityChanged && c.stamp != nil && c.stamp.saved != saved
	c.stamp = &next
	if identityChanged {
		lang, ok := languageForPath(path)
		if !ok {
			c.Restart()
			again := next
			c.stamp = &again
			return
		}
		absolute, err := absolutePath(path)
		if err != nil {
			c.fail(fmt.Sprintf("LSP path: %s", ioErrorText(err)))
			return
		}
		uri, ok := fileURI(absolute)
		if !ok {
			c.fail("LSP: invalid file path")
			return
		}
		c.uri = uri
		root := lang.root(absolute)
		if c.key == nil || *c.key != (languageKey{lang, root}) || c.failure {
			c.retire()
			program, args := lang.command()
			c.Status = program + ": starting"
			c.failure = false
			ctx := c.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			c.session = startSession(ctx, program, args, root, c.clientVersion())
			c.key = &languageKey{lang, root}
		}
	}
	if contentsChanged {
		c.CancelNavigation()
		if c.version == 1<<31-1 {
			c.version = 1
		} else {
			c.version++
		}
		c.Diagnostics = nil
		c.clearTokens()
	}
	if c.session != nil && c.key != nil {
		doc := document{
			uri:        c.uri,
			generation: generation,
			version:    c.version,
			saved:      saved,
			saveEvent:  saveEvent,
			language:   c.key.language,
			text:       text(),
		}
		if err := c.session.input.trySend(inputDocument{doc}); err != nil {
			c.fail("LSP: server cannot keep up; :lsp-restart to retry")
		}
	}
}

func (c *Client) clientVersion() string {
	if c.Version == "" {
		return defaultVersion
	}
	return c.Version
}

// Poll collects what the session worker has reported since the last call.
// lines is the current buffer, for converting server positions.
func (c *Client) Poll(lines [][]rune) {
	if c.navigationPending != nil && time.Since(c.navigationPending.started) >= navigationTimeout+time.Second {
		kind := c.navigationPending.request.kind
		c.CancelNavigation()
		c.NavigationResult = &NavigationResult{
			Kind: kind,
			Err:  errors.New("Language-server navigation timed out; use project search instead"),
		}
	}
	c.retired = slices.DeleteFunc(c.retired, func(s *session) bool {
		select {
		case <-s.finished:
			return true
		default:
			return false
		}
	})
	// Bound each UI iteration even if a server floods notifications.
	for range pollBound {
		if c.session == nil {
			break
		}
		var event output
		select {
		case e, ok := <-c.session.output:
			if !ok {
				c.fail("LSP: server stopped; :lsp-restart to retry")
				return
			}
			event = e
		default:
			return
		}
		switch event := event.(type) {
		case outputNavigation:
			if c.navigationPending != nil && c.navigationPending.request.serial == event.request.serial &&
				c.stamp != nil && c.stamp.generation == event.request.document.generation &&
				c.uri == event.request.document.uri &&
				c.version == event.request.document.version {
				c.navigationPending = nil
				c.NavigationResult = &NavigationResult{Kind: event.request.kind, Targets: event.targets, Err: event.err}
			}
		case outputTokens:
			if c.stamp != nil && c.stamp.generation == event.document.generation &&
				c.uri == event.document.uri && c.version == event.document.version {
				c.SemanticTokens = nil
				if event.hasData {
					if tokens, ok := semantic.Decode(event.data, event.legend, lines); ok {
						c.SemanticTokens = tokens
					}
				}
				c.SemanticRevision++
			}
		case outputReady:
			if c.key != nil {
				program, _ := c.key.language.command()
				c.Status = program
			}
		case outputFailed:
			c.fail(event.message)
			return
		case outputDiagnostics:
			if c.stamp == nil || c.stamp.generation != event.generation ||
				event.version != c.version ||
				event.publication.uri != c.uri ||
				(event.publication.hasVersion && event.publication.version != c.version) {
				continue
			}
			c.Diagnostics = nil
			for _, d := range event.publication.diagnostics {
				start, ok := scalarPosition(lines, d.rng.start)
				if !ok {
					continue
				}
				end, ok := scalarPosition(lines, d.rng.end)
				if !ok {
					continue
				}
				if end.Less(start) {
					continue
				}
				severity := uint8(1)
				if d.hasSeverity && d.severity >= 1 && d.severity <= 4 {
					severity = d.severity
				}
				c.Diagnostics = append(c.Diagnostics, Diagnostic{Start: start, End: end, Severity: severity, Message: d.message})
			}
		}
	}
}

func (c *Client) retire() {
	if c.session != nil {
		_ = c.session.input.trySend(inputStop{})
		c.retired = append(c.retired, c.session)
		c.session = nil
	}
}

func (c *Client) clearTokens() {
	c.SemanticTokens = nil
	c.SemanticRevision++
}

// Close retires the session and keeps shutdown workers alive long enough to
// close documents and reap children before the program exits (up to one
// second); file switches never wait on this path. It is safe to call more
// than once.
func (c *Client) Close() {
	c.retire()
	c.enabled = false
	deadline := time.Now().Add(time.Second)
	for _, s := range c.retired {
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-s.finished:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// absolutePath is the file's absolute path with symlinked directories
// resolved. New files do not exist yet, but their parent does.
func absolutePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("cannot make an empty path absolute")
	}
	absolute, err := fsx.Absolute(path)
	if err != nil {
		return "", err
	}
	// A trailing separator stays (as in std::path::absolute), which makes
	// exists() fail for a file.
	// Canonicalization also removes '..' and resolves symlinked directories
	// before building a file URI.
	if fsx.Exists(absolute) {
		return fsx.Canonicalize(absolute)
	}
	parent, ok := fsx.Parent(absolute)
	if !ok {
		parent = "/"
	}
	canonical, err := fsx.Canonicalize(parent)
	if err != nil {
		return "", err
	}
	name, _ := fsx.FileName(absolute)
	return fsx.Join(canonical, name), nil
}

// lenUTF16 is char::len_utf16.
func lenUTF16(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// scalarPosition converts a protocol position (UTF-16 offset) to a character
// column, never splitting a surrogate pair. A line's trailing CR does not
// count unless it is the last line.
func scalarPosition(lines [][]rune, pos wirePosition) (Position, bool) {
	if pos.line >= uint64(len(lines)) {
		return Position{}, false
	}
	row := int(pos.line)
	line := lines[row]
	length := len(line)
	if row+1 < len(lines) && length > 0 && line[length-1] == '\r' {
		length--
	}
	var units uint64
	col := 0
	for _, r := range line[:length] {
		if units+uint64(lenUTF16(r)) > pos.character {
			break
		}
		units += uint64(lenUTF16(r))
		col++
	}
	return Position{row, col}, true
}

// endPosition is the protocol position just after text.
func endPosition(text string) object {
	line := strings.Count(text, "\n")
	last := text[strings.LastIndexByte(text, '\n')+1:]
	character := 0
	for _, r := range last {
		character += lenUTF16(r)
	}
	return object{"line": line, "character": character}
}

func notify(method string, params any) object {
	return object{"jsonrpc": "2.0", "method": method, "params": params}
}

// ioErrorText renders an error the way Rust's io::Error does: operating
// system errors as "description (os error N)".
func ioErrorText(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		text := errno.Error()
		if text != "" {
			text = strings.ToUpper(text[:1]) + text[1:]
		}
		return fmt.Sprintf("%s (os error %d)", text, int(errno))
	}
	return err.Error()
}
