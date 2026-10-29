// Package editor is the editor's single owned state value and the modal
// input machine that drives it: Normal, Insert, Visual, Command and Search
// modes, counts and operators, the unnamed register, undo grouping, file
// switching with unsaved-change prompts, and the overlays (menus, help,
// pickers, the explorer, result lists). Nothing here touches the terminal.
package editor

import (
	"context"
	"strconv"
	"time"

	"github.com/fclaude/coigue/internal/actions"
	"github.com/fclaude/coigue/internal/buffer"
	"github.com/fclaude/coigue/internal/explorer"
	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/lsp"
	"github.com/fclaude/coigue/internal/modsrc"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/picker"
	"github.com/fclaude/coigue/internal/search"
	"github.com/fclaude/coigue/internal/theme"
)

// Mode is the editing mode.
type Mode uint8

// Modes.
const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
	ModeVisualLine
	ModeCommand
	ModeSearch
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "Normal"
	case ModeInsert:
		return "Insert"
	case ModeVisual:
		return "Visual"
	case ModeVisualLine:
		return "VisualLine"
	case ModeCommand:
		return "Command"
	}
	return "Search"
}

// Viewport is the scroll position of the buffer view.
type Viewport struct{ Top, Left int }

// register is the unnamed register: whole lines (terminated when more lines
// followed the range) or a run of characters.
type register struct {
	lines      [][]rune
	terminated bool
	chars      []rune
	isLine     bool
}

func lineRegister(lines [][]rune, terminated bool) *register {
	return &register{lines: lines, terminated: terminated, isLine: true}
}

func charRegister(chars []rune) *register { return &register{chars: chars} }

type clipboardExport struct {
	text string
	reg  *register
}

// FilesystemAction is a pending explorer operation.
type FilesystemAction uint8

// Explorer operations.
const (
	CreateFile FilesystemAction = iota
	CreateDirectory
	Rename
	Delete
)

// FilesystemPrompt asks for the input of an explorer operation, or (Delete)
// for a confirmation.
type FilesystemPrompt struct {
	Action FilesystemAction
	// Source is the entry a Rename or Delete applies to.
	Source string
	Input  string
}

// Display is the prompt line.
func (p *FilesystemPrompt) Display() string {
	switch p.Action {
	case CreateFile:
		return "New file: " + p.Input
	case CreateDirectory:
		return "New directory: " + p.Input
	case Rename:
		return "Rename/move (Ctrl-U clears): " + p.Input
	}
	return "Trash? [y] yes [Esc/n] cancel | " + fsx.Display(p.Source)
}

type pendingJump struct {
	location navigation.Location
	travel   navigation.Travel
}

// Editor is the whole editor state. It is created by New or NewWelcome and
// driven by Key and Tick; Close releases its background work.
type Editor struct {
	ctx context.Context

	textwidth       int
	selectionAnchor *[2]int
	clipboardExport *clipboardExport
	pendingQuery    *time.Time
	searchJob       *search.Search
	history         navigation.History
	pendingJump     *pendingJump
	commands        *commandTable

	// extensions is the Scheme interpreter, created when an init file is
	// loaded. interrupter lets the user stop a running extension.
	extensions  *ext.Runtime
	interrupter ext.Watcher
	// initPath is where the init file is read from, for :reload; it is empty
	// when the machine has no place for one. startDone is set once the
	// on-start procedure has run.
	initPath  string
	startDone bool
	// extDeadline overrides the extension deadline; tests shorten it.
	extDeadline time.Duration
	// commandVisual is the Visual mode a command line was opened from (0 if
	// it was not); invocationSelection is the selection handed to the
	// extension command being executed.
	commandVisual       Mode
	invocationSelection *ext.Selection

	// Locations is the open result list (project search, symbols, recent files...).
	Locations *navigation.List
	// Viewport is the scroll position the renderer last used, restored on jumps.
	Viewport         Viewport
	ViewportRevision uint64
	Menu             *actions.Menu
	Help             *help.Help
	Theme            theme.Theme
	// Themes holds the built-in themes and those the init file defined; Theme
	// is one of them.
	Themes theme.Set
	LSP    *lsp.Client
	// Welcome is the launch directory while no file is open, else "".
	Welcome string
	Buffer  *buffer.Buffer
	File    *file.File
	Mode    Mode
	Command string
	Message string

	commandCompletion *commandCompletion
	pending           rune
	count             int
	operatorCount     int
	register          *register
	Search            string
	lastSearch        string
	Picker            *picker.Picker
	Generation        uint64
	Explorer          *explorer.Explorer
	explorerMemory    explorer.Memory
	// SwitchPrompt holds the path of a pending save/discard/cancel choice, else "".
	SwitchPrompt     string
	FilesystemPrompt *FilesystemPrompt
	// ModuleReview is the page of a :module-add or :module-update waiting for
	// an answer, or the :module-list page; see modules.go.
	ModuleReview *ModuleReview
	moduleJob    *moduleJob
	// moduleFetcher is how modules are fetched; nil means GitHub. Tests set it.
	moduleFetcher modsrc.Fetcher
}

