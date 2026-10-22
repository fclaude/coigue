package walk_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/walk"
)

// This file holds the machinery shared by the differential and golden tests:
// random directory trees with random ignore files, a text form for committing
// them, and the common listing format the Rust reference program prints.

// specEntry is one thing in a generated tree. Kind is 'd' (directory),
// 'f' (file), 'l' (symlink to Target) or 'p' (FIFO); Mode 0 means the default.
type specEntry struct {
	Kind    byte
	Path    string
	Content string
	Target  string
	Mode    os.FileMode
	// Size, when set, extends a file to that many bytes (sparse).
	Size int64
}

type treeSpec []specEntry

// build creates the tree below dir, directories first.
func (s treeSpec) build(t testing.TB, dir string) {
	t.Helper()
	ordered := append(treeSpec(nil), s...)
	sort.SliceStable(ordered, func(i, j int) bool {
		di, dj := ordered[i].Kind == 'd', ordered[j].Kind == 'd'
		if di != dj {
			return di
		}
		return len(ordered[i].Path) < len(ordered[j].Path)
	})
	var restore []string
	for _, e := range ordered {
		path := filepath.Join(dir, e.Path)
		var err error
		if e.Kind != 'd' {
			// Directories the entry implies, for hand-written trees.
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("building %q: %v", e.Path, err)
			}
		}
		switch e.Kind {
		case 'd':
			err = os.MkdirAll(path, 0o755)
		case 'f':
			err = os.WriteFile(path, []byte(e.Content), 0o644)
			if err == nil && e.Size > 0 {
				err = os.Truncate(path, e.Size)
			}
		case 'l':
			err = os.Symlink(e.Target, path)
		case 'p':
			err = mkfifo(path)
		}
		if err != nil {
			t.Fatalf("building %q: %v", e.Path, err)
		}
		if e.Mode != 0 {
			restore = append(restore, path)
		}
	}
	// Unreadable directories last, so that nothing inside them is needed after.
	for _, e := range ordered {
		if e.Mode != 0 {
			if err := os.Chmod(filepath.Join(dir, e.Path), e.Mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, p := range restore {
			_ = os.Chmod(p, 0o755)
		}
	})
}

type generator struct {
	r     *rand.Rand
	names []string
	dirs  []string
	// compat restricts the pattern alphabet to constructs on which the crate
	// and git agree, so the git cross-check can assert equality.
	compat bool
	// text gives regular files searchable content.
	text bool
}

var fileNames = []string{
	"a", "b", "c", "foo", "bar", "x", "y", "a.txt", "b.txt", "c.log", "d.log", "e.rs", "f.rs",
	"keep.log", "main.rs", ".hidden", ".env", "#h", "!bang", "é", "é.txt", "x y", "build", "out",
	"tmp", "t.tmp", "a.b.c", "[a]", "a[b", "x{y,z}", "a,b", "trail.", "a\\b", "ab", "abc", ".a.", "ü.rs",
	"lib.so", "Makefile", "a b.txt", "fooa", "foob", "foo.a", "xa", "€", "日本", "ñ", "😀", "€.txt", "a€b", "ç.log",
}

var dirNames = []string{
	"src", "dir", "sub", "build", "out", ".cache", "node", "a", "b", "foo", "lib", "docs", "x", "é", "tmp",
	"deep", "z", "a b",
}

func (g *generator) pick(list []string) string { return list[g.r.Intn(len(list))] }

// gitAgrees reports whether a pattern line avoids the constructs on which the
// crate deviates from git: braces (alternates), POSIX, negated and unclosed
// classes (classes may match '/' in the crate, an unclosed one is literal),
// runs of three stars, trailing whitespace other than spaces, and a trailing
// escaped slash. The deviations are described in deviations_test.go.
func gitAgrees(line string) bool {
	switch {
	case strings.ContainsAny(line, "{}\t\r") || strings.Contains(line, "\xff"):
		return false
	case strings.Contains(line, "[:"), strings.Contains(line, "***"), strings.Contains(line, "[!"), strings.Contains(line, "[^"):
		return false
	case strings.Contains(line, "[/"):
		return false
	}
	switch trimmed := strings.TrimRight(line, " "); {
	case trimmed == "!", strings.HasSuffix(trimmed, "\\/"), strings.HasSuffix(trimmed, "**//"):
		return false
	}
	for i := 0; i < len(line); i++ {
		if line[i] == '[' && !strings.Contains(line[i+1:], "]") {
			return false
		}
	}
	return true
}

