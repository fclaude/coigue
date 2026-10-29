package modsrc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// fakeRepo is a Fetcher over in-memory repositories.
type fakeRepo struct {
	refs    map[string]string // "owner/repo@ref" (ref may be empty) -> commit
	commits map[string][]File // "owner/repo@commit" -> modules
	fetches []string
}

func (f *fakeRepo) Resolve(_ context.Context, src Source) (string, error) {
	if IsCommit(src.Ref) {
		return src.Ref, nil
	}
	c, ok := f.refs[src.String()]
	if !ok {
		return "", fmt.Errorf("%s: not found on GitHub", src)
	}
	return c, nil
}

func (f *fakeRepo) Fetch(_ context.Context, src Source, commit string) ([]File, error) {
	f.fetches = append(f.fetches, src.Slug()+"@"+commit)
	files, ok := f.commits[src.Slug()+"@"+commit]
	if !ok {
		return nil, fmt.Errorf("%s: not found on GitHub", src.Slug())
	}
	return files, nil
}

func mod(name, body string) File {
	return File{Module: name, Data: []byte("(define-module " + name + ")\n" + body)}
}

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: t.TempDir()}
}

func (s Store) put(t *testing.T, module, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(s.Dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path(module), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s Store) text(t *testing.T, module string) string {
	t.Helper()
	data, err := os.ReadFile(s.path(module))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(data)
}

func (s Store) exists(module string) bool {
	_, err := os.Lstat(s.path(module))
	return err == nil
}

func repo(commit string, files ...File) *fakeRepo {
	return &fakeRepo{
		refs:    map[string]string{"o/r": commit, "o/r@v1": commit},
		commits: map[string][]File{"o/r@" + commit: files},
	}
}

var ctx = context.Background()

func mustPlanAdd(t *testing.T, s Store, f Fetcher, spec string, names ...string) *Plan {
	t.Helper()
	src, err := ParseSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PlanAdd(ctx, f, src, names)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddInstallsFilesAndRecordsTheLock(t *testing.T) {
	s := newStore(t)
	f := repo(commitA, mod("alpha", "1"), mod("beta", "2"))
	p := mustPlanAdd(t, s, f, "o/r@v1")
	if len(p.Changes) != 2 || p.Changes[0].Kind != Add || len(p.Blocked()) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	if s.exists("alpha") {
		t.Fatal("planning wrote a file")
	}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if got := s.text(t, "alpha"); got != string(mod("alpha", "1").Data) {
		t.Fatalf("alpha = %q", got)
	}
	lock, err := ReadLock(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := lock.Find("beta")
	want := Entry{Module: "beta", Source: "o/r", Ref: "v1", Commit: commitA, SHA256: mod("beta", "2").Sum()}
	if !ok || !reflect.DeepEqual(e, want) {
		t.Fatalf("entry = %+v; want %+v", e, want)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].State != StateOK || list[1].State != StateOK {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestAddCanTakeSomeModulesAndNamesTheOnesThatExist(t *testing.T) {
	s := newStore(t)
	f := repo(commitA, mod("alpha", ""), mod("beta", ""))
	p := mustPlanAdd(t, s, f, "o/r", "beta", "beta")
	if len(p.Changes) != 1 || p.Changes[0].Module != "beta" {
		t.Fatalf("plan = %+v", p.Changes)
	}
	src, _ := ParseSource("o/r")
	if _, err := s.PlanAdd(ctx, f, src, []string{"gamma"}); err == nil ||
		!strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("got %v; want the available names", err)
	}
}

func TestAddOfARepositoryWithoutModulesSaysSo(t *testing.T) {
	s := newStore(t)
	src, _ := ParseSource("o/r")
	if _, err := s.PlanAdd(ctx, repo(commitA), src, nil); err == nil || !strings.Contains(err.Error(), "no modules") {
		t.Fatalf("got %v", err)
	}
}

func TestAFileThatCannotBeAModuleIsBlocked(t *testing.T) {
	s := newStore(t)
	f := repo(commitA,
		File{Module: "wrong", Data: []byte("(define-module other)")},
		File{Module: "bare", Data: []byte("(display 1)")},
		File{Module: "broken", Data: []byte("(define-module broken")},
		File{Module: "empty", Data: nil},
	)
	p := mustPlanAdd(t, s, f, "o/r")
	if len(p.Blocked()) != 4 {
		t.Fatalf("blocked = %+v", p.Blocked())
	}
	if err := s.Apply(p); err == nil {
		t.Fatal("a blocked plan applied")
	}
	if s.exists("wrong") {
		t.Fatal("a file was written")
	}
}

func TestAddNeverCoversWhatIsNotItsOwn(t *testing.T) {
	s := newStore(t)
	s.put(t, "mine", "(define-module mine)\n;; written by hand\n")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("/nonexistent", s.path("linked")); err != nil {
			t.Fatal(err)
		}
	}
	f := repo(commitA, mod("mine", "from the repo"), mod("linked", ""), mod("fresh", ""))
	p := mustPlanAdd(t, s, f, "o/r")
	reasons := map[string]string{}
	for _, c := range p.Blocked() {
		reasons[c.Module] = c.Blocked
	}
	if !strings.Contains(reasons["mine"], "not installed from a repository") {
		t.Errorf("mine: %q", reasons["mine"])
	}
	if runtime.GOOS != "windows" && !strings.Contains(reasons["linked"], "not a regular file") {
		t.Errorf("linked: %q", reasons["linked"])
	}
	if err := s.Apply(p); err == nil {
		t.Fatal("applied")
	}
	if s.exists("fresh") {
		t.Fatal("the plan is all or nothing, but fresh was written")
	}
	if got := s.text(t, "mine"); !strings.Contains(got, "written by hand") {
		t.Fatalf("mine was overwritten: %q", got)
	}
}

func TestAddRefusesAModuleInstalledFromElsewhereOrEdited(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", "1"), mod("beta", "1")), "o/r")); err != nil {
		t.Fatal(err)
	}
	// alpha comes from another repository now.
	other := &fakeRepo{
		refs:    map[string]string{"x/y": commitB},
		commits: map[string][]File{"x/y@" + commitB: {mod("alpha", "x")}},
	}
	p := mustPlanAdd(t, s, other, "x/y")
	if b := p.Blocked(); len(b) != 1 || !strings.Contains(b[0].Blocked, "installed from o/r") {
		t.Fatalf("blocked = %+v", b)
	}
	// beta was edited.
	s.put(t, "beta", string(mod("beta", "1").Data)+";; my change\n")
	p = mustPlanAdd(t, s, repo(commitB, mod("beta", "2")), "o/r")
	if b := p.Blocked(); len(b) != 1 || !strings.Contains(b[0].Blocked, "edited") {
		t.Fatalf("blocked = %+v", b)
	}
}

func TestApplyRefusesAFileThatChangedAfterThePlan(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", "1")), "o/r")); err != nil {
		t.Fatal(err)
	}
	plans, err := s.PlanUpdate(ctx, repo(commitB, mod("alpha", "2")), nil)
	if err != nil || len(plans) != 1 {
		t.Fatalf("%v, %v", plans, err)
	}
	s.put(t, "alpha", ";; edited after the review\n")
	if err := s.Apply(plans[0]); err == nil || !strings.Contains(err.Error(), "changed since the plan") {
		t.Fatalf("got %v", err)
	}
	if got := s.text(t, "alpha"); got != ";; edited after the review\n" {
		t.Fatalf("alpha = %q", got)
	}

	// A file that appears after the plan of an add is refused too.
	s2 := newStore(t)
	p := mustPlanAdd(t, s2, repo(commitA, mod("zeta", "")), "o/r")
	s2.put(t, "zeta", "mine")
	if err := s2.Apply(p); err == nil {
		t.Fatal("overwrote a file that appeared")
	}
	if got := s2.text(t, "zeta"); got != "mine" {
		t.Fatalf("zeta = %q", got)
	}
}

func TestAFailedApplyPutsEverythingBack(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write")
	}
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", "1")), "o/r")); err != nil {
		t.Fatal(err)
	}
	before := s.text(t, "alpha")
	lockBefore, _ := os.ReadFile(filepath.Join(s.Dir, LockName))

	f := repo(commitB, mod("alpha", "2"), mod("beta", "new"))
	plans, err := s.PlanUpdate(ctx, f, []string{"alpha"})
	if err != nil || len(plans) != 1 {
		t.Fatalf("%v, %v", plans, err)
	}
	p := mustPlanAdd(t, s, f, "o/r")
	if len(p.Changes) != 2 {
		t.Fatalf("plan = %+v", p.Changes)
	}
	// The module files can be written but the lockfile cannot: the directory
	// that holds the lock is read-only, modules/ is not.
	if err := os.Chmod(s.Dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.Dir, 0o755)
	if err := s.Apply(p); err == nil {
		t.Fatal("want an error from the lockfile write")
	}
	os.Chmod(s.Dir, 0o755)
	if got := s.text(t, "alpha"); got != before {
		t.Errorf("alpha = %q; want it put back to %q", got, before)
	}
	if s.exists("beta") {
		t.Error("beta was left behind")
	}
	if lockNow, _ := os.ReadFile(filepath.Join(s.Dir, LockName)); string(lockNow) != string(lockBefore) {
		t.Error("the lockfile changed")
	}
}

