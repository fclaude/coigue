package walk_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/walk"
)

func tree(t *testing.T, spec ...string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range spec {
		path, content, _ := strings.Cut(s, "=")
		full := filepath.Join(dir, path)
		if strings.HasSuffix(path, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type result struct {
	paths  []string // relative to the root, below it, in walk order
	kinds  map[string]walk.Kind
	errors int
	flags  map[string]bool // entries carrying an ignore-file error
}

func run(t *testing.T, root string, opts walk.Options) result {
	t.Helper()
	r := result{kinds: map[string]walk.Kind{}, flags: map[string]bool{}}
	w := walk.New(root, opts)
	for {
		item, ok := w.Next(context.Background())
		if !ok {
			return r
		}
		if item.Err != nil {
			r.errors++
			continue
		}
		rel, _ := fsx.StripPrefix(item.Entry.Path, root)
		if item.Entry.Depth > 0 {
			r.paths = append(r.paths, rel)
			r.kinds[rel] = item.Entry.Kind
		}
		if item.Entry.Err != nil {
			r.flags[rel] = true
		}
	}
}

func sortedPaths(r result) []string {
	p := slices.Clone(r.paths)
	sort.Strings(p)
	return p
}

var pickerOpts = walk.Options{GitIgnore: true, GitExclude: true, Parents: true,
	Filter: func(e *walk.Entry) bool { return e.Name != ".git" }}

// The tests below follow the unit tests of ignore::walk (ignore-0.4.33).

func TestNoIgnores(t *testing.T) {
	root := tree(t, "a", "b", "c/d", "c/e/f", ".hidden")
	got := run(t, root, walk.Options{})
	want := []string{".hidden", "a", "b", "c", "c/d", "c/e", "c/e/f"}
	if !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestHiddenSkipsDotFiles(t *testing.T) {
	root := tree(t, "a", ".b", ".c/d", "e/.f", "e/g")
	got := run(t, root, walk.Options{Hidden: true})
	if want := []string{"a", "e", "e/g"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestGitignoreRequiresNoRepository(t *testing.T) {
	root := tree(t, ".gitignore=foo\n!bar\n", "foo", "bar", "baz", "a/foo", "a/bar")
	got := run(t, root, pickerOpts)
	if want := []string{".gitignore", "a", "a/bar", "bar", "baz"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestGitExcludeAndGitignorePrecedence(t *testing.T) {
	// .gitignore beats .git/info/exclude whatever the nesting, and the nearest
	// ignore file with an opinion wins.
	root := tree(t,
		".git/info/exclude=*.tmp\nkeep.log\n",
		".gitignore=*.log\n",
		"x.tmp", "x.log", "keep.log",
		"sub/.gitignore=!x.log\n!x.tmp\n",
		"sub/x.log", "sub/x.tmp", "sub/y.log")
	got := run(t, root, pickerOpts)
	want := []string{".gitignore", "sub", "sub/.gitignore", "sub/x.log", "sub/x.tmp"}
	if !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestGitignoreParent(t *testing.T) {
	root := tree(t, ".gitignore=*.a\n", "foo.a", "bar/baz", "bar/foo.a", "bar/baz.a")
	got := run(t, filepath.Join(root, "bar"), pickerOpts)
	if want := []string{"baz"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
	// The rules of a parent do not stop at a repository boundary.
	nested := tree(t, ".gitignore=*.a\n", "inner/.git/HEAD", "inner/x.a", "inner/y")
	got = run(t, filepath.Join(nested, "inner"), pickerOpts)
	if want := []string{"y"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestParentsOffIgnoresAncestors(t *testing.T) {
	root := tree(t, ".gitignore=*.a\n", "bar/baz.a", "bar/baz")
	opts := pickerOpts
	opts.Parents = false
	// With the parents rule off, the ancestors' ignore files are not applied
	// to what is below the walk root (the walk root's own .gitignore is).
	got := run(t, filepath.Join(root, "bar"), opts)
	if want := []string{"baz", "baz.a"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestMaxDepth(t *testing.T) {
	root := tree(t, "a", "b/c", "b/d/e", "f/.gitignore=g\n", "f/g", "f/h")
	opts := pickerOpts
	opts.MaxDepth = 1
	got := run(t, root, opts)
	if want := []string{"a", "b", "f"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
	opts.MaxDepth = 2
	got = run(t, root, opts)
	if want := []string{"a", "b", "b/c", "b/d", "f", "f/.gitignore", "f/h"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := tree(t, "real/a", "file")
	if err := os.Symlink("real", filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "filelink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	got := run(t, root, walk.Options{})
	if want := []string{"dirlink", "file", "filelink", "loop", "real", "real/a"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
	for _, name := range []string{"dirlink", "filelink", "loop"} {
		if got.kinds[name] != walk.KindSymlink {
			t.Errorf("%s has kind %v, want symlink", name, got.kinds[name])
		}
	}
	// A directory-only pattern does not match a link to a directory.
	ignoring := tree(t, ".gitignore=dirlink/\nreal/\n", "real/a")
	if err := os.Symlink("real", filepath.Join(ignoring, "dirlink")); err != nil {
		t.Fatal(err)
	}
	got = run(t, ignoring, pickerOpts)
	if want := []string{".gitignore", "dirlink"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestRootThatIsASymlinkOrAFileIsWalked(t *testing.T) {
	root := tree(t, "real/a", "real/b/c", "file")
	link := root + "-link"
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Fatal(err)
	}
	got := run(t, link, walk.Options{})
	if want := []string{"a", "b", "b/c"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("walking a link: got %q, want %q", sortedPaths(got), want)
	}
	w := walk.New(filepath.Join(root, "file"), walk.Options{})
	item, ok := w.Next(context.Background())
	if !ok || item.Err != nil || item.Entry.Kind != walk.KindFile || item.Entry.Depth != 0 {
		t.Fatalf("walking a file gave %+v, %v", item, ok)
	}
	if _, ok := w.Next(context.Background()); ok {
		t.Error("a file root has no descendants")
	}
	fileLink := filepath.Join(root, "file-link")
	if err := os.Symlink("file", fileLink); err != nil {
		t.Fatal(err)
	}
	w = walk.New(fileLink, walk.Options{})
	item, ok = w.Next(context.Background())
	if !ok || item.Err != nil || item.Entry.Kind != walk.KindFile {
		t.Errorf("a link to a file is followed and reported as a file: %+v", item)
	}
}

func TestMissingAndDanglingRootsYieldOneError(t *testing.T) {
	root := tree(t, "a")
	if err := os.Symlink("nowhere", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "dangling"} {
		w := walk.New(filepath.Join(root, name), pickerOpts)
		item, ok := w.Next(context.Background())
		if !ok || item.Err == nil {
			t.Errorf("%s: got %+v, %v, want an error", name, item, ok)
		}
		if _, ok := w.Next(context.Background()); ok {
			t.Errorf("%s: the walk continued after the error", name)
		}
	}
}

func TestFilterPrunesDirectoriesAndFilesButNotTheRoot(t *testing.T) {
	root := tree(t, "a", "skip/b", "x/skip", "x/y", "x/.git/z")
	opts := walk.Options{Filter: func(e *walk.Entry) bool { return e.Name != "skip" && e.Name != ".git" }}
	got := run(t, root, opts)
	if want := []string{"a", "x", "x/y"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
	// The root is exempt: a walk may start inside a directory the filter
	// would reject.
	got = run(t, filepath.Join(root, "skip"), opts)
	if want := []string{"b"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestHiddenEntryWhitelistedByAnIgnoreFileIsListed(t *testing.T) {
	root := tree(t, ".gitignore=!.keep\n", ".keep", ".drop", "x")
	opts := pickerOpts
	opts.Hidden = true
	got := run(t, root, opts)
	if want := []string{".keep", "x"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
}

func TestIgnoreFileErrorsAreAttachedToTheDirectoryEntry(t *testing.T) {
	root := tree(t, "ok/.gitignore=a\n", "bad/.gitignore=[z-a]\nb\n", "bad/b", "bad/c", "ok/a", "ok/c")
	got := run(t, root, pickerOpts)
	if want := []string{"bad", "bad/.gitignore", "bad/c", "ok", "ok/.gitignore", "ok/c"}; !slices.Equal(sortedPaths(got), want) {
		t.Errorf("got %q, want %q", sortedPaths(got), want)
	}
	if !got.flags["bad"] || len(got.flags) != 1 {
		t.Errorf("flagged entries %v, want only bad", got.flags)
	}
	// A bad glob in an ancestor's file is reported once for the walk.
	parent := tree(t, ".gitignore=[z-a]\n", "sub/a")
	got = run(t, filepath.Join(parent, "sub"), pickerOpts)
	if got.errors != 1 || !slices.Equal(got.paths, []string{"a"}) {
		t.Errorf("errors %d paths %q", got.errors, got.paths)
	}
}

// requireCaseSensitive skips a test that creates names differing only in case,
// which a case-insensitive volume (macOS by default) cannot hold.
func requireCaseSensitive(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "X")); err == nil {
		t.Skip("this test needs a case-sensitive file system")
	}
}

func TestEntriesComeInNameOrderDepthFirst(t *testing.T) {
	requireCaseSensitive(t)
	root := tree(t, "b/z", "b/a", "a/b/c", "a.txt", "B")
	got := run(t, root, walk.Options{})
	want := []string{"B", "a", "a/b", "a/b/c", "a.txt", "b", "b/a", "b/z"}
	if !slices.Equal(got.paths, want) {
		t.Errorf("got %q, want %q", got.paths, want)
	}
}

func TestUnreadableDirectoryIsAnErrorNotTheEnd(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	root := tree(t, "a/x", "locked/y", "z")
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	got := run(t, root, walk.Options{})
	if want := []string{"a", "a/x", "locked", "z"}; !slices.Equal(got.paths, want) {
		t.Errorf("got %q, want %q", got.paths, want)
	}
	if got.errors != 1 {
		t.Errorf("errors = %d, want 1", got.errors)
	}
	// At the depth limit the directory is not opened, so there is no error.
	got = run(t, root, walk.Options{MaxDepth: 1})
	if got.errors != 0 {
		t.Errorf("errors = %d at max depth, want 0", got.errors)
	}
}

func TestCancellationStopsTheWalkBetweenEntries(t *testing.T) {
	var spec []string
	for i := 0; i < 50; i++ {
		spec = append(spec, "d/f"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	root := tree(t, spec...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := walk.New(root, walk.Options{})
	seen := 0
	for {
		if _, ok := w.Next(ctx); !ok {
			break
		}
		seen++
		if seen == 5 {
			cancel()
		}
	}
	if seen != 5 {
		t.Errorf("saw %d entries; the walk should stop right after cancellation", seen)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Error("expected the context to be cancelled")
	}
	// A context that is done before the first call yields nothing.
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, ok := walk.New(root, walk.Options{}).Next(cancelled); ok {
		t.Error("a cancelled context must end the walk at once")
	}
}

func TestNextAfterTheEndStaysAtTheEnd(t *testing.T) {
	w := walk.New(tree(t, "a"), walk.Options{})
	for range 10 {
		if _, ok := w.Next(context.Background()); !ok {
			break
		}
	}
	deadline := time.Now().Add(time.Second)
	for i := 0; i < 3 && time.Now().Before(deadline); i++ {
		if _, ok := w.Next(context.Background()); ok {
			t.Fatal("Next returned an item after the end")
		}
	}
}
