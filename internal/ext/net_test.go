package ext

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fclaude/coigue/internal/grant"
)

// privateOK lets the tests reach httptest servers on 127.0.0.1, which the
// editor never allows. Grants add it too, since a header cannot.
func privateOK(t *testing.T) {
	t.Helper()
	allowPrivateForTests = true
	t.Cleanup(func() { allowPrivateForTests = false })
}

func withPrivate(_ string, asked grant.Grant) (grant.Grant, string) {
	asked.Private = true
	return asked, ""
}

// words answers /words with a fixed list, /post with the method it saw, and
// /hop with a redirect to another server.
func words(t *testing.T, hop string) (*httptest.Server, string) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/hop":
			http.Redirect(w, r, hop, http.StatusFound)
		case "/post":
			fmt.Fprint(w, r.Method)
		case "/rhyme":
			fmt.Fprintf(w, `[{"word":"free","score":2000},{"word":"sea","score":1900}]`)
		default:
			fmt.Fprint(w, "tree,free,sea")
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return srv, u.Host
}

// The shared Scheme: try runs a request and reports what happened as text.
const netHelpers = `
(define (try thunk)
  (guard (e (#t (string-append "err:" (assoc-ref e 'message))))
    (string-append "ok:" (cdr (assoc "body" (thunk))))))
(define-command "fetch"
  (lambda (url)
    (on-done (spawn-detached (lambda () (try (lambda () (http-get url)))))
             (lambda (t) (insert-text (task-wait t))))))
(define-command "post"
  (lambda (url)
    (on-done (spawn-detached (lambda () (try (lambda () (http-post url "x")))))
             (lambda (t) (insert-text (task-wait t))))))
(define-command "now"
  (lambda (url) (insert-text (try (lambda () (http-get url))))))
(define-command "policy"
  (lambda ()
    (on-done (spawn-detached (lambda () (try (lambda () (modules)))))
             (lambda (t) (insert-text (task-wait t))))))
`

func netRuntime(t *testing.T, opts Options, modules map[string]string, init string) *Runtime {
	t.Helper()
	opts.ConfigDir = writeModules(t, modules)
	return loaded(t, opts, init)
}

// fetched runs a command that starts a task and returns what its handler
// inserted.
func fetched(t *testing.T, r *Runtime, command string, args ...string) string {
	t.Helper()
	run(t, r, command, args...)
	evs := events(t, r, 1)
	return deliver(t, r, evs[0]).Buffer.Text()
}

// refused runs a command that starts a task and returns the error its handler
// failed with. A denial cannot be caught by guard, so a task that was refused
// fails and the handler's task-wait raises it.
func refused(t *testing.T, r *Runtime, command string, args ...string) string {
	t.Helper()
	run(t, r, command, args...)
	evs := events(t, r, 1)
	s := session("", 0, 0, nil, false)
	err := r.RunEvent(context.Background(), evs[0], s)
	if err == nil {
		t.Fatalf("%s was not refused; it inserted %q", command, s.Buffer.Text())
	}
	return err.Error()
}

func TestAModuleWithAGrantFetchesInATask(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "` + host + `"))))` + netHelpers,
	}, `(require lex)`)
	if got := fetched(t, r, "lex/fetch", "http://"+host+"/words"); got != "ok:tree,free,sea" {
		t.Fatalf("got %q", got)
	}
}

func TestACommandCannotWaitOnTheNetwork(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "` + host + `"))))` + netHelpers,
	}, `(require lex)`)
	err := r.Run(context.Background(), "lex/now", []string{"http://" + host + "/words"}, session("", 0, 0, nil, false))
	if err == nil || !strings.Contains(err.Error(), "a command cannot use the network while the editor waits") ||
		!strings.Contains(err.Error(), "spawn-detached") {
		t.Fatalf("got %v", err)
	}
}

func TestAModuleWithoutAGrantIsToldWhatToAdd(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"plain.scm": `(define-module plain)` + netHelpers,
	}, `(require plain)`)
	got := refused(t, r, "plain/fetch", "http://"+host+"/words")
	if !strings.Contains(got, "this module has no network access") || !strings.Contains(got, "(requires (net (allow \"host\")))") {
		t.Fatalf("got %q", got)
	}
}