func TestUpdateMovesToTheNewCommitAndReusesTheRef(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", "1"), mod("beta", "1")), "o/r@v1")); err != nil {
		t.Fatal(err)
	}
	f := &fakeRepo{
		refs: map[string]string{"o/r@v1": commitB},
		commits: map[string][]File{"o/r@" + commitB: {
			mod("alpha", "2"), mod("beta", "1"), // beta is unchanged: only the lock moves
		}},
	}
	plans, err := s.PlanUpdate(ctx, f, nil)
	if err != nil || len(plans) != 1 {
		t.Fatalf("%v, %v", plans, err)
	}
	kinds := map[string]Kind{}
	for _, c := range plans[0].Changes {
		kinds[c.Module] = c.Kind
	}
	if kinds["alpha"] != Update || kinds["beta"] != Same {
		t.Fatalf("kinds = %v", kinds)
	}
	if len(f.fetches) != 1 {
		t.Fatalf("fetches = %v; one repository is fetched once", f.fetches)
	}
	if err := s.Apply(plans[0]); err != nil {
		t.Fatal(err)
	}
	lock, _ := ReadLock(s.Dir)
	for _, e := range lock.Entries {
		if e.Commit != commitB || e.Ref != "v1" {
			t.Errorf("entry = %+v", e)
		}
	}
	if got := s.text(t, "alpha"); got != string(mod("alpha", "2").Data) {
		t.Errorf("alpha = %q", got)
	}
}

