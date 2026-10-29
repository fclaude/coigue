package modsrc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fclaude/brote/scheme"
	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/grant"
)

// Fetcher is what a Store needs from GitHub. Client is one.
type Fetcher interface {
	Resolve(ctx context.Context, src Source) (string, error)
	Fetch(ctx context.Context, src Source, commit string) ([]File, error)
}

// Store is the configuration directory: modules/NAME.scm beside init.scm, and
// the lockfile that says which of those files came from a repository.
type Store struct {
	Dir string
}

func (s Store) path(module string) string {
	return filepath.Join(s.Dir, "modules", module+".scm")
}

// disk is what is at a module's path now.
type disk struct {
	data    []byte
	exists  bool
	regular bool
}

func (s Store) read(module string) (disk, error) {
	path := s.path(module)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return disk{}, nil
	}
	if err != nil {
		return disk{}, err
	}
	if !info.Mode().IsRegular() {
		return disk{exists: true}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return disk{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil {
		return disk{}, err
	}
	return disk{data: data, exists: true, regular: true}, nil
}

// State is how an installed module's file compares with its lock entry.
type State string

const (
	StateOK       State = "ok"       // the file is what was installed
	StateModified State = "modified" // the file was changed after it was installed
	StateMissing  State = "missing"  // the file is gone
	StateLocal    State = "local"    // a file no repository installed
)

// Status is one module in the list.
type Status struct {
	Module string
	State  State
	Entry  Entry // empty for a local module
}

// List reports every installed module and every other file in modules/.
func (s Store) List() ([]Status, error) {
	lock, err := ReadLock(s.Dir)
	if err != nil {
		return nil, err
	}
	var out []Status
	listed := map[string]bool{}
	for _, e := range lock.Entries {
		listed[e.Module] = true
		d, err := s.read(e.Module)
		if err != nil {
			return nil, err
		}
		state := StateOK
		switch {
		case !d.exists:
			state = StateMissing
		case !d.regular || Sum(d.data) != e.SHA256:
			state = StateModified
		}
		out = append(out, Status{Module: e.Module, State: state, Entry: e})
	}
	names, err := os.ReadDir(filepath.Join(s.Dir, "modules"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, de := range names {
		module, ok := strings.CutSuffix(de.Name(), ".scm")
		if !ok || listed[module] || ext.CheckModuleName(module) != nil {
			continue
		}
		out = append(out, Status{Module: module, State: StateLocal})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Module < out[j].Module })
	return out, nil
}

// Kind says what applying a Change does to the module's file.
type Kind string

const (
	Add    Kind = "add"
	Update Kind = "update"
	Same   Kind = "same" // the file already matches; only the lock moves
)

// Change is one module of a Plan.
type Change struct {
	Module string
	Kind   Kind
	Old    []byte // the file now, for a diff; nil for an add
	New    []byte
	// Blocked is why this cannot be applied. A plan with a blocked change
	// applies none of its changes.
	Blocked string
	// Grant is the network the new file asks for, and Was what the person
	// approved for the file it replaces. Applying the plan approves Grant.
	Grant, Was grant.Grant

	prior string // hash of the file when the plan was made, "" if absent
}

// Plan is what installing or updating would do, made without writing anything,
// so the person can read it first.
type Plan struct {
	Source  Source // with the ref asked for
	Commit  string
	Changes []Change
	// Notes are things worth knowing that do not stop the plan: a module the
	// repository dropped, a module that needs another that is not installed.
	Notes []string
}

// Blocked lists the changes that stop the plan.
func (p *Plan) Blocked() []Change {
	var out []Change
	for _, c := range p.Changes {
		if c.Blocked != "" {
			out = append(out, c)
		}
	}
	return out
}

// Pending reports whether applying the plan would change anything on disk or
// in the lock.
func (p *Plan) Pending() bool { return len(p.Changes) > 0 }

// PlanAdd plans installing modules of src. With no names it takes every module
// the repository has at that commit; a name it does not have is an error.
func (s Store) PlanAdd(ctx context.Context, f Fetcher, src Source, names []string) (*Plan, error) {
	commit, err := f.Resolve(ctx, src)
	if err != nil {
		return nil, err
	}
	files, err := f.Fetch(ctx, src, commit)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s has no modules/NAME.scm files at %s", src.Slug(), commit[:7])
	}
	chosen, err := choose(src, files, names)
	if err != nil {
		return nil, err
	}
	lock, err := ReadLock(s.Dir)
	if err != nil {
		return nil, err
	}
	return s.plan(src, commit, chosen, lock)
}

func choose(src Source, files []File, names []string) ([]File, error) {
	if len(names) == 0 {
		return files, nil
	}
	byName := map[string]File{}
	for _, f := range files {
		byName[f.Module] = f
	}
	var out []File
	seen := map[string]bool{}
	for _, n := range names {
		f, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("%s has no module %s; it has %s", src.Slug(), n, listNames(files))
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, f)
		}
	}
	return out, nil
}

