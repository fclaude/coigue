package lsp

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// Corpus lines are {"in": {...}, "out": ...} where out is what the Rust
// function returned for in: scalar_position and end_position over generated
// text and positions (emoji, combining marks, CR before LF, huge numbers),
// and Language::for_path over generated file names.
func checkMiscCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<24)
	for scanner.Scan() {
		var entry struct {
			In  map[string]json.RawMessage `json:"in"`
			Out json.RawMessage            `json:"out"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		var op string
		json.Unmarshal(entry.In["op"], &op)
		want := string(entry.Out)
		var got string
		switch op {
		case "pos":
			var lines []string
			json.Unmarshal(entry.In["lines"], &lines)
			line, _ := strconv.ParseUint(string(entry.In["line"]), 10, 64)
			character, _ := strconv.ParseUint(string(entry.In["character"]), 10, 64)
			buffer := make([][]rune, len(lines))
			for i, l := range lines {
				buffer[i] = []rune(l)
			}
			if p, ok := scalarPosition(buffer, wirePosition{line, character}); ok {
				got = "[" + strconv.Itoa(p.Row) + ", " + strconv.Itoa(p.Col) + "]"
			} else {
				got = "null"
			}
			want = compact(t, want)
			got = compact(t, got)
		case "end":
			var text string
			json.Unmarshal(entry.In["text"], &text)
			got = string(marshal(endPosition(text)))
			want = compact(t, want)
		case "language":
			var h string
			json.Unmarshal(entry.In["hex"], &h)
			data, _ := hex.DecodeString(h)
			if l, ok := languageForPath(string(data)); ok {
				program, args := l.command()
				got = string(marshal(object{"id": l.id(), "command": program, "args": argsOrEmpty(args)}))
			} else {
				got = "null"
			}
			want = compact(t, want)
		default:
			t.Fatalf("unknown op %q", op)
		}
		checked++
		if canonicalText(t, got) != canonicalText(t, want) {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("%s %s: got %s, Rust %s", op, scanner.Text(), got, want)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checked, mismatches
}

func argsOrEmpty(args []string) []any {
	out := []any{}
	for _, a := range args {
		out = append(out, a)
	}
	return out
}

func compact(t *testing.T, text string) string { return canonicalText(t, text) }

// canonicalText normalizes JSON text (key order, spacing) through our own
// decoder and encoder.
func canonicalText(t *testing.T, text string) string {
	t.Helper()
	value, err := unmarshal([]byte(text))
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return string(marshal(value))
}

func TestPositionsAndLanguagesAgreeWithRustGolden(t *testing.T) {
	if checked, _ := checkMiscCorpus(t, "testdata/misc_golden.jsonl"); checked < 150 {
		t.Fatalf("only %d golden cases", checked)
	}
}

func TestPositionsAndLanguagesAgreeWithRustCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_MISC_CORPUS")
	if path == "" {
		t.Skip("COIGUE_MISC_CORPUS not set")
	}
	checked, mismatches := checkMiscCorpus(t, path)
	t.Logf("%d cases, %d mismatches", checked, mismatches)
}
