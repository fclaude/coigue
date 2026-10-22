// Package fileops implements the explorer's non-overwriting filesystem
// operations and the persistent, same-filesystem recovery storage: deleting
// moves an entry into <parent>/.coigue-trash/entry-<timestamp>-<random>/item
// together with a record of its name, and restoring moves the newest one back.
package fileops

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"sort"
	"time"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/ucd"
)

// Trash is the name of the recovery directory kept beside deleted entries.
const Trash = ".coigue-trash"

func permitted(path string) error {
	if fsx.HasComponent(path, ".git") || fsx.HasComponent(path, Trash) {
		return errors.New("Git metadata and recovery storage cannot be modified")
	}
	return nil
}

// Destination resolves the path typed in a prompt: relative to directory,
// with the parent made canonical but a final symlink never followed.
func Destination(directory, input string) (string, error) {
	if input == "" || hasControl(input) {
		return "", errors.New("Enter a nonempty path without control characters")
	}
	path := fsx.Join(directory, input)
	if err := permitted(path); err != nil {
		return "", err
	}
	// Resolve the parent, but never follow an existing destination symlink.
	name, ok := fsx.FileName(path)
	if !ok {
		return "", errors.New("A file or directory name is required")
	}
	parent, ok := fsx.Parent(path)
	if !ok {
		return "", errors.New("A parent directory is required")
	}
	parent, err := fsx.Canonicalize(parent)
	if err != nil {
		return "", err
	}
	resolved := fsx.Join(parent, name)
	if err := permitted(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if ucd.IsControl(r) {
			return true
		}
	}
	return false
}

func source(path string) error {
	if err := permitted(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return errors.New("Only regular files and directories can be changed")
	}
	return nil
}

// Create makes an empty file or a directory, failing when the path exists.
func Create(path string, directory bool) error {
	if err := permitted(path); err != nil {
		return err
	}
	if directory {
		return os.Mkdir(path, 0o777)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	err = file.Sync()
	_ = file.Close() // the Rust drops the handle after sync_all, ignoring close errors
	return err
}

// MoveEntry renames a file or directory without ever replacing the target.
func MoveEntry(from, to string) error {
	if err := source(from); err != nil {
		return err
	}
	if err := permitted(to); err != nil {
		return err
	}
	if samePath(from, to) {
		return nil
	}
	if fsx.StartsWith(to, from) {
		return errors.New("Cannot move a directory into itself")
	}
	return renameExclusive(from, to)
}

// samePath is Path equality: the same components, however spelled.
func samePath(a, b string) bool {
	ac, bc := fsx.Components(a), fsx.Components(b)
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

func trashRoot(directory string, create bool) (string, error) {
	root := fsx.Join(directory, Trash)
	if create {
		err := os.Mkdir(root, 0o700)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("Recovery storage must be a real directory")
	}
	return root, nil
}

// encode and decode turn an entry name into the bytes of its recovery record:
// on Unix a name is those bytes, whatever their encoding.
func encode(path string) ([]byte, error) { return []byte(path), nil }

func decode(data []byte) (string, error) { return string(data), nil }

const randomChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// makeRecord creates the record directory as tempfile::Builder does: the
// prefix, six random alphanumeric characters, and the default permissions;
// a name collision just draws another suffix.
func makeRecord(root, prefix string) (string, error) {
	if !fsx.IsAbsolute(root) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = fsx.Join(cwd, root)
	}
	for range 65536 {
		var suffix [6]byte
		for i := range suffix {
			suffix[i] = randomChars[rand.IntN(len(randomChars))]
		}
		path := fsx.Join(root, prefix+string(suffix[:]))
		err := os.Mkdir(path, 0o777)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", errors.New("too many temporary files exist")
}

// TrashEntry moves path into recovery storage beside it and returns where the
// data now lives. The record is persisted before the move so that a failure
// can never lose the item.
func TrashEntry(path string) (string, error) {
	if err := source(path); err != nil {
		return "", err
	}
	parent, ok := fsx.Parent(path)
	if !ok {
		return "", errors.New("Cannot delete the filesystem root")
	}
	root, err := trashRoot(parent, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if now.Before(time.Unix(0, 0)) {
		return "", errors.New("second time provided was later than self")
	}
	record, err := makeRecord(root, fmt.Sprintf("entry-%039d-", now.UnixNano()))
	if err != nil {
		return "", err
	}
	// Until the record is complete, any failure removes it again (a Rust
	// TempDir does that when it is dropped).
	if err := writeManifest(record, path); err != nil {
		_ = os.RemoveAll(record)
		return "", err
	}
	item := fsx.Join(record, "item")
	if err := renameExclusive(path, item); err != nil {
		_ = os.Remove(fsx.Join(record, "original"))
		_ = os.Remove(record)
		return "", err
	}
	return item, nil
}

func writeManifest(record, path string) error {
	manifest, err := os.OpenFile(fsx.Join(record, "original"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	defer manifest.Close()
	name, ok := fsx.FileName(path)
	if !ok {
		return errors.New("No entry name")
	}
	data, err := encode(name)
	if err != nil {
		return err
	}
	if _, err := manifest.Write(data); err != nil {
		return err
	}
	return manifest.Sync()
}

// RestoreLatest moves the newest recoverable entry of directory back to its
// original name, never replacing an existing entry, and returns that path.
func RestoreLatest(directory string) (string, error) {
	root, err := trashRoot(directory, false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", errors.New("No deleted entries to restore in this directory")
		}
		return "", err
	}
	records, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Name() > records[j].Name() })
	for _, entry := range records {
		if !entry.IsDir() {
			continue
		}
		record := fsx.Join(root, entry.Name())
		item := fsx.Join(record, "item")
		info, err := os.Lstat(item)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			continue
		}
		manifest := fsx.Join(record, "original")
		info, err = os.Lstat(manifest)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("Invalid recovery record")
		}
		data, err := os.ReadFile(manifest)
		if err != nil {
			return "", err
		}
		name, err := decode(data)
		if err != nil {
			return "", err
		}
		if fsx.IsAbsolute(name) || len(fsx.Components(name)) != 1 {
			return "", errors.New("Invalid recovery entry name")
		}
		if base, ok := fsx.FileName(name); !ok || base != name {
			return "", errors.New("Invalid recovery entry name")
		}
		original := fsx.Join(directory, name)
		if err := permitted(original); err != nil {
			return "", err
		}
		if err := renameExclusive(item, original); err != nil {
			return "", err
		}
		_ = os.Remove(fsx.Join(record, "original"))
		_ = os.Remove(record)
		return original, nil
	}
	return "", errors.New("No deleted entries to restore in this directory")
}
