package term

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/ucd"
)

// EventType says what an Event carries.
type EventType uint8

// Event types. Mouse, focus and terminal-report sequences are parsed so that
// they are consumed, but surface only as Ignored: the editor never requests
// them and drops them if a terminal sends them anyway.
const (
	KeyEvent EventType = iota
	PasteEvent
	ResizeEvent
	Ignored
	// internal marks terminal replies (cursor position, keyboard flags, device
	// attributes) that crossterm consumes without delivering to the application.
	internal
)

// KeyKind distinguishes presses from repeats and releases; only enhanced
// keyboard protocols report the latter two.
type KeyKind uint8

// Key kinds.
const (
	Press KeyKind = iota
	Repeat
	Release
)

// Event is one decoded input event.
type Event struct {
	Type  EventType
	Key   key.Key
	Kind  KeyKind
	Paste string
}

type status uint8

const (
	statusNeedMore status = iota // Ok(None): wait for more bytes
	statusDone                   // Ok(Some(event)): consume the buffer
	statusFail                   // Err: drop the buffer
)

func keyEvent(code key.Code, mods key.Mods) (Event, status) {
	return Event{Type: KeyEvent, Key: key.New(code, mods)}, statusDone
}

func ignored() (Event, status) { return Event{Type: Ignored}, statusDone }

func swallowed() (Event, status) { return Event{Type: internal}, statusDone }

// Parser turns the bytes read from a terminal into events. It mirrors
// crossterm 0.29's Unix parser byte for byte, including its quirks: input is
// appended one byte at a time and re-parsed until the buffer forms an event
// (kept), is invalid (dropped), or needs more bytes (kept); a lone ESC is Esc
// only when no more bytes are expected in the same read; and ESC ESC is a
// single Esc.
type Parser struct {
	buf []byte
	// Raw reports whether the terminal is in raw mode. In raw mode a line
	// feed is Ctrl-J rather than Enter, as the terminal no longer maps CR to LF.
	Raw bool
}

// Feed appends data and returns the events it completes. more says whether
// further bytes are likely waiting (a read that filled the whole buffer), which
// keeps a trailing ESC pending instead of reporting it as the Esc key.
func (p *Parser) Feed(data []byte, more bool) []Event {
	var events []Event
	for i, b := range data {
		p.buf = append(p.buf, b)
		moreAfter := i+1 < len(data) || more
		event, st := parseEvent(p.buf, moreAfter, p.Raw)
		switch st {
		case statusDone:
			if event.Type != internal {
				events = append(events, event)
			}
			p.buf = p.buf[:0]
		case statusFail:
			p.buf = p.buf[:0]
		}
	}
	return events
}

func parseEvent(buffer []byte, inputAvailable, raw bool) (Event, status) {
	if len(buffer) == 0 {
		return Event{}, statusNeedMore
	}
	switch c := buffer[0]; {
	case c == 0x1b:
		if len(buffer) == 1 {
			if inputAvailable {
				return Event{}, statusNeedMore // possible escape sequence
			}
			return keyEvent(key.Esc, 0)
		}
		switch buffer[1] {
		case 'O':
			if len(buffer) == 2 {
				return Event{}, statusNeedMore
			}
			switch v := buffer[2]; {
			case v == 'D':
				return keyEvent(key.Left, 0)
			case v == 'C':
				return keyEvent(key.Right, 0)
			case v == 'A':
				return keyEvent(key.Up, 0)
			case v == 'B':
				return keyEvent(key.Down, 0)
			case v == 'H':
				return keyEvent(key.Home, 0)
			case v == 'F':
				return keyEvent(key.End, 0)
			case v >= 'P' && v <= 'S':
				return keyEvent(key.F(1+int(v-'P')), 0)
			}
			return Event{}, statusFail
		case '[':
			return parseCSI(buffer, raw)
		case 0x1b:
			return keyEvent(key.Esc, 0)
		}
		event, st := parseEvent(buffer[1:], inputAvailable, raw)
		if st == statusDone && event.Type == KeyEvent {
			event.Key.Mods |= key.Alt
		}
		return event, st
	case c == '\r':
		return keyEvent(key.Enter, 0)
	case c == '\n' && !raw:
		return keyEvent(key.Enter, 0)
	case c == '\t':
		return keyEvent(key.Tab, 0)
	case c == 0x7f:
		return keyEvent(key.Backspace, 0)
	case c >= 0x01 && c <= 0x1a:
		return keyEvent(key.Code(rune(c-1+'a')), key.Ctrl)
	case c >= 0x1c && c <= 0x1f:
		return keyEvent(key.Code(rune(c-0x1c+'4')), key.Ctrl)
	case c == 0:
		return keyEvent(key.Code(' '), key.Ctrl)
	}
	r, st := parseUTF8Char(buffer)
	if st != statusDone {
		return Event{}, st
	}
	var mods key.Mods
	if ucd.IsUppercase(r) {
		mods = key.Shift // crossterm marks uppercase characters as shifted
	}
	return keyEvent(key.Code(r), mods)
}

