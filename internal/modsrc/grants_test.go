package modsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/grant"
)

func netMod(name, clauses string) File {
	return File{Module: name, Data: []byte("(define-module " + name + " " + clauses + ")\n(define x 1)\n")}
}

const asksDatamuse = `(requires (net (allow "api.datamuse.com") (methods get)))`

func TestInstallingRecordsTheGrantThePersonApproved(t *testing.T) {
	s := newStore(t)
	p := mustPlanAdd(t, s, repo(commitA, netMod("words", asksDatamuse), mod("plain", "1")), "o/r")
	review := strings.Join(Review([]*Plan{p}), "\n")
	for _, want := range []string{"NETWORK: can reach api.datamuse.com (GET)", "could send text to these hosts"} {
		if !strings.Contains(review, want) {
			t.Errorf("the review page lacks %q:\n%s", want, review)
		}
	}
	if strings.Count(review, "NETWORK:") != 1 {
		t.Errorf("only the module that asks should show a network line:\n%s", review)
	}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	lock, err := ReadLock(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := lock.Find("words")
	if e.Net.String() != `(net (allow "api.datamuse.com") (methods get))` {
		t.Fatalf("lock grant %q", e.Net.String())
	}
	if plain, _ := lock.Find("plain"); !plain.Net.None() {
		t.Fatalf("a module that asks for nothing was recorded with %v", plain.Net)
	}
	text, _ := os.ReadFile(filepath.Join(s.Dir, LockName))
	if !strings.Contains(string(text), `(net (allow "api.datamuse.com") (methods get))`) {
		t.Fatalf("the lockfile does not show the grant:\n%s", text)
	}
}

func TestAnUpdateThatAsksForMoreIsMarked(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, netMod("words", asksDatamuse)), "o/r@v1")); err != nil {
		t.Fatal(err)
	}
	more := netMod("words", `(requires (net (allow "api.datamuse.com" "evil.example.com") (methods get post)))`)
	f := &fakeRepo{refs: map[string]string{"o/r@v1": commitB}, commits: map[string][]File{"o/r@" + commitB: {more}}}
	plans, err := s.PlanUpdate(ctx, f, nil)
	if err != nil || len(plans) != 1 {
		t.Fatalf("%v %v", plans, err)
	}
	review := strings.Join(Review(plans), "\n")
	if !strings.Contains(review, "NETWORK ACCESS GROWS (new hosts: evil.example.com; new methods: POST)") {
		t.Fatalf("the page does not mark the change:\n%s", review)
	}
	// Reading and then applying records the new grant.
	if err := s.Apply(plans[0]); err != nil {
		t.Fatal(err)
	}
	lock, _ := ReadLock(s.Dir)
	if e, _ := lock.Find("words"); !strings.Contains(e.Net.String(), "evil.example.com") {
		t.Fatalf("lock grant %q", e.Net.String())
	}
}

func TestAnUpdateThatAsksForLessIsNotMarkedAsGrowing(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, netMod("words", asksDatamuse)), "o/r@v1")); err != nil {
		t.Fatal(err)
	}
	f := &fakeRepo{refs: map[string]string{"o/r@v1": commitB}, commits: map[string][]File{"o/r@" + commitB: {mod("words", "2")}}}
	plans, err := s.PlanUpdate(ctx, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	review := strings.Join(Review(plans), "\n")
	if strings.Contains(review, "GROWS") || !strings.Contains(review, "no longer asks for network access") {
		t.Fatalf("page:\n%s", review)
	}
}

func TestAHeaderTheEditorCannotReadBlocksTheInstall(t *testing.T) {
	s := newStore(t)
	p := mustPlanAdd(t, s, repo(commitA, netMod("words", `(requires (net (allow "*.example.com")))`)), "o/r")
	if len(p.Blocked()) != 1 || !strings.Contains(p.Blocked()[0].Blocked, "wildcard") {
		t.Fatalf("plan %+v", p.Changes)
	}
	if err := s.Apply(p); err == nil {
		t.Fatal("a blocked plan was applied")
	}
}

func TestTheLockfileWithoutAGrantStillReads(t *testing.T) {
	lock, err := ParseLock(`(modules (alpha (source "o/r") (commit "` + commitA + `") (sha256 "` + strings.Repeat("a", 64) + `")))`)
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := lock.Find("alpha"); !e.Net.None() {
		t.Fatalf("%+v", e)
	}
	if _, err := ParseLock(`(modules (alpha (source "o/r") (commit "` + commitA + `") (sha256 "` + strings.Repeat("a", 64) + `") (net (allow "*"))))`); err == nil {
		t.Fatal("a wildcard grant in the lockfile was accepted")
	}
}

func TestGrantsFollowWhatWasApproved(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, netMod("words", asksDatamuse)), "o/r")); err != nil {
		t.Fatal(err)
	}
	asked := func(header string) grant.Grant {
		g, err := grant.FromModule("(define-module m " + header + ")")
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	decide := Grants(s.Dir)

	if g, why := decide("words", asked(asksDatamuse)); g.None() || why != "" {
		t.Fatalf("what was approved: %v %q", g, why)
	}
	if g, why := decide("words", asked(`(requires (net (allow "api.datamuse.com") (methods get head)))`)); !g.None() || !strings.Contains(why, "approved can reach api.datamuse.com (GET)") ||
		!strings.Contains(why, "run :module-update to review it") {
		t.Fatalf("a wider ask got %v %q", g, why)
	}
	if g, why := decide("words", asked(`(requires (net (allow "other.example.com") (methods get)))`)); !g.None() || why == "" {
		t.Fatalf("another host got %v %q", g, why)
	}
	// A module the person wrote is not in the lock.
	if g, why := decide("mine", asked(`(requires (net (allow "anything.example.com")))`)); g.None() || why != "" {
		t.Fatalf("a local module got %v %q", g, why)
	}
}

func TestAModuleThatWasInstalledWithoutAGrantCannotGrowOneOnDisk(t *testing.T) {
	s := newStore(t)
	if err := s.Apply(mustPlanAdd(t, s, repo(commitA, mod("plain", "1")), "o/r")); err != nil {
		t.Fatal(err)
	}
	g, _ := grant.FromModule("(define-module plain " + asksDatamuse + ")")
	if got, why := Grants(s.Dir)("plain", g); !got.None() || why == "" {
		t.Fatalf("an edit added a grant nobody approved: %v %q", got, why)
	}
}

func TestAnUnreadableLockfileGrantsNothing(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir, LockName), []byte("(modules (broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := grant.FromModule("(define-module m " + asksDatamuse + ")")
	if got, why := Grants(s.Dir)("m", g); !got.None() || !strings.Contains(why, "could not be read") {
		t.Fatalf("%v %q", got, why)
	}
}
