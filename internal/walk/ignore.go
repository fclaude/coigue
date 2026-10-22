package walk

import (
	"os"
	"strings"

	"github.com/fclaude/coigue/internal/fsx"
)

// ignore is one level of the persistent matcher chain (the crate's Ignore):
// the rules of one directory plus a link to the rules of its parent. Chains
// are immutable once built, so a walker can keep several alive while it
// descends and climbs.
type ignore struct {
	parent *ignore
	dir    string
	// absoluteParent marks ancestors of the walk root, which the walk adds
	// up-front from the canonical path; they are matched against a path
	// rebuilt below the canonical base.
	absoluteParent bool
	gitignore      *gitignore
	exclude        *gitignore
}

// ignoreErrors accumulates what the crate reports as an entry's `error()`:
// ignore files with lines that failed to compile.
type ignoreErrors struct{ globErrors int }

func (e *ignoreErrors) err() error {
	if e.globErrors == 0 {
		return nil
	}
	return ErrIgnoreFile
}

// addChild builds the ignore level for dir, reading dir/.gitignore and
// dir/.git/info/exclude as the options say. require_git is off, so a
// repository is never needed (and its presence changes nothing). A missing
// file is no error; I/O errors are dropped like the crate does.
func (w *Walker) addChild(parent *ignore, dir string, errs *ignoreErrors) *ignore {
	child := &ignore{parent: parent, dir: dir}
	if w.opts.GitIgnore {
		child.gitignore = readIgnoreFile(dir, fsx.Join(dir, ".gitignore"), errs)
	}
	if w.opts.GitExclude {
		child.exclude = readIgnoreFile(dir, fsx.Join(fsx.Join(dir, ".git"), "info/exclude"), errs)
	}
	return child
}

func readIgnoreFile(dir, path string, errs *ignoreErrors) *gitignore {
	// A stat that follows links stands in for the crate's exists() check.
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	b := newGitignoreBuilder(dir)
	b.addFile(path)
	errs.globErrors += b.globErrors
	return b.build()
}

// addParents is Ignore::add_parents: it adds the ignore levels of every
// ancestor of root, from the filesystem root down, and remembers the canonical
// path of root. It does nothing unless some rule family needs the parents.
func (w *Walker) addParents(base *ignore, root string, errs *ignoreErrors) *ignore {
	if !w.opts.Parents && !w.opts.GitIgnore && !w.opts.GitExclude {
		return base
	}
	canonical, err := fsx.Canonicalize(root)
	if err != nil {
		// Nothing sensible to do; like the crate, drop the error.
		return base
	}
	w.absoluteBase = canonical
	var parents []string
	for path := canonical; ; {
		parent, ok := fsx.Parent(path)
		if !ok {
			break
		}
		parents = append(parents, parent)
		path = parent
	}
	ig := base
	for i := len(parents) - 1; i >= 0; i-- {
		child := w.addChild(ig, parents[i], errs)
		child.absoluteParent = true
		ig = child
	}
	return ig
}

// matchedDirEntry is Ignore::matched_dir_entry: ignore rules first, then the
// hidden-file filter for entries no rule mentioned (a whitelist match
// un-hides).
func (w *Walker) matchedDirEntry(ig *ignore, path string, isDir bool) match {
	m := w.matched(ig, path, isDir)
	if m == matchNone && w.opts.Hidden && isHiddenPath(path) {
		return matchIgnore
	}
	return m
}

func (w *Walker) matched(ig *ignore, path string, isDir bool) match {
	path = strings.TrimPrefix(path, "./")
	if !w.opts.GitIgnore && !w.opts.GitExclude {
		return matchNone
	}
	return w.matchedIgnore(ig, path, isDir)
}

// matchedIgnore is Ignore::matched_ignore for the configurations the editor
// uses: .ignore files and the global excludes file are off, and require_git
// is off, so every level's rules apply and no `.git` boundary stops the climb.
// For each rule family the nearest level with any matching glob decides; a
// .gitignore verdict beats a .git/info/exclude verdict whatever their depth.
func (w *Walker) matchedIgnore(ig *ignore, path string, isDir bool) match {
	var gi, exclude match
	cur := ig
	var topmost *ignore
	for ; cur != nil && !cur.absoluteParent; cur = cur.parent {
		if gi == matchNone {
			gi = cur.gitignore.matched(path, isDir)
		}
		if exclude == matchNone {
			exclude = cur.exclude.matched(path, isDir)
		}
		topmost = cur
	}
	if w.opts.Parents && w.absoluteBase != "" {
		// Rebuild the path below the canonical base of the walk root so that
		// the ancestors' ignore files see it relative to their own directory.
		relative := path
		if topmost != nil && topmost.dir != "." {
			withoutDotSlash := stripIfPrefix("./", topmost.dir)
			relativeBase := stripIfPrefix(withoutDotSlash, path)
			relative = stripIfPrefix("/", relativeBase)
		}
		absolute := fsx.Join(w.absoluteBase, relative)
		for ; cur != nil; cur = cur.parent {
			if gi == matchNone {
				gi = cur.gitignore.matched(absolute, isDir)
			}
			if exclude == matchNone {
				exclude = cur.exclude.matched(absolute, isDir)
			}
		}
	}
	return gi.or(exclude)
}

func stripIfPrefix(prefix, path string) string {
	if rest, ok := strings.CutPrefix(path, prefix); ok {
		return rest
	}
	return path
}

// isHiddenPath is the crate's is_hidden_path_only: the file name starts with
// '.', where paths ending in '.' or '..' have no file name at all.
func isHiddenPath(path string) bool {
	switch {
	case path == "", path == ".", strings.HasSuffix(path, "."):
		return false
	}
	name := path[strings.LastIndexByte(path, '/')+1:]
	return strings.HasPrefix(name, ".")
}
