package editor

import (
	"errors"
	"os"

	"github.com/fclaude/coigue/internal/buffer"
	"github.com/fclaude/coigue/internal/explorer"
	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/fileops"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/navigation"
	"github.com/fclaude/coigue/internal/picker"
)

// activePath is the absolute, parent-canonical path of the open file.
func (e *Editor) activePath() (string, error) {
	path := e.File.Path
	if !fsx.IsAbsolute(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = fsx.Join(cwd, path)
	}
	parent, ok := fsx.Parent(path)
	if !ok {
		return "", errors.New("No parent directory")
	}
	parent, err := fsx.Canonicalize(parent)
	if err != nil {
		return "", err
	}
	name, ok := fsx.FileName(path)
	if !ok {
		return "", errors.New("No file name")
	}
	return fsx.Join(parent, name), nil
}

func (e *Editor) takeExplorerMemory() explorer.Memory {
	memory := e.explorerMemory
	e.explorerMemory = explorer.Memory{}
	return memory
}

func (e *Editor) replaceExplorer(x *explorer.Explorer) {
	if e.Explorer != nil {
		e.Explorer.Close()
	}
	e.Explorer = x
}

func (e *Editor) openExplorer() {
	e.closeLocations()
	e.Message = ""
	if e.IsWelcome() {
		e.replaceExplorer(explorer.New(e.ctx, e.Welcome, "", e.takeExplorerMemory()))
		e.resetPending()
		return
	}
	path, err := e.activePath()
	if err != nil {
		e.Message = fsx.Describe(err)
		return
	}
	parent, ok := fsx.Parent(path)
	if !ok {
		return
	}
	e.replaceExplorer(explorer.New(e.ctx, parent, path, e.takeExplorerMemory()))
	e.resetPending()
}

func (e *Editor) closeExplorer() {
	if x := e.Explorer; x != nil {
		e.Explorer = nil
		x.Remember()
		e.explorerMemory = x.Memory
		x.Memory = explorer.Memory{}
		x.Close()
	}
}

func (e *Editor) explorerKey(k key.Key, pageRows int) {
	e.Message = ""
	if k.Mods == key.Ctrl && k.Code == 'p' {
		e.openPicker()
		return
	}
	if ctrlAltSuper(k.Mods) {
		return
	}
	x := e.Explorer
	if x.Filtering {
		switch {
		case k.Code == key.Esc:
			x.CancelFilter()
		case k.Code == key.Tab:
			x.AcceptFilter()
		case k.Code == key.Backspace:
			x.Query = popRune(x.Query)
			x.Filter()
		case k.Code.IsChar():
			x.Query += string(k.Code.Rune())
			x.Filter()
		default:
			e.explorerNavigation(k.Code, pageRows)
		}
		return
	}
	switch {
	case k.Code == key.Esc:
		e.closeExplorer()
	case k.Code == '/':
		x.StartFilter()
	case k.Code == '.':
		x.ToggleVisibility()
	case k.Code == 'a' || k.Code == 'A' || k.Code == 'r' || k.Code == 'd':
		e.startFilesystemAction(k.Code.Rune())
	case k.Code == 'u':
		e.restoreDeleted()
	case k.Code == 'h' || k.Code == '-' || k.Code == key.Left:
		x.Parent()
	case k.Code == 'j':
		x.MoveBy(1)
	case k.Code == 'k':
		x.MoveBy(-1)
	case k.Code == 'l' || k.Code == key.Right:
		e.explorerNavigation(key.Enter, pageRows)
	default:
		e.explorerNavigation(k.Code, pageRows)
	}
}

func (e *Editor) startFilesystemAction(k rune) {
	x := e.Explorer
	var prompt FilesystemPrompt
	switch k {
	case 'a':
		prompt = FilesystemPrompt{Action: CreateFile}
	case 'A':
		prompt = FilesystemPrompt{Action: CreateDirectory}
	case 'r', 'd':
		entry, ok := x.Entry(x.Selected)
		if !ok {
			return
		}
		if entry.Name == ".." {
			e.Message = "Select a child entry; the parent entry cannot be changed"
			return
		}
		if k == 'r' {
			prompt = FilesystemPrompt{Action: Rename, Source: entry.Path, Input: entry.Name}
		} else {
			prompt = FilesystemPrompt{Action: Delete, Source: entry.Path}
		}
	default:
		return
	}
	e.FilesystemPrompt = &prompt
}

