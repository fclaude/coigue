// Package semantic decodes LSP relative UTF-16 tokens into theme-independent,
// character-indexed spans.
package semantic

import (
	"sort"

	"github.com/fclaude/coigue/internal/syntax"
)

// Types is the token type vocabulary the client announces to servers. It is
// never modified.
var Types = []string{
	"namespace",
	"type",
	"class",
	"enum",
	"interface",
	"struct",
	"typeParameter",
	"parameter",
	"variable",
	"property",
	"enumMember",
	"event",
	"function",
	"method",
	"macro",
	"keyword",
	"modifier",
	"comment",
	"string",
	"number",
	"regexp",
	"operator",
	"decorator",
	"lifetime",
	"attribute",
	"boolean",
	"builtinType",
	"typeAlias",
	"derive",
	"deriveHelper",
	"constParameter",
	"selfKeyword",
	"selfTypeKeyword",
	"escapeSequence",
	"formatSpecifier",
}

// Modifiers is the token modifier vocabulary the client announces to servers.
// It is never modified.
var Modifiers = []string{
	"declaration",
	"definition",
	"readonly",
	"static",
	"deprecated",
	"abstract",
	"async",
	"modification",
	"documentation",
	"defaultLibrary",
}

// Legend is the server's own numbering of token types and modifiers.
type Legend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

// Token is one styled span of a single line, in characters.
type Token struct {
	Row   int
	Start int
	End   int
	Style syntax.Style
}

func style(kind string, readonly bool) (syntax.Style, bool) {
	switch kind {
	case "namespace":
		return syntax.Namespace, true
	case "type", "class", "enum", "interface", "struct", "typeParameter", "builtinType",
		"typeAlias", "selfTypeKeyword":
		return syntax.Type, true
	case "function", "method":
		return syntax.Function, true
	case "variable", "parameter", "property":
		if readonly {
			return syntax.Constant, true
		}
		if kind == "property" {
			return syntax.Property, true
		}
		return syntax.Variable, true
	case "event":
		return syntax.Property, true
	case "enumMember", "constParameter", "boolean":
		return syntax.Constant, true
	case "macro", "decorator", "attribute", "derive", "deriveHelper":
		return syntax.Macro, true
	case "keyword", "modifier", "selfKeyword":
		return syntax.Keyword, true
	case "comment":
		return syntax.Comment, true
	case "string", "regexp", "escapeSequence", "formatSpecifier":
		return syntax.String, true
	case "number":
		return syntax.Number, true
	case "operator":
		return syntax.Punctuation, true
	case "lifetime":
		return syntax.Lifetime, true
	}
	return syntax.Plain, false
}

// lenUTF16 is char::len_utf16.
func lenUTF16(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// Decode turns the server's relative token data into spans. It reports false
// for data that is malformed, overlapping, or splits a surrogate pair, in
// which case the caller keeps no semantic colours at all.
func Decode(data []uint32, legend Legend, lines [][]rune) ([]Token, bool) {
	if len(data)%5 != 0 {
		return nil, false
	}
	tokens := make([]Token, 0, len(data)/5)
	row := 0
	start := 0
	previousEnd := 0
	var offsets []int
	cachedRow := -1
	readonly := -1
	for i, modifier := range legend.TokenModifiers {
		if modifier == "readonly" {
			if i < 32 {
				readonly = i
			}
			break
		}
	}
	for i := 0; i+5 <= len(data); i += 5 {
		chunk := data[i : i+5]
		row += int(chunk[0])
		if chunk[0] == 0 {
			start += int(chunk[1])
		} else {
			start = int(chunk[1])
		}
		if row >= len(lines) {
			return nil, false
		}
		line := lines[row]
		if cachedRow != row {
			offsets = append(offsets[:0], 0)
			length := len(line)
			if row+1 < len(lines) && length > 0 && line[length-1] == '\r' {
				length--
			}
			for _, c := range line[:length] {
				offsets = append(offsets, offsets[len(offsets)-1]+lenUTF16(c))
			}
			cachedRow = row
			previousEnd = 0
		}
		if chunk[2] == 0 || start < previousEnd || chunk[3] >= 65536 {
			return nil, false
		}
		end := min(start+int(chunk[2]), offsets[len(offsets)-1])
		scalarStart, ok := binarySearch(offsets, start)
		if !ok {
			return nil, false
		}
		scalarEnd, ok := binarySearch(offsets, end)
		if !ok {
			return nil, false
		}
		if scalarEnd <= scalarStart {
			return nil, false
		}
		if int(chunk[3]) >= len(legend.TokenTypes) {
			return nil, false
		}
		kind := legend.TokenTypes[chunk[3]]
		isReadonly := readonly >= 0 && chunk[4]&(1<<readonly) != 0
		if s, ok := style(kind, isReadonly); ok {
			tokens = append(tokens, Token{Row: row, Start: scalarStart, End: scalarEnd, Style: s})
		}
		previousEnd = end
	}
	return tokens, true
}

// binarySearch is slice::binary_search on strictly increasing offsets,
// keeping only the Ok(index) case.
func binarySearch(offsets []int, target int) (int, bool) {
	i := sort.SearchInts(offsets, target)
	return i, i < len(offsets) && offsets[i] == target
}
