package ext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/buffer"
)

const wrapProgram = `
(define (wrap open close)
  (replace-selection (string-append open (selection-text) close)))
(define-command "wrap" wrap "Wrap the selection")
`

// loaded returns a Runtime with the program loaded and fails the test if
// loading failed.
func loaded(t *testing.T, opts Options, program string) *Runtime {
	t.Helper()
	r := New(opts)
	if err := r.Load(context.Background(), program); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

// session builds a session over a buffer holding text with the cursor at
// (row, col) and, if anchor is not nil, a selection from it to the cursor.
func session(text string, row, col int, anchor *[2]int, linewise bool) *Session {
	b := buffer.New(text)
	b.Row, b.Col = row, col
	var sel *Selection
	if anchor != nil {
		sel = &Selection{Anchor: *anchor, Linewise: linewise}
	}
	return NewSession(b, sel)
}

func TestWrapOnMultibyteSelection(t *testing.T) {
	r := loaded(t, Options{}, wrapProgram)
	// "héllo 界" is selected from column 0 to the cursor on 界 (column 6).
	s := session("héllo 界 wörld\nsecond", 0, 6, &[2]int{0, 0}, false)
	if err := r.Run(context.Background(), "wrap", []string{"「", "」"}, s); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := s.Buffer.Text(); got != "「héllo 界」 wörld\nsecond" {
		t.Fatalf("text %q", got)
	}
	if s.Buffer.Row != 0 || s.Buffer.Col != 0 || !s.Edited() {
		t.Fatalf("cursor (%d,%d) edited %v", s.Buffer.Row, s.Buffer.Col, s.Edited())
	}
}

func TestHostOperations(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		row, col int
		anchor   *[2]int
		linewise bool
		body     string // body of (define (t . args) ...)
		args     []string
		want     string // buffer text afterwards
		wantRow  int
		wantCol  int
		message  string
		err      string // substring of the error; "" for success
	}{
		{name: "selection spanning lines includes the newline", text: "ab\ncd\nef", row: 1, col: 0, anchor: &[2]int{0, 1},
			body: `(replace-selection (format "[~s]" (selection-text)))`, want: "a[\"b\\nc\"]d\nef", wantRow: 0, wantCol: 1},
		{name: "linewise selection is whole lines without the last newline", text: "ab\ncd\nef\ngh", row: 2, col: 1, anchor: &[2]int{1, 0}, linewise: true,
			body: `(replace-selection (string-upcase (selection-text)))`, want: "ab\nCD\nEF\ngh", wantRow: 1, wantCol: 0},
		{name: "backwards selection", text: "abcdef", row: 0, col: 4, anchor: &[2]int{0, 1},
			body: `(replace-selection "-")`, want: "a-f", wantRow: 0, wantCol: 1},
		{name: "selection reads do not need an edit", text: "abc", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(message (selection-text))`, want: "abc", wantRow: 0, wantCol: 1, message: "ab"},
		{name: "insert-text goes before the cursor character", text: "héllo", row: 0, col: 1,
			body: `(insert-text "界")`, want: "h界éllo", wantRow: 0, wantCol: 1},
		{name: "insert-text with newlines", text: "ab", row: 0, col: 1,
			body: `(insert-text "x\ny")`, want: "ax\nyb", wantRow: 0, wantCol: 1},
		{name: "reads see earlier edits", text: "a\nb", row: 0, col: 0,
			body: `(insert-text "x\n") (message (buffer-text 1 2))`, want: "x\na\nb", wantRow: 0, wantCol: 0, message: "x a"},
		{name: "buffer-text, line-count and cursor-line", text: "one\ntwo\nthree", row: 1, col: 0,
			body: `(message (format "~a ~a ~s ~s" (line-count) (cursor-line) (buffer-text 2) (buffer-text 1 3)))`,
			want: "one\ntwo\nthree", wantRow: 1, wantCol: 0, message: `3 2 "two" "one\ntwo\nthree"`},
		{name: "selection? before and after an edit", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(define before (selection?)) (insert-text "x") (message (format "~a ~a" before (selection?)))`, want: "axb", wantRow: 0, wantCol: 1, message: "#t #f"},
		{name: "message is tidied", text: "ab", row: 0, col: 0,
			body: `(message "a\nb\tc")`, want: "ab", message: "a b c"},
		{name: "arguments are strings", text: "ab", row: 0, col: 0, args: []string{"x", "界"},
			body: `(insert-text (string-append (car args) (car (cdr args))))`, want: "x界ab"},
		{name: "empty replacement deletes", text: "abc", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(replace-selection "")`, want: "c", wantRow: 0, wantCol: 0},

		{name: "no selection", text: "ab", body: `(selection-text)`, err: "selection-text: no selection; select text with v or V, then press !"},
		{name: "no selection for replace", text: "ab", body: `(replace-selection "x")`, err: "replace-selection: no selection"},
		{name: "selection after an edit", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(insert-text "x") (selection-text)`, err: "selection-text: the selection is gone after an earlier edit in this command"},
		{name: "second replace", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(replace-selection "x") (replace-selection "y")`, err: "replace-selection: the selection is gone"},
		{name: "oversized selection", text: strings.Repeat("x", MaxRegion+1), row: 0, col: MaxRegion, anchor: &[2]int{0, 0},
			body: `(selection-text)`, err: "selection-text: selection is 65537 characters; the limit is 65536"},
		{name: "selection at the limit", text: strings.Repeat("é", MaxRegion), row: 0, col: MaxRegion - 1, anchor: &[2]int{0, 0},
			body: `(message (number->string (string-length (selection-text))))`, want: strings.Repeat("é", MaxRegion), wantCol: MaxRegion - 1, message: "65536"},
		{name: "invalid UTF-8", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(replace-selection "\xff")`, err: "replace-selection: text is not valid UTF-8"},
		{name: "oversized replacement", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(replace-selection (make-big))`, err: "replace-selection: text is 65537 characters; the limit is 65536"},
		{name: "too much inserted overall", text: "ab", row: 0, col: 0,
			body: `(do ((i 0 (+ i 1))) ((= i 17)) (insert-text (make-max)))`, err: "insert-text: this command has inserted more than 1048576 characters"},
		{name: "wrong type", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(replace-selection 42)`, err: "replace-selection: argument 1: expected string"},
		{name: "wrong argument count", text: "ab", body: `(buffer-text)`, err: "buffer-text: expected 1 to 2 argument(s), got 0"},
		{name: "line out of range", text: "ab", body: `(buffer-text 2)`, err: "buffer-text: line 2 is outside 1..1"},
		{name: "line zero", text: "ab", body: `(buffer-text 0 1)`, err: "buffer-text: line 0 is outside 1..1"},
		{name: "end before start", text: "a\nb", body: `(buffer-text 2 1)`, err: "buffer-text: lines 2..1: the end is before the start"},
		{name: "line range too long", text: strings.Repeat("x\n", MaxRegion), body: `(buffer-text 1 (line-count))`, err: "buffer-text: lines 1..65537 are longer than 65536 characters"},
		{name: "scheme error", text: "ab", body: `(error "boom" 1 "two")`, err: `boom 1 "two"`},
		{name: "unbound variable", text: "ab", body: `(no-such-procedure)`, err: "unbound variable: no-such-procedure"},
		{name: "define-command in a command", text: "ab", body: `(define-command "late" t)`, err: "define-command: only allowed while init.scm loads"},
		{name: "a rejected edit can be caught and the rest still counts", text: "ab", row: 0, col: 1, anchor: &[2]int{0, 0},
			body: `(guard (e (#t (message "caught"))) (replace-selection "\xff")) (replace-selection "ok")`, want: "ok", message: "caught"},
	}
	program := `
(define (make-big) (make-string-of 65537))
(define (make-max) (make-string-of 65536))
(define (make-string-of n)
  (let loop ((s "x") (len 1))
    (if (>= len n) (substring s 0 n) (loop (string-append s s) (* len 2)))))
`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// These cases test the limits and errors, not the deadline: 17 inserts of 64 KiB take
			// 5.5 s under -race on a GitHub macOS Intel runner, which is the default deadline.
			r := loaded(t, Options{Deadline: time.Minute}, program+"(define (t . args) "+c.body+")\n(define-command \"t\" t)")
			s := session(c.text, c.row, c.col, c.anchor, c.linewise)
			err := r.Run(context.Background(), "t", c.args, s)
			if c.err != "" {
				var re *RunError
				if !errors.As(err, &re) || re.Stop != Failed || !strings.Contains(re.Error(), c.err) {
					t.Fatalf("error %v, want one containing %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := s.Buffer.Text(); got != c.want {
				t.Fatalf("text %q, want %q", got, c.want)
			}
			if s.Buffer.Row != c.wantRow || s.Buffer.Col != c.wantCol {
				t.Fatalf("cursor (%d,%d), want (%d,%d)", s.Buffer.Row, s.Buffer.Col, c.wantRow, c.wantCol)
			}
			if s.Message() != c.message {
				t.Fatalf("message %q, want %q", s.Message(), c.message)
			}
		})
	}
}