func TestInitScmHasNoNetworkAndIsToldWhere(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := loaded(t, Options{Grants: withPrivate}, netHelpers)
	got := refused(t, r, "fetch", "http://"+host+"/words")
	if !strings.Contains(got, "init.scm cannot use the network") {
		t.Fatalf("got %q", got)
	}
}

func TestEachModuleKeepsItsOwnGrantOnOneInterpreter(t *testing.T) {
	privateOK(t)
	_, a := words(t, "")
	_, b := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"one.scm": `(define-module one (requires (net (allow "` + a + `"))))` + netHelpers,
		"two.scm": `(define-module two (requires (net (allow "` + b + `"))))` + netHelpers,
	}, `(require one) (require two)`)
	for _, c := range []struct{ command, host string }{{"one/fetch", a}, {"two/fetch", b}} {
		if got := fetched(t, r, c.command, "http://"+c.host+"/words"); got != "ok:tree,free,sea" {
			t.Errorf("%s -> %s: got %q", c.command, c.host, got)
		}
	}
	for _, c := range []struct{ command, host string }{{"one/fetch", b}, {"two/fetch", a}} {
		if got := refused(t, r, c.command, "http://"+c.host+"/words"); !strings.Contains(got, "is not a host this module may reach") {
			t.Errorf("%s -> %s: got %q", c.command, c.host, got)
		}
	}
}

func TestARedirectToAHostOutsideTheGrantIsRefused(t *testing.T) {
	privateOK(t)
	other, otherHost := words(t, "")
	_ = other
	_, host := words(t, "http://"+otherHost+"/words")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"lex.scm":  `(define-module lex (requires (net (allow "` + host + `"))))` + netHelpers,
		"wide.scm": `(define-module wide (requires (net (allow "` + host + `" "` + otherHost + `"))))` + netHelpers,
	}, `(require lex) (require wide)`)
	if got := refused(t, r, "lex/fetch", "http://"+host+"/hop"); !strings.Contains(got, "redirected to") ||
		!strings.Contains(got, "is not a host this module may reach") {
		t.Fatalf("a redirect left the grant: %q", got)
	}
	// The module that may reach both follows it, so it is the grant that decided.
	if got := fetched(t, r, "wide/fetch", "http://"+host+"/hop"); got != "ok:tree,free,sea" {
		t.Fatalf("got %q", got)
	}
}

func TestOnlyTheGrantedMethodsAreSent(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"reader.scm": `(define-module reader (requires (net (allow "` + host + `"))))` + netHelpers,
		"writer.scm": `(define-module writer (requires (net (allow "` + host + `") (methods get post))))` + netHelpers,
	}, `(require reader) (require writer)`)
	if got := refused(t, r, "reader/post", "http://"+host+"/post"); !strings.Contains(got, "method POST is not allowed for this module") {
		t.Fatalf("a module granted only to read sent a POST: %q", got)
	}
	if got := fetched(t, r, "writer/post", "http://"+host+"/post"); got != "ok:POST" {
		t.Fatalf("got %q", got)
	}
}

func TestATaskStartedByAHandlerKeepsTheModule(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "` + host + `"))))
(define (try thunk)
  (guard (e (#t (string-append "err:" (assoc-ref e 'message))))
    (string-append "ok:" (cdr (assoc "body" (thunk))))))
(define-command "chain"
  (lambda (url)
    (on-done (spawn-detached (lambda () 1))
             (lambda (t)
               (on-done (spawn-detached (lambda () (try (lambda () (http-get url)))))
                        (lambda (u) (insert-text (task-wait u))))))))`,
	}, `(require lex)`)
	run(t, r, "lex/chain", "http://"+host+"/words")
	first := deliver(t, r, events(t, r, 1)[0])
	r.Adopt(first)
	if got := deliver(t, r, events(t, r, 1)[0]).Buffer.Text(); got != "ok:tree,free,sea" {
		t.Fatalf("got %q", got)
	}
}

func TestTheEditorsOwnLimitHoldsWhateverTheGrantSays(t *testing.T) {
	// Without the test switch the installed policy refuses loopback, so a
	// module that names it still cannot reach it.
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "` + host + `"))))` + netHelpers,
	}, `(require lex)`)
	got := fetched(t, r, "lex/fetch", "http://"+host+"/words")
	if !strings.HasPrefix(got, "err:") {
		t.Fatalf("a module reached a loopback address: %q", got)
	}
}

