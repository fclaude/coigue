package ext

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/brote/scheme"
)

// lineArg reads argument i as a 1-based line number of the session's buffer.
func lineArg(c *scheme.Call, s *Session, i int) (int, error) {
	n, err := c.Int(i)
	if err != nil {
		return 0, err
	}
	if count := s.Buffer.LineCount(); n < 1 || n > int64(count) {
		return 0, fmt.Errorf("line %d is outside 1..%d", n, count)
	}
	return int(n), nil
}

// columnArg reads argument i as a 1-based column of the line at row. One past
// the last character is allowed, which is where text is appended.
func columnArg(c *scheme.Call, s *Session, i, row int) (int, error) {
	n, err := c.Int(i)
	if err != nil {
		return 0, err
	}
	if length := len(s.Buffer.Lines()[row]); n < 1 || n > int64(length)+1 {
		return 0, fmt.Errorf("column %d is outside 1..%d on line %d", n, length+1, row+1)
	}
	return int(n), nil
}

// lineRange reads arguments first and first+1 as the lines of a range.
func lineRange(c *scheme.Call, s *Session, first int) (start, end int, err error) {
	if start, err = lineArg(c, s, first); err != nil {
		return 0, 0, err
	}
	if end, err = lineArg(c, s, first+1); err != nil {
		return 0, 0, err
	}
	if end < start {
		return 0, 0, fmt.Errorf("lines %d..%d: the end is before the start", start, end)
	}
	return start, end, nil
}

// readOperations are the questions a command can ask beyond the text it reads.
func readOperations() []*scheme.Prim {
	return []*scheme.Prim{
		inCommand("buffer-path", "The path of the file being edited, or \"\" when the buffer has no file.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Str(s.Path), nil
			}),
		inCommand("cursor-column", "The 1-based column of the cursor, counted in characters.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Int(int64(s.Buffer.Col + 1)), nil
			}),
		inCommand("mode", "The mode the command was started from: \"normal\", \"visual\" or \"visual-line\".", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Str(s.Mode), nil
			}),
		inCommand("selection-range", "The selection as the list (START-LINE START-COLUMN END-LINE END-COLUMN), 1-based and inclusive at both ends, or #f when there is no selection or an edit has ended it. A V selection runs from the first column of its first line to the last character of its last.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				if s.selection == nil || s.edited {
					return scheme.False, nil
				}
				b := s.Buffer
				a := s.selection.Anchor
				from, to := a, [2]int{b.Row, b.Col}
				if to[0] < from[0] || (to[0] == from[0] && to[1] < from[1]) {
					from, to = to, from
				}
				if s.selection.Linewise {
					from[1] = 0
					to[1] = max(len(b.Lines()[min(to[0], b.LineCount()-1)])-1, 0)
				}
				return scheme.List(
					scheme.Int(int64(from[0]+1)), scheme.Int(int64(from[1]+1)),
					scheme.Int(int64(to[0]+1)), scheme.Int(int64(to[1]+1))), nil
			}),
		inCommand("word-at-cursor", "The word under the cursor: the letters, digits and underscores around it on its line. It is \"\" when the cursor is on anything else.", nil,
			func(_ *scheme.Call, s *Session) (scheme.Value, error) {
				return scheme.Str(string(s.Buffer.WordAt())), nil
			}),
		inCommand("find", "The first place TEXT occurs at or after the position (LINE COLUMN), as the list (LINE COLUMN), or #f. The search is literal, case-sensitive and does not wrap; LINE and COLUMN default to the start of the buffer, and TEXT must be on one line.",
			[]scheme.Param{
				{Name: "text", Type: "string", Doc: "what to look for, not empty"},
				{Name: "line", Type: "integer", Doc: "line to start on", Optional: true},
				{Name: "column", Type: "integer", Doc: "column to start at on that line", Optional: true},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				text, err := c.String(0)
				if err != nil {
					return scheme.Value{}, err
				}
				switch {
				case text == "":
					return scheme.Value{}, fmt.Errorf("there is nothing to look for in an empty string")
				case !utf8.ValidString(text):
					return scheme.Value{}, errTextNotUTF8
				case strings.Contains(text, "\n"):
					return scheme.Value{}, fmt.Errorf("text must be on one line")
				case utf8.RuneCountInString(text) > MaxRegion:
					return scheme.Value{}, fmt.Errorf("text is longer than %d characters", MaxRegion)
				}
				row, col := 0, 0
				if len(c.Args) > 1 {
					if row, err = lineArg(c, s, 1); err != nil {
						return scheme.Value{}, err
					}
					row--
					if len(c.Args) > 2 {
						if col, err = columnArg(c, s, 2, row); err != nil {
							return scheme.Value{}, err
						}
						col--
					}
				}
				lines := s.Buffer.Lines()
				for ; row < len(lines); row, col = row+1, 0 {
					if col > len(lines[row]) {
						continue
					}
					line := string(lines[row])
					from := len(string(lines[row][:col]))
					if at := strings.Index(line[from:], text); at >= 0 {
						return scheme.List(
							scheme.Int(int64(row+1)),
							scheme.Int(int64(col+utf8.RuneCountInString(line[from:from+at])+1))), nil
					}
				}
				return scheme.False, nil
			}),
	}
}