func TestDefineCommandRules(t *testing.T) {
	var defined []string
	reject := errors.New("\"w\" is a built-in command")
	opts := Options{Define: func(name, doc string) error {
		if name == "w" {
			return reject
		}
		defined = append(defined, name+": "+doc)
		return nil
	}}
	for program, want := range map[string]string{
		`(define-command "Wrap" car)`:                            `"Wrap" is not a command name`,
		`(define-command "9lives" car)`:                          "is not a command name",
		`(define-command "has space" car)`:                       "is not a command name",
		`(define-command "" car)`:                                "is not a command name",
		`(define-command "` + strings.Repeat("a", 33) + `" car)`: "is not a command name",
		`(define-command "w" car)`:                               `"w" is a built-in command`,
		`(define-command "x" 42)`:                                "expected procedure",
		`(define-command 'x car)`:                                "expected string",
		`(define-command "x" car 1)`:                             "expected string",
		`(define-command "x")`:                                   "expected 2 to 3 argument(s), got 1",
		`(define-command "a" car) (define-command "a" cdr)`:      `the command "a" is already defined`,
	} {
		r := New(opts)
		err := r.Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", program, err, want)
		}
	}
	r := New(opts)
	if err := r.Load(context.Background(), `(define-command "ok-1" car "  doc\nline ") (define-command "also" cdr)`); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(defined, "|"); !strings.Contains(got, "ok-1:   doc line ") {
		t.Fatalf("defined %q", got)
	}
	if cmds := r.Commands(); len(cmds) != 2 || cmds[0].Name != "ok-1" || cmds[1].Name != "also" {
		t.Fatalf("Commands() = %+v", cmds)
	}
	// The limit on the number of commands.
	r = New(Options{})
	var sb strings.Builder
	for i := 0; i <= MaxCommands; i++ {
		sb.WriteString("(define-command \"c" + strings.Repeat("a", i%20) + string(rune('a'+i/20)) + "\" car)\n")
	}
	err := r.Load(context.Background(), sb.String())
	if err == nil || !strings.Contains(err.Error(), "at most 100 commands") || len(r.Commands()) != MaxCommands {
		t.Fatalf("limit: %v with %d commands", err, len(r.Commands()))
	}
}

