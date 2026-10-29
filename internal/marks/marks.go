// Package marks moves positions in a text to where the same place is after
// the text changed. It is how a position that must outlive an edit (a mark)
// finds its way, without every edit operation of the buffer having to know
// about marks: compare the text before and after.
//
// Lines the change left alone keep their place exactly. Inside the lines it
// did change, a position is moved by characters: before the changed part it
// stays, after it it shifts by the change in length, and inside it falls to
// the start of the change.
package marks

// Pos is a row and a column, both 0-based. The column counts runes and may be
// one past the end of the line.
type Pos struct{ Row, Col int }

// maxCells bounds the work of aligning lines: (old lines + 1) times (new lines
// + 1) after the unchanged lines at both ends were set aside. A bigger change
// is treated as one block, which is coarser but never slow.
const maxCells = 4_000_000

// block is a run of old lines [os, oe) that became the new lines [ns, ne).
type block struct{ os, oe, ns, ne int }

// Mapper maps positions in the old text to the new.
type Mapper struct {
	old, new [][]rune
	pl, sl   int         // lines the two texts share at the start and at the end
	kept     map[int]int // old row -> new row, for equal lines between changes
	blocks   []block     // the changes, in order
}

func equal(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NewMapper compares the two texts. old and new are not modified or kept
// beyond the Mapper's use, so they must not change while it is in use.
func NewMapper(old, new [][]rune) *Mapper {
	m := &Mapper{old: old, new: new}
	n := min(len(old), len(new))
	for m.pl < n && equal(old[m.pl], new[m.pl]) {
		m.pl++
	}
	for m.sl < n-m.pl && equal(old[len(old)-1-m.sl], new[len(new)-1-m.sl]) {
		m.sl++
	}
	oldEnd, newEnd := len(old)-m.sl, len(new)-m.sl
	if m.pl == oldEnd && m.pl == newEnd {
		return m
	}
	if (oldEnd-m.pl+1)*(newEnd-m.pl+1) > maxCells {
		m.blocks = []block{{m.pl, oldEnd, m.pl, newEnd}}
		return m
	}
	m.align(oldEnd, newEnd)
	return m
}

// align finds the lines the middle of the two texts have in common (a longest
// common subsequence) and records what lies between them as blocks.
func (m *Mapper) align(oldEnd, newEnd int) {
	a, b := m.old[m.pl:oldEnd], m.new[m.pl:newEnd]
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if equal(a[i], b[j]) {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	m.kept = map[int]int{}
	var open *block
	flush := func() {
		if open != nil {
			m.blocks = append(m.blocks, *open)
			open = nil
		}
	}
	touch := func(i, j int) {
		if open == nil {
			open = &block{os: m.pl + i, oe: m.pl + i, ns: m.pl + j, ne: m.pl + j}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && equal(a[i], b[j]):
			flush()
			m.kept[m.pl+i] = m.pl + j
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			touch(i, j)
			i++
			open.oe = m.pl + i
		default:
			touch(i, j)
			j++
			open.ne = m.pl + j
		}
	}
	flush()
}

func flatten(lines [][]rune) []rune {
	var out []rune
	for i, l := range lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, l...)
	}
	return out
}

// Map returns where the position is in the new text. exact is false when the
// position lay inside text the change replaced, so the answer is the nearest
// place and not the same place.
func (m *Mapper) Map(p Pos) (q Pos, exact bool) {
	q, exact = m.locate(p)
	// Keep the result inside the new text.
	q.Row = max(min(q.Row, len(m.new)-1), 0)
	if len(m.new) > 0 {
		q.Col = max(min(q.Col, len(m.new[q.Row])), 0)
	} else {
		q.Col = 0
	}
	return q, exact
}

func (m *Mapper) locate(p Pos) (Pos, bool) {
	r := p.Row
	switch {
	case r < m.pl:
		return p, true
	case r >= len(m.old)-m.sl:
		return Pos{r + len(m.new) - len(m.old), p.Col}, true
	}
	if nr, ok := m.kept[r]; ok {
		return Pos{nr, p.Col}, true
	}
	for _, b := range m.blocks {
		if r >= b.os && r < b.oe {
			return m.inBlock(b, p)
		}
	}
	return p, true
}

// inBlock maps a position inside a changed block by characters: the lines of
// the block on each side are joined, and what they share at the start and end
// is the part the change left alone.
func (m *Mapper) inBlock(b block, p Pos) (Pos, bool) {
	oldFlat, newFlat := flatten(m.old[b.os:b.oe]), flatten(m.new[b.ns:b.ne])
	offset := min(p.Col, len(m.old[p.Row]))
	for k := b.os; k < p.Row; k++ {
		offset += len(m.old[k]) + 1
	}
	pre := 0
	for pre < min(len(oldFlat), len(newFlat)) && oldFlat[pre] == newFlat[pre] {
		pre++
	}
	suf := 0
	for suf < min(len(oldFlat), len(newFlat))-pre && oldFlat[len(oldFlat)-1-suf] == newFlat[len(newFlat)-1-suf] {
		suf++
	}
	var at int
	exact := true
	switch {
	case offset <= pre:
		at = offset
	case offset >= len(oldFlat)-suf:
		at = offset + len(newFlat) - len(oldFlat)
	default:
		at, exact = pre, false
	}
	at = max(min(at, len(newFlat)), 0)
	for k := b.ns; k < b.ne; k++ {
		if at <= len(m.new[k]) {
			return Pos{k, at}, exact
		}
		at -= len(m.new[k]) + 1
	}
	// The block has no new lines: the text after the deletion is where it
	// was, or the end of the text when nothing follows.
	if b.ns >= len(m.new) {
		last := len(m.new) - 1
		return Pos{last, len(m.new[last])}, false
	}
	return Pos{b.ns, 0}, false
}
