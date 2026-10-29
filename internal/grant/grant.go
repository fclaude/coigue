// Package grant is what a module may reach beyond the editor. A module asks in
// its header, in Brote's policy vocabulary:
//
//	(define-module wordsmith
//	  (requires (net (allow "api.datamuse.com") (methods get))))
//
// The editor decides what it gives: a module a person wrote gets what it
// asks, and one installed from a repository gets what was approved at
// install. Only the network is offered, and only to hosts named one by one.
package grant

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// MaxHosts bounds the hosts one grant names.
const MaxHosts = 16

// methods are the HTTP methods Brote's net module knows, in the order they are
// shown.
var methods = []string{"get", "head", "post", "put", "patch", "delete", "options"}

var defaultMethods = []string{"get", "head"}

var hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// Grant is the network a module may reach. The zero value reaches nothing.
type Grant struct {
	// Hosts are "name" or "name:port", lower case and sorted. A name with no
	// port means the scheme's own: 443 for https, 80 for http.
	Hosts []string
	// Methods are lower case and in a fixed order; get and head by default.
	Methods []string
	// Private lets the module reach loopback and private addresses. A header
	// cannot ask for it; only Go code, in tests, sets it.
	Private bool
}

// None reports whether the grant reaches nothing.
func (g Grant) None() bool { return len(g.Hosts) == 0 }

// Parse reads the clauses of a (requires ...) form. Only (net (allow HOST ...)
// [(methods M ...)]) is offered.
func Parse(clauses []scheme.Value) (Grant, error) {
	var g Grant
	sawNet := false
	for _, clause := range clauses {
		parts, ok := clause.Slice()
		if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol {
			return Grant{}, fmt.Errorf("requires takes clauses like (net (allow \"host\"))")
		}
		if parts[0].S != "net" {
			return Grant{}, fmt.Errorf("requires: %q is not something a module can ask for; the only grant is net", parts[0].S)
		}
		if sawNet {
			return Grant{}, fmt.Errorf("requires: net is given twice")
		}
		sawNet = true
		net, err := parseNet(parts[1:])
		if err != nil {
			return Grant{}, err
		}
		g = net
	}
	return g, nil
}

// ParseNet reads one (net ...) form, as the lockfile stores a grant.
func ParseNet(v scheme.Value) (Grant, error) {
	parts, ok := v.Slice()
	if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol || parts[0].S != "net" {
		return Grant{}, fmt.Errorf("expected (net (allow \"host\" ...) (methods ...))")
	}
	return parseNet(parts[1:])
}

func parseNet(fields []scheme.Value) (Grant, error) {
	var g Grant
	var sawAllow, sawMethods bool
	for _, f := range fields {
		parts, ok := f.Slice()
		if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol {
			return Grant{}, fmt.Errorf("net: a clause is (allow \"host\" ...) or (methods get ...)")
		}
		switch parts[0].S {
		case "allow":
			if sawAllow {
				return Grant{}, fmt.Errorf("net: allow is given twice")
			}
			sawAllow = true
			for _, h := range parts[1:] {
				if h.Kind != scheme.KString {
					return Grant{}, fmt.Errorf("net: hosts are strings; wildcards are not offered, name each host")
				}
				host, err := canonHost(h.S)
				if err != nil {
					return Grant{}, err
				}
				g.Hosts = append(g.Hosts, host)
			}
		case "methods":
			if sawMethods {
				return Grant{}, fmt.Errorf("net: methods is given twice")
			}
			sawMethods = true
			for _, m := range parts[1:] {
				if m.Kind != scheme.KSymbol {
					return Grant{}, fmt.Errorf("net: methods are names like get and post")
				}
				name := strings.ToLower(m.S)
				if !contains(methods, name) {
					return Grant{}, fmt.Errorf("net: %q is not a method; use %s", m.S, strings.Join(methods, ", "))
				}
				g.Methods = append(g.Methods, name)
			}
		case "private", "max-body", "timeout":
			return Grant{}, fmt.Errorf("net: %s is not something a module can ask for; the editor sets the limits", parts[0].S)
		default:
			return Grant{}, fmt.Errorf("net: %q is not a clause; use allow or methods", parts[0].S)
		}
	}
	g.normalize()
	if g.None() {
		return Grant{}, fmt.Errorf("net: name at least one host, as in (net (allow \"api.example.com\"))")
	}
	if len(g.Hosts) > MaxHosts {
		return Grant{}, fmt.Errorf("net: at most %d hosts", MaxHosts)
	}
	if _, err := g.Policy(); err != nil {
		return Grant{}, err
	}
	return g, nil
}

