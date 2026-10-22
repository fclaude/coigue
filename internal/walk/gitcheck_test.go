package walk_test

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/walk"
)

// gitVisible asks `git check-ignore --no-index` about every path of repo and
// returns the paths git does not consider ignored (git reports a path below an
// ignored directory as ignored, because it never looks inside). The second
// result says that git ignores root itself (or a directory above it); the
// crate, which never tests the walk root, lists such a tree anyway, so the
// two cannot be compared.
func gitVisible(t testing.TB, repo, root string) (map[string]bool, bool) {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repo, path)
		if rel == "." {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdin bytes.Buffer
	for _, p := range paths {
		stdin.WriteString(p)
		stdin.WriteByte(0)
	}
	cmd := exec.Command("git", "-C", repo, "-c", "core.quotePath=off", "check-ignore", "--no-index", "-v", "-n", "-z", "--stdin")
	cmd.Stdin = &stdin
	cmd.Env = gitEnvironment(t)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		// Exit status 1 only says that no path matched.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("git check-ignore: %v\n%s", err, stderr.String())
		}
	}
	fields := strings.Split(strings.TrimSuffix(out.String(), "\x00"), "\x00")
	if len(fields)%4 != 0 {
		t.Fatalf("unexpected check-ignore output: %q", out.String())
	}
	ignored := map[string]bool{}
	for i := 0; i+3 < len(fields); i += 4 {
		pattern, path := fields[i+2], fields[i+3]
		ignored[path] = pattern != "" && !strings.HasPrefix(pattern, "!")
	}
	visible := map[string]bool{}
	for _, p := range paths {
		ok := true
		for q := p; q != "." && q != ""; q = filepath.Dir(q) {
			if ignored[q] || filepath.Base(q) == ".coigue-trash" {
				ok = false
				break
			}
		}
		if ok {
			visible[p] = true
		}
	}
	return visible, root != "" && ignored[root]
}

// gitFriendly turns a generated tree into one git can be asked about: no
// repositories inside it except the root one (git would stop at them), no
// unreadable directories, no ignore files above the repository (git does not
// read those). It returns the content for .git/info/exclude.
func gitFriendly(spec treeSpec) (treeSpec, string) {
	var out treeSpec
	exclude := ""
	for _, e := range spec {
		if e.Path == "proj/.git/info/exclude" {
			exclude = e.Content
		}
		if e.Path == ".gitignore" || strings.HasPrefix(e.Path, ".gitignore/") || e.Mode != 0 || hasComponent(e.Path, ".git") {
			continue
		}
		out = append(out, e)
	}
	return out, exclude
}

// crateVisible lists the paths the picker configuration walks below root,
// relative to the repository.
func crateVisible(t testing.TB, repo, root string) map[string]bool {
	t.Helper()
	visible := map[string]bool{}
	w := walk.New(filepath.Join(repo, root), modePicker.options())
	for {
		item, ok := w.Next(context.Background())
		if !ok {
			return visible
		}
		if item.Err != nil || item.Entry.Depth == 0 {
			continue
		}
		rel, _ := filepath.Rel(repo, item.Entry.Path)
		visible[rel] = true
	}
}

// gitEnvironment keeps the user's git configuration (global excludes, say)
// out of the comparison.
func gitEnvironment(t testing.TB) []string {
	home := t.TempDir()
	return append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

func hasComponent(path, name string) bool {
	for _, c := range strings.Split(path, "/") {
		if c == name {
			return true
		}
	}
	return false
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// TestAgainstGitCheckIgnore compares the walker with `git check-ignore
// --no-index` on random trees that stay within the constructs the crate and
// git agree on. COIGUE_WALK_GIT_FULL=1 uses the whole pattern alphabet and
// only reports, to survey where the crate deviates from git (see the notes in
// the package documentation of this file's neighbours).
func TestAgainstGitCheckIgnore(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git is not installed")
	}
	full := os.Getenv("COIGUE_WALK_GIT_FULL") != ""
	trees := envInt("COIGUE_WALK_GIT_TREES", 150)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if found := ancestorsHaveIgnoreFiles(base); found != "" {
		t.Skipf("%s would add rules to every walk", found)
	}
	agree, disagree, skipped := 0, 0, 0
	for i := 0; i < trees; i++ {
		r := rand.New(rand.NewSource(int64(7_000_000 + i)))
		spec, exclude := gitFriendly(genTreeFor(r, !full))
		dir := filepath.Join(base, fmt.Sprintf("g%d", i))
		spec.build(t, dir)
		repo := filepath.Join(dir, "proj")
		initGit(t, repo)
		if exclude != "" {
			if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte(exclude), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		roots := []string{""}
		for _, e := range spec {
			if e.Kind == 'd' && strings.HasPrefix(e.Path, "proj/") && !hasComponent(e.Path, ".coigue-trash") && r.Intn(6) == 0 {
				roots = append(roots, strings.TrimPrefix(e.Path, "proj/"))
			}
		}
		for _, root := range roots {
			want, rootIgnored := gitVisible(t, repo, root)
			if rootIgnored {
				skipped++
				continue
			}
			got := crateVisible(t, repo, root)
			if root != "" {
				for p := range want {
					if !strings.HasPrefix(p, root+"/") {
						delete(want, p)
					}
				}
			}
			var onlyGit, onlyCrate []string
			for p := range want {
				if !got[p] {
					onlyGit = append(onlyGit, p)
				}
			}
			for p := range got {
				if !want[p] {
					onlyCrate = append(onlyCrate, p)
				}
			}
			sort.Strings(onlyGit)
			sort.Strings(onlyCrate)
			if len(onlyGit)+len(onlyCrate) == 0 {
				agree++
				continue
			}
			disagree++
			msg := fmt.Sprintf("tree %d root %q: git lists but the crate does not: %q; the crate lists but git does not: %q\n%s",
				i, root, onlyGit, onlyCrate, describeSpec(spec))
			if exclude != "" {
				msg += fmt.Sprintf("  .git/info/exclude: %q\n", exclude)
			}
			if full {
				t.Log(msg)
			} else {
				t.Error(msg)
			}
		}
	}
	t.Logf("%d trees: %d walks agree with git, %d differ, %d roots skipped because git ignores them (full alphabet: %v)", trees, agree, disagree, skipped, full)
}
