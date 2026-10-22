package walk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cases below are the unit tests of the Rust `ignore` crate's gitignore
// module (ignore-0.4.33/src/gitignore.rs), run against the Go matcher.

func giFromString(root, text string) *gitignore {
	b := newGitignoreBuilder(root)
	for _, line := range strings.Split(text, "\n") {
		_ = b.addLine(strings.TrimSuffix(line, "\r"))
	}
	return b.build()
}

const crateRoot = "/home/foobar/rust/rg"

type giCase struct {
	name, root, gitignore, path string
	isDir                       bool
}

func TestCrateGitignoreIgnored(t *testing.T) {
	cases := []giCase{
		{"ig1", crateRoot, "months", "months", false},
		{"ig2", crateRoot, "*.lock", "Cargo.lock", false},
		{"ig3", crateRoot, "*.rs", "src/main.rs", false},
		{"ig4", crateRoot, "src/*.rs", "src/main.rs", false},
		{"ig5", crateRoot, "/*.c", "cat-file.c", false},
		{"ig6", crateRoot, "/src/*.rs", "src/main.rs", false},
		{"ig7", crateRoot, "!src/main.rs\n*.rs", "src/main.rs", false},
		{"ig8", crateRoot, "foo/", "foo", true},
		{"ig9", crateRoot, "**/foo", "foo", false},
		{"ig10", crateRoot, "**/foo", "src/foo", false},
		{"ig11", crateRoot, "**/foo/**", "src/foo/bar", false},
		{"ig12", crateRoot, "**/foo/**", "wat/src/foo/bar/baz", false},
		{"ig13", crateRoot, "**/foo/bar", "foo/bar", false},
		{"ig14", crateRoot, "**/foo/bar", "src/foo/bar", false},
		{"ig15", crateRoot, "abc/**", "abc/x", false},
		{"ig16", crateRoot, "abc/**", "abc/x/y", false},
		{"ig17", crateRoot, "abc/**", "abc/x/y/z", false},
		{"ig18", crateRoot, "a/**/b", "a/b", false},
		{"ig19", crateRoot, "a/**/b", "a/x/b", false},
		{"ig20", crateRoot, "a/**/b", "a/x/y/b", false},
		{"ig21", crateRoot, `\!xy`, "!xy", false},
		{"ig22", crateRoot, `\#foo`, "#foo", false},
		{"ig23", crateRoot, "foo", "./foo", false},
		{"ig24", crateRoot, "target", "grep/target", false},
		{"ig25", crateRoot, "Cargo.lock", "./tabwriter-bin/Cargo.lock", false},
		{"ig26", crateRoot, "/foo/bar/baz", "./foo/bar/baz", false},
		{"ig27", crateRoot, "foo/", "xyz/foo", true},
		{"ig28", "./src", "/llvm/", "./src/llvm", true},
		{"ig29", crateRoot, "node_modules/ ", "node_modules", true},
		{"ig30", crateRoot, "**/", "foo/bar", true},
		{"ig31", crateRoot, "path1/*", "path1/foo", false},
		{"ig32", crateRoot, ".a/b", ".a/b", false},
		{"ig33", "./", ".a/b", ".a/b", false},
		{"ig34", ".", ".a/b", ".a/b", false},
		{"ig35", "./.", ".a/b", ".a/b", false},
		{"ig36", "././", ".a/b", ".a/b", false},
		{"ig37", "././.", ".a/b", ".a/b", false},
		{"ig38", crateRoot, "\\[", "[", false},
		{"ig39", crateRoot, "\\?", "?", false},
		{"ig40", crateRoot, "\\*", "*", false},
		{"ig41", crateRoot, "\\a", "a", false},
		{"ig42", crateRoot, "s*.rs", "sfoo.rs", false},
		{"ig43", crateRoot, "**", "foo.rs", false},
		{"ig44", crateRoot, "**/**/*", "a/foo.rs", false},
		{"cs1", crateRoot, "*.html", "foo.html", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := giFromString(c.root, c.gitignore).matched(c.path, c.isDir); got != matchIgnore {
				t.Errorf("%q in root %q: %q matched as %d, want ignore", c.gitignore, c.root, c.path, got)
			}
		})
	}
}

