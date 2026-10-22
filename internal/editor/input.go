package editor

import (
	"strconv"

	"github.com/fclaude/coigue/internal/buffer"
	"github.com/fclaude/coigue/internal/clipboard"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/reflow"
)

func popRune(s string) string {
	runes := []rune(s)
	if len(runes) == 0 {
		return s
	}
	return string(runes[:len(runes)-1])
}

// keyInner is the mode-level key handler. Its order is the editor's input
// precedence: reset completion, confirmations, browsers, global shortcuts,
// remapped movement, Esc, then the handler of the current mode.
func (e *Editor) keyInner(k key.Key, pageRows int) bool {
	if k.Code != key.Tab && k.Code != key.BackTab {
		e.commandCompletion = nil
	}
	if e.SwitchPrompt != "" {
		e.switchKey(k)
		return false
	}
	if e.FilesystemPrompt != nil {
		e.filesystemKey(k)
		return false
	}
	if k.Mods == key.Ctrl && k.Code == 'o' && e.Picker == nil && (e.Mode == ModeNormal || e.Mode == ModeInsert) {
		if e.Explorer != nil {
			e.closeExplorer()
		} else {
			e.openExplorer()
		}
		return false
	}
	if e.Explorer != nil && e.Picker == nil {
		e.explorerKey(k, pageRows)
		return false
	}
	if e.Picker != nil {
		e.pickerKey(k, pageRows)
		return false
	}
	if k.Mods == key.Ctrl && k.Code == 'p' && (e.Mode == ModeNormal || e.Mode == ModeInsert) {
		e.openPicker()
		return false
	}
	if e.IsWelcome() && e.Mode == ModeNormal {
		if k.Mods == 0 {
			switch k.Code {
			case '-':
				e.openExplorer()
			case ':':
				e.Command = ""
				e.Mode = ModeCommand
			}
		}
		return false
	}
	if e.Mode == ModeNormal && k.Mods == key.Ctrl && k.Code == 'r' {
		count, _ := e.takeCount()
		e.resetPending()
		changed := false
		for i := 0; i < count; i++ {
			if !e.Buffer.Redo() {
				break
			}
			changed = true
		}
		if changed {
			e.Message = "Redone"
		} else {
			e.Message = "Nothing to redo"
		}
		return false
	}
	if k.Mods == key.Ctrl {
		switch k.Code {
		case 'a':
			k.Code = key.Home
		case 'e':
			k.Code = key.End
		case 'f':
			k.Code = key.PageDown
		case 'b':
			k.Code = key.PageUp
		}
		switch k.Code {
		case key.Home, key.End, key.PageDown, key.PageUp:
			k.Mods = 0
		}
	}
	if k.Code == key.Esc {
		if e.Mode == ModeVisual || e.Mode == ModeVisualLine {
			e.Message = ""
		}
		if e.Mode == ModeInsert {
			e.Buffer.Col = max(e.Buffer.Col-1, 0)
		}
		e.Mode = ModeNormal
		e.selectionAnchor = nil
		e.resetPending()
		e.Command = ""
		e.Search = ""
		e.Buffer.Clamp(false)
		return false
	}
	if ctrlAltSuper(k.Mods) {
		return false
	}
	if e.Mode == ModeSearch {
		switch {
		case k.Code.IsChar():
			e.Search += string(k.Code.Rune())
		case k.Code == key.Backspace:
			e.Search = popRune(e.Search)
		case k.Code == key.Enter:
			query := e.Search
			e.Search = ""
			if query != "" {
				e.lastSearch = query
			}
			e.Mode = ModeNormal
			e.find(true)
		}
		return false
	}
	if e.Mode == ModeCommand {
		switch {
		case k.Code.IsChar():
			e.Command += string(k.Code.Rune())
		case k.Code == key.Tab || k.Code == key.BackTab:
			backwards := k.Code == key.BackTab || k.Mods.Has(key.Shift)
			if e.commandCompletion != nil {
				e.commandCompletion.cycle(backwards)
			} else {
				e.commandCompletion = e.commands.newCompletion(e.Command, backwards)
			}
			if c := e.commandCompletion; c != nil {
				e.Command = c.candidates[c.selected]
			}
		case k.Code == key.Backspace:
			e.Command = popRune(e.Command)
		case k.Code == key.Enter:
			cmd := e.Command
			e.Command = ""
			e.Mode = ModeNormal
			return e.execute(cmd)
		}
		return false
	}
	if e.Mode == ModeVisual || e.Mode == ModeVisualLine {
		e.visualKey(k.Code, pageRows)
		e.Buffer.Clamp(false)
		return false
	}
	if e.Mode == ModeNormal {
		e.normalKey(k.Code, pageRows)
		e.Buffer.Clamp(e.Mode == ModeInsert)
		return false
	}
	b := e.Buffer
	switch {
	case k.Code.IsChar():
		b.Insert(k.Code.Rune())
	case k.Code == key.Enter:
		b.Insert('\n')
	case k.Code == key.Tab:
		b.Insert('\t')
	case k.Code == key.Backspace:
		b.Backspace()
	}
	switch k.Code {
	case key.Home:
		b.Col = 0
	case key.End:
		b.Col = len(b.Lines()[b.Row])
	case key.PageUp:
		b.Row = max(b.Row-max(pageRows, 1), 0)
	case key.PageDown:
		b.Row = min(saturatingAdd(b.Row, max(pageRows, 1)), b.LineCount()-1)
	case key.Left:
		b.Col = max(b.Col-1, 0)
	case key.Right:
		b.Col++
	case key.Up:
		b.Row = max(b.Row-1, 0)
	case key.Down:
		b.Row = min(b.Row+1, b.LineCount()-1)
	}
	b.Clamp(e.Mode == ModeInsert)
	return false
}

