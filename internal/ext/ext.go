// Package ext runs the user's Scheme extensions: one Brote interpreter per
// editor, the host operations a Scheme program may use, and the limits on
// every run. It knows nothing of the editor's state. An invocation works on a
// Session, a private copy of the buffer that the editor builds before the run
// and applies after it succeeded, so a failure, a timeout or an interrupt
// leaves nothing behind. See docs/extending.md for the contract.
//
// Threading. A Runtime is used by one goroutine at a time, the editor's.
// Scheme is evaluated, and every host operation runs, on the goroutine that
// calls Run or Load. Two helper goroutines exist during a run, both started
// through guard.Go: the Watcher the editor supplies (it reads the keyboard)
// and a memory watchdog. They call the cancel function and nothing else.
package ext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"runtime/metrics"
	"sync/atomic"
	"time"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/theme"
)

// Limits. They are part of the documented contract.
const (
	// MaxRegion bounds, in characters, what one read returns and what one
	// edit inserts.
	MaxRegion = 1 << 16
	// MaxInserted bounds the characters all edits of one command insert.
	MaxInserted = 1 << 20
	// MaxMessage bounds a status message, in characters.
	MaxMessage = 500
	// MaxCommands bounds the commands a program can define.
	MaxCommands = 100
	// MaxInitSize bounds the init file, in bytes.
	MaxInitSize = 1 << 20
	// DefaultDeadline bounds one run.
	DefaultDeadline = 5 * time.Second
	// DefaultMemoryLimit is how far the heap may grow during one run.
	DefaultMemoryLimit = 128 << 20
	memoryPoll         = 10 * time.Millisecond
)

// Watcher watches for the user's request to stop a running extension. It
// starts watching and arranges for interrupt to be called, from any
// goroutine, if the user asks; stop ends the watch and returns once the
// watcher no longer runs.
type Watcher func(interrupt func()) (stop func())

// Options configure a Runtime. The zero value is usable.
type Options struct {
	// Define is called, while the init file loads, for each command the
	// program defines, after the name was checked. Returning an error
	// rejects the definition and raises it in the program.
	Define func(name, doc string) error
	// Themes holds the themes define-theme adds to and set-theme chooses
	// from; nil means the runtime keeps a set of its own.
	Themes *theme.Set
	// SetTheme is called, while the init file loads, with the theme
	// set-theme chose; nil means the choice is checked and dropped.
	SetTheme func(theme.Theme)
	// Bind is called, while the init file loads, for each key the program
	// binds, after its syntax and its conflicts with the program's other
	// bindings were checked. It is where the editor refuses keys it keeps for
	// itself. Returning an error rejects the binding and raises it in the
	// program; nil accepts every key.
	Bind func(mode string, k key.Key, override bool) error
	// Watch lets the user interrupt a run; nil means no one can.
	Watch Watcher
	// Deadline bounds each run; 0 means DefaultDeadline.
	Deadline time.Duration
	// MemoryLimit is how far the heap may grow during a run; 0 means
	// DefaultMemoryLimit.
	MemoryLimit uint64
}

// Command is a command the program defined.
type Command struct {
	Name, Doc string
	proc      scheme.Value
}

var commandName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Runtime is the editor's interpreter. It is not safe for concurrent use.
type Runtime struct {
	interp      *scheme.Interp
	opts        Options
	deadline    time.Duration
	memoryLimit uint64
	commands    map[string]*Command
	order       []string
	bindings    []*Binding
	start       *scheme.Value
	themes      *theme.Set
	busy        atomic.Bool
}

// New creates a Runtime with the host operations registered, the gate
// installed and registration closed.
func New(opts Options) *Runtime { return newRuntime(opts) }

func newRuntime(opts Options, extra ...*scheme.Prim) *Runtime {
	r := &Runtime{
		interp:      scheme.New(scheme.WithOutput(io.Discard)),
		opts:        opts,
		deadline:    opts.Deadline,
		memoryLimit: opts.MemoryLimit,
		commands:    map[string]*Command{},
		themes:      opts.Themes,
	}
	if r.themes == nil {
		r.themes = &theme.Set{}
	}
	if r.deadline <= 0 {
		r.deadline = DefaultDeadline
	}
	if r.memoryLimit == 0 {
		r.memoryLimit = DefaultMemoryLimit
	}
	for _, p := range r.hostOperations() {
		if err := r.interp.Register(p); err != nil {
			panic(err)
		}
	}
	for _, p := range extra {
		if err := r.interp.Register(p); err != nil {
			panic(err)
		}
	}
	r.interp.SetGate(gate{})
	// No program can add a primitive (register-tool) once this is called.
	r.interp.Freeze()
	return r
}

// Commands lists the defined commands in the order they were defined.
func (r *Runtime) Commands() []Command {
	out := make([]Command, len(r.order))
	for i, name := range r.order {
		out[i] = *r.commands[name]
	}
	return out
}

