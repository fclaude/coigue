// Package actions is the discoverable action surface: the Files, Code, Help and
// All menus that F2-F4 open, listing every command with its shortcut. The
// shortcuts remain available directly.
package actions

import (
	"strings"

	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/ucd"
)

// Strip is the always-visible hint row.
const Strip = "F2 Files | F3 Code | F1 Help | F4 Actions"

// Group is a menu category.
type Group uint8

// Groups, in the order Tab cycles through them.
const (
	Files Group = iota
	Code
	Help
	All
)

// Title is the group's heading.
func (g Group) Title() string {
	switch g {
	case Files:
		return "Files"
	case Code:
		return "Code"
	case Help:
		return "Help"
	}
	return "All actions"
}

// Next is the group Tab switches to.
func (g Group) Next() Group {
	switch g {
	case Files:
		return Code
	case Code:
		return Help
	case Help:
		return All
	}
	return Files
}

// Kind identifies what an action does.
type Kind uint8

// Action kinds.
const (
	ProjectSearch Kind = iota
	Symbols
	Definition
	References
	WorkspaceSymbols
	Back
	Forward
	Recent
	FindFile
	Browse
	FindText
	GoLine
	FirstLine
	LastLine
	ShowHelp
)

// Action is a menu choice; ShowHelp carries the topic to open.
type Action struct {
	Kind  Kind
	Topic help.Topic
}

// Entry is one row of a menu.
type Entry struct {
	Group    Group
	Title    string
	Shortcut string
	Action   Action
}

// Entries lists every action, in menu order. It must not be modified.
var Entries = []Entry{
	{Group: Code, Title: "Search text across the project", Shortcut: "F5 / :project-search", Action: Action{Kind: ProjectSearch}},
	{Group: Code, Title: "Symbols in this file", Shortcut: "F6 / :symbols", Action: Action{Kind: Symbols}},
	{Group: Code, Title: "Go to definition", Shortcut: "F7 / :definition", Action: Action{Kind: Definition}},
	{Group: Code, Title: "Find references", Shortcut: "F8 / :references", Action: Action{Kind: References}},
	{Group: Code, Title: "Search workspace symbols", Shortcut: "F9 / :workspace-symbols", Action: Action{Kind: WorkspaceSymbols}},
	{Group: Help, Title: "Code navigation", Shortcut: "F1", Action: Action{Kind: ShowHelp, Topic: help.Code}},
	{Group: Files, Title: "Recent files", Shortcut: ":recent", Action: Action{Kind: Recent}},
	{Group: Code, Title: "Navigate back", Shortcut: "Alt-Left / :back", Action: Action{Kind: Back}},
	{Group: Code, Title: "Navigate forward", Shortcut: "Alt-Right / :forward", Action: Action{Kind: Forward}},
	{Group: Files, Title: "Find a file", Shortcut: "Ctrl-P / :open", Action: Action{Kind: FindFile}},
	{Group: Files, Title: "Browse and create files", Shortcut: "Ctrl-O / :explore", Action: Action{Kind: Browse}},
	{Group: Code, Title: "Find text in this file", Shortcut: "/", Action: Action{Kind: FindText}},
	{Group: Code, Title: "Go to line", Shortcut: ":42", Action: Action{Kind: GoLine}},
	{Group: Code, Title: "First line", Shortcut: "gg", Action: Action{Kind: FirstLine}},
	{Group: Code, Title: "Last line", Shortcut: "G", Action: Action{Kind: LastLine}},
	{Group: Help, Title: "Getting started", Shortcut: "F1 / :help", Action: Action{Kind: ShowHelp, Topic: help.Start}},
	{Group: Help, Title: "Editing and movement", Shortcut: "F1", Action: Action{Kind: ShowHelp, Topic: help.Editing}},
	{Group: Help, Title: "Finding files", Shortcut: "F1", Action: Action{Kind: ShowHelp, Topic: help.Files}},
	{Group: Help, Title: "Browsing and creating files", Shortcut: "F1", Action: Action{Kind: ShowHelp, Topic: help.Explorer}},
	{Group: Help, Title: "Commands and confirmations", Shortcut: "F1", Action: Action{Kind: ShowHelp, Topic: help.Prompts}},
}

// Menu is an open action menu.
type Menu struct {
	Group    Group
	Query    string
	Selected int
}

// NewMenu opens a menu on a group.
func NewMenu(group Group) *Menu { return &Menu{Group: group} }

// Entries returns the entries of the group (every one for All) whose title and
// shortcut contain each word of the query, ignoring case.
func (m *Menu) Entries() []*Entry {
	words := strings.Fields(ucd.Lower(m.Query))
	var out []*Entry
	for i := range Entries {
		entry := &Entries[i]
		if m.Group != All && m.Group != entry.Group {
			continue
		}
		text := ucd.Lower(entry.Title + " " + entry.Shortcut)
		matched := true
		for _, word := range words {
			if !strings.Contains(text, word) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, entry)
		}
	}
	return out
}

// Key handles a key press and returns the chosen action on Enter.
func (m *Menu) Key(code key.Code, rows int) (Action, bool) {
	rows = max(rows, 1)
	switch {
	case code.IsChar():
		m.Query += string(code.Rune())
		m.Selected = 0
	case code == key.Backspace:
		if n := len(m.Query); n > 0 {
			_, size := lastRune(m.Query)
			m.Query = m.Query[:n-size]
		}
		m.Selected = 0
	case code == key.Tab:
		m.Group = m.Group.Next()
		m.Query = ""
		m.Selected = 0
	case code == key.Up:
		m.Selected = max(m.Selected-1, 0)
	case code == key.Down:
		m.Selected++
	case code == key.PageUp:
		m.Selected = max(m.Selected-rows, 0)
	case code == key.PageDown:
		m.Selected += rows
	case code == key.Home:
		m.Selected = 0
	case code == key.End:
		m.Selected = max(len(m.Entries())-1, 0)
	case code == key.Enter:
		entries := m.Entries()
		if m.Selected < len(entries) {
			return entries[m.Selected].Action, true
		}
		return Action{}, false
	}
	m.Selected = min(m.Selected, max(len(m.Entries())-1, 0))
	return Action{}, false
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			for _, r := range s[i:] {
				return r, len(s) - i
			}
		}
	}
	return 0, 0
}
