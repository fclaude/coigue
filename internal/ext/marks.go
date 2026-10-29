package ext

import (
	"errors"
	"fmt"
	"runtime"
	"weak"

	"github.com/fclaude/brote/scheme"

	"github.com/fclaude/coigue/internal/buffer"
	"github.com/fclaude/coigue/internal/marks"
)

// Marks. A mark is a place in the text that stays with the text it was made on
// while the rest changes: made in a command, used by a handler seconds later,
// after the person typed above it. A mark is a position and the text that
// position refers to; moving it to the current text is a comparison of the two
// (package marks), so no edit operation needs to know about marks.
//
// Inside a command the mark is resolved against the command's own copy of the
// buffer, so an insert at one mark does not put the next mark in the wrong
// place. Between commands the runtime keeps every live mark on the live text:
// SyncMarks moves them after each change, and marks nothing refers to any more
// are collected.

// MaxMarks bounds the marks alive at once.
const MaxMarks = 256

// textSnap is a copy of a text that marks refer to. It is never changed, so
// many marks share one.
type textSnap struct{ lines [][]rune }

// markObj is the native value (*scheme.Object) of a mark.
type markObj struct {
	row, col int       // 0-based, in base
	base     *textSnap // the text row and col refer to; nil while it is the live text of a command that just ended
	// baseRev is the revision of the command's buffer base was copied at, for a
	// mark made in a command that has not ended.
	baseRev   uint64
	session   *Session // the command that made it, until the editor adopts it
	disturbed bool     // text around it was replaced since it was made
	dead      bool     // its file is no longer open
}

func (*markObj) Kind() string   { return "mark" }
func (*markObj) String() string { return "#<mark>" }

// tracker keeps the marks of a runtime on the live text.
type tracker struct {
	snap    *textSnap
	rev     uint64
	gen     uint64
	live    []weak.Pointer[markObj]
	pending []*markObj // adopted, waiting for the live text to be synced
}

func (t *tracker) prune() []*markObj {
	var out []*markObj
	kept := t.live[:0]
	for _, w := range t.live {
		if m := w.Value(); m != nil {
			out = append(out, m)
			kept = append(kept, w)
		}
	}
	t.live = kept
	return out
}

// HasMarks reports whether any mark is waiting to be kept on the live text.
func (r *Runtime) HasMarks() bool {
	return len(r.tracker.live) > 0 || len(r.tracker.pending) > 0
}

// SyncMarks moves every mark to the live text, which is lines at revision
// rev of the file generation gen. The editor calls it after anything that can
// change the text, and it costs nothing when the revision has not moved.
func (r *Runtime) SyncMarks(lines [][]rune, rev, gen uint64) {
	t := &r.tracker
	if t.snap == nil && len(t.pending) == 0 {
		return
	}
	if t.snap != nil && t.rev == rev && t.gen == gen && len(t.pending) == 0 {
		return
	}
	if t.snap != nil && t.gen != gen {
		// Another file: every mark was about text that is gone.
		for _, m := range t.prune() {
			m.dead = true
		}
		t.live, t.snap = nil, nil
	}
	snap := &textSnap{lines: buffer.CloneLines(lines)}
	if t.snap != nil && t.rev != rev {
		mapper := marks.NewMapper(t.snap.lines, lines)
		for _, m := range t.prune() {
			q, exact := mapper.Map(marks.Pos{Row: m.row, Col: m.col})
			m.row, m.col = q.Row, q.Col
			m.disturbed = m.disturbed || !exact
		}
	}
	for _, m := range t.prune() {
		m.base = snap
	}
	for _, m := range t.pending {
		m.base = snap
		t.live = append(t.live, weak.Make(m))
	}
	t.pending = nil
	t.snap, t.rev, t.gen = snap, rev, gen
	if len(t.live) == 0 {
		t.snap = nil
	}
}

var (
	errMarkDead    = errors.New("that mark was made in a file that is no longer open")
	errMarkOrphan  = errors.New("that mark was made by a command that did not finish")
	errNotAMark    = errors.New("expected a mark made by mark-at")
	errMarkCapFull = fmt.Errorf("at most %d marks can be alive at once", MaxMarks)
)

