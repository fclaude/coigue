// Package render turns the editor's state into terminal bytes. It keeps the
// previous frame as one byte string per row and, on each draw, rewrites only
// the rows that changed, wrapped in a synchronized update; an unchanged frame
// writes nothing. Resizing never clears the whole screen: the cache is dropped
// and every row is rewritten with cursor addressing and erase-to-end-of-line.
package render

import (
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/fclaude/coigue/internal/actions"
	"github.com/fclaude/coigue/internal/editor"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/semantic"
	"github.com/fclaude/coigue/internal/syntax"
	"github.com/fclaude/coigue/internal/term"
	"github.com/fclaude/coigue/internal/theme"
	"github.com/fclaude/coigue/internal/width"
)

// View is the renderer's cache: viewport, last frame and highlighting.
type View struct {
	top, left        int
	cols, rows       int
	previous         [][]byte
	noColor          paint
	cursor           *[2]int
	revision         *[2]uint64
	styles           [][]syntax.Style
	semanticRevision uint64
	dirty            bool
	generation       uint64
	viewportRevision uint64
	syntaxPath       *string
	theme            *theme.Theme
}

// charWidth is c.width().unwrap_or(0).
func charWidth(c rune) int {
	w, _ := width.Char(c)
	return w
}

// cell returns the text drawn for a character and the columns it occupies:
// tabs are four spaces, control characters a replacement mark.
func cell(c rune) (string, int) {
	switch {
	case c == '\t':
		return "    ", 4
	case isControl(c):
		return "�", 1
	}
	return string(c), charWidth(c)
}

func isControl(c rune) bool { return c < 0x20 || (c >= 0x7f && c < 0xa0) }

func cellWidth(c rune) int {
	_, w := cell(c)
	return w
}

func lineCells(line []rune) int {
	n := 0
	for _, c := range line {
		n += cellWidth(c)
	}
	return n
}

func textCells(s string) int {
	n := 0
	for _, c := range s {
		n += cellWidth(c)
	}
	return n
}

// visibleCells sums the plain display widths of already-transformed text.
func visibleCells(s string) int {
	n := 0
	for _, c := range s {
		n += charWidth(c)
	}
	return n
}

// clipped keeps the part of text that lies in display columns
// [left, left+width); a wide character cut by either edge becomes spaces.
func clipped(text []rune, left, width int) string {
	col := 0
	var out strings.Builder
	for _, c := range text {
		s, w := cell(c)
		switch {
		case col >= left && col+w <= left+width:
			out.WriteString(s)
		case col < left+width && col+w > left:
			out.WriteString(strings.Repeat(" ", min(col+w, left+width)-max(col, left)))
		}
		col += w
	}
	return out.String()
}

// paint selects how colours are written. With NO_COLOR set, crossterm
// reduced every colour command to a bare "ESC[m" while still sending the
// attribute commands; paint(true) reproduces that (Rust oddity ODD-19).
type paint bool

func (p paint) foreground(c theme.Color) string {
	if p {
		return "\x1b[m"
	}
	return foreground(c)
}

func (p paint) background(c theme.Color) string {
	if p {
		return "\x1b[m"
	}
	return background(c)
}

func foreground(c theme.Color) string {
	switch c.Kind {
	case theme.ColorRGB:
		return term.ForegroundRGB(c.R, c.G, c.B)
	case theme.ColorAnsi:
		return term.ForegroundAnsi(c.Index)
	}
	return term.ResetForeground
}

func background(c theme.Color) string {
	switch c.Kind {
	case theme.ColorRGB:
		return term.BackgroundRGB(c.R, c.G, c.B)
	case theme.ColorAnsi:
		return term.BackgroundAnsi(c.Index)
	}
	return term.ResetBackground
}

func rowColors(out *[]byte, p paint, palette theme.Palette, surface bool) {
	bg := palette.Background
	if surface {
		bg = palette.Surface
	}
	*out = append(*out, p.foreground(palette.Foreground)...)
	*out = append(*out, p.background(bg)...)
}

// selectionRange is a half-open range of character indices.
type selectionRange struct{ start, end int }

func (r selectionRange) contains(i int) bool { return i >= r.start && i < r.end }

