// Package navigation holds the jump history, recent files and the searchable
// result lists shared by project search, symbols and recent-file navigation.
// Successful jumps, rather than key presses, define navigation history.
package navigation

import (
	"strings"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/key"
)

// Location is a position in a file plus the viewport that showed it.
type Location struct {
	Path string
	Row  int
	Col  int
	Top  int
	Left int
}

// NewLocation canonicalizes the path when it exists, otherwise makes it
// absolute (as std::path::absolute does) and falls back to the input.
func NewLocation(path string) Location {
	if resolved, err := fsx.Canonicalize(path); err == nil {
		path = resolved
	} else if abs, err := fsx.Absolute(path); err == nil {
		path = abs
	}
	return Location{Path: path}
}

// Travel says how a jump relates to the history stacks.
type Travel uint8

// Travel kinds.
const (
	New Travel = iota
	Back
	Forward
)

const (
	stackLimit  = 100
	recentLimit = 50
)

// History is the back/forward stacks and the recent-files list.
type History struct {
	back    []Location
	forward []Location
	// Recent lists visited files, most recent first.
	Recent []Location
}

// Destination peeks the location a back/forward jump would reach.
func (h *History) Destination(travel Travel) (Location, bool) {
	var stack []Location
	switch travel {
	case Back:
		stack = h.back
	case Forward:
		stack = h.forward
	}
	if len(stack) == 0 {
		return Location{}, false
	}
	return stack[len(stack)-1], true
}

// Visit moves the location's file to the front of the recent list.
func (h *History) Visit(location Location) {
	kept := h.Recent[:0:0]
	for _, old := range h.Recent {
		if old.Path != location.Path {
			kept = append(kept, old)
		}
	}
	h.Recent = append([]Location{location}, kept...)
	if len(h.Recent) > recentLimit {
		h.Recent = h.Recent[:recentLimit]
	}
}

func push(stack []Location, location Location) []Location {
	if len(stack) == 0 || stack[len(stack)-1] != location {
		stack = append(stack, location)
	}
	if len(stack) > stackLimit {
		stack = stack[1:]
	}
	return stack
}

// Depart records an in-file jump origin.
func (h *History) Depart(origin Location) {
	h.back = push(h.back, origin)
	h.forward = nil
	h.Visit(origin)
}

// Arrived records a completed jump from origin (when there was one) to target.
func (h *History) Arrived(origin *Location, target Location, travel Travel) {
	if origin != nil {
		switch {
		case travel == New && *origin != target:
			h.back = push(h.back, *origin)
			h.forward = nil
		case travel == Back:
			if len(h.back) > 0 {
				h.back = h.back[:len(h.back)-1]
			}
			h.forward = push(h.forward, *origin)
		case travel == Forward:
			if len(h.forward) > 0 {
				h.forward = h.forward[:len(h.forward)-1]
			}
			h.back = push(h.back, *origin)
		}
		h.Visit(*origin)
	}
	h.Visit(target)
}

// Rename rewrites every remembered path below source to live below target.
func (h *History) Rename(source, target string) {
	for _, list := range [][]Location{h.back, h.forward, h.Recent} {
		for i := range list {
			if suffix, ok := fsx.StripPrefix(list[i].Path, source); ok {
				list[i].Path = fsx.Join(target, suffix)
			}
		}
	}
}

// Target is one selectable result.
type Target struct {
	Location Location
	Label    string
}

// Source says which feature produced a result list.
type Source uint8

// Result list sources.
const (
	Recent Source = iota
	Project
	Symbols
	Definition
	References
	Workspace
)

// Live reports whether the list is re-queried as the user types, instead of
// being filtered locally.
func (s Source) Live() bool { return s == Project || s == Workspace }

// List is a filterable result list.
type List struct {
	Source   Source
	Busy     bool
	Notice   string
	Title    string
	Query    string
	Selected int
	Targets  []Target
}

// NewList creates a list of local, filterable targets.
func NewList(title string, targets []Target) *List {
	return &List{Source: Recent, Title: title, Targets: targets}
}

// Matches returns the targets that satisfy the query: every whitespace
// separated word must occur in the lower-cased label, unless the list is
// queried live.
func (l *List) Matches() []*Target {
	query := strings.ToLower(l.Query)
	words := strings.Fields(query)
	var out []*Target
	for i := range l.Targets {
		target := &l.Targets[i]
		if l.Source.Live() {
			out = append(out, target)
			continue
		}
		label := strings.ToLower(target.Label)
		matched := true
		for _, word := range words {
			if !strings.Contains(label, word) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, target)
		}
	}
	return out
}

func saturatingSub(a, b int) int {
	if b > a {
		return 0
	}
	return a - b
}

func saturatingAdd(a, b int) int {
	if sum := a + b; sum >= a {
		return sum
	}
	return int(^uint(0) >> 1)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Key applies a key press and returns the chosen location on Enter.
func (l *List) Key(code key.Code, rows int) (Location, bool) {
	switch {
	case code.IsChar():
		l.Query += string(code.Rune())
		l.Selected = 0
	case code == key.Backspace:
		if n := len(l.Query); n > 0 {
			_, size := lastRune(l.Query)
			l.Query = l.Query[:n-size]
		}
		l.Selected = 0
	case code == key.Up:
		l.Selected = saturatingSub(l.Selected, 1)
	case code == key.Down:
		l.Selected++
	case code == key.PageUp:
		l.Selected = saturatingSub(l.Selected, maxInt(rows, 1))
	case code == key.PageDown:
		l.Selected = saturatingAdd(l.Selected, maxInt(rows, 1))
	case code == key.Home:
		l.Selected = 0
	case code == key.End:
		l.Selected = saturatingSub(len(l.Matches()), 1)
	case code == key.Enter:
		matches := l.Matches()
		if l.Selected < len(matches) {
			return matches[l.Selected].Location, true
		}
		return Location{}, false
	}
	l.Selected = min(l.Selected, saturatingSub(len(l.Matches()), 1))
	return Location{}, false
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			r := []rune(s[i:])
			return r[0], len(s) - i
		}
	}
	return 0, 0
}