func TestUpdateOfWhatIsCurrentPlansNothingAndDoesNotFetch(t *testing.T) {
	s := newStore(t)
	f := repo(commitA, mod("alpha", ""))
	if err := s.Apply(mustPlanAdd(t, s, f, "o/r")); err != nil {
		t.Fatal(err)
	}
	f.fetches = nil
	plans, err := s.PlanUpdate(ctx, f, nil)
	if err != nil || len(plans) != 0 || len(f.fetches) != 0 {
		t.Fatalf("plans %v, err %v, fetches %v", plans, err, f.fetches)
	}
}

func TestUpdateNeedsInstalledModules(t *testing.T) {
	s := newStore(t)
	if _, err := s.PlanUpdate(ctx, &fakeRepo{}, nil); err == nil {
		t.Error("nothing is installed")
	}
	s.put(t, "mine", "x")
	if _, err := s.PlanUpdate(ctx, &fakeRepo{}, []string{"mine"}); err == nil || !strings.Contains(err.Error(), "not installed from a repository") {
		t.Errorf("got %v", err)
	}
}

func TestUpdateKeepsAModuleTheRepositoryDropped(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", ""), mod("beta", "")), "o/r")); err != nil {
		t.Fatal(err)
	}
	plans, err := s.PlanUpdate(ctx, repo(commitB, mod("alpha", "new")), nil)
	if err != nil || len(plans) != 1 {
		t.Fatalf("%v, %v", plans, err)
	}
	if len(plans[0].Notes) != 1 || !strings.Contains(plans[0].Notes[0], "beta is no longer in o/r") {
		t.Fatalf("notes = %v", plans[0].Notes)
	}
	if err := s.Apply(plans[0]); err != nil || !s.exists("beta") {
		t.Fatalf("beta must stay: %v", err)
	}
}

func TestPlanNotesAModuleThatNeedsOneThatIsNotInstalled(t *testing.T) {
	s := newStore(t)
	s.put(t, "have", "(define-module have)")
	files := []File{
		{Module: "a", Data: []byte("(define-module a)\n(require b)\n(require 'have)\n(require (quote c) (prefix c:))\n(require a-tool)")},
		mod("b", ""),
	}
	p := mustPlanAdd(t, s, repo(commitA, files...), "o/r", "a")
	got := strings.Join(p.Notes, "; ")
	for _, want := range []string{"a requires b", "a requires c", "a requires a-tool"} {
		if !strings.Contains(got, want) {
			t.Errorf("notes %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "have") {
		t.Errorf("notes %q mention an installed module", got)
	}
	// Taking b along removes the note about b.
	p = mustPlanAdd(t, s, repo(commitA, files...), "o/r", "a", "b")
	if strings.Contains(strings.Join(p.Notes, ";"), "requires b") {
		t.Errorf("notes = %v", p.Notes)
	}
}

func TestRemove(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", ""), mod("beta", ""), mod("gamma", "")), "o/r")); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("alpha"); err != nil || s.exists("alpha") {
		t.Fatalf("remove: %v", err)
	}
	if lock, _ := ReadLock(s.Dir); len(lock.Entries) != 2 {
		t.Fatalf("lock = %+v", lock.Entries)
	}
	// An edited file is not thrown away.
	s.put(t, "beta", "edited")
	if err := s.Remove("beta"); err == nil || !strings.Contains(err.Error(), "edited") || !s.exists("beta") {
		t.Fatalf("got %v", err)
	}
	// A file already gone leaves only its entry to drop.
	if err := os.Remove(s.path("gamma")); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("gamma"); err != nil {
		t.Fatal(err)
	}
	// What no repository installed is not ours to delete.
	s.put(t, "mine", "x")
	if err := s.Remove("mine"); err == nil || !s.exists("mine") {
		t.Fatalf("got %v", err)
	}
	if err := s.Remove("../etc/passwd"); err == nil {
		t.Fatal("a path is not a module name")
	}
}

func TestListShowsLocalAndModifiedAndMissing(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("alpha", ""), mod("beta", ""), mod("gamma", "")), "o/r")); err != nil {
		t.Fatal(err)
	}
	s.put(t, "beta", "edited")
	os.Remove(s.path("gamma"))
	s.put(t, "mine", "x")
	s.put(t, "Not_A_Module", "x")
	got := map[string]State{}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range list {
		got[st.Module] = st.State
	}
	want := map[string]State{"alpha": StateOK, "beta": StateModified, "gamma": StateMissing, "mine": StateLocal}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

