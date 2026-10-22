package fsx

import (
	"errors"
	"fmt"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// ErrUnsupported reports an operation that needs Linux or macOS.
var ErrUnsupported = errors.New("Non-overwriting moves require Linux or macOS")

// Describe renders an error the way Rust's io::Error Display does, which is
// what the editor shows in its message line: operating-system failures read
// "No such file or directory (os error 2)" with no operation or path prefix
// (Go's *PathError would add both), and every other error is its own text.
func Describe(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		text := errno.Error()
		first, size := utf8.DecodeRuneInString(text)
		return fmt.Sprintf("%c%s (os error %d)", unicode.ToUpper(first), text[size:], int(errno))
	}
	return err.Error()
}
