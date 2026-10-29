package ext

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/brote/scheme"
)

// Windows. show-list, show-text and prompt each ask the editor for a modal
// window and return a channel. The answer, if there is one, arrives on that
// channel and the channel is then closed; a window that is cancelled closes it
// with nothing sent. So a window and a timer are used the same way:
// (on-channel (prompt "Rename to" "x") rename).

const (
	// MaxWindows bounds the windows waiting for the one on screen.
	MaxWindows = 8
	// MaxListItems, MaxTextLines and the length limits bound what a window
	// shows; past them the call is an error and nothing is shown.
	MaxListItems  = 1000
	MaxTextLines  = 10000
	MaxLabelRunes = 500
	MaxLineRunes  = 2000
	MaxTitleRunes = 200
)

// WindowKind is what a window asks of the person.
type WindowKind uint8

// Kinds.
const (
	ListWindow WindowKind = iota
	TextWindow
	PromptWindow
)

// WindowReq is a window a command asked for.
type WindowReq struct {
	Kind  WindowKind
	Title string
	// Lines are the item labels of a list or the lines of a text.
	Lines []string
	// Initial is the starting text of a prompt.
	Initial string

	values []scheme.Value // what a list sends for each item
	ch     scheme.Channel
}

func (r *Runtime) newWindow(c *scheme.Call, s *Session, req *WindowReq) (scheme.Value, error) {
	if len(r.windows)+len(s.windows) >= MaxWindows {
		return scheme.Value{}, fmt.Errorf("at most %d windows can be waiting", MaxWindows)
	}
	ch, err := scheme.NewChannel(r.interp, 1)
	if err != nil {
		return scheme.Value{}, err
	}
	req.ch = ch
	s.windows = append(s.windows, req)
	return ch.Value(), nil
}

func titleArg(c *scheme.Call) (string, error) {
	title, err := c.String(0)
	if err != nil {
		return "", err
	}
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		return "", fmt.Errorf("a title is at most %d characters", MaxTitleRunes)
	}
	return title, nil
}

func (r *Runtime) showList(c *scheme.Call, s *Session) (scheme.Value, error) {
	title, err := titleArg(c)
	if err != nil {
		return scheme.Value{}, err
	}
	items, err := c.List(1)
	if err != nil {
		return scheme.Value{}, err
	}
	if len(items) > MaxListItems {
		return scheme.Value{}, fmt.Errorf("a list shows at most %d items; got %d", MaxListItems, len(items))
	}
	req := &WindowReq{Kind: ListWindow, Title: title}
	for i, it := range items {
		label, value := it, it
		if it.Kind == scheme.KPair {
			label, value = it.P.Car, it.P.Cdr
		}
		if label.Kind != scheme.KString {
			return scheme.Value{}, fmt.Errorf("item %d must be a string or a pair whose first part is a string; got %s", i+1, scheme.Write(it))
		}
		if utf8.RuneCountInString(label.S) > MaxLabelRunes {
			return scheme.Value{}, fmt.Errorf("item %d is longer than %d characters", i+1, MaxLabelRunes)
		}
		req.Lines = append(req.Lines, label.S)
		req.values = append(req.values, value)
	}
	return r.newWindow(c, s, req)
}

func (r *Runtime) showText(c *scheme.Call, s *Session) (scheme.Value, error) {
	title, err := titleArg(c)
	if err != nil {
		return scheme.Value{}, err
	}
	var lines []string
	if arg := c.Args[1]; arg.Kind == scheme.KString {
		lines = strings.Split(arg.S, "\n")
	} else {
		items, err := c.List(1)
		if err != nil {
			return scheme.Value{}, err
		}
		for i, it := range items {
			if it.Kind != scheme.KString {
				return scheme.Value{}, fmt.Errorf("line %d must be a string; got %s", i+1, scheme.Write(it))
			}
			lines = append(lines, it.S)
		}
	}
	if len(lines) > MaxTextLines {
		return scheme.Value{}, fmt.Errorf("a text shows at most %d lines; got %d", MaxTextLines, len(lines))
	}
	for i, l := range lines {
		if utf8.RuneCountInString(l) > MaxLineRunes {
			return scheme.Value{}, fmt.Errorf("line %d is longer than %d characters", i+1, MaxLineRunes)
		}
	}
	return r.newWindow(c, s, &WindowReq{Kind: TextWindow, Title: title, Lines: lines})
}

func (r *Runtime) prompt(c *scheme.Call, s *Session) (scheme.Value, error) {
	title, err := titleArg(c)
	if err != nil {
		return scheme.Value{}, err
	}
	initial := ""
	if len(c.Args) > 1 {
		if initial, err = c.String(1); err != nil {
			return scheme.Value{}, err
		}
	}
	if utf8.RuneCountInString(initial) > MaxLabelRunes {
		return scheme.Value{}, fmt.Errorf("the starting text is at most %d characters", MaxLabelRunes)
	}
	return r.newWindow(c, s, &WindowReq{Kind: PromptWindow, Title: title, Initial: initial})
}

// TakeWindow removes and returns the oldest window waiting to be shown.
func (r *Runtime) TakeWindow() (*WindowReq, bool) {
	if len(r.windows) == 0 {
		return nil, false
	}
	req := r.windows[0]
	r.windows = r.windows[1:]
	return req, true
}

// WindowsWaiting is how many windows wait to be shown.
func (r *Runtime) WindowsWaiting() int { return len(r.windows) }

// Answer sends what the person chose or typed on the window's channel and
// closes it. index is the chosen item of a list; text is what a prompt got.
func (r *Runtime) Answer(req *WindowReq, index int, text string) {
	var v scheme.Value
	switch req.Kind {
	case ListWindow:
		if index < 0 || index >= len(req.values) {
			r.Dismiss(req)
			return
		}
		v = req.values[index]
	case PromptWindow:
		v = scheme.Str(text)
	}
	_, _ = req.ch.TrySend(v)
	req.ch.Close()
}

// Dismiss closes the window's channel with nothing sent.
func (r *Runtime) Dismiss(req *WindowReq) { req.ch.Close() }

// DropWindows forgets the windows that have not been shown.
func (r *Runtime) DropWindows() { r.windows = nil }
