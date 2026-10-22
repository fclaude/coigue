//go:build linux

package fsx

import (
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace renames from to to, failing with EEXIST instead of
// replacing an existing destination. A check-then-rename sequence would race
// with a concurrent creator; renameat2(RENAME_NOREPLACE) is atomic.
func RenameNoReplace(from, to string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
