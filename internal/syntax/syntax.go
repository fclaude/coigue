package syntax

// Lightweight fallback lexers. Character-indexed styles match the editor buffer.
// Relex from the beginning after edits so multiline state stays correct.

import (
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/ucd"
)

// ForPath picks a lexer by file extension (ASCII case-insensitive): rs, go,
// scm, md and markdown. It returns nil when the extension has no lexer, like the
// None of Rust's for_path; a file with lexer and no lines gives an empty,
// non-nil result.
func ForPath(path string, lines [][]rune) [][]Style {
	ext, ok := fsx.Extension(path)
	if !ok {
		return nil
	}
	switch lowerASCII(ext) {
	case "rs":
		return Rust(lines)
	case "go":
		return Go(lines)
	case "scm":
		return Scheme(lines)
	case "md", "markdown":
		return highlightMarkdown(lines)
	}
	return nil
}

// lowerASCII is str::to_ascii_lowercase: only A-Z change. strings.ToLower
// would also map characters such as U+212A KELVIN SIGN to ASCII letters.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// none stands for Rust's None in the Option<char> that slice::get returns.
const none rune = -1

// get is line.get(i).copied(): the character at i, or none past the end.
func get(line []rune, i int) rune {
	if i < 0 || i >= len(line) {
		return none
	}
	return line[i]
}

// Go styles Go source: line comments, block comments and raw strings that
// span lines, interpreted strings and runes, numbers, and keywords.
func Go(lines [][]rune) [][]Style {
	blockComment := false
	rawString := false
	result := make([][]Style, 0, len(lines))
	for _, line := range lines {
		styles := make([]Style, len(line))
		i := 0
		for i < len(line) {
			start := i
			c := line[i]
			next := get(line, i+1)
			var style Style
			switch {
			case blockComment:
				style = Comment
				if c == '*' && next == '/' {
					blockComment = false
					i += 2
				} else {
					i++
				}
			case rawString:
				style = String
				if c == '`' {
					rawString = false
				}
				i++
			case c == '/' && next == '/':
				style = Comment
				i = len(line)
			case c == '/' && next == '*':
				style = Comment
				blockComment = true
				i += 2
			case c == '`':
				style = String
				rawString = true
				i++
			case c == '"' || c == '\'':
				style = String
				i++
				for i < len(line) {
					ch := line[i]
					i++
					if ch == '\\' {
						i = min(i+1, len(line))
					} else if ch == c {
						break
					}
				}
			case ucd.IsASCIIDigit(c) || (c == '.' && next >= 0 && ucd.IsASCIIDigit(next)):
				style = Number
				i++
				// Rust oddity preserved: a sign continues the number after any
				// e, E, p or P, so the hex literal 0xe+1 is one number, and
				// every '.' continues it, so 1.2.3 is one number.
				for i < len(line) {
					ch := line[i]
					if ucd.IsASCIIAlphanumeric(ch) ||
						ch == '_' ||
						ch == '.' ||
						((ch == '+' || ch == '-') && isOneOf(line[i-1], 'e', 'E', 'p', 'P')) {
						i++
					} else {
						break
					}
				}
			case identStart(c):
				i++
				for i < len(line) && ident(line[i]) {
					i++
				}
				if goKeywords[string(line[start:i])] {
					style = Keyword
				} else {
					style = Plain
				}
			default:
				if ucd.IsASCIIPunctuation(c) {
					style = Punctuation
				} else {
					style = Plain
				}
				i++
			}
			fill(styles[start:i], style)
		}
		result = append(result, styles)
	}
	return result
}

// goKeywords is read-only.
var goKeywords = map[string]bool{
	"break":       true,
	"case":        true,
	"chan":        true,
	"const":       true,
	"continue":    true,
	"default":     true,
	"defer":       true,
	"else":        true,
	"fallthrough": true,
	"for":         true,
	"func":        true,
	"go":          true,
	"goto":        true,
	"if":          true,
	"import":      true,
	"interface":   true,
	"map":         true,
	"package":     true,
	"range":       true,
	"return":      true,
	"select":      true,
	"struct":      true,
	"switch":      true,
	"type":        true,
	"var":         true,
}

