package ext

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/theme"
)

// sessionFor returns the live session of a call, or the error that explains
// why there is none. This is what makes an accidental callback impossible: an
// operation reached with no invocation in its context, after the invocation
// ended, or on a goroutine other than the one that started it, changes nothing.
func sessionFor(c *scheme.Call, want kind) (*Session, error) {
	s, _ := c.Ctx.Value(sessionKey{}).(*Session)
	if s == nil || !s.live.Load() || s.kind != want || s.owner != goroutineID() {
		if want == kindInit {
			return nil, errNotInInit
		}
		if want == kindCommand && s != nil && s.kind == kindCommand {
			// A task of this command, or a detached one that outlived it.
			return nil, errInTask
		}
		return nil, errNotInCommand
	}
	return s, nil
}

// operation is the shared shape of the host operations that need a command.
type operation func(c *scheme.Call, s *Session) (scheme.Value, error)

func inCommand(name, doc string, params []scheme.Param, f operation) *scheme.Prim {
	return &scheme.Prim{
		Name: name,
		Meta: scheme.Meta{Doc: doc, Params: params, Origin: "builtin"},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			s, err := sessionFor(c, kindCommand)
			if err != nil {
				return scheme.Value{}, err
			}
			return f(c, s)
		},
	}
}

// hostOperations are every primitive extensions get beyond Brote's language.
// Handler errors carry no prefix: Brote adds "name: ".
func (r *Runtime) hostOperations() []*scheme.Prim {
	prims := []*scheme.Prim{
		{
			Name: "define-command",
			Meta: scheme.Meta{
				Doc: "Define the editor command :NAME as the procedure PROC applied to the words typed after it. Only while init.scm loads.",
				Params: []scheme.Param{
					{Name: "name", Type: "string", Doc: "lowercase letters, digits and -, starting with a letter, up to 32 characters"},
					{Name: "proc", Type: "any", Doc: "procedure taking one string per word"},
					{Name: "doc", Type: "string", Doc: "one-line description", Optional: true},
				},
				Origin: "builtin",
			},
			Handler: r.defineCommand,
		},
		{
			Name: "define-theme",
			Meta: scheme.Meta{
				Doc: "Define the theme NAME, usable with :theme and set-theme. SPEC is a list of (\"key\" . value) pairs: base names the built-in theme to start from (mocha when left out), every other key sets one palette entry to a \"#rrggbb\" colour or \"default\", and bold-keywords takes #t or #f. Only while init.scm loads.",
				Params: []scheme.Param{
					{Name: "name", Type: "string", Doc: "lowercase letters, digits and -, starting with a letter, up to 32 characters"},
					{Name: "spec", Type: "alist", Doc: "the pairs described above"},
				},
				Origin: "builtin",
			},
			Handler: r.defineTheme,
		},
		{
			Name: "set-theme",
			Meta: scheme.Meta{
				Doc:    "Start the editor with the theme NAME, a built-in one or one define-theme made. Only while init.scm loads.",
				Params: []scheme.Param{{Name: "name", Type: "string", Doc: "the theme to start with"}},
				Origin: "builtin",
			},
			Handler: r.setTheme,
		},
		inCommand("selection?", "True if the command was started from a Visual selection and no edit has happened yet.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Bool(s.selection != nil && !s.edited), nil
			}),
		inCommand("selection-text", "The selected text: v selections include each newline they cross, V selections are whole lines joined with newlines.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				start, end, err := s.selectionSpan()
				if err != nil {
					return scheme.Value{}, err
				}
				if end-start > MaxRegion {
					return scheme.Value{}, fmt.Errorf("selection is %d characters; the limit is %d", end-start, MaxRegion)
				}
				return scheme.Str(string(s.Buffer.Span(start, end))), nil
			}),
		inCommand("buffer-text", "The text of lines START to END of the buffer, 1-based and inclusive, END defaulting to START, joined with newlines.",
			[]scheme.Param{
				{Name: "start", Type: "integer", Doc: "first line"},
				{Name: "end", Type: "integer", Doc: "last line", Optional: true},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				first, err := c.Int(0)
				if err != nil {
					return scheme.Value{}, err
				}
				last := first
				if len(c.Args) > 1 {
					if last, err = c.Int(1); err != nil {
						return scheme.Value{}, err
					}
				}
				lines := s.Buffer.Lines()
				for _, n := range []int64{first, last} {
					if n < 1 || n > int64(len(lines)) {
						return scheme.Value{}, fmt.Errorf("line %d is outside 1..%d", n, len(lines))
					}
				}
				if last < first {
					return scheme.Value{}, fmt.Errorf("lines %d..%d: the end is before the start", first, last)
				}
				size := int(last - first)
				for _, line := range lines[first-1 : last] {
					size += len(line)
					if size > MaxRegion {
						return scheme.Value{}, fmt.Errorf("lines %d..%d are longer than %d characters", first, last, MaxRegion)
					}
				}
				var sb strings.Builder
				for i, line := range lines[first-1 : last] {
					if i > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(string(line))
				}
				return scheme.Str(sb.String()), nil
			}),
		inCommand("line-count", "The number of lines in the buffer.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Int(int64(s.Buffer.LineCount())), nil
			}),
		inCommand("cursor-line", "The 1-based line of the cursor, which is the end of the selection when there is one.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Int(int64(s.Buffer.Row + 1)), nil
			}),
		inCommand("replace-selection", "Replace the selection with TEXT. The cursor ends on the first character of TEXT.",
			[]scheme.Param{{Name: "text", Type: "string", Doc: "the replacement"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				text, err := c.String(0)
				if err != nil {
					return scheme.Value{}, err
				}
				start, end, err := s.selectionSpan()
				if err != nil {
					return scheme.Value{}, err
				}
				runes, err := s.checkText(text)
				if err != nil {
					return scheme.Value{}, err
				}
				s.Buffer.ReplaceSpan(start, end, runes)
				s.edited = true
				s.inserted += len(runes)
				return scheme.Void, nil
			}),
		inCommand("insert-text", "Insert TEXT before the cursor character. The cursor ends on the first character of TEXT.",
			[]scheme.Param{{Name: "text", Type: "string", Doc: "the text to insert"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				text, err := c.String(0)
				if err != nil {
					return scheme.Value{}, err
				}
				runes, err := s.checkText(text)
				if err != nil {
					return scheme.Value{}, err
				}
				at := s.Buffer.Offset()
				s.Buffer.ReplaceSpan(at, at, runes)
				s.edited = true
				s.inserted += len(runes)
				return scheme.Void, nil
			}),
		inCommand("message", "Set the status line shown when the command finishes; the last call wins.",
			[]scheme.Param{{Name: "text", Type: "string", Doc: "the message"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				text, err := c.String(0)
				if err != nil {
					return scheme.Value{}, err
				}
				s.message = Tidy(text)
				return scheme.Void, nil
			}),
		inCommand("on-channel", "Run the procedure HANDLER with each value that arrives on CHANNEL, as a command of its own with a fresh copy of the buffer, until the channel is closed. CHANNEL should be buffered (make-channel n), because a send on an unbuffered one waits for a receiver and the editor never waits. A handler runs when the editor is in Normal mode with nothing pending; until then values stay in the channel. :reload drops it.",
			[]scheme.Param{
				{Name: "channel", Type: "any", Doc: "a channel made by make-channel"},
				{Name: "handler", Type: "any", Doc: "procedure taking the received value"},
			},
			r.onChannel),
		inCommand("on-done", "Run the procedure HANDLER with TASK when it finishes, as a command of its own with a fresh copy of the buffer. TASK is one made by spawn-detached, which keeps running after this command ends. Inside HANDLER, (task-wait task) gives the task's value or raises its error, so guard works as usual. A task that is cancelled (task-cancel!, :reload, quitting) does not run its handler. A handler runs when the editor is in Normal mode with nothing pending; until then it waits. :reload drops it and ends the task.",
			[]scheme.Param{
				{Name: "task", Type: "any", Doc: "a task made by spawn-detached"},
				{Name: "handler", Type: "any", Doc: "procedure taking the finished task"},
			},
			r.onDone),
		inCommand("timer", "A channel that receives #t once, SECONDS from now (0.01 to 604800), kept by the editor. Brote's own (after secs) ends with the command that made it, so use this one to wait across commands: (on-channel (timer 60) handler). :reload drops it.",
			[]scheme.Param{{Name: "seconds", Type: "number", Doc: "the delay"}},
			r.timer),
		inCommand("show-list", "Show a window listing ITEMS to choose from, and return a channel. Typing narrows the list, Enter chooses, Esc cancels. An item is a string, or a pair (label . value) to show the label and send the value. The channel receives the choice and is then closed; a cancelled window closes it with nothing sent. Use it with on-channel.",
			[]scheme.Param{
				{Name: "title", Type: "string", Doc: "the heading"},
				{Name: "items", Type: "list", Doc: "strings or (label . value) pairs, at most 1000"},
			},
			r.showList),
		inCommand("show-text", "Show a read-only window with LINES, a string or a list of strings, and return a channel that is closed when it is dismissed with Enter, q or Esc. Long lines wrap.",
			[]scheme.Param{
				{Name: "title", Type: "string", Doc: "the heading"},
				{Name: "lines", Type: "any", Doc: "a string with newlines, or a list of strings, at most 10000 lines"},
			},
			r.showText),
		inCommand("prompt", "Ask for a line of text and return a channel. Enter sends the text, Ctrl-U clears it, Esc cancels. The channel is closed after the answer; a cancelled prompt closes it with nothing sent.",
			[]scheme.Param{
				{Name: "title", Type: "string", Doc: "what is being asked"},
				{Name: "initial", Type: "string", Doc: "the starting text", Optional: true},
			},
			r.prompt),
		{
			Name: "bind-key",
			Meta: scheme.Meta{
				Doc: "Bind a key in MODE to the procedure PROC, which runs as a command does, with no arguments. KEY is one character such as \"x\", Ctrl and a lowercase letter such as \"<C-x>\", or Alt and a character such as \"<A-x>\". A key the editor already uses needs OVERRIDE to be #t. Only while init.scm loads.",
				Params: []scheme.Param{
					{Name: "mode", Type: "string", Doc: "\"normal\" or \"visual\" (which covers V as well)"},
					{Name: "key", Type: "string", Doc: "the key, in the syntax above"},
					{Name: "proc", Type: "any", Doc: "procedure taking no arguments"},
					{Name: "doc", Type: "string", Doc: "one-line description for the guide", Optional: true},
					{Name: "override", Type: "boolean", Doc: "#t to shadow a built-in meaning of the key", Optional: true},
				},
				Origin: "builtin",
			},
			Handler: r.bindKey,
		},
		{
			Name: "on-start",
			Meta: scheme.Meta{
				Doc:    "Run the procedure PROC once, with no arguments and as a command does, after the editor has started and a file is open. Only while init.scm loads, and once.",
				Params: []scheme.Param{{Name: "proc", Type: "any", Doc: "procedure taking no arguments"}},
				Origin: "builtin",
			},
			Handler: r.onStart,
		},
	}
	prims = append(prims, readOperations()...)
	prims = append(prims, markOps(r)...)
	return append(prims, editOperations()...)
}

// Tidy makes text safe for one row of the status line: control characters
// become spaces and the length is bounded by MaxMessage.
func Tidy(text string) string {
	runes := []rune(strings.ToValidUTF8(text, "�"))
	if len(runes) > MaxMessage {
		runes = runes[:MaxMessage]
	}
	for i, r := range runes {
		if unicode.IsControl(r) {
			runes[i] = ' '
		}
	}
	return string(runes)
}

func (r *Runtime) defineCommand(c *scheme.Call) (scheme.Value, error) {
	if _, err := sessionFor(c, kindInit); err != nil {
		return scheme.Value{}, err
	}
	name, err := c.String(0)
	if err != nil {
		return scheme.Value{}, err
	}
	proc, err := c.Proc(1)
	if err != nil {
		return scheme.Value{}, err
	}
	doc := ""
	if len(c.Args) > 2 {
		if doc, err = c.String(2); err != nil {
			return scheme.Value{}, err
		}
	}
	owner := r.owner()
	switch {
	case !commandName.MatchString(name):
		return scheme.Value{}, fmt.Errorf("%q is not a command name: use a lowercase letter, then lowercase letters, digits and -, up to 32 characters", name)
	case r.commandTaken(owner, name):
		if owner == InitModule {
			return scheme.Value{}, fmt.Errorf("the command %q is already defined", name)
		}
		return scheme.Value{}, fmt.Errorf("the command %q is already defined in %s", name, owner)
	case len(r.pendingCommands) >= MaxCommands:
		if owner == InitModule {
			return scheme.Value{}, fmt.Errorf("at most %d commands can be defined", MaxCommands)
		}
		return scheme.Value{}, fmt.Errorf("at most %d commands can be defined; %s reached the limit", MaxCommands, owner)
	}
	doc = Tidy(doc)
	if r.opts.Define != nil {
		if err := r.opts.Define(name, doc); err != nil {
			if owner != InitModule {
				return scheme.Value{}, fmt.Errorf("%s: %w", owner, err)
			}
			return scheme.Value{}, err
		}
	}
	r.pendingCommands = append(r.pendingCommands, pendingCommand{owner: owner, name: name, doc: doc, proc: proc})
	if owner != InitModule {
		if m := r.modules[owner]; m != nil {
			m.commands = append(m.commands, owner+"/"+name)
		}
	}
	return scheme.Void, nil
}

func (r *Runtime) defineTheme(c *scheme.Call) (scheme.Value, error) {
	if _, err := sessionFor(c, kindInit); err != nil {
		return scheme.Value{}, err
	}
	name, err := c.String(0)
	if err != nil {
		return scheme.Value{}, err
	}
	owner := r.owner()
	if !commandName.MatchString(name) {
		return scheme.Value{}, fmt.Errorf("%q is not a theme name: use a lowercase letter, then lowercase letters, digits and -, up to 32 characters", name)
	}
	if owner != InitModule {
		name = owner + "/" + name
	}
	items, err := c.List(1)
	if err != nil {
		return scheme.Value{}, err
	}
	base, baseGiven := "", false
	var entries []theme.Entry
	for _, item := range items {
		if item.Kind != scheme.KPair || item.P.Car.Kind != scheme.KString {
			return scheme.Value{}, fmt.Errorf("spec is a list of (\"key\" . value) pairs")
		}
		key, value := item.P.Car.S, item.P.Cdr
		if key == "base" {
			if baseGiven {
				return scheme.Value{}, fmt.Errorf("base is given twice")
			}
			if value.Kind != scheme.KString {
				return scheme.Value{}, fmt.Errorf("base takes a theme name as a string")
			}
			base, baseGiven = value.S, true
			continue
		}
		switch value.Kind {
		case scheme.KString:
			entries = append(entries, theme.Entry{Key: key, Value: value.S})
		case scheme.KBool:
			entries = append(entries, theme.Entry{Key: key, Value: value.I != 0})
		default:
			return scheme.Value{}, fmt.Errorf("%s takes a string, or #t or #f for bold-keywords", key)
		}
	}
	if _, err := r.themes.Define(name, base, entries); err != nil {
		if owner != InitModule {
			return scheme.Value{}, fmt.Errorf("%s: %w", owner, err)
		}
		return scheme.Value{}, err
	}
	return scheme.Void, nil
}

func (r *Runtime) setTheme(c *scheme.Call) (scheme.Value, error) {
	if _, err := sessionFor(c, kindInit); err != nil {
		return scheme.Value{}, err
	}
	if r.owner() != InitModule {
		return scheme.Value{}, fmt.Errorf("only init.scm may choose the starting theme")
	}
	name, err := c.String(0)
	if err != nil {
		return scheme.Value{}, err
	}
	t, ok := r.themes.Parse(name)
	if !ok {
		return scheme.Value{}, fmt.Errorf("there is no theme %q; use %s", name, r.themes.Choices())
	}
	if r.opts.SetTheme != nil {
		r.opts.SetTheme(t)
	}
	return scheme.Void, nil
}