func (g *generator) chance(p float64) bool { return g.r.Float64() < p }

var lineAtoms = []string{
	"*", "**", "?", "[ab]", "[!a]", "[^a]", "[a-c]", "[]a]", "[a-]", "[-a]", "[", "[a", "{a,b}", "{a,}",
	"{,}", "{foo,*.rs}", "{a,b", "}", "a", "b", "c", "foo", ".", "/", "\\*", "\\?", "\\\\", "\\", "é",
	"[é-ñ]", "[à]", "x y", "*.log", "*.txt", "*.rs", "build", "src", "dir", "sub", ".hidden", "#h", "!bang",
	"{a,{b,c}}", "[[]", "[[:alpha:]]", "**/", "/**", "/*", "a**", "**b", "***", "?*", "t?p", "ab*",
}

var wholeLines = []string{
	"", "   ", "# comment", "**", "/", "!", "!!", "\\", "foo\\ ", "\\ ", "*", "!*", "*/", "!*/", "**/",
	"/**", "a/**/b", "**/a/**", "*.*", "\\#foo", "\\!bar", "!/", "!**", "!**/", "*/*", "dir/*", "!dir/keep",
	"/*", "!/*/", "a/", "b/", "*.log", "!keep.log", "#h", ".*", "!.hidden", "[a-z]*", "x\\ y", "x y",
	"x y ", "\t", "a\r", "foo\t",
}

var linePrefixes = []string{"", "", "", "", "/", "!", "!/", "**/", "\\!", "\\#", "#", "src/", "dir/", "sub/", "a/", "/src/", "build/", "**/dir/", "!src/", " "}
var lineSuffixes = []string{"", "", "", "", "/", "/*", "/**", "/**/*", " ", "\\ ", "\t", "/ ", "**"}

// realPath returns the path of an existing entry as an ignore file in dir
// would see it (relative to dir when it is below, else just the name).
func (g *generator) realPath(dir string) string {
	all := append(append([]string(nil), g.names...), g.dirs...)
	p := all[g.r.Intn(len(all))]
	if dir == "" && g.chance(0.6) {
		return p
	}
	if dir != "" && strings.HasPrefix(p, dir+"/") && g.chance(0.8) {
		return p[len(dir)+1:]
	}
	return p[strings.LastIndexByte(p, '/')+1:]
}

var slashSubstitutes = []string{"[!a]", "[^x]", "?", "[/]", "[a-z]", "[^/]", "*", "**", "[!/]", "{/,x}", "/", "/**/"}
var runeClasses = []string{"[é]", "[à-ü]", "[^é]", "[€]", "[€-₿]", "[^€]", "[日]", "[😀]", "[ñ-ü]", "[!ç]", "?", "??", "???", "*", "[ç]"}

// mutatedPath takes a real path and perturbs it so that it still often
// matches: wildcards in place of characters, classes in place of slashes,
// alternates around a name, and so on.
func (g *generator) mutatedPath(dir string) string {
	p := g.realPath(dir)
	runes := []rune(p)
	switch g.r.Intn(8) {
	case 0: // a slash becomes a wildcard or class
		for i := range runes {
			if runes[i] == '/' && g.chance(0.7) {
				return string(runes[:i]) + g.pick(slashSubstitutes) + string(runes[i+1:])
			}
		}
		return p + g.pick(slashSubstitutes)
	case 1: // one character becomes ? or a class
		i := g.r.Intn(len(runes))
		return string(runes[:i]) + g.pick(runeClasses) + string(runes[i+1:])
	case 2: // alternates with and without empty branches
		forms := []string{"%s{a,}", "{,a}%s", "%s{,.txt}", "{%s,}", "{%s,x}", "{x,%s}", "%s{,}", "{%s}", "%s{a,b}", "{%s,%s}", "{a,%s,b}"}
		f := g.pick(forms)
		return strings.ReplaceAll(f, "%s", p)
	case 3: // segment-wise wildcards
		parts := strings.Split(p, "/")
		i := g.r.Intn(len(parts))
		parts[i] = g.pick([]string{"*", "**", "?*", "*?", "[!x]*", "*" + parts[i], parts[i] + "*", "**" + parts[i], parts[i] + "**"})
		return strings.Join(parts, "/")
	case 4: // doublestar forms
		return g.pick([]string{"**/", "/**/", "a/**/", "**/a/**/", "/"}) + p
	case 5:
		return p + g.pick([]string{"/**", "/**/", "/*", "/**/*", "/", "**", "/*/"})
	case 6: // unicode classes against whole names
		return g.pick(runeClasses) + g.pick(runeClasses) + string(runes[min(1, len(runes)-1):])
	default:
		return p
	}
}

