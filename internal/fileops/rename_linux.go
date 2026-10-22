//go:build linux

package fileops

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameExclusive renames from to to unless to exists. A check followed by a
// plain rename could overwrite a destination created in between; the kernel's
// no-replace flag makes the test and the move one atomic step.
func renameExclusive(from, to string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
