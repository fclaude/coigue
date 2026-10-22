package editor

import (
	"errors"
	"strings"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/help"
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
	scratch := e.Buffer.Clone()
	session := ext.NewSession(scratch, selection)
	if err := e.extensions.Run(e.ctx, name, args, session); err != nil {
		e.Message = failureMessage(name, err)
		return false
	}
	if session.Edited() {
		e.Buffer.Edit(func() { e.Buffer.Adopt(scratch) })
		e.Buffer.Clamp(false)
	}
	if message := session.Message(); message != "" {
		e.Message = message
	} else {
		e.Message = "Ran :" + name
	}
	return false
}

func failureMessage(name string, err error) string {
	text := err.Error()
	var runErr *ext.RunError
	if errors.As(err, &runErr) && runErr.Stopped() {
		text += "; no changes made"
	}
	return ext.Tidy(":" + name + " " + text)
}

// newHelp opens help at a topic. When init.scm defined commands the guide has
// one more topic listing them, so they are documented like the built-in ones.
func (e *Editor) newHelp(topic help.Topic) *help.Help {
	h := help.New(topic)
	commands := e.ExtensionCommands()
	if len(commands) == 0 {
		return h
	}
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
	return h
}
