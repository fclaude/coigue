// Package ucd gives Go the Unicode character predicates and case mapping that
// Rust's char and str methods provide, because the editor's word motions,
// lexers and fuzzy matching were written against those definitions. Go's
// unicode.IsLetter, strings.ToLower and friends differ from them for some
// characters (Alphabetic is wider than Letter, Rust lower-casing uses the full
// mappings for U+0130 and the final-sigma rule), and Go 1.26's tables are
// Unicode 15.0 while the Rust standard library the editor was built with is
// Unicode 17.0, which would otherwise change where a word ends or which paths
// a query matches for characters added since. The predicates therefore read
// range tables dumped from the Rust standard library (testdata/dump.rs,
// regenerated with go generate).
package ucd

//go:generate go run gen.go

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

func inRanges(table [][2]rune, r rune) bool {
	i := sort.Search(len(table), func(i int) bool { return table[i][1] >= r })
	return i < len(table) && table[i][0] <= r
}

// IsWhitespace is char::is_whitespace (the White_Space property).
func IsWhitespace(r rune) bool { return inRanges(whitespaceRanges, r) }

// IsAlphabetic is char::is_alphabetic (the Alphabetic property).
func IsAlphabetic(r rune) bool { return inRanges(alphabeticRanges, r) }

// IsNumeric is char::is_numeric (general categories Nd, Nl and No).
func IsNumeric(r rune) bool { return inRanges(numericRanges, r) }

// IsAlphanumeric is char::is_alphanumeric.
func IsAlphanumeric(r rune) bool { return IsAlphabetic(r) || IsNumeric(r) }

// IsControl is char::is_control (general category Cc).
func IsControl(r rune) bool { return inRanges(controlRanges, r) }

// IsUppercase is char::is_uppercase (the Uppercase property).
func IsUppercase(r rune) bool { return inRanges(uppercaseRanges, r) }

// IsLowercase is char::is_lowercase (the Lowercase property).
func IsLowercase(r rune) bool { return inRanges(lowercaseRanges, r) }

// IsASCIIPunctuation is char::is_ascii_punctuation.
func IsASCIIPunctuation(r rune) bool {
	return (r >= '!' && r <= '/') || (r >= ':' && r <= '@') || (r >= '[' && r <= '`') || (r >= '{' && r <= '~')
}

// IsASCIIDigit is char::is_ascii_digit.
func IsASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// IsASCIIAlphanumeric is char::is_ascii_alphanumeric.
func IsASCIIAlphanumeric(r rune) bool {
	return IsASCIIDigit(r) || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// IsASCIIHexDigit is char::is_ascii_hexdigit.
func IsASCIIHexDigit(r rune) bool {
	return IsASCIIDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isCased(r rune) bool {
	return IsUppercase(r) || IsLowercase(r) || unicode.Is(unicode.Lt, r)
}

// isCaseIgnorable follows the Case_Ignorable derived property: marks,
// format characters, modifier letters and symbols, and the Word_Break
// MidLetter, MidNumLet and Single_Quote characters.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027,
		0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

func caseIgnorableThenCased(runes func() (rune, bool)) bool {
	for {
		r, ok := runes()
		if !ok {
			return false
		}
		if !isCaseIgnorable(r) {
			return isCased(r)
		}
	}
}

func lowerRune(r rune) rune {
	if r < 'A' || (r > 'Z' && r < 0xC0) { // fast path: no mapping between these
		return r
	}
	i := sort.Search(len(lowerMap), func(i int) bool { return lowerMap[i][0] >= r })
	if i < len(lowerMap) && lowerMap[i][0] == r {
		return lowerMap[i][1]
	}
	return r
}

// Lower is str::to_lowercase: simple per-character lower-casing, except that
// U+0130 expands to "i" plus a combining dot and capital sigma becomes a final
// sigma at the end of a word.
func Lower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r == 'Σ':
			before := s[:i]
			after := s[i+utf8.RuneLen(r):]
			previous := func() (rune, bool) {
				if before == "" {
					return 0, false
				}
				c, size := utf8.DecodeLastRuneInString(before)
				before = before[:len(before)-size]
				return c, true
			}
			next := func() (rune, bool) {
				if after == "" {
					return 0, false
				}
				c, size := utf8.DecodeRuneInString(after)
				after = after[size:]
				return c, true
			}
			if caseIgnorableThenCased(previous) && !caseIgnorableThenCased(next) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		case r == 0x130:
			b.WriteString("i̇")
		default:
			b.WriteRune(lowerRune(r))
		}
	}
	return b.String()
}
