package editor

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/modsrc"
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
	e.initPath = path
	source, found, err := ext.ReadInit(path)
	switch {
	case err != nil:
		e.Message = "init.scm: " + ext.Tidy(fsx.Describe(err))
		return
	case !found:
		return
	}
	e.listConfigCommands()
	if e.extensions == nil {
		e.extensions = ext.New(e.extensionOptions(&e.Themes, func(t theme.Theme) { e.Theme = t }, e.checkExtensionCommand))
	}
	if err := e.extensions.Load(e.ctx, source); err != nil {
		e.Message = "init.scm: " + ext.Tidy(err.Error())
	}
	// Commands defined before an error stay. A key two modules bound, and
	// that init.scm did not, is left unbound; the status line says so.
	e.installExtensionCommands(e.extensions)
}

// extensionOptions are the options of a runtime that loads init.scm into
// themes, with the given way to define commands and to choose the theme.
func (e *Editor) extensionOptions(themes *theme.Set, setTheme func(theme.Theme), define func(name, doc string) error) ext.Options {
	return ext.Options{
		Define:    define,
		Themes:    themes,
		SetTheme:  setTheme,
		Bind:      bindable,
		ConfigDir: filepath.Dir(e.initPath),
		Grants:    modsrc.Grants(filepath.Dir(e.initPath)),
		Deadline:  e.extDeadline,
		Watch: func(interrupt func()) func() {
			if e.interrupter == nil {
				return func() {}
			}
			return e.interrupter(interrupt)
		},
	}
}

// listConfigCommands puts the commands that work on the configuration into
// Tab completion, once there is an init file.
func (e *Editor) listConfigCommands() {
	for _, name := range []string{"reload", "module-add", "module-list", "module-remove", "module-sync", "module-update"} {
		e.commands.list(name)
	}
}

func reloadCommand(e *Editor, _ string, _ []string) bool {
	e.reloadInit()
	return false
}

// reloadInit reads init.scm again into a fresh runtime. Only when the file
// loads without error does the editor switch to it, replacing commands,
// themes, key bindings and the start-up procedure as a unit; otherwise
// nothing the editor holds has been touched and the status line says why.
func (e *Editor) reloadInit() {
	if e.initPath == "" {
		e.Message = "init.scm: no place for it on this machine; set HOME or XDG_CONFIG_HOME"
		return
	}
	source, found, err := ext.ReadInit(e.initPath)
	switch {
	case err != nil:
		e.Message = "init.scm: " + ext.Tidy(fsx.Describe(err)) + "; nothing changed"
		return
	case !found:
		e.Message = "init.scm: not found at " + ext.Tidy(e.initPath) + "; nothing changed"
		return
	}
	var themes theme.Set
	var chosen *theme.Theme
	stage := ext.New(e.extensionOptions(&themes, func(t theme.Theme) { chosen = &t }, e.checkExtensionCommand))
	if err := stage.Load(e.ctx, source); err != nil {
		endRuntime(stage, false)
		e.Message = "init.scm: " + ext.Tidy(err.Error()) + "; nothing changed"
		return
	}
	e.commitReload(stage, themes, chosen)
}

func (e *Editor) commitReload(stage *ext.Runtime, themes theme.Set, chosen *theme.Theme) {
	e.queuedWindows = nil
	e.installExtensionCommands(stage)
	e.listConfigCommands()
	previous := e.Themes.Name(e.Theme)
	endRuntime(e.extensions, false)
	e.extensions = stage
	e.Themes = themes
	note := ""
	if chosen != nil {
		e.Theme = *chosen
	} else if t, ok := e.Themes.Parse(previous); ok {
		e.Theme = t
	} else {
		e.Theme = theme.CatppuccinMocha
		note = "; theme " + previous + " is gone, using " + e.Themes.Name(e.Theme)
	}
	e.Message = ext.Tidy("Reloaded init.scm: " + reloadSummary(stage) + note)
}

// installExtensionCommands replaces the extension commands with the ones the
// runtime resolved: the short names, then every module/name form.
func (e *Editor) installExtensionCommands(rt *ext.Runtime) {
	e.commands.removeExtensions()
	add := func(c ext.Command) {
		e.commands.add(commandSpec{name: c.Name, args: true, run: extensionCommand, extension: true})
	}
	for _, c := range rt.Commands() {
		add(c)
	}
	for _, c := range rt.QualifiedCommands() {
		add(c)
	}
	e.commandCompletion = nil
}

