package width

import (
	"bufio"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// readRanges parses a testdata dump: lines of "START END [fields...]" (hex bounds), '#' comments.
func readRanges(t *testing.T, name string, fn func(lo, hi rune, fields []string)) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		lo, err1 := strconv.ParseInt(fields[0], 16, 32)
		hi, err2 := strconv.ParseInt(fields[1], 16, 32)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: bad line %q", name, line)
		}
		fn(rune(lo), rune(hi), fields[2:])
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}

func isSurrogate(r rune) bool { return 0xD800 <= r && r <= 0xDFFF }

// TestCharAllScalars compares Char with char::width() of the Rust crate for every code point
// (testdata/char_widths.txt: width, -1 = None, -2 = surrogate).
func TestCharAllScalars(t *testing.T) {
	next := rune(0)
	mismatches := 0
	readRanges(t, "char_widths.txt", func(lo, hi rune, f []string) {
		if lo != next {
			t.Fatalf("char_widths.txt: gap before %X", lo)
		}
		next = hi + 1
		want, _ := strconv.Atoi(f[0])
		for r := lo; r <= hi; r++ {
			if want == -2 {
				if !isSurrogate(r) {
					t.Fatalf("U+%04X marked as surrogate", r)
				}
				continue
			}
			w, ok := Char(r)
			if want == -1 {
				if ok {
					mismatches++
					t.Errorf("Char(U+%04X) = %d, true; want None", r, w)
				}
				continue
			}
			if !ok || w != want {
				mismatches++
				t.Errorf("Char(U+%04X) = %d, %v; want %d", r, w, ok, want)
			}
			if mismatches > 20 {
				t.FailNow()
			}
		}
	})
	if next != 0x110000 {
		t.Fatalf("char_widths.txt ends at %X", next)
	}
}

// TestLookupWidthAllScalars checks the generated trie runs and the hand-ported special cases of
// lookupWidth against the crate's lookup_width (testdata/lookup.txt: raw trie entry, width, info).
func TestLookupWidthAllScalars(t *testing.T) {
	readRanges(t, "lookup.txt", func(lo, hi rune, f []string) {
		raw, _ := strconv.Atoi(f[0])
		width, _ := strconv.Atoi(f[1])
		info, _ := strconv.ParseUint(f[2], 16, 16)
		for r := lo; r <= hi; r++ {
			if got := rawWidth(r); int(got) != raw {
				t.Fatalf("rawWidth(U+%04X) = %d, want %d", r, got, raw)
			}
			if isSurrogate(r) {
				continue
			}
			w, i := lookupWidth(r)
			if w != width || i != widthInfo(info) {
				t.Fatalf("lookupWidth(U+%04X) = %d, %#04x; want %d, %#04x", r, w, i, width, info)
			}
		}
	})
}

// TestPredicatesAllScalars checks the helper predicates for every code point against the crate.
func TestPredicatesAllScalars(t *testing.T) {
	for _, tc := range []struct {
		file string
		fn   func(rune) bool
	}{
		{"transparent_zero_width.txt", isTransparentZeroWidth},
		{"ligature_transparent.txt", isLigatureTransparent},
		{"emoji_presentation_seq.txt", startsEmojiPresentationSeq},
		{"text_presentation_seq.txt", startsNonIdeographicTextPresentationSeq},
		{"emoji_modifier_base.txt", isEmojiModifierBase},
	} {
		want := make([]bool, 0x110000)
		readRanges(t, tc.file, func(lo, hi rune, _ []string) {
			for r := lo; r <= hi; r++ {
				want[r] = true
			}
		})
		for r := rune(0); r <= utf8.MaxRune; r++ {
			if isSurrogate(r) {
				continue
			}
			if got := tc.fn(r); got != want[r] {
				t.Fatalf("%s: U+%04X = %v, want %v", tc.file, r, got, want[r])
			}
		}
	}
}

