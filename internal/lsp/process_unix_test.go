//go:build unix

package lsp

import "syscall"

// processAlive reports whether pid is still running. Signal 0 does not
// deliver anything; it only checks that the process exists.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