func markArg(c *scheme.Call, i int) (*markObj, error) {
	m, ok := scheme.ObjectOf(c.Args[i]).(*markObj)
	if !ok {
		return nil, fmt.Errorf("argument %d: %w; got %s", i+1, errNotAMark, scheme.Write(c.Args[i]))
	}
	return m, nil
}

// snapshot is a copy of the command's buffer as it is now, shared by the marks
// made at this revision.
func (s *Session) snapshot() *textSnap {
	if s.snap == nil || s.snapRev != s.Buffer.Revision() {
		s.snap = &textSnap{lines: buffer.CloneLines(s.Buffer.Lines())}
		s.snapRev = s.Buffer.Revision()
	}
	return s.snap
}

// resolve is where the mark is in the command's buffer now, and whether that is
// the same place or the nearest one to text that was replaced.
func (s *Session) resolve(m *markObj) (marks.Pos, bool, error) {
	switch {
	case m.dead:
		return marks.Pos{}, false, errMarkDead
	case m.session != nil && m.session != s:
		return marks.Pos{}, false, errMarkOrphan
	}
	p := marks.Pos{Row: m.row, Col: m.col}
	if m.session == s {
		if m.baseRev == s.Buffer.Revision() {
			return p, !m.disturbed, nil
		}
	} else if m.base == nil || s.Buffer.Revision() == 0 {
		// Made in a command that ended, before the live text was synced, or on
		// the live text with nothing changed since the command began.
		return p, !m.disturbed, nil
	}
	mapper := s.mapperFor(m.base)
	q, exact := mapper.Map(p)
	return q, exact && !m.disturbed, nil
}

func (s *Session) mapperFor(base *textSnap) *marks.Mapper {
	if s.mappers == nil {
		s.mappers = map[*textSnap]*cachedMapper{}
	}
	c := s.mappers[base]
	if c == nil || c.rev != s.Buffer.Revision() {
		c = &cachedMapper{rev: s.Buffer.Revision(), m: marks.NewMapper(base.lines, s.Buffer.Lines())}
		s.mappers[base] = c
	}
	return c.m
}

type cachedMapper struct {
	rev uint64
	m   *marks.Mapper
}

func (r *Runtime) markAt(c *scheme.Call, s *Session) (scheme.Value, error) {
	row, err := lineArg(c, s, 0)
	if err != nil {
		return scheme.Value{}, err
	}
	col, err := columnArg(c, s, 1, row-1)
	if err != nil {
		return scheme.Value{}, err
	}
	if r.markCount()+len(s.newMarks) >= MaxMarks {
		runtime.GC()
		if r.markCount()+len(s.newMarks) >= MaxMarks {
			return scheme.Value{}, errMarkCapFull
		}
	}
	m := &markObj{row: row - 1, col: col - 1, base: s.snapshot(), baseRev: s.Buffer.Revision(), session: s}
	s.newMarks = append(s.newMarks, m)
	return scheme.ObjectValue(m), nil
}

func (r *Runtime) markCount() int { return len(r.tracker.prune()) + len(r.tracker.pending) }

// adoptMarks takes the marks a successful command made. Their places are moved
// to the command's final text, which is about to become the live text.
func (r *Runtime) adoptMarks(s *Session) {
	for _, m := range s.newMarks {
		p, exact, err := s.resolve(m)
		if err != nil {
			continue
		}
		m.row, m.col = p.Row, p.Col
		m.disturbed = m.disturbed || !exact
		m.session, m.base = nil, nil
		r.tracker.pending = append(r.tracker.pending, m)
	}
	s.newMarks = nil
}