func TestRunOfAnUnknownCommand(t *testing.T) {
	r := New(Options{})
	err := r.Run(context.Background(), "nothing", nil, session("x", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "no extension command") {
		t.Fatalf("error %v", err)
	}
}

func TestOperationsRefuseOutsideInvocation(t *testing.T) {
	r := loaded(t, Options{}, `
(define (leak) (replace-selection "x"))
(define (read) (selection-text))
(define-command "leak" leak)`)
	// A procedure retained from init, applied by something that is not an
	// invocation the editor started: no session in the context.
	for _, name := range []string{"leak", "read"} {
		proc, ok := r.interp.Global().Lookup(name)
		if !ok {
			t.Fatalf("%s not defined", name)
		}
		_, err := r.interp.Apply(context.Background(), proc, nil)
		if err == nil || !strings.Contains(err.Error(), "not inside an extension command") {
			t.Fatalf("%s: error %v", name, err)
		}
	}
	// A context that outlived its invocation.
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	if err := r.Run(context.Background(), "leak", nil, s); err != nil || s.Buffer.Text() != "xc" {
		t.Fatalf("run: %v, %q", err, s.Buffer.Text())
	}
	stale := context.WithValue(context.Background(), sessionKey{}, s)
	for _, p := range r.hostOperations() {
		if p.Name == "define-command" {
			continue
		}
		_, err := p.Handler(&scheme.Call{Ctx: stale, In: r.interp, Name: p.Name, Args: []scheme.Value{scheme.Str("y")}})
		if !errors.Is(err, errNotInCommand) {
			t.Errorf("%s after its invocation: %v", p.Name, err)
		}
	}
	// During init there is a session but no buffer to work on.
	err := New(Options{}).Load(context.Background(), `(insert-text "x")`)
	if err == nil || !strings.Contains(err.Error(), "insert-text: not inside an extension command") {
		t.Fatalf("operation during init: %v", err)
	}
	// Outside init, define-command has nowhere to define into.
	p, _ := r.interp.Lookup("define-command")
	_, err = p.Handler(&scheme.Call{Ctx: context.Background(), In: r.interp, Name: p.Name,
		Args: []scheme.Value{scheme.Str("late"), scheme.Sym("car")}})
	if !errors.Is(err, errNotInInit) {
		t.Fatalf("define-command with no session: %v", err)
	}
}

func TestGateRefusesLoadAndEverythingThatTouchesTheSystem(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "evil.scm")
	if err := os.WriteFile(marker, []byte(`(define loaded-evil #t)`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{
		`(load "` + marker + `")`,
		`(guard (e (#t 'caught)) (load "` + marker + `"))`,
		`(guard (e ((string? e) 1) (else 'caught)) (apply load (list "` + marker + `")))`,
		`(map load (list "` + marker + `"))`,
	} {
		r := New(Options{})
		err := r.Load(context.Background(), program)
		if err == nil || !strings.Contains(err.Error(), "load: denied: editor extensions cannot use operating-system operations") {
			t.Errorf("%s: error %v", program, err)
		}
		var denied *scheme.Denied
		if !errors.As(err, &denied) {
			t.Errorf("%s: %v is not a denial", program, err)
		}
		if _, ok := r.interp.Global().Lookup("loaded-evil"); ok {
			t.Errorf("%s: the file was evaluated", program)
		}
	}
	// Also from inside a command, where the program was written earlier.
	r := loaded(t, Options{}, `(define (go) (load "`+marker+`")) (define-command "go" go)`)
	err := r.Run(context.Background(), "go", nil, session("x", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "load: denied") {
		t.Fatalf("load inside a command: %v", err)
	}
	// No program can add a primitive or a policy check, and nothing but the
	// documented host operations and Brote's pure language is registered.
	for _, program := range []string{
		`(register-tool "evil" (lambda () 1) '((doc . "x") (params . ())))`,
		`(register-check "c" "deny" (lambda (n a) #f))`,
	} {
		if err := New(Options{}).Load(context.Background(), program); err == nil {
			t.Errorf("%s was allowed", program)
		}
	}
	allowed := map[string]bool{}
	for _, p := range New(Options{}).hostOperations() {
		allowed[p.Name] = true
	}
	for _, p := range New(Options{}).interp.Prims() {
		if p.Meta.Host || p.Meta.Egress || p.Meta.Irreversible {
			if p.Name != "load" {
				t.Errorf("Brote now offers %s, which touches the system: review the gate", p.Name)
			}
			continue
		}
		if p.Meta.Origin == "scheme" {
			t.Errorf("%s was registered by a program", p.Name)
		}
	}
}

func TestDeadlineStopsARunawayLoopAndTheRuntimeSurvives(t *testing.T) {
	r := loaded(t, Options{Deadline: 50 * time.Millisecond}, wrapProgram+`
(define (spin) (let loop () (loop)))
(define (spin-guarded) (let loop () (guard (e (#t #f)) (loop))))
(define-command "spin" spin)
(define-command "spin-guarded" spin-guarded)`)
	for _, name := range []string{"spin", "spin-guarded"} {
		s := session("abc", 0, 1, &[2]int{0, 0}, false)
		start := time.Now()
		err := r.Run(context.Background(), name, nil, s)
		var re *RunError
		if !errors.As(err, &re) || re.Stop != Deadline || re.Error() != "stopped after 50ms" || !re.Stopped() {
			t.Fatalf("%s: %v", name, err)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatalf("%s took %v", name, time.Since(start))
		}
		if s.Edited() || s.Buffer.Text() != "abc" {
			t.Fatalf("%s changed the session", name)
		}
	}
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	if err := r.Run(context.Background(), "wrap", []string{"(", ")"}, s); err != nil || s.Buffer.Text() != "(ab)c" {
		t.Fatalf("after a deadline: %v %q", err, s.Buffer.Text())
	}
}

// watcher interrupts the run after the delay, from another goroutine.
func watcher(delay time.Duration) Watcher {
	return func(interrupt func()) func() {
		done, exited := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(exited)
			select {
			case <-time.After(delay):
				interrupt()
			case <-done:
			}
		}()
		return func() {
			close(done)
			<-exited
		}
	}
}

func TestInterruptStopsARunawayLoop(t *testing.T) {
	r := loaded(t, Options{Watch: watcher(20 * time.Millisecond)}, wrapProgram+`
(define (spin) (let loop () (loop)))
(define-command "spin" spin)`)
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	start := time.Now()
	err := r.Run(context.Background(), "spin", nil, s)
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Interrupted || re.Error() != "interrupted" {
		t.Fatalf("error %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	// A quick command is not interrupted by a watcher whose time comes later.
	r.opts.Watch = watcher(time.Hour)
	if err := r.Run(context.Background(), "wrap", []string{"[", "]"}, s); err != nil || s.Buffer.Text() != "[ab]c" {
		t.Fatalf("after an interrupt: %v %q", err, s.Buffer.Text())
	}
}

func TestInterruptedLoadKeepsEarlierDefinitions(t *testing.T) {
	r := New(Options{Watch: watcher(20 * time.Millisecond)})
	err := r.Load(context.Background(), `(define-command "early" car "first") (let loop () (loop)) (define-command "late" car)`)
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Interrupted {
		t.Fatalf("error %v", err)
	}
	if cmds := r.Commands(); len(cmds) != 1 || cmds[0].Name != "early" {
		t.Fatalf("commands %+v", cmds)
	}
}

func TestParentCancellationStopsARun(t *testing.T) {
	r := loaded(t, Options{}, `(define (spin) (let loop () (loop))) (define-command "spin" spin)`)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	err := r.Run(ctx, "spin", nil, session("x", 0, 0, nil, false))
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Interrupted {
		t.Fatalf("error %v", err)
	}
}

func TestMemoryLimitStopsAStringThatDoublesForever(t *testing.T) {
	r := loaded(t, Options{MemoryLimit: 16 << 20, Deadline: 30 * time.Second}, wrapProgram+`
(define (grow) (let loop ((s "xxxxxxxx")) (loop (string-append s s))))
(define-command "grow" grow)`)
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	err := r.Run(context.Background(), "grow", nil, s)
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Memory || re.Error() != "stopped: used more than 16 MiB of memory" {
		t.Fatalf("error %v", err)
	}
	if s.Edited() || s.Buffer.Text() != "abc" {
		t.Fatal("the session changed")
	}
	if err := r.Run(context.Background(), "wrap", []string{"(", ")"}, s); err != nil {
		t.Fatalf("after the limit: %v", err)
	}
}

func TestRunRefusesReentry(t *testing.T) {
	var r *Runtime
	var armed bool
	var inner error
	r = loaded(t, Options{Watch: func(func()) func() {
		if armed {
			// From another goroutine, as a stray background callback would.
			done := make(chan struct{})
			go func() {
				defer close(done)
				inner = r.Run(context.Background(), "wrap", []string{"(", ")"}, session("abc", 0, 1, &[2]int{0, 0}, false))
			}()
			<-done
		}
		return func() {}
	}}, wrapProgram)
	armed = true
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	if err := r.Run(context.Background(), "wrap", []string{"(", ")"}, s); err != nil || s.Buffer.Text() != "(ab)c" {
		t.Fatalf("outer run: %v %q", err, s.Buffer.Text())
	}
	if !errors.Is(inner, ErrBusy) {
		t.Fatalf("inner run: %v", inner)
	}
	// Load is refused the same way while a run is in progress.
	r.opts.Watch = func(func()) func() {
		inner = r.Load(context.Background(), "(define x 1)")
		return func() {}
	}
	inner = nil
	if err := r.Run(context.Background(), "wrap", []string{"(", ")"}, session("abc", 0, 1, &[2]int{0, 0}, false)); err != nil || !errors.Is(inner, ErrBusy) {
		t.Fatalf("load during a run: %v, %v", err, inner)
	}
	if _, ok := r.interp.Global().Lookup("x"); ok {
		t.Fatal("the refused load ran")
	}
}

func TestAPanicBecomesAnErrorAndTheRuntimeSurvives(t *testing.T) {
	r := newRuntime(Options{}, &scheme.Prim{
		Name:    "go-panic",
		Meta:    scheme.Meta{Doc: "Test: panic."},
		Handler: func(*scheme.Call) (scheme.Value, error) { panic("handler bug") },
	})
	if err := r.Load(context.Background(), wrapProgram+`(define (p) (go-panic)) (define-command "p" p)`); err != nil {
		t.Fatal(err)
	}
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	err := r.Run(context.Background(), "p", nil, s)
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Internal || re.Error() != "failed: internal error: handler bug" {
		t.Fatalf("error %v", err)
	}
	if err := r.Run(context.Background(), "wrap", []string{"(", ")"}, s); err != nil || s.Buffer.Text() != "(ab)c" {
		t.Fatalf("after a panic: %v %q", err, s.Buffer.Text())
	}
}

func TestHostOperationsAreDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "extending.md"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(Options{})
	for _, p := range r.hostOperations() {
		if !strings.Contains(string(doc), "`("+p.Name) {
			t.Errorf("docs/extending.md does not document (%s ...)", p.Name)
		}
		if _, ok := r.interp.Lookup(p.Name); !ok {
			t.Errorf("%s is not registered", p.Name)
		}
	}
	// The other direction: nothing the doc lists is missing.
	registered := map[string]bool{}
	for _, p := range r.hostOperations() {
		registered[p.Name] = true
	}
	section := string(doc)
	section = section[strings.Index(section, "## The host interface"):]
	section = section[:strings.Index(section[3:], "\n## ")+3]
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `(") {
			continue
		}
		name := strings.Fields(strings.TrimPrefix(line, "| `("))[0]
		name = strings.TrimRight(name, ")`")
		if !registered[name] {
			t.Errorf("docs/extending.md documents (%s), which is not a host operation", name)
		}
	}
}

func TestInitPath(t *testing.T) {
	home := func() (string, error) { return "/home/u", nil }
	noHome := func() (string, error) { return "", errors.New("no home") }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		env  map[string]string
		home func() (string, error)
		want string
		ok   bool
	}{
		{"xdg", map[string]string{"XDG_CONFIG_HOME": "/x/cfg"}, home, "/x/cfg/coigue/init.scm", true},
		{"unset", nil, home, "/home/u/.config/coigue/init.scm", true},
		{"empty", map[string]string{"XDG_CONFIG_HOME": ""}, home, "/home/u/.config/coigue/init.scm", true},
		{"relative is ignored", map[string]string{"XDG_CONFIG_HOME": "cfg"}, home, "/home/u/.config/coigue/init.scm", true},
		{"xdg without a home", map[string]string{"XDG_CONFIG_HOME": "/x"}, noHome, "/x/coigue/init.scm", true},
		{"no home", nil, noHome, "", false},
	} {
		got, ok := InitPath(env(c.env), c.home)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: %q, %v", c.name, got, ok)
		}
	}
}

func TestReadInit(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := ReadInit(filepath.Join(dir, "missing.scm")); found || err != nil {
		t.Fatalf("missing: found %v, err %v", found, err)
	}
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if src, found, err := ReadInit(write("ok.scm", []byte("(+ 1 2)"))); src != "(+ 1 2)" || !found || err != nil {
		t.Fatalf("ok: %q %v %v", src, found, err)
	}
	if _, found, err := ReadInit(write("big.scm", make([]byte, MaxInitSize+1))); !found || err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("big: found %v, err %v", found, err)
	}
	if _, found, err := ReadInit(write("exact.scm", make([]byte, MaxInitSize))); !found || err != nil {
		t.Fatalf("exactly the limit: found %v, err %v", found, err)
	}
	if _, found, err := ReadInit(dir); !found || err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory: found %v, err %v", found, err)
	}
	if _, found, err := ReadInit(filepath.Join(dir, "ok.scm", "inside")); !found || err == nil {
		t.Fatalf("a file where a directory should be: found %v, err %v", found, err)
	}
}

