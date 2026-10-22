// Package file loads and saves the document behind a buffer. Saving is
// designed so that no failure can lose the previous contents: the new text is
// written and synced to a temporary file beside the target and then renamed
// over it, but only if the file on disk is still exactly what was loaded.
package file

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/fsx"
)

// File is the on-disk identity of a buffer: its path, the bytes last read or
// written (the baseline for dirty and external-change detection), and a
// counter bumped by every successful save.
type File struct {
	// Path is empty for an unattached session (the welcome screen).
	Path        string
	original    []byte
	hasOriginal bool // false when the file did not exist at load time
	revision    uint64
}

// read returns the file's bytes, or exists=false when it is absent.
func read(path string) (data []byte, exists bool, err error) {
	data, err = os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	}
	return nil, false, err
}

// Unattached returns a file with no filesystem target, even if saved
// accidentally.
func Unattached() *File { return &File{} }

// Open reads path, resolving existing symlinks so an atomic save updates
// their target. A missing file opens as empty text. The returned text is the
// file's contents; invalid UTF-8 is refused without touching the file.
func Open(path string) (*File, string, error) {
	if fsx.Exists(path) {
		resolved, err := fsx.Canonicalize(path)
		if err != nil {
			return nil, "", err
		}
		path = resolved
	}
	original, exists, err := read(path)
	if err != nil {
		return nil, "", err
	}
	if !utf8.Valid(original) {
		return nil, "", errors.New("file is not UTF-8")
	}
	return &File{Path: path, original: original, hasOriginal: exists}, string(original), nil
}

// Revision counts successful saves.
func (f *File) Revision() uint64 { return f.revision }

// Dirty reports whether text differs from what is on disk (a missing file
// counts as empty).
func (f *File) Dirty(text string) bool {
	return string(f.original) != text
}

func (f *File) matchesDisk(data []byte, exists bool) bool {
	if exists != f.hasOriginal {
		return false
	}
	return bytes.Equal(data, f.original)
}

var errChangedExternally = errors.New("file changed externally; save refused")

// Save atomically replaces the file with text.
func (f *File) Save(text string) error {
	if f.Path == "" {
		return errors.New("No file open; use :explore or :open")
	}
	return f.saveWith(text, func(file *os.File, data []byte) error {
		_, err := file.Write(data)
		return err
	})
}

func (f *File) saveWith(text string, write func(*os.File, []byte) error) (err error) {
	data, exists, err := read(f.Path)
	if err != nil {
		return err
	}
	if !f.matchesDisk(data, exists) {
		return errChangedExternally
	}
	parent, ok := fsx.Parent(f.Path)
	if !ok || parent == "" {
		parent = "."
	}
	temp, err := os.CreateTemp(parent, ".tmp")
	if err != nil {
		return err
	}
	persisted := false
	defer func() {
		temp.Close()
		if !persisted {
			os.Remove(temp.Name())
		}
	}()
	var mode fs.FileMode
	if f.hasOriginal {
		info, err := os.Lstat(f.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o222 == 0 {
			return errors.New("destination is not a writable regular file")
		}
		mode = info.Mode()
	}
	if err := write(temp, []byte(text)); err != nil {
		return err
	}
	// Set permissions after writing, since writes can clear special mode bits.
	if f.hasOriginal {
		if err := temp.Chmod(mode); err != nil {
			return err
		}
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	data, exists, err = read(f.Path)
	if err != nil {
		return err
	}
	if !f.matchesDisk(data, exists) {
		return errChangedExternally
	}
	if f.hasOriginal {
		err = os.Rename(temp.Name(), f.Path)
	} else {
		err = persistNoClobber(temp.Name(), f.Path)
	}
	if err != nil {
		return err
	}
	persisted = true
	f.original = []byte(text)
	f.hasOriginal = true
	f.revision++
	return nil
}

// persistNoClobber moves a new file into place without ever replacing a name
// that appeared in the meantime. Where the kernel or filesystem lacks
// RENAME_NOREPLACE it falls back to link-then-unlink, as the Rust tempfile
// crate does.
func persistNoClobber(from, to string) error {
	err := fsx.RenameNoReplace(from, to)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, fsx.ErrUnsupported) {
		return err
	}
	if err := os.Link(from, to); err != nil {
		return err
	}
	os.Remove(from)
	return nil
}
