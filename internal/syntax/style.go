// Package syntax holds the lightweight, character-indexed lexers that colour
// source text. Styles are theme independent; package theme maps them to colours.
package syntax

// Style classifies one character of the buffer.
type Style uint8

// Styles, in the order of the Rust enum they were ported from.
const (
	Plain Style = iota
	Keyword
	Comment
	String
	Number
	Macro
	Punctuation
	Lifetime
	Type
	Function
	Variable
	Property
	Constant
	Namespace
	Heading
	Emphasis
	Strong
	Link
)