// Stop says why a run did not complete.
type Stop uint8

// Reasons.
const (
	// Failed: the program raised an error, or an operation refused.
	Failed Stop = iota
	// Interrupted: the user asked the run to stop.
	Interrupted
	// Deadline: the run exceeded the deadline.
	Deadline
	// Memory: the heap grew past the limit.
	Memory
	// Internal: the interpreter or an operation panicked.
	Internal
)

// RunError is why Run or Load did not complete. Its text reads after the
// command name: "failed: selection-text: no selection...".
type RunError struct {
	Stop     Stop
	Err      error
	Deadline time.Duration
	Limit    uint64
}

func (e *RunError) Error() string {
	switch e.Stop {
	case Interrupted:
		return "interrupted"
	case Deadline:
		return "stopped after " + e.Deadline.String()
	case Memory:
		return fmt.Sprintf("stopped: used more than %d MiB of memory", e.Limit>>20)
	case Internal:
		return "failed: internal error: " + e.Err.Error()
	}
	return "failed: " + e.Err.Error()
}

func (e *RunError) Unwrap() error { return e.Err }

// Stopped reports whether the run was cut short from outside rather than
// failing on its own.
func (e *RunError) Stopped() bool {
	return e.Stop == Interrupted || e.Stop == Deadline || e.Stop == Memory
}

// ErrBusy is returned when a run starts while another is in progress on the
// same Runtime: the interpreter has no locking.
var ErrBusy = errors.New("the extension interpreter is already running")

// Load evaluates the init program, in which define-command is allowed.
// Definitions made before an error stay.
func (r *Runtime) Load(ctx context.Context, source string) error {
	return r.invoke(ctx, initSession(), func(ctx context.Context) error {
		_, err := r.interp.EvalString(ctx, source)
		return err
	})
}

// Run applies the command to the arguments, working on s. Nothing but s
// changes. On success the caller applies s; on any error it drops s.
func (r *Runtime) Run(ctx context.Context, name string, args []string, s *Session) error {
	cmd, ok := r.commands[name]
	if !ok {
		return &RunError{Stop: Failed, Err: fmt.Errorf("no extension command %q", name)}
	}
	values := make([]scheme.Value, len(args))
	for i, a := range args {
		values[i] = scheme.Str(a)
	}
	s.kind = kindCommand
	return r.invoke(ctx, s, func(ctx context.Context) error {
		_, err := r.interp.Apply(ctx, cmd.proc, values)
		return err
	})
}

func (r *Runtime) invoke(parent context.Context, s *Session, evaluate func(context.Context) error) (err error) {
	if !r.busy.CompareAndSwap(false, true) {
		return &RunError{Stop: Failed, Err: ErrBusy}
	}
	defer r.busy.Store(false)
	ctx, cancel := context.WithTimeout(parent, r.deadline)
	defer cancel()
	var interrupted, overMemory atomic.Bool
	if r.opts.Watch != nil {
		stop := r.opts.Watch(func() {
			interrupted.Store(true)
			cancel()
		})
		defer stop()
	}
	defer r.watchMemory(func() {
		overMemory.Store(true)
		cancel()
	})()
	s.owner = goroutineID()
	s.live.Store(true)
	defer s.live.Store(false)
	defer func() {
		// A bug in the interpreter or an operation must not take the editor,
		// and the unsaved text in it, down. s is private, so dropping it
		// loses nothing.
		if p := recover(); p != nil {
			err = &RunError{Stop: Internal, Err: fmt.Errorf("%v", p)}
		}
	}()
	err = evaluate(context.WithValue(ctx, sessionKey{}, s))
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &RunError{Stop: Deadline, Err: err, Deadline: r.deadline}
	case errors.Is(err, context.Canceled) && overMemory.Load():
		return &RunError{Stop: Memory, Err: err, Limit: r.memoryLimit}
	case errors.Is(err, context.Canceled):
		return &RunError{Stop: Interrupted, Err: err}
	}
	return &RunError{Stop: Failed, Err: err}
}

// watchMemory calls exceeded if the heap grows past the limit while it runs.
// Brote does not meter memory, so a program that doubles a string in a loop
// can use gigabytes in a few milliseconds, far inside any deadline. The heap
// figure counts garbage not yet collected, so a candidate is confirmed with a
// collection before the run is stopped.
func (r *Runtime) watchMemory(exceeded func()) (stop func()) {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	heap := func() uint64 {
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}
	limit := heap() + r.memoryLimit
	done, exited := make(chan struct{}), make(chan struct{})
	guard.Go(func() {
		defer close(exited)
		tick := time.NewTicker(memoryPoll)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if heap() > limit {
					runtime.GC()
					if heap() > limit {
						exceeded()
						return
					}
				}
			}
		}
	})
	return func() {
		close(done)
		<-exited
	}
}
