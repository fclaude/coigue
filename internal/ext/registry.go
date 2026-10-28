package ext

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/key"
)

// pendingCommand is one define-command, remembered until the load finishes
// and the short names are decided.
type pendingCommand struct {
	owner, name, doc string
	proc             scheme.Value
}

// pendingBinding is one bind-key. Two modules may bind the same key; that is
// decided in resolve, not when the key is bound.
type pendingBinding struct {
	owner string
	b     *Binding
}

// starter is one module's on-start procedure, or init.scm's.
type starter struct {
	owner string
	proc  scheme.Value
}

func (r *Runtime) commandTaken(owner, name string) bool {
	for _, c := range r.pendingCommands {
		if c.owner == owner && c.name == name {
			return true
		}
	}
	return false
}

// QualifiedCommands lists every defined command as module/name, including
// init/name for init.scm. There is one for each define-command that succeeded.
func (r *Runtime) QualifiedCommands() []Command {
	out := make([]Command, len(r.qualified))
	copy(out, r.qualified)
	return out
}

// resolve decides the short command names and the keys. init.scm wins both.
// A short command name claimed by several modules and not by init.scm stays,
// and running it reports the ambiguity. A key claimed by several modules and
// not by init.scm is left unbound, which fails the load.
func (r *Runtime) resolve() error {
	for _, m := range r.modules {
		m.extra = nil
	}
	if r.root != nil {
		r.root.extra = nil
	}
	r.commands = map[string]*Command{}
	r.order = nil
	r.qualified = nil
	r.bindings = nil

	groups := map[string][]pendingCommand{}
	var seen []string
	for _, c := range r.pendingCommands {
		if _, ok := groups[c.name]; !ok {
			seen = append(seen, c.name)
		}
		groups[c.name] = append(groups[c.name], c)
		q := &Command{Name: c.owner + "/" + c.name, Doc: c.doc, proc: c.proc}
		r.commands[q.Name] = q
		r.qualified = append(r.qualified, *q)
	}
	for _, name := range seen {
		regs := groups[name]
		var fromInit *pendingCommand
		var mods []pendingCommand
		for i := range regs {
			if regs[i].owner == InitModule {
				fromInit = &regs[i]
			} else {
				mods = append(mods, regs[i])
			}
		}
		switch {
		case fromInit != nil:
			r.addShort(fromInit.name, fromInit.doc, fromInit.proc, "")
			for _, m := range mods {
				r.note(m.owner, fmt.Sprintf("command %s is displaced by init.scm; :%s/%s still runs it", m.name, m.owner, m.name))
			}
		case len(mods) == 1:
			r.addShort(mods[0].name, mods[0].doc, mods[0].proc, "")
		case len(mods) > 1:
			names := make([]string, len(mods))
			for i, m := range mods {
				names[i] = m.owner + "/" + m.name
			}
			sort.Strings(names)
			msg := name + " is ambiguous: " + strings.Join(names, ", ")
			r.addShort(name, msg, scheme.Value{}, msg)
			for _, m := range mods {
				r.note(m.owner, fmt.Sprintf("command %s is ambiguous; :%s/%s runs this one", m.name, m.owner, m.name))
			}
		}
	}

	type bkey struct {
		mode string
		k    key.Key
	}
	bgroups := map[bkey][]pendingBinding{}
	var bseen []bkey
	for _, p := range r.pendingBindings {
		k := bkey{p.b.Mode, p.b.Key}
		if _, ok := bgroups[k]; !ok {
			bseen = append(bseen, k)
		}
		bgroups[k] = append(bgroups[k], p)
	}
	var clashes []string
	for _, k := range bseen {
		group := bgroups[k]
		var fromInit *pendingBinding
		var mods []pendingBinding
		for i := range group {
			if group[i].owner == InitModule {
				fromInit = &group[i]
			} else {
				mods = append(mods, group[i])
			}
		}
		switch {
		case fromInit != nil:
			r.bindings = append(r.bindings, fromInit.b)
			for _, m := range mods {
				r.note(m.owner, fmt.Sprintf("%s %s is displaced by init.scm", m.b.Mode, m.b.Spec))
			}
		case len(mods) == 1:
			r.bindings = append(r.bindings, mods[0].b)
		case len(mods) > 1:
			clashes = append(clashes, clashText(mods))
			owners := make([]string, len(mods))
			for i, m := range mods {
				owners[i] = m.owner
			}
			sort.Strings(owners)
			for _, m := range mods {
				var others []string
				for _, o := range owners {
					if o != m.owner {
						others = append(others, o)
					}
				}
				r.note(m.owner, fmt.Sprintf("%s %s is also bound by %s, so it is left unbound", m.b.Mode, m.b.Spec, joinAnd(others)))
			}
		}
	}
	if len(clashes) > 0 {
		return fmt.Errorf("%s", strings.Join(clashes, "; "))
	}
	return nil
}

func (r *Runtime) addShort(name, doc string, proc scheme.Value, clash string) {
	c := &Command{Name: name, Doc: doc, proc: proc, Clash: clash}
	r.commands[name] = c
	r.order = append(r.order, name)
}

func (r *Runtime) note(owner, text string) {
	if m := r.moduleBy(owner); m != nil {
		m.extra = append(m.extra, text)
	}
}

func clashText(group []pendingBinding) string {
	sorted := append([]pendingBinding(nil), group...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].owner < sorted[j].owner })
	parts := make([]string, len(sorted))
	for i, g := range sorted {
		parts[i] = fmt.Sprintf("%s (binding %d, %s)", g.owner, g.b.Number, describe(g.b.Doc))
	}
	return fmt.Sprintf("%s %s is bound by %s; a key has no qualified name, so it is left unbound",
		sorted[0].b.Mode, sorted[0].b.Spec, joinAnd(parts))
}

func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}