func TestSyncRestoresWhatIsMissingAndChecksTheHash(t *testing.T) {
	s := newStore(t)
	f := repo(commitA, mod("alpha", "1"), mod("beta", "1"), mod("gamma", "1"), mod("delta", "1"))
	if err := s.Apply(mustPlanAdd(t, s, f, "o/r")); err != nil {
		t.Fatal(err)
	}
	os.Remove(s.path("alpha"))
	os.Remove(s.path("gamma"))
	os.Remove(s.path("delta"))
	s.put(t, "beta", "edited")

	// The repository now serves different bytes for gamma, and no delta.
	f.commits["o/r@"+commitA] = []File{mod("alpha", "1"), mod("beta", "1"), mod("gamma", "TAMPERED")}
	report, err := s.Sync(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(report.Restored) != "[alpha]" || fmt.Sprint(report.Modified) != "[beta]" {
		t.Errorf("report = %+v", report)
	}
	if err := report.Failed["gamma"]; err == nil || !strings.Contains(err.Error(), "does not match the lock") {
		t.Errorf("gamma: %v", err)
	}
	if err := report.Failed["delta"]; err == nil || !strings.Contains(err.Error(), "no longer in") {
		t.Errorf("delta: %v", err)
	}
	if s.exists("gamma") || s.exists("delta") {
		t.Error("a file that failed its check was written")
	}
	if got := s.text(t, "beta"); got != "edited" {
		t.Errorf("beta = %q; sync must leave an edited file alone", got)
	}
	if got := s.text(t, "alpha"); got != string(mod("alpha", "1").Data) {
		t.Errorf("alpha = %q", got)
	}
}

func TestSyncFetchesEachCommitOnceAndReportsAFailedFetch(t *testing.T) {
	s := newStore(t)
	f := repo(commitA, mod("alpha", ""), mod("beta", ""))
	if err := s.Apply(mustPlanAdd(t, s, f, "o/r")); err != nil {
		t.Fatal(err)
	}
	os.Remove(s.path("alpha"))
	os.Remove(s.path("beta"))
	f.fetches = nil
	report, err := s.Sync(ctx, f)
	if err != nil || len(report.Restored) != 2 || len(f.fetches) != 1 {
		t.Fatalf("report %+v, err %v, fetches %v", report, err, f.fetches)
	}

	os.Remove(s.path("alpha"))
	os.Remove(s.path("beta"))
	delete(f.commits, "o/r@"+commitA)
	f.fetches = nil
	report, err = s.Sync(ctx, f)
	if err != nil || len(report.Failed) != 2 || len(f.fetches) != 1 {
		t.Fatalf("report %+v, err %v, fetches %v", report, err, f.fetches)
	}
}

func TestStoreErrorsFromTheLockfileAreReported(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir, LockName), []byte("(modules (a"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, _ := ParseSource("o/r")
	for name, err := range map[string]error{
		"list":   second(s.List()),
		"add":    second(s.PlanAdd(ctx, repo(commitA, mod("a", "")), src, nil)),
		"update": second(s.PlanUpdate(ctx, &fakeRepo{}, nil)),
		"remove": s.Remove("a"),
		"sync":   second(s.Sync(ctx, &fakeRepo{})),
	} {
		if err == nil || !strings.Contains(err.Error(), LockName) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func second[T any](_ T, err error) error { return err }
