package ext

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/fclaude/brote/scheme"
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
	return []*scheme.Prim{
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
	}
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
	switch {
	case !commandName.MatchString(name):
		return scheme.Value{}, fmt.Errorf("%q is not a command name: use a lowercase letter, then lowercase letters, digits and -, up to 32 characters", name)
	case r.commands[name] != nil:
		return scheme.Value{}, fmt.Errorf("the command %q is already defined", name)
	case len(r.commands) >= MaxCommands:
		return scheme.Value{}, fmt.Errorf("at most %d commands can be defined", MaxCommands)
	}
	doc = Tidy(doc)
	if r.opts.Define != nil {
		if err := r.opts.Define(name, doc); err != nil {
			return scheme.Value{}, err
		}
	}
	r.commands[name] = &Command{Name: name, Doc: doc, proc: proc}
	r.order = append(r.order, name)
	return scheme.Void, nil
}
