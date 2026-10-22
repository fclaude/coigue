//go:build !linux && !darwin

package fsx

// RenameNoReplace is only implemented for Linux and macOS, like the Rust.
func RenameNoReplace(from, to string) error {
	return ErrUnsupported
}
