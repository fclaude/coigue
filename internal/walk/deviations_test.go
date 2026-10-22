package walk_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The `ignore` crate is the reference for the editor's behaviour, so where it
// differs from git the Go port keeps the crate's behaviour. Each case is a
// small tree below "proj", the entries the crate lists (which the port must
// list, whatever git says) and the entries git lists (checked against
// `git check-ignore --no-index` when git is installed, so the table cannot
// drift from reality). Entries are those below proj without the ignore files.
//
// The random comparison with git (TestAgainstGitCheckIgnore) avoids exactly
// these constructs; the two tests together say what "the same as git" covers.

type deviation struct {
	name  string
	tree  treeSpec
	crate []string
	// git is the listing git gives; nil means git cannot be asked (a nested
	// repository is not covered by the outer one).
	git []string
	// root is the directory below proj to walk from, "" for proj itself.
	root string
	// noGit skips the git side.
	noGit bool
}

func entries(paths ...string) treeSpec {
	var s treeSpec
	for _, p := range paths {
		kind := byte('f')
		if strings.HasSuffix(p, "/") {
			kind, p = 'd', strings.TrimSuffix(p, "/")
		}
		s = append(s, specEntry{Kind: kind, Path: "proj/" + p})
	}
	return s
}

func withIgnore(tree treeSpec, content string) treeSpec {
	return append(tree, specEntry{Kind: 'f', Path: "proj/.gitignore", Content: content})
}

func deviations() []deviation {
	with := func(t treeSpec) treeSpec { return append(treeSpec{{Kind: 'd', Path: "proj"}}, t...) }
	return []deviation{
		{
			// {a,b} is an alternation in the glob dialect of the crate and a
			// literal in git.
			name:  "braces are alternates",
			tree:  with(withIgnore(entries("a.txt", "b.txt", "c.txt", "{a,b}.txt"), "{a,b}.txt\n")),
			crate: []string{"c.txt", "{a,b}.txt"},
			git:   []string{"a.txt", "b.txt", "c.txt"},
		},
		{
			// Alternates drop their empty branch: x{,.txt} is only x.txt.
			name:  "empty alternate branches are dropped",
			tree:  with(withIgnore(entries("x", "x.txt"), "x{,.txt}\n")),
			crate: []string{"x"},
			git:   []string{"x", "x.txt"},
		},
		{
			// A character class may match '/', which git never lets it do.
			name:  "classes match a slash",
			tree:  with(withIgnore(entries("dir/file", "other"), "dir[!x]file\n")),
			crate: []string{"dir", "other"},
			git:   []string{"dir", "dir/file", "other"},
		},
		{
			name:  "POSIX classes are not supported",
			tree:  with(withIgnore(entries("1.txt", "a.txt"), "[[:digit:]].txt\n")),
			crate: []string{"1.txt", "a.txt"},
			git:   []string{"a.txt"},
		},
		{
			// An unclosed '[' is a literal in the crate and invalidates the
			// pattern in git.
			name:  "unclosed class is literal",
			tree:  with(withIgnore(entries("a[b", "ab"), "a[b\n")),
			crate: []string{"ab"},
			git:   []string{"a[b", "ab"},
		},
		{
			// Bytes: a non-ASCII member is a set of single bytes, so [é] never
			// matches the two-byte é as a whole...
			name:  "non-ASCII class members are bytes",
			tree:  with(withIgnore(entries("é", "e"), "[é]\n")),
			crate: []string{"e", "é"},
			git:   []string{"e", "é"},
		},
		{
			// ...but [é]* matches it by its first byte, in both.
			name:  "non-ASCII class members still match a prefix",
			tree:  with(withIgnore(entries("é", "e"), "[é]*\n")),
			crate: []string{"e"},
			git:   []string{"e"},
		},
		{
			// More than two stars: the crate sees three single stars, git
			// sees a double star.
			name:  "three stars",
			tree:  with(withIgnore(entries("b", "x/b", "x/y/b"), "***/b\n")),
			crate: []string{"b", "x", "x/y", "x/y/b"},
			git:   []string{"x", "x/y"},
		},
		{
			// The crate trims every Unicode white space at the end of a line;
			// git trims spaces only.
			name:  "trailing tab",
			tree:  with(withIgnore(entries("a", "b"), "a\t\n")),
			crate: []string{"b"},
			git:   []string{"a", "b"},
		},
		{
			// The crate strips every leading byte order mark, git one.
			name:  "repeated byte order mark",
			tree:  with(withIgnore(entries("x", "y"), "\xef\xbb\xbf\xef\xbb\xbfx\n")),
			crate: []string{"y"},
			git:   []string{"x", "y"},
		},
		{
			// The crate stops reading at a line that is not UTF-8.
			name:  "invalid UTF-8 ends the file",
			tree:  with(withIgnore(entries("a", "b", "c"), "a\n\xff\nb\n")),
			crate: []string{"b", "c"},
			git:   []string{"c"},
		},
		{
			// A lone "!" whitelists everything in the crate and nothing in git.
			name:  "lone negation",
			tree:  with(withIgnore(entries("x.log", "y.txt"), "*.log\n!\n")),
			crate: []string{"x.log", "y.txt"},
			git:   []string{"y.txt"},
		},
		{
			// "\/" is a directory-only pattern for the empty name, which
			// matches every directory.
			name:  "escaped slash pattern",
			tree:  with(withIgnore(entries("d/f", "g"), "\\/\n")),
			crate: []string{"g"},
			git:   []string{"d", "d/f", "g"},
		},
		{
			// The crate follows a symbolic link .gitignore; git does not read
			// it.
			name: "symlinked ignore file",
			tree: with(append(entries("x", "y"),
				specEntry{Kind: 'f', Path: "proj/rules", Content: "x\n"},
				specEntry{Kind: 'l', Path: "proj/.gitignore", Target: "rules"})),
			crate: []string{"rules", "y"},
			git:   []string{"rules", "x", "y"},
		},
		{
			// Ignore files above the repository apply to the walk (the walk
			// does not look for a repository), but not to git.
			name: "ignore file above the repository",
			tree: append(with(entries("x", "y")),
				specEntry{Kind: 'f', Path: ".gitignore", Content: "x\n"}),
			crate: []string{"y"},
			git:   []string{"x", "y"},
		},
		{
			// A .git/info/exclude is read in every directory that has one, also
			// in a nested repository the outer repository would not look into.
			name: "exclude file of a nested repository",
			tree: with(append(entries("sub/x", "sub/y"),
				specEntry{Kind: 'f', Path: "proj/sub/.git/info/exclude", Content: "x\n"})),
			crate: []string{"sub", "sub/y"},
			noGit: true,
		},
		{
			// Rules never apply to the walk root itself: walking from inside an
			// ignored directory lists its contents. (Git considers them all
			// ignored.)
			name: "walk root inside an ignored directory",
			tree: with(withIgnore(entries("build/x", "build/y/z", "other"), "build/\n")),
			root: "build",
			// Relative to proj: the walk lists below proj/build only.
			crate: []string{"build/x", "build/y", "build/y/z"},
			noGit: true,
		},
		{
			// A rule of a parent directory applies to the entries below the
			// walk root by their path relative to the parent's directory.
			name:  "parent rules apply below the walk root",
			tree:  with(withIgnore(entries("sub/x.log", "sub/y.txt", "sub/deep/z.log"), "sub/*.log\n")),
			root:  "sub",
			crate: []string{"sub/deep", "sub/deep/z.log", "sub/y.txt"},
			noGit: true,
		},
	}
}