func markOps(r *Runtime) []*scheme.Prim {
	return []*scheme.Prim{
		inCommand("mark-at", "A mark at LINE and COLUMN, 1-based, that stays with the text around it as the buffer changes, so a handler that runs later can still find the place. At most 256 are alive at once; marks nothing refers to any more are freed. Text typed exactly at a mark goes after it.",
			[]scheme.Param{
				{Name: "line", Type: "integer", Doc: "the line"},
				{Name: "column", Type: "integer", Doc: "the column; one past the end of a line is allowed"},
			},
			r.markAt),
		inCommand("mark-line", "The 1-based line a mark is on now.",
			[]scheme.Param{{Name: "mark", Type: "any", Doc: "a mark from mark-at"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				m, err := markArg(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				p, _, err := s.resolve(m)
				return scheme.Int(int64(p.Row + 1)), err
			}),
		inCommand("mark-column", "The 1-based column a mark is on now.",
			[]scheme.Param{{Name: "mark", Type: "any", Doc: "a mark from mark-at"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				m, err := markArg(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				p, _, err := s.resolve(m)
				return scheme.Int(int64(p.Col + 1)), err
			}),
		inCommand("mark-disturbed?", "True if the text the mark was on has been replaced since it was made, so it only stands for the nearest place. A mark that only moved with its text is not disturbed.",
			[]scheme.Param{{Name: "mark", Type: "any", Doc: "a mark from mark-at"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				m, err := markArg(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				_, exact, err := s.resolve(m)
				return scheme.Bool(!exact), err
			}),
		inCommand("insert-at-mark", "Insert TEXT at a mark, as insert-at does at a line and column. The cursor ends on the first character of TEXT. Ends the selection.",
			[]scheme.Param{
				{Name: "mark", Type: "any", Doc: "a mark from mark-at"},
				{Name: "text", Type: "string", Doc: "the text to insert"},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				m, err := markArg(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				text, err := c.String(1)
				if err != nil {
					return scheme.Value{}, err
				}
				p, _, err := s.resolve(m)
				if err != nil {
					return scheme.Value{}, err
				}
				runes, err := s.checkText(text)
				if err != nil {
					return scheme.Value{}, err
				}
				at, _ := s.Buffer.LineSpan(p.Row, p.Row)
				at += p.Col
				s.Buffer.ReplaceSpan(at, at, runes)
				s.edited = true
				s.inserted += len(runes)
				return scheme.Void, nil
			}),
		inCommand("replace-at-mark", "Replace the text EXPECTED, which must be exactly what is at a mark now, with TEXT. If the text there is something else, for example because the word was edited since the mark was made, it is an error that says what is there and nothing changes. TEXT may be empty, which deletes EXPECTED. The cursor ends on the first character of TEXT. Ends the selection.",
			[]scheme.Param{
				{Name: "mark", Type: "any", Doc: "a mark from mark-at"},
				{Name: "expected", Type: "string", Doc: "the text that should be at the mark, not empty"},
				{Name: "text", Type: "string", Doc: "what to put there"},
			},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				m, err := markArg(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				expected, err := c.String(1)
				if err != nil {
					return scheme.Value{}, err
				}
				text, err := c.String(2)
				if err != nil {
					return scheme.Value{}, err
				}
				want, err := s.checkText(expected)
				if err != nil {
					return scheme.Value{}, err
				}
				if len(want) == 0 {
					return scheme.Value{}, errors.New("expected is the text to replace, so it cannot be empty; use insert-at-mark to add text")
				}
				runes, err := s.checkText(text)
				if err != nil {
					return scheme.Value{}, err
				}
				p, _, err := s.resolve(m)
				if err != nil {
					return scheme.Value{}, err
				}
				start, _ := s.Buffer.LineSpan(p.Row, p.Row)
				start += p.Col
				if got := s.Buffer.Span(start, start+len(want)); string(got) != string(want) {
					return scheme.Value{}, fmt.Errorf("the text at the mark is %s, not %s", shown(got), shown(want))
				}
				s.Buffer.ReplaceSpan(start, start+len(want), runes)
				s.edited = true
				s.inserted += len(runes)
				return scheme.Void, nil
			}),
		inCommand("goto-mark", "Move the cursor to a mark, as set-cursor does. It does not end the selection.",
			[]scheme.Param{{Name: "mark", Type: "any", Doc: "a mark from mark-at"}},
			func(c *scheme.Call, s *Session) (scheme.Value, error) {
				m, err := markArg(c, 0)
				if err != nil {
					return scheme.Value{}, err
				}
				p, _, err := s.resolve(m)
				if err != nil {
					return scheme.Value{}, err
				}
				s.Buffer.Row, s.Buffer.Col = p.Row, p.Col
				s.Buffer.Clamp(false)
				s.moved = true
				return scheme.Void, nil
			}),
	}
}

// shown quotes a few characters of text for an error message.
func shown(text []rune) string {
	const most = 40
	if len(text) > most {
		return fmt.Sprintf("%q...", string(text[:most]))
	}
	return fmt.Sprintf("%q", string(text))
}
