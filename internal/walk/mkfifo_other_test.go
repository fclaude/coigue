//go:build !unix

package walk_test

import "os"

// mkfifo has no equivalent here; a plain file stands in.
func mkfifo(path string) error { return os.WriteFile(path, nil, 0o644) }
