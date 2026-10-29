package modsrc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/ext"
)

const (
	commitA = "0123456789abcdef0123456789abcdef01234567"
	commitB = "fedcba9876543210fedcba9876543210fedcba98"
)

type entry struct {
	name string
	body string
	typ  byte
	link string
}

// archive builds a gzipped tar the way GitHub lays one out, with a global
// header first.
func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header",
		PAXRecords: map[string]string{"comment": commitA}}))
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Typeflag: typ, Name: e.name, Mode: 0o644, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		must(tw.WriteHeader(h))
		if typ == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			must(err)
		}
	}
	must(tw.Close())
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	_, err := zw.Write(raw.Bytes())
	must(err)
	must(zw.Close())
	return out.Bytes()
}

func extract(t *testing.T, entries ...entry) ([]File, error) {
	t.Helper()
	return Extract(bytes.NewReader(archive(t, entries...)))
}

func TestParseSource(t *testing.T) {
	good := map[string]Source{
		"fclaude/coigue-tools":                  {Owner: "fclaude", Repo: "coigue-tools"},
		"fclaude/tools@v1.2.0":                  {Owner: "fclaude", Repo: "tools", Ref: "v1.2.0"},
		"github.com/a/b.c@feature/x":            {Owner: "a", Repo: "b.c", Ref: "feature/x"},
		"https://github.com/a-b/c_d@" + commitA: {Owner: "a-b", Repo: "c_d", Ref: commitA},
	}
	for in, want := range good {
		got, err := ParseSource(in)
		if err != nil || got != want {
			t.Errorf("ParseSource(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	bad := []string{
		"", "tools", "a/", "/b", "a/b/c", "-a/b", "a/..", "a/.", "a b/c", "a/b c",
		"a/b@", "a/b@..", "a/b@x..y", "a/b@/x", "a/b@x/", "a/b@-x", "a/b@x.lock", "a/b@x//y", "a/b@x y",
		"https://example.com/a/b", "a/b?x=1", "a/b#frag", "a/b@x?y",
	}
	for _, in := range bad {
		if got, err := ParseSource(in); err == nil {
			t.Errorf("ParseSource(%q) = %+v; want an error", in, got)
		}
	}
}

func TestSourceRoundTrips(t *testing.T) {
	for _, in := range []string{"a/b", "a/b@main", "a/b@" + commitA} {
		s, err := ParseSource(in)
		if err != nil || s.String() != in {
			t.Errorf("%q -> %+v (%v) -> %q", in, s, err, s.String())
		}
	}
}

func TestExtractReturnsOnlyTopLevelModules(t *testing.T) {
	files, err := extract(t,
		entry{name: "o-r-abc/", typ: tar.TypeDir},
		entry{name: "o-r-abc/README.md", body: "readme"},
		entry{name: "o-r-abc/modules/", typ: tar.TypeDir},
		entry{name: "o-r-abc/modules/zeta.scm", body: "(define-module zeta)"},
		entry{name: "o-r-abc/modules/alpha.scm", body: "(define-module alpha)"},
		entry{name: "o-r-abc/modules/notes.txt", body: "ignored"},
		entry{name: "o-r-abc/modules/deep/inner.scm", body: "ignored: too deep"},
		entry{name: "o-r-abc/src/modules/other.scm", body: "ignored: not the modules directory"},
		entry{name: "o-r-abc/modules.scm", body: "ignored"},
	)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Module)
	}
	if got := strings.Join(names, ","); got != "alpha,zeta" {
		t.Fatalf("modules = %s; want alpha,zeta (sorted, only modules/*.scm)", got)
	}
	if string(files[0].Data) != "(define-module alpha)" || files[0].Sum() != Sum([]byte("(define-module alpha)")) {
		t.Errorf("alpha = %q, sum %s", files[0].Data, files[0].Sum())
	}
}

func TestExtractWithoutModulesIsEmptyNotAnError(t *testing.T) {
	files, err := extract(t, entry{name: "o-r-abc/README.md", body: "x"})
	if err != nil || len(files) != 0 {
		t.Fatalf("got %v, %v", files, err)
	}
}

func TestExtractRefusesWhatItCannotTrust(t *testing.T) {
	big := strings.Repeat("x", MaxFile+1)
	var many []entry
	for i := 0; i <= ext.MaxModules; i++ {
		many = append(many, entry{name: fmt.Sprintf("r/modules/m%d.scm", i), body: "x"})
	}
	cases := []struct {
		name    string
		entries []entry
		want    string
	}{
		{"a symbolic link", []entry{{name: "r/modules/a.scm", typ: tar.TypeSymlink, link: "/etc/passwd"}}, "not a regular file"},
		{"a hard link", []entry{{name: "r/modules/a.scm", typ: tar.TypeLink, link: "r/README"}}, "not a regular file"},
		{"a name Scheme cannot use", []entry{{name: "r/modules/Bad_Name.scm", body: "x"}}, "not a module name"},
		{"the reserved init name", []entry{{name: "r/modules/init.scm", body: "x"}}, "reserved"},
		{"the same module twice", []entry{{name: "r/modules/a.scm", body: "x"}, {name: "r/modules/a.scm", body: "y"}}, "twice"},
		{"a file over the limit", []entry{{name: "r/modules/a.scm", body: big}}, "larger than"},
		{"too many modules", many, "more than"},
		{"a path with ..", []entry{{name: "r/../x/modules/a.scm", body: "x"}}, "leaves its directory"},
		{"an empty path part", []entry{{name: "r//modules/a.scm", body: "x"}}, "leaves its directory"},
		{"two top directories", []entry{{name: "r/README", body: "x"}, {name: "s/modules/a.scm", body: "x"}}, "more than one top directory"},
	}
	for _, c := range cases {
		files, err := extract(t, c.entries...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, %v; want an error containing %q", c.name, files, err, c.want)
		}
	}
}