func listNames(files []File) string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Module
	}
	return strings.Join(names, ", ")
}

func (s Store) plan(src Source, commit string, chosen []File, lock *Lock) (*Plan, error) {
	p := &Plan{Source: src, Commit: commit}
	inPlan := map[string]bool{}
	for _, f := range chosen {
		inPlan[f.Module] = true
	}
	for _, f := range chosen {
		c, err := s.change(src, f, lock)
		if err != nil {
			return nil, err
		}
		p.Changes = append(p.Changes, c)
	}
	p.Notes = s.requireNotes(chosen, inPlan, lock)
	return p, nil
}

func (s Store) change(src Source, f File, lock *Lock) (Change, error) {
	c := Change{Module: f.Module, New: f.Data}
	if err := checkHeader(f); err != nil {
		c.Blocked = err.Error()
		return c, nil
	}
	asked, err := grant.FromModule(string(f.Data))
	if err != nil {
		c.Blocked = fmt.Sprintf("%s.scm: %v", f.Module, err)
		return c, nil
	}
	c.Grant = asked
	d, err := s.read(f.Module)
	if err != nil {
		return Change{}, err
	}
	entry, locked := lock.Find(f.Module)
	c.Was = entry.Net
	switch {
	case !d.exists && !locked:
		c.Kind = Add
	case !d.exists:
		c.Kind = Add // lost the file; installing it again repairs the entry
	case !d.regular:
		c.Blocked = fmt.Sprintf("modules/%s.scm is not a regular file", f.Module)
	case !locked:
		c.Blocked = fmt.Sprintf("modules/%s.scm exists and was not installed from a repository; move or delete it first", f.Module)
	case entry.Source != src.Slug():
		c.Blocked = fmt.Sprintf("modules/%s.scm was installed from %s", f.Module, entry.Source)
	case Sum(d.data) != entry.SHA256:
		c.Blocked = fmt.Sprintf("modules/%s.scm was edited after it was installed; delete it first to take the repository's copy", f.Module)
	default:
		c.Kind = Update
		c.Old = d.data
		c.prior = entry.SHA256
		if bytes.Equal(d.data, f.Data) {
			c.Kind = Same
		}
	}
	return c, nil
}

// checkHeader makes sure the file begins with the (define-module NAME ...)
// form the loader requires, so a repository's stray file is not installed as
// a module that can only fail.
func checkHeader(f File) error {
	forms, err := scheme.Read(string(f.Data))
	if err != nil {
		return fmt.Errorf("%s.scm does not read as Scheme: %v", f.Module, err)
	}
	if len(forms) > 0 {
		if items, ok := forms[0].Slice(); ok && len(items) >= 2 &&
			items[0].Kind == scheme.KSymbol && items[0].S == "define-module" &&
			items[1].Kind == scheme.KSymbol && items[1].S == f.Module {
			return nil
		}
	}
	return fmt.Errorf("%s.scm must start with (define-module %s ...)", f.Module, f.Module)
}

