package editor

import (
	"fmt"
	"strings"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/key"
)

// The keys init.scm may bind are decided here, next to the handlers that give
// the others their meaning. TestUnlistedKeysDoNothing keeps the lists below
// honest: a key left out of them must have no effect in its mode, so that a
// change to the handlers which gives one a meaning fails there instead of
// silently shadowing a user's binding.

// neverBound are the keys that keep their meaning whatever the program asks
// for, because without them the editor cannot be left, helped or controlled.
func neverBound(mode string, k key.Key) string {
	switch {
	case k.Mods == 0 && k.Code == ':':
		return "it opens the command line, which is how a bound key is undone"
	case k.Mods == 0 && k.Code == '!' && mode == ext.ModeVisual:
		return "it opens the command line with the selection"
	case k.Mods == key.Ctrl && k.Code == 'o':
		return "Ctrl-O opens the file explorer"
	case k.Mods == key.Ctrl && k.Code == 'p':
		return "Ctrl-P opens the file picker"
	}
	return ""
}

// builtinMeaning says what a key does in a mode before any binding, or "" for
// a key with no meaning there. A binding on such a key needs override.
func builtinMeaning(mode string, k key.Key) string {
	if k.Mods == key.Ctrl {
		switch k.Code {
		case 'a', 'e', 'f', 'b':
			return "Ctrl-" + strings.ToUpper(string(k.Code.Rune())) + " moves like Home, End, Page Down or Page Up"
		case 'r':
			if mode == ext.ModeNormal {
				return "Ctrl-R redoes"
			}
		}
		return ""
	}
	if k.Mods != 0 || !k.Code.IsChar() {
		return ""
	}
	r := k.Code.Rune()
	switch {
	case r >= '0' && r <= '9':
		return "digits are counts and 0 moves to the start of the line"
	case strings.ContainsRune("hjklwbe$gGnN", r):
		return "it is a motion"
	}
	if mode == ext.ModeVisual {
		if strings.ContainsRune("vVoyYd", r) {
			return "it is a Visual command"
		}
		return ""
	}
	if strings.ContainsRune("dcygr><vVxpPuAiao/-", r) {
		return "it is a Normal command"
	}
	return ""
}

// bindable is the editor's part of checking a binding while init.scm loads.
func bindable(mode string, k key.Key, override bool) error {
	if why := neverBound(mode, k); why != "" {
		return fmt.Errorf("%s cannot be bound in %s mode: %s", keyName(k), mode, why)
	}
	if why := builtinMeaning(mode, k); why != "" && !override {
		return fmt.Errorf("%s already has a meaning in %s mode (%s); pass #t as the override argument to replace it", keyName(k), mode, why)
	}
	return nil
}

func keyName(k key.Key) string {
	r := string(k.Code.Rune())
	switch k.Mods {
	case key.Ctrl:
		return "<C-" + r + ">"
	case key.Alt:
		return "<A-" + r + ">"
	}
	return "\"" + r + "\""
}
