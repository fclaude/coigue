package syntax

import (
	"unicode"

	"github.com/fclaude/brote/scheme"
)

// Scheme highlights Brote source without evaluating it or requiring a language
// server. Like Brote's reader, #| comments end at the first |#, strings may span
// lines, and triple-quoted strings are raw. Relexing the buffer after edits
// recomputes all multiline state.
func Scheme(lines [][]rune) [][]Style {
	result := make([][]Style, 0, len(lines))
	block, quoted, raw := false, false, false
	for _, line := range lines {
		styles := make([]Style, len(line))
		escaped := false
		for i := 0; i < len(line); {
			start := i
			style := Plain
			switch {
			case block:
				style = Comment
				if line[i] == '|' && get(line, i+1) == '#' {
					block = false
					i += 2
				} else {
					i++
				}
			case raw:
				style = String
				if tripleQuote(line, i) {
					raw = false
					i += 3
				} else {
					i++
				}
			case quoted:
				style = String
				c := line[i]
				i++
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					quoted = false
				}
			case line[i] == ';':
				style = Comment
				i = len(line)
			case line[i] == '#' && get(line, i+1) == '|':
				style = Comment
				block = true
				i += 2
			case tripleQuote(line, i):
				style = String
				raw = true
				i += 3
			case line[i] == '"':
				style = String
				quoted = true
				i++
			case line[i] == '#' && get(line, i+1) == '(':
				style = Punctuation
				i += 2
			case isOneOf(line[i], '(', ')', '\'', '`', ','):
				style = Punctuation
				if line[i] == ',' && get(line, i+1) == '@' {
					i += 2
				} else {
					i++
				}
			case unicode.IsSpace(line[i]):
				i++
			default:
				// Consume a whole reader token: define-value, -name, 1name and foo#t
				// must not acquire the colour of a shorter keyword or number.
				i++
				for i < len(line) && !schemeDelimiter(line[i]) {
					i++
				}
				token := string(line[start:i])
				switch {
				case schemeKeywords[token]:
					style = Keyword
				case token == "#t" || token == "#f":
					style = Constant
				case token == ".":
					style = Punctuation
				case schemeNumber(token):
					style = Number
				}
			}
			fill(styles[start:i], style)
		}
		result = append(result, styles)
	}
	return result
}
func tripleQuote(line []rune, i int) bool {
	return get(line, i) == '"' && get(line, i+1) == '"' && get(line, i+2) == '"'
}
func schemeDelimiter(c rune) bool {
	return c == 0 || unicode.IsSpace(c) || isOneOf(c, '(', ')', '"', ';', '\'')
}
func schemeNumber(token string) bool {
	if token == "" {
		return false
	}
	c := token[0]
	if (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
		return false
	}
	value, err := scheme.ReadOne(token)
	return err == nil && (value.Kind == scheme.KInt || value.Kind == scheme.KFloat)
}

var schemeKeywords = map[string]bool{
	"define": true, "lambda": true, "if": true, "let": true, "let*": true, "letrec": true,
	"begin": true, "set!": true, "quote": true, "quasiquote": true, "unquote": true,
	"unquote-splicing": true, "cond": true, "case": true, "else": true, "=>": true,
	"and": true, "or": true, "do": true, "when": true, "unless": true, "guard": true,
	"with-output-to-string": true, "define-module": true, "require": true,
}