// New creates an editor on an opened file. ctx bounds every background job
// (scans, searches, language servers) the editor starts.
func New(ctx context.Context, f *file.File, text string) *Editor {
	e := &Editor{
		ctx:       ctx,
		textwidth: 80,
		LSP:       &lsp.Client{},
		Buffer:    buffer.New(text),
		File:      f,
		commands:  defaultCommands(),
	}
	if f.Path != "" {
		e.history.Visit(navigation.NewLocation(f.Path))
	}
	return e
}

// NewWelcome creates the unattached welcome session rooted at directory.
func NewWelcome(ctx context.Context, directory string) *Editor {
	e := New(ctx, file.Unattached(), "")
	e.Welcome = directory
	return e
}

// IsWelcome reports whether no file is open.
func (e *Editor) IsWelcome() bool { return e.Welcome != "" }

// Close stops all background work. The editor must not be used afterwards.
func (e *Editor) Close() {
	e.dropSearch()
	e.dropPicker()
	if e.Explorer != nil {
		e.Explorer.Close()
		e.Explorer = nil
	}
	e.LSP.Close()
}

func (e *Editor) dropPicker() {
	if e.Picker != nil {
		e.Picker.Close()
		e.Picker = nil
	}
}

func (e *Editor) dropSearch() {
	if e.searchJob != nil {
		e.searchJob.Close()
		e.searchJob = nil
	}
}

func ctrlAltSuper(m key.Mods) bool { return m.Intersects(key.Ctrl | key.Alt | key.Super) }

// Key handles one key press. pageRows is the page size of the terminal at the
// moment the key arrived. It reports whether the editor should quit.
func (e *Editor) Key(k key.Key, pageRows int) bool {
	if e.ModuleReview != nil {
		e.moduleReviewKey(k, pageRows)
		return false
	}
	// Help must not split an Insert session, consume pending keys, or touch
	// the suspended prompt/browser. Intercept before edit bookkeeping.
	if n, ok := k.Code.FunctionKey(); ok && n >= 2 && n <= 4 {
		e.Help = nil
		group := actions.All
		switch n {
		case 2:
			group = actions.Files
		case 3:
			group = actions.Code
		}
		e.Menu = actions.NewMenu(group)
		return false
	}
	if e.Help != nil {
		if k.Code == key.Esc || k.Code == key.F(1) {
			e.Help = nil
		} else if !ctrlAltSuper(k.Mods) {
			code := k.Code
			if code == key.Tab && k.Mods.Has(key.Shift) {
				code = key.BackTab
			}
			e.Help.Key(code, pageRows)
		}
		return false
	}
	if k.Code == key.F(1) {
		e.openHelp()
		return false
	}
	if e.Menu != nil {
		if k.Code == key.Esc {
			e.Menu = nil
		} else if !ctrlAltSuper(k.Mods) {
			if action, ok := e.Menu.Key(k.Code, pageRows); ok {
				e.Menu = nil
				e.activate(action)
			}
		}
		return false
	}
	confirming := e.SwitchPrompt != "" || e.FilesystemPrompt != nil
	if n, ok := k.Code.FunctionKey(); ok && n >= 5 && n <= 9 && !confirming {
		kinds := [...]actions.Kind{actions.ProjectSearch, actions.Symbols, actions.Definition, actions.References, actions.WorkspaceSymbols}
		e.activate(actions.Action{Kind: kinds[n-5]})
		return false
	}
	if k.Mods == key.Alt && (k.Code == key.Left || k.Code == key.Right) && !confirming {
		if k.Code == key.Left {
			e.activate(actions.Action{Kind: actions.Back})
		} else {
			e.activate(actions.Action{Kind: actions.Forward})
		}
		return false
	}
	if e.Locations != nil && e.SwitchPrompt == "" {
		e.locationsKey(k, pageRows)
		return false
	}
	e.Buffer.BeginEdit()
	quit := e.keyInner(k, pageRows)
	if e.Mode != ModeInsert || e.Picker != nil || e.Explorer != nil {
		e.Buffer.FinishEdit()
	}
	e.syncLSP()
	return quit
}

