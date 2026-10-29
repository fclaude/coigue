package ext

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/fclaude/brote/scheme"
)

// Events turn Brote channels and tasks into editor commands. A command
// registers a channel with on-channel, or a task with on-done, and when
// something arrives the editor runs the handler as a command of its own, with
// a fresh copy of the buffer. timer makes a channel that receives once after a
// delay. The host never waits on any of it: it looks between keys, through the
// Go handles Brote gives a host (scheme.Channel and scheme.Task), which never
// take part in Brote's deadlock detection.

// handlerReg is one on-channel.
type handlerReg struct {
	ch      scheme.Channel
	handler scheme.Value
	origin  string // the command that registered it, for messages
}

// doneReg is one on-done.
type doneReg struct {
	task    scheme.Task
	handler scheme.Value
	origin  string
}

// timerReq is one timer: a channel to send #t on, and when.
type timerReq struct {
	ch  scheme.Channel
	due time.Time
}

// Event is one thing ready for its handler: a value that arrived on a
// channel, or a task that finished.
type Event struct {
	handler scheme.Value
	arg     scheme.Value
	origin  string
}

// Origin names the command that registered the handler, or "".
func (e Event) Origin() string { return e.origin }

// minTimer and maxTimer bound a timer's delay, in seconds.
const (
	minTimer = 0.01
	maxTimer = 7 * 24 * 3600
)

func (r *Runtime) onChannel(c *scheme.Call, s *Session) (scheme.Value, error) {
	ch, ok := scheme.AsChannel(c.Args[0])
	if !ok {
		return scheme.Value{}, fmt.Errorf("argument 1 must be a channel made by make-channel; got %s", scheme.Write(c.Args[0]))
	}
	handler, err := c.Proc(1)
	if err != nil {
		return scheme.Value{}, err
	}
	if len(r.handlers)+len(s.handlers) >= MaxHandlers {
		return scheme.Value{}, fmt.Errorf("at most %d channels can have a handler", MaxHandlers)
	}
	for _, h := range append(append([]*handlerReg{}, r.handlers...), s.handlers...) {
		if h.ch == ch {
			return scheme.Value{}, errors.New("that channel already has a handler")
		}
	}
	// The channel is deliberately not marked HostFed. The editor takes values
	// between keys, never while a command runs, so a command that waits on this
	// channel for the editor to fill it is deadlocked, and Brote should say so
	// at once instead of the command running into its deadline.
	s.handlers = append(s.handlers, &handlerReg{ch: ch, handler: handler, origin: s.label})
	return scheme.Void, nil
}

func (r *Runtime) timer(c *scheme.Call, s *Session) (scheme.Value, error) {
	n, err := c.Number(0)
	if err != nil {
		return scheme.Value{}, err
	}
	secs := float64(n.I)
	if n.Kind == scheme.KFloat {
		secs = n.F
	}
	if math.IsNaN(secs) || secs < minTimer || secs > maxTimer {
		return scheme.Value{}, fmt.Errorf("a timer is %g to %d seconds; got %g", minTimer, maxTimer, secs)
	}
	if len(r.timers)+len(s.timers) >= MaxTimers {
		return scheme.Value{}, fmt.Errorf("at most %d timers can be waiting", MaxTimers)
	}
	ch, err := scheme.NewChannel(r.interp, 1)
	if err != nil {
		return scheme.Value{}, err
	}
	s.timers = append(s.timers, &timerReq{ch: ch, due: time.Now().Add(time.Duration(secs * float64(time.Second)))})
	return ch.Value(), nil
}

func (r *Runtime) onDone(c *scheme.Call, s *Session) (scheme.Value, error) {
	task, ok := scheme.AsTask(c.Args[0])
	if !ok {
		return scheme.Value{}, fmt.Errorf("argument 1 must be a task made by spawn-detached; got %s", scheme.Write(c.Args[0]))
	}
	handler, err := c.Proc(1)
	if err != nil {
		return scheme.Value{}, err
	}
	if len(r.dones)+len(s.dones) >= MaxHandlers {
		return scheme.Value{}, fmt.Errorf("at most %d tasks can have a handler", MaxHandlers)
	}
	for _, d := range append(append([]*doneReg{}, r.dones...), s.dones...) {
		if d.task == task {
			return scheme.Value{}, errors.New("that task already has a handler")
		}
	}
	s.dones = append(s.dones, &doneReg{task: task, handler: handler, origin: s.label})
	return scheme.Void, nil
}

