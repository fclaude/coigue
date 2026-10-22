package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestComponentsFollowRustPath(t *testing.T) {
	for in, want := range map[string][]string{
		"":          nil,
		"/":         {"/"},
		"/a//b/":    {"/", "a", "b"},
		"./a/./b":   {".", "a", "b"},
		"a/./b/../": {"a", "b", ".."},
		"../x":      {"..", "x"},
	} {
		if got := Components(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Components(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParentFileNameAndPrefixes(t *testing.T) {
	parents := map[string]struct {
		parent string
		ok     bool
	}{
		"/":      {"", false},
		"":       {"", false},
		"a":      {"", true},
		"/a":     {"/", true},
		"/a/b":   {"/a", true},
		"/a/b/":  {"/a", true},
		"a//b":   {"a", true},
		"/a/b/c": {"/a/b", true},
		// Expected values are Path::parent results from the Rust standard library.
		"/a/./b/c": {"/a/./b", true},
		"/a/./b":   {"/a", true},
		"a/./b":    {"a", true},
		"./a":      {".", true},
		".":        {"", true},
		"a/.":      {"", true},
		"/a/b/.":   {"/a", true},
		"//a":      {"/", true},
		"./":       {"", true},
		"../a":     {"..", true},
		"a/../b":   {"a/..", true},
	}
	for in, want := range parents {
		got, ok := Parent(in)
		if got != want.parent || ok != want.ok {
			t.Errorf("Parent(%q) = %q,%v want %q,%v", in, got, ok, want.parent, want.ok)
		}
	}
	for in, want := range map[string]string{"/a/b.txt": "b.txt", "a": "a", "/a/..": "", "/": ""} {
		got, ok := FileName(in)
		if got != want || ok != (want != "") {
			t.Errorf("FileName(%q) = %q,%v", in, got, ok)
		}
	}
	if rest, ok := StripPrefix("/a/b/c", "/a/b"); !ok || rest != "c" {
		t.Errorf("StripPrefix = %q,%v", rest, ok)
	}
	if rest, ok := StripPrefix("/a/b", "/a/b"); !ok || rest != "" {
		t.Errorf("equal StripPrefix = %q,%v", rest, ok)
	}
	if _, ok := StripPrefix("/a/bc", "/a/b"); ok {
		t.Error("prefix must match whole components")
	}
	if !StartsWith("/a/b", "/a") || StartsWith("/ab", "/a") {
		t.Error("StartsWith is component-wise")
	}
	if !EndsWith("/x/bucket/renamed.rs", "bucket/renamed.rs") || EndsWith("/x/abucket/renamed.rs", "bucket/renamed.rs") {
		t.Error("EndsWith is component-wise")
	}
}

func TestJoinReplacesOnAbsoluteAndNeverCleans(t *testing.T) {
	for _, c := range []struct{ base, part, want string }{
		{"/a", "b", "/a/b"},
		{"/a/", "b", "/a/b"},
		{"/a", "/b", "/b"},
		{"", "b", "b"},
		{"/a", "../b", "/a/../b"},
	} {
		if got := Join(c.base, c.part); got != c.want {
			t.Errorf("Join(%q,%q) = %q want %q", c.base, c.part, got, c.want)
		}
	}
}

func TestAbsoluteMatchesRustForTrailingAndLeadingSlashes(t *testing.T) {
	cwd, _ := os.Getwd()
	// Expected values are std::path::absolute results from the Rust standard library.
	for in, want := range map[string]string{
		"a/":        cwd + "/a/",
		"//a":       "//a",
		"///a":      "/a",
		"a//b/./c/": cwd + "/a/b/c/",
		"/":         "/",
		"//":        "//",
		"./a":       cwd + "/a",
		"a/..":      cwd + "/a/..",
		"/a/b/":     "/a/b/",
		"/a/./b/":   "/a/b/",
		"a/./":      cwd + "/a/",
		"/./a":      "/a",
		".":         cwd,
	} {
		if got, err := Absolute(in); err != nil || got != want {
			t.Errorf("Absolute(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAbsoluteKeepsParentComponentsAndCanonicalizeResolvesLinks(t *testing.T) {
	cwd, _ := os.Getwd()
	got, err := Absolute("x/./y//z/../w")
	if err != nil || got != Join(cwd, "x/y/z/../w") {
		t.Fatalf("Absolute = %q, %v", got, err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(target)
	if got, err := Canonicalize(link); err != nil || got != want {
		t.Fatalf("Canonicalize = %q, %v; want %q", got, err, want)
	}
	if _, err := Canonicalize(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing path error = %v", err)
	}
	if Exists("") || !Exists(link) {
		t.Fatal("Exists mismatch")
	}
}

func TestExtensionFollowsRustPath(t *testing.T) {
	for in, want := range map[string]struct {
		ext string
		ok  bool
	}{
		"a.rs":      {"rs", true},
		"/x/a.b.GO": {"GO", true},
		".rs":       {"", false},
		"noext":     {"", false},
		"a.":        {"", true},
		"/x/..":     {"", false},
		"":          {"", false},
	} {
		if ext, ok := Extension(in); ext != want.ext || ok != want.ok {
			t.Errorf("Extension(%q) = %q,%v want %q,%v", in, ext, ok, want.ext, want.ok)
		}
	}
}

func TestRenameNoReplaceNeverOverwritesAndDescribeMatchesRustWording(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("a"), 0o600)
	os.WriteFile(b, []byte("b"), 0o600)
	err := RenameNoReplace(a, b)
	if err == nil || Describe(err) != "File exists (os error 17)" {
		t.Fatalf("existing destination: %v (%q)", err, Describe(err))
	}
	if got, _ := os.ReadFile(b); string(got) != "b" {
		t.Fatalf("destination overwritten: %q", got)
	}
	if err := RenameNoReplace(a, filepath.Join(dir, "c")); err != nil {
		t.Fatal(err)
	}
	_, err = os.ReadFile(filepath.Join(dir, "missing"))
	if got := Describe(err); got != "No such file or directory (os error 2)" {
		t.Fatalf("Describe = %q", got)
	}
	if got := Describe(errors.New("plain message")); got != "plain message" {
		t.Fatalf("Describe = %q", got)
	}
}

func TestDisplayReplacesMaximalInvalidSequencesLikeRust(t *testing.T) {
	// Expected values are Path::display() results from the Rust standard library.
	for in, want := range map[string]string{
		"ok é界":        "ok é界",
		"f\xff":        "f�",
		"\xff\xff":     "��",
		"a\xe2\x82b":   "a�b",
		"a\xe2b":       "a�b",
		"\xf0\x9f\x98": "�",
		"\xc0\x80":     "��",
		"\xed\xa0\x80": "���",
		"x\x80y":       "x�y",
	} {
		if got := Display(in); got != want {
			t.Errorf("Display(%q) = %q, want %q", in, got, want)
		}
	}
}
