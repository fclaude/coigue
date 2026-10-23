package key

import (
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	for spec, want := range map[string]Key{
		"x":       Char('x'),
		"X":       Char('X'),
		"ä":       Char('ä'),
		"界":       Char('界'),
		"<":       Char('<'),
		">":       Char('>'),
		"-":       Char('-'),
		"<C-x>":   WithCtrl('x'),
		"<C-d>":   WithCtrl('d'),
		"<A-x>":   {Code: 'x', Mods: Alt},
		"<A-X>":   {Code: 'X', Mods: Alt},
		"<A-;>":   {Code: ';', Mods: Alt},
		"<A-界>":   {Code: '界', Mods: Alt},
		"<A-<>":   {Code: '<', Mods: Alt},
		"<C-r>":   WithCtrl('r'),
		"<C-a>":   WithCtrl('a'),
		"<A-->":   {Code: '-', Mods: Alt},
		"<A-<A->": {},
	} {
		got, err := ParseSpec(spec)
		if want == (Key{}) {
			if err == nil {
				t.Errorf("%q parsed as %v", spec, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: %v, %v; want %v", spec, got, err, want)
		}
	}
}

func TestParseSpecRefusals(t *testing.T) {
	for spec, want := range map[string]string{
		"":           "is not a key",
		"xy":         "is not a key",
		" ":          "space and control characters",
		"\t":         "space and control characters",
		"\x1b":       "space and control characters",
		"\xff":       "is not a key",
		"<Esc>":      "Escape cannot be bound",
		"<esc>":      "Escape cannot be bound",
		"<Escape>":   "Escape cannot be bound",
		"<C-c>":      "Ctrl-C interrupts",
		"<C-z>":      "suspends",
		"<C-s>":      "stop and start",
		"<C-q>":      "stop and start",
		"<C-i>":      "as Tab",
		"<C-m>":      "as Enter",
		"<C-j>":      "newline",
		"<C-h>":      "Backspace",
		"<C-X>":      "lowercase letter",
		"<C-1>":      "lowercase letter",
		"<C-[>":      "lowercase letter",
		"<C-A-x>":    "is not a key",
		"<S-x>":      "is not a key",
		"<A-xy>":     "is not a key",
		"<A->":       "is not a key",
		"<A- >":      "space and control characters",
		"<Tab>":      "is not a key",
		"<F5>":       "is not a key",
		"<C-x":       "is not a key",
		"C-x":        "is not a key",
		"<>":         "is not a key",
		"<c-x>":      "is not a key",
		"<C-\x00>":   "lowercase letter",
		"<A-\x07>":   "space and control characters",
		"<A-\u200b>": "space and control characters",
	} {
		_, err := ParseSpec(spec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want one containing %q", spec, err, want)
		}
	}
}