func canonHost(s string) (string, error) {
	h := strings.ToLower(s)
	if strings.Contains(h, "*") {
		return "", fmt.Errorf("net: %q has a wildcard; name each host", s)
	}
	if !hostRE.MatchString(h) || len(h) > 253 {
		return "", fmt.Errorf("net: %q is not a host; write host or host:port, with no scheme or path", s)
	}
	return h, nil
}

func (g *Grant) normalize() {
	g.Hosts = uniqueSorted(g.Hosts)
	if len(g.Methods) == 0 {
		g.Methods = append([]string(nil), defaultMethods...)
		return
	}
	set := map[string]bool{}
	for _, m := range g.Methods {
		set[m] = true
	}
	g.Methods = g.Methods[:0:0]
	for _, m := range methods {
		if set[m] {
			g.Methods = append(g.Methods, m)
		}
	}
}

// FromModule reads the grant a module's header asks for. A file with no
// requires clause asks for nothing. The file need not be a valid module in
// other ways.
func FromModule(src string) (Grant, error) {
	forms, err := scheme.Read(src)
	if err != nil {
		return Grant{}, err
	}
	if len(forms) == 0 {
		return Grant{}, nil
	}
	items, ok := forms[0].Slice()
	if !ok || len(items) == 0 || items[0].Kind != scheme.KSymbol || items[0].S != "define-module" {
		return Grant{}, nil
	}
	var got Grant
	seen := false
	for _, clause := range items[1:] {
		parts, ok := clause.Slice()
		if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol || parts[0].S != "requires" {
			continue
		}
		if seen {
			return Grant{}, fmt.Errorf("requires is given twice")
		}
		seen = true
		if got, err = Parse(parts[1:]); err != nil {
			return Grant{}, err
		}
	}
	return got, nil
}

// Within reports whether everything g reaches, o reaches too.
func (g Grant) Within(o Grant) bool {
	if g.None() {
		return true
	}
	if g.Private && !o.Private {
		return false
	}
	for _, h := range g.Hosts {
		if !contains(o.Hosts, h) {
			return false
		}
	}
	for _, m := range g.Methods {
		if !contains(o.Methods, m) {
			return false
		}
	}
	return true
}

// Equal reports whether the two grants reach the same.
func (g Grant) Equal(o Grant) bool { return g.Within(o) && o.Within(g) }

// String is the grant as the header and the lockfile write it, or "" for none.
func (g Grant) String() string {
	if g.None() {
		return ""
	}
	var b strings.Builder
	b.WriteString("(net (allow")
	for _, h := range g.Hosts {
		fmt.Fprintf(&b, " %q", h)
	}
	b.WriteString(") (methods")
	for _, m := range g.Methods {
		b.WriteString(" " + m)
	}
	b.WriteString("))")
	return b.String()
}

// Describe says what the grant allows, for a person deciding: "can reach
// api.datamuse.com (GET, HEAD)".
func (g Grant) Describe() string {
	if g.None() {
		return "no network access"
	}
	upper := make([]string, len(g.Methods))
	for i, m := range g.Methods {
		upper[i] = strings.ToUpper(m)
	}
	return "can reach " + strings.Join(g.Hosts, ", ") + " (" + strings.Join(upper, ", ") + ")"
}

// Policy is the grant as a Brote policy, which judges requests for it.
func (g Grant) Policy() (*std.Policy, error) {
	var b strings.Builder
	b.WriteString("(policy (net (allow")
	for _, h := range g.Hosts {
		fmt.Fprintf(&b, " %q", h)
	}
	b.WriteString(") (methods " + strings.Join(g.Methods, " ") + ")")
	if g.Private {
		b.WriteString(" (private #t)")
	}
	b.WriteString("))")
	p, err := std.ParsePolicy(b.String())
	if err != nil {
		return nil, fmt.Errorf("net: %w", err)
	}
	return p, nil
}

// Widen lists what n reaches that o does not, in words, for a review page
// that marks a request growing. It is empty when n is within o.
func Widen(o, n Grant) []string {
	var out []string
	var hosts, ms []string
	for _, h := range n.Hosts {
		if !contains(o.Hosts, h) {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) > 0 {
		out = append(out, "new hosts: "+strings.Join(hosts, ", "))
	}
	if !o.None() {
		for _, m := range n.Methods {
			if !contains(o.Methods, m) {
				ms = append(ms, strings.ToUpper(m))
			}
		}
		if len(ms) > 0 {
			out = append(out, "new methods: "+strings.Join(ms, ", "))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	n := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[i-1] {
			out[n] = out[i]
			n++
		}
	}
	return out[:n]
}
