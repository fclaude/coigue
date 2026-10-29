package editor

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fclaude/coigue/internal/ext"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/modsrc"
)

// Modules from GitHub; see docs/extending.md. The :module-* commands fetch,
// check and write ordinary files into modules/, and nothing else: require
// still reads only those files, and no Scheme runs while one is fetched. A
// fetch runs in the background and ends in a page showing the code, which is
// applied only when the person answers y.

// moduleJobTimeout bounds one fetch from start to finish.
const moduleJobTimeout = 90 * time.Second

// moduleResult is what a background fetch hands back.
type moduleResult struct {
	plans   []*modsrc.Plan
	message string
	err     error
	update  bool // the plans move installed modules, rather than add
}

type moduleJob struct {
	done   chan moduleResult
	cancel context.CancelFunc
}

// ModuleReview is the page that shows what a :module-add or :module-update
// would do, with the code, and waits for y or n. A page with no plans is only
// shown (:module-list).
type ModuleReview struct {
	Title  string
	Lines  []string
	Scroll int

	plans  []*modsrc.Plan
	update bool
}

// ReadOnly reports whether the page only shows something and has no plan to
// answer.
func (r *ModuleReview) ReadOnly() bool { return len(r.plans) == 0 }

// CanApply reports whether y does anything: there is a plan and none of its
// changes is blocked.
func (r *ModuleReview) CanApply() bool {
	if r.ReadOnly() {
		return false
	}
	for _, p := range r.plans {
		if len(p.Blocked()) > 0 {
			return false
		}
	}
	return true
}

// Fit keeps the scroll position inside the page. The view calls it with the
// number of rows the lines take at its width, once they are wrapped.
func (r *ModuleReview) Fit(total, rows int) {
	r.Scroll = max(min(r.Scroll, total-max(rows, 1)), 0)
}

func (e *Editor) moduleStore() (modsrc.Store, bool) {
	if e.initPath == "" {
		e.Message = "modules: no place for them on this machine; set HOME or XDG_CONFIG_HOME"
		return modsrc.Store{}, false
	}
	return modsrc.Store{Dir: filepath.Dir(e.initPath)}, true
}

func (e *Editor) fetcher() modsrc.Fetcher {
	if e.moduleFetcher == nil {
		e.moduleFetcher = modsrc.NewClient()
	}
	return e.moduleFetcher
}

// startModuleJob runs work in the background. Its result is collected by
// Tick, so the editor stays responsive and the screen keeps redrawing.
func (e *Editor) startModuleJob(status string, work func(ctx context.Context) moduleResult) {
	if e.moduleJob != nil {
		e.Message = "A module command is still running"
		return
	}
	ctx, cancel := context.WithTimeout(e.ctx, moduleJobTimeout)
	job := &moduleJob{done: make(chan moduleResult, 1), cancel: cancel}
	e.moduleJob = job
	e.Message = status
	go func() {
		defer cancel()
		job.done <- work(ctx)
	}()
}

func (e *Editor) pollModules() {
	if e.moduleJob == nil {
		return
	}
	var res moduleResult
	select {
	case res = <-e.moduleJob.done:
	default:
		return
	}
	e.moduleJob = nil
	switch {
	case res.err != nil:
		e.Message = ext.Tidy(res.err.Error())
	case res.message != "":
		e.Message = ext.Tidy(res.message)
	case len(res.plans) == 0:
		e.Message = "Nothing to do"
	default:
		title := "Install modules from GitHub"
		if res.update {
			title = "Update modules from GitHub"
		}
		e.ModuleReview = &ModuleReview{Title: title, Lines: modsrc.Review(res.plans), plans: res.plans, update: res.update}
		e.Message = ""
	}
}

func moduleAddCommand(e *Editor, _ string, args []string) bool {
	store, ok := e.moduleStore()
	if !ok {
		return false
	}
	if len(args) == 0 {
		e.Message = "Usage: :module-add owner/repo[@ref] [module...]"
		return false
	}
	src, err := modsrc.ParseSource(args[0])
	if err != nil {
		e.Message = ext.Tidy(err.Error())
		return false
	}
	names := args[1:]
	f := e.fetcher()
	e.startModuleJob("Fetching "+src.String()+"...", func(ctx context.Context) moduleResult {
		plan, err := store.PlanAdd(ctx, f, src, names)
		if err != nil {
			return moduleResult{err: err}
		}
		return moduleResult{plans: []*modsrc.Plan{plan}}
	})
	return false
}