// requireNotes names modules the planned files require that will not be there.
func (s Store) requireNotes(chosen []File, inPlan map[string]bool, lock *Lock) []string {
	var notes []string
	for _, f := range chosen {
		forms, err := scheme.Read(string(f.Data))
		if err != nil {
			continue
		}
		for _, form := range forms {
			items, ok := form.Slice()
			if !ok || len(items) < 2 || items[0].Kind != scheme.KSymbol || items[0].S != "require" {
				continue
			}
			need := requiredName(items[1])
			if need == "" || inPlan[need] {
				continue
			}
			if _, locked := lock.Find(need); locked {
				continue
			}
			if d, err := s.read(need); err == nil && d.exists {
				continue
			}
			notes = append(notes, fmt.Sprintf("%s requires %s, which is not installed", f.Module, need))
		}
	}
	return notes
}

func requiredName(v scheme.Value) string {
	if v.Kind == scheme.KSymbol {
		return v.S
	}
	if items, ok := v.Slice(); ok && len(items) == 2 &&
		items[0].Kind == scheme.KSymbol && items[0].S == "quote" && items[1].Kind == scheme.KSymbol {
		return items[1].S
	}
	return ""
}

// PlanUpdate plans moving installed modules to the commit their ref names now.
// With no names it covers every installed module. Modules installed from the
// same repository and ref are fetched once. A group already at the newest
// commit contributes no changes.
func (s Store) PlanUpdate(ctx context.Context, f Fetcher, names []string) ([]*Plan, error) {
	lock, err := ReadLock(s.Dir)
	if err != nil {
		return nil, err
	}
	var chosen []Entry
	if len(names) == 0 {
		chosen = lock.Entries
	}
	for _, n := range names {
		e, ok := lock.Find(n)
		if !ok {
			return nil, fmt.Errorf("%s was not installed from a repository", n)
		}
		chosen = append(chosen, e)
	}
	if len(chosen) == 0 {
		return nil, errors.New("no modules were installed from a repository")
	}
	groups := map[Source][]Entry{}
	var order []Source
	for _, e := range chosen {
		src := e.From()
		if _, ok := groups[src]; !ok {
			order = append(order, src)
		}
		groups[src] = append(groups[src], e)
	}
	var plans []*Plan
	for _, src := range order {
		entries := groups[src]
		commit, err := f.Resolve(ctx, src)
		if err != nil {
			return nil, err
		}
		stale := false
		for _, e := range entries {
			stale = stale || e.Commit != commit
		}
		if !stale {
			continue
		}
		files, err := f.Fetch(ctx, src, commit)
		if err != nil {
			return nil, err
		}
		byName := map[string]File{}
		for _, file := range files {
			byName[file.Module] = file
		}
		var chosenFiles []File
		var gone []string
		for _, e := range entries {
			if file, ok := byName[e.Module]; ok {
				chosenFiles = append(chosenFiles, file)
			} else {
				gone = append(gone, e.Module)
			}
		}
		p, err := s.plan(src, commit, chosenFiles, lock)
		if err != nil {
			return nil, err
		}
		for _, m := range gone {
			p.Notes = append(p.Notes, fmt.Sprintf("%s is no longer in %s at %s; it stays as installed", m, src.Slug(), commit[:7]))
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// Apply writes the plan: each module file, then the lockfile. If anything
// fails the files already written are put back as they were. It refuses a plan
// with a blocked change, and one whose files changed on disk after it was made.
func (s Store) Apply(p *Plan) error {
	if blocked := p.Blocked(); len(blocked) > 0 {
		return errors.New(blocked[0].Blocked)
	}
	lock, err := ReadLock(s.Dir)
	if err != nil {
		return err
	}
	for _, c := range p.Changes {
		d, err := s.read(c.Module)
		if err != nil {
			return err
		}
		now := ""
		if d.exists {
			now = "?"
			if d.regular {
				now = Sum(d.data)
			}
		}
		if now != c.prior {
			return fmt.Errorf("modules/%s.scm changed since the plan was made; run the command again", c.Module)
		}
	}
	type undo struct {
		module string
		old    []byte
		had    bool
	}
	var done []undo
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			u := done[i]
			if u.had {
				_ = writeFile(s.path(u.module), u.old)
			} else {
				_ = os.Remove(s.path(u.module))
			}
		}
	}
	for _, c := range p.Changes {
		if c.Kind != Same {
			if err := writeFile(s.path(c.Module), c.New); err != nil {
				rollback()
				return fmt.Errorf("writing modules/%s.scm: %w", c.Module, err)
			}
			done = append(done, undo{module: c.Module, old: c.Old, had: c.prior != ""})
		}
		lock.Set(Entry{Module: c.Module, Source: p.Source.Slug(), Ref: p.Source.Ref,
			Commit: p.Commit, SHA256: Sum(c.New), Net: c.Grant})
	}
	if err := WriteLock(s.Dir, lock); err != nil {
		rollback()
		return err
	}
	return nil
}

// Remove deletes an installed module's file and its lock entry. A file that
// was edited since it was installed is refused, so work is not lost; if the
// file is already gone only the entry is dropped.
func (s Store) Remove(module string) error {
	lock, err := ReadLock(s.Dir)
	if err != nil {
		return err
	}
	e, ok := lock.Find(module)
	if !ok {
		if err := ext.CheckModuleName(module); err != nil {
			return err
		}
		return fmt.Errorf("%s was not installed from a repository; delete modules/%s.scm yourself", module, module)
	}
	d, err := s.read(module)
	if err != nil {
		return err
	}
	if d.exists && (!d.regular || Sum(d.data) != e.SHA256) {
		return fmt.Errorf("modules/%s.scm was edited after it was installed; delete it yourself", module)
	}
	if d.exists {
		if err := os.Remove(s.path(module)); err != nil {
			return err
		}
	}
	lock.Remove(module)
	if err := WriteLock(s.Dir, lock); err != nil {
		if d.exists {
			_ = writeFile(s.path(module), d.data)
		}
		return err
	}
	return nil
}

// SyncReport says what Sync did, by module name.
type SyncReport struct {
	Restored []string // written because the file was missing
	Present  []string // already as locked
	Modified []string // changed locally, left alone
	Failed   map[string]error
}

// Sync makes sure every locked module is on disk exactly as locked: it fetches
// each commit in the lockfile, restores a missing file and checks the hash of
// what it fetched against the lock. A file that differs from the lock is left
// alone. A repository whose files no longer match their locked hash is an
// error and nothing from it is written.
func (s Store) Sync(ctx context.Context, f Fetcher) (SyncReport, error) {
	lock, err := ReadLock(s.Dir)
	if err != nil {
		return SyncReport{}, err
	}
	report := SyncReport{Failed: map[string]error{}}
	type key struct{ src, commit string }
	fetched := map[key][]File{}
	failed := map[key]error{}
	for _, e := range lock.Entries {
		d, err := s.read(e.Module)
		if err != nil {
			report.Failed[e.Module] = err
			continue
		}
		if d.exists {
			if d.regular && Sum(d.data) == e.SHA256 {
				report.Present = append(report.Present, e.Module)
			} else {
				report.Modified = append(report.Modified, e.Module)
			}
			continue
		}
		k := key{e.Source, e.Commit}
		if err, bad := failed[k]; bad {
			report.Failed[e.Module] = err
			continue
		}
		files, ok := fetched[k]
		if !ok {
			files, err = f.Fetch(ctx, e.From(), e.Commit)
			if err != nil {
				failed[k] = err
				report.Failed[e.Module] = err
				continue
			}
			fetched[k] = files
		}
		var found *File
		for i := range files {
			if files[i].Module == e.Module {
				found = &files[i]
			}
		}
		switch {
		case found == nil:
			report.Failed[e.Module] = fmt.Errorf("%s is no longer in %s at %s", e.Module, e.Source, e.Commit[:7])
		case found.Sum() != e.SHA256:
			report.Failed[e.Module] = fmt.Errorf("%s from %s at %s does not match the lock; the history changed or the lock was edited", e.Module, e.Source, e.Commit[:7])
		default:
			if err := writeFile(s.path(e.Module), found.Data); err != nil {
				report.Failed[e.Module] = err
				continue
			}
			report.Restored = append(report.Restored, e.Module)
		}
	}
	return report, nil
}