func reloadSummary(rt *ext.Runtime) string {
	parts := make([]string, 0, 3)
	if n := len(rt.Modules()); n > 0 {
		parts = append(parts, plural(n, "module"))
	}
	parts = append(parts, plural(len(rt.QualifiedCommands()), "command"), plural(len(rt.Bindings()), "key binding"))
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Palette is the colours of the current theme.
func (e *Editor) Palette() theme.Palette { return e.Themes.Palette(e.Theme) }

// checkExtensionCommand refuses the names of built-in commands. A command
// init.scm defined earlier is not in the way: it is the one being replaced.
func (e *Editor) checkExtensionCommand(name, _ string) error {
	if spec, taken := e.commands.lookup(name); taken && !spec.extension {
		return errors.New("\"" + name + "\" is a built-in command")
	}
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
	e.syncMarks()
	scratch := e.Buffer.Clone()
	session := ext.NewSession(scratch, selection)
	session.Path = e.File.Path
	if err := run(session); err != nil {
		e.Message = failureMessage(label, err)
		return
	}
	if e.extensions != nil {
		e.extensions.Adopt(session)
		defer e.showNextWindow()
	}
	if session.Edited() {
		e.Buffer.Edit(func() { e.Buffer.Adopt(scratch) })
		e.Buffer.Clamp(false)
	}
	e.syncMarks()
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

// RunStart runs the procedure init.scm gave to on-start, once, as soon as a
// file is open: right away when the editor started on one, otherwise when
// the first is opened. A failure is reported in the status line and nothing
// else; a procedure that has nothing to say leaves the status line alone.
func (e *Editor) RunStart() {
	if e.startDone || e.extensions == nil || !e.extensions.HasStart() || e.IsWelcome() {
		return
	}
	e.startDone = true
	for _, owner := range e.extensions.StartOwners() {
		label := "on-start"
		if owner != ext.InitModule {
			label = "on-start " + owner
		}
		before := e.Message
		e.runExtension(label, nil, func(s *ext.Session) error {
			return e.extensions.RunStartOwner(e.ctx, owner, s)
		})
		if e.Message == "Ran "+label {
			e.Message = before
		}
	}
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
	if len(commands) == 0 && len(bindings) == 0 && len(moduleGuide(nil, e.extensions)) == 0 {
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
	h.Extension = moduleGuide(h.Extension, e.extensions)
	return h
}

// moduleGuide appends the modules a load read: what each exports, the
// commands and keys it registered, and the notes about names it hides or
// lost to init.scm. With no modules and nothing to note, the guide is unchanged.
func moduleGuide(lines []string, rt *ext.Runtime) []string {
	if rt == nil {
		return lines
	}
	mods := rt.Modules()
	notes := rt.InitNotes()
	if len(mods) == 0 && len(notes) == 0 {
		return lines
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, "Modules (see docs/extending.md).", "")
	if len(notes) > 0 {
		lines = append(lines, "init.scm")
		for _, n := range notes {
			lines = append(lines, "  "+n)
		}
		if len(mods) > 0 {
			lines = append(lines, "")
		}
	}
	for i, m := range mods {
		if i > 0 {
			lines = append(lines, "")
		}
		head := m.Name
		if m.Doc != "" {
			head += "  " + m.Doc
		}
		lines = append(lines, head)
		if len(m.Exports) > 0 {
			lines = append(lines, "  exports "+strings.Join(m.Exports, " "))
		}
		for _, c := range m.Commands {
			lines = append(lines, "  :"+c)
		}
		for _, k := range m.Keys {
			lines = append(lines, "  "+k)
		}
		for _, n := range m.Notes {
			lines = append(lines, "  "+n)
		}
	}
	return lines
}

// maxEventsPerTick bounds how many handlers run between two frames, so a
// channel that is filled faster than it is read cannot hold the screen.
const maxEventsPerTick = 8

// pollEvents fires the timers that are due and runs the handlers of the
// channels that have values. A handler is a command, so it must not land in
// the middle of something the person is doing: it runs only in Normal mode
// with nothing pending and no window open, and the values wait in their
// channels until then.
func (e *Editor) pollEvents() {
	if e.extensions == nil {
		return
	}
	e.reportTaskFailures()
	handlers, timers := e.extensions.Waiting()
	if handlers == 0 && timers == 0 {
		return
	}
	e.extensions.FireTimers(time.Now())
	if !e.idleForEvents() {
		return
	}
	for _, ev := range e.extensions.PollEvents(maxEventsPerTick) {
		label := "on-channel"
		if origin := ev.Origin(); origin != "" {
			label = "on-channel from :" + origin
		}
		before := e.Message
		e.runExtension(label, nil, func(s *ext.Session) error {
			return e.extensions.RunEvent(e.ctx, ev, s)
		})
		// A handler with nothing to say leaves the status line as it was.
		if e.Message == "Ran "+label {
			e.Message = before
		}
	}
}

func (e *Editor) idleForEvents() bool {
	return e.Mode == ModeNormal && e.pending == 0 && e.count == 0 && e.operatorCount == 0 &&
		e.selectionAnchor == nil && e.commandVisual == 0 && !e.IsWelcome() &&
		e.Help == nil && e.Menu == nil && e.Locations == nil && e.Picker == nil &&
		e.Explorer == nil && e.SwitchPrompt == "" && e.FilesystemPrompt == nil && e.Window == nil
}

// syncMarks moves the marks extensions hold to the text as it is now. It costs
// nothing while there are none or the text has not changed.
func (e *Editor) syncMarks() {
	if e.extensions != nil && e.extensions.HasMarks() {
		e.extensions.SyncMarks(e.Buffer.Lines(), e.Buffer.Revision(), e.Generation)
	}
}

// reportTaskFailures puts the failure of a background task nobody was waiting
// for in the status line. A task an on-done handler watches reports through
// its handler instead.
func (e *Editor) reportTaskFailures() {
	failures := e.extensions.TakeFailures()
	if len(failures) == 0 {
		return
	}
	text := "background task failed: " + ext.Tidy(failures[len(failures)-1].Error())
	if n := len(failures); n > 1 {
		text += " (and " + strconv.Itoa(n-1) + " more)"
	}
	e.Message = text
}

// shutdownTimeout bounds how long ending a runtime's background tasks may take.
const shutdownTimeout = 2 * time.Second

// endRuntime ends the background tasks and timers of a runtime the editor no
// longer uses, so nothing started by old code outlives it. A reload does not
// wait for them: they are cancelled at once and finish on their own.
func endRuntime(rt *ext.Runtime, wait bool) {
	if rt == nil {
		return
	}
	end := func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = rt.Shutdown(ctx)
	}
	if wait {
		end()
		return
	}
	go end()
}