func (g *generator) line(dir string) string {
	for {
		if l := g.rawLine(dir); !g.compat || gitAgrees(l) {
			return l
		}
	}
}

func (g *generator) rawLine(dir string) string {
	switch p := g.r.Float64(); {
	case p < 0.2:
		return g.pick(linePrefixes) + g.pick(append(append([]string(nil), fileNames...), dirNames...)) + g.pick(lineSuffixes)
	case p < 0.45:
		var b strings.Builder
		b.WriteString(g.pick(linePrefixes))
		for i := 1 + g.r.Intn(4); i > 0; i-- {
			b.WriteString(g.pick(lineAtoms))
		}
		b.WriteString(g.pick(lineSuffixes))
		return b.String()
	case p < 0.55:
		return g.pick(wholeLines)
	case p < 0.8:
		if len(g.names) == 0 {
			return g.pick(wholeLines)
		}
		return g.pick(linePrefixes) + g.mutatedPath(dir) + g.pick(lineSuffixes)
	default:
		if len(g.names) == 0 {
			return g.pick(wholeLines)
		}
		return g.pick(linePrefixes) + g.realPath(dir) + g.pick(lineSuffixes)
	}
}

var textLines = []string{
	"needle", "a needle in a haystack", "foo", "x := foo(a, b)", "é needle", "\tindented needle", "  padded needle  ",
	"nbsp\u00a0needle", "emoji 😀 needle", "NEEDLE", "", "fn main() {}", "日本 needle 日本", "needleneedle", "a",
	"\u2003em space needle\u2003", "tab\tneedle\t", "carriage\r", "ß needle İ",
}

// fileText is random source text, now and then with a NUL or invalid UTF-8
// (which the search skips) and very long lines (which it truncates).
func (g *generator) fileText() string {
	if g.chance(0.15) {
		return ""
	}
	var b strings.Builder
	eol := "\n"
	if g.chance(0.2) {
		eol = "\r\n"
	}
	for i := g.r.Intn(7); i > 0; i-- {
		line := g.pick(textLines)
		if g.chance(0.05) {
			line = strings.Repeat("é ", 100) + "needle " + strings.Repeat("x", 200)
		}
		b.WriteString(line + eol)
	}
	switch {
	case g.chance(0.03):
		b.WriteString("bin\x00needle\n")
	case g.chance(0.03):
		b.WriteString("bad\xff needle\n")
	case g.chance(0.5):
		b.WriteString("last needle")
	}
	return b.String()
}

func (g *generator) ignoreFile(dir string) string {
	var b strings.Builder
	eol := "\n"
	if g.chance(0.1) {
		eol = "\r\n"
	}
	if g.chance(0.06) {
		b.WriteString("\xef\xbb\xbf")
		if !g.compat && g.chance(0.3) {
			b.WriteString("\xef\xbb\xbf")
		}
		b.WriteString(g.line(dir) + eol)
	}
	n := g.r.Intn(9)
	for i := 0; i < n; i++ {
		b.WriteString(g.line(dir))
		if i < n-1 || g.chance(0.85) {
			b.WriteString(eol)
		}
	}
	if !g.compat && g.chance(0.03) {
		b.WriteString("\xff\xfe-bad\n" + g.line(dir) + "\n")
	}
	return b.String()
}