// parseUTF8Char decodes the first scalar of buffer, waiting for more bytes
// while a multi-byte sequence is valid so far.
func parseUTF8Char(buffer []byte) (rune, status) {
	if utf8.Valid(buffer) {
		r, _ := utf8.DecodeRune(buffer)
		return r, statusDone
	}
	var required int
	switch b := buffer[0]; {
	case b <= 0x7f:
		required = 1
	case b >= 0xc0 && b <= 0xdf:
		required = 2
	case b >= 0xe0 && b <= 0xef:
		required = 3
	case b >= 0xf0 && b <= 0xf7:
		required = 4
	default:
		return 0, statusFail
	}
	if required > 1 && len(buffer) > 1 {
		for _, b := range buffer[1:] {
			if b&^0b0011_1111 != 0b1000_0000 {
				return 0, statusFail
			}
		}
	}
	if len(buffer) < required {
		return 0, statusNeedMore
	}
	return 0, statusFail
}

func parseCSI(buffer []byte, raw bool) (Event, status) {
	if len(buffer) == 2 {
		return Event{}, statusNeedMore
	}
	switch c := buffer[2]; {
	case c == '[':
		if len(buffer) == 3 {
			return Event{}, statusNeedMore
		}
		if v := buffer[3]; v >= 'A' && v <= 'E' {
			return keyEvent(key.F(1+int(v-'A')), 0)
		}
		return Event{}, statusFail
	case c == 'D':
		return keyEvent(key.Left, 0)
	case c == 'C':
		return keyEvent(key.Right, 0)
	case c == 'A':
		return keyEvent(key.Up, 0)
	case c == 'B':
		return keyEvent(key.Down, 0)
	case c == 'H':
		return keyEvent(key.Home, 0)
	case c == 'F':
		return keyEvent(key.End, 0)
	case c == 'Z':
		return keyEvent(key.BackTab, key.Shift)
	case c == 'M':
		return parseNormalMouse(buffer)
	case c == '<':
		return parseSGRMouse(buffer)
	case c == 'I' || c == 'O':
		return ignored() // focus gained / lost
	case c == ';':
		return parseModifierKeyCode(buffer)
	case c == 'P':
		return keyEvent(key.F(1), 0)
	case c == 'Q':
		return keyEvent(key.F(2), 0)
	case c == 'S':
		return keyEvent(key.F(4), 0)
	case c == '?':
		switch buffer[len(buffer)-1] {
		case 'u', 'c':
			return swallowed() // keyboard-enhancement flags or device attributes report
		}
		return Event{}, statusNeedMore
	case c >= '0' && c <= '9':
		if len(buffer) == 3 {
			return Event{}, statusNeedMore
		}
		// The final byte of a CSI sequence is in 64..=126; keep reading anything else.
		last := buffer[len(buffer)-1]
		if last < 64 || last > 126 {
			return Event{}, statusNeedMore
		}
		if strings.HasPrefix(string(buffer), "\x1b[200~") {
			return parseBracketedPaste(buffer)
		}
		switch last {
		case 'M':
			return parseRxvtMouse(buffer)
		case '~':
			return parseSpecialKeyCode(buffer)
		case 'u':
			return parseCSIu(buffer, raw)
		case 'R':
			return parseCursorPosition(buffer)
		}
		return parseModifierKeyCode(buffer)
	}
	return Event{}, statusFail
}

// parseUint parses a decimal number with the bit size of the Rust type the
// crossterm parser reads it into; anything else is a parse failure.
func parseUint(s string, bits int) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, bits)
	return n, err == nil && s != "" && s[0] != '+'
}

func parseModifiers(mask uint8) key.Mods {
	if mask > 0 {
		mask--
	}
	var mods key.Mods
	if mask&1 != 0 {
		mods |= key.Shift
	}
	if mask&2 != 0 {
		mods |= key.Alt
	}
	if mask&4 != 0 {
		mods |= key.Ctrl
	}
	if mask&8 != 0 {
		mods |= key.Super
	}
	if mask&16 != 0 {
		mods |= key.Hyper
	}
	if mask&32 != 0 {
		mods |= key.Meta
	}
	return mods
}

// modifierAndKind parses "mask" or "mask:kind" (kind defaults to press).
func modifierAndKind(field string) (mask uint8, kind uint8, ok bool) {
	parts := strings.Split(field, ":")
	m, ok := parseUint(parts[0], 8)
	if !ok {
		return 0, 0, false
	}
	kind = 1
	if len(parts) > 1 {
		if k, ok := parseUint(parts[1], 8); ok {
			kind = uint8(k)
		}
	}
	return uint8(m), kind, true
}