const maxInt = int(^uint(0) >> 1)

func saturatingAdd(a, b int) int {
	if sum := a + b; sum >= a {
		return sum
	}
	return maxInt
}

func saturatingMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	if p := a * b; p/b == a && p >= 0 {
		return p
	}
	return maxInt
}

func (e *Editor) visualKey(code key.Code, pageRows int) {
	b := e.Buffer
	switch {
	case code == 'v' || code == 'V':
		mode := ModeVisual
		if code == 'V' {
			mode = ModeVisualLine
		}
		if e.Mode == mode {
			e.Mode = ModeNormal
			e.selectionAnchor = nil
			e.Message = ""
		} else {
			e.Mode = mode
		}
		e.resetPending()
	case code == 'o':
		anchor := e.selectionAnchor
		anchor[0], b.Row = b.Row, anchor[0]
		anchor[1], b.Col = b.Col, anchor[1]
		e.resetPending()
	case code == 'y' || code == 'Y' || code == 'd':
		op := code.Rune()
		anchor := *e.selectionAnchor
		e.selectionAnchor = nil
		yanked := true
		lines := b.Lines()
		if e.Mode == ModeVisualLine {
			start := min(anchor[0], b.Row)
			end := max(anchor[0], b.Row) + 1
			e.register = lineRegister(buffer.CloneLines(lines[start:end]), end < len(lines))
			b.Row = start
			b.Col = 0
			if op == 'd' {
				for i := start; i < end; i++ {
					b.Row = start
					b.DeleteLine()
				}
			}
		} else {
			chars := b.SelectionChars(anchor, op == 'd')
			yanked = len(chars) > 0
			if yanked {
				e.register = charRegister(chars)
			}
		}
		e.Mode = ModeNormal
		e.resetPending()
		if op == 'y' {
			e.Message = "Selection yanked"
		} else {
			e.Message = "Selection deleted"
		}
		if op == 'Y' {
			if yanked {
				e.copyClipboard()
			} else {
				e.Message = "Empty selection; clipboard unchanged, internal yank kept"
			}
		}
	case isVisualMotion(code):
		e.normalKey(code, pageRows)
	default:
		e.resetPending()
	}
}

func isVisualMotion(code key.Code) bool {
	if code.IsChar() {
		switch r := code.Rune(); {
		case r == 'h' || r == 'j' || r == 'k' || r == 'l' || r == 'w' || r == 'b' || r == 'e',
			r >= '0' && r <= '9', r == '$' || r == 'g' || r == 'G' || r == 'n' || r == 'N':
			return true
		}
		return false
	}
	switch code {
	case key.Left, key.Right, key.Up, key.Down, key.Home, key.End, key.PageUp, key.PageDown:
		return true
	}
	return false
}

