package theme

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// MaxUser is how many themes a program can define.
const MaxUser = 16

// firstUser is the Theme value of the first user-defined theme; the values
// after it follow in definition order.
const firstUser = Terminal + 1

var userThemeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Keys are the palette entries a user theme can override, in the order the
// documentation lists them. All but bold-keywords take a colour.
var Keys = []string{
	"background", "foreground", "muted", "surface",
	"keyword", "comment", "string", "number", "macro", "lifetime", "type",
	"function", "property", "constant", "namespace",
	"error", "warning", "info", "hint",
	"bold-keywords",
}

// Entry is one override of a user theme: Value is a colour string
// ("#rrggbb" or "default") for every key but bold-keywords, which takes a bool.
type Entry struct {
	Key   string
	Value any
}

type userTheme struct {
	name    string
	palette Palette
}

// Set is the themes one editor knows: the built-in ones and the ones the
// user's init file defined. The zero value knows only the built-in ones.
type Set struct {
	user []userTheme
}

// slot is the colour field a key overrides, or nil for a key that is not one.
func (p *Palette) slot(key string) *Color {
	switch key {
	case "background":
		return &p.Background
	case "foreground":
		return &p.Foreground
	case "muted":
		return &p.Muted
	case "surface":
		return &p.Surface
	case "keyword":
		return &p.keyword
	case "comment":
		return &p.comment
	case "string":
		return &p.string
	case "number":
		return &p.number
	case "macro":
		return &p.macroColor
	case "lifetime":
		return &p.lifetime
	case "type":
		return &p.typeColor
	case "function":
		return &p.function
	case "property":
		return &p.property
	case "constant":
		return &p.constant
	case "namespace":
		return &p.namespace
	case "error":
		return &p.error
	case "warning":
		return &p.warning
	case "info":
		return &p.info
	case "hint":
		return &p.hint
	}
	return nil
}

// ParseColor reads "#rrggbb" (either case) or "default", the terminal's own
// colour.
func ParseColor(s string) (Color, error) {
	if s == "default" {
		return Color{Kind: ColorReset}, nil
	}
	if !hexColor.MatchString(s) {
		return Color{}, fmt.Errorf("%q is not a colour: use #rrggbb or default", s)
	}
	n, _ := strconv.ParseUint(s[1:], 16, 32)
	return rgb(uint32(n)), nil
}

// Define adds a theme called name that starts from the built-in theme base
// ("" means mocha) and applies the entries. Nothing changes on an error.
func (s *Set) Define(name, base string, entries []Entry) (Theme, error) {
	if !userThemeName.MatchString(name) {
		return 0, fmt.Errorf("%q is not a theme name: use a lowercase letter, then lowercase letters, digits and -, up to 32 characters", name)
	}
	if _, taken := s.Parse(name); taken {
		return 0, fmt.Errorf("the theme %q already exists", name)
	}
	if len(s.user) >= MaxUser {
		return 0, fmt.Errorf("at most %d themes can be defined", MaxUser)
	}
	if base == "" {
		base = "mocha"
	}
	b, ok := Parse(base)
	if !ok {
		return 0, fmt.Errorf("base %q is not a built-in theme; use %s", base, Choices)
	}
	p := b.Palette()
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Key] {
			return 0, fmt.Errorf("%s is given twice", e.Key)
		}
		seen[e.Key] = true
		if e.Key == "bold-keywords" {
			v, ok := e.Value.(bool)
			if !ok {
				return 0, fmt.Errorf("bold-keywords takes #t or #f")
			}
			p.boldKeywords = v
			continue
		}
		slot := p.slot(e.Key)
		if slot == nil {
			return 0, fmt.Errorf("%q is not a theme entry; use base or %s", e.Key, strings.Join(Keys, ", "))
		}
		text, ok := e.Value.(string)
		if !ok {
			return 0, fmt.Errorf("%s takes a colour string: #rrggbb or default", e.Key)
		}
		c, err := ParseColor(text)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", e.Key, err)
		}
		*slot = c
	}
	s.user = append(s.user, userTheme{name: name, palette: p})
	return firstUser + Theme(len(s.user)-1), nil
}

// Parse is the package's Parse extended with the user-defined themes.
func (s *Set) Parse(name string) (Theme, bool) {
	if t, ok := Parse(name); ok {
		return t, true
	}
	for i, u := range s.user {
		if u.name == name {
			return firstUser + Theme(i), true
		}
	}
	return 0, false
}

// Name is the canonical name of the theme.
func (s *Set) Name(t Theme) string {
	if t >= firstUser && int(t-firstUser) < len(s.user) {
		return s.user[t-firstUser].name
	}
	return t.Name()
}

// Palette is the colours of the theme.
func (s *Set) Palette(t Theme) Palette {
	if t >= firstUser && int(t-firstUser) < len(s.user) {
		return s.user[t-firstUser].palette
	}
	return t.Palette()
}

// Completions is the package's Completions followed by the user-defined names.
func (s *Set) Completions() []string {
	out := Completions()
	for _, u := range s.user {
		out = append(out, u.name)
	}
	return out
}

// Choices is the package's Choices with the user-defined names added.
func (s *Set) Choices() string {
	names := make([]string, 0, len(s.user)+1)
	names = append(names, Choices)
	for _, u := range s.user {
		names = append(names, u.name)
	}
	return strings.Join(slices.Clip(names), "|")
}
