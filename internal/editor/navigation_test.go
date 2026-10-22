package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fclaude/coigue/internal/actions"
	"github.com/fclaude/coigue/internal/help"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/lsp"
	"github.com/fclaude/coigue/internal/navigation"
)

func definitionResult(targets ...navigation.Target) *lsp.NavigationResult {
	return &lsp.NavigationResult{Kind: lsp.Definition, Targets: targets}
}

// Rust: editor::tests::definition_cancel_keeps_result_and_help_prevents_background_jumps
// Parity: LNV, SWT, HLP.
func TestDefinitionCancelKeepsResultAndHelpPreventsBackgroundJumps(t *testing.T) {
	dir, e := editingFixture(t, "original")
	original := e.File.Path
	targetPath := filepath.Join(dir, "definition.rs")
	write(t, targetPath, "fn target() {}")
	targetPath = canonical(t, targetPath)
	location := navigation.NewLocation(targetPath)
	location.Col = 3
	target := navigation.Target{Location: location, Label: "target"}
	send(e, "i!")
	press(e, key.Esc)
	e.activate(actions.Action{Kind: actions.Definition})
	e.LSP.NavigationResult = definitionResult(target)
	e.pollLocations()
	if e.SwitchPrompt == "" {
		t.Fatal("a dirty cross-file jump did not ask for confirmation")
	}
	if len(e.Locations.Targets) != 1 {
		t.Fatalf("result list lost its target: %d", len(e.Locations.Targets))
	}
	send(e, "c")
	if e.File.Path != original {
		t.Fatalf("path = %q", e.File.Path)
	}
	press(e, key.Enter)
	if e.SwitchPrompt == "" {
		t.Fatal("Enter on the kept result did not ask again")
	}
	send(e, "s")
	if e.File.Path != targetPath || e.Buffer.Col != 3 {
		t.Fatalf("after save-and-open: %q col %d", e.File.Path, e.Buffer.Col)
	}
	e.travel(navigation.Back)
	if e.File.Path != original {
		t.Fatalf("back = %q", e.File.Path)
	}
	e.activate(actions.Action{Kind: actions.Definition})
	other := target
	other.Location.Col = 4
	other.Label = "second definition"
	e.LSP.NavigationResult = definitionResult(target, other)
	e.pollLocations()
	if e.File.Path != original || len(e.Locations.Targets) != 2 {
		t.Fatalf("two definitions: path %q targets %d", e.File.Path, len(e.Locations.Targets))
	}
	press(e, key.Esc)
	e.activate(actions.Action{Kind: actions.Definition})
	e.openHelp()
	e.LSP.NavigationResult = definitionResult(target)
	e.pollLocations()
	if e.File.Path != original || e.Help == nil || len(e.Locations.Targets) != 1 {
		t.Fatalf("help open: path %q help %v targets %d", e.File.Path, e.Help, len(e.Locations.Targets))
	}
	press(e, key.Esc)
	press(e, key.Enter)
	if e.File.Path != targetPath {
		t.Fatalf("final path = %q", e.File.Path)
	}
}