// Adopt takes the registrations a successful command made. A command that
// failed registers nothing, like it changes no text.
func (r *Runtime) Adopt(s *Session) {
	r.adoptMarks(s)
	r.handlers = append(r.handlers, s.handlers...)
	r.dones = append(r.dones, s.dones...)
	r.timers = append(r.timers, s.timers...)
	r.windows = append(r.windows, s.windows...)
	s.handlers, s.dones, s.timers, s.windows = nil, nil, nil, nil
}

// Waiting reports how many channels and tasks have a handler and how many
// timers have not fired.
func (r *Runtime) Waiting() (handlers, timers int) {
	return len(r.handlers) + len(r.dones), len(r.timers)
}

// FireTimers sends #t on the channel of every timer that is due.
func (r *Runtime) FireTimers(now time.Time) {
	kept := r.timers[:0]
	for _, t := range r.timers {
		if now.Before(t.due) {
			kept = append(kept, t)
			continue
		}
		_, _ = t.ch.TrySend(scheme.True)
	}
	r.timers = kept
}

// PollEvents takes up to max things that are ready for their handlers: values
// from channels that have one, and tasks that finished. A channel that is
// closed and empty, and a task that finished, lose their handler. It never
// waits.
func (r *Runtime) PollEvents(max int) []Event {
	var events []Event
	keptCh := r.handlers[:0]
	for _, h := range r.handlers {
		alive := true
		for len(events) < max {
			v, ok, closed := h.ch.TryRecv()
			if ok {
				events = append(events, Event{handler: h.handler, arg: v, origin: h.origin})
				continue
			}
			alive = !closed
			break
		}
		if alive {
			keptCh = append(keptCh, h)
		}
	}
	r.handlers = keptCh

	keptDone := r.dones[:0]
	for _, d := range r.dones {
		select {
		case <-d.task.Done():
			if len(events) >= max {
				keptDone = append(keptDone, d)
				continue
			}
			r.remember(d.task)
			// A task that was cancelled (by task-cancel!, :reload or quitting)
			// has nothing to report; a failed one is reported to its handler,
			// whose task-wait raises the error.
			if _, err := d.task.Result(); errors.Is(err, context.Canceled) {
				continue
			}
			events = append(events, Event{handler: d.handler, arg: d.task.Value(), origin: d.origin})
		default:
			keptDone = append(keptDone, d)
		}
	}
	r.dones = keptDone
	return events
}

// RunEvent runs the handler of ev on its argument, working on s like Run does.
func (r *Runtime) RunEvent(ctx context.Context, ev Event, s *Session) error {
	s.kind = kindCommand
	s.label = ev.origin
	return r.invoke(ctx, s, func(ctx context.Context) error {
		_, err := r.interp.Apply(ctx, ev.handler, []scheme.Value{ev.arg})
		return err
	})
}

// taskFailure is a detached task that failed with nobody waiting for it.
type taskFailure struct {
	task scheme.Task
	err  error
}

// failures collects what scheme.WithTaskFailed reports. The hook runs on the
// failed task's own goroutine, so it only records; the editor reads the list
// between keys.
type failures struct {
	mu   sync.Mutex
	list []taskFailure
}

func (f *failures) add(t scheme.Task, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.list) < 64 { // a failure storm must not grow without bound
		f.list = append(f.list, taskFailure{task: t, err: err})
	}
}

// TakeFailures returns the errors of background tasks that failed with nobody
// waiting for them, oldest first. A task that has an on-done handler is not
// among them: the handler is told, and its task-wait raises the error.
func (r *Runtime) TakeFailures() []error {
	r.failed.mu.Lock()
	list := r.failed.list
	r.failed.list = nil
	r.failed.mu.Unlock()
	var out []error
	for _, f := range list {
		if r.watched(f.task) {
			continue
		}
		out = append(out, f.err)
	}
	return out
}

// remember notes a task whose handler has just been taken, so a failure the
// hook reports a moment later is still known to have been handled.
func (r *Runtime) remember(t scheme.Task) {
	const most = 64
	r.recent = append(r.recent, t)
	if len(r.recent) > most {
		r.recent = r.recent[len(r.recent)-most:]
	}
}

// watched reports whether a task has an on-done handler, or had one that has
// just been run.
func (r *Runtime) watched(t scheme.Task) bool {
	for _, d := range r.dones {
		if d.task == t {
			return true
		}
	}
	for _, d := range r.recent {
		if d == t {
			return true
		}
	}
	return false
}

// Shutdown ends every background task and timer and waits for them, as long
// as ctx allows. Call it when the runtime is replaced (:reload) or the editor
// closes, so nothing started by old code outlives it.
func (r *Runtime) Shutdown(ctx context.Context) error { return r.interp.Shutdown(ctx) }
