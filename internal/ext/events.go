package ext

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/fclaude/brote/scheme"
)

// Events turn a Brote channel into editor commands. A command registers a
// channel with on-channel, and for each value that arrives the editor runs the
// handler as a command of its own, with a fresh copy of the buffer. timer makes
// a channel that receives once after a delay. The host never waits on a
// channel: Brote reports a wait that nothing else can end as a deadlock, so
// the editor takes values with channel-try-recv, between keys.

// handlerReg is one on-channel.
type handlerReg struct {
	channel, handler scheme.Value
	origin           string // the command that registered it, for messages
}

// timerReq is one timer: a channel to send #t on, and when.
type timerReq struct {
	channel scheme.Value
	due     time.Time
}

// Event is one value ready for its handler.
type Event struct {
	reg   *handlerReg
	value scheme.Value
}

// Origin names the command that registered the handler, or "".
func (e Event) Origin() string { return e.reg.origin }

// none is what channel-try-recv gives back when nothing was ready. It is a
// native object, so no program can send one.
type none struct{}

func (*none) Kind() string   { return "none" }
func (*none) String() string { return "#<none>" }

var (
	noneObject = &none{}
	noneValue  = scheme.ObjectValue(noneObject)
)

func isNone(v scheme.Value) bool { return scheme.ObjectOf(v) == scheme.Object(noneObject) }

// minTimer and maxTimer bound a timer's delay, in seconds.
const (
	minTimer = 0.01
	maxTimer = 7 * 24 * 3600
)

func (r *Runtime) onChannel(c *scheme.Call, s *Session) (scheme.Value, error) {
	ch := c.Args[0]
	if o := scheme.ObjectOf(ch); o == nil || o.Kind() != "channel" {
		return scheme.Value{}, fmt.Errorf("argument 1 must be a channel made by make-channel; got %s", scheme.Write(ch))
	}
	handler, err := c.Proc(1)
	if err != nil {
		return scheme.Value{}, err
	}
	if len(r.handlers)+len(s.handlers) >= MaxHandlers {
		return scheme.Value{}, fmt.Errorf("at most %d channels can have a handler", MaxHandlers)
	}
	for _, h := range append(append([]*handlerReg{}, r.handlers...), s.handlers...) {
		if scheme.ObjectOf(h.channel) == scheme.ObjectOf(ch) {
			return scheme.Value{}, errors.New("that channel already has a handler")
		}
	}
	s.handlers = append(s.handlers, &handlerReg{channel: ch, handler: handler, origin: s.label})
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
	ch, err := c.In.Apply(c.Ctx, r.pristine["make-channel"], []scheme.Value{scheme.Int(1)})
	if err != nil {
		return scheme.Value{}, err
	}
	s.timers = append(s.timers, &timerReq{channel: ch, due: time.Now().Add(time.Duration(secs * float64(time.Second)))})
	return ch, nil
}

// Adopt takes the handlers and timers a successful command registered. A
// command that failed registers nothing, like it changes no text.
func (r *Runtime) Adopt(s *Session) {
	r.adoptMarks(s)
	r.handlers = append(r.handlers, s.handlers...)
	r.timers = append(r.timers, s.timers...)
	r.windows = append(r.windows, s.windows...)
	s.handlers, s.timers, s.windows = nil, nil, nil
}

// Waiting reports how many channels have a handler and how many timers have
// not fired.
func (r *Runtime) Waiting() (handlers, timers int) { return len(r.handlers), len(r.timers) }

// FireTimers sends #t on the channel of every timer that is due.
func (r *Runtime) FireTimers(ctx context.Context, now time.Time) {
	kept := r.timers[:0]
	for _, t := range r.timers {
		if now.Before(t.due) {
			kept = append(kept, t)
			continue
		}
		r.call(ctx, "channel-send!", t.channel, scheme.True)
	}
	r.timers = kept
}

// call applies one of Brote's own channel procedures outside any command.
func (r *Runtime) call(ctx context.Context, name string, args ...scheme.Value) (scheme.Value, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return r.interp.Apply(ctx, r.pristine[name], args)
}

// PollEvents takes up to max values from the channels that have a handler. A
// channel that is closed and empty loses its handler. It never waits.
func (r *Runtime) PollEvents(ctx context.Context, max int) []Event {
	var events []Event
	kept := r.handlers[:0]
	for _, h := range r.handlers {
		alive := true
		for len(events) < max {
			v, err := r.call(ctx, "channel-try-recv", h.channel, noneValue)
			if err != nil {
				alive = false
				break
			}
			if !isNone(v) {
				events = append(events, Event{reg: h, value: v})
				continue
			}
			// Empty. If it was closed, a value sent just before the close may have
			// arrived since the receive above, so take once more before leaving.
			closed, err := r.call(ctx, "channel-closed?", h.channel)
			if err != nil || !closed.IsTrue() {
				alive = err == nil
				break
			}
			v, err = r.call(ctx, "channel-try-recv", h.channel, noneValue)
			if err == nil && !isNone(v) {
				events = append(events, Event{reg: h, value: v})
				continue
			}
			alive = false
			break
		}
		if alive {
			kept = append(kept, h)
		}
	}
	r.handlers = kept
	return events
}

// RunEvent runs the handler of ev on its value, working on s like Run does.
func (r *Runtime) RunEvent(ctx context.Context, ev Event, s *Session) error {
	s.kind = kindCommand
	s.label = ev.reg.origin
	return r.invoke(ctx, s, func(ctx context.Context) error {
		_, err := r.interp.Apply(ctx, ev.reg.handler, []scheme.Value{ev.value})
		return err
	})
}