func keyKind(kind uint8) KeyKind {
	switch kind {
	case 2:
		return Repeat
	case 3:
		return Release
	}
	return Press
}

func csiParams(buffer []byte, from int) ([]string, bool) {
	body := buffer[from : len(buffer)-1]
	if !utf8.Valid(body) {
		return nil, false
	}
	return strings.Split(string(body), ";"), true
}

func parseModifierKeyCode(buffer []byte) (Event, status) {
	parts, ok := csiParams(buffer, 2)
	if !ok {
		return Event{}, statusFail
	}
	var mods key.Mods
	var kind KeyKind
	mask, rawKind, found := uint8(0), uint8(0), false
	if len(parts) > 1 {
		mask, rawKind, found = modifierAndKind(parts[1])
	}
	switch {
	case found:
		mods, kind = parseModifiers(mask), keyKind(rawKind)
	case len(buffer) > 3:
		d := buffer[len(buffer)-2]
		if d < '0' || d > '9' {
			return Event{}, statusFail
		}
		mods = parseModifiers(d - '0')
	}
	var code key.Code
	switch buffer[len(buffer)-1] {
	case 'A':
		code = key.Up
	case 'B':
		code = key.Down
	case 'C':
		code = key.Right
	case 'D':
		code = key.Left
	case 'F':
		code = key.End
	case 'H':
		code = key.Home
	case 'P':
		code = key.F(1)
	case 'Q':
		code = key.F(2)
	case 'R':
		code = key.F(3)
	case 'S':
		code = key.F(4)
	default:
		return Event{}, statusFail
	}
	return Event{Type: KeyEvent, Key: key.New(code, mods), Kind: kind}, statusDone
}

func parseSpecialKeyCode(buffer []byte) (Event, status) {
	parts, ok := csiParams(buffer, 2)
	if !ok {
		return Event{}, statusFail
	}
	first, ok := parseUint(parts[0], 8)
	if !ok {
		return Event{}, statusFail
	}
	var mods key.Mods
	var kind KeyKind
	if len(parts) > 1 {
		if mask, k, ok := modifierAndKind(parts[1]); ok {
			mods, kind = parseModifiers(mask), keyKind(k)
		}
	}
	var code key.Code
	switch v := int(first); {
	case v == 1 || v == 7:
		code = key.Home
	case v == 2:
		code = key.Insert
	case v == 3:
		code = key.Delete
	case v == 4 || v == 8:
		code = key.End
	case v == 5:
		code = key.PageUp
	case v == 6:
		code = key.PageDown
	case v >= 11 && v <= 15:
		code = key.F(v - 10)
	case v >= 17 && v <= 21:
		code = key.F(v - 11)
	case v >= 23 && v <= 26:
		code = key.F(v - 12)
	case v >= 28 && v <= 29:
		code = key.F(v - 15)
	case v >= 31 && v <= 34:
		code = key.F(v - 17)
	default:
		return Event{}, statusFail
	}
	return Event{Type: KeyEvent, Key: key.New(code, mods), Kind: kind}, statusDone
}

// functionalKey maps the private-use code points of the kitty keyboard
// protocol to keys.
func functionalKey(codepoint uint32) (key.Code, bool) {
	switch {
	case codepoint >= 57399 && codepoint <= 57408:
		return key.Code('0' + rune(codepoint-57399)), true
	case codepoint == 57409:
		return '.', true
	case codepoint == 57410:
		return '/', true
	case codepoint == 57411:
		return '*', true
	case codepoint == 57412:
		return '-', true
	case codepoint == 57413:
		return '+', true
	case codepoint == 57414:
		return key.Enter, true
	case codepoint == 57415:
		return '=', true
	case codepoint == 57416:
		return ',', true
	case codepoint == 57417:
		return key.Left, true
	case codepoint == 57418:
		return key.Right, true
	case codepoint == 57419:
		return key.Up, true
	case codepoint == 57420:
		return key.Down, true
	case codepoint == 57421:
		return key.PageUp, true
	case codepoint == 57422:
		return key.PageDown, true
	case codepoint == 57423:
		return key.Home, true
	case codepoint == 57424:
		return key.End, true
	case codepoint == 57425:
		return key.Insert, true
	case codepoint == 57426:
		return key.Delete, true
	case codepoint >= 57376 && codepoint <= 57398:
		return key.F(13 + int(codepoint-57376)), true
	case codepoint == 57427, // keypad begin
		codepoint >= 57358 && codepoint <= 57363, // locks, print screen, pause, menu
		codepoint >= 57428 && codepoint <= 57454: // media and modifier keys
		return key.Other, true
	}
	return 0, false
}

