package editor

import (
	"strconv"
	"strings"

	"github.com/fclaude/coigue/internal/actions"
	"github.com/fclaude/coigue/internal/clipboard"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/ucd"
)

// commandSpec is one entry of the ":" command table. run receives the name it
// was invoked by (and the arguments, for commands that take them) and reports
// whether the editor should quit. A command that takes arguments is found by
// its first word; any other must match the whole trimmed command line.
type commandSpec struct {
	name string
	args bool
	run  func(e *Editor, name string, args []string) (quit bool)
	// extension marks a command init.scm defined, which :reload replaces.
	extension bool
	// unlisted keeps the command out of Tab completion. :reload is until an
	// init file has been found, so an editor without one completes exactly
	// the commands it always did; the command itself works from the start,
	// for the person who is writing their first init.scm.
	unlisted bool
}

// commandTable is the one place commands are looked up by name. The order
// is the order Tab completion offers them.
type commandTable struct {
	specs  []commandSpec
	byName map[string]*commandSpec
}

func (t *commandTable) add(spec commandSpec) {
	t.specs = append(t.specs, spec)
	t.byName = nil
}

// remove drops the command called name, if there is one.
func (t *commandTable) remove(name string) {
	for i := range t.specs {
		if t.specs[i].name == name {
			t.specs = append(t.specs[:i], t.specs[i+1:]...)
			t.byName = nil
			return
		}
	}
}

// list makes the command called name show up in Tab completion.
func (t *commandTable) list(name string) {
	for i := range t.specs {
		if t.specs[i].name == name {
			t.specs[i].unlisted = false
		}
	}
}

func (t *commandTable) lookup(name string) (*commandSpec, bool) {
	if t.byName == nil {
		t.byName = make(map[string]*commandSpec, len(t.specs))
		for i := range t.specs {
			t.byName[t.specs[i].name] = &t.specs[i]
		}
	}
	spec, ok := t.byName[name]
	return spec, ok
}

func menuCommand(group actions.Group) func(*Editor, string, []string) bool {
	return func(e *Editor, _ string, _ []string) bool {
		e.Menu = actions.NewMenu(group)
		return false
	}
}

func activateCommand(kind actions.Kind) func(*Editor, string, []string) bool {
	return func(e *Editor, _ string, _ []string) bool {
		e.activate(actions.Action{Kind: kind})
		return false
	}
}

func defaultCommands() *commandTable {
	t := &commandTable{}
	for _, spec := range []commandSpec{
		{name: "actions", run: menuCommand(actions.All)},
		{name: "back", run: func(e *Editor, _ string, _ []string) bool { e.travel(navigation.Back); return false }},
		{name: "clipboard-copy", run: clipboardCopy},
		{name: "clipboard-paste", run: clipboardPaste},
		{name: "clipboard-paste-before", run: clipboardPaste},
		{name: "code", run: menuCommand(actions.Code)},
		{name: "definition", run: activateCommand(actions.Definition)},
		{name: "explore", run: func(e *Editor, _ string, _ []string) bool { e.openExplorer(); return false }},
		{name: "files", run: menuCommand(actions.Files)},
		{name: "forward", run: func(e *Editor, _ string, _ []string) bool { e.travel(navigation.Forward); return false }},
		{name: "help", run: func(e *Editor, _ string, _ []string) bool { e.openHelp(); return false }},
		{name: "justify", args: true, run: widthCommand},
		{name: "lsp-restart", run: func(e *Editor, _ string, _ []string) bool {
			e.LSP.Restart()
			e.Message = ""
			return false
		}},
		{name: "open", run: func(e *Editor, _ string, _ []string) bool { e.openPicker(); return false }},
		{name: "project-search", run: activateCommand(actions.ProjectSearch)},
		{name: "q", run: quitCommand},
		{name: "q!", run: quitCommand},
		{name: "recent", run: activateCommand(actions.Recent)},
		{name: "reload", run: reloadCommand, unlisted: true},
		{name: "references", run: activateCommand(actions.References)},
		{name: "symbols", run: activateCommand(actions.Symbols)},
		{name: "textwidth", args: true, run: widthCommand},
		{name: "theme", args: true, run: themeCommand},
		{name: "w", run: writeCommand},
		{name: "wq", run: writeCommand},
		{name: "workspace-symbols", run: activateCommand(actions.WorkspaceSymbols)},
	} {
		t.add(spec)
	}
	return t
}

func clipboardCopy(e *Editor, _ string, _ []string) bool {
	if e.IsWelcome() {
		e.Message = "No file open"
	} else {
		e.copyClipboard()
	}
	return false
}

func clipboardPaste(e *Editor, name string, _ []string) bool {
	if e.IsWelcome() {
		e.Message = "No file open"
	} else {
		text, err := clipboard.Paste(e.ctx)
		e.pasteClipboardResult(text, err, name == "clipboard-paste")
	}
	return false
}

