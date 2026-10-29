package ext

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/grant"
)

// InitModule is the owner of everything init.scm registers. It is not a file
// in the modules directory, and it cannot be required.
const InitModule = "init"

// Module is one module a load read, for the guide.
type Module struct {
	Name, Doc string
	Exports   []string
	Commands  []string // "name/command", every command the module defined
	Keys      []string // "mode spec", every key the module bound
	Notes     []string
}

// module is the environment and the bookkeeping of one loaded file.
type module struct {
	name    string
	doc     string
	loading bool
	ready   bool

	imports *scheme.Env
	defs    *scheme.Env
	// imported records the local name's origin, so a second import of the
	// same export is quiet and any other collision is an error.
	imported map[string]importOrigin
	// exportList is the names the header exports, in source order.
	exportList    []string
	exportsValues map[string]scheme.Value

	// asked is the network the header requests; granted is what the editor
	// gave, and why it is less when it is.
	asked     grant.Grant
	granted   grant.Grant
	denied    string
	grantNote string

	commands []string
	keys     []string
	// notes come from finishing the body (a definition hiding a built-in).
	// extra is rebuilt by resolve (a command or key that did not win).
	notes []string
	extra []string
}

type importOrigin struct {
	module, remote string
}

// importOp is one option of a require, applied left to right.
type importOp struct {
	kind  string
	names []string
}

type moduleError struct {
	name string
	err  error
}

func (e *moduleError) Error() string {
	text := strings.TrimPrefix(e.err.Error(), "eval: ")
	return e.name + ".scm: " + text
}

func (e *moduleError) Unwrap() error { return e.err }

func moduleErr(name string, err error) error {
	var already *moduleError
	if errors.As(err, &already) {
		return err
	}
	return &moduleError{name: name, err: err}
}

// CheckModuleName reports why name cannot name a module file, for code that
// writes modules/NAME.scm without loading it.
func CheckModuleName(name string) error { return checkModuleName(name) }

func checkModuleName(name string) error {
	switch name {
	case InitModule, "coigue":
		return fmt.Errorf("%q is a reserved module name", name)
	}
	if !commandName.MatchString(name) {
		return fmt.Errorf("%q is not a module name: use a lowercase letter, then lowercase letters, digits and -, up to 32 characters", name)
	}
	return nil
}

// owner is the module whose top-level code is running.
func (r *Runtime) owner() string {
	if n := len(r.loadStack); n > 0 {
		return r.loadStack[n-1]
	}
	return InitModule
}

func (r *Runtime) moduleBy(owner string) *module {
	if owner == InitModule {
		return r.root
	}
	return r.modules[owner]
}

// Modules lists the modules this load read, in the order they were first required.
func (r *Runtime) Modules() []Module {
	if r == nil {
		return nil
	}
	out := make([]Module, 0, len(r.moduleOrder))
	for _, name := range r.moduleOrder {
		m := r.modules[name]
		if m == nil {
			continue
		}
		out = append(out, m.info())
	}
	return out
}

// InitNotes are the visibility notes for init.scm itself, such as a
// definition that hides a built-in inside that file.
func (r *Runtime) InitNotes() []string {
	if r == nil || r.root == nil {
		return nil
	}
	return r.root.allNotes()
}

func (m *module) info() Module {
	return Module{
		Name:     m.name,
		Doc:      m.doc,
		Exports:  append([]string(nil), m.exportList...),
		Commands: append([]string(nil), m.commands...),
		Keys:     append([]string(nil), m.keys...),
		Notes:    m.allNotes(),
	}
}

func (m *module) allNotes() []string {
	if len(m.notes) == 0 && len(m.extra) == 0 && m.grantNote == "" {
		return nil
	}
	out := make([]string, 0, len(m.notes)+len(m.extra)+1)
	if m.grantNote != "" {
		out = append(out, m.grantNote)
	}
	out = append(out, m.notes...)
	out = append(out, m.extra...)
	return out
}

func (m *module) label() string {
	if m.name == InitModule {
		return "init.scm"
	}
	return m.name
}

func (r *Runtime) newModule(name string) *module {
	base := scheme.NewEnv(nil)
	base.Restore(r.pristine)
	imports := scheme.NewEnv(base)
	return &module{
		name:          name,
		imports:       imports,
		defs:          scheme.NewEnv(imports),
		imported:      map[string]importOrigin{},
		exportsValues: map[string]scheme.Value{},
	}
}

