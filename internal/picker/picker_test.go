package picker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/fileops"
	"github.com/fclaude/coigue/internal/fsx"
)

// finish polls until the scan is done, like the Rust tests' three-second loops.
func finish(t *testing.T, p *Picker) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for p.Scanning {
		p.Poll()
		if !time.Now().Before(deadline) {
			t.Fatal("the scan did not finish in 3 s")
		}
		time.Sleep(50 * time.Microsecond)
	}
}

func discovered(t *testing.T, root string) []string {
	t.Helper()
	p := New(context.Background(), root)
	defer p.Close()
	finish(t, p)
	labels := slices.Clone(p.labels)
	sort.Strings(labels)
	return labels
}

func mkdirAll(t *testing.T, path ...string) {
	t.Helper()
	for _, p := range path {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sorted(names ...string) []string {
	sort.Strings(names)
	return names
}

// Rust: picker::tests::gitignore_patterns_nested_overrides_and_excluded_directories
func TestGitignorePatternsNestedOverridesAndExcludedDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".git/info", "src/deep", "target", "cache"} {
		mkdirAll(t, filepath.Join(dir, name))
	}
	write(t, filepath.Join(dir, ".gitignore"),
		"*.log\n!keep.log\n/root.txt\ntarget/\ncache/*\n!cache/keep.txt\n**/generated-*.rs\n\\#literal\n\\!literal\nwith space.txt\n")
	write(t, filepath.Join(dir, "src/.gitignore"), "!local.log\nprivate.rs\n")
	write(t, filepath.Join(dir, ".git/info/exclude"), "local-only\n")
	for _, name := range []string{
		"debug.log",
		"keep.log",
		"root.txt",
		"src/root.txt",
		"src/local.log",
		"src/deep/debug.log",
		"src/private.rs",
		"src/main.rs",
		"src/generated-code.rs",
		"target/artifact",
		"cache/drop.txt",
		"cache/keep.txt",
		"#literal",
		"!literal",
		"with space.txt",
		"local-only",
		".hidden",
		"visible",
	} {
		write(t, filepath.Join(dir, name), "")
	}
	expected := sorted(
		".gitignore",
		"src/.gitignore",
		"keep.log",
		"src/root.txt",
		"src/local.log",
		"src/main.rs",
		"cache/keep.txt",
		".hidden",
		"visible",
	)
	if got := discovered(t, dir); !slices.Equal(got, expected) {
		t.Errorf("discovered %q, want %q", got, expected)
	}
	// Starting below the repo root still honors parent rules, but returns
	// only descendants of the working directory.
	nested := sorted(".gitignore", "root.txt", "local.log", "main.rs")
	if got := discovered(t, filepath.Join(dir, "src")); !slices.Equal(got, nested) {
		t.Errorf("discovered below the root %q, want %q", got, nested)
	}
}

// Rust: picker::tests::gitignore_works_without_a_repository_and_reloads_on_reopen
func TestGitignoreWorksWithoutARepositoryAndReloadsOnReopen(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".gitignore"), "excluded\n")
	write(t, filepath.Join(dir, "excluded"), "")
	if slices.Contains(discovered(t, dir), "excluded") {
		t.Error("an ignored file was discovered")
	}
	write(t, filepath.Join(dir, ".gitignore"), "")
	if !slices.Contains(discovered(t, dir), "excluded") {
		t.Error("the file stayed hidden after the ignore file was emptied")
	}
}

// Rust: picker::tests::fuzzy_paths_unicode_and_ranking
func TestFuzzyPathsUnicodeAndRanking(t *testing.T) {
	has := func(label, query string) bool { _, ok := Score(label, query); return ok }
	score := func(label, query string) int64 {
		s, ok := Score(label, query)
		if !ok {
			t.Fatalf("%q does not match %q", label, query)
		}
		return s
	}
	if !has("src/editor.rs", "s/edr") {
		t.Error("s/edr should match src/editor.rs")
	}
	if has("src/editor.rs", "deri") {
		t.Error("deri must not match src/editor.rs")
	}
	if !has("CAFÉ.rs", "café") {
		t.Error("café should match CAFÉ.rs")
	}
	if !(score("rust/main.rs", "rs") > score("rust/misc.txt", "rs")) {
		t.Error("a basename match should rank higher")
	}
	if has("a.rs", "aa") {
		t.Error("each query character needs its own label character")
	}
	if !(score("src/main.rs", "main") > score("many/ancient/items/notes.rs", "main")) {
		t.Error("a contiguous basename match should beat a scattered one")
	}
}

// Rust: picker::tests::unreadable_root_and_cancellation
func TestUnreadableRootAndCancellation(t *testing.T) {
	dir := t.TempDir()
	p := New(context.Background(), filepath.Join(dir, "missing"))
	finish(t, p)
	if p.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", p.Skipped)
	}
	if p.Len() != 0 {
		t.Errorf("len = %d, want 0", p.Len())
	}
	// Dropping the picker cancels the scan (Rust: the shared flag is set).
	p.Close()
	p.Close() // idempotent
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the scan goroutine did not exit after Close")
	}
}

