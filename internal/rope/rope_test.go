package rope

import (
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"testing"
)

type rng uint64

func (r *rng) intn(n int) int {
	*r = *r*6364136223846793005 + 1442695040888963407
	return int(uint64(*r)>>33) % n
}

var sample = []rune{'a', 'é', '€', '😀', ' ', -1}

func (r *rng) lines(n int) [][]rune {
	out := make([][]rune, r.intn(n+1))
	for i := range out {
		line := make([]rune, r.intn(6))
		for j := range line {
			line[j] = sample[r.intn(len(sample))]
		}
		out[i] = line
	}
	return out
}

func text(lines [][]rune) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

// check verifies every cached count and the balance of every inner node.
func check(t *testing.T, n *node) (count, chars, bytes int) {
	t.Helper()
	if n == nil {
		return 0, 0, 0
	}
	if n.left == nil {
		if n.right != nil || len(n.lines) == 0 || n.height != 0 {
			t.Fatalf("malformed leaf %+v", n)
		}
		for _, l := range n.lines {
			chars += len(l)
			bytes += lineBytes(l)
		}
		count = len(n.lines)
	} else {
		if n.right == nil {
			t.Fatal("inner node with one child")
		}
		if d := height(n.left) - height(n.right); d > 1 || d < -1 {
			t.Fatalf("unbalanced: heights %d and %d", height(n.left), height(n.right))
		}
		if n.height != max(n.left.height, n.right.height)+1 {
			t.Fatal("wrong height")
		}
		lc, lch, lb := check(t, n.left)
		rc, rch, rb := check(t, n.right)
		count, chars, bytes = lc+rc, lch+rch, lb+rb
	}
	if n.count != count || n.chars != chars || n.bytes != bytes {
		t.Fatalf("cached %d/%d/%d, actual %d/%d/%d", n.count, n.chars, n.bytes, count, chars, bytes)
	}
	return count, chars, bytes
}

func TestReplaceAgreesWithASliceOfLines(t *testing.T) {
	for seed := range 60 {
		r := rng(seed + 1)
		model := r.lines(200)
		rope := FromLines(slices.Clone(model))
		var versions []Rope
		var texts []string
		for step := range 50 {
			from := r.intn(len(model) + 1)
			to := from + r.intn(min(len(model)-from, 70)+1)
			repl := r.lines(150 * r.intn(2))
			before := rope
			rope = rope.Replace(from, to, slices.Clone(repl))
			model = slices.Concat(model[:from:from], repl, model[to:])
			label := fmt.Sprintf("seed %d step %d Replace(%d, %d, %d lines)", seed, step, from, to, len(repl))

			check(t, rope.root)
			if rope.Len() != len(model) || text(rope.Lines()) != text(model) {
				t.Fatalf("%s: lines differ", label)
			}
			want := text(model)
			if rope.Bytes() != max(len(want), 0) || (len(model) > 0 && rope.Chars() != len([]rune(want))) {
				t.Fatalf("%s: Bytes %d Chars %d for %q", label, rope.Bytes(), rope.Chars(), want)
			}
			// Every line's Line, Offset and RowAt, on every other step: the
			// tree checks above already run after every edit.
			offset := 0
			for i, line := range model {
				if step%2 == 1 {
					break
				}
				if string(rope.Line(i)) != string(line) || rope.Offset(i) != offset {
					t.Fatalf("%s: Line/Offset(%d)", label, i)
				}
				// The rule changes only at line breaks: check the start, the
				// middle and the end of each line.
				for _, o := range []int{offset, offset + len(line)/2, offset + len(line)} {
					if got := rope.RowAt(o); got != i && !(o == offset && got == i-1) {
						t.Fatalf("%s: RowAt(%d) = %d, want %d", label, o, got, i)
					}
				}
				offset += len(line) + 1
			}
			// FirstDifference against the version before this edit.
			wantDiff := 0
			old := before.Lines()
			for wantDiff < min(len(old), len(model)) && string(old[wantDiff]) == string(model[wantDiff]) {
				wantDiff++
			}
			if got := FirstDifference(before, rope); got != wantDiff {
				t.Fatalf("%s: FirstDifference = %d, want %d", label, got, wantDiff)
			}
			if Equal(before, rope) != (text(old) == want && len(old) == len(model)) {
				t.Fatalf("%s: Equal disagrees", label)
			}
			if step%10 == 0 {
				versions = append(versions, rope)
				texts = append(texts, want)
			}
		}
		for i, v := range versions {
			if text(v.Lines()) != texts[i] {
				t.Fatalf("seed %d: an old version changed", seed)
			}
		}
	}
}

