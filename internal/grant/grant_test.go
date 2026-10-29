package grant

import (
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

func fromHeader(t *testing.T, header string) (Grant, error) {
	t.Helper()
	return FromModule("(define-module m " + header + ")\n(define x 1)")
}

func TestARequestIsReadFromTheHeader(t *testing.T) {
	g, err := fromHeader(t, `(doc "x") (requires (net (allow "API.Datamuse.com" "b.example.org:8443") (methods post get)))`)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.String(); got != `(net (allow "api.datamuse.com" "b.example.org:8443") (methods get post))` {
		t.Fatalf("grant %s", got)
	}
	if got := g.Describe(); got != "can reach api.datamuse.com, b.example.org:8443 (GET, POST)" {
		t.Fatalf("describe %q", got)
	}
	if g.Private {
		t.Fatal("a header cannot ask for private addresses")
	}
}

func TestWithoutMethodsOnlyReadingIsGranted(t *testing.T) {
	g, err := fromHeader(t, `(requires (net (allow "a.example.com")))`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(g.Methods, ",") != "get,head" {
		t.Fatalf("methods %v", g.Methods)
	}
}

func TestAFileWithoutARequestAsksForNothing(t *testing.T) {
	for _, src := range []string{"", "(define x 1)", "(define-module m (doc \"x\"))", "(define-module m)"} {
		g, err := FromModule(src)
		if err != nil || !g.None() || g.String() != "" || g.Describe() != "no network access" {
			t.Fatalf("%q: %+v %v", src, g, err)
		}
	}
}

func TestRequestsThatAreNotOffered(t *testing.T) {
	cases := map[string]string{
		`(requires (fs (read "x")))`:                                                "the only grant is net",
		`(requires (exec (allow "git")))`:                                           "the only grant is net",
		`(requires (net))`:                                                          "name at least one host",
		`(requires (net (allow)))`:                                                  "name at least one host",
		`(requires (net (allow "*")))`:                                              "wildcard",
		`(requires (net (allow *)))`:                                                "hosts are strings",
		`(requires (net (allow "*.example.com")))`:                                  "wildcard",
		`(requires (net (allow "https://a.example.com/x")))`:                        "not a host",
		`(requires (net (allow "a.example.com/path")))`:                             "not a host",
		`(requires (net (allow "user@a.example.com")))`:                             "not a host",
		`(requires (net (allow "a.example.com") (private #t)))`:                     "not something a module can ask for",
		`(requires (net (allow "a.example.com") (max-body 1)))`:                     "the editor sets the limits",
		`(requires (net (allow "a.example.com") (methods fetch)))`:                  "not a method",
		`(requires (net (allow "a.example.com")) (net (allow "b.com")))`:            "net is given twice",
		`(requires (net (allow "a.example.com"))) (requires (net (allow "b.com")))`: "requires is given twice",
		`(requires (net (allow "a.example.com") (allow "b.com")))`:                  "allow is given twice",
		`(requires net)`:             "requires takes clauses",
		`(requires (net (bogus 1)))`: "not a clause",
	}
	for header, want := range cases {
		_, err := fromHeader(t, header)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", header, err, want)
		}
	}
}

func TestTooManyHostsAreRefused(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= MaxHosts; i++ {
		b.WriteString(` "h` + string(rune('a'+i)) + `.example.com"`)
	}
	if _, err := fromHeader(t, `(requires (net (allow`+b.String()+`)))`); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("error %v", err)
	}
}

func TestAGrantRoundTripsThroughTheLockfileForm(t *testing.T) {
	g, err := fromHeader(t, `(requires (net (allow "b.com" "a.com") (methods get head post)))`)
	if err != nil {
		t.Fatal(err)
	}
	forms, err := scheme.Read(g.String())
	if err != nil || len(forms) != 1 {
		t.Fatalf("%v %d", err, len(forms))
	}
	back, err := ParseNet(forms[0])
	if err != nil || !back.Equal(g) || back.String() != g.String() {
		t.Fatalf("%+v %v", back, err)
	}
}

func TestWithinAndWidening(t *testing.T) {
	old, _ := fromHeader(t, `(requires (net (allow "a.com") (methods get)))`)
	same, _ := fromHeader(t, `(requires (net (allow "A.com") (methods get)))`)
	wider, _ := fromHeader(t, `(requires (net (allow "a.com") (methods get head)))`)
	moreHosts, _ := fromHeader(t, `(requires (net (allow "a.com" "b.com") (methods get)))`)
	post, _ := fromHeader(t, `(requires (net (allow "a.com") (methods post)))`)
	none := Grant{}

	if !same.Within(old) || !old.Within(same) || !old.Equal(same) {
		t.Fatal("the same hosts and methods, written differently, are the same grant")
	}
	if !old.Within(wider) || wider.Within(old) {
		t.Fatal("a narrower request is within the wider one, not the other way")
	}
	if moreHosts.Within(old) || post.Within(old) {
		t.Fatal("a new host or method is not within the old grant")
	}
	if !none.Within(old) || old.Within(none) {
		t.Fatal("asking for nothing is always within; asking for something is not within nothing")
	}
	if got := Widen(old, moreHosts); len(got) != 1 || got[0] != "new hosts: b.com" {
		t.Fatalf("widen %v", got)
	}
	if got := Widen(old, post); len(got) != 1 || got[0] != "new methods: POST" {
		t.Fatalf("widen %v", got)
	}
	if got := Widen(wider, old); len(got) != 0 {
		t.Fatalf("widen %v", got)
	}
	if got := Widen(none, old); len(got) != 1 || got[0] != "new hosts: a.com" {
		t.Fatalf("widen %v", got)
	}
	priv := old
	priv.Private = true
	if priv.Within(old) || !old.Within(priv) {
		t.Fatal("private addresses are a widening")
	}
}

func TestThePolicyJudgesRequests(t *testing.T) {
	g, _ := fromHeader(t, `(requires (net (allow "api.datamuse.com")))`)
	p, err := g.Policy()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CheckNet("GET", "https://api.datamuse.com/words?rel_rhy=tree"); err != nil {
		t.Fatalf("an allowed request: %v", err)
	}
	for _, c := range [][2]string{
		{"POST", "https://api.datamuse.com/words"},
		{"GET", "https://evil.example.com/"},
		{"GET", "http://api.datamuse.com:8080/"},
	} {
		if err := p.CheckNet(c[0], c[1]); err == nil {
			t.Errorf("%s %s was allowed", c[0], c[1])
		}
	}
}