// tree makes a random tree whose top directory is "proj" (so that ignore files
// of the directory above it, "", can matter too).
func genTree(r *rand.Rand) treeSpec { return genTreeFor(r, false) }

func genTreeFor(r *rand.Rand, compat bool) treeSpec { return genTreeWith(r, compat, false) }

// genTreeText is a tree whose files hold text for the project search.
func genTreeText(r *rand.Rand) treeSpec { return genTreeWith(r, false, true) }

func genTreeWith(r *rand.Rand, compat, text bool) treeSpec {
	g := &generator{r: r, compat: compat, text: text}
	var spec treeSpec
	used := map[string]bool{}
	nonDir := map[string]bool{}
	implied := map[string]bool{}
	add := func(e specEntry) bool {
		if used[e.Path] {
			return false
		}
		if e.Kind != 'd' && implied[e.Path] {
			return false
		}
		for i := 0; i < len(e.Path); i++ {
			if e.Path[i] == '/' && nonDir[e.Path[:i]] {
				return false
			}
		}
		for i := 0; i < len(e.Path); i++ {
			if e.Path[i] == '/' {
				implied[e.Path[:i]] = true
			}
		}
		used[e.Path] = true
		if e.Kind != 'd' {
			nonDir[e.Path] = true
		}
		spec = append(spec, e)
		if e.Kind == 'd' {
			g.dirs = append(g.dirs, e.Path)
		} else {
			g.names = append(g.names, e.Path)
		}
		return true
	}
	add(specEntry{Kind: 'd', Path: "proj"})
	var grow func(dir string, depth int)
	grow = func(dir string, depth int) {
		n := g.r.Intn(6)
		if depth == 0 {
			n = 2 + g.r.Intn(5)
		}
		for i := 0; i < n; i++ {
			if depth < 4 && g.chance(0.4-0.07*float64(depth)) {
				name := g.pick(dirNames)
				if add(specEntry{Kind: 'd', Path: dir + "/" + name}) {
					grow(dir+"/"+name, depth+1)
				}
			} else {
				entry := specEntry{Kind: 'f', Path: dir + "/" + g.pick(fileNames)}
				if g.text {
					entry.Content = g.fileText()
					if g.chance(0.01) {
						entry.Size = 2*1024*1024 + int64(g.r.Intn(3))
					}
				}
				add(entry)
			}
		}
	}
	grow("proj", 0)
	// Ignore files, assigned after the names exist so that lines can hit them.
	dirs := append([]string{""}, g.dirs...)
	for _, d := range dirs {
		if g.chance(0.4) {
			path := ".gitignore"
			if d != "" {
				path = d + "/.gitignore"
			}
			switch p := g.r.Float64(); {
			case p < 0.02:
				add(specEntry{Kind: 'd', Path: path})
			case p < 0.05 && len(g.names) > 0 && !g.compat:
				add(specEntry{Kind: 'l', Path: path, Target: "../" + g.names[g.r.Intn(len(g.names))]})
			default:
				add(specEntry{Kind: 'f', Path: path, Content: g.ignoreFile(d)})
			}
		}
	}
	// Repository metadata.
	if g.chance(0.4) {
		add(specEntry{Kind: 'd', Path: "proj/.git/info"})
		add(specEntry{Kind: 'f', Path: "proj/.git/HEAD", Content: "ref: refs/heads/main\n"})
		if g.chance(0.7) {
			add(specEntry{Kind: 'f', Path: "proj/.git/info/exclude", Content: g.ignoreFile("proj")})
		}
	}
	for _, d := range g.dirs {
		if g.chance(0.12) {
			switch g.r.Intn(3) {
			case 0:
				add(specEntry{Kind: 'f', Path: d + "/.git", Content: "gitdir: ../.git/worktrees/x\n"})
			default:
				add(specEntry{Kind: 'd', Path: d + "/.git/info"})
				if g.chance(0.8) {
					add(specEntry{Kind: 'f', Path: d + "/.git/info/exclude", Content: g.ignoreFile(d)})
				}
				add(specEntry{Kind: 'f', Path: d + "/.git/config", Content: "[core]\n"})
			}
		}
		if g.chance(0.1) {
			if g.chance(0.8) {
				add(specEntry{Kind: 'd', Path: d + "/.coigue-trash"})
				add(specEntry{Kind: 'f', Path: d + "/.coigue-trash/item", Content: "x"})
			} else {
				add(specEntry{Kind: 'f', Path: d + "/.coigue-trash", Content: "x"})
			}
		}
	}
	// Links, special files, unreadable directories.
	if g.chance(0.4) {
		for i := 1 + g.r.Intn(3); i > 0; i-- {
			dir := g.dirs[g.r.Intn(len(g.dirs))]
			name := dir + "/" + g.pick([]string{"link", "loop", "ln", "dangling", "up", "out", "l"})
			var target string
			switch g.r.Intn(5) {
			case 0:
				target = ".." // loop to the parent
			case 1:
				target = "nowhere"
			case 2:
				target = "/etc/hostname"
			case 3:
				if len(g.names) > 0 {
					target = filepath.Base(g.names[g.r.Intn(len(g.names))])
				} else {
					target = "x"
				}
			default:
				target = filepath.Base(g.dirs[g.r.Intn(len(g.dirs))])
			}
			add(specEntry{Kind: 'l', Path: name, Target: target})
		}
	}
	if g.chance(0.05) {
		add(specEntry{Kind: 'p', Path: g.dirs[g.r.Intn(len(g.dirs))] + "/fifo"})
	}
	if g.chance(0.05) && len(g.dirs) > 1 {
		d := g.dirs[1+g.r.Intn(len(g.dirs)-1)]
		for i := range spec {
			if spec[i].Kind == 'd' && spec[i].Path == d {
				spec[i].Mode = 0o000
			}
		}
	}
	return spec
}