func TestAGrantTheEditorRefusesLeavesTheModuleWithoutNetworkAndSaysWhy(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	deny := func(string, grant.Grant) (grant.Grant, string) {
		return grant.Grant{}, "modules.lock approved nothing for it; run :module-update to review"
	}
	r := netRuntime(t, Options{Grants: deny}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "` + host + `"))))` + netHelpers,
	}, `(require lex)`)
	got := refused(t, r, "lex/fetch", "http://"+host+"/words")
	if !strings.Contains(got, "network access was not granted") || !strings.Contains(got, "run :module-update to review") {
		t.Fatalf("got %q", got)
	}
	mods := r.Modules()
	if len(mods) != 1 || len(mods[0].Notes) == 0 || !strings.Contains(mods[0].Notes[0], "asks for network access") {
		t.Fatalf("the guide should say the request was not granted: %+v", mods)
	}
}

func TestWithoutAGrantsFunctionAModuleGetsWhatItAsks(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	// Private addresses are not in a header, so with no Grants function the
	// installed policy's rule still stops this; the grant itself was honoured.
	r := netRuntime(t, Options{}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "` + host + `"))))` + netHelpers,
	}, `(require lex)`)
	got := refused(t, r, "lex/fetch", "http://"+host+"/words")
	if strings.Contains(got, "not granted") || strings.Contains(got, "no network access") || !strings.Contains(got, "private or local address") {
		t.Fatalf("the module's own request was not honoured, or the private rule did not apply: %q", got)
	}
}

func TestBroteIntrospectionIsRefusedWithAnExplanation(t *testing.T) {
	r := netRuntime(t, Options{}, map[string]string{
		"lex.scm": `(define-module lex (requires (net (allow "api.example.com"))))` + netHelpers,
	}, `(require lex)`)
	got := refused(t, r, "lex/policy")
	if !strings.Contains(got, "describes the editor's own policy") {
		t.Fatalf("got %q", got)
	}
}

func TestABadRequestsClauseFailsTheModuleWithAnExplanation(t *testing.T) {
	for header, want := range map[string]string{
		`(requires (fs (read "x")))`:                    "the only grant is net",
		`(requires (net (allow "*.example.com")))`:      "wildcard",
		`(requires (net (allow "a.com") (private #t)))`: "not something a module can ask for",
	} {
		dir := writeModules(t, map[string]string{"bad.scm": "(define-module bad " + header + ")"})
		err := New(Options{ConfigDir: dir}).Load(context.Background(), "(require bad)")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", header, err, want)
		}
	}
}

func TestTheUrlHelperIsThereWithoutAGrant(t *testing.T) {
	r := loaded(t, Options{}, `(define-command "enc" (lambda () (insert-text (url-encode "a b&c"))))`)
	if got := run(t, r, "enc").Buffer.Text(); got != "a+b%26c" {
		t.Fatalf("got %q", got)
	}
}

// The example in *The network* with the server's address in place of
// api.datamuse.com, run for real.
func TestTheDocumentedRhymeExampleRuns(t *testing.T) {
	privateOK(t)
	_, host := words(t, "")
	r := netRuntime(t, Options{Grants: withPrivate}, map[string]string{
		"wordsmith.scm": `(define-module wordsmith
  (doc "Rhymes for the word under the cursor.")
  (export rhymes)
  (requires (net (allow "` + host + `") (methods get))))

(define (rhymes word)
  (map (lambda (entry) (cdr (assoc "word" entry)))
       (http-get-json (string-append "http://` + host + `/rhyme?rel_rhy=" (url-encode word)))))

(define-command "rhyme"
  (lambda ()
    (let ((word (word-at-cursor)))
      (on-done (spawn-detached (lambda () (rhymes word)))
               (lambda (task)
                 (message (apply string-append (map (lambda (w) (string-append w " ")) (task-wait task)))))))))`,
	}, `(require wordsmith)`)
	s := session("tree", 0, 1, nil, false)
	if err := r.Run(context.Background(), "wordsmith/rhyme", nil, s); err != nil {
		t.Fatal(err)
	}
	r.Adopt(s)
	done := session("tree", 0, 1, nil, false)
	if err := r.RunEvent(context.Background(), events(t, r, 1)[0], done); err != nil {
		t.Fatal(err)
	}
	if got := done.Message(); got != "free sea " {
		t.Fatalf("message %q", got)
	}
}