// TestStringGolden checks String against strings whose widths come from the Rust crate
// (testdata/string_golden.txt: "width<TAB>hex(utf-8)"). The strings were chosen to cover every
// state transition of the reverse fold, see testdata/dump/main.rs in the git tag rust-reference.
func TestStringGolden(t *testing.T) {
	f, err := os.Open("testdata/string_golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		w, h, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("bad line %q", line)
		}
		want, err := strconv.Atoi(w)
		if err != nil {
			t.Fatal(err)
		}
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		n++
		if got := String(string(b)); got != want {
			t.Errorf("String(%+q) = %d, want %d", string(b), got, want)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 2000 {
		t.Errorf("golden file has only %d strings", n)
	}
}

// TestCrateExamples runs the literal assertions of unicode-width 0.2.2's own tests/tests.rs
// (non-CJK column; the Rust loops are ported below).
func TestCrateExamples(t *testing.T) {
	for _, tc := range crateStrings {
		if got := String(tc.s); got != tc.want {
			t.Errorf("tests.rs:%d: String(%+q) = %d, want %d", tc.line, tc.s, got, tc.want)
		}
	}
	for _, tc := range crateChars {
		w, ok := Char(tc.r)
		if ok != tc.ok || w != tc.want {
			t.Errorf("tests.rs:%d: Char(%U) = %d, %v; want %d, %v", tc.line, tc.r, w, ok, tc.want, tc.ok)
		}
	}
}

// Rust: unicode-width tests::char_str_consistent
func TestCharStringConsistent(t *testing.T) {
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if isSurrogate(r) {
			continue
		}
		w, ok := Char(r)
		if !ok {
			w = 1
		}
		if got := String(string(r)); got != w {
			t.Fatalf("U+%04X: String = %d, Char = %d", r, got, w)
		}
	}
}

// Rust: unicode-width tests::test_prepended_concatenation_marks
func TestPrependedConcatenationMarks(t *testing.T) {
	for _, r := range []rune{0x0600, 0x0601, 0x0602, 0x0603, 0x0604, 0x06DD, 0x110BD, 0x110CD} {
		if w, ok := Char(r); !ok || w != 1 {
			t.Errorf("Char(%U) = %d, %v; want 1", r, w, ok)
		}
	}
	for _, r := range []rune{0x0605, 0x070F, 0x0890, 0x0891, 0x08E2} {
		if w, ok := Char(r); !ok || w != 0 {
			t.Errorf("Char(%U) = %d, %v; want 0", r, w, ok)
		}
	}
}

// Rust: unicode-width tests::test_lisu_tones
func TestLisuTones(t *testing.T) {
	for c := rune(0xA4F8); c <= 0xA4FD; c++ {
		if w, ok := Char(c); !ok || w != 1 {
			t.Errorf("Char(%U) = %d, %v", c, w, ok)
		}
		if got := String(string(c)); got != 1 {
			t.Errorf("String(%U) = %d", c, got)
		}
	}
	for c1 := rune(0xA4F8); c1 <= 0xA4FD; c1++ {
		for c2 := rune(0xA4F8); c2 <= 0xA4FD; c2++ {
			want := 2
			if c1 <= 0xA4FB && c2 >= 0xA4FC {
				want = 1
			}
			if got := String(string([]rune{c1, c2})); got != want {
				t.Errorf("String(%U %U) = %d, want %d", c1, c2, got, want)
			}
		}
	}
}

func isKhmerCoengEligible(c rune) bool {
	switch {
	case 0x1780 <= c && c <= 0x1782, 0x1784 <= c && c <= 0x1787, 0x1789 <= c && c <= 0x178C,
		0x178E <= c && c <= 0x1793, 0x1795 <= c && c <= 0x1798, 0x179B <= c && c <= 0x179D,
		c == 0x17A0, c == 0x17A2, c == 0x17A7, 0x17AB <= c && c <= 0x17AC, c == 0x17AF:
		return true
	}
	return false
}

