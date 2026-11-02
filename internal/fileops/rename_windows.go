//go:build windows

package fileops

import "github.com/fclaude/coigue/internal/fsx"

func renameExclusive(from, to string) error { return fsx.RenameNoReplace(from, to) }
