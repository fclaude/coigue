package buffer

import (
	"unicode/utf8"
)

// Two things about the text are known without looking at all of it:
//
//   - its size in bytes, as Text would encode it (Len, kept by the rope), which
//     makes "is this still the file on disk?" a comparison of two numbers in
//     almost every case;
//   - for each recent revision, the first line that the change to it could
//     have altered (FirstChangedRow), so a highlighter can keep everything
//     above that line.

// damageLog is how many recent revisions FirstChangedRow can answer for. A
// reader further behind than this starts again from the top.
const damageLog = 256

type damage struct {
	revision uint64
	row      int
}

// Len is the size in bytes of Text.
func (b *Buffer) Len() int { return b.text.Bytes() }

// noteDamage records that the change making the current revision may have
// altered row and the rows below it; the rows above are as they were.
func (b *Buffer) noteDamage(row int) {
	b.damage[b.revision%damageLog] = damage{revision: b.revision, row: max(row, 0)}
}

// FirstChangedRow is the first row that may differ from the text at revision
// since; every row above it is unchanged and in the same place. ok is false
// when since is too far back to know (or is not a revision of this buffer),
// and the caller must assume everything changed. With nothing changed since,
// it is LineCount().
func (b *Buffer) FirstChangedRow(since uint64) (row int, ok bool) {
	if since > b.revision || b.revision-since > damageLog {
		return 0, false
	}
	row = b.text.Len()
	for rev := since + 1; rev <= b.revision; rev++ {
		d := b.damage[rev%damageLog]
		if d.revision != rev {
			return 0, false
		}
		row = min(row, d.row)
	}
	return row, true
}

// EqualBytes reports whether Text() would equal text, without building it.
func (b *Buffer) EqualBytes(text []byte) bool {
	if b.Len() != len(text) {
		return false
	}
	at := 0
	var enc [utf8.UTFMax]byte
	equal := true
	b.text.Each(0, func(i int, line []rune) bool {
		if i > 0 {
			if text[at] != '\n' {
				equal = false
				return false
			}
			at++
		}
		for _, r := range line {
			if uint32(r) < utf8.RuneSelf {
				if text[at] != byte(r) {
					equal = false
					return false
				}
				at++
				continue
			}
			n := utf8.EncodeRune(enc[:], r)
			for k := range n {
				if text[at+k] != enc[k] {
					equal = false
					return false
				}
			}
			at += n
		}
		return true
	})
	return equal && at == len(text)
}
