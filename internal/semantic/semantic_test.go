package semantic

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/syntax"
)

// bufferLines is Buffer::new(text).lines: split on '\n', keeping any '\r'.
func bufferLines(text string) [][]rune {
	var lines [][]rune
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, []rune(line))
	}
	return lines
}

func testLegend() Legend {
	return Legend{
		TokenTypes:     []string{"function", "variable", "type", "future-token", "comment"},
		TokenModifiers: []string{"readonly"},
	}
}

// Rust: semantic::tests::decodes_relative_utf16_spans_and_modifiers_using_the_server_legend
func TestDecodesRelativeUTF16SpansAndModifiersUsingTheServerLegend(t *testing.T) {
	lines := bufferLines("😀name value\r\nType unknown\n// 😀")
	tokens, ok := Decode([]uint32{
		0, 2, 4, 0, 0, 0, 5, 5, 1, 1, 1, 0, 4, 2, 0, 0, 5, 7, 3, 0, 1, 0, 999, 4, 0,
	}, testLegend(), lines)
	if !ok {
		t.Fatal("decode failed")
	}
	want := []Token{
		{Row: 0, Start: 1, End: 5, Style: syntax.Function},
		{Row: 0, Start: 6, End: 11, Style: syntax.Constant},
		{Row: 1, Start: 0, End: 4, Style: syntax.Type},
		{Row: 2, Start: 0, End: 4, Style: syntax.Comment},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	empty, ok := Decode(nil, testLegend(), lines)
	if !ok || len(empty) != 0 {
		t.Fatalf("empty data = %v, %v; want empty, true", empty, ok)
	}
}

// Rust: semantic::tests::rejects_malformed_overlapping_and_split_surrogate_tokens
func TestRejectsMalformedOverlappingAndSplitSurrogateTokens(t *testing.T) {
	lines := bufferLines("a😀b\nsecond")
	for _, data := range [][]uint32{
		{0},
		{0, 0, 0, 0, 0},
		{0, 2, 1, 0, 0},
		{0, 1, 1, 0, 0},
		{2, 0, 1, 0, 0},
		{0, 9, 1, 0, 0},
		{0, 0, 1, 99, 0},
		{0, 0, 1, 65536, 0},
		{0, 0, 3, 0, 0, 0, 1, 1, 0, 0},
		{0, 0, 1, 0, 0, math.MaxUint32, math.MaxUint32, math.MaxUint32, 0, 0},
	} {
		if tokens, ok := Decode(data, testLegend(), lines); ok {
			t.Errorf("Decode(%v) = %v, want rejection", data, tokens)
		}
	}
}

// Rust: semantic::tests::token_classes_preserve_unknown_fallback_and_map_supported_categories
func TestTokenClassesPreserveUnknownFallbackAndMapSupportedCategories(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		expected syntax.Style
	}{
		{"struct", syntax.Type},
		{"method", syntax.Function},
		{"parameter", syntax.Variable},
		{"property", syntax.Property},
		{"enumMember", syntax.Constant},
		{"namespace", syntax.Namespace},
		{"macro", syntax.Macro},
		{"keyword", syntax.Keyword},
		{"string", syntax.String},
		{"number", syntax.Number},
		{"lifetime", syntax.Lifetime},
		{"operator", syntax.Punctuation},
	} {
		if got, ok := style(tc.kind, false); !ok || got != tc.expected {
			t.Errorf("style(%q, false) = %v, %v; want %v", tc.kind, got, ok, tc.expected)
		}
	}
	if got, ok := style("variable", true); !ok || got != syntax.Constant {
		t.Errorf("style(variable, true) = %v, %v", got, ok)
	}
	if _, ok := style("unknown", false); ok {
		t.Error(`style("unknown") must be absent`)
	}
}

func TestEveryAnnouncedTokenTypeIsColoured(t *testing.T) {
	// Verified against the Rust mapping: every type the client announces has a
	// style, so a server using the announced vocabulary never falls back.
	for _, kind := range Types {
		if _, ok := style(kind, false); !ok {
			t.Errorf("announced token type %q has no style", kind)
		}
	}
	if len(Types) != 35 || len(Modifiers) != 10 {
		t.Fatalf("vocabulary sizes = %d, %d; want 35, 10", len(Types), len(Modifiers))
	}
}