func (r *Runtime) loadRoot(ctx context.Context, source string) error {
	forms, err := scheme.Read(source)
	if err != nil {
		return err
	}
	if len(forms) > 0 && isForm(forms[0], "define-module") {
		return fmt.Errorf("init.scm cannot start with define-module; it belongs in modules/NAME.scm")
	}
	body, reqs, err := splitTop(forms, false)
	if err != nil {
		return err
	}
	m := r.newModule(InitModule)
	r.root = m
	r.loadStack = append(r.loadStack, InitModule)
	defer func() { r.loadStack = r.loadStack[:len(r.loadStack)-1] }()
	if err := r.applyRequires(ctx, m, reqs); err != nil {
		return err
	}
	if err := r.evalForms(ctx, m.defs, body); err != nil {
		return err
	}
	return r.finish(m)
}

func (r *Runtime) loadModule(ctx context.Context, name string) error {
	if err := checkModuleName(name); err != nil {
		return err
	}
	if m, ok := r.modules[name]; ok {
		if m.loading {
			return fmt.Errorf("module cycle: %s", cyclePath(r.loadStack, name))
		}
		if m.ready {
			return nil
		}
		return fmt.Errorf("%s.scm: it failed to load earlier in this file", name)
	}
	if r.opts.ConfigDir == "" {
		return fmt.Errorf("module %s: there is no modules directory", name)
	}
	if len(r.modules) >= MaxModules {
		return fmt.Errorf("at most %d modules can be loaded", MaxModules)
	}
	source, err := ReadModule(r.opts.ConfigDir, name)
	if err != nil {
		return err
	}
	forms, err := scheme.Read(source)
	if err != nil {
		return moduleErr(name, err)
	}
	if len(forms) == 0 || !isForm(forms[0], "define-module") {
		return moduleErr(name, fmt.Errorf("must start with (define-module %s ...)", name))
	}
	doc, exports, asked, err := parseHeader(forms[0], name)
	if err != nil {
		return moduleErr(name, err)
	}
	body, reqs, err := splitTop(forms[1:], true)
	if err != nil {
		return moduleErr(name, err)
	}
	m := r.newModule(name)
	m.doc = doc
	m.exportList = exports
	m.asked = asked
	r.decideGrant(m)
	m.loading = true
	r.modules[name] = m
	r.moduleOrder = append(r.moduleOrder, name)
	r.loadStack = append(r.loadStack, name)
	defer func() {
		m.loading = false
		r.loadStack = r.loadStack[:len(r.loadStack)-1]
	}()
	if err := r.applyRequires(ctx, m, reqs); err != nil {
		return err
	}
	if err := r.evalForms(ctx, m.defs, body); err != nil {
		return moduleErr(name, err)
	}
	if err := r.finish(m); err != nil {
		return moduleErr(name, err)
	}
	m.ready = true
	return nil
}