func TestCrateGitignoreNotIgnored(t *testing.T) {
	cases := []giCase{
		{"ignot1", crateRoot, "amonths", "months", false},
		{"ignot2", crateRoot, "monthsa", "months", false},
		{"ignot3", crateRoot, "/src/*.rs", "src/grep/src/main.rs", false},
		{"ignot4", crateRoot, "/*.c", "mozilla-sha1/sha1.c", false},
		{"ignot5", crateRoot, "/src/*.rs", "src/grep/src/main.rs", false},
		{"ignot6", crateRoot, "*.rs\n!src/main.rs", "src/main.rs", false},
		{"ignot7", crateRoot, "foo/", "foo", false},
		{"ignot8", crateRoot, "**/foo/**", "wat/src/afoo/bar/baz", false},
		{"ignot9", crateRoot, "**/foo/**", "wat/src/fooa/bar/baz", false},
		{"ignot10", crateRoot, "**/foo/bar", "foo/src/bar", false},
		{"ignot11", crateRoot, "#foo", "#foo", false},
		{"ignot12", crateRoot, "\n\n\n", "foo", false},
		{"ignot13", crateRoot, "foo/**", "foo", true},
		{"ignot14", "./third_party/protobuf", "m4/ltoptions.m4", "./third_party/protobuf/csharp/src/packages/repositories.config", false},
		{"ignot15", crateRoot, "!/bar", "foo/bar", false},
		{"ignot16", crateRoot, "*\n!**/", "foo", true},
		{"ignot17", crateRoot, "src/*.rs", "src/grep/src/main.rs", false},
		{"ignot18", crateRoot, "path1/*", "path2/path1/foo", false},
		{"ignot19", crateRoot, "s*.rs", "src/foo.rs", false},
		{"cs2", crateRoot, "*.html", "foo.HTML", false},
		{"cs3", crateRoot, "*.html", "foo.htm", false},
		{"cs4", crateRoot, "*.html", "foo.HTM", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := giFromString(c.root, c.gitignore).matched(c.path, c.isDir); got == matchIgnore {
				t.Errorf("%q in root %q: %q was ignored", c.gitignore, c.root, c.path)
			}
		})
	}
}

// Regression 106 of the crate: a line of one space must not break the builder.
func TestCrateRegression106(t *testing.T) {
	giFromString("/", " ")
}

func TestLineParsing(t *testing.T) {
	cases := []struct {
		line string
		want match
		path string
		dir  bool
	}{
		{"foo", matchIgnore, "a/b/foo", false},
		{"!foo", matchWhitelist, "foo", false},
		{"foo  ", matchIgnore, "foo", false},
		{"foo\\ ", matchIgnore, "foo ", false},
		{"foo\\ ", matchNone, "foo", false},
		{"#foo", matchNone, "#foo", false},
		{"\\#foo", matchIgnore, "#foo", false},
		{"\\!foo", matchIgnore, "!foo", false},
		{"foo/", matchNone, "foo", false},
		{"foo/", matchIgnore, "foo", true},
		{"foo\\/", matchIgnore, "foo", true},
		{"/foo", matchIgnore, "foo", false},
		{"/foo", matchNone, "a/foo", false},
		{"a/b", matchIgnore, "a/b", false},
		{"a/b", matchNone, "x/a/b", false},
		{"a*b", matchNone, "a/b", false},
		{"a**b", matchIgnore, "axb", false},
		{"a**b", matchNone, "a/b", false},
		{"*", matchIgnore, "x", false},
		{"!", matchWhitelist, "anything", false},
	}
	for _, c := range cases {
		b := newGitignoreBuilder("/r")
		if err := b.addLine(c.line); err != nil {
			t.Errorf("%q: %v", c.line, err)
		}
		gi := b.build()
		if got := gi.matched(c.path, c.dir); got != c.want {
			t.Errorf("%q on %q (dir=%v): got %d, want %d", c.line, c.path, c.dir, got, c.want)
		}
	}
}

// Two trailing spaces after an escape trim down to the escape, which then
// dangles: a glob error that adds nothing.
func TestDanglingEscapeAfterTrimmingIsAnError(t *testing.T) {
	b := newGitignoreBuilder("/r")
	if err := b.addLine("foo\\  "); err == nil {
		t.Error("expected a glob error")
	}
	if len(b.build().globs) != 0 {
		t.Error("an invalid line must add nothing")
	}
}