func TestRowAtMatchesTheBufferRule(t *testing.T) {
	// The rule the buffer's cursor uses: stay on a row while the offset is at
	// most its length, so an offset at a line break is the end of that row.
	lines := [][]rune{[]rune("ab"), {}, []rune("c")}
	rope := FromLines(lines)
	for offset, want := range []int{0, 0, 0, 1, 2, 2, 2} {
		if got := rope.RowAt(offset); got != want {
			t.Errorf("RowAt(%d) = %d, want %d", offset, got, want)
		}
	}
}

func TestEditsKeepTheTreeShallow(t *testing.T) {
	lines := make([][]rune, 100_000)
	for i := range lines {
		lines[i] = []rune("line")
	}
	rope := FromLines(lines)
	r := rng(3)
	for range 20_000 {
		row := r.intn(rope.Len())
		switch r.intn(3) {
		case 0:
			rope = rope.Replace(row, row+1, [][]rune{[]rune("edited")})
		case 1:
			rope = rope.Replace(row, row, [][]rune{[]rune("new"), []rune("lines")})
		default:
			rope = rope.Replace(row, min(row+2, rope.Len()), nil)
		}
	}
	check(t, rope.root)
	leaves := rope.Len()/leafLines + 1
	if limit := 2*bits.Len(uint(leaves)) + 4; rope.root.height > limit {
		t.Fatalf("height %d for %d lines (limit %d)", rope.root.height, rope.Len(), limit)
	}
}

func TestFirstDifferenceSkipsSharedSubtrees(t *testing.T) {
	lines := make([][]rune, 50_000)
	for i := range lines {
		lines[i] = []rune(fmt.Sprint(i))
	}
	a := FromLines(lines)
	b := a.Replace(40_000, 40_001, [][]rune{[]rune("changed")})
	if got := FirstDifference(a, b); got != 40_000 {
		t.Fatalf("FirstDifference = %d", got)
	}
	// Equal versions share the root and compare in constant time.
	if !Equal(b, b) || Equal(a, b) {
		t.Fatal("Equal")
	}
}

// BenchmarkFirstDifference compares a 100,000-line rope with the version
// after one edit near its end. Skipping the subtrees the two share, it costs
// about the depth of the tree, not the 90,000 lines before the change.
func BenchmarkFirstDifference(b *testing.B) {
	lines := make([][]rune, 100_000)
	for i := range lines {
		lines[i] = []rune(fmt.Sprint("line ", i))
	}
	before := FromLines(lines)
	after := before.Replace(90_000, 90_001, [][]rune{[]rune("changed")})
	for range b.N {
		if FirstDifference(before, after) != 90_000 {
			b.Fatal("wrong row")
		}
	}
}

// BenchmarkReplaceLine is replacing one line of a 100,000-line rope.
func BenchmarkReplaceLine(b *testing.B) {
	lines := make([][]rune, 100_000)
	for i := range lines {
		lines[i] = []rune(fmt.Sprint("line ", i))
	}
	r := FromLines(lines)
	edited := [][]rune{[]rune("edited")}
	for i := range b.N {
		row := (i * 7919) % len(lines)
		r = r.Replace(row, row+1, edited)
	}
}
