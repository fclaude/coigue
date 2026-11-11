package term

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/key"
)

func formatEvent(e Event) string {
	switch e.Type {
	case KeyEvent:
		var code string
		switch c := e.Key.Code; {
		case c.IsChar():
			code = fmt.Sprintf("c:%X", int32(c))
		default:
			if n, ok := c.FunctionKey(); ok {
				code = fmt.Sprintf("f:%d", n)
			} else {
				code = c.String()
			}
		}
		kind := [...]string{"press", "repeat", "release"}[e.Kind]
		return fmt.Sprintf("key %s %d %s", code, e.Key.Mods, kind)
	case PasteEvent:
		return "paste " + strings.ToUpper(hex.EncodeToString([]byte(e.Paste)))
	}
	return "other"
}

// normalize maps crossterm Debug names of keys the editor has no meaning for
// (media, modifier and lock keys) to "Other".
func normalizeGolden(event string) string {
	fields := strings.Fields(event)
	if len(fields) == 4 && fields[0] == "key" {
		switch code := fields[1]; {
		case strings.HasPrefix(code, "c:"), strings.HasPrefix(code, "f:"):
		case code == "Backspace", code == "Enter", code == "Left", code == "Right", code == "Up",
			code == "Down", code == "Home", code == "End", code == "PageUp", code == "PageDown",
			code == "Tab", code == "BackTab", code == "Delete", code == "Insert", code == "Esc":
		default:
			fields[1] = "Other"
		}
		return strings.Join(fields, " ")
	}
	return event
}

func decodeChunks(t *testing.T, label string) [][]byte {
	var chunks [][]byte
	for _, part := range strings.Split(label, "+") {
		b, err := hex.DecodeString(part)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, b)
	}
	return chunks
}

// TestParserMatchesCrossterm replays ~1100 byte sequences against the golden
// recorded from the real crossterm 0.29 event reader on a PTY (the harness is
// in the git tag rust-reference, as testdata/keydump). Inputs
// cover every control byte, Alt-chords, SS3/CSI/tilde/kitty "CSI u" keys with
// modifiers and event kinds, bracketed paste, mouse and report sequences,
// UTF-8 and invalid UTF-8, malformed sequences followed by text, and
// sequences split across two reads.
func TestParserMatchesCrossterm(t *testing.T) {
	f, err := os.Open("testdata/crossterm-keys.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	cases, bad := 0, 0
	for sc.Scan() {
		input, want, _ := strings.Cut(sc.Text(), "\t")
		p := Parser{Raw: true}
		var got []string
		for _, chunk := range decodeChunks(t, input) {
			for _, e := range p.Feed(chunk, false) {
				got = append(got, formatEvent(e))
			}
		}
		var expected []string
		for _, e := range strings.Split(want, ";") {
			if e != "" {
				expected = append(expected, normalizeGolden(e))
			}
		}
		cases++
		if strings.Join(got, ";") != strings.Join(expected, ";") {
			if bad++; bad <= 25 {
				t.Errorf("input %s\n  got      %v\n  crossterm %v", input, got, expected)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d inputs differ from crossterm", bad, cases)
	}
	if cases < 1000 {
		t.Fatalf("only %d golden cases", cases)
	}
}

func TestLoneEscapeNeedsNoMoreInputToBecomeEsc(t *testing.T) {
	p := Parser{Raw: true}
	if got := p.Feed([]byte{0x1b}, true); len(got) != 0 {
		t.Fatalf("an ESC with more input expected must wait, got %v", got)
	}
	got := p.Feed([]byte("[A"), false)
	if len(got) != 1 || got[0].Key != key.Press(key.Up) {
		t.Fatalf("completed sequence = %v", got)
	}
	if got := p.Feed([]byte{0x1b}, false); len(got) != 1 || got[0].Key != key.Press(key.Esc) {
		t.Fatalf("lone ESC = %v", got)
	}
}

func TestLineFeedIsCtrlJInRawModeAndEnterOtherwise(t *testing.T) {
	raw := Parser{Raw: true}
	if got := raw.Feed([]byte{'\n'}, false); got[0].Key != key.WithCtrl('j') {
		t.Fatalf("raw: %v", got[0].Key)
	}
	cooked := Parser{}
	if got := cooked.Feed([]byte{'\n'}, false); got[0].Key != key.Press(key.Enter) {
		t.Fatalf("cooked: %v", got[0].Key)
	}
}

// referenceFeed is Feed without the bracketed-paste shortcut: every byte
// re-parses the whole buffer, as crossterm does.
func referenceFeed(buf *[]byte, data []byte, more, raw bool) []Event {
	var events []Event
	for i, b := range data {
		*buf = append(*buf, b)
		event, st := parseEvent(*buf, i+1 < len(data) || more, raw)
		switch st {
		case statusDone:
			if event.Type != internal {
				events = append(events, event)
			}
			*buf = (*buf)[:0]
		case statusFail:
			*buf = (*buf)[:0]
		}
	}
	return events
}

func TestPasteShortcutMatchesFullReparse(t *testing.T) {
	pieces := []string{"\x1b[200~", "\x1b[201~", "\x1b[20", "0~", "1~", "\x1b", "[", "a", "é", "\r", "\n",
		"\x1b[A", "\x1b[1;5C", "\xff", "~", "2", "0", "😀", "\x1b[200~x\x1b[201~"}
	rng := uint64(1)
	next := func(n int) int {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int(rng>>33) % n
	}
	for c := range 3000 {
		var input []byte
		for range 1 + next(12) {
			input = append(input, pieces[next(len(pieces))]...)
		}
		var chunks [][]byte
		for rest := input; len(rest) > 0; {
			k := 1 + next(len(rest))
			chunks = append(chunks, rest[:k])
			rest = rest[k:]
		}
		p := Parser{Raw: true}
		var ref []byte
		var got, want []string
		for i, chunk := range chunks {
			more := i+1 < len(chunks) && next(2) == 0
			for _, e := range p.Feed(chunk, more) {
				got = append(got, formatEvent(e))
			}
			for _, e := range referenceFeed(&ref, chunk, more, true) {
				want = append(want, formatEvent(e))
			}
		}
		if strings.Join(got, ";") != strings.Join(want, ";") {
			t.Fatalf("case %d input %q\n  got  %v\n  want %v", c, input, got, want)
		}
	}
}

func TestLongBracketedPasteIsOneEvent(t *testing.T) {
	text := strings.Repeat("line of pasted text\r", 50_000)
	p := Parser{Raw: true}
	var events []Event
	data := []byte("\x1b[200~" + text + "\x1b[201~")
	for len(data) > 0 {
		k := min(len(data), readSize)
		events = append(events, p.Feed(data[:k], k == readSize)...)
		data = data[k:]
	}
	if len(events) != 1 || events[0].Type != PasteEvent || events[0].Paste != text {
		t.Fatalf("%d events", len(events))
	}
}
