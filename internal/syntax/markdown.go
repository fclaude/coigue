package syntax

// Lightweight Markdown source highlighting, with character offsets like the other lexers.

import (
	"strings"

	"github.com/fclaude/coigue/internal/ucd"
)

// highlightMarkdown is markdown::highlight, private to the package like it is
// in Rust; ForPath reaches it for .md and .markdown files.
func highlightMarkdown(lines [][]rune) [][]Style {
	result := make([][]Style, len(lines))
	for i, l := range lines {
		result[i] = make([]Style, len(l))
	}
	row := 0
	comment := false
	paragraph := false
	for row < len(lines) {
		line := lines[row]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		indent := countLeading(line, ' ')
		start := min(indent, len(line))
		if !comment && indent <= 3 {
			if marker, count, info, ok := fence(line[start:]); ok {
				end, found := -1, false
				for r := row + 1; r < len(lines); r++ {
					// The closing fence is looked for in the raw line: a CR counts
					// as the whitespace after it.
					candidate := lines[r]
					leading := countLeading(candidate, ' ')
					if leading > 3 {
						continue
					}
					candidate = candidate[leading:]
					length := countLeading(candidate, marker)
					if length >= count && allWhitespace(candidate[length:]) {
						end, found = r, true
						break
					}
				}
				fill(result[row], Macro)
				codeEnd := len(lines)
				if found {
					codeEnd = end
				}
				code := lines[row+1 : codeEnd]
				var styles [][]Style
				switch info {
				case "rust", "rs":
					styles = Rust(code)
				case "go", "golang":
					styles = Go(code)
				default:
					styles = make([][]Style, len(code))
					for i, l := range code {
						styles[i] = make([]Style, len(l))
						fill(styles[i], String)
					}
				}
				copy(result[row+1:codeEnd], styles)
				if found {
					fill(result[end], Macro)
					row = end + 1
				} else {
					row = len(lines)
				}
				paragraph = false
				continue
			}
			trimmed := line[start:]
			if paragraph && setext(trimmed) {
				fill(result[row-1], Heading)
				fill(result[row], Heading)
				paragraph = false
				row++
				continue
			}
			if thematicBreak(trimmed) {
				fill(result[row], Macro)
				paragraph = false
				row++
				continue
			}
		}
		// Rust oddity preserved: only spaces count towards the four of indented
		// code, and a tab makes code only in the first column (" \tcode" is not).
		if !comment && (indent >= 4 || get(line, 0) == '\t') {
			fill(result[row], String)
			paragraph = false
			row++
			continue
		}
		styles := result[row]
		content := start
		if comment {
			content = 0
		}
		block := false
		if !comment && indent <= 3 {
			for get(line, content) == '>' {
				styles[content] = Macro
				content++
				if get(line, content) == ' ' {
					content++
				}
				block = true
			}
			var markerEnd int
			if c := get(line, content); c == '-' || c == '+' || c == '*' {
				markerEnd = content + 1
			} else {
				digits := 0
				for content+digits < len(line) && ucd.IsASCIIDigit(line[content+digits]) {
					digits++
				}
				if d := get(line, content+digits); digits >= 1 && digits <= 9 && (d == '.' || d == ')') {
					markerEnd = content + digits + 1
				} else {
					markerEnd = content
				}
			}
			// Rust oddity preserved: any Unicode whitespace after the marker makes a
			// list item, so "*\u00a0a*" is not emphasis.
			if markerEnd > content && noneOrWhitespace(line, markerEnd) {
				fill(styles[content:markerEnd], Macro)
				content = markerEnd
				for someWhitespace(line, content) {
					content++
				}
				if get(line, content) == '[' &&
					isOneOf(get(line, content+1), ' ', 'x', 'X') &&
					get(line, content+2) == ']' {
					fill(styles[content:content+3], Constant)
					content += 3
				}
				block = true
			}
			hashes := countLeading(line[content:], '#')
			if hashes >= 1 && hashes <= 6 && noneOrWhitespace(line, content+hashes) {
				// Rust oddity preserved: inline markup in the heading text is
				// styled afterwards and overwrites this.
				fill(styles[content:], Heading)
				block = true
			}
			if get(line, content) == '[' {
				if end, ok := balanced(line, content, '[', ']'); ok && get(line, end+1) == ':' {
					fill(styles[content:end+2], Link)
					fill(styles[end+2:], String)
					paragraph = false
					row++
					continue
				}
			}
		}
		inline(line[content:], styles[content:len(line)], &comment)
		paragraph = !block && !allWhitespace(line) && !comment
		row++
	}
	return result
}

// countLeading is line.iter().take_while(|c| **c == marker).count().
func countLeading(line []rune, marker rune) int {
	n := 0
	for n < len(line) && line[n] == marker {
		n++
	}
	return n
}

// allWhitespace is line.iter().all(|c| c.is_whitespace()).
func allWhitespace(line []rune) bool {
	for _, c := range line {
		if !ucd.IsWhitespace(c) {
			return false
		}
	}
	return true
}

// noneOrWhitespace is line.get(i).is_none_or(|c| c.is_whitespace()).
func noneOrWhitespace(line []rune, i int) bool {
	c := get(line, i)
	return c < 0 || ucd.IsWhitespace(c)
}

// someWhitespace is line.get(i).is_some_and(|c| c.is_whitespace()).
func someWhitespace(line []rune, i int) bool {
	c := get(line, i)
	return c >= 0 && ucd.IsWhitespace(c)
}