func TestKnownDeviationsFromGit(t *testing.T) {
	haveGit := gitAvailable()
	for _, d := range deviations() {
		t.Run(d.name, func(t *testing.T) {
			base, err := canonicalTempDir(t)
			if err != nil {
				t.Fatal(err)
			}
			d.tree.build(t, base)
			repo := filepath.Join(base, "proj")
			got := sortedKeys(crateVisible(t, repo, d.root))
			got = slices.DeleteFunc(got, func(p string) bool { return filepath.Base(p) == ".gitignore" })
			if !slices.Equal(got, d.crate) {
				t.Errorf("the crate's listing: got %q, want %q", got, d.crate)
			}
			if d.noGit || !haveGit {
				return
			}
			initGit(t, repo)
			visible, _ := gitVisible(t, repo, "")
			want := sortedKeys(visible)
			want = slices.DeleteFunc(want, func(p string) bool { return filepath.Base(p) == ".gitignore" })
			if !slices.Equal(want, d.git) {
				t.Errorf("git's listing: got %q, documented %q", want, d.git)
			}
		})
	}
}

func canonicalTempDir(t *testing.T) (string, error) {
	t.Helper()
	dir := t.TempDir()
	return filepath.EvalSymlinks(dir)
}

func sortedKeys(m map[string]bool) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func initGit(t *testing.T, repo string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", repo)
	cmd.Env = gitEnvironment(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// TestDeviationsMatchTheRealCrate confirms the "crate" column against the
// reference program (COIGUE_WALKLIST), so that the table records what the
// crate does and not only what the port does.
func TestDeviationsMatchTheRealCrate(t *testing.T) {
	bin := os.Getenv("COIGUE_WALKLIST")
	if bin == "" {
		t.Skip("set COIGUE_WALKLIST to the walklist reference binary to run")
	}
	for _, d := range deviations() {
		t.Run(d.name, func(t *testing.T) {
			base, err := canonicalTempDir(t)
			if err != nil {
				t.Fatal(err)
			}
			d.tree.build(t, base)
			job := walkJob{Mode: modePicker, Kind: jobAbs, Rel: filepath.Join("proj", d.root)}
			lines := rustListings(t, bin, []walkJob{job}, []string{base})[0]
			prefix := ""
			if d.root != "" {
				prefix = d.root + "/"
			}
			var got []string
			for _, l := range lines {
				if !strings.HasPrefix(l, "E ") || l[2] == 'l' {
					continue
				}
				fields := strings.SplitN(l, " ", 5)
				if fields[2] == "0" || strings.HasSuffix(fields[4], ".gitignore") {
					continue
				}
				got = append(got, prefix+unescape(fields[4]))
			}
			sort.Strings(got)
			want := slices.Clone(d.crate)
			// The symlinked ignore file is a link entry, skipped above.
			if !slices.Equal(got, want) {
				t.Errorf("the real crate lists %q, the table says %q", got, want)
			}
		})
	}
}

// unescape reverses escapeBytes.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s)+0 && s[i+1] == 'x' {
			var v byte
			for _, c := range []byte(s[i+2 : i+4]) {
				v <<= 4
				switch {
				case c >= 'a':
					v |= c - 'a' + 10
				default:
					v |= c - '0'
				}
			}
			b.WriteByte(v)
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