// Rust: unicode-width tests::test_khmer_coeng
func TestKhmerCoeng(t *testing.T) {
	for c := rune(0); c <= utf8.MaxRune; c++ {
		if isSurrogate(c) {
			continue
		}
		if isKhmerCoengEligible(c) {
			if got := String("\u17D2" + string(c)); got != 0 {
				t.Fatalf("String(coeng %U) = %d, want 0", c, got)
			}
			if got := String("\u17D2\u200D\u200D" + string(c)); got != 0 {
				t.Fatalf("String(coeng ZWJ ZWJ %U) = %d, want 0", c, got)
			}
			continue
		}
		w, ok := Char(c)
		if !ok {
			w = 1
		}
		if got := String("\u17D2" + string(c)); got != w {
			t.Fatalf("String(coeng %U) = %d, want %d", c, got, w)
		}
	}
}

// Rust: unicode-width tests::test_vs1_vs2_vs3
func TestVS1VS2VS3(t *testing.T) {
	for c := rune(0); c <= utf8.MaxRune; c++ {
		if isSurrogate(c) {
			continue
		}
		s := string(c)
		if c == 0x2018 || c == 0x2019 || c == 0x201C || c == 0x201D {
			for _, tc := range []struct {
				suffix string
				want   int
			}{
				{"︀", 1}, {"︀︁", 1}, {"︁", 2}, {"︁︀", 2}, {"︂", 1}, {"︂︁", 1},
			} {
				if got := String(s + tc.suffix); got != tc.want {
					t.Fatalf("String(%U %+q) = %d, want %d", c, tc.suffix, got, tc.want)
				}
			}
			continue
		}
		w, ok := Char(c)
		if !ok {
			w = 1
		}
		for _, vs := range []string{"︀", "︁", "︂"} {
			if got := String(s + vs); got != w {
				t.Fatalf("String(%U %+q) = %d, want %d", c, vs, got, w)
			}
		}
	}
}

func TestCharInvalidScalars(t *testing.T) {
	for _, r := range []rune{-1, 0xD800, 0xDFFF, 0x110000} {
		w, ok := Char(r)
		fw, fok := Char(utf8.RuneError)
		if w != fw || ok != fok {
			t.Errorf("Char(%#x) = %d, %v; want the result for U+FFFD (%d, %v)", r, w, ok, fw, fok)
		}
	}
	if got := String("a\xffb\xc3"); got != 4 {
		t.Errorf("String with invalid UTF-8 = %d, want 4", got)
	}
}

// Rust: unicode-width README example
func TestStringFullwidth(t *testing.T) {
	if got := String("Ｈｅｌｌｏ, ｗｏｒｌｄ!"); got != 23 {
		t.Errorf("got %d", got)
	}
}

func TestCharASCII(t *testing.T) {
	for r := rune(0); r < 0x80; r++ {
		w, ok := Char(r)
		wantOK := r >= 0x20 && r != 0x7F
		if ok != wantOK || (ok && w != 1) {
			t.Errorf("Char(%U) = %d, %v", r, w, ok)
		}
	}
}

