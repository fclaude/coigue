package file

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/buffer"
)

func TestRoundtripAndConflict(t *testing.T) {
	// Rust: file::tests::roundtrip_and_conflict
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	f, text, err := Open(path)
	if err != nil || text != "" {
		t.Fatalf("Open = %q, %v", text, err)
	}
	if err := f.Save("é\n\n"); err != nil {
		t.Fatal(err)
	}
	if f.Dirty("é\n\n") {
		t.Fatal("dirty after save")
	}
	if _, text, err := Open(path); err != nil || text != "é\n\n" {
		t.Fatalf("reopen = %q, %v", text, err)
	}
	os.WriteFile(path, []byte("external"), 0o644)
	if err := f.Save("lost"); err == nil {
		t.Fatal("save over an external change succeeded")
	}
	if got, _ := os.ReadFile(path); string(got) != "external" {
		t.Fatalf("file = %q", got)
	}
}

func TestFailedSavePreservesContent(t *testing.T) {
	// Rust: file::tests::failed_save_preserves_content
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	os.WriteFile(path, []byte("old"), 0o644)
	f, _, _ := Open(path)
	err := f.saveWith("new", func(file *os.File, data []byte) error {
		file.Write(data[:1])
		return errors.New("injected partial write failure")
	})
	if err == nil || !strings.Contains(err.Error(), "partial write") {
		t.Fatalf("error = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("file = %q", got)
	}
	if f.Dirty("old") || !f.Dirty("new") {
		t.Fatal("dirty tracking changed by a failed save")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
	if err := f.Save("new"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("file = %q", got)
	}
}

func TestExactRoundtripsAndInvalidUTF8(t *testing.T) {
	// Rust: file::tests::exact_roundtrips_and_invalid_utf8
	path := filepath.Join(t.TempDir(), "file")
	for _, data := range [][]byte{{}, []byte("\n"), []byte("a\n\n"), []byte("a\r\nb\r\n"), []byte("a")} {
		os.WriteFile(path, data, 0o644)
		f, text, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Save(buffer.New(text).Text()); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
			t.Fatalf("roundtrip of %q gave %q", data, got)
		}
	}
	os.WriteFile(path, []byte{255}, 0o644)
	if _, _, err := Open(path); err == nil || err.Error() != "file is not UTF-8" {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, []byte{255}) {
		t.Fatalf("file changed: %v", got)
	}
}

func TestExternalCreationAndDeletionAreConflicts(t *testing.T) {
	// Rust: file::tests::external_creation_and_deletion_are_conflicts
	path := filepath.Join(t.TempDir(), "file")
	f, _, _ := Open(path)
	os.WriteFile(path, []byte("external"), 0o644)
	if err := f.Save("ours"); err == nil {
		t.Fatal("save after external creation succeeded")
	}
	if got, _ := os.ReadFile(path); string(got) != "external" {
		t.Fatalf("file = %q", got)
	}
	f, _, _ = Open(path)
	os.Remove(path)
	if err := f.Save("ours"); err == nil {
		t.Fatal("save after external deletion succeeded")
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("deleted file was recreated")
	}
}

func TestPermissionsAndSymlinks(t *testing.T) {
	// Rust: file::tests::permissions_and_symlinks
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	os.WriteFile(path, []byte("old"), 0o644)
	os.Chmod(path, 0o640)
	os.Symlink(path, link)
	f, _, err := Open(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Save("new"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced the symlink")
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	os.Chmod(path, 0o440)
	if err := f.Save("forbidden"); err == nil {
		t.Fatal("save of a read-only file succeeded")
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
	os.Remove(path)
	other := filepath.Join(dir, "other")
	os.WriteFile(other, []byte("new"), 0o644)
	os.Symlink(other, path)
	if err := f.Save("forbidden"); err == nil {
		t.Fatal("save through a swapped-in symlink succeeded")
	}
	if got, _ := os.ReadFile(other); string(got) != "new" {
		t.Fatalf("other = %q", got)
	}
	dangling := filepath.Join(dir, "dangling")
	os.Symlink(filepath.Join(dir, "absent"), dangling)
	f, _, _ = Open(dangling)
	if err := f.Save("forbidden"); err == nil {
		t.Fatal("save onto a dangling symlink succeeded")
	}
	if info, err := os.Lstat(dangling); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dangling symlink was replaced")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 4 { // target, link, other, dangling
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

func TestUnattachedRefusesToSaveAndEmptyDirtyMeansMissing(t *testing.T) {
	f := Unattached()
	if err := f.Save("x"); err == nil || err.Error() != "No file open; use :explore or :open" {
		t.Fatalf("error = %v", err)
	}
	if f.Dirty("") || !f.Dirty("x") {
		t.Fatal("an unattached file is clean exactly when empty")
	}
}

func TestSavedFileKeepsSpecialModeBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("a"), 0o644)
	os.Chmod(path, 0o755|os.ModeSetgid)
	f, _, _ := Open(path)
	if err := f.Save("b"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode()&os.ModeSetgid == 0 || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode after save = %v", info.Mode())
	}
}
