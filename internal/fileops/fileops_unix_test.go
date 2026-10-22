//go:build unix

package fileops

import (
	"syscall"
	"testing"

	"github.com/fclaude/coigue/internal/fsx"
)

func TestOnlyRegularFilesAndDirectoriesCanBeChanged(t *testing.T) {
	root := root(t)
	fifo := fsx.Join(root, "fifo")
	ok(t, syscall.Mkfifo(fifo, 0o644))
	const message = "Only regular files and directories can be changed"
	if err := MoveEntry(fifo, fsx.Join(root, "moved")); err == nil || err.Error() != message {
		t.Errorf("moving a FIFO: %v", err)
	}
	if _, err := TrashEntry(fifo); err == nil || err.Error() != message {
		t.Errorf("trashing a FIFO: %v", err)
	}
	if exists(fsx.Join(root, Trash)) {
		t.Error("a refused trash must not create recovery storage")
	}
}