func TestIgnoreFileReading(t *testing.T) {
	dir := t.TempDir()
	read := func(content string) (*gitignore, int) {
		path := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		b := newGitignoreBuilder("/r")
		b.addFile(path)
		return b.build(), b.globErrors
	}
	// A byte order mark (even repeated) is not part of the first pattern.
	gi, _ := read("\xef\xbb\xbf\xef\xbb\xbfa\nb\n")
	if gi.matched("a", false) != matchIgnore || gi.matched("b", false) != matchIgnore {
		t.Error("the byte order mark leaked into the pattern")
	}
	// A mark that is not at the start of the file is an ordinary character.
	gi, _ = read("b\n\xef\xbb\xbfa\n")
	if gi.matched("a", false) != matchNone {
		t.Error("only the first line loses its byte order mark")
	}
	// CRLF is a line ending; a lone CR is trailing white space.
	gi, _ = read("a\r\nb\rc\r")
	if gi.matched("a", false) != matchIgnore || gi.matched("b\rc", false) != matchIgnore {
		t.Error("carriage returns")
	}
	// Reading stops at the first line that is not UTF-8.
	gi, _ = read("a\n\xff\nb\n")
	if gi.matched("a", false) != matchIgnore || gi.matched("b", false) != matchNone {
		t.Error("an invalid line ends the file")
	}
	// Bad globs are counted and skipped, the rest of the file applies.
	gi, errs := read("[z-a]\nb\n{\n")
	if errs != 2 || gi.matched("b", false) != matchIgnore {
		t.Errorf("%d errors, b matched %d", errs, gi.matched("b", false))
	}
	// A missing file adds nothing and is no error.
	b := newGitignoreBuilder("/r")
	b.addFile(filepath.Join(dir, "missing"))
	if len(b.build().globs) != 0 || b.globErrors != 0 {
		t.Error("a missing file")
	}
}

func TestGlobDialectQuirks(t *testing.T) {
	match := func(glob, path string) bool {
		t.Helper()
		tokens, err := parseGlob(glob)
		if err != nil {
			t.Fatalf("%q: %v", glob, err)
		}
		return compileGlob(tokens).match(path)
	}
	// ? and classes work on bytes: one character outside ASCII needs ??.
	if match("a?b", "aéb") || !match("a??b", "aéb") {
		t.Error("? must consume one byte")
	}
	if match("[é]", "é") || !match("[é]*", "é") {
		t.Error("a class with a non-ASCII member is a set of single bytes")
	}
	// Negated classes may match '/', alternates are supported, and empty
	// branches are dropped.
	if !match("a[!x]b", "a/b") {
		t.Error("[!x] should match /")
	}
	if !match("{a,b}", "b") || match("{a,b}", "{a,b}") {
		t.Error("alternates")
	}
	if match("x{,.txt}", "x") || !match("x{,.txt}", "x.txt") {
		t.Error("empty alternate branches are dropped")
	}
	if !match("{a,{b,c}}", "c") {
		t.Error("nested alternates")
	}
	if !match("[", "[") || !match("a[b", "a[b") || !match("[[]", "[") {
		t.Error("unclosed class is literal")
	}
	if !match("[]a]", "]") || !match("[a-]", "-") || !match("[-a]", "-") {
		t.Error("class edge members")
	}
	for _, bad := range []string{"{a", "a}", "\\", "[z-a]", "{a,b"} {
		if _, err := parseGlob(bad); err == nil {
			t.Errorf("%q should not compile", bad)
		}
	}
}

func TestClassSet(t *testing.T) {
	set := classSet(token{kind: tokClass, ranges: [][2]rune{{'€', '₿'}}})
	// € is e2 82 ac and ₿ is e2 82 bf: bytes e2 and 82 individually, then the
	// range ac-e2, then the rest of the upper end.
	for b := 0; b < 256; b++ {
		want := b == 0xe2 || b == 0x82 || (b >= 0xac && b <= 0xe2) || b == 0xbf
		if set[b] != want {
			t.Errorf("byte %#x: got %v, want %v", b, set[b], want)
		}
	}
}
