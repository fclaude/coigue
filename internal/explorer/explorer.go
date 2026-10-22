// Package explorer is a temporary, single-directory browser. Browsing never
// changes the process working directory; a directory is listed in the
// background and handed over with Poll.
package explorer

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fclaude/coigue/internal/fileops"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/picker"
	"github.com/fclaude/coigue/internal/ucd"
	"github.com/fclaude/coigue/internal/walk"
)

// Memory is what the explorer remembers between visits: the selection and the
// scroll position of every directory it showed, and the visibility toggle.
type Memory struct {
	positions map[string]position
	ShowAll   bool
}

type position struct {
	path    string
	hasPath bool
	top     int
}

// pathKey identifies a directory the way Rust's PathBuf does: by its
// components, so "/a/b" and "/a/b/" are the same key.
func pathKey(path string) string { return strings.Join(fsx.Components(path), "\x00") }

// Entry is one row of a listing.
type Entry struct {
	Path      string
	Name      string
	Directory bool
}

type listing struct {
	entries []Entry
	err     string
}

// Explorer lists one directory at a time.
type Explorer struct {
	Directory string
	Query     string
	Filtering bool
	Selected  int
	Top       int
	Loading   bool
	Message   string
	Memory    Memory

	ctx          context.Context
	entries      []Entry
	matches      []int
	receiver     chan listing
	cancel       context.CancelFunc
	done         chan struct{}
	focus        string
	hasFocus     bool
	filterOrigin *filterOrigin
}

type filterOrigin struct {
	path    string
	hasPath bool
	top     int
}

// New shows directory, selecting focus (when not empty) once it is listed.
func New(ctx context.Context, directory, focus string, memory Memory) *Explorer {
	view := &Explorer{
		Directory: directory,
		Memory:    memory,
		ctx:       ctx,
		cancel:    func() {},
	}
	view.load(focus)
	return view
}

// load starts listing the directory in the background. A listing still in
// flight is cancelled and its result never delivered.
func (e *Explorer) load(reveal string) {
	e.cancel()
	ctx, cancel := context.WithCancel(e.ctx)
	e.cancel = cancel
	receiver := make(chan listing, 1)
	e.receiver = receiver
	done := make(chan struct{})
	e.done = done
	directory := e.Directory
	showAll := e.Memory.ShowAll
	remembered, known := e.Memory.positions[pathKey(directory)]
	switch {
	case reveal != "":
		e.focus, e.hasFocus = reveal, true
	case known && remembered.hasPath:
		e.focus, e.hasFocus = remembered.path, true
	default:
		e.focus, e.hasFocus = "", false
	}
	e.Top = 0
	if known {
		e.Top = remembered.top
	}
	e.Selected = 0
	e.entries = nil
	e.matches = nil
	e.Loading = true
	e.Message = ""
	guard.Go(func() {
		defer close(done)
		if result, ok := list(ctx, directory, reveal, showAll); ok {
			receiver <- result
		}
	})
}

// list reads the directory: the entries a one-level walk finds (hidden and
// ignored ones only when showAll), the entry being revealed, and a ".." row.
func list(ctx context.Context, directory, reveal string, showAll bool) (listing, bool) {
	var entries []Entry
	errors := 0
	walker := walk.New(directory, walk.Options{
		MaxDepth:   1,
		Hidden:     !showAll,
		GitIgnore:  !showAll,
		GitExclude: !showAll,
		Parents:    !showAll,
		Filter: func(entry *walk.Entry) bool {
			return entry.Name != ".git" && entry.Name != fileops.Trash
		},
	})
	for {
		item, ok := walker.Next(ctx)
		if !ok {
			break
		}
		if item.Err != nil {
			errors++
			continue
		}
		entry := item.Entry
		if entry.Err != nil {
			errors++
		}
		if entry.Depth == 0 {
			continue
		}
		if entry.Kind == walk.KindFile || entry.Kind == walk.KindDir {
			entries = append(entries, Entry{
				Name:      walk.Lossy(entry.Name),
				Directory: entry.Kind == walk.KindDir,
				Path:      entry.Path,
			})
		}
	}
	if ctx.Err() != nil {
		return listing{}, false
	}
	// Reveal the file being edited (or directory just left), even if
	// normally hidden or ignored. The visibility toggle remains explicit.
	if reveal != "" {
		if parent, ok := fsx.Parent(reveal); ok && samePath(parent, directory) {
			name, hasName := fsx.FileName(reveal)
			if !containsPath(entries, reveal) && hasName && name != ".git" && name != fileops.Trash {
				if info, err := os.Lstat(reveal); err == nil && (info.Mode().IsRegular() || info.IsDir()) {
					entries = append(entries, Entry{Name: walk.Lossy(name), Directory: info.IsDir(), Path: reveal})
				}
			}
		}
	}
	if parent, ok := fsx.Parent(directory); ok {
		entries = append(entries, Entry{Name: "..", Path: parent, Directory: true})
	}
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].Directory != entries[b].Directory {
			return entries[a].Directory
		}
		return entries[a].Name < entries[b].Name
	})
	result := listing{entries: entries}
	if errors > 0 {
		result.err = fmt.Sprintf("%d unreadable entries or ignore errors", errors)
	}
	return result, true
}

