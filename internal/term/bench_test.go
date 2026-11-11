package term

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/benchdata"
)

// BenchmarkParsePaste decodes a paste as the terminal reader receives it, in
// 1024-byte reads: as plain keys (no bracketed paste) and as one bracketed
// paste.
func BenchmarkParsePaste(b *testing.B) {
	for _, n := range []int{1 << 10, 64 << 10} {
		text := strings.ReplaceAll(benchdata.Prose(n), "\n", "\r")
		for _, mode := range []string{"keys", "bracketed"} {
			data := []byte(text)
			if mode == "bracketed" {
				data = []byte("\x1b[200~" + text + "\x1b[201~")
			}
			b.Run(fmt.Sprintf("%s/%dKB", mode, n>>10), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for range b.N {
					p := Parser{Raw: true}
					events := 0
					for rest := data; len(rest) > 0; {
						k := min(len(rest), readSize)
						events += len(p.Feed(rest[:k], k == readSize))
						rest = rest[k:]
					}
					if events == 0 {
						b.Fatal("no events")
					}
				}
			})
		}
	}
}