var crateStrings = []struct {
	s    string
	want int
	line int // line in unicode-width-0.2.2/tests/tests.rs
}{
	{"\uFF48\uFF45\uFF4C\uFF4C\uFF4F", 10, 28},
	{"\u0000\u0000\u0000\u0001\u0001", 5, 29},
	{"", 0, 30},
	{"\u2081\u2082\u2083\u2084", 4, 31},
	{"\U0001F469", 2, 36},
	{"\U0001F52C", 2, 37},
	{"\U0001F469\u200D\U0001F52C", 2, 38},
	{"\u00B7", 1, 83},
	{"\u0387", 1, 84},
	{"\u00A8", 1, 85},
	{"\u02C9", 1, 86},
	{"\u0D4E\u0D09", 1, 123},
	{"\U00011A89", 0, 124},
	{"<\u0338", 1, 160},
	{"=\u0338", 1, 161},
	{">\u0338", 1, 162},
	{"=\u0301\u0338", 1, 163},
	{"=\u0338\u0301", 1, 164},
	{"=\uFE0F\u0338", 1, 165},
	{"#\uFE0F\u0338", 2, 166},
	{"#\u0338\uFE0F", 1, 167},
	{"\u06B8\u0338\u0627", 1, 169},
	{"\u06B8\u0338\uFE0E\u0627", 1, 170},
	{"\u06B8\u0338\uFE0F\u0627", 1, 171},
	{"\u06B8\uFE0E\u0338\u0627", 1, 172},
	{"\u06B8\uFE0F\u0338\u0627", 1, 173},
	{"=\u0338\u0627", 2, 175},
	{"#\uFE0F", 2, 182},
	{"a#\uFE0Fa", 4, 183},
	{"#a\uFE0F", 2, 184},
	{"a\uFE0F", 1, 185},
	{"##\uFE0Fa", 4, 186},
	{"*\uFE0F", 2, 187},
	{"\u23F9\uFE0F", 2, 188},
	{"\u24C2\uFE0F", 2, 189},
	{"\U0001F6F3\uFE0F", 2, 190},
	{"\U0001F700\uFE0F", 1, 191},
	{"*\u0301\uFE0F", 1, 192},
	{"*\u200D\uFE0F", 1, 193},
	{"*\uFE0E\uFE0F", 1, 194},
	{"\u2648\uFE0E", 1, 201},
	{"\u2648\uFE0E\uFE0F", 1, 202},
	{"\u2648\uFE0F\uFE0E", 2, 203},
	{"\U0001F21A\uFE0E", 2, 204},
	{"\u0301\uFE0E", 0, 205},
	{"a\uFE0E", 1, 206},
	{"\U00018000\uFE0E", 2, 207},
	{"\u2648\u0301\uFE0E", 2, 208},
	{"\u2648\u200D\uFE0E", 2, 209},
	{"\r", 1, 218},
	{"\n", 1, 219},
	{"\r\n", 1, 220},
	{"\u0000", 1, 221},
	{"1\t2\r\n3\u00854", 7, 222},
	{"\r\uFE0F\n", 2, 223},
	{"\r\u200D\n", 2, 224},
	{"\uA4EA\uA4F9", 2, 257},
	{"\uA4EA\uA4F9\uA4FC", 2, 258},
	{"\uA4EA\uA4F9\uFE0F\uA4FC", 2, 259},
	{"\uA4EA\uA4F9\u200D\uA4FC", 2, 260},
	{"\uA4EA\uA4F9\uA4FC\uFE0F", 2, 261},
	{"\uA4EA\uA4F9\u0301\uA4FC", 3, 262},
	{"\uA4EA\uA4F9\uA4F9", 3, 263},
	{"\uA4EA\uA4FC\uA4FC", 3, 264},
	{"\u05D0", 1, 269},
	{"\u05DC", 1, 270},
	{"\u05D0\u05DC", 2, 271},
	{"\u05D0\u200D\u05DC", 1, 272},
	{"\u05D0\u200D\u200D\u200D\u200D\u200D\u200D\u200D\u05DC", 1, 273},
	{"\u05D0\u05D0\u200D\u05DC", 2, 278},
	{"\u05D0\u05D0\u200D\u200D\u200D\u200D\u200D\u200D\u05DC", 2, 279},
	{"\u05D0\uFE0F\u200D\uFE0F\u05DC\uFE0F", 1, 284},
	{"\u05D0\uFE0E\u200D\uFE0E\u05DC\uFE0E", 1, 285},
	{"\u0644", 1, 290},
	{"\u06B8", 1, 291},
	{"\u0623", 1, 293},
	{"\u0627", 1, 294},
	{"\u0644\u0623", 1, 296},
	{"\u0644\u0627", 1, 297},
	{"\u06B8\u0623", 1, 298},
	{"\u06B8\u0627", 1, 299},
	{"\u0644\u065F\u065E\u0623", 1, 301},
	{"\u0644\u065F\u065E\u0627", 1, 302},
	{"\u06B8\u065F\u065E\u0623", 1, 303},
	{"\u06B8\u065F\u065E\u0627", 1, 304},
	{"\u06B8\uFE0E\u0627", 1, 306},
	{"\u06B8\uFE0F\u0627", 1, 307},
	{"\u06B8\u17B5\u0627", 1, 308},
	{"\u0644\u0644\u0623", 2, 310},
	{"\u0644\u0644\u0627", 2, 311},
	{"\u06B8\u06B8\u0623", 2, 312},
	{"\u06B8\u06B8\u0627", 2, 313},
	{"\u0644\u200D\u0623", 2, 315},
	{"\u0644\u200D\u0627", 2, 316},
	{"\u06B8\u200D\u0623", 2, 317},
	{"\u06B8\u200D\u0627", 2, 318},
	{"\u0644\U0001E94B\u0623", 3, 320},
	{"\u0644\U0001E94B\u0627", 3, 321},
	{"\u06B8\U0001E94B\u0623", 3, 322},
	{"\u06B8\U0001E94B\u0627", 3, 323},
	{"\u1A15", 1, 328},
	{"\u1A17", 0, 329},
	{"\u1A10", 1, 330},
	{"\u1A15\u1A17\u200D\u1A10", 1, 332},
	{"\u1A15\u1A17\u200D\u200D\u200D\u200D\u1A10", 1, 333},
	{"\u1A15\u1A17\u200D\u0338", 1, 338},
	{"\u1A15\uFE0E\u1A17\u200D", 1, 339},
	{"\u1A15\uFE0F\u1A17\u200D", 1, 340},
	{"\u1A15\u1A17\uFE0E\u200D", 1, 341},
	{"\u1A15\u1A17\uFE0F\u200D", 1, 342},
	{"\u1A15\u1A17\u200D\uFE0E", 1, 343},
	{"\u1A15\u1A17\u200D\uFE0F", 1, 344},
	{"\u1A15\u17B5\u200D\uFE0E\u1A17\u200D\uFE0F\u200D\uFE0F", 1, 345},
	{"\u1A15\u1A15\u1A17\u200D\u1A10", 2, 351},
	{"\u1A15\u1A15\u1A17\u200D\u200D\u200D\u200D\u1A10", 2, 352},
	{"\u1A15\u1A17\u1A10", 2, 358},
	{"\u1A15\u200D\u1A10", 2, 359},
	{"\u1A15\u1A10", 2, 360},
	{"\u1A15\u1A17\u1A17\u200D\u1A10", 2, 361},
	{"\u1A15\u1A17\u0338\u200D\u1A10", 2, 362},
	{"\u2D4F", 1, 367},
	{"\u2D3E", 1, 368},
	{"\u2D7F", 1, 369},
	{"\u2D4F\u200D\u2D3E", 1, 371},
	{"\u2D4F\u2D7F\u2D3E", 1, 372},
	{"\u2D4F\u200D\u2D3E", 1, 373},
	{"\u2D4F\uFE0F\u200D\u2D7F\uFE0E\u200D\u17B5\u2D3E", 1, 374},
	{"\u2D4F\u0301\u2D7F\u2D3E", 3, 380},
	{"\u2D4F\u0301\u200D\u2D3E", 2, 381},
	{"\u2D4F\u2D3E", 2, 382},
	{"\u2D4F\u2D7F\u2D7F\u2D3E", 4, 383},
	{"\u2D7F\u2D3E", 2, 384},
	{"\u2D7F\u2D7F\u2D66", 3, 385},
	{"\u2D66\u2D7F\u2D3E", 3, 386},
	{"\U00010C32", 1, 391},
	{"\U00010C03", 1, 392},
	{"\U00010C32\U00010C03", 2, 393},
	{"\U00010C32\u200D\U00010C03", 1, 395},
	{"\U00010C32\uFE0F\u200D\uFE0E\U00010C03", 1, 396},
	{"\U00010C32\u2D7F\U00010C03", 3, 398},
	{"\U00010C32\u0301\u200D\U00010C03", 2, 399},
	{"\U00010C03\u200D\U00010C32", 2, 400},
	{"\u200D\U00010C32", 1, 401},
	{"\u179B", 1, 406},
	{"\u1784", 1, 407},
	{"\u179B\u1784", 2, 408},
	{"\u179B\u17D2\u1784", 1, 409},
	{"\u17A4", 2, 434},
	{"\u17A2\u17A6", 2, 435},
	{"\u17D8", 3, 440},
	{"\u17D4\u179B\u17D4", 3, 441},
	{"\U0001F46A", 2, 446},
	{"\U0001F3FB", 2, 447},
	{"\U0001F46A\U0001F3FB", 2, 448},
	{"\U0001F46A\u200D\u200D\U0001F3FB", 4, 449},
	{"\U0001F9D1\u200D\U0001F91D\u200D\U0001F9D1", 2, 454},
	{"\U0001F1EE\U0001F1F1\U0001F54A\uFE0F\U0001F1F5\U0001F1F8", 6, 456},
	{"\U0001F1F5\U0001F1F8\u200D\U0001F54A\uFE0F\u200D\U0001F1EE\U0001F1F1", 2, 457},
	{"\U0001F1EE\U0001F1F1\u200D\U0001F54A\uFE0F\u200D\u200D\U0001F1F5\U0001F1F8", 4, 458},
	{"\U0001F1F5\U0001F1F8\u200D\u200D\U0001F54A\uFE0F\u200D\U0001F1EE\U0001F1F1", 4, 459},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 2, 461},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6", 3, 462},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6", 3, 463},
	{"\U0001F1E6\U0001F1E6\u200D\u200D\U0001F1E6\U0001F1E6", 4, 465},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 5, 466},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 2, 467},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 5, 468},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 4, 469},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 7, 470},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 6, 471},
	{"\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\U0001F1E6\u200D\U0001F1E6\U0001F1E6", 9, 472},
	{"\U0001F3F4\U000E0067\U000E0062\U000E0077\U000E006C\U000E0073\U000E007F", 2, 474},
	{"\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F\u200D\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F\u200D\U0001F3F4\U000E0067\U000E0062\U000E0077\U000E006C\U000E0073\U000E007F", 2, 475},
	{"\U0001F1E6\U0001F46A\u200D\U0001F3FF", 3, 477},
	{"\U0001F1E6\U0001F3FF\u200D\U0001F3FF", 3, 478},
	{"\U000E0031", 0, 481},
	{"\U000E0063", 0, 482},
	{"\U000E007F", 0, 483},
	{"\U0001F3F4\u200D\u24C2\uFE0F", 2, 484},
	{"\U0001F3F4\U000E0031\u200D\u24C2\uFE0F", 4, 485},
	{"\U0001F3F4\U000E0063\u200D\u24C2\uFE0F", 4, 486},
	{"\U0001F3F4\U000E007F\u200D\u24C2\uFE0F", 4, 487},
	{"\U0001F3F4\U000E0031\U000E007F\u200D\u24C2\uFE0F", 4, 488},
	{"\U0001F3F4\U000E0031\U000E0031\U000E007F\u200D\u24C2\uFE0F", 4, 489},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E007F\u200D\u24C2\uFE0F", 2, 490},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E0031\U000E007F\u200D\u24C2\uFE0F", 4, 491},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 496},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 501},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 506},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E0063\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 511},
	{"\U0001F3F4\U000E0031\U000E0031\U000E0031\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 4, 516},
	{"\U0001F3F4\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 4, 521},
	{"\U0001F3F4\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 522},
	{"\U0001F3F4\U000E0063\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 523},
	{"\U0001F3F4\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 528},
	{"\U0001F3F4\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 2, 533},
	{"\U0001F3F4\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E0063\U000E007F\u200D\u24C2\uFE0F", 4, 538},
	{"a\u200D\U0001F3F4\U000E0067\U000E0062\U000E0077\U000E006C\U000E0073\U000E007F", 3, 544},
	{"\U0001F46A\u200Da", 3, 545},
	{"a\u200Da", 2, 546},
	{"*\uFE0F", 2, 548},
	{"*\u20E3", 1, 549},
	{"*\uFE0F\u20E3", 2, 550},
	{"*\uFE0F", 2, 551},
	{"*\u20E3\uFE0F", 1, 552},
	{"*\uFE0F\u20E3\u200D\U0001F46A", 2, 553},
	{"*\u20E3\uFE0F\u200D\U0001F46A", 3, 554},
	{"*\u20E3\u200D\U0001F46A", 3, 555},
	{"*\uFE0F\u200D\U0001F46A", 2, 556},
	{"*\uFE0F\u20E3\u20E3\u200D\U0001F46A", 4, 557},
	{"*\uFE0F\uFE0F\u20E3\u200D\U0001F46A", 4, 558},
	{"\U0001F1E6\U0001F46A\u200D\U0001F3FF\u200D\U0001F46A\U0001F3FB\u200D\u24C2\uFE0F\u200D*\uFE0F\u200D\U0001F1E6\U0001F1E6\u200D\U0001F3F4\U000E0067\U000E0062\U000E0077\U000E006C\U000E0073\U000E007F\u200D\U0001F46A", 3, 560},
	{"\u24EA", 1, 595},
	{"\u2616", 1, 596},
	{"\u2780", 1, 597},
}

var crateChars = []struct {
	r    rune
	want int
	ok   bool
	line int // line in unicode-width-0.2.2/tests/tests.rs
}{
	{'\uFF48', 2, true, 51},
	{'\u0000', 0, false, 52},
	{'\u0001', 0, false, 53},
	{'\u2081', 1, true, 54},
	{'\u000A', 0, false, 59},
	{'\u0077', 1, true, 60},
	{'\uFF48', 2, true, 61},
	{'\u00AD', 0, true, 62},
	{'\u1160', 0, true, 63},
	{'\u00A1', 1, true, 64},
	{'\u0300', 0, true, 65},
	{'\U0001F971', 2, true, 70},
	{'\u1160', 0, true, 75},
	{'\u3164', 0, true, 76},
	{'\uFFA0', 0, true, 77},
	{'\U000E0000', 0, true, 78},
	{'\u1100', 2, true, 91},
	{'\uA97C', 2, true, 92},
	{'\u115F', 2, true, 94},
	{'\u1160', 0, true, 95},
	{'\uD7C6', 0, true, 96},
	{'\u11A8', 0, true, 97},
	{'\uD7FB', 0, true, 98},
	{'\uFFF9', 1, true, 129},
	{'\uFFFA', 1, true, 130},
	{'\uFFFB', 1, true, 131},
	{'\U00013430', 1, true, 136},
	{'\U00013436', 1, true, 137},
	{'\U0001343C', 1, true, 138},
	{'\u0301', 0, true, 144},
	{'\u20DD', 0, true, 146},
	{'\u09CB', 1, true, 148},
	{'\u09BE', 0, true, 150},
	{'\uA8FA', 0, true, 155},
	{'\u0023', 1, true, 180},
	{'\uFE0F', 0, true, 181},
	{'\uFE0E', 0, true, 199},
	{'\u2648', 2, true, 200},
	{'\u2028', 1, true, 214},
	{'\u2029', 1, true, 215},
	{'\u000D', 0, false, 216},
	{'\u000A', 0, false, 217},
	{'\U0001F3F4', 2, true, 480},
	{'\uFE00', 0, true, 602},
	{'\uFE01', 0, true, 603},
	{'\uFE02', 0, true, 604},
}
