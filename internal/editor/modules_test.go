package editor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/file"
	"github.com/fclaude/coigue/internal/key"
	"github.com/fclaude/coigue/internal/modsrc"
)

const (
	commitOne = "0123456789abcdef0123456789abcdef01234567"
	commitTwo = "fedcba9876543210fedcba9876543210fedcba98"
)

// stubRepo serves one repository. While gate is set, resolving waits for it.
type stubRepo struct {
	commit string
	files  []modsrc.File
	gate   chan struct{}
}

func (s *stubRepo) Resolve(ctx context.Context, _ modsrc.Source) (string, error) {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return s.commit, nil
}

func (s *stubRepo) Fetch(context.Context, modsrc.Source, string) ([]modsrc.File, error) {
	return s.files, nil
}

func moduleFile(name, body string) modsrc.File {
	return modsrc.File{Module: name, Data: []byte("(define-module " + name + " (export hello))\n" + body)}
}

// moduleEditor is an editor whose configuration directory is empty.
func moduleEditor(t *testing.T, repo modsrc.Fetcher) (*Editor, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "coigue")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := newEditor(t, &file.File{Path: filepath.Join(t.TempDir(), "doc.txt")}, "text")
	e.initPath = filepath.Join(dir, "init.scm")
	e.moduleFetcher = repo
	return e, dir
}

// finishJob drives Tick until the background fetch has ended.
func finishJob(t *testing.T, e *Editor) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.moduleJob != nil {
		if time.Now().After(deadline) {
			t.Fatal("the module job did not finish")
		}
		e.Tick(22)
		time.Sleep(2 * time.Millisecond)
	}
}

func reviewText(e *Editor) string {
	if e.Window == nil {
		return "<no review>"
	}
	return strings.Join(e.Window.Lines, "\n")
}

func TestModuleAddShowsTheCodeThenInstallsOnYAndTheLoaderUsesIt(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", "(define (hello) \"hi\")\n")}}
	e, dir := moduleEditor(t, repo)

	command(e, "module-add fclaude/tools@v1")
	if e.moduleJob == nil || !strings.Contains(e.Message, "Fetching fclaude/tools@v1") {
		t.Fatalf("job %v, message %q", e.moduleJob, e.Message)
	}
	finishJob(t, e)
	if e.Window == nil || !e.Window.CanAccept() {
		t.Fatalf("no review to answer: %q", e.Message)
	}
	text := reviewText(e)
	for _, want := range []string{"fclaude/tools@v1 at commit " + commitOne, "add           alpha", "+ (define-module alpha (export hello))", `+ (define (hello) "hi")`} {
		if !strings.Contains(text, want) {
			t.Errorf("the review lacks %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "modules", "alpha.scm")); err == nil {
		t.Fatal("a file was written before the answer")
	}

	// While the page is open, nothing reaches the editor behind it.
	before := e.Buffer.Text()
	send(e, "i:x")
	press(e, key.F(1))
	if e.Mode != ModeNormal || e.Buffer.Text() != before || e.Help != nil || e.Window == nil {
		t.Fatalf("a key got through: mode %v help %v review %v", e.Mode, e.Help, e.Window)
	}

	send(e, "y")
	if e.Window != nil || e.Message != "Installed alpha; require alpha in init.scm, then :reload" {
		t.Fatalf("review %v, message %q", e.Window, e.Message)
	}
	if got := read(t, filepath.Join(dir, "modules", "alpha.scm")); got != string(repo.files[0].Data) {
		t.Fatalf("alpha.scm = %q", got)
	}
	lock := read(t, filepath.Join(dir, modsrc.LockName))
	if !strings.Contains(lock, `(alpha (source "fclaude/tools") (ref "v1") (commit "`+commitOne+`")`) {
		t.Fatalf("lock = %s", lock)
	}

	// What was installed is an ordinary module the real loader can require.
	write(t, e.initPath, "(require alpha (prefix alpha:))\n(define-command \"greet\" (lambda () (message (alpha:hello))) \"Greet\")\n")
	command(e, "reload")
	if !strings.HasPrefix(e.Message, "Reloaded init.scm") {
		t.Fatalf("reload: %q", e.Message)
	}
	command(e, "greet")
	if e.Message != "hi" {
		t.Fatalf("greet: %q", e.Message)
	}
}

func TestModuleAddCancelWritesNothing(t *testing.T) {
	for _, answer := range []key.Key{key.Char('n'), key.Press(key.Esc), key.Char('q')} {
		repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", "")}}
		e, dir := moduleEditor(t, repo)
		command(e, "module-add o/r")
		finishJob(t, e)
		e.Key(answer, 22)
		if e.Window != nil || e.Message != "Cancelled; nothing was changed" {
			t.Errorf("review %v, message %q", e.Window, e.Message)
		}
		if _, err := os.Stat(filepath.Join(dir, "modules")); err == nil {
			t.Error("modules/ was created")
		}
		if _, err := os.Stat(filepath.Join(dir, modsrc.LockName)); err == nil {
			t.Error("a lockfile was written")
		}
	}
}

func TestAReviewWithABlockedChangeCannotBeApplied(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", "from the repository")}}
	e, dir := moduleEditor(t, repo)
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "modules", "alpha.scm")
	write(t, mine, "(define-module alpha)\n;; mine\n")
	command(e, "module-add o/r")
	finishJob(t, e)
	if e.Window == nil || e.Window.CanAccept() || !strings.Contains(reviewText(e), "CANNOT APPLY  alpha") {
		t.Fatalf("review: %s", reviewText(e))
	}
	send(e, "y")
	if e.Window == nil {
		t.Fatal("y closed a review that cannot be applied")
	}
	press(e, key.Esc)
	if got := read(t, mine); !strings.Contains(got, ";; mine") {
		t.Fatalf("alpha.scm = %q", got)
	}
}

