package editor

import (
	"errors"
	"strings"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/theme"
)

// Extensions are Scheme commands defined in the user's init file; see
// docs/extending.md. Scheme is evaluated, and every host operation runs, on
// the goroutine that calls Key, and only against a private copy of the buffer
// that is applied here, in one edit transaction, when the command succeeded.

// SetInterrupter installs how the user's request to stop a running extension
// is noticed (the terminal loop reads the keyboard for Ctrl-C). Without one
// only the deadline stops a runaway command.
func (e *Editor) SetInterrupter(w ext.Watcher) { e.interrupter = w }

// ExtensionCommands lists the commands the init file defined, in order.
func (e *Editor) ExtensionCommands() []ext.Command {
	if e.extensions == nil {
		return nil
	}
	return e.extensions.Commands()
}

// LoadInit loads the user's init file, defining commands. A missing file is
// normal. Any problem is reported in the status line and never stops the
// editor; commands defined before an error stay.
func (e *Editor) LoadInit(path string) {
	source, found, err := ext.ReadInit(path)
	switch {
	case err != nil:
		e.Message = "init.scm: " + ext.Tidy(fsx.Describe(err))
		return
	case !found:
		return
	}
	if e.extensions == nil {
		e.extensions = ext.New(ext.Options{
			Define:   e.defineExtensionCommand,
			Themes:   &e.Themes,
			SetTheme: func(t theme.Theme) { e.Theme = t },
			Bind:     bindable,
			Deadline: e.extDeadline,
			Watch: func(interrupt func()) func() {
				if e.interrupter == nil {
					return func() {}
				}
				return e.interrupter(interrupt)
			},
		})
	}
	if err := e.extensions.Load(e.ctx, source); err != nil {
		e.Message = "init.scm: " + ext.Tidy(err.Error())
	}
}

// Palette is the colours of the current theme.
func (e *Editor) Palette() theme.Palette { return e.Themes.Palette(e.Theme) }

// defineExtensionCommand adds :name to the command table, so completion and
// execution treat it like a built-in one.
func (e *Editor) defineExtensionCommand(name, _ string) error {
	if _, taken := e.commands.lookup(name); taken {
		return errors.New("\"" + name + "\" is a built-in command")
	}
	e.commands.add(commandSpec{name: name, args: true, run: extensionCommand})
	return nil
}

// takeCommandSelection returns the selection a command line opened from
// Visual mode carries, and ends the selection.
func (e *Editor) takeCommandSelection() *ext.Selection {
	mode, anchor := e.commandVisual, e.selectionAnchor
	e.commandVisual = 0
	if mode == 0 || anchor == nil {
		return nil
	}
	e.selectionAnchor = nil
	e.Message = ""
	return &ext.Selection{Anchor: *anchor, Linewise: mode == ModeVisualLine}
}

func extensionCommand(e *Editor, name string, args []string) bool {
	selection := e.invocationSelection
	e.invocationSelection = nil
	if e.IsWelcome() {
		e.Message = "No file open"
		return false
	}
	e.runExtension(":"+name, selection, func(s *ext.Session) error {
		return e.extensions.Run(e.ctx, name, args, s)
	})
	return false
}

// runExtension runs one Scheme procedure on a private copy of the buffer and
// applies the copy in one edit transaction if the procedure succeeded. label
// names it in the status line.
func (e *Editor) runExtension(label string, selection *ext.Selection, run func(*ext.Session) error) {
	scratch := e.Buffer.Clone()
	session := ext.NewSession(scratch, selection)
	session.Path = e.File.Path
	if err := run(session); err != nil {
		e.Message = failureMessage(label, err)
		return
	}
	if session.Edited() {
		e.Buffer.Edit(func() { e.Buffer.Adopt(scratch) })
		e.Buffer.Clamp(false)
	}
	if message := session.Message(); message != "" {
		e.Message = message
	} else {
		e.Message = "Ran " + label
	}
}

func failureMessage(label string, err error) string {
	text := err.Error()
	var runErr *ext.RunError
	if errors.As(err, &runErr) && runErr.Stopped() {
		text += "; no changes made"
	}
	return ext.Tidy(label + " " + text)
}

// runBinding runs what init.scm bound k to in the current mode, if anything.
// A Visual selection goes to the procedure and ends, as with the ! command.
// Nothing runs while an operator or a character to replace is pending: the
// key then does what it always did there, which is to cancel it.
func (e *Editor) runBinding(k key.Key) bool {
	if e.extensions == nil || e.pending != 0 {
		return false
	}
	var mode string
	switch e.Mode {
	case ModeNormal:
		mode = ext.ModeNormal
	case ModeVisual, ModeVisualLine:
		mode = ext.ModeVisual
	default:
		return false
	}
	binding, ok := e.extensions.Binding(mode, k)
	if !ok {
		return false
	}
	var selection *ext.Selection
	if e.selectionAnchor != nil && mode == ext.ModeVisual {
		selection = &ext.Selection{Anchor: *e.selectionAnchor, Linewise: e.Mode == ModeVisualLine}
	}
	e.selectionAnchor = nil
	e.Mode = ModeNormal
	e.resetPending()
	e.runExtension("binding "+binding.Spec, selection, func(s *ext.Session) error {
		return e.extensions.RunBinding(e.ctx, mode, k, s)
	})
	return true
}

// newHelp opens help at a topic. When init.scm defined commands the guide has
// one more topic listing them, so they are documented like the built-in ones.
func (e *Editor) newHelp(topic help.Topic) *help.Help {
	h := help.New(topic)
	commands := e.ExtensionCommands()
	var bindings []ext.Binding
	if e.extensions != nil {
		bindings = e.extensions.Bindings()
	}
	if len(commands) == 0 && len(bindings) == 0 {
		return h
	}
	if len(commands) > 0 {
		h.Extension = []string{
			"Commands defined in your init.scm (see docs/extending.md).",
			"",
		}
		for _, c := range commands {
			name := ":" + c.Name
			line := name + strings.Repeat(" ", max(21-len(name), 1)) + c.Doc
			h.Extension = append(h.Extension, strings.TrimRight(line, " "))
		}
		h.Extension = append(h.Extension,
			"",
			"In Visual mode, ! opens the command line and keeps the selection for",
			"the command you type. Ctrl-C stops a command that runs too long.")
	}
	if len(bindings) > 0 {
		if len(commands) > 0 {
			h.Extension = append(h.Extension, "")
		}
		h.Extension = append(h.Extension,
			"Keys bound in your init.scm (see docs/extending.md).",
			"")
		for _, b := range bindings {
			name := b.Mode + " " + b.Spec
			line := name + strings.Repeat(" ", max(21-len(name), 1)) + b.Doc
			h.Extension = append(h.Extension, strings.TrimRight(line, " "))
		}
	}
	return h
}
