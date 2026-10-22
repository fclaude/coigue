package lsp

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The corpus lines are "hex(bytes) <TAB> ok:<serde_json::Value::to_string> | err",
// produced by running serde_json::from_slice::<Value> (the version the editor
// is built with) over generated documents and byte mutations of them.
func checkJSONCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<26)
	for scanner.Scan() {
		input, expected, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			t.Fatalf("bad line %q", scanner.Text())
		}
		data, err := hex.DecodeString(input)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		value, parseErr := unmarshal(data)
		got := "err"
		if parseErr == nil {
			got = "ok:" + string(marshal(value))
		}
		if got != expected {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("input %q: got %.200q, Rust %.200q", data, got, expected)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checked, mismatches
}

func TestJSONAgreesWithSerdeJSONGolden(t *testing.T) {
	if checked, _ := checkJSONCorpus(t, "testdata/json_golden.tsv"); checked < 150 {
		t.Fatalf("only %d golden cases", checked)
	}
}

func TestJSONAgreesWithSerdeJSONCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_JSON_CORPUS")
	if path == "" {
		t.Skip("COIGUE_JSON_CORPUS not set")
	}
	checked, mismatches := checkJSONCorpus(t, path)
	t.Logf("%d cases, %d mismatches", checked, mismatches)
}

// Frame corpus lines are "hex(bytes) <TAB> ok:<value> | err:<message>" from
// read_message in src/lsp.rs. Errors from the framing code itself must match
// word for word; for a body serde_json rejects only the fact of the error is
// compared, since its text names positions in its own wording.
var framingErrors = map[string]bool{
	"invalid LSP Content-Length":             true,
	"invalid LSP header":                     true,
	"invalid digit found in string":          true,
	"server closed stdout":                   true,
	"LSP header too large":                   true,
	"duplicate Content-Length":               true,
	"stream did not contain valid UTF-8":     true,
	"cannot parse integer from empty string": true,
	"failed to fill whole buffer":            true,
	"number too large to fit in target type": true,
}

func checkFrameCorpus(t *testing.T, path string) (checked, mismatches int) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<26)
	for scanner.Scan() {
		input, expected, ok := strings.Cut(scanner.Text(), "\t")
		if !ok {
			t.Fatalf("bad line %q", scanner.Text())
		}
		data, err := hex.DecodeString(input)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		value, readErr := readMessage(bufio.NewReader(strings.NewReader(string(data))))
		got := "ok:" + string(marshal(value))
		if readErr != nil {
			got = "err:" + readErr.Error()
			if message, isErr := strings.CutPrefix(expected, "err:"); isErr && !framingErrors[message] {
				expected = got // a serde_json message
				if framingErrors[readErr.Error()] {
					expected = "err:serde_json"
				}
			}
		}
		if got != expected {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("frame %.120q: got %.200q, Rust %.200q", data, got, expected)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return checked, mismatches
}

func TestFramingAgreesWithReadMessageGolden(t *testing.T) {
	if checked, _ := checkFrameCorpus(t, "testdata/frame_golden.tsv"); checked < 150 {
		t.Fatalf("only %d golden cases", checked)
	}
}

func TestFramingAgreesWithReadMessageCorpus(t *testing.T) {
	path := os.Getenv("COIGUE_FRAME_CORPUS")
	if path == "" {
		t.Skip("COIGUE_FRAME_CORPUS not set")
	}
	checked, mismatches := checkFrameCorpus(t, path)
	t.Logf("%d cases, %d mismatches", checked, mismatches)
}