// Rust: editor::tests::navigation_history_preserves_positions_failures_dirty_choices_and_branches
// Parity: NAV, SWT.
func TestNavigationHistoryPreservesPositionsFailuresDirtyChoicesAndBranches(t *testing.T) {
	dir, e := editingFixture(t, "first\nsecond\nthird")
	first := e.File.Path
	second := filepath.Join(dir, "second.txt")
	write(t, second, "alpha\nbeta\ngamma")
	second = canonical(t, second)
	e.Buffer.Row = 2
	e.Buffer.Col = 3
	e.Viewport = Viewport{Top: 1, Left: 2}
	e.requestOpen(second)
	if e.Buffer.Row != 0 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	e.Buffer.Row = 1
	e.Buffer.Col = 2
	e.Viewport = Viewport{Top: 1, Left: 1}
	e.travel(navigation.Back)
	if e.File.Path != first || cursor(e) != [2]int{2, 3} || e.Viewport != (Viewport{Top: 1, Left: 2}) {
		t.Fatalf("back: %q %v %v", e.File.Path, cursor(e), e.Viewport)
	}
	e.travel(navigation.Forward)
	if e.File.Path != second || cursor(e) != [2]int{1, 2} || e.Viewport != (Viewport{Top: 1, Left: 1}) {
		t.Fatalf("forward: %q %v %v", e.File.Path, cursor(e), e.Viewport)
	}
	send(e, "i!")
	press(e, key.Esc)
	e.travel(navigation.Back)
	if e.SwitchPrompt == "" {
		t.Fatal("dirty back did not prompt")
	}
	press(e, key.Esc)
	if e.File.Path != second {
		t.Fatalf("path = %q", e.File.Path)
	}
	if dest, ok := e.history.Destination(navigation.Back); !ok || dest.Path != first {
		t.Fatalf("back destination = %+v", dest)
	}
	e.travel(navigation.Back)
	send(e, "s")
	if got := read(t, second); got != "alpha\nbe!ta\ngamma" {
		t.Fatalf("saved = %q", got)
	}
	// Removing a destination must not pop the forward stack or current buffer.
	os.Remove(second)
	e.travel(navigation.Forward)
	if e.File.Path != first || !contains(e.Message, "Cannot open") {
		t.Fatalf("failed forward: %q %q", e.File.Path, e.Message)
	}
	if _, ok := e.history.Destination(navigation.Forward); !ok {
		t.Fatal("failed open popped the forward stack")
	}
	// A new same-file line jump clears forward history and remains undo-neutral.
	e.execute("1")
	if _, ok := e.history.Destination(navigation.Forward); ok {
		t.Fatal("a new jump kept forward history")
	}
	e.travel(navigation.Back)
	if e.Buffer.Row != 2 {
		t.Fatalf("row = %d", e.Buffer.Row)
	}
	send(e, "i!")
	press(e, key.Esc)
	e.travel(navigation.Forward)
	if e.SwitchPrompt != "" || !e.File.Dirty(e.Buffer.Text()) {
		t.Fatalf("same-file forward: prompt %q dirty %v", e.SwitchPrompt, e.File.Dirty(e.Buffer.Text()))
	}
	send(e, "u")
	if e.Buffer.Text() != "first\nsecond\nthird" {
		t.Fatalf("undo = %q", e.Buffer.Text())
	}
	e.activate(actions.Action{Kind: actions.Recent})
	if len(e.Locations.Targets) != 2 {
		t.Fatalf("recent files = %d", len(e.Locations.Targets))
	}
	e.Locations.Query = "second.txt"
	if len(e.Locations.Matches()) != 1 {
		t.Fatalf("matches = %d", len(e.Locations.Matches()))
	}
	press(e, key.Enter)
	if e.Locations == nil || e.File.Path != first {
		t.Fatalf("Enter on a missing recent file: list %v path %q", e.Locations, e.File.Path)
	}
	press(e, key.Esc)
	if e.Locations != nil {
		t.Fatal("Esc did not close the list")
	}
}

// Rust: editor::tests::action_menus_preserve_canceled_state_and_dispatch_filtered_actions
// Parity: ACT, KEY-01, KEY-10.
func TestActionMenusPreserveCanceledStateAndDispatchFilteredActions(t *testing.T) {
	_, e := editingFixture(t, "first\nsecond\nthird")
	send(e, "iONE")
	press(e, key.F(2))
	send(e, "impossible action")
	press(e, key.Enter)
	if e.Menu == nil {
		t.Fatal("an empty selection closed the menu")
	}
	press(e, key.Esc)
	if e.Mode != ModeInsert {
		t.Fatalf("mode = %v", e.Mode)
	}
	send(e, "TWO")
	press(e, key.Esc)
	send(e, "u")
	if e.Buffer.Text() != "first\nsecond\nthird" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, ":the")
	press(e, key.F(3))
	press(e, key.Esc)
	if e.Command != "the" || e.Mode != ModeCommand {
		t.Fatalf("command %q mode %v", e.Command, e.Mode)
	}
	press(e, key.Esc)
	press(e, key.F(3))
	send(e, "last line")
	press(e, key.Enter)
	if e.Buffer.Row != 2 || e.Menu != nil {
		t.Fatalf("row %d menu %v", e.Buffer.Row, e.Menu)
	}
	press(e, key.F(4))
	send(e, "getting started")
	press(e, key.Enter)
	if e.Help == nil || e.Help.Topic != help.Start {
		t.Fatalf("help = %+v", e.Help)
	}
	press(e, key.Esc)
	press(e, key.F(2))
	send(e, "find")
	press(e, key.Enter)
	if e.Picker == nil || e.Buffer.Row != 2 {
		t.Fatalf("picker %v row %d", e.Picker, e.Buffer.Row)
	}
	press(e, key.Esc)
	e.SwitchPrompt = "pending"
	press(e, key.F(3))
	send(e, "first")
	press(e, key.Enter)
	if e.SwitchPrompt == "" || e.Buffer.Row != 2 {
		t.Fatalf("prompt %q row %d", e.SwitchPrompt, e.Buffer.Row)
	}
}

