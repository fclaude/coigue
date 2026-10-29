package ext

import (
	"context"
	"errors"

	"github.com/fclaude/brote/scheme"
)

// gate is the policy in front of every primitive application. Extensions get
// the host operations of this package and Brote's pure language; anything that
// reaches the operating system, the network or something irreversible is
// refused, wherever it was called from. Brote's own `load` is the one builtin
// declared Host.
type gate struct{ r *Runtime }

func (g gate) Before(ctx context.Context, p *scheme.Prim, _ []scheme.Value) error {
	if g.r != nil && g.r.std != nil {
		switch g.r.std.ModuleOf(p.Name) {
		case "net":
			return g.r.beforeNet(ctx, p)
		case "encoding":
			return nil
		case "":
			if introspection[p.Name] {
				return &scheme.Denied{Prim: p.Name, Reason: "this describes the editor's own policy; a module's network access is the (requires (net ...)) in its header"}
			}
		}
	}
	switch {
	case p.Meta.Host:
		return &scheme.Denied{Prim: p.Name, Reason: "editor extensions cannot use operating-system operations"}
	case p.Meta.Egress:
		return &scheme.Denied{Prim: p.Name, Reason: "editor extensions cannot send data off the machine"}
	case p.Meta.Irreversible:
		return &scheme.Denied{Prim: p.Name, Reason: "editor extensions cannot make irreversible changes"}
	}
	return nil
}

// ConcurrentSafe says the gate keeps no state, so tasks may call it at once.
func (g gate) ConcurrentSafe() {}

func (g gate) RegisterChecker(string, string, scheme.Value) error {
	return errors.New("editor extensions cannot add policy checks")
}
