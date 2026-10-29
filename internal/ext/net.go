package ext

import (
	"context"
	"fmt"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// The network. Brote's net module is installed once, with the widest policy
// any module could be given (any public host, any method, 2 MB, 20 s), and
// every step of every request is then narrowed to the grant of the module
// whose code started it: the request, each redirect and each connection (see
// WithNetCheck in Brote's documentation). A module with no grant, init.scm
// and the editor's own evaluations have none, so a step with no module behind
// it is refused. The gate adds the rule that the editor's own goroutine never
// waits on the network: a command starts a task and hears back through
// on-done.

// installedPolicy is the outer limit. The private-address rule is the
// default one, so no module can reach loopback, a private or a link-local
// address, whatever it asks.
const installedPolicy = `(policy (encoding)
  (net (allow *) (methods get head post put patch delete options) (max-body 2MB) (timeout 20s)))`

// allowPrivateForTests lets tests reach a server on 127.0.0.1. It is set from
// test files only.
var allowPrivateForTests bool

// netEntry is what the network check and the gate know about one module.
type netEntry struct {
	policy *std.Policy // nil when the request was not granted
	why    string      // when it was not
}

func (r *Runtime) installStd() {
	src := installedPolicy
	if allowPrivateForTests {
		src = `(policy (encoding)
  (net (allow *) (methods get head post put patch delete options) (max-body 2MB) (timeout 20s) (private #t)))`
	}
	pol, err := std.ParsePolicy(src)
	if err != nil {
		panic(err)
	}
	rt, err := std.Install(r.interp, pol, std.WithNetCheck(r.netCheck))
	if err != nil {
		panic(err)
	}
	r.std = rt
}

// decideGrant records what a module's header asked for and what it gets.
func (r *Runtime) decideGrant(m *module) {
	if m.asked.None() {
		return
	}
	m.granted = m.asked
	if r.opts.Grants != nil {
		m.granted, m.denied = r.opts.Grants(m.name, m.asked)
	}
	switch {
	case m.granted.None():
		why := m.denied
		if why == "" {
			why = "not granted"
		}
		m.grantNote = fmt.Sprintf("asks for network access (%s), which was not granted: %s", m.asked.Describe(), why)
	case !m.asked.Within(m.granted):
		m.grantNote = fmt.Sprintf("asks for more network access (%s) than it was granted (%s)", m.asked.Describe(), m.granted.Describe())
	}
}

// publishNets makes the grants of the loaded modules visible to tasks, which
// run on other goroutines. It is called when a load ends, after which the
// map is never changed.
func (r *Runtime) publishNets() {
	nets := map[string]netEntry{}
	for name, m := range r.modules {
		if m.asked.None() {
			continue
		}
		e := netEntry{why: m.grantNote}
		if !m.granted.None() && m.asked.Within(m.granted) {
			if p, err := m.granted.Policy(); err == nil {
				e.policy = p
			} else {
				e.why = err.Error()
			}
		}
		nets[name] = e
	}
	r.nets.Store(&nets)
}

// netOf returns what the module running ctx's evaluation may reach.
func (r *Runtime) netOf(ctx context.Context) (netEntry, string) {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	if s == nil {
		return netEntry{}, ""
	}
	nets := r.nets.Load()
	if nets == nil {
		return netEntry{}, s.module
	}
	return (*nets)[s.module], s.module
}

func noGrant(module string) string {
	if module == InitModule {
		return "init.scm cannot use the network; put that code in a module (modules/NAME.scm) with (requires (net (allow \"host\"))) in its header"
	}
	return "this module has no network access; add (requires (net (allow \"host\"))) to its header"
}

// beforeNet is the gate's decision on a net procedure.
func (r *Runtime) beforeNet(ctx context.Context, p *scheme.Prim) error {
	e, module := r.netOf(ctx)
	switch {
	case e.policy == nil && e.why != "":
		return &scheme.Denied{Prim: p.Name, Reason: "this module's network access was not granted: " + e.why}
	case e.policy == nil:
		return &scheme.Denied{Prim: p.Name, Reason: noGrant(module)}
	}
	if s, _ := ctx.Value(sessionKey{}).(*Session); s != nil && s.live.Load() && s.owner == goroutineID() {
		return &scheme.Denied{Prim: p.Name, Reason: "a command cannot use the network while the editor waits; start a (spawn-detached ...) task and handle its result with on-done"}
	}
	return nil
}

// netCheck judges each step of a request against the calling module's grant.
func (r *Runtime) netCheck(ctx context.Context, ev std.NetEvent) error {
	e, module := r.netOf(ctx)
	if e.policy == nil {
		reason := noGrant(module)
		if e.why != "" {
			reason = "this module's network access was not granted: " + e.why
		}
		return &scheme.Denied{Prim: "http-request", Reason: reason}
	}
	if err := e.policy.CheckNetEvent(ev); err != nil {
		return &scheme.Denied{Prim: "http-request", Reason: err.Error()}
	}
	return nil
}

// introspection are Brote's require, modules and module-info. They describe
// the installed policy, not the calling module's grant, so they are refused.
var introspection = map[string]bool{"require": true, "modules": true, "module-info": true}