// editOperations change the command's copy of the buffer beyond the selection.
// Line numbers and columns always mean the buffer as it is when the operation
// runs, so an edit that adds or removes lines moves the ones after it.
func editOperations() []*scheme.Prim {
	return []*scheme.Prim{
		inCommand("replace-lines", "Replace lines START to END, 1-based and inclusive, with TEXT, which may hold any number of lines; \"\" leaves one empty line. The cursor ends on the first character of TEXT. Ends the selection.",
			[]scheme.Param{
				{Name: "start", Type: "integer", Doc: "first line to replace"},
				{Name: "end", Type: "integer", Doc: "last line to replace"},
				{Name: "text", Type: "string", Doc: "the replacement"},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				start, end, err := lineRange(c, s, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				text, err := c.String(2)
				if err != nil {
					return scheme.Value{}, err
				}
				runes, err := s.checkText(text)
				if err != nil {
					return scheme.Value{}, err
				}
				from, to := s.Buffer.LineSpan(start-1, end-1)
				s.Buffer.ReplaceSpan(from, to, runes)
				s.edited = true
				s.inserted += len(runes)
				return scheme.Void, nil
			}),
		inCommand("delete-lines", "Delete lines START to END, 1-based and inclusive. Deleting every line leaves one empty line. The cursor ends on the first column of the line that took the place of the first deleted one, or of the last line. Ends the selection.",
			[]scheme.Param{
				{Name: "start", Type: "integer", Doc: "first line to delete"},
				{Name: "end", Type: "integer", Doc: "last line to delete"},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				start, end, err := lineRange(c, s, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				b := s.Buffer
				from, to := b.LineSpan(start-1, end-1)
				switch {
				case end < b.LineCount():
					to++ // the newline after the last deleted line
				case start > 1:
					from-- // the newline before the first deleted line
				}
				b.ReplaceSpan(from, to, nil)
				b.Row, b.Col = min(start-1, b.LineCount()-1), 0
				s.edited = true
				return scheme.Void, nil
			}),
		inCommand("insert-at", "Insert TEXT before the character at LINE and COLUMN, 1-based; the column one past the end of a line appends to it. TEXT may hold several lines. The cursor ends on the first character of TEXT. Ends the selection.",
			[]scheme.Param{
				{Name: "line", Type: "integer", Doc: "line to insert into"},
				{Name: "column", Type: "integer", Doc: "column to insert before"},
				{Name: "text", Type: "string", Doc: "the text to insert"},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				row, err := lineArg(c, s, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				col, err := columnArg(c, s, 1, row-1)
				if err != nil {
					return scheme.Value{}, err
				}
				text, err := c.String(2)
				if err != nil {
					return scheme.Value{}, err
				}
				runes, err := s.checkText(text)
				if err != nil {
					return scheme.Value{}, err
				}
				at, _ := s.Buffer.LineSpan(row-1, row-1)
				at += col - 1
				s.Buffer.ReplaceSpan(at, at, runes)
				s.edited = true
				s.inserted += len(runes)
				return scheme.Void, nil
			}),
		inCommand("set-cursor", "Move the cursor to LINE and COLUMN, 1-based; a column one past the end of a line puts it on the last character, as in Normal mode. It does not end the selection: the selection then runs from where it started to the new cursor.",
			[]scheme.Param{
				{Name: "line", Type: "integer", Doc: "the line"},
				{Name: "column", Type: "integer", Doc: "the column"},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				row, err := lineArg(c, s, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				col, err := columnArg(c, s, 1, row-1)
				if err != nil {
					return scheme.Value{}, err
				}
				s.Buffer.Row, s.Buffer.Col = row-1, col-1
				s.Buffer.Clamp(false)
				s.moved = true
				return scheme.Void, nil
			}),
	}
}
