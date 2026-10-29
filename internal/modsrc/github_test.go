package modsrc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub serves the two endpoints the client uses, under /api and /code.
type fakeGitHub struct {
	srv      *httptest.Server
	requests atomic.Int32
	commits  map[string]string // "owner/repo/ref" -> reply body
	archive  func(w http.ResponseWriter, r *http.Request)
	last     atomic.Value // last request path (escaped)
	accept   atomic.Value
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{commits: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/repos/", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.last.Store(r.URL.EscapedPath())
		f.accept.Store(r.Header.Get("Accept"))
		rest := strings.TrimPrefix(r.URL.Path, "/api/repos/")
		owner, rest, _ := strings.Cut(rest, "/")
		repo, rest, _ := strings.Cut(rest, "/")
		ref := strings.TrimPrefix(rest, "commits/")
		body, ok := f.commits[owner+"/"+repo+"/"+ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	})
	mux.HandleFunc("/code/", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		f.last.Store(r.URL.EscapedPath())
		if f.archive == nil {
			http.NotFound(w, r)
			return
		}
		f.archive(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) client() *Client {
	return &Client{API: f.srv.URL + "/api", Codeload: f.srv.URL + "/code"}
}

func TestResolveAsksGitHubForTheCommit(t *testing.T) {
	f := newFakeGitHub(t)
	f.commits["o/r/v1.0"] = commitA + "\n"
	f.commits["o/r/feature/x"] = commitB
	f.commits["o/r/HEAD"] = commitA
	c := f.client()
	ctx := context.Background()

	for ref, want := range map[string]string{"v1.0": commitA, "feature/x": commitB, "": commitA} {
		got, err := c.Resolve(ctx, Source{Owner: "o", Repo: "r", Ref: ref})
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if got := f.accept.Load(); got != "application/vnd.github.sha" {
		t.Errorf("Accept = %v", got)
	}
}

func TestResolveOfACommitMakesNoRequest(t *testing.T) {
	f := newFakeGitHub(t)
	got, err := f.client().Resolve(context.Background(),
		Source{Owner: "o", Repo: "r", Ref: strings.ToUpper(commitA)})
	if err != nil || got != commitA {
		t.Fatalf("got %q, %v", got, err)
	}
	if n := f.requests.Load(); n != 0 {
		t.Fatalf("%d requests; want none", n)
	}
}

func TestResolveRefusesWhatIsNotACommitHash(t *testing.T) {
	f := newFakeGitHub(t)
	f.commits["o/r/main"] = "<html>surprise</html>"
	if _, err := f.client().Resolve(context.Background(), Source{Owner: "o", Repo: "r", Ref: "main"}); err == nil ||
		!strings.Contains(err.Error(), "commit hash") {
		t.Fatalf("got %v", err)
	}
}

func TestNotFoundSaysWhatToCheck(t *testing.T) {
	f := newFakeGitHub(t)
	_, err := f.client().Resolve(context.Background(), Source{Owner: "o", Repo: "r", Ref: "nope"})
	if err == nil || !strings.Contains(err.Error(), "only public repositories") || !strings.Contains(err.Error(), "o/r@nope") {
		t.Fatalf("got %v", err)
	}
}

func TestRateLimitIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret details that must not be shown", http.StatusForbidden)
	}))
	defer srv.Close()
	c := &Client{API: srv.URL, Codeload: srv.URL}
	_, err := c.Resolve(context.Background(), Source{Owner: "o", Repo: "r", Ref: "main"})
	if err == nil || !strings.Contains(err.Error(), "rate limiting") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("got %v", err)
	}
}

func TestFetchReadsTheModulesOfTheCommit(t *testing.T) {
	f := newFakeGitHub(t)
	f.archive = func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive(t, entry{name: "o-r-" + commitA[:7] + "/modules/tools.scm", body: "(define-module tools)"}))
	}
	files, err := f.client().Fetch(context.Background(), Source{Owner: "o", Repo: "r"}, commitA)
	if err != nil || len(files) != 1 || files[0].Module != "tools" {
		t.Fatalf("got %+v, %v", files, err)
	}
	if got := f.last.Load(); got != "/code/o/r/tar.gz/"+commitA {
		t.Errorf("requested %v", got)
	}
}

func TestFetchOnlyTakesAFullCommitHash(t *testing.T) {
	f := newFakeGitHub(t)
	for _, bad := range []string{"", "main", commitA[:7], commitA + "0", strings.ToUpper(commitA), "../" + commitA} {
		if _, err := f.client().Fetch(context.Background(), Source{Owner: "o", Repo: "r"}, bad); err == nil {
			t.Errorf("Fetch(%q) should fail", bad)
		}
	}
	if n := f.requests.Load(); n != 0 {
		t.Errorf("%d requests were made", n)
	}
}

func TestFetchStopsADownloadThatIsTooLarge(t *testing.T) {
	f := newFakeGitHub(t)
	f.archive = func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for i := range chunk {
			chunk[i] = byte(i * 7919) // not compressible, and not gzip either
		}
		for i := 0; i < MaxDownload>>20+2; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}
	_, err := f.client().Fetch(context.Background(), Source{Owner: "o", Repo: "r"}, commitA)
	if err == nil {
		t.Fatal("want an error")
	}
}

func TestARedirectToAnotherHostIsRefused(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the other host was contacted")
	}))
	defer elsewhere.Close()
	f := newFakeGitHub(t)
	f.archive = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/x", http.StatusFound)
	}
	_, err := f.client().Fetch(context.Background(), Source{Owner: "o", Repo: "r"}, commitA)
	if err == nil || !strings.Contains(err.Error(), "not a GitHub download host") {
		t.Fatalf("got %v", err)
	}
}

func TestARedirectBetweenTheTwoHostsIsFollowed(t *testing.T) {
	f := newFakeGitHub(t)
	f.archive = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/code/moved" {
			w.Write(archive(t, entry{name: "r/modules/a.scm", body: "x"}))
			return
		}
		http.Redirect(w, r, "/code/moved", http.StatusMovedPermanently)
	}
	files, err := f.client().Fetch(context.Background(), Source{Owner: "o", Repo: "r"}, commitA)
	if err != nil || len(files) != 1 {
		t.Fatalf("got %+v, %v", files, err)
	}
}

func TestRedirectPolicyChecks(t *testing.T) {
	c := &Client{API: "https://api.example.test", Codeload: "https://code.example.test"}
	policy := c.redirectPolicy()
	req := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: u}
	}
	if err := policy(req("https://code.example.test/x"), nil); err != nil {
		t.Errorf("a download host: %v", err)
	}
	for _, bad := range []string{
		"http://code.example.test/x",           // a downgrade
		"https://evil.example.test/x",          // another host
		"https://user:pw@code.example.test/x",  // credentials in the address
		"https://code.example.test.evil.test/", // a look-alike
	} {
		if err := policy(req(bad), nil); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if err := policy(req("https://code.example.test/x"), make([]*http.Request, maxRedirects+1)); err == nil {
		t.Error("a long chain should stop")
	}
}

func TestContextCancelsAFetch(t *testing.T) {
	f := newFakeGitHub(t)
	f.archive = func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := f.client().Fetch(ctx, Source{Owner: "o", Repo: "r"}, commitA); err == nil {
		t.Fatal("want an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the fetch did not stop")
	}
}
