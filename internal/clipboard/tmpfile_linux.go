//go:build linux

package clipboard

import (
	"os"

	"golang.org/x/sys/unix"
)

// openUnnamed opens a file in the temporary directory that never has a name
// (O_TMPFILE), so that a crash cannot leave it behind. Like the tempfile crate,
// it reports errNoUnnamed where the kernel or file system lacks O_TMPFILE.
func openUnnamed() (*os.File, error) {
	fd, err := unix.Open(os.TempDir(), unix.O_RDWR|unix.O_TMPFILE|unix.O_CLOEXEC, 0o600)
	switch err {
	case nil:
		return os.NewFile(uintptr(fd), "clipboard"), nil
	case unix.EOPNOTSUPP, unix.EISDIR, unix.ENOENT:
		return nil, errNoUnnamed
	}
	return nil, &os.PathError{Op: "open", Path: os.TempDir(), Err: err}
}
