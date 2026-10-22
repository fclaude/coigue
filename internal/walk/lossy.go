package walk

import (
	"strings"
	"unicode/utf8"
)

// Lossy converts a file name or path to text the way Rust's
// OsStr::to_string_lossy does on Unix: every maximal invalid byte sequence
// becomes one U+FFFD, which differs from Go's per-byte conversion for
// truncated multi-byte sequences.
func Lossy(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		valid, invalid := splitInvalid(s)
		b.WriteString(s[:valid])
		if invalid > 0 {
			b.WriteString("�")
		}
		s = s[valid+invalid:]
	}
	return b.String()
}

// splitInvalid is core::str::Utf8Chunks::next: the length of the valid prefix
// of s and the length (1 to 3) of the invalid sequence that follows it, or 0
// when s is valid to the end.
func splitInvalid(s string) (valid, invalid int) {
	get := func(i int) byte {
		if i < len(s) {
			return s[i]
		}
		return 0
	}
	continuation := func(b byte) bool { return int8(b) < -64 }
	validUpTo := 0
	i := 0
	for i < len(s) {
		b := s[i]
		i++
		if b >= 128 {
			switch width(b) {
			case 2:
				if !continuation(get(i)) {
					return validUpTo, i - validUpTo
				}
				i++
			case 3:
				second := get(i)
				ok := (b == 0xE0 && second >= 0xA0 && second <= 0xBF) ||
					(b >= 0xE1 && b <= 0xEC && second >= 0x80 && second <= 0xBF) ||
					(b == 0xED && second >= 0x80 && second <= 0x9F) ||
					(b >= 0xEE && b <= 0xEF && second >= 0x80 && second <= 0xBF)
				if !ok {
					return validUpTo, i - validUpTo
				}
				i++
				if !continuation(get(i)) {
					return validUpTo, i - validUpTo
				}
				i++
			case 4:
				second := get(i)
				ok := (b == 0xF0 && second >= 0x90 && second <= 0xBF) ||
					(b >= 0xF1 && b <= 0xF3 && second >= 0x80 && second <= 0xBF) ||
					(b == 0xF4 && second >= 0x80 && second <= 0x8F)
				if !ok {
					return validUpTo, i - validUpTo
				}
				i++
				if !continuation(get(i)) {
					return validUpTo, i - validUpTo
				}
				i++
				if !continuation(get(i)) {
					return validUpTo, i - validUpTo
				}
				i++
			default:
				return validUpTo, i - validUpTo
			}
		}
		validUpTo = i
	}
	return validUpTo, 0
}

// width is core::str::utf8_char_width.
func width(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2:
		return 0
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF5:
		return 4
	}
	return 0
}