func (e *Editor) locationsKey(k key.Key, pageRows int) {
	if k.Code == key.Esc {
		e.closeLocations()
		return
	}
	if ctrlAltSuper(k.Mods) {
		return
	}
	before := e.Locations.Query
	if location, ok := e.Locations.Key(k.Code, pageRows); ok {
		e.requestLocation(location, navigation.New)
	} else if e.Locations != nil && e.Locations.Source.Live() && e.Locations.Query != before {
		// Debounce bursts of typing. Cancel old work immediately,
		// then search only once the latest query has settled.
		e.dropSearch()
		e.LSP.CancelNavigation()
		list := e.Locations
		list.Targets = nil
		list.Notice = ""
		list.Busy = true
		now := time.Now()
		e.pendingQuery = &now
	}
}

func (e *Editor) activate(action actions.Action) {
	if action.Kind == actions.ShowHelp {
		e.Help = e.newHelp(action.Topic)
		return
	}
	if e.SwitchPrompt != "" || e.FilesystemPrompt != nil {
		e.Message = "Finish or cancel the current confirmation first"
		return
	}
	e.Buffer.FinishEdit()
	e.closeLocations()
	e.Mode = ModeNormal
	e.selectionAnchor = nil
	e.commandVisual = 0
	e.resetPending()
	e.Command = ""
	e.Search = ""
	e.Buffer.Clamp(false)
	switch action.Kind {
	case actions.FindFile:
		e.openPicker()
	case actions.Browse:
		e.dropPicker()
		e.openExplorer()
	case actions.ProjectSearch:
		e.openLocations(navigation.Project)
	case actions.Symbols:
		e.openLocations(navigation.Symbols)
	case actions.Definition:
		e.openLocations(navigation.Definition)
	case actions.References:
		e.openLocations(navigation.References)
	case actions.WorkspaceSymbols:
		e.openLocations(navigation.Workspace)
	case actions.Recent:
		if location, ok := e.location(); ok {
			e.history.Visit(location)
		}
		var targets []navigation.Target
		for _, location := range e.history.Recent {
			label := fsx.Display(location.Path) + ":" + strconv.Itoa(location.Row+1) + ":" + strconv.Itoa(location.Col+1)
			targets = append(targets, navigation.Target{Location: location, Label: label})
		}
		e.Locations = navigation.NewList("Recent files", targets)
	case actions.Back:
		e.travel(navigation.Back)
	case actions.Forward:
		e.travel(navigation.Forward)
	default:
		if e.IsWelcome() {
			e.Message = "No file open; use F2 to find or create one"
			return
		}
		switch action.Kind {
		case actions.FindText, actions.GoLine:
			e.dropPicker()
			e.closeExplorer()
			if action.Kind == actions.FindText {
				e.Mode = ModeSearch
			} else {
				e.Mode = ModeCommand
			}
		case actions.FirstLine, actions.LastLine:
			e.dropPicker()
			e.closeExplorer()
			row := 0
			if action.Kind == actions.LastLine {
				row = e.Buffer.LineCount() - 1
			}
			e.rememberJump(row, 0)
			e.Buffer.Row = row
			e.Buffer.Col = 0
		}
	}
}

