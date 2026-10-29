// Package version holds the build identity reported by --version. The
// package revision and source commit are fixed at link time, never read from
// git at run time:
//
//	go build -ldflags "-X github.com/fclaude/coigue/internal/version.PackageRevision=<rev> \
//	                   -X github.com/fclaude/coigue/internal/version.SourceCommit=<sha>" ./cmd/coigue
//
// Version is the source version (Cargo.toml's version in the Rust editor) and
// can be overridden the same way.
package version

import "fmt"

// Build identity. These are variables only so that -ldflags -X can set them.
var (
	Version         = "0.4.0"
	PackageRevision = "development"
	SourceCommit    = "unknown"
)

// Line is the one-line --version report.
func Line() string {
	return fmt.Sprintf("coigue %s (main build %s; commit %s)", Version, PackageRevision, SourceCommit)
}
