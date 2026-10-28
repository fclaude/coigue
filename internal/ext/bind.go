package ext

import (
	"context"
	"fmt"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/key"
)

// MaxBindings bounds the key bindings a program can make.
const MaxBindings = 100

// Modes a key can be bound in. "visual" covers characterwise and linewise
// selections alike.
const (
	ModeNormal = "normal"
	ModeVisual = "visual"
)

// Binding is a key the program bound to a procedure.
type Binding struct {
	Mode string
	Key  key.Key
	// Spec is the key as the program wrote it, for messages and the guide.
	Spec string
	Doc  string
	// Override says the program asked to shadow a built-in meaning of the key.
	Override bool
	// Number counts the program's bindings from 1, so a conflict can name both.
	Number int
	proc   scheme.Value
}

// Bindings lists the key bindings in the order they were made.
func (r *Runtime) Bindings() []Binding {
	out := make([]Binding, len(r.bindings))
	for i, b := range r.bindings {
		out[i] = *b
	}
	return out
}

// Binding finds what the program bound k to in mode. The Shift modifier does
// not count: terminals report capital letters with it or without it.
func (r *Runtime) Binding(mode string, k key.Key) (Binding, bool) {
	k.Mods &^= key.Shift
	for _, b := range r.bindings {
		if b.Mode == mode && b.Key == k {
			return *b, true
		}
	}
	return Binding{}, false
}

// RunBinding calls the procedure bound to k in mode with no arguments,
// working on s, under the same rules as Run.
func (r *Runtime) RunBinding(ctx context.Context, mode string, k key.Key, s *Session) error {
	k.Mods &^= key.Shift
	for _, b := range r.bindings {
		if b.Mode == mode && b.Key == k {
			return r.runProc(ctx, b.proc, s)
		}
	}
	return &RunError{Stop: Failed, Err: fmt.Errorf("no binding for %s in %s mode", k.Code, mode)}
}

// HasStart reports whether the program registered a start-up procedure.
func (r *Runtime) HasStart() bool { return len(r.starts) > 0 }

// StartOwners lists who registered a start-up procedure, in the order they
// run: modules in the order they loaded, then init.scm.
func (r *Runtime) StartOwners() []string {
	out := make([]string, len(r.starts))
	for i, st := range r.starts {
		out[i] = st.owner
	}
	return out
}

// RunStart calls one start-up procedure, under the same rules as Run: the
// init.scm one when it registered one, otherwise the only one. The editor
// runs each owner itself, through RunStartOwner, so each has its own undo
// step and deadline.
func (r *Runtime) RunStart(ctx context.Context, s *Session) error {
	if len(r.starts) == 0 {
		return &RunError{Stop: Failed, Err: fmt.Errorf("no on-start procedure")}
	}
	owner := r.starts[0].owner
	for _, st := range r.starts {
		if st.owner == InitModule {
			owner = InitModule
			break
		}
	}
	return r.RunStartOwner(ctx, owner, s)
}

// RunStartOwner calls the start-up procedure owner registered.
func (r *Runtime) RunStartOwner(ctx context.Context, owner string, s *Session) error {
	for _, st := range r.starts {
		if st.owner == owner {
			return r.runProc(ctx, st.proc, s)
		}
	}
	return &RunError{Stop: Failed, Err: fmt.Errorf("no on-start procedure")}
}

func (r *Runtime) runProc(ctx context.Context, proc scheme.Value, s *Session) error {
	s.kind = kindCommand
	return r.invoke(ctx, s, func(ctx context.Context) error {
		_, err := r.interp.Apply(ctx, proc, nil)
		return err
	})
}

func (r *Runtime) bindKey(c *scheme.Call) (scheme.Value, error) {
	if _, err := sessionFor(c, kindInit); err != nil {
		return scheme.Value{}, err
	}
	mode, err := c.String(0)
	if err != nil {
		return scheme.Value{}, err
	}
	spec, err := c.String(1)
	if err != nil {
		return scheme.Value{}, err
	}
	proc, err := c.Proc(2)
	if err != nil {
		return scheme.Value{}, err
	}
	doc, override := "", false
	if len(c.Args) > 3 {
		if doc, err = c.String(3); err != nil {
			return scheme.Value{}, err
		}
	}
	if len(c.Args) > 4 {
		if c.Args[4].Kind != scheme.KBool {
			return scheme.Value{}, fmt.Errorf("override is #t or #f")
		}
		override = c.Args[4].IsTrue()
	}
	owner := r.owner()
	if override && owner != InitModule {
		return scheme.Value{}, fmt.Errorf("only init.scm may override a built-in key")
	}
	if mode != ModeNormal && mode != ModeVisual {
		return scheme.Value{}, fmt.Errorf("%q is not a mode: use \"normal\" or \"visual\"", mode)
	}
	k, err := key.ParseSpec(spec)
	if err != nil {
		return scheme.Value{}, err
	}
	number := len(r.pendingBindings) + 1
	for _, b := range r.pendingBindings {
		if b.owner == owner && b.b.Mode == mode && b.b.Key == k {
			return scheme.Value{}, fmt.Errorf("%s %s is already bound by binding %d (%s); binding %d (%s) cannot also use it",
				mode, spec, b.b.Number, describe(b.b.Doc), number, describe(doc))
		}
	}
	if len(r.pendingBindings) >= MaxBindings {
		if owner == InitModule {
			return scheme.Value{}, fmt.Errorf("at most %d keys can be bound", MaxBindings)
		}
		return scheme.Value{}, fmt.Errorf("at most %d keys can be bound; %s reached the limit", MaxBindings, owner)
	}
	if r.opts.Bind != nil {
		if err := r.opts.Bind(mode, k, override); err != nil {
			if owner != InitModule {
				return scheme.Value{}, fmt.Errorf("%s: %w", owner, err)
			}
			return scheme.Value{}, err
		}
	}
	doc = Tidy(doc)
	r.pendingBindings = append(r.pendingBindings, pendingBinding{owner: owner, b: &Binding{
		Mode: mode, Key: k, Spec: spec, Doc: doc, Override: override, Number: number, proc: proc,
	}})
	if owner != InitModule {
		if m := r.modules[owner]; m != nil {
			m.keys = append(m.keys, mode+" "+spec)
		}
	}
	return scheme.Void, nil
}

func describe(doc string) string {
	if doc == "" {
		return "no description"
	}
	return fmt.Sprintf("%q", Tidy(doc))
}

func (r *Runtime) onStart(c *scheme.Call) (scheme.Value, error) {
	if _, err := sessionFor(c, kindInit); err != nil {
		return scheme.Value{}, err
	}
	proc, err := c.Proc(0)
	if err != nil {
		return scheme.Value{}, err
	}
	owner := r.owner()
	for _, st := range r.starts {
		if st.owner != owner {
			continue
		}
		if owner == InitModule {
			return scheme.Value{}, fmt.Errorf("on-start was already called; there is one start-up procedure")
		}
		return scheme.Value{}, fmt.Errorf("on-start was already called in %s", owner)
	}
	r.starts = append(r.starts, starter{owner: owner, proc: proc})
	return scheme.Void, nil
}
