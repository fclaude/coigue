// Package theme holds the built-in palettes. Terminal uses the user's existing ANSI colors.
// Catppuccin and Gruvbox palette sources: https://github.com/catppuccin/catppuccin and
// https://github.com/morhetz/gruvbox (both MIT licensed).
// Monochrome uses an original grayscale palette.
// Attribution and license terms are in THIRD_PARTY_NOTICES.md.
package theme

import (
	"slices"

	"github.com/fclaude/coigue/internal/syntax"
)

// Theme selects one of the built-in palettes. The zero value is the default,
// Catppuccin Mocha. Theme values compare with ==.
type Theme uint8

// Themes, in the order of the Rust enum they were ported from.
const (
	CatppuccinMocha Theme = iota
	GruvboxDark
	CatppuccinLatte
	Monochrome
	Terminal
)

// Choices is the short list shown in prompts and usage messages.
const Choices = "mocha|gruvbox|latte|monochrome|terminal"

var completions = [...]string{
	"mocha",
	"gruvbox",
	"latte",
	"monochrome",
	"terminal",
	"catppuccin-mocha",
	"catppuccin-latte",
	"gruvbox-dark",
}

// Completions returns the names the :theme command offers, each accepted by
// Parse. The slice is a fresh copy.
func Completions() []string {
	return slices.Clone(completions[:])
}

// Name is the canonical name of the theme. A value outside the defined
// themes, which the Rust enum cannot express, is reported as the default.
func (t Theme) Name() string {
	switch t {
	case CatppuccinMocha:
		return "catppuccin-mocha"
	case GruvboxDark:
		return "gruvbox-dark"
	case CatppuccinLatte:
		return "catppuccin-latte"
	case Monochrome:
		return "monochrome"
	case Terminal:
		return "terminal"
	}
	return CatppuccinMocha.Name()
}

// Parse accepts the canonical names and the short aliases, case-sensitively.
func Parse(name string) (Theme, bool) {
	switch name {
	case "catppuccin-mocha", "mocha":
		return CatppuccinMocha, true
	case "gruvbox-dark", "gruvbox":
		return GruvboxDark, true
	case "catppuccin-latte", "latte":
		return CatppuccinLatte, true
	case "monochrome":
		return Monochrome, true
	case "terminal":
		return Terminal, true
	}
	return 0, false
}

// Palette returns the colours of the theme. A value outside the defined
// themes gets the default palette.
func (t Theme) Palette() Palette {
	switch t {
	case CatppuccinMocha:
		return Palette{
			boldKeywords: false,
			Background:   rgb(0x1e1e2e),
			Foreground:   rgb(0xcdd6f4),
			Muted:        rgb(0x7f849c),
			Surface:      rgb(0x313244),
			keyword:      rgb(0xcba6f7),
			comment:      rgb(0x7f849c),
			string:       rgb(0xa6e3a1),
			number:       rgb(0xfab387),
			macroColor:   rgb(0x89b4fa),
			lifetime:     rgb(0xf9e2af),
			typeColor:    rgb(0xf9e2af),
			function:     rgb(0x89b4fa),
			property:     rgb(0xb4befe),
			constant:     rgb(0xfab387),
			namespace:    rgb(0x94e2d5),
			error:        rgb(0xf38ba8),
			warning:      rgb(0xf9e2af),
			info:         rgb(0x89dceb),
			hint:         rgb(0x94e2d5),
		}
	case GruvboxDark:
		return Palette{
			boldKeywords: false,
			Background:   rgb(0x282828),
			Foreground:   rgb(0xebdbb2),
			Muted:        rgb(0x928374),
			Surface:      rgb(0x3c3836),
			keyword:      rgb(0xfb4934),
			comment:      rgb(0x928374),
			string:       rgb(0xb8bb26),
			number:       rgb(0xd3869b),
			macroColor:   rgb(0xfabd2f),
			lifetime:     rgb(0x8ec07c),
			typeColor:    rgb(0xfabd2f),
			function:     rgb(0xb8bb26),
			property:     rgb(0x83a598),
			constant:     rgb(0xd3869b),
			namespace:    rgb(0x8ec07c),
			error:        rgb(0xfb4934),
			warning:      rgb(0xfabd2f),
			info:         rgb(0x83a598),
			hint:         rgb(0x8ec07c),
		}
	case CatppuccinLatte:
		return Palette{
			boldKeywords: false,
			Background:   rgb(0xeff1f5),
			Foreground:   rgb(0x4c4f69),
			Muted:        rgb(0x6c6f85),
			Surface:      rgb(0xccd0da),
			keyword:      rgb(0x8839ef),
			comment:      rgb(0x6c6f85),
			string:       rgb(0x40a02b),
			number:       rgb(0xfe640b),
			macroColor:   rgb(0x1e66f5),
			lifetime:     rgb(0xdf8e1d),
			typeColor:    rgb(0xdf8e1d),
			function:     rgb(0x1e66f5),
			property:     rgb(0x7287fd),
			constant:     rgb(0xfe640b),
			namespace:    rgb(0x179299),
			error:        rgb(0xd20f39),
			warning:      rgb(0xdf8e1d),
			info:         rgb(0x209fb5),
			hint:         rgb(0x179299),
		}
	case Monochrome:
		return Palette{
			boldKeywords: true,
			Background:   rgb(0x080808),
			Foreground:   rgb(0xb0b0b0),
			Muted:        rgb(0x5e5e5e),
			Surface:      rgb(0x303030),
			keyword:      rgb(0xffffff),
			comment:      rgb(0x7c7c7c),
			string:       rgb(0xd8d8d8),
			number:       rgb(0x989898),
			macroColor:   rgb(0xffffff),
			lifetime:     rgb(0xd8d8d8),
			typeColor:    rgb(0xd8d8d8),
			function:     rgb(0xffffff),
			property:     rgb(0xb0b0b0),
			constant:     rgb(0x989898),
			namespace:    rgb(0x989898),
			error:        rgb(0xffffff),
			warning:      rgb(0xd8d8d8),
			info:         rgb(0xb0b0b0),
			hint:         rgb(0x7c7c7c),
		}
	case Terminal:
		return Palette{
			boldKeywords: false,
			Background:   reset,
			Foreground:   reset,
			Muted:        darkGrey,
			Surface:      reset,
			keyword:      magenta,
			comment:      darkGrey,
			string:       green,
			number:       cyan,
			macroColor:   yellow,
			lifetime:     blue,
			typeColor:    yellow,
			function:     blue,
			property:     cyan,
			constant:     cyan,
			namespace:    blue,
			error:        red,
			warning:      yellow,
			info:         cyan,
			hint:         cyan,
		}
	}
	return CatppuccinMocha.Palette()
}

