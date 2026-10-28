//go:build windows

package lsp

import (
	"errors"
	"os"
)

// executable reports whether path can be opened. Windows directories have no
// Unix execute bit; opening the directory is the permission check a failed
// chdir would hit. The error handed back is the operating-system error, so
// the caller can wrap it the same way as on Unix.
func executable(path string) error {
	file, err := os.Open(path)
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return pathErr.Err
		}
		return err
	}
	return file.Close()
}