var styleNames = map[string]syntax.Style{
	"Plain": syntax.Plain, "Keyword": syntax.Keyword, "Comment": syntax.Comment,
	"String": syntax.String, "Number": syntax.Number, "Macro": syntax.Macro,
	"Punctuation": syntax.Punctuation, "Lifetime": syntax.Lifetime, "Type": syntax.Type,
	"Function": syntax.Function, "Variable": syntax.Variable, "Property": syntax.Property,
	"Constant": syntax.Constant, "Namespace": syntax.Namespace, "Heading": syntax.Heading,
	"Emphasis": syntax.Emphasis, "Strong": syntax.Strong, "Link": syntax.Link,
}

type decodeCase struct {
	Text      string   `json:"text"`
	Data      []uint32 `json:"data"`
	Types     []string `json:"types"`
	Modifiers []string `json:"modifiers"`
}

// expectedTokens decodes the Rust program's rendering of Option<Vec<Token>>:
// null for None, otherwise [[row, start, end, "Style"], ...].
func expectedTokens(t *testing.T, raw json.RawMessage) ([]Token, bool) {
	t.Helper()
	if string(raw) == "null" {
		return nil, false
	}
	var rows [][]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	tokens := []Token{}
	for _, row := range rows {
		tokens = append(tokens, Token{
			Row: int(row[0].(float64)), Start: int(row[1].(float64)), End: int(row[2].(float64)),
			Style: styleNames[row[3].(string)],
		})
	}
	return tokens, true
}

func checkDecodeLine(t *testing.T, name string, c decodeCase, raw json.RawMessage) {
	t.Helper()
	want, wantOK := expectedTokens(t, raw)
	got, gotOK := Decode(c.Data, Legend{TokenTypes: c.Types, TokenModifiers: c.Modifiers}, bufferLines(c.Text))
	if gotOK != wantOK || (gotOK && !reflect.DeepEqual(got, want)) {
		t.Errorf("%s: Decode(%v) over %q = %v, %v; Rust gave %v, %v", name, c.Data, c.Text, got, gotOK, want, wantOK)
	}
}

// The golden file holds cases run through the Rust decode (src/semantic.rs,
// same source file) by a scratch program; see the report for the generator.
func TestDecodeAgreesWithRustGolden(t *testing.T) {
	file, err := os.Open("testdata/decode_golden.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	n := 0
	for scanner.Scan() {
		var entry struct {
			Case     decodeCase      `json:"case"`
			Expected json.RawMessage `json:"expected"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		n++
		checkDecodeLine(t, "golden "+strconv.Itoa(n), entry.Case, entry.Expected)
	}
	if n < 150 {
		t.Fatalf("only %d golden cases", n)
	}
}

// COIGUE_SEMANTIC_CORPUS=cases.jsonl:expected.jsonl runs a larger corpus
// produced by the same scratch program (30,000 cases in the recorded run).
func TestDecodeAgreesWithRustCorpus(t *testing.T) {
	spec := os.Getenv("COIGUE_SEMANTIC_CORPUS")
	if spec == "" {
		t.Skip("COIGUE_SEMANTIC_CORPUS not set")
	}
	paths := strings.SplitN(spec, ":", 2)
	cases, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	caseLines := strings.Split(strings.TrimSpace(string(cases)), "\n")
	expectedLines := strings.Split(strings.TrimSpace(string(expected)), "\n")
	if len(caseLines) != len(expectedLines) {
		t.Fatalf("%d cases but %d results", len(caseLines), len(expectedLines))
	}
	for i := range caseLines {
		var c decodeCase
		if err := json.Unmarshal([]byte(caseLines[i]), &c); err != nil {
			t.Fatal(err)
		}
		checkDecodeLine(t, "corpus "+strconv.Itoa(i), c, json.RawMessage(expectedLines[i]))
	}
	t.Logf("%d cases agree with Rust", len(caseLines))
}