func quitCommand(e *Editor, name string, _ []string) bool {
	if name == "q" && e.File.Dirty(e.Buffer.Text()) {
		e.Message = "Unsaved changes; use :w or :q!"
		return false
	}
	return true
}

func writeCommand(e *Editor, name string, _ []string) bool {
	if err := e.saveCurrent(); err != nil {
		e.Message = fsx.Describe(err)
		return false
	}
	e.Message = "Written"
	return name == "wq"
}

// parseUsize is Rust's usize::from_str: an optional '+' and decimal digits
// that fit.
func parseUsize(s string) (uint64, bool) {
	s = strings.TrimPrefix(s, "+")
	if s == "" || s[0] == '+' || s[0] == '-' {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func widthCommand(e *Editor, name string, args []string) bool {
	var width int
	ok := false
	switch len(args) {
	case 0:
		width, ok = e.textwidth, true
	case 1:
		if n, valid := parseUsize(args[0]); valid && n >= 1 && n <= 10000 {
			width, ok = int(n), true
		}
	}
	if !ok {
		e.Message = "Usage: :justify [1..10000] or :textwidth [1..10000]"
		return false
	}
	if name == "justify" {
		e.justify(width)
	} else {
		e.textwidth = width
		e.Message = "Text width: " + strconv.Itoa(width)
	}
	return false
}

func themeCommand(e *Editor, _ string, args []string) bool {
	switch len(args) {
	case 0:
		e.Message = "Theme: " + e.Themes.Name(e.Theme) + " | :theme <" + e.Themes.Choices() + ">"
	case 1:
		if t, ok := e.Themes.Parse(args[0]); ok {
			e.Theme = t
			e.Message = "Theme: " + e.Themes.Name(t)
		} else {
			e.Message = "Unknown theme: " + args[0] + "; use :theme"
		}
	default:
		e.Message = "Usage: :theme <" + e.Themes.Choices() + ">"
	}
	return false
}

func fields(s string) []string { return strings.FieldsFunc(s, ucd.IsWhitespace) }

// execute runs a command line and reports whether the editor should quit.
func (e *Editor) execute(line string) bool {
	cmd := strings.TrimFunc(line, ucd.IsWhitespace)
	words := fields(cmd)
	if len(words) > 0 {
		if spec, ok := e.commands.lookup(words[0]); ok && spec.args {
			return spec.run(e, words[0], words[1:])
		}
	}
	if spec, ok := e.commands.lookup(cmd); ok && !spec.args {
		return spec.run(e, cmd, nil)
	}
	if cmd != "" && strings.Trim(cmd, "0123456789") == "" {
		e.gotoLine(cmd)
		return false
	}
	e.Message = "Unknown command: " + cmd
	return false
}

func (e *Editor) gotoLine(cmd string) {
	if e.IsWelcome() {
		e.Message = "No file open"
		return
	}
	line, err := strconv.ParseUint(cmd, 10, 64)
	if err != nil || line == 0 {
		e.Message = "Invalid line number; use a positive whole number"
		return
	}
	row := e.Buffer.LineCount() - 1
	if line-1 < uint64(row) {
		row = int(line - 1)
	}
	e.rememberJump(row, 0)
	e.Buffer.Row = row
	e.Buffer.Col = 0
	e.Message = ""
}

// commandCompletion cycles through the commands, or theme names after
// ":theme", that start with what was typed.
type commandCompletion struct {
	candidates []string
	selected   int
}

func (t *commandTable) newCompletion(input string, backwards bool, themes []string) *commandCompletion {
	trimmed := strings.TrimLeftFunc(input, ucd.IsWhitespace)
	var prefix string
	var choices []string
	if end := strings.IndexFunc(trimmed, ucd.IsWhitespace); end >= 0 {
		if trimmed[:end] != "theme" {
			return nil
		}
		argument := strings.TrimLeftFunc(trimmed[end:], ucd.IsWhitespace)
		if strings.IndexFunc(argument, ucd.IsWhitespace) >= 0 {
			return nil
		}
		prefix, choices = argument, themes
	} else {
		prefix = trimmed
		for _, spec := range t.specs {
			if !spec.unlisted {
				choices = append(choices, spec.name)
			}
		}
	}
	before := input[:len(input)-len(prefix)]
	var candidates []string
	for _, choice := range choices {
		if strings.HasPrefix(choice, prefix) {
			candidates = append(candidates, before+choice)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	selected := 0
	if backwards {
		selected = len(candidates) - 1
	}
	return &commandCompletion{candidates: candidates, selected: selected}
}

func (c *commandCompletion) cycle(backwards bool) {
	n := len(c.candidates)
	if backwards {
		c.selected = (c.selected + n - 1) % n
	} else {
		c.selected = (c.selected + 1) % n
	}
}
