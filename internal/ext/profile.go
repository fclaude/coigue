package ext

import (
	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/brote/std"
)

// HostProfile returns data-only language-server metadata from the same primitive
// declarations the runtime registers. Constructing the declarations invokes no
// handlers and loads no user configuration.
func HostProfile() map[string]any {
	r := &Runtime{}
	symbols := []any{}
	for _, p := range r.hostOperations() {
		params := []string{}
		for _, param := range p.Meta.Params {
			name := param.Name
			if param.Optional {
				name = "[" + name + "]"
			}
			params = append(params, name)
		}
		symbols = append(symbols, map[string]any{"name": p.Name, "doc": p.Meta.Doc, "parameters": params, "variadic": p.Meta.Variadic})
	}
	symbols = append(symbols, stdSymbols()...)
	// These forms are handled by the module loader, rather than primitives.
	symbols = append(symbols,
		map[string]any{"name": "require", "doc": "Import a module from modules/ beside init.scm; supports only, except, rename and prefix.", "parameters": []string{"module", "[options ...]"}},
		map[string]any{"name": "define-module", "doc": "Declare the module name, documentation and exported names as the first form of a module.", "parameters": []string{"name", "[clauses ...]"}})
	return map[string]any{"name": "coigue", "symbols": symbols}
}

// stdSymbols describes what Brote's std gives an extension: the procedures of
// the net and encoding modules. The helpers that Brote defines in Scheme
// (http-get and the like) carry no declaration, so they are listed here.
func stdSymbols() []any {
	in := scheme.New()
	pol, err := std.ParsePolicy(installedPolicy)
	if err != nil {
		panic(err)
	}
	rt, err := std.Install(in, pol)
	if err != nil {
		panic(err)
	}
	var out []any
	for _, p := range in.Prims() {
		if rt.ModuleOf(p.Name) == "" {
			continue
		}
		params := []string{}
		for _, param := range p.Meta.Params {
			name := param.Name
			if param.Optional {
				name = "[" + name + "]"
			}
			params = append(params, name)
		}
		out = append(out, map[string]any{"name": p.Name, "doc": p.Meta.Doc, "parameters": params, "variadic": p.Meta.Variadic})
	}
	const note = " Needs (requires (net ...)) in the module's header, and runs in a (spawn-detached ...) task, not in a command."
	for _, h := range []struct {
		name, doc string
		params    []string
	}{
		{"http-get", "(http-request \"GET\" url headers): the response as an association list." + note, []string{"url", "[headers]"}},
		{"http-post", "(http-request \"POST\" url headers body): the response as an association list." + note, []string{"url", "body", "[headers]"}},
		{"http-get-json", "The response body of a GET, parsed as JSON. A status of 400 or more is an error." + note, []string{"url", "[headers]"}},
		{"http-post-json", "Send value as JSON in a POST and return the parsed JSON reply. A status of 400 or more is an error." + note, []string{"url", "value", "[headers]"}},
	} {
		out = append(out, map[string]any{"name": h.name, "doc": h.doc, "parameters": h.params, "variadic": false})
	}
	return out
}
