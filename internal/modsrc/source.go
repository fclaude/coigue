// Package modsrc fetches Scheme modules from GitHub repositories and records
// where each came from. It never runs anything and is never called while a
// configuration loads: a module it installs is an ordinary file in
// modules/NAME.scm, and require reads it like any other.
package modsrc

import (
	"fmt"
	"regexp"
	"strings"
)

// Source names a repository and, optionally, a ref (a tag, a branch or a
// commit) to take the modules from.
type Source struct {
	Owner, Repo, Ref string
}

var (
	ownerRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	repoRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	refRE    = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ParseSource reads owner/repo or owner/repo@ref. A leading github.com/ or
// https://github.com/ is accepted so a pasted address works.
func ParseSource(s string) (Source, error) {
	text := strings.TrimSpace(s)
	for _, prefix := range []string{"https://github.com/", "github.com/"} {
		text = strings.TrimPrefix(text, prefix)
	}
	slug, ref, hasRef := strings.Cut(text, "@")
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok || !ownerRE.MatchString(owner) || !repoRE.MatchString(repo) || repo == "." || repo == ".." {
		return Source{}, fmt.Errorf("%q is not a GitHub repository: write owner/repo or owner/repo@ref", s)
	}
	src := Source{Owner: owner, Repo: repo}
	if hasRef {
		if err := checkRef(ref); err != nil {
			return Source{}, err
		}
		src.Ref = ref
	}
	return src, nil
}

func checkRef(ref string) error {
	bad := !refRE.MatchString(ref) ||
		strings.Contains(ref, "..") || strings.Contains(ref, "//") ||
		strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") ||
		strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, ".lock")
	if bad {
		return fmt.Errorf("%q is not a usable tag, branch or commit name", ref)
	}
	return nil
}

// Slug is owner/repo.
func (s Source) Slug() string { return s.Owner + "/" + s.Repo }

// String is the form ParseSource reads.
func (s Source) String() string {
	if s.Ref == "" {
		return s.Slug()
	}
	return s.Slug() + "@" + s.Ref
}

// IsCommit reports whether ref is already a full commit hash, so there is
// nothing to resolve.
func IsCommit(ref string) bool { return commitRE.MatchString(ref) }
