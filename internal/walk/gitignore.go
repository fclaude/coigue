package walk

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/ucd"
)

// match is the verdict of an ignore file for a path.
type match uint8

const (
	matchNone match = iota
	matchIgnore
	matchWhitelist
)

// or keeps the first verdict that is not "no match".
func (m match) or(other match) match {
	if m != matchNone {
		return m
	}
	return other
}

type glob struct {
	whitelist bool
	onlyDir   bool
	matcher   *matcher
}

// gitignore is the matcher for the globs of one ignore file, as the `ignore`
// crate's Gitignore: paths are made relative to root before matching.
type gitignore struct {
	root  string
	globs []glob
}

// gitignoreBuilder mirrors GitignoreBuilder (case-sensitive, unclosed classes
// allowed).
type gitignoreBuilder struct {
	root  string
	globs []glob
	// globErrors counts lines that failed to compile. I/O errors never count:
	// the crate drops them (`maybe_push_ignore_io`) unless a glob error
	// accompanies them, and then the glob error alone already makes the error
	// visible.
	globErrors int
}

func newGitignoreBuilder(root string) *gitignoreBuilder {
	return &gitignoreBuilder{root: strings.TrimPrefix(root, "./")}
}

func (b *gitignoreBuilder) build() *gitignore {
	return &gitignore{root: b.root, globs: b.globs}
}

// addFile reads an ignore file like GitignoreBuilder::add. A missing or
// unreadable file adds nothing. Reading stops at the first line that is not
// valid UTF-8 (Rust's BufRead::lines fails there), keeping earlier lines.
func (b *gitignoreBuilder) addFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for i := 0; len(data) > 0; i++ {
		var line []byte
		if nl := indexByte(data, '\n'); nl >= 0 {
			line, data = data[:nl], data[nl+1:]
			line = trimSuffixByte(line, '\r')
		} else {
			line, data = data, nil
		}
		if !utf8.Valid(line) {
			return
		}
		text := string(line)
		if i == 0 {
			// Match git: a leading byte order mark is not part of the first
			// pattern (trim_start_matches removes repeated marks).
			for strings.HasPrefix(text, byteOrderMark) {
				text = text[len(byteOrderMark):]
			}
		}
		if err := b.addLine(text); err != nil {
			b.globErrors++
		}
	}
}

const byteOrderMark = "\xef\xbb\xbf"

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func trimSuffixByte(b []byte, c byte) []byte {
	if n := len(b); n > 0 && b[n-1] == c {
		return b[:n-1]
	}
	return b
}

// addLine is GitignoreBuilder::add_line.
func (b *gitignoreBuilder) addLine(line string) error {
	if strings.HasPrefix(line, "#") {
		return nil
	}
	if !strings.HasSuffix(line, "\\ ") {
		line = trimEndSpace(line)
	}
	if line == "" {
		return nil
	}
	g := glob{}
	isAbsolute := false
	if strings.HasPrefix(line, "\\!") || strings.HasPrefix(line, "\\#") {
		line = line[1:]
		isAbsolute = strings.HasPrefix(line, "/")
	} else {
		if strings.HasPrefix(line, "!") {
			g.whitelist = true
			line = line[1:]
		}
		if strings.HasPrefix(line, "/") {
			// A leading slash anchors the glob to the ignore file's
			// directory, which the glob dialect achieves by never letting a
			// wildcard match '/'.
			line = line[1:]
			isAbsolute = true
		}
	}
	// A trailing slash makes the glob directory-only but is not part of it.
	if strings.HasSuffix(line, "/") {
		g.onlyDir = true
		line = line[:len(line)-1]
		// An escaped slash loses its escape.
		line = strings.TrimSuffix(line, "\\")
	}
	actual := line
	// A glob without a slash may match at any depth, unless it already says so.
	if !isAbsolute && !strings.Contains(line, "/") {
		if !(strings.HasPrefix(actual, "**/") || actual == "**") {
			actual = "**/" + actual
		}
	}
	// `dir/**` matches what is inside dir, not dir itself.
	if strings.HasSuffix(actual, "/**") {
		actual += "/*"
	}
	tokens, err := parseGlob(actual)
	if err != nil {
		return err
	}
	g.matcher = compileGlob(tokens)
	b.globs = append(b.globs, g)
	return nil
}

// trimEndSpace is str::trim_end: Unicode White_Space.
func trimEndSpace(s string) string {
	end := len(s)
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !ucd.IsWhitespace(r) {
			break
		}
		end -= size
	}
	return s[:end]
}

// matched is Gitignore::matched: the last glob that matches decides; a
// directory-only glob needs a directory.
func (g *gitignore) matched(path string, isDir bool) match {
	if g == nil || len(g.globs) == 0 {
		return matchNone
	}
	path = g.strip(path)
	for i := len(g.globs) - 1; i >= 0; i-- {
		gl := &g.globs[i]
		if (!gl.onlyDir || isDir) && gl.matcher.match(path) {
			if gl.whitelist {
				return matchWhitelist
			}
			return matchIgnore
		}
	}
	return matchNone
}

// strip makes path relative to the directory of the ignore file. Prefixes are
// compared as bytes, not components, like the crate does.
func (g *gitignore) strip(path string) string {
	path = strings.TrimPrefix(path, "./")
	// A bare file name has nothing to strip; a root of "." must not eat the
	// leading dot of a hidden name.
	if g.root != "." && !isFileName(path) {
		if rest, ok := strings.CutPrefix(path, g.root); ok {
			path = strings.TrimPrefix(rest, "/")
		}
	}
	return path
}

func isFileName(path string) bool { return !strings.Contains(path, "/") }