func (e *Editor) normalKey(code key.Code, pageRows int) {
	b := e.Buffer
	// Replacement data must bypass digit parsing (e.g. 2r3).
	if e.pending == 'r' {
		count, _ := e.takeCount()
		e.resetPending()
		var ch rune
		switch {
		case code.IsChar():
			ch = code.Rune()
		case code == key.Tab:
			ch = '\t'
		default:
			return
		}
		start := b.Col
		line := b.Lines()[b.Row]
		for col := start; col < min(saturatingAdd(start, count), len(line)); col++ {
			b.Col = col
			b.Replace(ch)
		}
		b.Col = start
		return
	}
	if code.IsChar() {
		if r := code.Rune(); r >= '0' && r <= '9' && (r != '0' || e.count != 0) {
			e.count = min(e.count*10+int(r-'0'), 9999)
			return
		}
	}
	pending := e.pending
	e.pending = 0
	if code.IsChar() {
		switch op := code.Rune(); op {
		case 'd', 'c', 'y', 'g', 'r', '>', '<':
			if pending != op {
				count, explicit := e.takeCount()
				if explicit {
					e.operatorCount = count
				} else {
					e.operatorCount = 0
				}
				e.pending = op
				return
			}
		}
	}
	count, explicit := e.takeCount()
	var ch rune = -1
	if code.IsChar() {
		ch = code.Rune()
	}
	switch {
	case (pending == 'd' || pending == 'c' || pending == 'y') && ch == 'w':
		chars := b.WordRange(pending != 'y', pending == 'c', count)
		if len(chars) > 0 {
			e.register = charRegister(chars)
		}
		if pending == 'c' {
			e.Mode = ModeInsert
		}
	case (pending == 'd' || pending == 'y') && ch == pending:
		lines := b.Lines()
		start := b.Row
		end := min(saturatingAdd(start, count), len(lines))
		e.register = lineRegister(buffer.CloneLines(lines[start:end]), end < len(lines))
		if pending == 'd' {
			// Remove at the original row, never clamp backwards and delete earlier lines.
			for i := start; i < end; i++ {
				b.Row = start
				b.DeleteLine()
			}
		} else {
			e.Message = strconv.Itoa(end-start) + " line(s) copied"
		}
	case (pending == '>' || pending == '<') && ch == pending:
		start := b.Row
		originalCol := b.Col
		firstCol := originalCol
		for row := start; row < min(saturatingAdd(start, count), b.LineCount()); row++ {
			b.Row = row
			b.Col = originalCol
			b.Indent(pending == '>')
			if row == start {
				firstCol = b.Col
			}
		}
		b.Row = start
		b.Col = firstCol
	case pending == 'g' && ch == 'q':
		e.justify(e.textwidth)
	case (pending == 'g' && ch == 'g') || ch == 'G':
		var row int
		switch {
		case explicit:
			row = min(count-1, b.LineCount()-1)
		case ch == 'G':
			row = b.LineCount() - 1
		}
		e.rememberJump(row, 0)
		b.Row = row
		b.Col = 0
	case ch == 'h' || code == key.Left:
		b.Col = max(b.Col-count, 0)
	case ch == 'l' || code == key.Right:
		b.Col = saturatingAdd(b.Col, count)
	case ch == 'k' || code == key.Up:
		b.Row = max(b.Row-count, 0)
	case ch == 'j' || code == key.Down:
		b.Row = saturatingAdd(b.Row, count)
	case code == key.PageUp:
		b.Row = max(b.Row-saturatingMul(max(pageRows, 1), count), 0)
	case code == key.PageDown:
		b.Row = saturatingAdd(b.Row, saturatingMul(max(pageRows, 1), count))
	case code == key.Home || ch == '0':
		b.Col = 0
	case code == key.End || ch == '$':
		b.Col = max(len(b.Lines()[b.Row])-1, 0)
	case ch == 'w' || ch == 'b' || ch == 'e':
		for i := 0; i < count; i++ {
			beforeRow, beforeCol := b.Row, b.Col
			b.WordMotion(ch)
			if beforeRow == b.Row && beforeCol == b.Col {
				break
			}
		}
	case ch == 'v' || ch == 'V':
		e.selectionAnchor = &[2]int{b.Row, b.Col}
		if ch == 'v' {
			e.Mode = ModeVisual
		} else {
			e.Mode = ModeVisualLine
		}
		e.Message = "Visual: motions select | y yank | Y clipboard | d delete | Esc cancel"
	case ch == 'x':
		line := b.Lines()[b.Row]
		end := min(saturatingAdd(b.Col, count), len(line))
		chars := append([]rune(nil), line[b.Col:end]...)
		if len(chars) > 0 {
			for range chars {
				b.Delete()
			}
			e.register = charRegister(chars)
		}
	case ch == 'p' || ch == 'P':
		if e.register == nil {
			e.Message = "Nothing to paste"
			break
		}
		if e.register.isLine {
			var repeated [][]rune
			for i := 0; i < count; i++ {
				repeated = append(repeated, e.register.lines...)
			}
			b.PasteLines(repeated, ch == 'p', e.register.terminated)
		} else {
			var repeated []rune
			for i := 0; i < count; i++ {
				repeated = append(repeated, e.register.chars...)
			}
			b.PasteChars(repeated, ch == 'p')
		}
	case ch == 'u':
		changed := false
		for i := 0; i < count; i++ {
			if !b.Undo() {
				break
			}
			changed = true
		}
		if changed {
			e.Message = "Undone"
		} else {
			e.Message = "Nothing to undo"
		}
	case ch == 'n' || ch == 'N':
		for i := 0; i < count; i++ {
			e.find(ch == 'n')
		}
	case ch == 'A':
		b.Col = len(b.Lines()[b.Row])
		e.Mode = ModeInsert
	case ch == 'i':
		e.Mode = ModeInsert
	case ch == 'a':
		b.Col = min(b.Col+1, len(b.Lines()[b.Row]))
		e.Mode = ModeInsert
	case ch == 'o':
		b.OpenLine()
		e.Mode = ModeInsert
	case ch == '/':
		e.Search = ""
		e.Message = ""
		e.Mode = ModeSearch
	case ch == ':':
		e.Command = ""
		e.Mode = ModeCommand
	case ch == '-':
		e.openExplorer()
	}
}