func (e *Editor) filesystemKey(k key.Key) {
	if k.Code == key.Esc {
		e.FilesystemPrompt = nil
		e.Message = ""
		return
	}
	prompt := e.FilesystemPrompt
	if prompt.Action == Delete {
		switch {
		case k.Code == 'y' && k.Mods == 0:
			e.applyFilesystemAction()
		case k.Code == 'n':
			e.FilesystemPrompt = nil
			e.Message = ""
		}
		return
	}
	if k.Mods == key.Ctrl && k.Code == 'u' {
		prompt.Input = ""
		return
	}
	if ctrlAltSuper(k.Mods) {
		return
	}
	switch {
	case k.Code.IsChar():
		prompt.Input += string(k.Code.Rune())
	case k.Code == key.Backspace:
		prompt.Input = popRune(prompt.Input)
	case k.Code == key.Enter:
		e.applyFilesystemAction()
	}
}

func (e *Editor) applyFilesystemAction() {
	prompt := *e.FilesystemPrompt
	directory := e.Explorer.Directory
	focus, hasFocus, err := e.runFilesystemAction(prompt, directory)
	if err != nil {
		e.Message = "Operation failed: " + fsx.Describe(err)
		return
	}
	e.FilesystemPrompt = nil
	if hasFocus {
		e.Explorer.Refresh(focus)
		e.Message = "Done: " + fsx.Display(focus)
	} else {
		e.Explorer.Refresh("")
		e.Message = "Moved to recovery storage; u restores the latest deletion here"
	}
}