func TestDeepRecursionIsAnErrorNotACrash(t *testing.T) {
	r := loaded(t, Options{}, `(define (deep n) (+ 1 (deep (+ n 1)))) (define (go) (deep 0)) (define-command "go" go)`)
	err := r.Run(context.Background(), "go", nil, session("x", 0, 0, nil, false))
	var re *RunError
	if !errors.As(err, &re) || re.Stop != Failed || !strings.Contains(err.Error(), "recursion too deep") {
		t.Fatalf("error %v", err)
	}
}

func TestOperationsRefuseOtherGoroutinesEvenDuringTheInvocation(t *testing.T) {
	var fromOther error
	var legit scheme.Value
	r := newRuntime(Options{}, &scheme.Prim{
		Name: "stray-callback",
		Meta: scheme.Meta{Doc: "Test: call a host operation from another goroutine with this call's context."},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			op, _ := c.In.Lookup("replace-selection")
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, fromOther = op.Handler(&scheme.Call{Ctx: c.Ctx, In: c.In, Name: op.Name, Args: []scheme.Value{scheme.Str("STRAY")}})
			}()
			<-done
			// The goroutine that owns the invocation is still honoured.
			var err error
			legit, err = op.Handler(&scheme.Call{Ctx: c.Ctx, In: c.In, Name: op.Name, Args: []scheme.Value{scheme.Str("ok")}})
			return legit, err
		},
	})
	if err := r.Load(context.Background(), `(define (go) (stray-callback)) (define-command "go" go)`); err != nil {
		t.Fatal(err)
	}
	s := session("abc", 0, 1, &[2]int{0, 0}, false)
	if err := r.Run(context.Background(), "go", nil, s); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(fromOther, errNotInCommand) {
		t.Fatalf("a callback from another goroutine: %v", fromOther)
	}
	if s.Buffer.Text() != "okc" {
		t.Fatalf("text %q", s.Buffer.Text())
	}
}

func TestGoroutineIDDiffersBetweenGoroutines(t *testing.T) {
	here := goroutineID()
	other := make(chan uint64)
	go func() { other <- goroutineID() }()
	if got := <-other; here == 0 || got == 0 || got == here {
		t.Fatalf("ids %d and %d", here, got)
	}
}
