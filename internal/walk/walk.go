// Package walk replaces the Rust `ignore` crate for the two walks the editor
// does: recursive file discovery (picker and project search) and the explorer's
// single-directory listing. It reads .gitignore files and .git/info/exclude
// like the crate's WalkBuilder does under this configuration, which the Rust
// source sets in src/picker.rs and src/explorer.rs:
//
//	.ignore(false)  .git_global(false)  .require_git(false)  .follow_links(false)
//
// plus per-call hidden / git_ignore / git_exclude / parents / max_depth. The
// crate's rules are reproduced as they are, not as git has them (see the
// package tests for the known differences from git).
package walk

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/fclaude/coigue/internal/fsx"
)

// ErrIgnoreFile is attached to an entry (or yielded for a walk) when an ignore
// file contains a line that is not a valid glob. The remaining lines of the
// file still apply.
var ErrIgnoreFile = errors.New("invalid glob in an ignore file")

// Kind is a file type as the walk saw it, without following symbolic links.
type Kind uint8

// File kinds.
const (
	KindFile Kind = iota
	KindDir
	KindSymlink
	KindOther
)

// Entry is one visited path.
type Entry struct {
	Path string
	// Name is the file name; the whole path for a root without one.
	Name  string
	Depth int
	Kind  Kind
	// Err is a problem with the ignore files of this directory. It does not
	// stop the walk (the Rust counts it as "skipped").
	Err error
}

// Item is what a walk yields: an entry, or an error for something that could
// not be read (the Rust's `Err(_)` items).
type Item struct {
	Entry Entry
	Err   error
}

// Options configures a walk.
type Options struct {
	// Hidden skips entries whose name starts with a dot unless an ignore file
	// whitelists them.
	Hidden bool
	// GitIgnore and GitExclude read .gitignore and .git/info/exclude.
	GitIgnore  bool
	GitExclude bool
	// Parents also applies the ignore files of the root's ancestors.
	Parents bool
	// MaxDepth limits recursion: 1 lists the root and its direct children.
	// Zero means no limit.
	MaxDepth int
	// Filter, when set, decides whether a non-root entry is kept; a rejected
	// directory is not entered. It runs after the ignore rules.
	Filter func(*Entry) bool
}

// Walker iterates over a directory tree. Within a directory entries come in
// byte order of their names (the crate leaves the order unspecified, and the
// project search sorts by path, which is the same order).
type Walker struct {
	root string
	opts Options

	stage        int
	done         bool
	rootIsFile   bool
	ig           *ignore
	absoluteBase string
	stack        []*frame
	pending      *frame
}

type dirItem struct {
	name string
	kind Kind
}

type frame struct {
	dir   string
	depth int
	ig    *ignore
	items []dirItem
	idx   int
}

const (
	stageInit = iota
	stageRoot
	stageWalk
)

// New starts a walk at root. Nothing is read until Next is called.
func New(root string, opts Options) *Walker {
	return &Walker{root: root, opts: opts}
}

// Next returns the next item, or false at the end of the walk or when ctx is
// done (callers distinguish the two with ctx.Err()).
func (w *Walker) Next(ctx context.Context) (Item, bool) {
	for {
		if w.done || ctx.Err() != nil {
			return Item{}, false
		}
		switch w.stage {
		case stageInit:
			w.stage = stageRoot
			base := &ignore{absoluteParent: true}
			info, err := os.Stat(w.root)
			w.rootIsFile = err == nil && info.Mode().IsRegular()
			if err == nil && info.IsDir() {
				var errs ignoreErrors
				w.ig = w.addParents(base, w.root, &errs)
				if errs.globErrors > 0 {
					return Item{Err: ErrIgnoreFile}, true
				}
			} else {
				w.ig = base
			}
		case stageRoot:
			w.stage = stageWalk
			item, ok := w.rootEntry()
			if !ok {
				w.done = true
			}
			return item, ok
		default:
			if item, ok, yielded := w.step(); yielded {
				return item, ok
			}
		}
	}
}

// rootEntry is the depth-0 entry. A symbolic link at the root is followed to a
// directory (the crate always does that for the start path), and to a file
// when the path is a file.
func (w *Walker) rootEntry() (Item, bool) {
	info, err := os.Lstat(w.root)
	if err != nil {
		return Item{Err: err}, true
	}
	kind := kindOf(info.Mode())
	isDir := kind == KindDir
	if kind == KindSymlink {
		target, err := os.Stat(w.root)
		if err != nil {
			return Item{Err: err}, true
		}
		if w.rootIsFile {
			kind = KindFile
		} else {
			isDir = target.IsDir()
		}
	}
	name, ok := fsx.FileName(w.root)
	if !ok {
		name = w.root
	}
	entry := Entry{Path: w.root, Name: name, Kind: kind}
	if isDir {
		var errs ignoreErrors
		child := w.addChild(w.ig, w.root, &errs)
		entry.Err = errs.err()
		w.pending = &frame{dir: w.root, depth: 1, ig: child}
	}
	return Item{Entry: entry}, true
}

// step advances the walk below the root; it reports whether it produced an
// item.
func (w *Walker) step() (Item, bool, bool) {
	if w.pending != nil {
		f := w.pending
		w.pending = nil
		entries, err := os.ReadDir(f.dir)
		for _, e := range entries {
			f.items = append(f.items, dirItem{name: e.Name(), kind: kindOf(e.Type())})
		}
		if len(f.items) > 0 || err == nil {
			w.stack = append(w.stack, f)
		}
		if err != nil {
			return Item{Err: err}, true, true
		}
		return Item{}, false, false
	}
	if len(w.stack) == 0 {
		w.done = true
		return Item{}, false, true
	}
	f := w.stack[len(w.stack)-1]
	if f.idx >= len(f.items) {
		w.stack = w.stack[:len(w.stack)-1]
		return Item{}, false, false
	}
	it := f.items[f.idx]
	f.idx++
	path := fsx.Join(f.dir, it.name)
	isDir := it.kind == KindDir
	if w.matchedDirEntry(f.ig, path, isDir) == matchIgnore {
		return Item{}, false, false
	}
	entry := Entry{Path: path, Name: it.name, Depth: f.depth, Kind: it.kind}
	if w.opts.Filter != nil && !w.opts.Filter(&entry) {
		return Item{}, false, false
	}
	if isDir && (w.opts.MaxDepth == 0 || f.depth < w.opts.MaxDepth) {
		var errs ignoreErrors
		child := w.addChild(f.ig, path, &errs)
		entry.Err = errs.err()
		w.pending = &frame{dir: path, depth: f.depth + 1, ig: child}
	}
	return Item{Entry: entry}, true, true
}

func kindOf(mode fs.FileMode) Kind {
	switch {
	case mode&fs.ModeSymlink != 0:
		return KindSymlink
	case mode.IsDir():
		return KindDir
	case mode.IsRegular():
		return KindFile
	default:
		return KindOther
	}
}