func (e *Editor) runFilesystemAction(prompt FilesystemPrompt, directory string) (focus string, hasFocus bool, err error) {
	switch prompt.Action {
	case CreateFile, CreateDirectory:
		target, err := fileops.Destination(directory, prompt.Input)
		if err != nil {
			return "", false, err
		}
		if err := fileops.Create(target, prompt.Action == CreateDirectory); err != nil {
			return "", false, err
		}
		return target, true, nil
	case Rename:
		source := prompt.Source
		target, err := fileops.Destination(directory, prompt.Input)
		if err != nil {
			return "", false, err
		}
		active := ""
		if !e.IsWelcome() {
			if active, err = e.activePath(); err != nil {
				return "", false, err
			}
		}
		if err := fileops.MoveEntry(source, target); err != nil {
			return "", false, err
		}
		e.history.Rename(source, target)
		if active != "" {
			if suffix, ok := fsx.StripPrefix(active, source); ok {
				if suffix == "" {
					e.File.Path = target
				} else {
					e.File.Path = fsx.Join(target, suffix)
				}
			}
		}
		return target, true, nil
	}
	// Delete.
	if !e.IsWelcome() {
		active, err := e.activePath()
		if err != nil {
			return "", false, err
		}
		if fsx.StartsWith(active, prompt.Source) {
			return "", false, errors.New("Open another file before deleting the current file or its parent")
		}
	}
	if _, err := fileops.TrashEntry(prompt.Source); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func (e *Editor) restoreDeleted() {
	path, err := fileops.RestoreLatest(e.Explorer.Directory)
	if err != nil {
		e.Message = "Restore failed: " + fsx.Describe(err)
		return
	}
	e.Explorer.Refresh(path)
	e.Message = "Restored: " + fsx.Display(path)
}

func (e *Editor) explorerNavigation(code key.Code, pageRows int) {
	x := e.Explorer
	switch code {
	case key.Up:
		x.MoveBy(-1)
	case key.Down:
		x.MoveBy(1)
	case key.PageUp:
		x.MoveBy(-pageRows)
	case key.PageDown:
		x.MoveBy(pageRows)
	case key.Home:
		x.Selected = 0
	case key.End:
		x.Selected = max(x.Len()-1, 0)
	case key.Enter:
		if entry, ok := x.Entry(x.Selected); ok {
			path := entry.Path
			switch {
			case entry.Name == "..":
				x.Parent()
			case entry.Directory:
				x.Navigate(path, "")
			default:
				e.requestOpen(path)
			}
		}
	}
}

func (e *Editor) openPicker() {
	e.closeLocations()
	e.Message = ""
	root, err := os.Getwd()
	if err != nil {
		e.Message = "Cannot open picker: " + fsx.Describe(err)
	} else {
		e.dropPicker()
		e.Picker = picker.New(e.ctx, root)
	}
	e.resetPending()
}

func (e *Editor) pickerKey(k key.Key, pageRows int) {
	e.Message = ""
	if k.Code == key.Esc {
		e.dropPicker()
		return
	}
	if ctrlAltSuper(k.Mods) {
		return
	}
	p := e.Picker
	switch {
	case k.Code.IsChar():
		p.Query += string(k.Code.Rune())
		p.QueryChanged()
	case k.Code == key.Backspace:
		p.Query = popRune(p.Query)
		p.QueryChanged()
	case k.Code == key.Up:
		p.MoveBy(-1)
	case k.Code == key.Down:
		p.MoveBy(1)
	case k.Code == key.PageUp:
		p.MoveBy(-max(pageRows, 1))
	case k.Code == key.PageDown:
		p.MoveBy(max(pageRows, 1))
	case k.Code == key.Home:
		p.Selected = 0
	case k.Code == key.End:
		p.Selected = max(p.Len()-1, 0)
	case k.Code == key.Enter:
		if path, ok := p.Path(); ok {
			e.requestOpen(path)
		}
	}
}

func (e *Editor) switchKey(k key.Key) {
	if ctrlAltSuper(k.Mods) {
		return
	}
	switch {
	case k.Code == key.Esc || k.Code == 'c':
		e.SwitchPrompt = ""
		e.pendingJump = nil
		e.Message = ""
	case k.Code == 's' || k.Code == 'd':
		if k.Code == 's' {
			if err := e.saveCurrent(); err != nil {
				e.Message = "Save failed: " + fsx.Describe(err)
				return
			}
		}
		path := e.SwitchPrompt
		e.SwitchPrompt = ""
		e.openPath(path)
	}
}

func (e *Editor) requestOpen(path string) {
	location := navigation.NewLocation(path)
	e.pendingJump = &pendingJump{location: location, travel: navigation.New}
	e.Message = ""
	if e.File.Dirty(e.Buffer.Text()) {
		e.SwitchPrompt = location.Path
	} else {
		e.openPath(location.Path)
	}
}

func (e *Editor) openPath(path string) {
	// Validate before replacing the live buffer, even after choosing discard.
	var opened *file.File
	var text string
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	if err == nil {
		opened, text, err = file.Open(path)
	}
	if err != nil {
		e.pendingJump = nil
		e.Message = "Cannot open file: " + fsx.Describe(err)
		return
	}
	origin, hasOrigin := e.location()
	target, travel := navigation.NewLocation(path), navigation.New
	if e.pendingJump != nil {
		target, travel = e.pendingJump.location, e.pendingJump.travel
		e.pendingJump = nil
	}
	target.Path = opened.Path
	wasWelcome := e.IsWelcome()
	e.Welcome = ""
	e.File = opened
	e.selectionAnchor = nil
	e.commandVisual = 0
	e.Buffer = buffer.New(text)
	e.Mode = ModeNormal
	e.Command = ""
	e.Search = ""
	e.resetPending()
	e.Message = ""
	e.Generation++
	e.applyLocation(target)
	current, _ := e.location()
	var originPtr *navigation.Location
	if hasOrigin {
		originPtr = &origin
	}
	e.history.Arrived(originPtr, current, travel)
	e.closeLocations()
	e.dropPicker()
	e.closeExplorer()
	if wasWelcome {
		e.RunStart()
	}
}
