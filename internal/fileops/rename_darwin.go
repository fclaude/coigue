//go:build darwin

package fileops

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameExclusive renames from to to unless to exists, atomically
// (renamex_np with RENAME_EXCL).
func renameExclusive(from, to string) error {
	if err := unix.RenamexNp(from, to, unix.RENAME_EXCL); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
