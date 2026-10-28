package ext

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
	// These forms are handled by the module loader, rather than primitives.
	symbols = append(symbols,
		map[string]any{"name": "require", "doc": "Import a module from modules/ beside init.scm; supports only, except, rename and prefix.", "parameters": []string{"module", "[options ...]"}},
		map[string]any{"name": "define-module", "doc": "Declare the module name, documentation and exported names as the first form of a module.", "parameters": []string{"name", "[clauses ...]"}})
	return map[string]any{"name": "coigue", "symbols": symbols}
}
