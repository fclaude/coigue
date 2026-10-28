//go:build unix

package lsp

import "syscall"

// executable is the execute-bit check a child's chdir performs (X_OK).
func executable(path string) error {
	return syscall.Access(path, 1)
}
