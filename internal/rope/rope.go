// Package rope is a persistent sequence of lines: a balanced binary tree whose
// leaves hold up to leafLines lines each, and whose inner nodes remember how
// many lines, characters and UTF-8 bytes lie below them.
//
// Persistent means a Rope is never changed: Replace returns a new Rope that
// shares every subtree the edit did not touch with the old one. Keeping an old
// version (for undo) therefore costs one pointer, an edit costs a path from
// the root to the leaves it changes, and comparing two versions skips the
// subtrees they share.
//
// For example, with leaves of four lines:
//
//	           (12 lines)                      (12 lines)
//	          /          \                    /          \
//	    (8 lines)     [i j k l]   ->    (8 lines)'    [i j k l]   <- shared
//	    /       \                       /       \
//	[a b c d] [e f g h]             [a b c d] [e F g h]'           <- [a b c d] shared
//
// Replacing line f copies the leaf that holds it and the two nodes above it;
// the other two leaves are the same objects in both versions.
//
// Lines are []rune slices that are never modified once they are in a Rope;
// callers must not modify the slices Line and Lines return, and must not
// modify a slice after handing it to FromLines or Replace.
package rope

import "unicode/utf8"

// leafLines is the most lines a leaf built by FromLines or Replace holds.
const leafLines = 64

type node struct {
	left, right *node    // nil for a leaf
	lines       [][]rune // the lines of a leaf
	count       int      // lines below
	chars       int      // characters below, not counting line breaks
	bytes       int      // UTF-8 bytes below, not counting line breaks
	height      int      // 0 for a leaf
}

// Rope is a sequence of lines. The zero Rope has no lines.
type Rope struct{ root *node }

func lineBytes(line []rune) int {
	n := 0
	for _, r := range line {
		if uint32(r) < utf8.RuneSelf {
			n++
		} else if k := utf8.RuneLen(r); k > 0 {
			n += k
		} else {
			n += utf8.RuneLen(utf8.RuneError)
		}
	}
	return n
}

func newLeaf(lines [][]rune) *node {
	if len(lines) == 0 {
		return nil
	}
	n := &node{lines: lines[:len(lines):len(lines)], count: len(lines)}
	for _, line := range lines {
		n.chars += len(line)
		n.bytes += lineBytes(line)
	}
	return n
}

func height(n *node) int {
	if n == nil {
		return -1
	}
	return n.height
}

// pair makes an inner node over two non-nil subtrees.
func pair(l, r *node) *node {
	return &node{
		left: l, right: r,
		count:  l.count + r.count,
		chars:  l.chars + r.chars,
		bytes:  l.bytes + r.bytes,
		height: max(l.height, r.height) + 1,
	}
}

// balance makes an inner node over l and r, whose heights differ by at most
// two, rotating so that they differ by at most one.
func balance(l, r *node) *node {
	switch {
	case height(l) > height(r)+1:
		if height(l.left) >= height(l.right) {
			return pair(l.left, pair(l.right, r))
		}
		return pair(pair(l.left, l.right.left), pair(l.right.right, r))
	case height(r) > height(l)+1:
		if height(r.right) >= height(r.left) {
			return pair(pair(l, r.left), r.right)
		}
		return pair(pair(l, r.left.left), pair(r.left.right, r.right))
	}
	return pair(l, r)
}

// join concatenates two trees, either of which may be nil.
func join(l, r *node) *node {
	switch {
	case l == nil:
		return r
	case r == nil:
		return l
	case l.left == nil && r.left == nil && l.count+r.count <= leafLines:
		// Two small leaves become one, so edits do not leave the tree full
		// of one-line leaves.
		lines := make([][]rune, 0, l.count+r.count)
		return newLeaf(append(append(lines, l.lines...), r.lines...))
	case height(l) > height(r)+1:
		return balance(l.left, join(l.right, r))
	case height(r) > height(l)+1:
		return balance(join(l, r.left), r.right)
	}
	return pair(l, r)
}

// split divides n into its first k lines and the rest.
func split(n *node, k int) (*node, *node) {
	switch {
	case n == nil:
		return nil, nil
	case k <= 0:
		return nil, n
	case k >= n.count:
		return n, nil
	case n.left == nil:
		return newLeaf(n.lines[:k]), newLeaf(n.lines[k:])
	case k < n.left.count:
		a, b := split(n.left, k)
		return a, join(b, n.right)
	case k == n.left.count:
		return n.left, n.right
	}
	a, b := split(n.right, k-n.left.count)
	return join(n.left, a), b
}

// build makes a balanced tree of leaves from lines.
func build(lines [][]rune) *node {
	if len(lines) <= leafLines {
		return newLeaf(lines)
	}
	leaves := (len(lines) + leafLines - 1) / leafLines
	mid := leaves / 2 * leafLines
	return pair(build(lines[:mid]), build(lines[mid:]))
}

// FromLines makes a Rope of lines, which it keeps (see the package comment).
func FromLines(lines [][]rune) Rope { return Rope{build(lines)} }

// Len is the number of lines.
func (r Rope) Len() int {
	if r.root == nil {
		return 0
	}
	return r.root.count
}

// Chars is the number of characters, counting one per line break between
// lines: the length of the text the lines spell joined with "\n".
func (r Rope) Chars() int {
	if r.root == nil {
		return 0
	}
	return r.root.chars + r.root.count - 1
}