// Rust: picker::tests::discovery_is_recursive_and_skips_git_and_symlinks
func TestDiscoveryIsRecursiveAndSkipsGitAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "nested/.git"), filepath.Join(dir, fileops.Trash))
	write(t, filepath.Join(dir, fileops.Trash, "item"), "")
	for _, name := range []string{"nested/café.rs", "alpha.rs", ".hidden", "nested/.git/config"} {
		write(t, filepath.Join(dir, name), "")
	}
	if err := os.Symlink(dir, filepath.Join(dir, "loop")); err != nil {
		t.Fatal(err)
	}
	p := New(context.Background(), dir)
	defer p.Close()
	finish(t, p)
	if p.Total() != 3 {
		t.Fatalf("total = %d, want 3", p.Total())
	}
	if label, ok := p.Label(0); !ok || label != ".hidden" {
		t.Errorf("label(0) = %q, %v", label, ok)
	}
	p.Query = "N/CF"
	p.QueryChanged()
	if p.Len() != 1 {
		t.Fatalf("len = %d, want 1", p.Len())
	}
	if label, ok := p.Label(0); !ok || label != "nested/café.rs" {
		t.Errorf("label(0) = %q, %v", label, ok)
	}
	if path, ok := p.Path(); !ok || !fsx.EndsWith(path, "nested/café.rs") {
		t.Errorf("path = %q, %v", path, ok)
	}
	p.Query = "no-match"
	p.QueryChanged()
	p.MoveBy(100)
	if path, ok := p.Path(); ok {
		t.Errorf("path = %q, want none", path)
	}
}

// The scan hands over 128 files per batch and Poll takes at most 32 batches
// per call, so a big tree needs more than one Poll even when the scan is over.
func TestPollTakesAtMostThirtyTwoBatches(t *testing.T) {
	dir := t.TempDir()
	const files = 32*128 + 300
	for i := 0; i < files; i++ {
		write(t, filepath.Join(dir, fmt.Sprintf("f%05d", i)), "")
	}
	p := New(context.Background(), dir)
	defer p.Close()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the scan did not finish")
	}
	p.Poll()
	if p.Total() != 32*128 {
		t.Errorf("first poll found %d files, want %d", p.Total(), 32*128)
	}
	if !p.Scanning {
		t.Error("the scan cannot be known to be finished while batches are queued")
	}
	p.Poll()
	if p.Total() != files || p.Scanning {
		t.Errorf("second poll: total %d, scanning %v", p.Total(), p.Scanning)
	}
}

func TestCancelledScanStops(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		write(t, filepath.Join(dir, fmt.Sprintf("f%03d", i)), "")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := New(ctx, dir)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("a scan started with a cancelled context did not stop")
	}
	p.Poll()
	p.Poll()
	if p.Scanning {
		t.Error("Poll should see the finished producer")
	}
	if p.Total() != 0 {
		t.Errorf("a cancelled scan reported %d files", p.Total())
	}
}

func TestSelectionFollowsTheSelectedFileWhenFilesArrive(t *testing.T) {
	p := &Picker{Root: "/r"}
	p.labels = []string{"b", "a", "c"}
	p.files = []string{"/r/b", "/r/a", "/r/c"}
	p.filter()
	if got := labelsOf(p); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("order %q", got)
	}
	p.MoveBy(2)
	if label, _ := p.Label(p.Selected); label != "c" {
		t.Fatalf("selected %q", label)
	}
	p.labels = append(p.labels, "0")
	p.files = append(p.files, "/r/0")
	p.filter()
	if label, _ := p.Label(p.Selected); label != "c" {
		t.Errorf("the selection moved to %q", label)
	}
	p.MoveBy(-100)
	if p.Selected != 0 {
		t.Errorf("selected = %d", p.Selected)
	}
}

func labelsOf(p *Picker) []string {
	var out []string
	for i := 0; i < p.Len(); i++ {
		label, _ := p.Label(i)
		out = append(out, label)
	}
	return out
}

func TestNonUTF8NamesGetLossyLabelsButKeepTheirPaths(t *testing.T) {
	dir := t.TempDir()
	name := "f\xff\xe2\x82"
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); errors.Is(err, syscall.EILSEQ) {
		t.Skip("this file system rejects file names that are not UTF-8, as APFS does")
	} else if err != nil {
		t.Fatal(err)
	}
	p := New(context.Background(), dir)
	defer p.Close()
	finish(t, p)
	if label, ok := p.Label(0); !ok || label != "f��" {
		t.Errorf("label = %q, %v", label, ok)
	}
	if path, ok := p.Path(); !ok || !strings.HasSuffix(path, name) {
		t.Errorf("path = %q, %v", path, ok)
	}
}

func TestRankingIsScoreThenLabel(t *testing.T) {
	p := &Picker{Root: "/r"}
	for _, l := range []string{"zz/a.rs", "a.rs", "b.rs", "sub/a.rs", "yy/a.rs"} {
		p.labels = append(p.labels, l)
		p.files = append(p.files, "/r/"+l)
	}
	p.Query = "a.rs"
	p.QueryChanged()
	// Shorter labels score higher; equal scores sort by label.
	if got := labelsOf(p); !slices.Equal(got, []string{"a.rs", "yy/a.rs", "zz/a.rs", "sub/a.rs"}) {
		t.Errorf("ranked %q", got)
	}
}

func TestCloseDuringALargeScanStopsIt(t *testing.T) {
	dir := t.TempDir()
	for d := 0; d < 40; d++ {
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", d))
		mkdirAll(t, sub)
		for i := 0; i < 100; i++ {
			write(t, filepath.Join(sub, fmt.Sprintf("f%03d", i)), "")
		}
	}
	p := New(context.Background(), dir)
	p.Poll()
	p.Close()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the scan did not stop after Close")
	}
	// Whatever was found stays usable.
	p.Poll()
	p.Poll()
	if p.Total() > 4000 {
		t.Errorf("total = %d", p.Total())
	}
}