func (e *Editor) openHelp() {
	var topic help.Topic
	switch {
	case e.SwitchPrompt != "" || e.FilesystemPrompt != nil:
		topic = help.Prompts
	case e.Picker != nil || (e.Locations != nil && e.Locations.Source == navigation.Recent):
		topic = help.Files
	case e.Locations != nil:
		topic = help.Code
	case e.Explorer != nil:
		topic = help.Explorer
	case e.Mode == ModeCommand || e.Mode == ModeSearch:
		topic = help.Prompts
	case e.IsWelcome():
		topic = help.Start
	default:
		topic = help.Editing
	}
	e.Help = e.newHelp(topic)
}

func (e *Editor) syncLSP() {
	e.LSP.Sync(e.File.Path, e.Generation, e.Buffer.Revision(), e.File.Revision(), e.Buffer.Text)
}

// Tick runs the periodic work between keys: language-server sync and
// results, scans for the picker and explorer, and delayed searches.
func (e *Editor) Tick(pageRows int) {
	e.pollLSP()
	e.pollModules()
	e.pollEvents()
	if e.Picker != nil {
		e.Picker.Poll()
	}
	if e.Explorer != nil {
		e.Explorer.Poll()
		e.Explorer.Fit(pageRows)
	}
}

func (e *Editor) pollLSP() {
	e.syncLSP()
	e.LSP.Poll(e.Buffer.Lines())
	e.pollLocations()
}

func (e *Editor) saveCurrent() error {
	if err := e.File.Save(e.Buffer.Text()); err != nil {
		return err
	}
	// Synchronize the old document before save-and-open replaces it.
	e.syncLSP()
	return nil
}

func (e *Editor) resetPending() {
	e.pending = 0
	e.count = 0
	e.operatorCount = 0
}

// PendingInput is the partly typed count and operator shown in the message row.
func (e *Editor) PendingInput() string {
	input := ""
	if e.operatorCount != 0 {
		input += strconv.Itoa(e.operatorCount)
	}
	if e.pending != 0 {
		input += string(e.pending)
	}
	if e.count != 0 {
		input += strconv.Itoa(e.count)
	}
	return input
}

// takeCount consumes the typed counts. A count typed before and after an
// operator multiplies; the result is capped at 9999.
func (e *Editor) takeCount() (count int, explicit bool) {
	explicit = e.count != 0 || e.operatorCount != 0
	count = 1
	if e.count != 0 {
		count = e.count
	}
	if e.operatorCount != 0 {
		count *= e.operatorCount
	}
	count = min(count, 9999)
	e.operatorCount = 0
	e.count = 0
	return count, explicit
}

// Selection returns the selected columns of a row [start, end), with a virtual
// cell for a selected newline or empty line.
func (e *Editor) Selection(row int) (start, end int, ok bool) {
	mode := e.Mode
	if mode == ModeCommand {
		// A command line opened from Visual mode keeps the selection visible.
		mode = e.commandVisual
	}
	if mode != ModeVisual && mode != ModeVisualLine {
		return 0, 0, false
	}
	if e.selectionAnchor == nil {
		return 0, 0, false
	}
	anchor := *e.selectionAnchor
	cursor := [2]int{e.Buffer.Row, e.Buffer.Col}
	first, last := anchor, cursor
	if posLess(cursor, anchor) {
		first, last = cursor, anchor
	}
	if row < first[0] || row > last[0] {
		return 0, 0, false
	}
	lines := e.Buffer.Lines()
	if row >= len(lines) {
		return 0, 0, false
	}
	length := len(lines[row])
	if mode == ModeVisualLine {
		return 0, length + 1, true
	}
	start, end = 0, length+1
	if row == first[0] {
		start = first[1]
	}
	if row == last[0] {
		end = last[1] + 1
	}
	return start, end, true
}

func posLess(a, b [2]int) bool { return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1]) }