func (e *Editor) find(forward bool) {
	if e.lastSearch == "" {
		e.Message = "No previous search; use / to find text"
		return
	}
	b := e.Buffer
	if row, col, ok := b.Find([]rune(e.lastSearch), forward); ok {
		var wrapped bool
		if forward {
			wrapped = !posLess([2]int{b.Row, b.Col}, [2]int{row, col})
		} else {
			wrapped = !posLess([2]int{row, col}, [2]int{b.Row, b.Col})
		}
		e.rememberJump(row, col)
		b.Row = row
		b.Col = col
		e.Message = "/" + e.lastSearch
		if wrapped {
			e.Message += " (wrapped)"
		}
	} else {
		e.Message = "Not found: " + e.lastSearch
	}
}

func (e *Editor) justify(width int) {
	switch {
	case e.IsWelcome():
		e.Message = "No file open"
	case e.Buffer.Justify(width, reflow.Paragraph):
		e.Message = "Paragraph filled to " + strconv.Itoa(width) + " columns"
	default:
		e.Message = "Paragraph unchanged (already filled or protected Markdown)"
	}
}

func (e *Editor) copyClipboard() {
	e.copyClipboardUsing(func(text string) error { return clipboard.Copy(e.ctx, text) })
}

func (e *Editor) registerText(r *register) string {
	if !r.isLine {
		return string(r.chars)
	}
	text := ""
	for i, line := range r.lines {
		if i > 0 {
			text += "\n"
		}
		text += string(line)
	}
	if r.terminated {
		text += "\n"
	}
	return text
}

func (e *Editor) copyClipboardUsing(copyFn func(string) error) {
	if e.register == nil {
		e.Message = "Nothing yanked; use y/yy or visual Y first"
		return
	}
	reg := e.register
	text := e.registerText(reg)
	if err := copyFn(text); err != nil {
		e.Message = "Clipboard copy failed; internal yank kept: " + err.Error()
		return
	}
	e.clipboardExport = &clipboardExport{text: text, reg: reg}
	e.Message = "Yank copied to system clipboard"
}

func (e *Editor) pasteClipboardResult(text string, err error, after bool) {
	switch {
	case err != nil:
		e.Message = "Clipboard paste failed: " + err.Error()
	case text == "":
		e.Message = "System clipboard is empty; no edit"
	default:
		if x := e.clipboardExport; x != nil && x.reg.isLine && x.text == text {
			e.Buffer.PasteLines(x.reg.lines, after, x.reg.terminated)
		} else {
			e.Buffer.PasteChars([]rune(text), after)
		}
		e.Buffer.Clamp(false)
		e.Message = "Pasted system clipboard (internal yank kept)"
	}
}
