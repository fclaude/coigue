//go:build windows

package fsx

import (
	"os"

	"golang.org/x/sys/windows"
)

// RenameNoReplace moves a file or directory without replacing an existing name.
func RenameNoReplace(from, to string) error {
	oldp, err := windows.UTF16PtrFromString(from)
	if err == nil {
		var newp *uint16
		newp, err = windows.UTF16PtrFromString(to)
		if err == nil {
			err = windows.MoveFile(oldp, newp)
		}
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
