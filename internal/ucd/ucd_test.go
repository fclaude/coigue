package ucd

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode"
)

func TestLowerMatchesRustToLowercase(t *testing.T) {
	// Expected values are the results of Rust's str::to_lowercase.
	for in, want := range map[string]string{
		"ΑΣ":      "ας",
		"ΑΣΑ":     "ασα",
		"Σ":       "σ",
		"ΟΔΟΣ.":   "οδος.",
		"Σ.Α":     "σ.α",
		"AΣ ΑΣ":   "aς ας",
		"İ":       "i̇",
		"CAFÉ.rs": "café.rs",
		"N/CF":    "n/cf",
		"ǅ":       "ǆ",
	} {
		if got := Lower(in); got != want {
			t.Errorf("Lower(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPredicatesDifferFromGoDefaultsWhereRustDoes(t *testing.T) {
	for _, c := range []struct {
		r          rune
		alpha, num bool
	}{
		{'a', true, false},
		{'_', false, false},
		{'Ⅷ', true, true}, // Nl is alphabetic and numeric in Rust
		{'ª', true, false},
		{'²', false, true},
		{'界', true, false},
		{'ͅ', true, false}, // Other_Alphabetic combining mark
		{'9', false, true},
		{' ', false, false},
	} {
		if IsAlphabetic(c.r) != c.alpha || IsNumeric(c.r) != c.num {
			t.Errorf("%U alphabetic=%v numeric=%v", c.r, IsAlphabetic(c.r), IsNumeric(c.r))
		}
	}
	if !IsWhitespace(' ') || !IsWhitespace('\u0085') || IsWhitespace('a') {
		t.Error("whitespace")
	}
	if !IsControl('\u001b') || IsControl('a') {
		t.Error("control")
	}
}

// readRanges loads the Rust-dumped range list that tables.go was generated from.
func readRanges(t *testing.T, name string) map[rune]bool {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	set := map[rune]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var a, b rune
		if _, err := fmt.Sscanf(line, "%X %X", &a, &b); err != nil {
			t.Fatalf("%s: %q: %v", name, line, err)
		}
		for r := a; r <= b; r++ {
			set[r] = true
		}
	}
	return set
}

func TestPredicatesAgreeWithTheRustDumpForEveryScalar(t *testing.T) {
	for _, c := range []struct {
		file string
		fn   func(rune) bool
	}{
		{"rust-alphabetic.txt", IsAlphabetic},
		{"rust-numeric.txt", IsNumeric},
		{"rust-whitespace.txt", IsWhitespace},
		{"rust-control.txt", IsControl},
		{"rust-uppercase.txt", IsUppercase},
		{"rust-lowercase.txt", IsLowercase},
	} {
		want := readRanges(t, c.file)
		bad := 0
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if r >= 0xD800 && r < 0xE000 {
				continue
			}
			if c.fn(r) != want[r] {
				if bad++; bad < 5 {
					t.Errorf("%s: U+%04X got %v", c.file, r, c.fn(r))
				}
			}
		}
	}
}

func TestLowerAgreesWithTheRustMappingForEveryScalar(t *testing.T) {
	data, err := os.ReadFile("testdata/rust-lower-map.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := map[rune]rune{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var from, to rune
		fmt.Sscanf(line, "%X %X", &from, &to)
		want[from] = to
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r < 0xE000 || r == 'Σ' || r == 0x130 {
			continue
		}
		expected := r
		if l, ok := want[r]; ok {
			expected = l
		}
		if got := Lower(string(r)); got != string(expected) {
			t.Fatalf("Lower(U+%04X) = %q, want %q", r, got, string(expected))
		}
	}
}