// styledLine writes line with exactly the same cell clipping as plain text;
// escape bytes never count as cells. A reverse-video selection, bold and
// italic attributes are switched on and off as the styles change and are all
// closed before returning, so nothing leaks into the next row.
func styledLine(out *[]byte, line []rune, styles []syntax.Style, left, width int, palette theme.Palette, selection *selectionRange, p paint) {
	col := 0
	previous := palette.Foreground
	previousBold, previousItalic, reversed := false, false, false
	for index, c := range line {
		if index >= len(styles) {
			break
		}
		style := styles[index]
		s, w := cell(c)
		var visible string
		switch {
		case col >= left && col+w <= left+width:
			visible = s
		case col < left+width && col+w > left:
			visible = strings.Repeat(" ", min(col+w, left+width)-max(col, left))
		}
		if visible != "" {
			selected := selection != nil && selection.contains(index)
			if selected != reversed {
				if selected {
					*out = append(*out, term.Reverse...)
				} else {
					*out = append(*out, term.NoReverse...)
				}
				reversed = selected
			}
			if next := palette.Syntax(style); next != previous {
				*out = append(*out, p.foreground(next)...)
				previous = next
			}
			if bold := palette.Bold(style); bold != previousBold {
				if bold {
					*out = append(*out, term.Bold...)
				} else {
					*out = append(*out, term.NormalIntensity...)
				}
				previousBold = bold
			}
			if italic := style == syntax.Emphasis; italic != previousItalic {
				if italic {
					*out = append(*out, term.Italic...)
				} else {
					*out = append(*out, term.NoItalic...)
				}
				previousItalic = italic
			}
			*out = append(*out, visible...)
		}
		col += w
	}
	if reversed {
		*out = append(*out, term.NoReverse...)
	}
	if selection != nil && selection.contains(len(line)) && col >= left && col < left+width {
		*out = append(*out, term.Reverse+" "+term.NoReverse...)
	}
	if previousBold {
		*out = append(*out, term.NormalIntensity...)
	}
	if previousItalic {
		*out = append(*out, term.NoItalic...)
	}
	*out = append(*out, p.foreground(palette.Foreground)...)
}

func switchPrompt(e *editor.Editor) string {
	return "Unsaved | [s] save/open  [d] discard/open  [c/Esc] cancel | " + fsx.Display(e.SwitchPrompt)
}

// SetNoColor makes the view write colour commands the way crossterm does
// when the NO_COLOR environment variable is non-empty.
func (v *View) SetNoColor(disabled bool) { v.noColor = paint(disabled) }

// PageRows is the number of rows a page movement covers on the last drawn frame.
func (v *View) PageRows() int {
	return max(v.rows-2, 1)
}

// Draw renders the editor to out at the terminal's current size, and records
// the viewport so a later jump back to this file restores it.
func (v *View) Draw(e *editor.Editor, t *term.Terminal, out io.Writer) error {
	w, h, err := t.Size()
	if err != nil {
		return err
	}
	if err := v.DrawTo(e, w, h, out); err != nil {
		return err
	}
	if !e.IsWelcome() && e.Help == nil && e.Menu == nil && e.Locations == nil &&
		e.Picker == nil && e.Explorer == nil {
		e.Viewport = editor.Viewport{Top: v.top, Left: v.left}
	}
	return nil
}

