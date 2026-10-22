// Package key defines the keyboard event vocabulary shared by the terminal
// input parser, the editor and every overlay that reacts to keys. It mirrors
// the subset of crossterm's KeyCode/KeyModifiers that Coigüe relies on.
package key

import "fmt"

// Code identifies a key. Non-negative values are Unicode scalar values
// (crossterm's KeyCode::Char); negative values are the special keys below.
type Code int32

// Special keys. F(n) lies below fBase.
const (
	Esc Code = -(iota + 1)
	Enter
	Tab
	BackTab
	Backspace
	Delete
	Insert
	Left
	Right
	Up
	Down
	Home
	End
	PageUp
	PageDown
	// Other stands for keys the editor has no meaning for (media, modifier and
	// lock keys reported by enhanced keyboard protocols). Like any unknown
	// key it still discards a pending operator in Normal mode.
	Other
)

const fBase Code = -100

// F returns the code of function key n (1-based).
func F(n int) Code { return fBase - Code(n) }

// IsChar reports whether the code is an ordinary character.
func (c Code) IsChar() bool { return c >= 0 }

// Rune returns the character of a Char code, or utf8.RuneError semantics
// are left to the caller: it returns -1 for special keys.
func (c Code) Rune() rune {
	if c >= 0 {
		return rune(c)
	}
	return -1
}

// FunctionKey returns n when the code is function key F(n).
func (c Code) FunctionKey() (int, bool) {
	if c < fBase {
		return int(fBase - c), true
	}
	return 0, false
}

func (c Code) String() string {
	switch {
	case c >= 0:
		return fmt.Sprintf("%q", rune(c))
	case c < fBase:
		return fmt.Sprintf("F%d", fBase-c)
	}
	switch c {
	case Esc:
		return "Esc"
	case Enter:
		return "Enter"
	case Tab:
		return "Tab"
	case BackTab:
		return "BackTab"
	case Backspace:
		return "Backspace"
	case Delete:
		return "Delete"
	case Insert:
		return "Insert"
	case Left:
		return "Left"
	case Right:
		return "Right"
	case Up:
		return "Up"
	case Down:
		return "Down"
	case Home:
		return "Home"
	case End:
		return "End"
	case PageUp:
		return "PageUp"
	case PageDown:
		return "PageDown"
	case Other:
		return "Other"
	}
	return fmt.Sprintf("Code(%d)", int32(c))
}

// Mods is a set of modifier keys, with crossterm's bit layout.
type Mods uint8

// Modifier bits.
const (
	Shift Mods = 1 << iota
	Ctrl
	Alt
	Super
	Hyper
	Meta
)

// None is the empty modifier set.
const None Mods = 0

// Has reports whether every modifier in m2 is present.
func (m Mods) Has(m2 Mods) bool { return m&m2 == m2 }

// Intersects reports whether any modifier in m2 is present.
func (m Mods) Intersects(m2 Mods) bool { return m&m2 != 0 }

// Key is one key press.
type Key struct {
	Code Code
	Mods Mods
}

// New builds a Key.
func New(code Code, mods Mods) Key { return Key{Code: code, Mods: mods} }

// Char builds the unmodified key for a character.
func Char(r rune) Key { return Key{Code: Code(r)} }

// Press builds an unmodified key for a special code.
func Press(code Code) Key { return Key{Code: code} }

// WithCtrl builds Ctrl+character.
func WithCtrl(r rune) Key { return Key{Code: Code(r), Mods: Ctrl} }
