package lsp

// Wire-level evidence before the editor exists. The fake servers are the ones
// tests/lsp_smoke.py installs as rust-analyzer, gopls and marksman (the script
// itself, run under the server's name), and each test below walks the Client
// through the same flow as one phase of that script's editor session: the
// Sync, Navigate and Close calls the editor makes at those moments.
//
// testdata/rust_wire.jsonl holds every distinct message the Rust editor sent
// those servers when tests/lsp_smoke.py ran against it, with the temporary
// directory replaced by <ROOT> and document versions zeroed. Every message
// the Go client sends must be one of them: same methods, same JSON, same
// positions, same texts.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type smoke struct {
	t      *testing.T
	root   string
	bin    string
	log    string
	python string
	client *Client
	lines  [][]rune
	cancel context.CancelFunc
}

// newSmoke prepares a project directory with the Cargo.toml, go.mod and
// .marksman.toml markers, a bin directory with the three fake servers, and a
// Client that starts them from PATH.
func newSmoke(t *testing.T, mode string) *smoke {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &smoke{t: t, root: filepath.Join(base, "run"), python: python}
	s.bin, s.log = filepath.Join(s.root, "bin"), filepath.Join(s.root, "log")
	for _, dir := range []string{s.root, s.bin, s.log} {
		if err := os.Mkdir(dir, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "tests", "lsp_smoke.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rust-analyzer", "gopls", "marksman"} {
		script := "#!" + python + "\n" + string(source)
		if err := os.WriteFile(filepath.Join(s.bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Cargo.toml", "go.mod", ".marksman.toml"} {
		s.write(name, "")
	}
	t.Setenv("PATH", s.bin)
	t.Setenv("COIGUE_LSP_LOG", s.log)
	t.Setenv("COIGUE_LSP_MODE", mode)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.client = &Client{}
	s.client.Enable(ctx)
	t.Cleanup(func() {
		s.client.Close()
		cancel()
	})
	return s
}

func (s *smoke) write(name, text string) string {
	s.t.Helper()
	path := filepath.Join(s.root, name)
	if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
		s.t.Fatal(err)
	}
	return path
}

// sync is what the editor does on every tick for its current buffer.
func (s *smoke) sync(path string, generation, revision, saved uint64, text string) {
	s.t.Helper()
	s.lines = bufferLines(text)
	s.client.Sync(path, generation, revision, saved, func() string { return text })
}

// wait polls the client, as the editor loop does, until the condition holds.
func (s *smoke) wait(what string, timeout time.Duration, condition func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		s.client.Poll(s.lines)
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %s (status %q)", what, s.client.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settle keeps polling for a while, as the editor's key handling does.
func (s *smoke) settle(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.client.Poll(s.lines)
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *smoke) warning(server string) func() bool {
	return func() bool {
		message, ok := s.client.AtCursor(0, 1)
		return ok && message == "LSP warning from "+server
	}
}

func (s *smoke) navigate(kind NavigationKind, query string, row, col int) *NavigationResult {
	s.t.Helper()
	if err := s.client.Navigate(kind, query, row, col, s.lines); err != nil {
		s.t.Fatal(err)
	}
	s.wait(kind.Title(), 8*time.Second, func() bool { return s.client.NavigationResult != nil })
	result := s.client.NavigationResult
	s.client.NavigationResult = nil
	return result
}

// records is the fake server's log for one server name.
func (s *smoke) records(name string) []any {
	path := filepath.Join(s.log, name+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return readLog(s.t, path)
}

func methodsOf(entries []any) []string {
	var methods []string
	for _, entry := range entries {
		if method, ok := asString(index(entry, "method")); ok {
			methods = append(methods, method)
		}
	}
	return methods
}

func (s *smoke) find(entries []any, method string) any {
	s.t.Helper()
	for _, entry := range entries {
		if equalsString(index(entry, "method"), method) {
			return entry
		}
	}
	s.t.Fatalf("no %s in %v", method, methodsOf(entries))
	return nil
}

// assertReaped is the script's assert_reaped: every server pid in the logs is gone.
func (s *smoke) assertReaped() {
	s.t.Helper()
	files, _ := filepath.Glob(filepath.Join(s.log, "*.jsonl"))
	for _, file := range files {
		for _, entry := range readLog(s.t, file) {
			pid, ok := asI64(index(entry, "pid"))
			if !ok {
				continue
			}
			if processAlive(int(pid)) {
				s.t.Errorf("language server not reaped: %d", pid)
			}
		}
	}
}

// normalized replaces the temporary directory by <ROOT>, as the golden file
// has it, and zeroes what depends on the process or on timing.
func normalized(v any, root string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, root, "<ROOT>")
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalized(e, root)
		}
		return out
	case object:
		out := object{}
		for k, e := range x {
			out[k] = normalized(e, root)
		}
		return out
	}
	return v
}

func canonical(t *testing.T, v any, root string) string {
	t.Helper()
	m, ok := v.(object)
	if !ok {
		t.Fatalf("unexpected record %v", v)
	}
	if _, spawn := m["pid"]; spawn {
		m = object{"spawn": object{"args": m["args"], "cwd": m["cwd"]}}
	} else {
		if equalsString(m["method"], "initialize") {
			params := m["params"].(object)
			params["processId"] = json0
		}
		if params, ok := m["params"].(object); ok {
			if td, ok := params["textDocument"].(object); ok {
				if _, has := td["version"]; has {
					td["version"] = json0
				}
			}
		}
	}
	return string(marshal(normalized(m, root)))
}

var json0 = jsonNumber("0")

func jsonNumber(text string) any {
	v, err := unmarshal([]byte(text))
	if err != nil {
		panic(err)
	}
	return v
}

// loadRustWire reads the messages the Rust editor sent, by server name.
func loadRustWire(t *testing.T) map[string]map[string]bool {
	t.Helper()
	file, err := os.Open("testdata/rust_wire.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	wire := map[string]map[string]bool{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<22)
	for scanner.Scan() {
		entry, _ := asArray(parse(t, scanner.Text()))
		server, _ := asString(entry[0])
		if wire[server] == nil {
			wire[server] = map[string]bool{}
		}
		wire[server][string(marshal(entry[1]))] = true
	}
	return wire
}

// checkWire fails for any message in the logs that the Rust editor did not
// send the same server, and returns the methods seen per server.
func (s *smoke) checkWire() map[string]map[string]bool {
	s.t.Helper()
	wire := loadRustWire(s.t)
	seen := map[string]map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(s.log, "*.jsonl"))
	sort.Strings(files)
	for _, file := range files {
		server := strings.TrimSuffix(filepath.Base(file), ".jsonl")
		seen[server] = map[string]bool{}
		for _, entry := range readLog(s.t, file) {
			text := canonical(s.t, entry, s.root)
			if !wire[server][text] {
				s.t.Errorf("%s: the Rust editor never sent\n%s", server, text)
			}
			if method, ok := asString(index(entry, "method")); ok {
				seen[server][method] = true
			}
		}
	}
	return seen
}

func requireMethods(t *testing.T, seen map[string]map[string]bool, server string, methods ...string) {
	t.Helper()
	for _, method := range methods {
		if !seen[server][method] {
			t.Errorf("%s never received %s", server, method)
		}
	}
}

// Navigation phase of tests/lsp_smoke.py: symbols, definitions, references,
// workspace symbols, Unicode, history.
func TestSmokeNavigationFlow(t *testing.T) {
	s := newSmoke(t, "navigation")
	source := s.write("navigation-source.rs", "a😀target\nfn local() {}")
	target := s.write("navigation-target.rs", "😀 target\nfn target() {}")
	sourceText, targetText := "a😀target\nfn local() {}", "😀 target\nfn target() {}"

	s.sync(source, 0, 0, 0, sourceText)
	s.wait("the rust-analyzer warning", 4*time.Second, s.warning("rust-analyzer"))
	if status := s.client.Status; status != "rust-analyzer" {
		t.Errorf("status = %q", status)
	}

	// :definition at column 2 (after the emoji) jumps into the target file.
	result := s.navigate(Definition, "", 0, 2)
	if result.Err != nil || len(result.Targets) != 1 || result.Kind != Definition {
		t.Fatalf("definition: %#v", result)
	}
	if got := result.Targets[0]; got.Location.Path != target || got.Location.Row != 0 || got.Location.Col != 2 ||
		got.Label != target+":1:3" {
		t.Errorf("definition target = %#v", got)
	}
	// Open the target, insert '!', save, go :back.
	s.sync(target, 1, 0, 0, targetText)
	s.settle(150 * time.Millisecond)
	targetText = "😀 !target\nfn target() {}"
	s.sync(target, 1, 1, 0, targetText)
	s.settle(250 * time.Millisecond)
	s.sync(target, 1, 1, 1, targetText)
	s.settle(150 * time.Millisecond)
	s.sync(source, 2, 0, 0, sourceText)
	s.settle(150 * time.Millisecond)

	// :symbols lists Outer and Outer / Local.
	result = s.navigate(Symbols, "", 0, 0)
	if result.Err != nil || len(result.Targets) != 2 || !strings.Contains(result.Targets[1].Label, "Outer / Local") {
		t.Fatalf("symbols: %#v", result)
	}
	// Document symbols carry no URI, so they are in the current file.
	if got := result.Targets[1].Location; got.Path != source || got.Row != 1 || got.Col != 3 {
		t.Errorf("Local at %#v", got)
	}
	// Jump to Local, insert '#', save.
	sourceText = "a😀target\nfn #local() {}"
	s.sync(source, 2, 1, 0, sourceText)
	s.settle(250 * time.Millisecond)
	s.sync(source, 2, 1, 1, sourceText)
	s.settle(150 * time.Millisecond)

	// :references from Local; the second location is in the target file.
	result = s.navigate(References, "", 1, 3)
	if result.Err != nil || len(result.Targets) != 2 {
		t.Fatalf("references: %#v", result)
	}
	if got := result.Targets[1].Location; got.Path != target || got.Row != 0 || got.Col != 2 {
		t.Errorf("reference target = %#v", got)
	}
	s.sync(target, 3, 0, 0, targetText)
	s.settle(150 * time.Millisecond)

	// :workspace-symbols Target.
	result = s.navigate(Workspace, "Target", 0, 0)
	if result.Err != nil || len(result.Targets) != 1 || !strings.HasPrefix(result.Targets[0].Label, "WorkspaceTarget  ") {
		t.Fatalf("workspace symbols: %#v", result)
	}
	if got := result.Targets[0].Location; got.Path != target || got.Row != 1 || got.Col != 3 {
		t.Errorf("workspace symbol at %#v", got)
	}
	s.client.Close()

	entries := s.records("rust-analyzer")
	definition := s.find(entries, "textDocument/definition")
	expectJSON(t, index(index(definition, "params"), "position"), `{"line":0,"character":3}`)
	if !hasRequest(entries, "workspace/symbol", func(e any) bool {
		return equalsString(index(index(e, "params"), "query"), "Target")
	}) {
		t.Error("no workspace/symbol request for Target")
	}
	if !hasRequest(entries, "textDocument/references", func(e any) bool {
		v, _ := asBool(index(index(index(e, "params"), "context"), "includeDeclaration"))
		return v
	}) {
		t.Error("no references request that includes the declaration")
	}
	s.assertReaped()
	seen := s.checkWire()
	requireMethods(t, seen, "rust-analyzer", "initialize", "initialized", "textDocument/didOpen", "textDocument/didChange",
		"textDocument/didSave", "textDocument/didClose", "textDocument/definition", "textDocument/references",
		"textDocument/documentSymbol", "workspace/symbol", "shutdown", "exit")
	if got, want := strings.Join(methodsOf(entries), " "), "initialize initialized"; !strings.HasPrefix(got, want) {
		t.Errorf("methods = %s", got)
	}
}

func hasRequest(entries []any, method string, match func(any) bool) bool {
	for _, entry := range entries {
		if equalsString(index(entry, "method"), method) && match(entry) {
			return true
		}
	}
	return false
}