func moduleUpdateCommand(e *Editor, _ string, args []string) bool {
	store, ok := e.moduleStore()
	if !ok {
		return false
	}
	f := e.fetcher()
	e.startModuleJob("Checking for newer modules...", func(ctx context.Context) moduleResult {
		plans, err := store.PlanUpdate(ctx, f, args)
		switch {
		case err != nil:
			return moduleResult{err: err}
		case len(plans) == 0:
			return moduleResult{message: "Installed modules are up to date"}
		}
		return moduleResult{plans: plans, update: true}
	})
	return false
}

func moduleSyncCommand(e *Editor, _ string, _ []string) bool {
	store, ok := e.moduleStore()
	if !ok {
		return false
	}
	f := e.fetcher()
	e.startModuleJob("Restoring modules from "+modsrc.LockName+"...", func(ctx context.Context) moduleResult {
		report, err := store.Sync(ctx, f)
		if err != nil {
			return moduleResult{err: err}
		}
		return moduleResult{message: syncMessage(report)}
	})
	return false
}

func syncMessage(r modsrc.SyncReport) string {
	var parts []string
	if n := len(r.Restored); n > 0 {
		parts = append(parts, "restored "+strings.Join(r.Restored, ", "))
	}
	if n := len(r.Modified); n > 0 {
		parts = append(parts, "left alone (edited): "+strings.Join(r.Modified, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(r.Failed)) {
		parts = append(parts, name+": "+r.Failed[name].Error())
	}
	if len(parts) == 0 {
		if len(r.Present) == 0 {
			return "No modules are installed from a repository"
		}
		return "All modules are in place"
	}
	return "Modules: " + strings.Join(parts, "; ")
}

func moduleRemoveCommand(e *Editor, _ string, args []string) bool {
	store, ok := e.moduleStore()
	if !ok {
		return false
	}
	if len(args) != 1 {
		e.Message = "Usage: :module-remove module"
		return false
	}
	if err := store.Remove(args[0]); err != nil {
		e.Message = ext.Tidy(err.Error())
		return false
	}
	e.Message = "Removed " + args[0] + "; :reload loads init.scm without it"
	return false
}

func moduleListCommand(e *Editor, _ string, _ []string) bool {
	store, ok := e.moduleStore()
	if !ok {
		return false
	}
	list, err := store.List()
	if err != nil {
		e.Message = ext.Tidy(err.Error())
		return false
	}
	if len(list) == 0 {
		e.Message = "No modules in " + filepath.Join(store.Dir, "modules")
		return false
	}
	lines := make([]string, 0, len(list)+2)
	lines = append(lines, "Modules in "+modsrc.Printable(filepath.Join(store.Dir, "modules")), "")
	for _, st := range list {
		line := fmt.Sprintf("%-20s %-9s", st.Module, st.State)
		if st.State != modsrc.StateLocal {
			line += " " + st.Entry.From().String() + " " + st.Entry.Commit[:7]
		}
		lines = append(lines, modsrc.Printable(line))
	}
	e.ModuleReview = &ModuleReview{Title: "Modules", Lines: lines}
	return false
}

// moduleReviewKey handles every key while the page is open: nothing reaches
// the buffer, a menu or a command until it is answered.
func (e *Editor) moduleReviewKey(k key.Key, pageRows int) {
	r := e.ModuleReview
	if ctrlAltSuper(k.Mods) {
		return
	}
	rows := max(pageRows, 1)
	switch {
	case k.Code == key.Esc || k.Code == 'n' || k.Code == 'q':
		e.ModuleReview = nil
		if !r.ReadOnly() {
			e.Message = "Cancelled; nothing was changed"
		}
	case k.Code == 'y' && r.CanApply():
		e.ModuleReview = nil
		e.applyModulePlans(r)
	case k.Code == key.Up || k.Code == 'k':
		r.Scroll = max(r.Scroll-1, 0)
	case k.Code == key.Down || k.Code == 'j':
		r.Scroll++
	case k.Code == key.PageUp:
		r.Scroll = max(r.Scroll-rows, 0)
	case k.Code == key.PageDown:
		r.Scroll += rows
	case k.Code == key.Home:
		r.Scroll = 0
	case k.Code == key.End:
		r.Scroll = 1 << 30
	}
}

func (e *Editor) applyModulePlans(r *ModuleReview) {
	store, ok := e.moduleStore()
	if !ok {
		return
	}
	var changed []string
	for _, p := range r.plans {
		if err := store.Apply(p); err != nil {
			msg := ext.Tidy(err.Error())
			if len(changed) > 0 {
				msg += " (already applied: " + strings.Join(changed, ", ") + ")"
			}
			e.Message = msg
			return
		}
		for _, c := range p.Changes {
			changed = append(changed, c.Module)
		}
	}
	list := strings.Join(changed, ", ")
	if r.update {
		e.Message = "Updated " + list + "; :reload runs the new code"
		return
	}
	e.Message = "Installed " + list + "; require " + changed[0] + " in init.scm, then :reload"
}