// fence recognises an opening code fence and returns its marker, its length
// and the lower-cased first word of the info string.
func fence(line []rune) (marker rune, count int, info string, ok bool) {
	if len(line) == 0 {
		return 0, 0, "", false
	}
	marker = line[0]
	if marker != '`' && marker != '~' {
		return 0, 0, "", false
	}
	count = countLeading(line, marker)
	if count < 3 || (marker == '`' && containsRune(line[count:], '`')) {
		return 0, 0, "", false
	}
	return marker, count, lowerASCII(firstWord(line[count:])), true
}

func containsRune(line []rune, c rune) bool {
	for _, r := range line {
		if r == c {
			return true
		}
	}
	return false
}

// firstWord is info.split_whitespace().next().unwrap_or("").
func firstWord(s []rune) string {
	i := 0
	for i < len(s) && ucd.IsWhitespace(s[i]) {
		i++
	}
	j := i
	for j < len(s) && !ucd.IsWhitespace(s[j]) {
		j++
	}
	return string(s[i:j])
}

func setext(line []rune) bool {
	if len(line) == 0 || (line[0] != '=' && line[0] != '-') {
		return false
	}
	count := countLeading(line, line[0])
	return allWhitespace(line[count:])
}

func thematicBreak(line []rune) bool {
	if len(line) == 0 || (line[0] != '-' && line[0] != '*' && line[0] != '_') {
		return false
	}
	marker := line[0]
	count := 0
	for _, c := range line {
		if c == marker {
			count++
		}
	}
	if count < 3 {
		return false
	}
	for _, c := range line {
		if c != marker && c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}

// balanced returns the index of the close that matches the open at start,
// skipping backslash-escaped characters.
func balanced(line []rune, start int, open, closer rune) (int, bool) {
	depth := 0
	i := start
	for i < len(line) {
		if line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] == open {
			depth++
		} else if line[i] == closer {
			depth--
			if depth == 0 {
				return i, true
			}
		}
		i++
	}
	return 0, false
}

// hasPrefix is line[i..].starts_with(prefix).
func hasPrefix(line []rune, prefix string) bool {
	for k, p := range []rune(prefix) {
		if get(line, k) != p {
			return false
		}
	}
	return true
}

func inline(line []rune, styles []Style, comment *bool) {
	i := 0
	for i < len(line) {
		if *comment || hasPrefix(line[i:], "<!--") {
			start := i
			if !*comment {
				i += 4
				*comment = true
			}
			for i < len(line) {
				if hasPrefix(line[i:], "-->") {
					i += 3
					*comment = false
					break
				}
				i++
			}
			fill(styles[start:i], Comment)
			continue
		}
		if line[i] == '\\' {
			if c := get(line, i+1); c >= 0 && ucd.IsASCIIPunctuation(c) {
				i += 2
				continue
			}
		}
		if line[i] == '`' {
			length := countLeading(line[i:], '`')
			end := i + length
			for end < len(line) {
				if line[end] == '`' {
					closing := countLeading(line[end:], '`')
					if closing == length {
						end += closing
						fill(styles[i:end], String)
						break
					}
					end += closing
				} else {
					end++
				}
			}
			if end < len(line) || styles[i] == String {
				i = end
			} else {
				i += length
			}
			continue
		}
		bracket := i
		if line[i] == '!' && get(line, i+1) == '[' {
			bracket++
		}
		if get(line, bracket) == '[' {
			if labelEnd, ok := balanced(line, bracket, '[', ']'); ok {
				destination := labelEnd + 1
				end, ok := 0, false
				switch get(line, destination) {
				case '(':
					end, ok = balanced(line, destination, '(', ')')
				case '[':
					end, ok = balanced(line, destination, '[', ']')
				}
				if ok {
					fill(styles[i:destination], Link)
					fill(styles[destination:end+1], String)
					i = end + 1
					continue
				}
			}
		}
		if line[i] == '<' {
			if length := indexRune(line[i+1:], '>'); length >= 0 {
				end := i + 1 + length
				target := string(line[i+1 : end])
				if !strings.ContainsFunc(target, ucd.IsWhitespace) &&
					(strings.HasPrefix(target, "https://") ||
						strings.HasPrefix(target, "http://") ||
						strings.HasPrefix(target, "mailto:") ||
						strings.Contains(target, "@")) {
					fill(styles[i:end+1], Link)
					i = end + 1
					continue
				}
			}
		}
		if line[i] == '*' || line[i] == '_' {
			marker := line[i]
			length := countLeading(line[i:], marker)
			start := i + length
			if c := get(line, start); length <= 3 &&
				c >= 0 && !ucd.IsWhitespace(c) &&
				(marker != '_' || i == 0 || !ucd.IsAlphanumeric(line[i-1])) {
				end := start
				for end < len(line) {
					if line[end] == '\\' {
						end += 2
						continue
					}
					if line[end] == marker {
						closing := countLeading(line[end:], marker)
						if closing == length &&
							end > start &&
							!ucd.IsWhitespace(line[end-1]) &&
							(marker != '_' || !someAlphanumeric(line, end+length)) {
							if length == 1 {
								fill(styles[i:end+length], Emphasis)
							} else {
								fill(styles[i:end+length], Strong)
							}
							i = end + length
							break
						}
						end += closing
					} else {
						end++
					}
				}
				if end < len(line) {
					continue
				}
			}
			i = start
			continue
		}
		i++
	}
}

// someAlphanumeric is line.get(i).is_some_and(|c| c.is_alphanumeric()).
func someAlphanumeric(line []rune, i int) bool {
	c := get(line, i)
	return c >= 0 && ucd.IsAlphanumeric(c)
}

// indexRune is iter().position(|c| *c == target), -1 when absent.
func indexRune(line []rune, target rune) int {
	for i, c := range line {
		if c == target {
			return i
		}
	}
	return -1
}
