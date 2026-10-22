package search

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/fileops"
	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/navigation"
)

// collect runs a search to the end, polling with a three-second deadline.
func collect(t *testing.T, root, query string, active *Active) *navigation.List {
	t.Helper()
	s := Start(context.Background(), root, query, active)
	defer s.Close()
	list := navigation.NewList("Search", nil)
	list.Busy = true
	deadline := time.Now().Add(3 * time.Second)
	for list.Busy {
		s.Poll(list)
		if !time.Now().Before(deadline) {
			t.Fatal("the search did not finish in 3 s")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return list
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Rust: search::tests::search_respects_discovery_limits_and_unsaved_unicode_positions
func TestSearchRespectsDiscoveryLimitsAndUnsavedUnicodePositions(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"))
	for _, name := range []string{".git", fileops.Trash} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, name, "private"), []byte("needle"))
	}
	write(t, filepath.Join(root, "ignored.txt"), []byte("needle"))
	write(t, filepath.Join(root, "binary"), []byte("needle\x00"))
	write(t, filepath.Join(root, "invalid"), []byte{255, 254})
	write(t, filepath.Join(root, "large"), bytes.Repeat([]byte{'x'}, fileLimit+1))
	active := filepath.Join(root, "active.rs")
	write(t, active, []byte("disk text"))
	write(t, filepath.Join(root, "other.rs"), []byte("NEEDLE\nneedle needle\n"))
	list := collect(t, root, "needle", &Active{Path: active, Text: "a😀 needle\r\nunsaved"})
	if len(list.Targets) != 2 {
		t.Fatalf("%d targets, want 2: %+v", len(list.Targets), list.Targets)
	}
	var hit *navigation.Target
	for i := range list.Targets {
		if fsx.EndsWith(list.Targets[i].Location.Path, "active.rs") {
			hit = &list.Targets[i]
		}
	}
	if hit == nil {
		t.Fatal("no hit in the active file")
	}
	if hit.Location.Row != 0 || hit.Location.Col != 3 {
		t.Errorf("hit at row %d col %d, want 0 3", hit.Location.Row, hit.Location.Col)
	}
	if !strings.Contains(list.Notice, "3 skipped") {
		t.Errorf("notice %q", list.Notice)
	}
	for _, target := range list.Targets {
		if fsx.EndsWith(target.Location.Path, "ignored.txt") {
			t.Error("an ignored file was searched")
		}
	}
	if got := collect(t, root, "", nil); len(got.Targets) != 0 {
		t.Errorf("an empty query found %d targets", len(got.Targets))
	}
}

// Rust: search::tests::search_caps_results_and_drops_a_blocked_producer
func TestSearchCapsResultsAndDropsABlockedProducer(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "many"), []byte(strings.Repeat("hit\n", 1500)))
	list := collect(t, dir, "hit", nil)
	if len(list.Targets) != resultLimit {
		t.Errorf("%d targets, want %d", len(list.Targets), resultLimit)
	}
	if !strings.Contains(list.Notice, "first 1000") {
		t.Errorf("notice %q", list.Notice)
	}
	s := Start(context.Background(), dir, "hit", nil)
	// More than eight batches fills the bounded channel. Closing the consumer
	// must release the producer rather than retaining the scan.
	deadline := time.Now().Add(3 * time.Second)
	for len(s.receiver) < channelBatch {
		if !time.Now().Before(deadline) {
			t.Fatalf("the channel holds %d batches, expected it to fill", len(s.receiver))
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the producer reach the blocked send
	s.Close()
	s.Close() // idempotent
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the producer is still blocked after Close")
	}
}

func TestResultsComeInPathOrderWithRuneColumnsAndTrimmedLabels(t *testing.T) {
	root, err := fsx.Canonicalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("é", 200)
	write(t, filepath.Join(root, "b.txt"), []byte("x\n \t  é needle "+long+"\n"))
	write(t, filepath.Join(root, "a.txt"), []byte("needle\n"))
	write(t, filepath.Join(root, "sub", "c.txt"), []byte("zz needle\nneedle"))
	list := collect(t, root, "needle", nil)
	var labels []string
	for _, target := range list.Targets {
		labels = append(labels, target.Label)
	}
	want := []string{
		"a.txt:1:1  needle",
		"b.txt:2:7  é needle " + strings.Repeat("é", 180-len([]rune("é needle "))),
		"sub/c.txt:1:4  zz needle",
		"sub/c.txt:2:1  needle",
	}
	if len(labels) != len(want) {
		t.Fatalf("labels %q", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("label %d = %q, want %q", i, labels[i], want[i])
		}
	}
	if list.Notice != "4 matching lines; 0 skipped" {
		t.Errorf("notice %q", list.Notice)
	}
	if got := list.Targets[1].Location; got.Row != 1 || got.Col != 6 || got.Path != filepath.Join(root, "b.txt") {
		t.Errorf("location %+v", got)
	}
}

func TestUnreadableFilesAreSkippedAndTheActiveCopyIsSizeLimited(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	write(t, locked, []byte("needle"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	big := filepath.Join(root, "big.rs")
	write(t, big, []byte("needle"))
	list := collect(t, root, "needle", &Active{Path: big, Text: strings.Repeat("x", fileLimit+1)})
	wantSkipped := 2
	if os.Geteuid() == 0 {
		wantSkipped = 1 // root can read the locked file
	}
	if want := fmt.Sprintf("%d matching lines; %d skipped", len(list.Targets), wantSkipped); list.Notice != want {
		t.Errorf("notice %q, want %q", list.Notice, want)
	}
}

func TestMissingRootCountsAsSkipped(t *testing.T) {
	list := collect(t, filepath.Join(t.TempDir(), "missing"), "x", nil)
	if list.Notice != "0 matching lines; 1 skipped" {
		t.Errorf("notice %q", list.Notice)
	}
}

func TestCancelledContextReleasesTheSearch(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "many"), []byte(strings.Repeat("hit\n", 1500)))
	ctx, cancel := context.WithCancel(context.Background())
	s := Start(ctx, dir, "hit", nil)
	cancel()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the context did not stop the search")
	}
	list := navigation.NewList("Search", nil)
	list.Busy = true
	for i := 0; i < 10 && list.Busy; i++ {
		s.Poll(list)
	}
	if list.Busy {
		t.Error("a stopped search must stop being busy once its results are drained")
	}
}