// ---- listing format shared with the Rust reference program ---------------

func escapeBytes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x21 && c < 0x7f && c != '\\' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\x%02x", c)
		}
	}
	return b.String()
}

type walkMode string

const (
	modePicker         walkMode = "picker"
	modeSearch         walkMode = "search"
	modeExplorerHidden walkMode = "explorer-hidden"
	modeExplorerAll    walkMode = "explorer-all"
)

func (m walkMode) options() walk.Options {
	filter := func(e *walk.Entry) bool { return e.Name != ".git" && e.Name != ".coigue-trash" }
	switch m {
	case modeExplorerHidden:
		return walk.Options{Hidden: true, GitIgnore: true, GitExclude: true, Parents: true, MaxDepth: 1, Filter: filter}
	case modeExplorerAll:
		return walk.Options{MaxDepth: 1, Filter: filter}
	default:
		return walk.Options{GitIgnore: true, GitExclude: true, Parents: true, Filter: filter}
	}
}

// listing walks root and prints the lines the Rust program prints: one
// `E kind depth err relpath` per entry, then `X n` with the number of
// errors. The lines are in walk order.
func listing(mode walkMode, root string) []string {
	var lines []string
	errors := 0
	w := walk.New(root, mode.options())
	for {
		item, ok := w.Next(context.Background())
		if !ok {
			break
		}
		if item.Err != nil {
			errors++
			continue
		}
		e := item.Entry
		rel := e.Path
		if r, ok := fsx.StripPrefix(e.Path, root); ok {
			rel = r
		}
		kind := map[walk.Kind]byte{walk.KindFile: 'f', walk.KindDir: 'd', walk.KindSymlink: 'l', walk.KindOther: 'o'}[e.Kind]
		flag := byte('-')
		if e.Err != nil {
			flag = 'e'
		}
		lines = append(lines, fmt.Sprintf("E %c %d %c %s", kind, e.Depth, flag, escapeBytes(rel)))
	}
	return append(lines, fmt.Sprintf("X %d", errors))
}

// sortedListing is listing without the order (the walk order is unspecified
// except for the search mode).
func sortedListing(mode walkMode, root string) []string {
	lines := listing(mode, root)
	if mode == modeSearch {
		return lines
	}
	sort.Strings(lines)
	return lines
}