// Rust: editor::tests::help_suspends_insert_commands_browsers_and_confirmations
// Parity: HLP, UND-12.
func TestHelpSuspendsInsertCommandsBrowsersAndConfirmations(t *testing.T) {
	dir, e := editingFixture(t, "original")
	send(e, "iONE")
	position := cursor(e)
	press(e, key.F(1))
	if e.Help == nil || e.Help.Topic != help.Editing {
		t.Fatalf("help = %+v", e.Help)
	}
	send(e, "dd:q!")
	press(e, key.Esc)
	if e.Mode != ModeInsert || cursor(e) != position {
		t.Fatalf("mode %v cursor %v", e.Mode, cursor(e))
	}
	send(e, "TWO")
	press(e, key.Esc)
	send(e, "u")
	if e.Buffer.Text() != "original" {
		t.Fatalf("text = %q", e.Buffer.Text())
	}
	send(e, ":w")
	press(e, key.Tab)
	press(e, key.F(1))
	if e.Help.Topic != help.Prompts {
		t.Fatalf("topic = %v", e.Help.Topic)
	}
	press(e, key.F(1))
	if e.Command != "w" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Tab)
	if e.Command != "wq" {
		t.Fatalf("command = %q", e.Command)
	}
	press(e, key.Esc)
	send(e, "2d")
	pending := e.PendingInput()
	press(e, key.F(1))
	press(e, key.Esc)
	if e.PendingInput() != pending {
		t.Fatalf("pending input %q, want %q", e.PendingInput(), pending)
	}
	press(e, key.Esc)
	e.openExplorer()
	readyExplorer(t, e)
	send(e, "/orig")
	query := e.Explorer.Query
	press(e, key.F(1))
	if e.Help.Topic != help.Explorer {
		t.Fatalf("topic = %v", e.Help.Topic)
	}
	press(e, key.Esc)
	if e.Explorer.Query != query || !e.Explorer.Filtering {
		t.Fatalf("explorer filter lost: %q %v", e.Explorer.Query, e.Explorer.Filtering)
	}
	e.FilesystemPrompt = &FilesystemPrompt{Action: CreateFile, Input: "draft"}
	press(e, key.F(1))
	if e.Help.Topic != help.Prompts {
		t.Fatalf("topic = %v", e.Help.Topic)
	}
	press(e, key.Esc)
	if e.FilesystemPrompt.Input != "draft" {
		t.Fatalf("prompt input = %q", e.FilesystemPrompt.Input)
	}
	e.FilesystemPrompt = nil
	e.SwitchPrompt = filepath.Join(dir, "next")
	press(e, key.F(1))
	press(e, key.Esc)
	if e.SwitchPrompt == "" {
		t.Fatal("help dismissed the switch prompt")
	}
	e.SwitchPrompt = ""
	e.openPicker()
	press(e, key.F(1))
	if e.Help.Topic != help.Files {
		t.Fatalf("topic = %v", e.Help.Topic)
	}
	press(e, key.Esc)
	if e.Picker == nil {
		t.Fatal("help closed the picker")
	}
	welcome := newWelcome(t, dir)
	press(welcome, key.F(1))
	if welcome.Help == nil || welcome.Help.Topic != help.Start {
		t.Fatalf("welcome help = %+v", welcome.Help)
	}
	press(welcome, key.Esc)
	send(welcome, ":help")
	press(welcome, key.Enter)
	if welcome.Help == nil {
		t.Fatal(":help did not open help")
	}
	press(welcome, key.F(2))
	if welcome.Help != nil || welcome.Menu == nil || !welcome.IsWelcome() {
		t.Fatalf("F2 over help: help %v menu %v", welcome.Help, welcome.Menu)
	}
}
