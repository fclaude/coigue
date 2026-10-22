//go:build darwin

package fsx

import (
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace renames from to to, failing with EEXIST instead of
// replacing an existing destination (renamex_np with RENAME_EXCL).
func RenameNoReplace(from, to string) error {
	if err := unix.RenamexNp(from, to, unix.RENAME_EXCL); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