func (r *Runtime) evalForms(ctx context.Context, env *scheme.Env, forms []scheme.Value) error {
	for _, f := range forms {
		if _, err := r.interp.Eval(ctx, f, env); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) applyRequires(ctx context.Context, into *module, forms []scheme.Value) error {
	for _, f := range forms {
		name, ops, err := parseRequire(f)
		if err != nil {
			return err
		}
		if err := r.loadModule(ctx, name); err != nil {
			return err
		}
		if err := r.importNames(into, name, ops); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) importNames(into *module, name string, ops []importOp) error {
	src := r.modules[name]
	names := make([]importedName, len(src.exportList))
	for i, exp := range src.exportList {
		names[i] = importedName{local: exp, remote: exp}
	}
	for _, op := range ops {
		var err error
		names, err = applyImportOp(name, names, op)
		if err != nil {
			return err
		}
	}
	for _, n := range names {
		if prev, ok := into.imported[n.local]; ok {
			if prev.module == name && prev.remote == n.remote {
				continue
			}
			return fmt.Errorf("%s is exported by %s and %s; use prefix, rename or only", n.local, prev.module, name)
		}
		if _, builtin := r.pristine[n.local]; builtin {
			return fmt.Errorf("%s is a built-in name; use prefix or rename to import it from %s", n.local, name)
		}
		into.imported[n.local] = importOrigin{module: name, remote: n.remote}
		into.imports.Define(n.local, src.exportsValues[n.remote])
	}
	return nil
}

type importedName struct {
	local, remote string
}

func applyImportOp(module string, names []importedName, op importOp) ([]importedName, error) {
	switch op.kind {
	case "only":
		next := make([]importedName, 0, len(op.names))
		for _, want := range op.names {
			found := false
			for _, n := range names {
				if n.local == want {
					next = append(next, n)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("%s does not export %s", module, want)
			}
		}
		return next, nil
	case "except":
		for _, drop := range op.names {
			found := false
			next := make([]importedName, 0, len(names))
			for _, n := range names {
				if n.local == drop {
					found = true
					continue
				}
				next = append(next, n)
			}
			if !found {
				return nil, fmt.Errorf("%s does not export %s", module, drop)
			}
			names = next
		}
		return names, nil
	case "rename":
		old, neu := op.names[0], op.names[1]
		idx := -1
		for i, n := range names {
			if n.local == neu && n.local != old {
				return nil, fmt.Errorf("rename: %s is already imported from %s", neu, module)
			}
			if n.local == old {
				idx = i
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("%s does not export %s", module, old)
		}
		names[idx].local = neu
		return names, nil
	case "prefix":
		for i := range names {
			names[i].local = op.names[0] + names[i].local
		}
		return names, nil
	default:
		return nil, fmt.Errorf("%q is not a require option", op.kind)
	}
}

// finish checks that the module defined what it exports and that none of its
// own definitions silently covers an import. A definition that covers a
// built-in is allowed inside this module and recorded as a note.
func (r *Runtime) finish(m *module) error {
	defined := m.defs.Snapshot()
	var shadowed []string
	for name := range defined {
		if prev, ok := m.imported[name]; ok {
			return fmt.Errorf("%s is defined in %s and also imported from %s; use prefix, rename or only", name, m.label(), prev.module)
		}
		if _, builtin := r.pristine[name]; builtin {
			shadowed = append(shadowed, name)
		}
	}
	sort.Strings(shadowed)
	where := m.label()
	for _, name := range shadowed {
		m.notes = append(m.notes, fmt.Sprintf("defines %s, which hides the built-in of that name inside %s", name, where))
	}
	vals := make(map[string]scheme.Value, len(m.exportList))
	for _, name := range m.exportList {
		v, ok := defined[name]
		if !ok {
			return fmt.Errorf("%s exports %s, which it does not define", m.name, name)
		}
		vals[name] = v
	}
	m.exportsValues = vals
	return nil
}

func cyclePath(stack []string, name string) string {
	start := 0
	for i, s := range stack {
		if s == name {
			start = i
			break
		}
	}
	parts := append(append([]string{}, stack[start:]...), name)
	return strings.Join(parts, " -> ")
}

func formParts(v scheme.Value) (head string, args []scheme.Value, ok bool) {
	items, ok := v.Slice()
	if !ok || len(items) == 0 || items[0].Kind != scheme.KSymbol {
		return "", nil, false
	}
	return items[0].S, items[1:], true
}

func isForm(v scheme.Value, name string) bool {
	head, _, ok := formParts(v)
	return ok && head == name
}

func splitTop(forms []scheme.Value, moduleBody bool) (body, reqs []scheme.Value, err error) {
	for _, f := range forms {
		head, _, ok := formParts(f)
		if ok && head == "define-module" {
			if moduleBody {
				return nil, nil, fmt.Errorf("define-module must be the first form")
			}
			return nil, nil, fmt.Errorf("define-module belongs in modules/NAME.scm, not in init.scm")
		}
		if ok && head == "require" {
			reqs = append(reqs, f)
			continue
		}
		body = append(body, f)
	}
	return body, reqs, nil
}

func parseHeader(form scheme.Value, want string) (doc string, exports []string, asked grant.Grant, err error) {
	_, args, ok := formParts(form)
	if !ok || len(args) == 0 || args[0].Kind != scheme.KSymbol {
		return "", nil, grant.Grant{}, fmt.Errorf("define-module takes the module name")
	}
	if args[0].S != want {
		return "", nil, grant.Grant{}, fmt.Errorf("defines the module %s, but the file is %s.scm", args[0].S, want)
	}
	var sawDoc, sawExport, sawRequires bool
	for _, clause := range args[1:] {
		parts, ok := clause.Slice()
		if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol {
			return "", nil, grant.Grant{}, fmt.Errorf("a define-module clause is (doc \"...\"), (export name ...) or (requires (net ...))")
		}
		switch parts[0].S {
		case "doc":
			if sawDoc {
				return "", nil, grant.Grant{}, fmt.Errorf("doc is given twice")
			}
			if len(parts) != 2 || parts[1].Kind != scheme.KString {
				return "", nil, grant.Grant{}, fmt.Errorf("doc takes one string")
			}
			doc = Tidy(parts[1].S)
			sawDoc = true
		case "export":
			if sawExport {
				return "", nil, grant.Grant{}, fmt.Errorf("export is given twice")
			}
			for _, n := range parts[1:] {
				if n.Kind != scheme.KSymbol {
					return "", nil, grant.Grant{}, fmt.Errorf("export names are symbols")
				}
				for _, have := range exports {
					if have == n.S {
						return "", nil, grant.Grant{}, fmt.Errorf("%s is exported twice", n.S)
					}
				}
				exports = append(exports, n.S)
			}
			sawExport = true
		case "requires":
			if sawRequires {
				return "", nil, grant.Grant{}, fmt.Errorf("requires is given twice")
			}
			sawRequires = true
			if asked, err = grant.Parse(parts[1:]); err != nil {
				return "", nil, grant.Grant{}, err
			}
		default:
			return "", nil, grant.Grant{}, fmt.Errorf("%q is not a define-module clause; use doc, export or requires", parts[0].S)
		}
	}
	return doc, exports, asked, nil
}

func parseRequire(form scheme.Value) (string, []importOp, error) {
	_, args, ok := formParts(form)
	if !ok || len(args) == 0 {
		return "", nil, fmt.Errorf("require takes a module name")
	}
	name, err := moduleNameValue(args[0])
	if err != nil {
		return "", nil, err
	}
	var ops []importOp
	sawOnly, sawExcept := false, false
	for _, opt := range args[1:] {
		parts, ok := opt.Slice()
		if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol {
			return "", nil, fmt.Errorf("a require option is (only ...), (except ...), (rename old new) or (prefix p)")
		}
		switch parts[0].S {
		case "only", "except":
			if parts[0].S == "only" {
				sawOnly = true
			} else {
				sawExcept = true
			}
			if sawOnly && sawExcept {
				return "", nil, fmt.Errorf("use only or except, not both")
			}
			names, err := symbolNames(parts[1:])
			if err != nil {
				return "", nil, err
			}
			if len(names) == 0 {
				return "", nil, fmt.Errorf("%s needs at least one name", parts[0].S)
			}
			if dup := duplicate(names); dup != "" {
				return "", nil, fmt.Errorf("%s is listed twice", dup)
			}
			ops = append(ops, importOp{kind: parts[0].S, names: names})
		case "rename":
			if len(parts) != 3 {
				return "", nil, fmt.Errorf("rename takes the old name and the new one")
			}
			names, err := symbolNames(parts[1:])
			if err != nil {
				return "", nil, err
			}
			ops = append(ops, importOp{kind: "rename", names: names})
		case "prefix":
			if len(parts) != 2 || parts[1].Kind != scheme.KSymbol || parts[1].S == "" {
				return "", nil, fmt.Errorf("prefix takes one symbol, such as str:")
			}
			ops = append(ops, importOp{kind: "prefix", names: []string{parts[1].S}})
		default:
			return "", nil, fmt.Errorf("%q is not a require option; use only, except, rename or prefix", parts[0].S)
		}
	}
	return name, ops, nil
}

func moduleNameValue(v scheme.Value) (string, error) {
	if v.Kind == scheme.KSymbol {
		return v.S, nil
	}
	head, args, ok := formParts(v)
	if ok && head == "quote" && len(args) == 1 && args[0].Kind == scheme.KSymbol {
		return args[0].S, nil
	}
	return "", fmt.Errorf("require takes a module name, as a symbol or a quoted symbol")
}

func symbolNames(vs []scheme.Value) ([]string, error) {
	out := make([]string, len(vs))
	for i, v := range vs {
		if v.Kind != scheme.KSymbol {
			return nil, fmt.Errorf("expected a symbol, got %s", v.Kind)
		}
		out[i] = v.S
	}
	return out, nil
}

func duplicate(names []string) string {
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			return n
		}
		seen[n] = true
	}
	return ""
}
