package editor

import (
	"strings"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/modsrc"
)

// A Window is a modal view an extension or the editor itself shows: a text to
// read, a text to confirm, a list to choose from, or a line to type. While one
// is open it gets every key and nothing reaches the buffer behind it.

// WindowKind is what a window asks.
type WindowKind uint8

// Kinds.
const (
	// WindowText is read-only. Enter, q and Esc close it.
	WindowText WindowKind = iota
	// WindowConfirm is read-only and waits for y (apply) or n, q or Esc.
	WindowConfirm
	// WindowList is a list narrowed by typing; Enter chooses.
	WindowList
	// WindowPrompt is one line of text; Enter sends it.
	WindowPrompt
)

// Window is shown by the view (see render) and answered by Key.
type Window struct {
	Kind   WindowKind
	Title  string
	Lines  []string // the text, or the items of a list
	Scroll int      // first line shown, for a text
	Query  string   // what is typed: the list filter, or the prompt's text
	// Selected indexes Matches, for a list.
	Selected int

	accept    func(index int, text string)
	cancel    func()
	canAccept func() bool
}

// CanAccept reports whether y (or Enter) can be answered: a confirmation whose
// plan has nothing blocked, or any other kind of window.
func (w *Window) CanAccept() bool {
	return w.canAccept == nil || w.canAccept()
}

// Matches are the indexes of the list items that contain the typed filter,
// ignoring case. With nothing typed it is every item.
func (w *Window) Matches() []int {
	want := strings.ToLower(w.Query)
	var out []int
	for i, l := range w.Lines {
		if want == "" || strings.Contains(strings.ToLower(l), want) {
			out = append(out, i)
		}
	}
	return out
}

// Fit keeps the scroll position inside a text. The view calls it with the
// number of rows the text takes at its width, once wrapped.
func (w *Window) Fit(total, rows int) {
	w.Scroll = max(min(w.Scroll, total-max(rows, 1)), 0)
}

// Footer names the keys that work, for the view's last row.
func (w *Window) Footer() string {
	switch w.Kind {
	case WindowConfirm:
		if w.CanAccept() {
			return "y apply | n/Esc cancel | Up/Down scroll | Page Up/Down"
		}
		return "Cannot apply: Esc close | Up/Down scroll | Page Up/Down"
	case WindowList:
		return "Type to filter | Enter choose | Esc cancel | Up/Down | Page Up/Down"
	case WindowPrompt:
		return "Enter send | Ctrl-U clear | Esc cancel"
	}
	return "Esc close | Up/Down scroll | Page Up/Down"
}

// Key handles one key. finished is true when the window is done, and accepted
// says whether it was answered (a choice, a line, a y) or dismissed.
func (w *Window) Key(k key.Key, rows int) (finished, accepted bool) {
	rows = max(rows, 1)
	if k.Mods == key.Ctrl && k.Code == 'u' && w.Kind == WindowPrompt {
		w.Query = ""
		return false, false
	}
	if ctrlAltSuper(k.Mods) {
		return false, false
	}
	code := k.Code
	switch w.Kind {
	case WindowConfirm:
		switch {
		case code == 'y':
			if w.CanAccept() {
				return true, true
			}
		case code == 'n' || code == 'q' || code == key.Esc:
			return true, false
		}
		w.scroll(code, rows)
	case WindowText:
		if code == key.Enter || code == 'q' || code == key.Esc {
			return true, false
		}
		w.scroll(code, rows)
	case WindowList:
		return w.listKey(code, rows)
	case WindowPrompt:
		switch {
		case code == key.Esc:
			return true, false
		case code == key.Enter:
			return true, true
		case code.IsChar():
			w.Query += string(code.Rune())
		case code == key.Backspace:
			w.Query = popRune(w.Query)
		}
	}
	return false, false
}

func (w *Window) scroll(code key.Code, rows int) {
	switch {
	case code == key.Up || code == 'k':
		w.Scroll = max(w.Scroll-1, 0)
	case code == key.Down || code == 'j':
		w.Scroll++
	case code == key.PageUp:
		w.Scroll = max(w.Scroll-rows, 0)
	case code == key.PageDown:
		w.Scroll += rows
	case code == key.Home:
		w.Scroll = 0
	case code == key.End:
		w.Scroll = 1 << 30
	}
}

func (w *Window) listKey(code key.Code, rows int) (finished, accepted bool) {
	switch {
	case code == key.Esc:
		return true, false
	case code == key.Enter:
		if m := w.Matches(); w.Selected < len(m) {
			return true, true
		}
		return false, false
	case code.IsChar():
		w.Query += string(code.Rune())
		w.Selected = 0
	case code == key.Backspace:
		w.Query = popRune(w.Query)
		w.Selected = 0
	case code == key.Up:
		w.Selected = max(w.Selected-1, 0)
	case code == key.Down:
		w.Selected++
	case code == key.PageUp:
		w.Selected = max(w.Selected-rows, 0)
	case code == key.PageDown:
		w.Selected += rows
	case code == key.Home:
		w.Selected = 0
	case code == key.End:
		w.Selected = 1 << 30
	}
	w.Selected = max(min(w.Selected, len(w.Matches())-1), 0)
	return false, false
}

// chosen is the index into Lines of the selected list item, or -1.
func (w *Window) chosen() int {
	if m := w.Matches(); w.Selected >= 0 && w.Selected < len(m) {
		return m[w.Selected]
	}
	return -1
}

// openWindow shows w, or queues it behind the one on screen.
func (e *Editor) openWindow(w *Window) {
	if e.Window == nil {
		e.Window = w
		return
	}
	e.queuedWindows = append(e.queuedWindows, w)
}

func (e *Editor) windowKey(k key.Key, pageRows int) {
	w := e.Window
	finished, accepted := w.Key(k, pageRows)
	if !finished {
		return
	}
	e.Window = nil
	switch {
	case accepted && w.accept != nil:
		w.accept(w.chosen(), w.Query)
	case !accepted && w.cancel != nil:
		w.cancel()
	}
	e.showNextWindow()
}

// showNextWindow puts the next queued window on screen, and then the next one
// an extension asked for.
func (e *Editor) showNextWindow() {
	if e.Window != nil {
		return
	}
	if len(e.queuedWindows) > 0 {
		e.Window, e.queuedWindows = e.queuedWindows[0], e.queuedWindows[1:]
		return
	}
	e.takeExtensionWindow()
}

// takeExtensionWindow turns the oldest window an extension asked for into a
// Window. Text is made safe to show first, as the review page does.
func (e *Editor) takeExtensionWindow() {
	if e.extensions == nil || e.Window != nil {
		return
	}
	req, ok := e.extensions.TakeWindow()
	if !ok {
		return
	}
	rt := e.extensions
	w := &Window{Title: modsrc.Printable(req.Title), Lines: make([]string, len(req.Lines)), Query: req.Initial}
	for i, l := range req.Lines {
		w.Lines[i] = modsrc.Printable(l)
	}
	switch req.Kind {
	case ext.ListWindow:
		w.Kind = WindowList
		w.accept = func(index int, _ string) { rt.Answer(e.ctx, req, index, "") }
	case ext.PromptWindow:
		w.Kind = WindowPrompt
		w.Query = modsrc.Printable(req.Initial)
		w.accept = func(_ int, text string) { rt.Answer(e.ctx, req, -1, text) }
	default:
		w.Kind = WindowText
	}
	w.cancel = func() { rt.Dismiss(e.ctx, req) }
	e.Window = w
}