func TestModuleListIsAPageWithNothingToAnswer(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", "")}}
	e, dir := moduleEditor(t, repo)
	command(e, "module-list")
	if e.Window != nil || !strings.HasPrefix(e.Message, "No modules in ") {
		t.Fatalf("empty: review %v message %q", e.Window, e.Message)
	}
	command(e, "module-add o/r")
	finishJob(t, e)
	send(e, "y")
	write(t, filepath.Join(dir, "modules", "mine.scm"), "x")
	command(e, "module-list")
	text := reviewText(e)
	if e.Window == nil || e.Window.Kind != WindowText ||
		!strings.Contains(text, "alpha") || !strings.Contains(text, "ok") || !strings.Contains(text, "o/r "+commitOne[:7]) ||
		!strings.Contains(text, "mine") || !strings.Contains(text, "local") {
		t.Fatalf("list:\n%s", text)
	}
	send(e, "y") // nothing to apply
	if e.Window == nil {
		t.Fatal("y closed a page with nothing to apply")
	}
	press(e, key.Esc)
	if e.Window != nil || e.Message != "" && strings.Contains(e.Message, "Cancelled") {
		t.Fatalf("review %v, message %q", e.Window, e.Message)
	}
}

func TestModuleUpdateShowsADiffAndAppliesIt(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", "(define (hello) 1)\n")}}
	e, dir := moduleEditor(t, repo)
	command(e, "module-add o/r")
	finishJob(t, e)
	send(e, "y")

	command(e, "module-update")
	finishJob(t, e)
	if e.Window != nil || e.Message != "Installed modules are up to date" {
		t.Fatalf("review %v, message %q", e.Window, e.Message)
	}

	repo.commit = commitTwo
	repo.files = []modsrc.File{moduleFile("alpha", "(define (hello) 2)\n")}
	command(e, "module-update")
	finishJob(t, e)
	text := reviewText(e)
	if e.Window == nil || !strings.Contains(e.Window.Title, "Update") ||
		!strings.Contains(text, "- (define (hello) 1)") || !strings.Contains(text, "+ (define (hello) 2)") {
		t.Fatalf("review:\n%s", text)
	}
	send(e, "y")
	if e.Message != "Updated alpha; :reload runs the new code" || !strings.Contains(read(t, filepath.Join(dir, "modules", "alpha.scm")), "hello) 2") {
		t.Fatalf("message %q", e.Message)
	}
}

func TestModuleRemoveAndSync(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", ""), moduleFile("beta", "")}}
	e, dir := moduleEditor(t, repo)
	command(e, "module-add o/r")
	finishJob(t, e)
	send(e, "y")

	command(e, "module-remove alpha")
	if !strings.HasPrefix(e.Message, "Removed alpha") {
		t.Fatalf("message %q", e.Message)
	}
	if _, err := os.Stat(filepath.Join(dir, "modules", "alpha.scm")); err == nil {
		t.Fatal("alpha.scm is still there")
	}

	if err := os.Remove(filepath.Join(dir, "modules", "beta.scm")); err != nil {
		t.Fatal(err)
	}
	command(e, "module-sync")
	finishJob(t, e)
	if e.Message != "Modules: restored beta" {
		t.Fatalf("sync: %q", e.Message)
	}
	command(e, "module-sync")
	finishJob(t, e)
	if e.Message != "All modules are in place" {
		t.Fatalf("second sync: %q", e.Message)
	}
}

func TestModuleCommandsExplainTheirMistakes(t *testing.T) {
	e, _ := moduleEditor(t, &stubRepo{})
	for line, want := range map[string]string{
		"module-add":                "Usage: :module-add",
		"module-add not-a-repo":     "is not a GitHub repository",
		"module-add o/r@..":         "not a usable tag",
		"module-remove":             "Usage: :module-remove",
		"module-remove a b":         "Usage: :module-remove",
		"module-remove nothing":     "was not installed from a repository",
		"module-remove ../../etc/x": "not a module name",
	} {
		command(e, line)
		if !strings.Contains(e.Message, want) || e.moduleJob != nil {
			t.Errorf(":%s -> %q; want %q", line, e.Message, want)
		}
	}
	e.initPath = ""
	command(e, "module-add o/r")
	if !strings.Contains(e.Message, "no place for them") || e.moduleJob != nil {
		t.Errorf("without a configuration directory: %q", e.Message)
	}
}