// samePath is Path equality: the same components, however spelled.
func samePath(a, b string) bool {
	ac, bc := fsx.Components(a), fsx.Components(b)
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

func containsPath(entries []Entry, path string) bool {
	for _, e := range entries {
		if samePath(e.Path, path) {
			return true
		}
	}
	return false
}

// Poll installs the listing once the background load has finished.
func (e *Explorer) Poll() {
	select {
	case result := <-e.receiver:
		e.entries = result.entries
		e.Message = result.err
		e.Loading = false
		top := e.Top
		e.Filter()
		if e.Query == "" {
			e.Top = top
		}
		focus, hasFocus := e.focus, e.hasFocus
		e.focus, e.hasFocus = "", false
		if hasFocus {
			if index := e.indexOf(focus); index >= 0 {
				e.Selected = index
			}
		}
	default:
	}
}

// indexOf is the position among the matches of the entry with the path, or -1.
func (e *Explorer) indexOf(path string) int {
	for position, i := range e.matches {
		if samePath(e.entries[i].Path, path) {
			return position
		}
	}
	return -1
}

// Remember records the selection and scroll position of the current directory.
func (e *Explorer) Remember() {
	if e.Loading {
		return
	}
	if e.Memory.positions == nil {
		e.Memory.positions = map[string]position{}
	}
	pos := position{top: e.Top}
	if entry, ok := e.Entry(e.Selected); ok {
		pos.path, pos.hasPath = entry.Path, true
	}
	e.Memory.positions[pathKey(e.Directory)] = pos
}

// Navigate shows another directory.
func (e *Explorer) Navigate(directory, focus string) {
	e.Remember()
	e.Directory = directory
	e.Query = ""
	e.Filtering = false
	e.filterOrigin = nil
	e.load(focus)
}

// Parent goes up one directory, selecting the one just left.
func (e *Explorer) Parent() {
	if parent, ok := fsx.Parent(e.Directory); ok {
		e.Navigate(parent, e.Directory)
	}
}

// Refresh lists the directory again.
func (e *Explorer) Refresh(focus string) {
	e.Remember()
	e.Query = ""
	e.Filtering = false
	e.filterOrigin = nil
	e.load(focus)
}

// ToggleVisibility shows or hides hidden and ignored entries.
func (e *Explorer) ToggleVisibility() {
	e.Remember()
	e.Memory.ShowAll = !e.Memory.ShowAll
	e.load("")
}

// StartFilter begins filtering by name, remembering where to return to.
func (e *Explorer) StartFilter() {
	origin := &filterOrigin{top: e.Top}
	if entry, ok := e.Entry(e.Selected); ok {
		origin.path, origin.hasPath = entry.Path, true
	}
	e.filterOrigin = origin
	e.Filtering = true
	e.Query = ""
}

// AcceptFilter leaves filtering with the selected entry still selected.
func (e *Explorer) AcceptFilter() {
	path, hasPath := "", false
	if entry, ok := e.Entry(e.Selected); ok {
		path, hasPath = entry.Path, true
	}
	e.CancelFilter()
	if hasPath {
		if index := e.indexOf(path); index >= 0 {
			e.Selected = index
		}
	}
}

// CancelFilter leaves filtering and restores the selection from before it.
func (e *Explorer) CancelFilter() {
	e.Query = ""
	e.Filtering = false
	e.Filter()
	origin := e.filterOrigin
	e.filterOrigin = nil
	if origin != nil && origin.hasPath {
		if index := e.indexOf(origin.path); index >= 0 {
			e.Selected = index
			e.Top = origin.top
		}
	}
}

// Filter ranks the entries against the query: directories first, then by
// score, then by name.
func (e *Explorer) Filter() {
	query := ucd.Lower(e.Query)
	type ranked struct {
		index int
		score int64
	}
	var list []ranked
	for i, entry := range e.entries {
		if score, ok := picker.Score(entry.Name, query); ok {
			list = append(list, ranked{i, score})
		}
	}
	sort.SliceStable(list, func(a, b int) bool {
		ea, eb := e.entries[list[a].index], e.entries[list[b].index]
		if ea.Directory != eb.Directory {
			return ea.Directory
		}
		if list[a].score != list[b].score {
			return list[a].score > list[b].score
		}
		return ea.Name < eb.Name
	})
	e.matches = e.matches[:0]
	for _, r := range list {
		e.matches = append(e.matches, r.index)
	}
	e.Selected = 0
	e.Top = 0
}

// MoveBy moves the selection, staying within the entries.
func (e *Explorer) MoveBy(delta int) {
	selected := e.Selected
	if delta < 0 {
		if -delta > selected {
			selected = 0
		} else {
			selected += delta
		}
	} else if selected > int(^uint(0)>>1)-delta {
		selected = int(^uint(0) >> 1)
	} else {
		selected += delta
	}
	e.Selected = min(selected, max(e.Len()-1, 0))
}

// Fit scrolls so that the selection is visible in a window of rows rows.
func (e *Explorer) Fit(rows int) {
	rows = max(rows, 1)
	if e.Selected < e.Top {
		e.Top = e.Selected
	}
	if e.Selected >= e.Top+rows {
		e.Top = e.Selected + 1 - rows
	}
	e.Top = min(e.Top, max(e.Len()-rows, 0))
}

// Len is the number of entries shown.
func (e *Explorer) Len() int { return len(e.matches) }

// Entry is the i-th entry shown.
func (e *Explorer) Entry(i int) (*Entry, bool) {
	if i < 0 || i >= len(e.matches) {
		return nil, false
	}
	return &e.entries[e.matches[i]], true
}

// Close cancels a listing still in flight. It may be called more than once.
func (e *Explorer) Close() { e.cancel() }
