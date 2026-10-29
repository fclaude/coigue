package modsrc

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/coigue/internal/ext"
)

// LockName is the lockfile's name, next to init.scm.
const LockName = "modules.lock"

const lockHeader = ";; Written by Coigüe's :module commands: which repository and commit each\n" +
	";; installed module came from, and the hash of the file as installed.\n" +
	";; It is safe to commit. Change it with the commands, not by hand.\n"

var sumRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Entry records one installed module.
type Entry struct {
	Module string // the module's name, the file modules/Module.scm
	Source string // owner/repo
	Commit string // the full commit hash the file was read at
	SHA256 string // the hash of the file as installed
}

// Lock is the set of installed modules, sorted by name.
type Lock struct {
	Entries []Entry
}

// Find returns the entry for a module.
func (l *Lock) Find(module string) (Entry, bool) {
	for _, e := range l.Entries {
		if e.Module == module {
			return e, true
		}
	}
	return Entry{}, false
}

// Set adds the entry or replaces the one for the same module.
func (l *Lock) Set(e Entry) {
	for i := range l.Entries {
		if l.Entries[i].Module == e.Module {
			l.Entries[i] = e
			return
		}
	}
	l.Entries = append(l.Entries, e)
	sort.Slice(l.Entries, func(i, j int) bool { return l.Entries[i].Module < l.Entries[j].Module })
}

// Remove drops the module's entry and reports whether there was one.
func (l *Lock) Remove(module string) bool {
	for i := range l.Entries {
		if l.Entries[i].Module == module {
			l.Entries = append(l.Entries[:i], l.Entries[i+1:]...)
			return true
		}
	}
	return false
}

func (e Entry) check() error {
	if err := ext.CheckModuleName(e.Module); err != nil {
		return err
	}
	src, err := ParseSource(e.Source)
	if err != nil || src.Ref != "" {
		return fmt.Errorf("%s: source %q must be owner/repo", e.Module, e.Source)
	}
	if !commitRE.MatchString(e.Commit) {
		return fmt.Errorf("%s: commit must be 40 hexadecimal digits", e.Module)
	}
	if !sumRE.MatchString(e.SHA256) {
		return fmt.Errorf("%s: sha256 must be 64 hexadecimal digits", e.Module)
	}
	return nil
}

// String is the lockfile's text. Every value was checked to hold no character
// that needs escaping in a Scheme string.
func (l *Lock) String() string {
	var b strings.Builder
	b.WriteString(lockHeader)
	b.WriteString("(modules")
	for _, e := range l.Entries {
		fmt.Fprintf(&b, "\n  (%s (source %q) (commit %q) (sha256 %q))", e.Module, e.Source, e.Commit, e.SHA256)
	}
	b.WriteString(")\n")
	return b.String()
}

// ParseLock reads the lockfile's text as data. Nothing in it is evaluated.
func ParseLock(src string) (*Lock, error) {
	forms, err := scheme.Read(src)
	if err != nil {
		return nil, err
	}
	if len(forms) == 0 {
		return &Lock{}, nil
	}
	if len(forms) > 1 {
		return nil, errors.New("expected one (modules ...) form")
	}
	items, ok := forms[0].Slice()
	if !ok || len(items) == 0 || items[0].Kind != scheme.KSymbol || items[0].S != "modules" {
		return nil, errors.New("expected (modules ...)")
	}
	lock := &Lock{}
	seen := map[string]bool{}
	for _, item := range items[1:] {
		e, err := parseEntry(item)
		if err != nil {
			return nil, err
		}
		if seen[e.Module] {
			return nil, fmt.Errorf("%s is listed twice", e.Module)
		}
		seen[e.Module] = true
		lock.Entries = append(lock.Entries, e)
	}
	sort.Slice(lock.Entries, func(i, j int) bool { return lock.Entries[i].Module < lock.Entries[j].Module })
	return lock, nil
}

func parseEntry(v scheme.Value) (Entry, error) {
	parts, ok := v.Slice()
	if !ok || len(parts) == 0 || parts[0].Kind != scheme.KSymbol {
		return Entry{}, errors.New("an entry is (name (source \"owner/repo\") (commit \"...\") (sha256 \"...\"))")
	}
	e := Entry{Module: parts[0].S}
	set := map[string]*string{"source": &e.Source, "commit": &e.Commit, "sha256": &e.SHA256}
	got := map[string]bool{}
	for _, field := range parts[1:] {
		kv, ok := field.Slice()
		if !ok || len(kv) != 2 || kv[0].Kind != scheme.KSymbol || kv[1].Kind != scheme.KString {
			return Entry{}, fmt.Errorf("%s: a field is (source \"...\"), (commit \"...\") or (sha256 \"...\")", e.Module)
		}
		dst, known := set[kv[0].S]
		if !known {
			return Entry{}, fmt.Errorf("%s: %q is not a field; use source, commit or sha256", e.Module, kv[0].S)
		}
		if got[kv[0].S] {
			return Entry{}, fmt.Errorf("%s: %s is given twice", e.Module, kv[0].S)
		}
		got[kv[0].S] = true
		*dst = kv[1].S
	}
	for name := range set {
		if !got[name] {
			return Entry{}, fmt.Errorf("%s: %s is missing", e.Module, name)
		}
	}
	if err := e.check(); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// ReadLock reads the lockfile in dir. A missing file is an empty lock.
func ReadLock(dir string) (*Lock, error) {
	path := filepath.Join(dir, LockName)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Lock{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", LockName)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFile {
		return nil, fmt.Errorf("%s: larger than %d bytes", LockName, MaxFile)
	}
	lock, err := ParseLock(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", LockName, err)
	}
	return lock, nil
}

// WriteLock replaces the lockfile in dir, all or nothing.
func WriteLock(dir string, l *Lock) error {
	for _, e := range l.Entries {
		if err := e.check(); err != nil {
			return err
		}
	}
	return writeFile(filepath.Join(dir, LockName), []byte(l.String()))
}

// writeFile replaces path with data through a temporary file in the same
// directory, so a failure leaves the old file whole. It makes the directory
// when it is missing and never follows a symbolic link at path: the rename
// replaces the link itself.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".coigue-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
