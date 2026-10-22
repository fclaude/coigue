package editor

import (
	"os"
	"strconv"
	"time"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/lsp"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/search"
)

// queryDebounce is how long a live result list waits after the last keystroke
// before searching again.
const queryDebounce = 75 * time.Millisecond

func (e *Editor) closeLocations() {
	e.pendingQuery = nil
	e.Locations = nil
	e.dropSearch()
	e.LSP.CancelNavigation()
}

func (e *Editor) openLocations(source navigation.Source) {
	if source != navigation.Project && e.IsWelcome() {
		e.Message = "Open a source file before asking its language server for navigation"
		return
	}
	var title string
	switch source {
	case navigation.Project:
		title = "Project search"
	case navigation.Symbols:
		title = "File symbols"
	case navigation.Definition:
		title = "Definitions"
	case navigation.References:
		title = "References"
	case navigation.Workspace:
		title = "Workspace symbols"
	default:
		title = "Recent files"
	}
	e.Message = ""
	list := navigation.NewList(title, nil)
	list.Source = source
	e.Locations = list
	e.refreshLocations()
}

func (e *Editor) refreshLocations() {
	e.pendingQuery = nil
	e.dropSearch()
	list := e.Locations
	list.Targets = nil
	list.Selected = 0
	list.Notice = ""
	source, query := list.Source, list.Query
	if source.Live() && query == "" {
		e.LSP.CancelNavigation()
		list.Busy = false
		list.Notice = "Type text to search; Enter opens a result, Esc returns"
		return
	}
	list.Busy = true
	if source == navigation.Project {
		root, err := os.Getwd()
		if err != nil {
			list.Busy = false
			list.Notice = "Cannot search project: " + fsx.Describe(err)
			return
		}
		var active *search.Active
		if location, ok := e.location(); ok {
			active = &search.Active{Path: location.Path, Text: e.Buffer.Text()}
		}
		e.searchJob = search.Start(e.ctx, root, query, active)
		return
	}
	var kind lsp.NavigationKind
	switch source {
	case navigation.Symbols:
		kind = lsp.Symbols
	case navigation.Definition:
		kind = lsp.Definition
	case navigation.References:
		kind = lsp.References
	case navigation.Workspace:
		kind = lsp.Workspace
	default:
		return
	}
	e.syncLSP()
	if err := e.LSP.Navigate(kind, query, e.Buffer.Row, e.Buffer.Col, e.Buffer.Lines()); err != nil {
		list.Busy = false
		list.Notice = err.Error()
	}
}

func (e *Editor) pollLocations() {
	if e.pendingQuery != nil && time.Since(*e.pendingQuery) >= queryDebounce {
		e.refreshLocations()
	}
	if e.searchJob != nil && e.Locations != nil {
		e.searchJob.Poll(e.Locations)
	}
	result := e.LSP.NavigationResult
	e.LSP.NavigationResult = nil
	if result == nil || e.Locations == nil {
		return
	}
	list := e.Locations
	list.Busy = false
	if result.Err != nil {
		list.Notice = result.Err.Error()
		return
	}
	targets := result.Targets
	if result.Kind == lsp.Definition && len(targets) == 1 && e.Help == nil && e.Menu == nil {
		target := targets[0].Location
		// Keep the result available if the user cancels a dirty
		// switch, or if opening the destination fails.
		list.Targets = targets
		e.requestLocation(target, navigation.New)
		e.syncLSP()
		return
	}
	if len(targets) == 1000 {
		list.Notice = "First 1000 results; refine the query"
	} else {
		list.Notice = strconv.Itoa(len(targets)) + " results; type to filter, Enter opens, Esc returns"
	}
	list.Targets = targets
	list.Selected = 0
}

// location is the cursor position and viewport of the open file, or false in
// the welcome state.
func (e *Editor) location() (navigation.Location, bool) {
	if e.IsWelcome() {
		return navigation.Location{}, false
	}
	location := navigation.NewLocation(e.File.Path)
	location.Row = e.Buffer.Row
	location.Col = e.Buffer.Col
	location.Top, location.Left = e.Viewport.Top, e.Viewport.Left
	return location, true
}

func (e *Editor) rememberJump(row, col int) {
	if row == e.Buffer.Row && col == e.Buffer.Col {
		return
	}
	if location, ok := e.location(); ok {
		e.history.Depart(location)
	}
}

func (e *Editor) travel(direction navigation.Travel) {
	if target, ok := e.history.Destination(direction); ok {
		e.requestLocation(target, direction)
	} else {
		e.Message = "No further navigation history"
	}
}

func (e *Editor) requestLocation(location navigation.Location, travel navigation.Travel) {
	e.Message = ""
	if current, ok := e.location(); ok && current.Path == location.Path {
		e.applyLocation(location)
		now, _ := e.location()
		e.history.Arrived(&current, now, travel)
		e.closeLocations()
		e.dropPicker()
		e.closeExplorer()
		return
	}
	e.pendingJump = &pendingJump{location: location, travel: travel}
	if e.File.Dirty(e.Buffer.Text()) {
		e.SwitchPrompt = location.Path
	} else {
		e.openPath(location.Path)
	}
}

func (e *Editor) applyLocation(location navigation.Location) {
	e.Buffer.Row = location.Row
	e.Buffer.Col = location.Col
	e.Buffer.Clamp(false)
	e.Viewport = Viewport{Top: min(location.Top, e.Buffer.LineCount()-1), Left: location.Left}
	e.ViewportRevision++
}
