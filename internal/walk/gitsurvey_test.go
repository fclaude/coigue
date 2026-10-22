package walk_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// surveyTree is a fixed tree whose names interact with the pattern alphabet.
var surveyTree = func() treeSpec {
	var s treeSpec
	for _, d := range []string{"proj", "proj/dir", "proj/dir/sub", "proj/dir/sub/deep", "proj/sub", "proj/sub/dir", "proj/foo", "proj/foo/b", "proj/src", "proj/src/build", "proj/build", "proj/.cache"} {
		s = append(s, specEntry{Kind: 'd', Path: d})
	}
	for _, f := range []string{
		"a", "ab", "abc", "b", "c", "foo.txt", "foo.log", "x y", "#h", "!bang", "a[b", "[a]", "x{y,z}", "a,b", "a\\b", "é", "é.txt", "€",
		"日本", ".hidden", "t.tmp", "ta", "a.b.c", "trail.", "dir/a", "dir/b.txt", "dir/sub/a", "dir/sub/deep/foo.log", "foo/a", "foo/b/c",
		"src/main.rs", "src/build/x", "build/y", "sub/a", "sub/dir/b", ".cache/z", "dir/.hidden", "foo/ab",
	} {
		s = append(s, specEntry{Kind: 'f', Path: "proj/" + f})
	}
	return s
}()

// TestSurveyAgainstGit runs every line of a large list as the only line of
// the root .gitignore of surveyTree and prints where the crate and git
// disagree about which entries are listed. It is a tool for maintaining the
// notes on deviations, not a regression test.
func TestSurveyAgainstGit(t *testing.T) {
	if os.Getenv("COIGUE_WALK_GIT_SURVEY") == "" || !gitAvailable() {
		t.Skip("set COIGUE_WALK_GIT_SURVEY=1 (needs git) to run the survey")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var lines []string
	add := func(l string) {
		if !seen[l] {
			seen[l] = true
			lines = append(lines, l)
		}
	}
	for _, l := range wholeLines {
		add(l)
	}
	names := []string{"a", "foo", "*.log", "dir", "sub", "build", "x y", "é", ".hidden", "#h", "a[b", "x{y,z}", "src/build", "dir/sub", "foo.txt", "b", "c"}
	for _, p := range linePrefixes {
		for _, n := range names {
			for _, s := range lineSuffixes {
				add(p + n + s)
			}
		}
	}
	for _, a := range lineAtoms {
		for _, b := range lineAtoms {
			add(a + b)
		}
		add("dir/" + a)
		add(a + "/b")
		add("a" + a + "b")
		add("!" + a)
		add(a + "/")
	}
	for _, mid := range []string{"[!a]", "[^a]", "[a-z]", "?", "[/]", "[!/]", "*", "**", "[a/]", "[!a/]", "[,-0]", "[--0]", "[+--]"} {
		add("dir" + mid + "sub")
		add("dir" + mid + "sub/a")
		add("dir/sub" + mid + "a")
		add("/dir" + mid + "sub")
		add("src" + mid + "build")
	}
	repoDir := filepath.Join(base, "survey")
	surveyTree.build(t, repoDir)
	repo := filepath.Join(repoDir, "proj")
	initGit(t, repo)
	type outcome struct{ onlyGit, onlyCrate []string }
	diffs := map[string]outcome{}
	for _, line := range lines {
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		want, _ := gitVisible(t, repo, "")
		got := crateVisible(t, repo, "")
		var o outcome
		for p := range want {
			if !got[p] {
				o.onlyGit = append(o.onlyGit, p)
			}
		}
		for p := range got {
			if !want[p] {
				o.onlyCrate = append(o.onlyCrate, p)
			}
		}
		if len(o.onlyGit)+len(o.onlyCrate) > 0 {
			sort.Strings(o.onlyGit)
			sort.Strings(o.onlyCrate)
			diffs[line] = o
		}
	}
	var keys []string
	for k := range diffs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%q\n    git lists, crate not: %q\n    crate lists, git not: %q\n", k, diffs[k].onlyGit, diffs[k].onlyCrate)
	}
	_ = os.WriteFile(os.Getenv("COIGUE_WALK_GIT_SURVEY"), []byte(b.String()), 0o644)
	t.Logf("%d lines surveyed, %d differ", len(lines), len(diffs))
}
