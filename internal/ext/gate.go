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
type gate struct{}

func (gate) Before(_ context.Context, p *scheme.Prim, _ []scheme.Value) error {
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

func (gate) RegisterChecker(string, string, scheme.Value) error {
	return errors.New("editor extensions cannot add policy checks")
}
