package modsrc

import (
	"fmt"

	"github.com/fclaude/coigue/internal/grant"
)

// Grants decides what network each module gets, for ext.Options.Grants. A
// module installed from a repository gets what its header asks for only up to
// what was approved when it was installed or updated, which modules.lock
// records. A module the person wrote, one with no entry, gets what it asks.
// If the lockfile cannot be read, no module gets anything: it is not known
// which ones came from a repository.
func Grants(dir string) func(module string, asked grant.Grant) (grant.Grant, string) {
	lock, err := ReadLock(dir)
	return func(module string, asked grant.Grant) (grant.Grant, string) {
		if err != nil {
			return grant.Grant{}, fmt.Sprintf("%s could not be read (%v), so it is not known which modules came from a repository", LockName, err)
		}
		entry, ok := lock.Find(module)
		if !ok || asked.Within(entry.Net) {
			return asked, ""
		}
		return grant.Grant{}, fmt.Sprintf("%s approved %s, but the file now asks for %s; run :module-update to review it, or edit the file back",
			LockName, approved(entry.Net), asked.Describe())
	}
}

func approved(g grant.Grant) string {
	if g.None() {
		return "no network access"
	}
	return g.Describe()
}