func TestOneModuleJobAtATime(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{moduleFile("alpha", "")}, gate: make(chan struct{})}
	e, _ := moduleEditor(t, repo)
	command(e, "module-add o/r")
	command(e, "module-add o/r")
	if e.Message != "A module command is still running" {
		t.Fatalf("message %q", e.Message)
	}
	close(repo.gate)
	finishJob(t, e)
	if e.Window == nil {
		t.Fatalf("the first job's page is missing: %q", e.Message)
	}
}

func TestAnUnreachableRepositoryIsAMessageNotAPage(t *testing.T) {
	e, _ := moduleEditor(t, failing{})
	command(e, "module-add o/r")
	finishJob(t, e)
	if e.Window != nil || !strings.Contains(e.Message, "not found on GitHub") {
		t.Fatalf("review %v, message %q", e.Window, e.Message)
	}
}

type failing struct{}

func (failing) Resolve(context.Context, modsrc.Source) (string, error) {
	return "", errors.New("o/r: not found on GitHub")
}

func (failing) Fetch(context.Context, modsrc.Source, string) ([]modsrc.File, error) {
	return nil, nil
}

func TestModuleReviewScrollsAndStaysInside(t *testing.T) {
	r := &Window{Lines: make([]string, 50)}
	r.Scroll = 1 << 30
	r.Fit(50, 10)
	if r.Scroll != 40 {
		t.Errorf("scroll = %d; want 40", r.Scroll)
	}
	r.Scroll = -3
	r.Fit(50, 10)
	if r.Scroll != 0 {
		t.Errorf("scroll = %d; want 0", r.Scroll)
	}
	r.Scroll = 5
	r.Fit(3, 10)
	if r.Scroll != 0 {
		t.Errorf("a short page scrolled to %d", r.Scroll)
	}
}

func TestModuleCommandsCompleteOnlyOnceThereIsAnInitFile(t *testing.T) {
	e, _ := moduleEditor(t, &stubRepo{})
	names := []string{"module-add", "module-list", "module-remove", "module-sync", "module-update"}
	for _, name := range names {
		spec, ok := e.commands.lookup(name)
		if !ok || !spec.unlisted {
			t.Errorf("%s: found %v, unlisted %v; it should work but not complete", name, ok, spec != nil && spec.unlisted)
		}
	}
	loadTree(t, e, "", nil)
	for _, name := range names {
		if spec, _ := e.commands.lookup(name); spec.unlisted {
			t.Errorf("%s should complete once init.scm exists", name)
		}
	}
}

func netModuleFile(name, hosts string) modsrc.File {
	return modsrc.File{Module: name, Data: []byte("(define-module " + name + " (export hello) (requires (net (allow " + hosts + ") (methods get))))\n(define (hello) \"hi\")\n")}
}

func TestTheReviewShowsTheNetworkAModuleAsksForAndTheLockLimitsIt(t *testing.T) {
	repo := &stubRepo{commit: commitOne, files: []modsrc.File{netModuleFile("words", `"api.datamuse.com"`)}}
	e, dir := moduleEditor(t, repo)

	command(e, "module-add fclaude/tools@v1")
	finishJob(t, e)
	text := reviewText(e)
	for _, want := range []string{"NETWORK: can reach api.datamuse.com (GET)", "could send text to these hosts"} {
		if !strings.Contains(text, want) {
			t.Errorf("the review lacks %q:\n%s", want, text)
		}
	}
	send(e, "y")
	if lock := read(t, filepath.Join(dir, modsrc.LockName)); !strings.Contains(lock, `(net (allow "api.datamuse.com") (methods get))`) {
		t.Fatalf("lock = %s", lock)
	}

	noteOf := func() string {
		for _, m := range e.extensions.Modules() {
			if m.Name == "words" {
				return strings.Join(m.Notes, "; ")
			}
		}
		return "<module not loaded>"
	}
	write(t, e.initPath, "(require words)\n")
	command(e, "reload")
	if got := noteOf(); got != "" {
		t.Fatalf("what was approved produced a note: %q", got)
	}

	// An edit on disk that asks for another host is not honoured.
	write(t, filepath.Join(dir, "modules", "words.scm"), string(netModuleFile("words", `"api.datamuse.com" "evil.example.com"`).Data))
	command(e, "reload")
	if got := noteOf(); !strings.Contains(got, "which was not granted") || !strings.Contains(got, "modules.lock approved can reach api.datamuse.com (GET)") {
		t.Fatalf("an unapproved ask was not refused: %q", got)
	}
}