// Rust styles Rust source: nested block comments, raw and byte/C strings that
// span lines, characters and lifetimes, numbers, macros and keywords.
func Rust(lines [][]rune) [][]Style {
	depth := 0
	quoted := false
	escaped := false
	raw := false // Rust's Option<usize>: raw reports Some, rawHashes is its value.
	rawHashes := 0
	result := make([][]Style, 0, len(lines))
	for _, line := range lines {
		styles := make([]Style, len(line))
		i := 0
		for i < len(line) {
			start := i
			c := line[i]
			next := get(line, i+1)
			var style Style
			if depth > 0 {
				style = Comment
				if c == '/' && next == '*' {
					depth++
					i += 2
				} else if c == '*' && next == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			} else if raw {
				style = String
				if c == '"' && rawTerminator(line, i, rawHashes) {
					i += 1 + rawHashes
					raw = false
				} else {
					i++
				}
			} else if quoted {
				style = String
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					quoted = false
				}
				i++
			} else if c == '/' && next == '/' {
				style = Comment
				i = len(line)
			} else if c == '/' && next == '*' {
				style = Comment
				depth = 1
				i += 2
			} else if end, hashes, ok := rawStart(line, i); ok {
				style = String
				raw = true
				rawHashes = hashes
				i = end
			} else if c == '"' || ((c == 'b' || c == 'c') && next == '"') {
				style = String
				quoted = true
				if c == '"' {
					i++
				} else {
					i += 2
				}
			} else if end, ok := charEnd(line, i); ok {
				style = String
				i = end
			} else if c == '\'' && next >= 0 && identStart(next) {
				style = Lifetime
				i += 2
				for i < len(line) && ident(line[i]) {
					i++
				}
			} else if ucd.IsASCIIDigit(c) {
				style = Number
				i++
				// Rust oddity preserved: a sign continues the number after any
				// e or E, so the hex literal 0xE+1 is one number.
				for i < len(line) {
					n := line[i]
					if ucd.IsASCIIAlphanumeric(n) ||
						n == '_' ||
						(n == '.' && ucd.IsASCIIDigit(get(line, i+1))) ||
						((n == '+' || n == '-') && isOneOf(get(line, i-1), 'e', 'E')) {
						i++
					} else {
						break
					}
				}
			} else if identStart(c) {
				i++
				for i < len(line) && ident(line[i]) {
					i++
				}
				word := string(line[start:i])
				// Raw identifiers deliberately bypass keyword styling.
				if word == "r" && get(line, i) == '#' && identStartAt(line, i+1) {
					i += 2
					for i < len(line) && ident(line[i]) {
						i++
					}
					style = Plain
				} else if get(line, i) == '!' && get(line, i+1) != '=' {
					// Rust oddity preserved: the bang is tested before the keyword
					// table, so `if!x` and `self!` are macro invocations.
					i++
					style = Macro
				} else if rustKeywords[word] {
					style = Keyword
				} else {
					style = Plain
				}
			} else {
				if ucd.IsASCIIPunctuation(c) {
					style = Punctuation
				} else {
					style = Plain
				}
				i++
			}
			fill(styles[start:i], style)
		}
		// A backslash at end of line escapes the newline, not the next character.
		escaped = false
		result = append(result, styles)
	}
	return result
}

// rawTerminator is (0..hashes).all(|n| line.get(i + 1 + n) == Some(&'#')).
func rawTerminator(line []rune, i, hashes int) bool {
	for n := 0; n < hashes; n++ {
		if get(line, i+1+n) != '#' {
			return false
		}
	}
	return true
}

func identStart(c rune) bool {
	return c == '_' || ucd.IsAlphabetic(c)
}

func ident(c rune) bool {
	return identStart(c) || ucd.IsNumeric(c)
}

// identStartAt is line.get(i).is_some_and(ident_start).
func identStartAt(line []rune, i int) bool {
	c := get(line, i)
	return c >= 0 && identStart(c)
}

func isOneOf(c rune, set ...rune) bool {
	for _, s := range set {
		if c == s {
			return true
		}
	}
	return false
}

func fill(styles []Style, style Style) {
	for i := range styles {
		styles[i] = style
	}
}

// rawStart recognises r"..", r#".."#, br".." and cr".." openers. It returns
// the index just past the opening quote and the number of hashes.
func rawStart(line []rune, i int) (end, hashes int, ok bool) {
	j := i
	if c := get(line, j); c == 'b' || c == 'c' {
		j++
	}
	if get(line, j) != 'r' {
		return 0, 0, false
	}
	j++
	start := j
	for get(line, j) == '#' {
		j++
	}
	if get(line, j) != '"' {
		return 0, 0, false
	}
	return j + 1, j - start, true
}

// charEnd returns the index just past a character literal ('x', '\n',
// '\u{..}', '\x41', b'x') starting at i, if there is one.
func charEnd(line []rune, i int) (int, bool) {
	j := i
	if get(line, j) == 'b' {
		j++
	}
	if get(line, j) != '\'' {
		return 0, false
	}
	j++
	if get(line, j) == '\\' {
		j++
		c := get(line, j)
		if c < 0 {
			return 0, false
		}
		switch {
		case c == 'u' && get(line, j+1) == '{':
			j += 2
			for {
				h := get(line, j)
				if h >= 0 && (ucd.IsASCIIHexDigit(h) || h == '_') {
					j++
				} else {
					break
				}
			}
			if get(line, j) != '}' {
				return 0, false
			}
			j++
		case c == 'x':
			j++
			for range 2 {
				h := get(line, j)
				if h < 0 || !ucd.IsASCIIHexDigit(h) {
					return 0, false
				}
				j++
			}
		default:
			j++
		}
	} else {
		if c := get(line, j); c < 0 || c == '\'' {
			return 0, false
		}
		j++
	}
	if get(line, j) != '\'' {
		return 0, false
	}
	return j + 1, true
}

// rustKeywords is read-only.
var rustKeywords = map[string]bool{
	"as": true, "async": true, "await": true, "break": true, "const": true, "continue": true,
	"crate": true, "dyn": true, "else": true, "enum": true, "extern": true, "false": true,
	"fn": true, "for": true, "if": true, "impl": true, "in": true, "let": true, "loop": true,
	"match": true, "mod": true, "move": true, "mut": true, "pub": true, "ref": true,
	"return": true, "self": true, "Self": true, "static": true, "struct": true, "super": true,
	"trait": true, "true": true, "type": true, "unsafe": true, "use": true, "where": true,
	"while": true, "abstract": true, "become": true, "box": true, "do": true, "final": true,
	"macro": true, "override": true, "priv": true, "try": true, "typeof": true,
	"unsized": true, "virtual": true, "yield": true, "gen": true,
}
