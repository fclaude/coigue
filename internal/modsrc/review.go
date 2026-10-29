package modsrc

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Printable makes text safe to show a person who is deciding whether to run
// it. Control characters, characters that reorder or hide text (the
// bidirectional controls, zero-width characters, line separators), blank
// letters and bytes that are not UTF-8 are written out as <U+202E> or <0xFF>,
// so a line reads as the characters it holds and nothing is hidden or
// repainted. A tab stays a tab.
func Printable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "<0x%02X>", s[i])
		case r == '\t':
			b.WriteByte('\t')
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == 0x2028, r == 0x2029,
			r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0:
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// maxDiffCells bounds the work of a line diff; a bigger pair is shown whole.
const maxDiffCells = 4_000_000

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

type edit struct {
	op   byte // ' ', '-' or '+'
	text string
}

// Diff is a unified diff of two versions of a file with three lines of
// context, or false when the files are too large to compare quickly. The text
// is not made Printable.
func Diff(old, new []byte) ([]string, bool) {
	a, b := splitLines(old), splitLines(new)
	if (len(a)+1)*(len(b)+1) > maxDiffCells {
		return nil, false
	}
	// lcs[i][j] is the length of the longest common run of a[i:] and b[j:].
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var script []edit
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			script = append(script, edit{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			script = append(script, edit{'-', a[i]})
			i++
		default:
			script = append(script, edit{'+', b[j]})
			j++
		}
	}
	return hunks(script), true
}

func hunks(script []edit) []string {
	const context = 3
	// keep marks the edits within context of a change.
	keep := make([]bool, len(script))
	for i, e := range script {
		if e.op == ' ' {
			continue
		}
		for k := max(i-context, 0); k <= min(i+context, len(script)-1); k++ {
			keep[k] = true
		}
	}
	var out []string
	oldLine, newLine := 1, 1
	for i := 0; i < len(script); {
		if !keep[i] {
			if script[i].op != '+' {
				oldLine++
			}
			if script[i].op != '-' {
				newLine++
			}
			i++
			continue
		}
		end := i
		oldCount, newCount := 0, 0
		for end < len(script) && keep[end] {
			if script[end].op != '+' {
				oldCount++
			}
			if script[end].op != '-' {
				newCount++
			}
			end++
		}
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldLine, oldCount, newLine, newCount))
		for _, e := range script[i:end] {
			out = append(out, string(e.op)+" "+e.text)
		}
		oldLine += oldCount
		newLine += newCount
		i = end
	}
	return out
}

// Review is the page a person reads before a plan is applied: where the files
// come from, what happens to each, and the code itself (a new file whole, a
// changed one as a diff). Every line is Printable.
func Review(plans []*Plan) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, Printable(fmt.Sprintf(format, args...)))
	}
	for pi, p := range plans {
		if pi > 0 {
			out = append(out, "")
		}
		add("%s at commit %s", p.Source, p.Commit)
		for _, c := range p.Changes {
			switch {
			case c.Blocked != "":
				add("  CANNOT APPLY  %s: %s", c.Module, c.Blocked)
			case c.Kind == Same:
				add("  unchanged     %s (only the lock moves to this commit)", c.Module)
			default:
				add("  %-13s %s (%d bytes, sha256 %s)", c.Kind, c.Module, len(c.New), Sum(c.New))
			}
		}
		for _, n := range p.Notes {
			add("  note: %s", n)
		}
		for _, c := range p.Changes {
			if c.Blocked != "" || c.Kind == Same {
				continue
			}
			out = append(out, "")
			if c.Kind == Add {
				add("=== modules/%s.scm: new file ===", c.Module)
				for _, line := range splitLines(c.New) {
					out = append(out, Printable("+ "+line))
				}
				continue
			}
			add("=== modules/%s.scm: changes ===", c.Module)
			diff, ok := Diff(c.Old, c.New)
			if !ok {
				add("(too large to compare; the new file follows)")
				for _, line := range splitLines(c.New) {
					out = append(out, Printable("+ "+line))
				}
				continue
			}
			for _, line := range diff {
				out = append(out, Printable(line))
			}
		}
	}
	return out
}
