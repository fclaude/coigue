package picker

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/ucd"
)

// TestScoreAgainstRust compares Score with the editor's score() on random
// labels and queries, including characters whose lower-casing is special.
// COIGUE_SCORE names the scratch binary built from src/picker.rs.
func TestScoreAgainstRust(t *testing.T) {
	bin := os.Getenv("COIGUE_SCORE")
	if bin == "" {
		t.Skip("set COIGUE_SCORE to the scratch score binary to run")
	}
	alphabet := []rune("abcdeAB/_-. ÉéİßΣσςǅẞﬃ日本xyz")
	r := rand.New(rand.NewSource(5))
	random := func(max int) string {
		n := r.Intn(max + 1)
		out := make([]rune, n)
		for i := range out {
			out[i] = alphabet[r.Intn(len(alphabet))]
		}
		return string(out)
	}
	type pair struct{ label, query string }
	var pairs []pair
	var in bytes.Buffer
	for i := 0; i < 100000; i++ {
		label := random(14)
		query := random(5)
		if i%2 == 0 {
			query = ucd.Lower(query) // the editor lower-cases before ranking
		}
		pairs = append(pairs, pair{label, query})
		fmt.Fprintf(&in, "%s\t%s\n", hex.EncodeToString([]byte(label)), hex.EncodeToString([]byte(query)))
	}
	cmd := exec.Command(bin)
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(pairs) {
		t.Fatalf("%d answers for %d questions", len(lines), len(pairs))
	}
	matched, bad := 0, 0
	for i, p := range pairs {
		got, ok := Score(p.label, p.query)
		want := "none"
		if ok {
			want = strconv.FormatInt(got, 10)
			matched++
		}
		if lines[i] != want {
			bad++
			if bad < 5 {
				t.Errorf("Score(%q, %q) = %s, Rust says %s", p.label, p.query, want, lines[i])
			}
		}
	}
	t.Logf("%d label/query pairs compared with Rust (%d matching), %d mismatches", len(pairs), matched, bad)
}
