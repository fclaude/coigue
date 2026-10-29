package marks

import (
	"strings"
	"testing"
)

func lines(text string) [][]rune {
	var out [][]rune
	for _, l := range strings.Split(text, "\n") {
		out = append(out, []rune(l))
	}
	return out
}

func TestMapper(t *testing.T) {
	cases := []struct {
		name      string
		old, new  string
		from, to  Pos
		wantExact bool
	}{
		{"nothing changed", "a\nb\nc", "a\nb\nc", Pos{1, 1}, Pos{1, 1}, true},
		{"a line added above", "a\nb\nc", "new\na\nb\nc", Pos{1, 1}, Pos{2, 1}, true},
		{"a line removed above", "a\nb\nc", "b\nc", Pos{2, 1}, Pos{1, 1}, true},
		{"a line added below", "a\nb", "a\nb\nc", Pos{0, 1}, Pos{0, 1}, true},
		{"typing before the mark on its line", "hello world", "hello big world", Pos{0, 8}, Pos{0, 12}, true},
		{"typing after the mark on its line", "hello world", "hello world!!", Pos{0, 2}, Pos{0, 2}, true},
		{"typing exactly at the mark leaves it before the text", "ab", "aXb", Pos{0, 1}, Pos{0, 1}, true},
		{"deleting before the mark on its line", "hello big world", "hello world", Pos{0, 12}, Pos{0, 8}, true},
		{"the mark's own text deleted", "hello big world", "hello world", Pos{0, 8}, Pos{0, 6}, false},
		{"a line split above", "abcdef\nx", "abc\ndef\nx", Pos{1, 0}, Pos{2, 0}, true},
		{"the mark on the second half of a split line", "abcdef", "abc\ndef", Pos{0, 5}, Pos{1, 2}, true},
		{"two lines joined, mark on the second", "abc\ndef", "abcdef", Pos{1, 1}, Pos{0, 4}, true},
		{"a deleted line's mark falls to the next line", "a\nb\nc", "a\nc", Pos{1, 1}, Pos{1, 0}, false},
		{"the last line deleted", "a\nb", "a", Pos{1, 1}, Pos{0, 1}, false},
		{"far apart edits keep the line between them", "a\nb\nc\nd\ne\nf\ng", "A\nb\nc\nd\ne\nf\nG", Pos{3, 1}, Pos{3, 1}, true},
		{"far apart edits that shift", "a\nb\nc\nd\ne\nf\ng", "a\nnew\nb\nc\nd\ne\nF\nextra\ng", Pos{3, 0}, Pos{4, 0}, true},
		{"a whole replacement falls to the start of the change", "one\ntwo", "completely\ndifferent\ntext", Pos{1, 2}, Pos{0, 0}, false},
		{"a mark at the end of text that was emptied stays at the end", "a\nb", "", Pos{1, 1}, Pos{0, 0}, true},
		{"from empty", "", "a\nb", Pos{0, 0}, Pos{0, 0}, true},
	}
	for _, c := range cases {
		got, exact := NewMapper(lines(c.old), lines(c.new)).Map(c.from)
		if got != c.to || exact != c.wantExact {
			t.Errorf("%s: %v -> %v exact %v; want %v exact %v", c.name, c.from, got, exact, c.to, c.wantExact)
		}
	}
}

func TestMapperStaysInsideTheNewText(t *testing.T) {
	m := NewMapper(lines("a\nb\nc\nd"), lines("a"))
	for _, p := range []Pos{{0, 5}, {1, 0}, {3, 9}, {10, 10}} {
		got, _ := m.Map(p)
		if got.Row != 0 || got.Col > 1 {
			t.Errorf("%v -> %v is outside the text", p, got)
		}
	}
}

func TestMapperOfAHugeChangeIsOneBlockNotASlowAlignment(t *testing.T) {
	var a, b strings.Builder
	for i := range 3000 {
		a.WriteString("old line\n")
		b.WriteString("new line\n")
		_ = i
	}
	m := NewMapper(lines(a.String()), lines(b.String()))
	if len(m.blocks) != 1 || m.kept != nil {
		t.Fatalf("blocks %d kept %v", len(m.blocks), m.kept != nil)
	}
	if got, _ := m.Map(Pos{100, 3}); got.Row < 0 || got.Row > 3000 {
		t.Fatalf("got %v", got)
	}
}
