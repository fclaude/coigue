package explorer

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/fileops"
	"github.com/fclaude/coigue/internal/fsx"
)

// ready polls until the listing arrived and fits four rows, as the Rust
// tests' helper does with a three-second deadline.
func ready(t *testing.T, view *Explorer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for view.Loading {
		view.Poll()
		if !time.Now().Before(deadline) {
			t.Fatal("the listing did not arrive in 3 s")
		}
		time.Sleep(50 * time.Microsecond)
	}
	view.Fit(4)
}

func names(view *Explorer) []string {
	out := []string{}
	for i := 0; i < view.Len(); i++ {
		entry, _ := view.Entry(i)
		out = append(out, entry.Name)
	}
	return out
}

func root(t *testing.T) string {
	t.Helper()
	dir, err := fsx.Canonicalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func selectedName(t *testing.T, view *Explorer) string {
	t.Helper()
	entry, ok := view.Entry(view.Selected)
	if !ok {
		t.Fatalf("nothing selected (selected %d of %d)", view.Selected, view.Len())
	}
	return entry.Name
}

// Rust: explorer::tests::directories_first_visibility_filtering_and_revealing_current_file
func TestDirectoriesFirstVisibilityFilteringAndRevealingCurrentFile(t *testing.T) {
	root := root(t)
	mkdir(t, fsx.Join(root, "nested"))
	mkdir(t, fsx.Join(root, ".git"))
	mkdir(t, fsx.Join(root, fileops.Trash))
	for _, name := range []string{"alpha", "café.rs", ".secret", "generated"} {
		write(t, fsx.Join(root, name), "")
	}
	write(t, fsx.Join(root, ".gitignore"), "generated\n")
	if err := os.Symlink(root, fsx.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	view := New(context.Background(), root, fsx.Join(root, "generated"), Memory{})
	defer view.Close()
	ready(t, view)
	if got, want := names(view), []string{"..", "nested", "alpha", "café.rs", "generated"}; !slices.Equal(got, want) {
		t.Fatalf("names %q, want %q", got, want)
	}
	if got := selectedName(t, view); got != "generated" {
		t.Errorf("selected %q, want generated", got)
	}
	view.ToggleVisibility()
	ready(t, view)
	all := names(view)
	if !slices.Contains(all, ".secret") {
		t.Error("show-all must list hidden files")
	}
	if slices.Contains(all, ".git") {
		t.Error(".git must stay excluded")
	}
	if slices.Contains(all, fileops.Trash) {
		t.Error("the recovery directory must stay excluded")
	}
	if slices.Contains(all, "loop") {
		t.Error("symlinks must stay excluded")
	}
	view.ToggleVisibility()
	ready(t, view)
	if slices.Contains(names(view), "generated") {
		t.Error("an ignored file reappeared after hiding again")
	}
	view.Selected = 3
	view.Fit(2)
	originSelected, originTop := view.Selected, view.Top
	view.StartFilter()
	view.Query = "CF"
	view.Filter()
	if got := names(view); !slices.Equal(got, []string{"café.rs"}) {
		t.Errorf("filtered names %q", got)
	}
	view.Query = "absent"
	view.Filter()
	view.MoveBy(50)
	view.Fit(1)
	if view.Len() != 0 {
		t.Errorf("len = %d", view.Len())
	}
	if view.Selected != 0 {
		t.Errorf("selected = %d", view.Selected)
	}
	view.CancelFilter()
	if view.Selected != originSelected || view.Top != originTop {
		t.Errorf("after cancel: selected %d top %d, want %d %d", view.Selected, view.Top, originSelected, originTop)
	}
}

// Rust: explorer::tests::parent_focus_and_directory_selection_scroll_are_remembered
func TestParentFocusAndDirectorySelectionScrollAreRemembered(t *testing.T) {
	root := root(t)
	child := fsx.Join(root, "child")
	mkdir(t, child)
	for i := 0; i < 15; i++ {
		write(t, fsx.Join(child, fmt.Sprintf("file%02d", i)), "")
	}
	view := New(context.Background(), child, fsx.Join(child, "file12"), Memory{})
	defer view.Close()
	ready(t, view)
	top := view.Top
	if top <= 0 {
		t.Fatalf("top = %d, want a scrolled list", top)
	}
	view.Parent()
	ready(t, view)
	if entry, _ := view.Entry(view.Selected); entry == nil || entry.Path != child {
		t.Errorf("after Parent the selection is %+v, want %s", entry, child)
	}
	view.Navigate(child, "")
	ready(t, view)
	if got := selectedName(t, view); got != "file12" {
		t.Errorf("selected %q, want file12", got)
	}
	if view.Top != top {
		t.Errorf("top = %d, want %d", view.Top, top)
	}
	view.Remember()
	memory := view.Memory
	view.Memory = Memory{}
	reopened := New(context.Background(), child, "", memory)
	defer reopened.Close()
	ready(t, reopened)
	if got := selectedName(t, reopened); got != "file12" {
		t.Errorf("reopened selected %q, want file12", got)
	}
	if reopened.Top != top {
		t.Errorf("reopened top = %d, want %d", reopened.Top, top)
	}
}

// Rust: explorer::tests::missing_directory_is_reported_and_can_be_left
func TestMissingDirectoryIsReportedAndCanBeLeft(t *testing.T) {
	root := root(t)
	view := New(context.Background(), fsx.Join(root, "missing"), "", Memory{})
	defer view.Close()
	ready(t, view)
	if view.Message == "" {
		t.Error("a missing directory must be reported")
	}
	if view.Message != "1 unreadable entries or ignore errors" {
		t.Errorf("message %q", view.Message)
	}
	view.Parent()
	ready(t, view)
	if view.Directory != root {
		t.Errorf("directory %q, want %q", view.Directory, root)
	}
}

func TestRevealShowsHiddenAndIgnoredEntriesOnlyForTheFocus(t *testing.T) {
	root := root(t)
	write(t, fsx.Join(root, ".hidden"), "")
	write(t, fsx.Join(root, "ignored"), "")
	write(t, fsx.Join(root, "plain"), "")
	write(t, fsx.Join(root, ".gitignore"), "ignored\n")
	mkdir(t, fsx.Join(root, ".git"))
	view := New(context.Background(), root, fsx.Join(root, ".hidden"), Memory{})
	defer view.Close()
	ready(t, view)
	if got, want := names(view), []string{"..", ".hidden", "plain"}; !slices.Equal(got, want) {
		t.Errorf("names %q, want %q", got, want)
	}
	// The reveal never exposes .git, and entries of other directories are not
	// revealed.
	view.Refresh(fsx.Join(root, ".git"))
	ready(t, view)
	if slices.Contains(names(view), ".git") {
		t.Error(".git was revealed")
	}
	view.Refresh(fsx.Join(root, "missing"))
	ready(t, view)
	if slices.Contains(names(view), "missing") {
		t.Error("a missing focus was revealed")
	}
	other := root + "-elsewhere"
	view.Refresh(fsx.Join(other, "x"))
	ready(t, view)
	if got, want := names(view), []string{"..", "plain"}; !slices.Equal(got, want) {
		t.Errorf("names %q, want %q", got, want)
	}
}

func TestIgnoreFileErrorsAreCounted(t *testing.T) {
	root := root(t)
	write(t, fsx.Join(root, ".gitignore"), "[z-a]\nok\n")
	write(t, fsx.Join(root, "ok"), "")
	write(t, fsx.Join(root, "kept"), "")
	view := New(context.Background(), root, "", Memory{})
	defer view.Close()
	ready(t, view)
	if view.Message != "1 unreadable entries or ignore errors" {
		t.Errorf("message %q", view.Message)
	}
	// The valid line still applies: only "ok" is dropped.
	if got := names(view); !slices.Equal(got, []string{"..", "kept"}) {
		t.Errorf("names %q", got)
	}
}

// A load that is replaced by another must never deliver its listing.
func TestStaleLoadIsCancelled(t *testing.T) {
	root := root(t)
	first := fsx.Join(root, "first")
	second := fsx.Join(root, "second")
	mkdir(t, first)
	mkdir(t, second)
	write(t, fsx.Join(first, "from-first"), "")
	write(t, fsx.Join(second, "from-second"), "")
	view := New(context.Background(), first, "", Memory{})
	defer view.Close()
	oldDone := view.done
	view.Navigate(second, "")
	ready(t, view)
	if got := names(view); !slices.Equal(got, []string{"..", "from-second"}) {
		t.Errorf("names %q", got)
	}
	select {
	case <-oldDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the replaced load did not stop")
	}
	view.Poll() // nothing stale arrives later
	if got := names(view); !slices.Equal(got, []string{"..", "from-second"}) {
		t.Errorf("names after a late poll %q", got)
	}
}

func TestCloseStopsTheListingAndIsIdempotent(t *testing.T) {
	root := root(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	view := New(ctx, root, "", Memory{})
	view.Close()
	view.Close()
	select {
	case <-view.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the listing goroutine did not exit")
	}
	view.Poll()
	if !view.Loading {
		t.Error("a cancelled listing never completes")
	}
}

func TestMemoryKeysPathsByComponents(t *testing.T) {
	root := root(t)
	child := fsx.Join(root, "child")
	mkdir(t, child)
	write(t, fsx.Join(child, "a"), "")
	write(t, fsx.Join(child, "b"), "")
	view := New(context.Background(), child, fsx.Join(child, "b"), Memory{})
	defer view.Close()
	ready(t, view)
	view.Remember()
	memory := view.Memory
	// The same directory spelled with a trailing slash finds the position.
	again := New(context.Background(), child+"/", "", memory)
	defer again.Close()
	ready(t, again)
	if got := selectedName(t, again); got != "b" {
		t.Errorf("selected %q, want b", got)
	}
}

func TestMoveByAndFitStayInRange(t *testing.T) {
	view := &Explorer{}
	view.entries = []Entry{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	view.matches = []int{0, 1, 2}
	view.MoveBy(-5)
	if view.Selected != 0 {
		t.Errorf("selected %d", view.Selected)
	}
	view.MoveBy(1 << 62)
	if view.Selected != 2 {
		t.Errorf("selected %d", view.Selected)
	}
	view.Fit(0) // at least one row
	if view.Top != 2 {
		t.Errorf("top %d", view.Top)
	}
	view.Fit(10)
	if view.Top != 0 {
		t.Errorf("top %d", view.Top)
	}
	if entry, ok := view.Entry(3); ok || entry != nil {
		t.Error("entry past the end")
	}
	view.matches = nil
	view.MoveBy(3)
	if view.Selected != 0 {
		t.Errorf("selected %d with no entries", view.Selected)
	}
}

func TestAcceptFilterKeepsTheSelectedEntry(t *testing.T) {
	root := root(t)
	for _, n := range []string{"apple", "banana", "cherry"} {
		write(t, fsx.Join(root, n), "")
	}
	view := New(context.Background(), root, "", Memory{})
	defer view.Close()
	ready(t, view)
	view.StartFilter()
	view.Query = "ch"
	view.Filter()
	if got := selectedName(t, view); got != "cherry" {
		t.Fatalf("selected %q", got)
	}
	view.AcceptFilter()
	if view.Filtering || view.Query != "" {
		t.Error("filtering should be over")
	}
	if got := selectedName(t, view); got != "cherry" {
		t.Errorf("selected %q after accepting", got)
	}
}
