package key

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseSpec reads the text form of a key that init.scm binds: one visible character
// ("x", "X", "ä"), Ctrl and a lowercase letter ("<C-x>") or Alt and one
// visible character ("<A-x>"). Chords and the other special keys have no
// form, because a second key would have to be told apart from the operators
// the editor already waits on, and a key the terminal reports as another one
// could not be told from it.
func ParseSpec(s string) (Key, error) {
	if inner, ok := strings.CutPrefix(s, "<"); ok && s != "<" {
		inner, ok = strings.CutSuffix(inner, ">")
		if !ok {
			return Key{}, syntaxError(s)
		}
		return parseModified(s, inner)
	}
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || size != len(s) || r == utf8.RuneError {
		return Key{}, syntaxError(s)
	}
	if err := visible(s, r); err != nil {
		return Key{}, err
	}
	return Char(r), nil
}

func parseModified(s, inner string) (Key, error) {
	if strings.EqualFold(inner, "esc") || strings.EqualFold(inner, "escape") {
		return Key{}, fmt.Errorf("%q: Escape cannot be bound; it always leaves the mode you are in", s)
	}
	mod, rest, ok := strings.Cut(inner, "-")
	r, size := utf8.DecodeRuneInString(rest)
	if !ok || size == 0 || size != len(rest) || r == utf8.RuneError {
		return Key{}, syntaxError(s)
	}
	switch mod {
	case "C":
		if r < 'a' || r > 'z' {
			return Key{}, fmt.Errorf("%q: Ctrl takes a lowercase letter a to z", s)
		}
		if reason, refused := ctrlRefused[r]; refused {
			return Key{}, fmt.Errorf("%q cannot be bound: %s", s, reason)
		}
		return WithCtrl(r), nil
	case "A":
		if err := visible(s, r); err != nil {
			return Key{}, err
		}
		return Key{Code: Code(r), Mods: Alt}, nil
	}
	return Key{}, syntaxError(s)
}

// ctrlRefused are the Ctrl letters a program cannot bind, with the reason:
// the terminal sends them as another key, or takes them for itself.
var ctrlRefused = map[rune]string{
	'c': "Ctrl-C interrupts a running command and ends the editor's input",
	'z': "Ctrl-Z suspends the editor",
	's': "Ctrl-S and Ctrl-Q stop and start the terminal's output",
	'q': "Ctrl-S and Ctrl-Q stop and start the terminal's output",
	'i': "terminals send Ctrl-I as Tab",
	'm': "terminals send Ctrl-M as Enter",
	'j': "terminals send Ctrl-J as a newline, which they also send for Enter",
	'h': "many terminals send Ctrl-H as Backspace",
}

func visible(s string, r rune) error {
	if unicode.IsSpace(r) || unicode.IsControl(r) || !unicode.IsPrint(r) {
		return fmt.Errorf("%q: a key is one visible character; space and control characters cannot be bound", s)
	}
	return nil
}

func syntaxError(s string) error {
	return fmt.Errorf("%q is not a key: use one character such as \"x\", Ctrl and a lowercase letter such as \"<C-x>\", or Alt and a character such as \"<A-x>\"", s)
}
