package fileops

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/fclaude/coigue/internal/fsx"
)

// root is Rust's `let (_dir, root) = root();`: a fresh directory, canonicalized.
func root(t *testing.T) string {
	t.Helper()
	dir, err := fsx.Canonicalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func destination(t *testing.T, directory, input string) string {
	t.Helper()
	path, err := Destination(directory, input)
	ok(t, err)
	return path
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// Rust: fileops::tests::create_move_collisions_and_reserved_paths
func TestCreateMoveCollisionsAndReservedPaths(t *testing.T) {
	root := root(t)
	file := destination(t, root, "café.txt")
	ok(t, Create(file, false))
	write(t, file, []byte("keep"))
	if Create(file, false) == nil {
		t.Error("creating an existing file must fail")
	}
	if Create(file, true) == nil {
		t.Error("creating a directory over a file must fail")
	}
	directory := destination(t, root, "nested")
	ok(t, Create(directory, true))
	if Create(directory, true) == nil {
		t.Error("creating an existing directory must fail")
	}
	moved := destination(t, root, "nested/moved")
	ok(t, MoveEntry(file, moved))
	if got := read(t, moved); got != "keep" {
		t.Errorf("moved file holds %q", got)
	}
	write(t, file, []byte("existing"))
	if MoveEntry(moved, file) == nil {
		t.Error("moving onto an existing file must fail")
	}
	if got := read(t, file); got != "existing" {
		t.Errorf("the existing file was overwritten: %q", got)
	}
	if got := read(t, moved); got != "keep" {
		t.Errorf("the source was disturbed: %q", got)
	}
	if MoveEntry(directory, fsx.Join(directory, "child")) == nil {
		t.Error("moving a directory into itself must fail")
	}
	for _, name := range []string{"", ".git", ".coigue-trash", "nested/.git", "missing/child", "bad\nname"} {
		if _, err := Destination(root, name); err == nil {
			t.Errorf("destination(%q) should fail", name)
		}
	}
}

// Rust: fileops::tests::concurrent_moves_never_replace_each_other
func TestConcurrentMovesNeverReplaceEachOther(t *testing.T) {
	root := root(t)
	target := fsx.Join(root, "target")
	var barrier sync.WaitGroup
	barrier.Add(2)
	type result struct {
		source  string
		success bool
	}
	results := make([]result, 2)
	var workers sync.WaitGroup
	for i, name := range []string{"first", "second"} {
		source := fsx.Join(root, name)
		write(t, source, []byte(name))
		workers.Add(1)
		go func() {
			defer workers.Done()
			barrier.Done()
			barrier.Wait()
			results[i] = result{source, MoveEntry(source, target) == nil}
		}()
	}
	workers.Wait()
	successes := 0
	for _, r := range results {
		if r.success {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d moves succeeded, want exactly 1", successes)
	}
	for _, r := range results {
		if r.success {
			if got := read(t, target); got != filepath.Base(r.source) {
				t.Errorf("target holds %q, want %q", got, filepath.Base(r.source))
			}
		} else if !isFile(r.source) {
			t.Errorf("the losing source %s disappeared", r.source)
		}
	}
}

// Rust: fileops::tests::recovery_persists_and_restores_directories_in_reverse_order
func TestRecoveryPersistsAndRestoresDirectoriesInReverseOrder(t *testing.T) {
	root := root(t)
	first := fsx.Join(root, "first")
	second := fsx.Join(root, "second")
	write(t, first, []byte{0, 255, 17})
	ok(t, os.Mkdir(second, 0o755))
	write(t, fsx.Join(second, "child"), []byte("nested data"))
	saved, err := TrashEntry(first)
	ok(t, err)
	if got := read(t, saved); got != "\x00\xff\x11" {
		t.Errorf("saved item holds %q", got)
	}
	_, err = TrashEntry(second)
	ok(t, err)
	if exists(first) || exists(second) {
		t.Error("trashed entries must be gone")
	}
	// Recovery discovers on-disk records; there is no in-memory history.
	restored, err := RestoreLatest(root)
	ok(t, err)
	if restored != second {
		t.Errorf("first restore gave %q, want %q", restored, second)
	}
	if got := read(t, fsx.Join(second, "child")); got != "nested data" {
		t.Errorf("restored directory holds %q", got)
	}
	restored, err = RestoreLatest(root)
	ok(t, err)
	if restored != first {
		t.Errorf("second restore gave %q, want %q", restored, first)
	}
	if got := read(t, first); got != "\x00\xff\x11" {
		t.Errorf("restored file holds %q", got)
	}
	if _, err := RestoreLatest(root); err == nil {
		t.Error("nothing is left to restore")
	}
}

// Rust: fileops::tests::recovery_survives_renaming_its_parent_and_rejects_forged_paths
func TestRecoverySurvivesRenamingItsParentAndRejectsForgedPaths(t *testing.T) {
	root := root(t)
	parent := fsx.Join(root, "parent")
	ok(t, os.Mkdir(parent, 0o755))
	victim := fsx.Join(parent, "victim")
	write(t, victim, []byte("recover"))
	_, err := TrashEntry(victim)
	ok(t, err)
	moved := fsx.Join(root, "moved")
	ok(t, MoveEntry(parent, moved))
	restored, err := RestoreLatest(moved)
	ok(t, err)
	if want := fsx.Join(moved, "victim"); restored != want {
		t.Errorf("restored %q, want %q", restored, want)
	}
	item, err := TrashEntry(fsx.Join(moved, "victim"))
	ok(t, err)
	write(t, fsx.Join(filepath.Dir(item), "original"), []byte("../escape"))
	if _, err := RestoreLatest(moved); err == nil {
		t.Error("a forged record must be rejected")
	}
	if !isFile(item) {
		t.Error("the item must stay in recovery storage")
	}
	if exists(fsx.Join(root, "escape")) {
		t.Error("nothing may be restored outside the directory")
	}
}

// Rust: fileops::tests::restore_conflict_keeps_recoverable_data_and_symlinks_are_not_followed
func TestRestoreConflictKeepsRecoverableDataAndSymlinksAreNotFollowed(t *testing.T) {
	root := root(t)
	path := fsx.Join(root, "file")
	write(t, path, []byte("deleted"))
	saved, err := TrashEntry(path)
	ok(t, err)
	write(t, path, []byte("new occupant"))
	if _, err := RestoreLatest(root); err == nil {
		t.Error("restoring over an occupied name must fail")
	}
	if got := read(t, path); got != "new occupant" {
		t.Errorf("the occupant was replaced: %q", got)
	}
	if got := read(t, saved); got != "deleted" {
		t.Errorf("the saved data changed: %q", got)
	}
	ok(t, os.Remove(path))
	restored, err := RestoreLatest(root)
	ok(t, err)
	if restored != path {
		t.Errorf("restored %q, want %q", restored, path)
	}
	link := fsx.Join(root, "link")
	ok(t, os.Symlink(path, link))
	if _, err := TrashEntry(link); err == nil {
		t.Error("trashing a symlink must fail")
	}
	if MoveEntry(link, fsx.Join(root, "moved")) == nil {
		t.Error("moving a symlink must fail")
	}
	ok(t, os.Remove(fsx.Join(root, Trash)))
	ok(t, os.Symlink(root, fsx.Join(root, Trash)))
	if _, err := TrashEntry(path); err == nil {
		t.Error("a symlinked recovery directory must be refused")
	}
	if !isFile(path) {
		t.Error("the file must stay where it was")
	}
}

// Rust: fileops::tests::manifest_round_trip_preserves_native_path_bytes
func TestManifestRoundTripPreservesNativePathBytes(t *testing.T) {
	path := "/f\xff"
	data, err := encode(path)
	ok(t, err)
	got, err := decode(data)
	ok(t, err)
	if got != path {
		t.Errorf("round trip gave %q, want %q", got, path)
	}
}

// Rust: fileops::tests::non_utf8_names_round_trip_through_persistent_recovery
func TestNonUTF8NamesRoundTripThroughPersistentRecovery(t *testing.T) {
	root := root(t)
	path := fsx.Join(root, "f\xff")
	write(t, path, []byte("data"))
	_, err := TrashEntry(path)
	ok(t, err)
	restored, err := RestoreLatest(root)
	ok(t, err)
	if restored != path {
		t.Errorf("restored %q, want %q", restored, path)
	}
	if got := read(t, path); got != "data" {
		t.Errorf("restored file holds %q", got)
	}
}

// The record layout is shared with the Rust editor: a user may delete with
// one and restore with the other, and the Python suite restores in a second
// run, so the names and contents must match byte for byte.
func TestRecordLayoutMatchesTheRustEditor(t *testing.T) {
	root := root(t)
	path := fsx.Join(root, "victim.txt")
	write(t, path, []byte("x"))
	item, err := TrashEntry(path)
	ok(t, err)
	record := filepath.Dir(item)
	if filepath.Base(item) != "item" {
		t.Errorf("item is %q", item)
	}
	if !regexp.MustCompile(`^entry-\d{39}-[A-Za-z0-9]{6}$`).MatchString(filepath.Base(record)) {
		t.Errorf("record directory %q does not look like entry-<39 digits>-<6 alphanumerics>", filepath.Base(record))
	}
	if got := read(t, fsx.Join(record, "original")); got != "victim.txt" {
		t.Errorf("manifest holds %q, want the bare name", got)
	}
	names := entryNames(t, record)
	if !slices.Equal(names, []string{"item", "original"}) {
		t.Errorf("record holds %v", names)
	}
	info, err := os.Lstat(fsx.Join(root, Trash))
	ok(t, err)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("recovery directory mode is %v, want 0700", info.Mode().Perm())
	}
	// A record written by the Rust editor restores here.
	foreign := fsx.Join(fsx.Join(root, Trash), "entry-"+string(bytes.Repeat([]byte("9"), 39))+"-AbCdEf")
	ok(t, os.Mkdir(foreign, 0o755))
	write(t, fsx.Join(foreign, "original"), []byte("from-rust"))
	write(t, fsx.Join(foreign, "item"), []byte("rust data"))
	restored, err := RestoreLatest(root)
	ok(t, err)
	if restored != fsx.Join(root, "from-rust") || read(t, restored) != "rust data" {
		t.Errorf("restored %q", restored)
	}
	if exists(foreign) {
		t.Error("a restored record is removed")
	}
	// Records sort by directory name, newest last.
	restored, err = RestoreLatest(root)
	ok(t, err)
	if restored != path {
		t.Errorf("restored %q, want %q", restored, path)
	}
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	ok(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestReservedPathsAreRefused(t *testing.T) {
	root := root(t)
	for _, name := range []string{".git", ".coigue-trash"} {
		path := fsx.Join(root, name)
		ok(t, os.Mkdir(path, 0o755))
		if _, err := TrashEntry(path); err == nil {
			t.Errorf("trashing %s must fail", name)
		}
		if MoveEntry(path, fsx.Join(root, "other")) == nil {
			t.Errorf("moving %s must fail", name)
		}
		if MoveEntry(fsx.Join(root, "none"), path) == nil {
			t.Errorf("moving onto %s must fail", name)
		}
		if Create(fsx.Join(path, "x"), false) == nil {
			t.Errorf("creating inside %s must fail", name)
		}
	}
	err := Create(fsx.Join(root, ".git"), true)
	if err == nil || err.Error() != "Git metadata and recovery storage cannot be modified" {
		t.Errorf("unexpected error %v", err)
	}
	// A restore record naming a reserved entry is refused before any move.
	file := fsx.Join(root, "ok")
	write(t, file, []byte("x"))
	item, err := TrashEntry(file)
	ok(t, err)
	write(t, fsx.Join(filepath.Dir(item), "original"), []byte(".git"))
	_, err = RestoreLatest(root)
	if err == nil || err.Error() != "Git metadata and recovery storage cannot be modified" {
		t.Errorf("restoring to a reserved name: %v", err)
	}
}

func TestMessagesAndEdges(t *testing.T) {
	root := root(t)
	if _, err := RestoreLatest(root); err == nil || err.Error() != "No deleted entries to restore in this directory" {
		t.Errorf("restore without a recovery directory: %v", err)
	}
	ok(t, os.Mkdir(fsx.Join(root, Trash), 0o700))
	if _, err := RestoreLatest(root); err == nil || err.Error() != "No deleted entries to restore in this directory" {
		t.Errorf("restore from an empty recovery directory: %v", err)
	}
	// Moving an entry onto itself is accepted and does nothing.
	file := fsx.Join(root, "same")
	write(t, file, []byte("x"))
	ok(t, MoveEntry(file, file))
	ok(t, MoveEntry(file, fsx.Join(root, ".", "same")))
	if MoveEntry(fsx.Join(root, "missing"), fsx.Join(root, "x")) == nil {
		t.Error("moving a missing entry must fail")
	}
	// A recovery record without an item is skipped; an invalid manifest is not.
	broken := fsx.Join(fsx.Join(root, Trash), "entry-zzz")
	ok(t, os.Mkdir(broken, 0o755))
	if _, err := RestoreLatest(root); err == nil || err.Error() != "No deleted entries to restore in this directory" {
		t.Errorf("a record without an item is skipped: %v", err)
	}
	write(t, fsx.Join(broken, "item"), []byte("x"))
	if _, err := RestoreLatest(root); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a record with an item but no manifest reports the missing manifest: %v", err)
	}
	ok(t, os.Mkdir(fsx.Join(broken, "original"), 0o755))
	if _, err := RestoreLatest(root); err == nil || err.Error() != "Invalid recovery record" {
		t.Errorf("a manifest that is not a file: %v", err)
	}
	ok(t, os.Remove(fsx.Join(broken, "original")))
	for _, name := range []string{"", ".", "..", "a/b", "/abs", "dir/", "./x"} {
		write(t, fsx.Join(broken, "original"), []byte(name))
		if _, err := RestoreLatest(root); err == nil || err.Error() != "Invalid recovery entry name" {
			t.Errorf("manifest name %q: %v", name, err)
		}
		ok(t, os.Remove(fsx.Join(broken, "original")))
	}
}
