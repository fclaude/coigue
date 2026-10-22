//go:build unix

package walk_test

import "syscall"

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o644) }
