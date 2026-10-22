package walk

import (
	"bytes"
	"encoding/hex"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLossy(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"café", "café"},
		{"f\xff", "f�"},
		{"\xff\xff", "��"},
		// A truncated three-byte sequence is one replacement, not two.
		{"a\xe2\x82b", "a�b"},
		{"a\xe2\x82", "a�"},
		{"\xe2", "�"},
		{"\xf0\x9f\x98", "�"},
		{"\xf0\x9f\x98x", "�x"},
		{"\xed\xa0\x80", "���"}, // a surrogate is three errors
		{"\xc0\x80", "��"},
		{"ok\xc3", "ok�"},
	}
	for _, c := range cases {
		if got := Lossy(c.in); got != c.want {
			t.Errorf("Lossy(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLossyAgainstRust compares with String::from_utf8_lossy on random bytes.
func TestLossyAgainstRust(t *testing.T) {
	bin := os.Getenv("COIGUE_LOSSY")
	if bin == "" {
		t.Skip("set COIGUE_LOSSY to the scratch lossy binary to run")
	}
	r := rand.New(rand.NewSource(3))
	alphabet := []byte{'a', 'b', 0x00, 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xc1, 0xc2, 0xc3, 0xdf, 0xe0, 0xe1, 0xec, 0xed, 0xee, 0xef, 0xf0, 0xf1, 0xf3, 0xf4, 0xf5, 0xff}
	var in bytes.Buffer
	var inputs []string
	for i := 0; i < 200000; i++ {
		n := r.Intn(9)
		b := make([]byte, n)
		for j := range b {
			b[j] = alphabet[r.Intn(len(alphabet))]
		}
		inputs = append(inputs, string(b))
		in.WriteString(hex.EncodeToString(b) + "\n")
	}
	cmd := exec.Command(bin)
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(inputs) {
		t.Fatalf("%d results for %d inputs", len(lines), len(inputs))
	}
	bad := 0
	for i, s := range inputs {
		if got := hex.EncodeToString([]byte(Lossy(s))); got != lines[i] {
			bad++
			if bad < 5 {
				t.Errorf("Lossy(%x) = %s, Rust %s", s, got, lines[i])
			}
		}
	}
	t.Logf("%d random byte strings compared with Rust, %d mismatches", len(inputs), bad)
}