func TestExtractStopsAnArchiveThatExpandsWithoutBound(t *testing.T) {
	// Highly compressible, so the download is small and the unpacked size is not.
	chunk := bytes.Repeat([]byte{0}, 1<<20)
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	size := int64(MaxExpanded + 2<<20)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "r/blob", Mode: 0o644, Size: size}); err != nil {
		t.Fatal(err)
	}
	for written := int64(0); written < size; written += int64(len(chunk)) {
		if _, err := tw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw.Bytes())
	zw.Close()
	if gz.Len() > MaxDownload {
		t.Fatalf("test archive is %d bytes; it should be small", gz.Len())
	}
	if _, err := Extract(&gz); err == nil || !strings.Contains(err.Error(), "unpacks to more than") {
		t.Fatalf("got %v; want the unpacked-size error", err)
	}
}

func TestExtractRefusesWhatIsNotGzip(t *testing.T) {
	if _, err := Extract(strings.NewReader("<html>not found</html>")); err == nil {
		t.Fatal("want an error")
	}
}

func lockEntry(module string) Entry {
	return Entry{Module: module, Source: "owner/repo", Commit: commitA, SHA256: Sum([]byte(module))}
}

func TestLockRoundTripsAndIsSorted(t *testing.T) {
	dir := t.TempDir()
	l := &Lock{}
	l.Set(lockEntry("zeta"))
	l.Set(lockEntry("alpha"))
	e := lockEntry("alpha")
	e.Commit = commitB
	l.Set(e)
	if len(l.Entries) != 2 || l.Entries[0].Module != "alpha" || l.Entries[0].Commit != commitB {
		t.Fatalf("entries = %+v", l.Entries)
	}
	if err := WriteLock(dir, l); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got.Entries) != fmt.Sprint(l.Entries) {
		t.Fatalf("read %+v; wrote %+v", got.Entries, l.Entries)
	}
	if text := got.String(); text != l.String() || !strings.HasPrefix(text, ";; ") {
		t.Fatalf("text does not round trip:\n%s", text)
	}
	if !got.Remove("alpha") || got.Remove("alpha") {
		t.Fatal("Remove should report once")
	}
	if _, ok := got.Find("alpha"); ok {
		t.Fatal("alpha is still found")
	}
	if names, _ := filepath.Glob(filepath.Join(dir, ".coigue-tmp-*")); len(names) != 0 {
		t.Fatalf("temporary files left: %v", names)
	}
}

func TestAMissingLockfileIsEmpty(t *testing.T) {
	l, err := ReadLock(t.TempDir())
	if err != nil || len(l.Entries) != 0 {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestLockfileIsReadAsDataAndCheckedClosely(t *testing.T) {
	ok := fmt.Sprintf(`(modules (a (source "o/r") (commit %q) (sha256 %q)))`, commitA, strings.Repeat("a", 64))
	if _, err := ParseLock(ok); err != nil {
		t.Fatalf("a good lockfile: %v", err)
	}
	cases := map[string]string{
		"not modules":      `(other)`,
		"two forms":        `(modules) (modules)`,
		"code":             `(modules (a (source (string-append "o" "/r")) (commit "x") (sha256 "y")))`,
		"a bad commit":     fmt.Sprintf(`(modules (a (source "o/r") (commit "abc") (sha256 %q)))`, strings.Repeat("a", 64)),
		"an upper hash":    fmt.Sprintf(`(modules (a (source "o/r") (commit %q) (sha256 %q)))`, commitA, strings.Repeat("A", 64)),
		"a ref in source":  fmt.Sprintf(`(modules (a (source "o/r@main") (commit %q) (sha256 %q)))`, commitA, strings.Repeat("a", 64)),
		"a bad module":     fmt.Sprintf(`(modules (init (source "o/r") (commit %q) (sha256 %q)))`, commitA, strings.Repeat("a", 64)),
		"a missing field":  fmt.Sprintf(`(modules (a (source "o/r") (commit %q)))`, commitA),
		"an unknown field": fmt.Sprintf(`(modules (a (source "o/r") (commit %q) (sha256 %q) (url "x")))`, commitA, strings.Repeat("a", 64)),
		"a repeated field": fmt.Sprintf(`(modules (a (source "o/r") (source "o/r") (commit %q) (sha256 %q)))`, commitA, strings.Repeat("a", 64)),
		"a duplicate":      fmt.Sprintf(`(modules (a (source "o/r") (commit %[1]q) (sha256 %[2]q)) (a (source "o/r") (commit %[1]q) (sha256 %[2]q)))`, commitA, strings.Repeat("a", 64)),
		"a syntax error":   `(modules (a`,
	}
	for name, text := range cases {
		if l, err := ParseLock(text); err == nil {
			t.Errorf("%s: parsed as %+v; want an error", name, l)
		}
	}
}

func TestWriteLockRefusesAnInvalidEntry(t *testing.T) {
	dir := t.TempDir()
	bad := &Lock{Entries: []Entry{{Module: "a", Source: "o/r", Commit: "nope", SHA256: "nope"}}}
	if err := WriteLock(dir, bad); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(filepath.Join(dir, LockName)); err == nil {
		t.Fatal("a lockfile was written for an invalid entry")
	}
}

func TestReadLockRefusesWhatIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, LockName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(dir); err == nil {
		t.Fatal("want an error for a directory")
	}
}