func parseCSIu(buffer []byte, raw bool) (Event, status) {
	parts, ok := csiParams(buffer, 2)
	if !ok {
		return Event{}, statusFail
	}
	codepoints := strings.Split(parts[0], ":")
	cp, ok := parseUint(codepoints[0], 32)
	if !ok {
		return Event{}, statusFail
	}
	var mods key.Mods
	var kind KeyKind
	if len(parts) > 1 {
		if mask, k, ok := modifierAndKind(parts[1]); ok {
			mods, kind = parseModifiers(mask), keyKind(k)
		}
	}
	var code key.Code
	if special, ok := functionalKey(uint32(cp)); ok {
		code = special
	} else if r := rune(cp); utf8.ValidRune(r) {
		switch {
		case r == 0x1b:
			code = key.Esc
		case r == '\r':
			code = key.Enter
		case r == '\n' && !raw:
			code = key.Enter
		case r == '\t':
			if mods.Has(key.Shift) {
				code = key.BackTab
			} else {
				code = key.Tab
			}
		case r == 0x7f:
			code = key.Backspace
		default:
			code = key.Code(r)
		}
	} else {
		return Event{}, statusFail
	}
	switch cp {
	case 57443, 57449: // alt
		mods |= key.Alt
	case 57442, 57448: // control
		mods |= key.Ctrl
	case 57441, 57447: // shift
		mods |= key.Shift
	case 57444, 57450: // super
		mods |= key.Super
	case 57445, 57451: // hyper
		mods |= key.Hyper
	case 57446, 57452: // meta
		mods |= key.Meta
	}
	// With "report alternate keys" the shifted character follows a ':'.
	if mods.Has(key.Shift) && len(codepoints) > 1 {
		if shifted, ok := parseUint(codepoints[1], 32); ok && utf8.ValidRune(rune(shifted)) {
			code = key.Code(rune(shifted))
			mods &^= key.Shift
		}
	}
	return Event{Type: KeyEvent, Key: key.New(code, mods), Kind: kind}, statusDone
}

func parseCursorPosition(buffer []byte) (Event, status) {
	parts, ok := csiParams(buffer, 2)
	if !ok || len(parts) < 2 {
		return Event{}, statusFail
	}
	for _, part := range parts[:2] {
		if n, ok := parseUint(part, 16); !ok || n == 0 {
			return Event{}, statusFail
		}
	}
	return swallowed()
}

func parseBracketedPaste(buffer []byte) (Event, status) {
	if !strings.HasSuffix(string(buffer), "\x1b[201~") {
		return Event{}, statusNeedMore
	}
	text := strings.ToValidUTF8(string(buffer[6:len(buffer)-6]), "�")
	return Event{Type: PasteEvent, Paste: text}, statusDone
}

// parseCb validates a mouse button byte; the editor never uses mouse events,
// so only whether it is one crossterm accepts matters.
func parseCb(cb uint8) bool {
	button := (cb & 0b0000_0011) | ((cb & 0b1100_0000) >> 4)
	dragging := cb&0b0010_0000 != 0
	switch {
	case button <= 2:
		return true
	case button == 3:
		return true
	case dragging && (button == 4 || button == 5):
		return true
	case !dragging && button >= 4 && button <= 7:
		return true
	}
	return false
}

func parseNormalMouse(buffer []byte) (Event, status) {
	if len(buffer) < 6 {
		return Event{}, statusNeedMore
	}
	if buffer[3] < 32 || !parseCb(buffer[3]-32) || buffer[4] < 33 || buffer[5] < 33 {
		return Event{}, statusFail
	}
	return ignored()
}

func parseRxvtMouse(buffer []byte) (Event, status) {
	parts, ok := csiParams(buffer, 2)
	if !ok || len(parts) < 3 {
		return Event{}, statusFail
	}
	cb, ok := parseUint(parts[0], 8)
	if !ok || cb < 32 || !parseCb(uint8(cb-32)) {
		return Event{}, statusFail
	}
	for _, part := range parts[1:3] {
		if n, ok := parseUint(part, 16); !ok || n == 0 {
			return Event{}, statusFail
		}
	}
	return ignored()
}

func parseSGRMouse(buffer []byte) (Event, status) {
	last := buffer[len(buffer)-1]
	if last != 'm' && last != 'M' {
		return Event{}, statusNeedMore
	}
	parts, ok := csiParams(buffer, 3)
	if !ok || len(parts) < 3 {
		return Event{}, statusFail
	}
	cb, ok := parseUint(parts[0], 8)
	if !ok || !parseCb(uint8(cb)) {
		return Event{}, statusFail
	}
	for _, part := range parts[1:3] {
		if n, ok := parseUint(part, 16); !ok || n == 0 {
			return Event{}, statusFail
		}
	}
	return ignored()
}
