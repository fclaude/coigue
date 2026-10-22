//go:build !linux && !darwin

package fileops

import "errors"

// errUnsupported is what the rename primitive reports on systems without a
// no-replace rename.
var errUnsupported = errors.New("Non-overwriting moves require Linux or macOS")

// renameExclusive has no atomic no-replace primitive to use here, and a
// check-then-rename would be racy, so it refuses (the Rust does the same).
func renameExclusive(from, to string) error {
	_, _ = from, to
	return errUnsupported
}