// Bytes is the size in bytes of the lines joined with "\n" as UTF-8, invalid
// scalars counting as U+FFFD.
func (r Rope) Bytes() int {
	if r.root == nil {
		return 0
	}
	return r.root.bytes + r.root.count - 1
}

// Line returns line i, which must exist.
func (r Rope) Line(i int) []rune {
	n := r.root
	for n.left != nil {
		if i < n.left.count {
			n = n.left
		} else {
			i -= n.left.count
			n = n.right
		}
	}
	return n.lines[i]
}

// Lines returns every line in a new slice (the lines themselves are shared).
func (r Rope) Lines() [][]rune {
	out := make([][]rune, 0, r.Len())
	r.Each(0, func(_ int, line []rune) bool {
		out = append(out, line)
		return true
	})
	return out
}

// Each calls fn with each line from line from on, in order, until fn returns
// false.
func (r Rope) Each(from int, fn func(i int, line []rune) bool) {
	each(r.root, 0, from, fn)
}

func each(n *node, base, from int, fn func(int, []rune) bool) bool {
	switch {
	case n == nil || base+n.count <= from:
		return true
	case n.left == nil:
		for i := max(from-base, 0); i < len(n.lines); i++ {
			if !fn(base+i, n.lines[i]) {
				return false
			}
		}
		return true
	}
	return each(n.left, base, from, fn) && each(n.right, base+n.left.count, from, fn)
}

// Replace returns a Rope with lines [from, to) replaced by lines, which it
// keeps (see the package comment).
func (r Rope) Replace(from, to int, lines [][]rune) Rope {
	left, rest := split(r.root, from)
	_, right := split(rest, to-from)
	return Rope{join(join(left, build(lines)), right)}
}

// Offset is the character offset of the start of row: the characters of
// the rows above it, one more for each of their line breaks.
func (r Rope) Offset(row int) int {
	offset := 0
	n := r.root
	for n != nil && n.left != nil {
		if row < n.left.count {
			n = n.left
		} else {
			offset += n.left.chars + n.left.count
			row -= n.left.count
			n = n.right
		}
	}
	if n != nil {
		for _, line := range n.lines[:row] {
			offset += len(line) + 1
		}
	}
	return offset
}

// RowAt is the row holding character offset: the first row whose end (the
// position of its line break) is at or after offset, or the last row.
func (r Rope) RowAt(offset int) int {
	row := 0
	n := r.root
	for n != nil && n.left != nil {
		// The left subtree's rows end at offset left.chars + left.count - 1.
		if offset <= n.left.chars+n.left.count-1 {
			n = n.left
		} else {
			offset -= n.left.chars + n.left.count
			row += n.left.count
			n = n.right
		}
	}
	if n == nil {
		return 0
	}
	for i, line := range n.lines {
		if i == len(n.lines)-1 || offset <= len(line) {
			return row + i
		}
		offset -= len(line) + 1
	}
	return row
}

// FirstDifference is the first row at which a and b differ, or the length
// of the shorter when one is a prefix of the other. Subtrees the two share at
// the same row are skipped without looking inside, so after an edit it costs
// about the depth of the tree plus the rows up to the change.
func FirstDifference(a, b Rope) int {
	ia, ib := newIterator(a.root), newIterator(b.root)
	row := 0
	for {
		na, ca, wholeA := ia.top()
		nb, cb, wholeB := ib.top()
		innerA, innerB := na != nil && na.left != nil, nb != nil && nb.left != nil
		switch {
		case na == nil || nb == nil:
			return row
		case na == nb && wholeA && wholeB:
			row += ca
			ia.skip()
			ib.skip()
		case innerA && innerB:
			// Open the bigger node, or both when they are the same size,
			// so that shared subtrees meet at the same row.
			if ca >= cb {
				ia.open()
			}
			if cb >= ca {
				ib.open()
			}
		case innerA:
			ia.open()
		case innerB:
			ib.open()
		default:
			if !sameLine(ia.line(), ib.line()) {
				return row
			}
			row++
			ia.advance()
			ib.advance()
		}
	}
}

func sameLine(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 || &a[0] == &b[0] {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Equal reports whether a and b hold the same lines.
func Equal(a, b Rope) bool {
	return a.Len() == b.Len() && a.Bytes() == b.Bytes() && FirstDifference(a, b) == a.Len()
}

// iterator walks a tree in row order. The top of its stack is the next
// unvisited subtree; within a leaf, at is the next line to read.
type iterator struct {
	stack []*node
	at    int
}

func newIterator(root *node) *iterator {
	it := &iterator{}
	if root != nil {
		it.stack = append(it.stack, root)
	}
	return it
}

// top is the next subtree, how many of its lines are still to be read, and
// whether that is all of them (a leaf partly read is not whole).
func (it *iterator) top() (n *node, count int, whole bool) {
	if len(it.stack) == 0 {
		return nil, 0, false
	}
	n = it.stack[len(it.stack)-1]
	return n, n.count - it.at, it.at == 0
}

func (it *iterator) skip() {
	it.stack = it.stack[:len(it.stack)-1]
	it.at = 0
}

func (it *iterator) open() {
	n := it.stack[len(it.stack)-1]
	it.stack[len(it.stack)-1] = n.right
	it.stack = append(it.stack, n.left)
}

func (it *iterator) line() []rune {
	return it.stack[len(it.stack)-1].lines[it.at]
}

func (it *iterator) advance() {
	n := it.stack[len(it.stack)-1]
	it.at++
	if it.at == len(n.lines) {
		it.skip()
	}
}