// ColorKind says which fields of a Color are meaningful.
type ColorKind uint8

const (
	// ColorReset is the terminal's default colour (SGR 39 / 49); the zero Color.
	ColorReset ColorKind = iota
	// ColorAnsi is an entry of the 256-colour palette, Color.Index (SGR 38;5;N).
	ColorAnsi
	// ColorRGB is a 24-bit colour, Color.R, Color.G and Color.B (SGR 38;2;R;G;B).
	ColorRGB
)

// Color is crossterm's style::Color reduced to what the palettes use. The
// named colours crossterm offers are stored as the palette index it emits for
// them. Color values compare with ==.
type Color struct {
	Kind    ColorKind
	Index   uint8
	R, G, B uint8
}

func rgb(hex uint32) Color {
	return Color{Kind: ColorRGB, R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex)}
}

func ansi(index uint8) Color {
	return Color{Kind: ColorAnsi, Index: index}
}

// The crossterm named colours the Terminal theme uses, as the palette index
// crossterm writes for each (Color::DarkGrey is "5;8", Color::Red "5;9", ...).
var (
	reset    = Color{Kind: ColorReset}
	darkGrey = ansi(8)
	red      = ansi(9)
	green    = ansi(10)
	yellow   = ansi(11)
	blue     = ansi(12)
	magenta  = ansi(13)
	cyan     = ansi(14)
)

// Palette is the set of colours of one theme.
type Palette struct {
	boldKeywords bool
	Background   Color
	Foreground   Color
	Muted        Color
	Surface      Color
	keyword      Color
	comment      Color
	string       Color
	number       Color
	macroColor   Color
	lifetime     Color
	typeColor    Color
	function     Color
	property     Color
	constant     Color
	namespace    Color
	error        Color
	warning      Color
	info         Color
	hint         Color
}

// Bold reports whether text of the style is drawn bold.
func (p Palette) Bold(style syntax.Style) bool {
	return style == syntax.Heading || style == syntax.Strong ||
		p.boldKeywords && (style == syntax.Keyword || style == syntax.Macro)
}

// Syntax is the foreground colour of text of the style.
func (p Palette) Syntax(style syntax.Style) Color {
	switch style {
	case syntax.Punctuation:
		return p.Foreground
	case syntax.Keyword, syntax.Heading:
		return p.keyword
	case syntax.Comment:
		return p.comment
	case syntax.String:
		return p.string
	case syntax.Number:
		return p.number
	case syntax.Macro, syntax.Strong:
		return p.macroColor
	case syntax.Lifetime, syntax.Emphasis:
		return p.lifetime
	case syntax.Type:
		return p.typeColor
	case syntax.Function, syntax.Link:
		return p.function
	case syntax.Plain, syntax.Variable:
		return p.Foreground
	case syntax.Property:
		return p.property
	case syntax.Constant:
		return p.constant
	case syntax.Namespace:
		return p.namespace
	}
	return p.Foreground
}

// Diagnostic is the colour of a language-server severity: 1 error, 2 warning,
// 3 information, anything else hint.
func (p Palette) Diagnostic(severity uint8) Color {
	switch severity {
	case 1:
		return p.error
	case 2:
		return p.warning
	case 3:
		return p.info
	}
	return p.hint
}