func resize(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

func (v *View) drawLocations(e *editor.Editor, out io.Writer) error {
	list := e.Locations
	rows := max(v.rows-2, 0)
	top := max(list.Selected-max(rows-1, 0), 0)
	matches := list.Matches()
	busy := ""
	if list.Busy {
		busy = " | searching..."
	}
	notice := "Enter open, Esc return"
	if list.Notice != "" {
		notice = list.Notice
	}
	lines := []string{list.Title + " " + strconv.Itoa(len(matches)) + "/" + strconv.Itoa(len(list.Targets)) + busy + " | " + notice}
	for i := top; i < len(matches) && i < top+rows; i++ {
		marker := " "
		if i == list.Selected {
			marker = ">"
		}
		lines = append(lines, marker+" "+matches[i].Label)
	}
	if len(matches) == 0 && rows > 0 {
		switch {
		case list.Busy:
			lines = append(lines, "Searching...")
		case len(list.Targets) == 0 && list.Notice != "":
			lines = append(lines, list.Notice)
		default:
			lines = append(lines, "No matches")
		}
	}
	lines = resize(lines, max(v.rows-1, 0))
	switch {
	case e.SwitchPrompt != "":
		lines = append(lines, switchPrompt(e))
	case e.Message != "":
		lines = append(lines, e.Message)
	default:
		label := "Filter"
		if list.Source.Live() {
			label = "Search"
		}
		lines = append(lines, label+": "+list.Query)
	}
	return v.drawBrowserLines(lines, e, out)
}

func (v *View) drawMenu(e *editor.Editor, out io.Writer) error {
	menu := e.Menu
	rows := max(v.rows-2, 0)
	top := max(menu.Selected-max(rows-1, 0), 0)
	entries := menu.Entries()
	lines := []string{menu.Group.Title() + " | Enter select, Esc return, Tab category"}
	for i := top; i < len(entries) && i < top+rows; i++ {
		marker := " "
		if i == menu.Selected {
			marker = ">"
		}
		lines = append(lines, marker+" "+entries[i].Title+"    "+entries[i].Shortcut)
	}
	if len(entries) == 0 && rows > 0 {
		lines = append(lines, "No matching actions")
	}
	lines = resize(lines, max(v.rows-1, 0))
	lines = append(lines, "Action: "+menu.Query)
	return v.drawBrowserLines(lines, e, out)
}

func (v *View) drawHelp(e *editor.Editor, out io.Writer) error {
	h := e.Help
	rows := max(v.rows-2, 0)
	lines := []string{"Help | " + h.Title() + " | Tab: next topic"}
	text := h.Lines()
	for i := h.Scroll; i < len(text) && i < h.Scroll+rows; i++ {
		lines = append(lines, text[i])
	}
	lines = resize(lines, max(v.rows-1, 0))
	lines = append(lines, "Esc/F1 return | Tab topics | Up/Down scroll | Page Up/Down")
	return v.drawBrowserLines(lines, e, out)
}

func (v *View) drawPicker(e *editor.Editor, out io.Writer) error {
	p := e.Picker
	h := v.rows
	rows := max(h-2, 0)
	top := max(p.Selected-max(rows-1, 0), 0)
	lines := make([]string, 0, h)
	scanning, skipped := "", ""
	if p.Scanning {
		scanning = " | scanning..."
	}
	if p.Skipped > 0 {
		skipped = " | " + strconv.Itoa(p.Skipped) + " unreadable entries skipped"
	}
	lines = append(lines, "Files "+strconv.Itoa(p.Len())+"/"+strconv.Itoa(p.Total())+scanning+skipped+
		" | Enter open, Esc cancel | "+fsx.Display(p.Root))
	for index := top; index < top+rows; index++ {
		if label, ok := p.Label(index); ok {
			marker := " "
			if index == p.Selected {
				marker = ">"
			}
			lines = append(lines, marker+" "+label)
		} else if index == top {
			lines = append(lines, "No matches")
		} else {
			lines = append(lines, "")
		}
	}
	if e.Message != "" {
		lines[0] = e.Message
	}
	prompt := "Open: " + p.Query
	if e.SwitchPrompt != "" {
		prompt = switchPrompt(e)
	}
	if h == 1 {
		lines = lines[:0]
	} else {
		lines = lines[:min(len(lines), h-1)]
	}
	lines = append(lines, prompt)
	return v.drawBrowserLines(lines, e, out)
}

func (v *View) drawExplorer(e *editor.Editor, out io.Writer) error {
	x := e.Explorer
	h := v.rows
	rows := max(h-2, 0)
	top := min(max(min(x.Top, x.Selected), max(x.Selected-max(rows-1, 0), 0)), max(x.Len()-rows, 0))
	loading, showAll := "", ""
	if x.Loading {
		loading = " | loading..."
	}
	if x.Memory.ShowAll {
		showAll = " | show all"
	}
	lines := []string{fsx.Display(x.Directory) + " | " + strconv.Itoa(x.Len()) + " entries" + loading + showAll}
	if x.Filtering {
		lines[0] = "Filter: Tab selects for actions, Esc cancels | " + fsx.Display(x.Directory)
	}
	if e.Message != "" {
		lines[0] = e.Message
	}
	for i := top; i < top+rows; i++ {
		if entry, ok := x.Entry(i); ok {
			marker, suffix := " ", ""
			if i == x.Selected {
				marker = ">"
			}
			if entry.Directory {
				suffix = "/"
			}
			lines = append(lines, marker+" "+entry.Name+suffix)
		} else if i == top && !x.Loading {
			lines = append(lines, "No matches")
		} else {
			lines = append(lines, "")
		}
	}
	var prompt string
	switch {
	case e.SwitchPrompt != "":
		prompt = switchPrompt(e)
	case e.FilesystemPrompt != nil:
		prompt = e.FilesystemPrompt.Display()
	case x.Filtering:
		prompt = "/" + x.Query
	case x.Message != "":
		prompt = x.Message
	default:
		prompt = "a file  A dir  r move  d trash  u restore  / filter  Esc return"
	}
	if h == 1 {
		lines = lines[:0]
	} else {
		lines = lines[:min(len(lines), h-1)]
	}
	lines = append(lines, prompt)
	return v.drawBrowserLines(lines, e, out)
}

func startsWithAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func (v *View) drawBrowserLines(lines []string, e *editor.Editor, out io.Writer) error {
	palette := e.Palette()
	w, h := v.cols, v.rows
	if h > 1 {
		strip := actions.Strip
		if w < 60 {
			strip = "F2 Files F3 Code F1 Help"
		}
		if lines[0] == "" {
			lines[0] = strip
		} else {
			lines[0] = strip + " | " + lines[0]
		}
	}
	prompt := lines[len(lines)-1]
	frame := make([][]byte, 0, len(lines))
	var update []byte
	update = append(update, term.BeginSynchronizedUpdate+term.Hide...)
	for y, line := range lines {
		// Keep the end of a long query visible while typing.
		left := 0
		if y == h-1 && startsWithAny(line, "/", ":", "Open:", "Action:", "Filter:", "Search:", "New ", "Rename/move") {
			left = max(textCells(line)+1-w, 0)
		}
		visible := clipped([]rune(line), left, w)
		bytes := []byte(visible)
		if y >= len(v.previous) || !equalBytes(v.previous[y], bytes) {
			surface := e.Welcome == "" || e.Picker != nil || e.Explorer != nil
			rowColors(&update, v.noColor, palette, surface && y < h-1 && (y == 0 || strings.HasPrefix(line, "> ")))
			update = append(update, term.MoveTo(0, y)...)
			update = append(update, visible...)
			if cells := visibleCells(visible); cells < w {
				update = append(update, term.MoveTo(cells, y)...)
				update = append(update, term.ClearUntilNewLine...)
			}
		}
		frame = append(frame, bytes)
	}
	x := min(textCells(prompt), w-1)
	if equalFrames(v.previous, frame) && v.cursor != nil && *v.cursor == [2]int{x, h - 1} {
		return nil
	}
	rowColors(&update, v.noColor, palette, false)
	update = append(update, term.MoveTo(x, h-1)...)
	update = append(update, term.Show+term.EndSynchronizedUpdate...)
	if _, err := out.Write(update); err != nil {
		return err
	}
	v.previous = frame
	v.cursor = &[2]int{x, h - 1}
	return nil
}

func equalBytes(a, b []byte) bool { return bytes.Equal(a, b) }

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// DrawTo renders one frame for a terminal of w columns by h rows into out.
func (v *View) DrawTo(e *editor.Editor, w, h int, out io.Writer) error {
	if v.cols != w || v.rows != h {
		v.previous = nil
		v.cols, v.rows = w, h
	}
	if v.theme == nil || *v.theme != e.Theme {
		// Plain rows and empty tails must repaint too, even when their text
		// and the cached syntax tokens are unchanged.
		v.previous = nil
		t := e.Theme
		v.theme = &t
	}
	if w == 0 || h == 0 {
		return nil
	}
	if v.generation != e.Generation {
		v.top, v.left = e.Viewport.Top, e.Viewport.Left
		v.revision = nil
		v.generation = e.Generation
	}
	if v.viewportRevision != e.ViewportRevision {
		v.top, v.left = e.Viewport.Top, e.Viewport.Left
		v.viewportRevision = e.ViewportRevision
	}
	switch {
	case e.Help != nil:
		return v.drawHelp(e, out)
	case e.Menu != nil:
		return v.drawMenu(e, out)
	case e.Locations != nil:
		return v.drawLocations(e, out)
	case e.Picker != nil:
		return v.drawPicker(e, out)
	case e.Explorer != nil:
		return v.drawExplorer(e, out)
	}
	if e.IsWelcome() {
		lines := make([]string, h)
		content := []string{
			"Coigüe",
			"Welcome",
			"Directory: " + fsx.Display(e.Welcome),
			"Ctrl-O / - / :explore   Browse and create files",
			"Ctrl-P / :open         Find a file",
			"F1 / :help            Getting started and key guide",
			":q                     Quit",
		}
		available := max(h-1, 0)
		start := max(available-len(content), 0) / 2
		for i, text := range content {
			if row := start + i; row < start+available && row < len(lines) {
				lines[row] = text
			}
		}
		switch {
		case e.Mode == editor.ModeCommand:
			lines[h-1] = ":" + e.Command
		case e.Message != "":
			lines[h-1] = e.Message
		default:
			lines[h-1] = "F1 help | Ctrl-O browse | Ctrl-P find | :q quit"
		}
		return v.drawBrowserLines(lines, e, out)
	}
	return v.drawBuffer(e, w, h, out)
}

func (v *View) drawBuffer(e *editor.Editor, w, h int, out io.Writer) error {
	palette := e.Palette()
	rows := max(h-2, 0)
	b := e.Buffer
	lines := b.Lines()
	// Keep at least one text cell on narrow terminals; omit the gutter if necessary.
	numberWidth := len(strconv.Itoa(len(lines))) + 1
	gutter := 0
	if w > numberWidth {
		gutter = numberWidth
	}
	textWidth := w - gutter
	if b.Row < v.top {
		v.top = b.Row
	} else if b.Row >= v.top+max(rows, 1) {
		v.top = b.Row + 1 - max(rows, 1)
	}
	col := lineCells(lines[b.Row][:min(b.Col, len(lines[b.Row]))])
	if col < v.left {
		v.left = col
	} else if col >= v.left+textWidth {
		v.left = col + 1 - textWidth
	}
	revision := [2]uint64{b.Revision(), e.File.Revision()}
	pathChanged := v.syntaxPath == nil || *v.syntaxPath != e.File.Path
	semanticChanged := v.semanticRevision != e.LSP.SemanticRevision
	if v.revision == nil || *v.revision != revision || pathChanged || semanticChanged {
		if v.revision == nil || v.revision[0] != revision[0] || pathChanged || semanticChanged {
			path := e.File.Path
			v.syntaxPath = &path
			v.styles = syntax.ForPath(path, lines)
			if v.styles != nil {
				applySemantic(v.styles, e.LSP.SemanticTokens)
			}
			v.semanticRevision = e.LSP.SemanticRevision
		}
		v.dirty = e.File.Dirty(b.Text())
		v.revision = &revision
	}
	frame := make([][]byte, h)
	for y := 0; y < rows; y++ {
		lineOut := &frame[y]
		if v.top+y >= len(lines) {
			*lineOut = append(*lineOut, '~')
			continue
		}
		line := lines[v.top+y]
		if gutter > 0 {
			severity, hasSeverity := e.LSP.LineSeverity(v.top + y)
			number := strconv.Itoa(v.top + y + 1)
			if pad := gutter - 1 - len(number); pad > 0 {
				number = strings.Repeat(" ", pad) + number
			}
			if !hasSeverity {
				number += " "
			}
			*lineOut = append(*lineOut, v.noColor.foreground(palette.Muted)...)
			*lineOut = append(*lineOut, number...)
			if hasSeverity {
				letter := "H"
				switch severity {
				case 1:
					letter = "E"
				case 2:
					letter = "W"
				case 3:
					letter = "I"
				}
				*lineOut = append(*lineOut, v.noColor.foreground(palette.Diagnostic(severity))...)
				*lineOut = append(*lineOut, letter...)
			}
			*lineOut = append(*lineOut, v.noColor.foreground(palette.Foreground)...)
		}
		selection := selectionOf(e, v.top+y)
		switch {
		case v.styles != nil:
			styledLine(lineOut, line, v.styles[v.top+y], v.left, textWidth, palette, selection, v.noColor)
		case selection != nil:
			styledLine(lineOut, line, make([]syntax.Style, len(line)), v.left, textWidth, palette, selection, v.noColor)
		default:
			*lineOut = append(*lineOut, clipped(line, v.left, textWidth)...)
		}
	}

	dirtyMark := ""
	if v.dirty {
		dirtyMark = " [+]"
	}
	strip := actions.Strip
	if w < 60 {
		strip = "F2 Files F3 Code F1 Help"
	}
	status := strip + " | " + e.Mode.String() + " " + fsx.Display(e.File.Path) + dirtyMark +
		"  " + strconv.Itoa(b.Row+1) + ":" + strconv.Itoa(b.Col+1)
	if e.LSP.Status != "" {
		errors, warnings := e.LSP.Counts()
		status += " | " + e.LSP.Status + " E:" + strconv.Itoa(errors) + " W:" + strconv.Itoa(warnings)
	}
	statusRunes := []rune(status)
	if h >= 2 {
		frame[h-2] = append(frame[h-2], clipped(statusRunes, 0, w)...)
	}
	var message string
	switch {
	case e.Mode == editor.ModeCommand:
		message = ":" + e.Command
	case e.Mode == editor.ModeSearch:
		message = "/" + e.Search
	case e.PendingInput() != "":
		message = e.PendingInput()
	case e.Message == "":
		if text, ok := e.LSP.AtCursor(b.Row, b.Col); ok {
			message = text
		} else if text, ok := e.LSP.Failure(); ok {
			message = text
		}
	default:
		message = e.Message
	}
	promptMode := e.Mode == editor.ModeCommand || e.Mode == editor.ModeSearch
	promptWidth := textCells(message)
	promptLeft := 0
	if promptMode {
		promptLeft = max(promptWidth+1-w, 0)
	}
	messageRunes := []rune(message)
	frame[h-1] = append(frame[h-1], clipped(messageRunes, promptLeft, w)...)
	var x, y int
	if promptMode {
		x, y = min(max(promptWidth-promptLeft, 0), w-1), h-1
	} else {
		x = min(gutter+col-v.left, w-1)
		y = min(b.Row-v.top, max(rows-1, 0))
	}
	// Assemble the entire update before touching the terminal. Unchanged rows
	// stay in place; synchronized terminals display changed rows atomically.
	if equalFrames(v.previous, frame) && v.cursor != nil && *v.cursor == [2]int{x, y} {
		return nil
	}
	var update []byte
	update = append(update, term.BeginSynchronizedUpdate+term.Hide...)
	for row, line := range frame {
		if row < len(v.previous) && bytes.Equal(v.previous[row], line) {
			continue
		}
		rowColors(&update, v.noColor, palette, h >= 2 && row == h-2)
		update = append(update, term.MoveTo(0, row)...)
		update = append(update, line...)
		// Reset a possible pending wrap before clearing the unused tail.
		// A full-width line needs no clearing.
		var cells int
		switch {
		case row < rows:
			if v.top+row >= len(lines) {
				cells = 1
			} else {
				text := lines[v.top+row]
				endCell := lineCells(text)
				selectedEnd := 0
				if sel := selectionOf(e, v.top+row); sel != nil && sel.contains(len(text)) &&
					endCell >= v.left && endCell < v.left+textWidth {
					selectedEnd = 1
				}
				cells = gutter + selectedEnd + visibleCells(clipped(text, v.left, textWidth))
			}
		case h >= 2 && row == h-2:
			cells = visibleCells(clipped(statusRunes, 0, w))
		default:
			cells = visibleCells(clipped(messageRunes, promptLeft, w))
		}
		if cells < w {
			update = append(update, term.MoveTo(cells, row)...)
			update = append(update, term.ClearUntilNewLine...)
		}
	}
	rowColors(&update, v.noColor, palette, false)
	update = append(update, term.MoveTo(x, y)...)
	update = append(update, term.Show+term.EndSynchronizedUpdate...)
	if _, err := out.Write(update); err != nil {
		return err
	}
	v.previous = frame
	v.cursor = &[2]int{x, y}
	return nil
}

func selectionOf(e *editor.Editor, row int) *selectionRange {
	start, end, ok := e.Selection(row)
	if !ok {
		return nil
	}
	return &selectionRange{start, end}
}

// applySemantic overlays server-provided token styles on the lexical ones.
func applySemantic(styles [][]syntax.Style, tokens []semantic.Token) {
	for _, token := range tokens {
		if token.Row >= len(styles) {
			continue
		}
		line := styles[token.Row]
		if token.Start > token.End || token.End > len(line) {
			continue
		}
		for i := token.Start; i < token.End; i++ {
			line[i] = token.Style
		}
	}
}
