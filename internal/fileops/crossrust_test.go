package fileops

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/fclaude/coigue/internal/fsx"
)

// rustFileops runs the editor's own fileops.rs, built into a scratch program,
// and returns its one-line answer.
func rustFileops(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).Output()
	if err != nil {
		t.Fatalf("%s %v: %v", bin, args, err)
	}
	return strings.TrimSuffix(string(out), "\n")
}

func goAnswer(path string, err error) string {
	if err != nil {
		return "err " + fsx.Describe(err)
	}
	if path == "" {
		return "ok"
	}
	return "ok " + path
}

// TestInteroperatesWithTheRustImplementation deletes with one implementation
// and restores with the other (the Python suite and the users do), and checks
// that both report the same errors. COIGUE_FILEOPS names the scratch binary.
func TestInteroperatesWithTheRustImplementation(t *testing.T) {
	bin := os.Getenv("COIGUE_FILEOPS")
	if bin == "" {
		t.Skip("set COIGUE_FILEOPS to the scratch fileops binary to run")
	}
	root := root(t)
	for _, name := range []string{"x", "y", "z", "w"} {
		write(t, fsx.Join(root, name), []byte("data "+name))
	}
	ok(t, os.Mkdir(fsx.Join(root, "dir"), 0o755))
	write(t, fsx.Join(root, "dir", "child"), []byte("nested"))
	// Interleave the implementations: the newest record wins whoever wrote it.
	_, err := TrashEntry(fsx.Join(root, "x"))
	ok(t, err)
	if got := rustFileops(t, bin, "trash", fsx.Join(root, "y")); !strings.HasPrefix(got, "ok ") {
		t.Fatal(got)
	}
	_, err = TrashEntry(fsx.Join(root, "dir"))
	ok(t, err)
	if got := rustFileops(t, bin, "trash", fsx.Join(root, "z")); !strings.HasPrefix(got, "ok ") {
		t.Fatal(got)
	}
	if got, want := rustFileops(t, bin, "restore", root), "ok "+fsx.Join(root, "z"); got != want {
		t.Errorf("Rust restore: %q, want %q", got, want)
	}
	restored, err := RestoreLatest(root)
	ok(t, err)
	if restored != fsx.Join(root, "dir") || read(t, fsx.Join(root, "dir", "child")) != "nested" {
		t.Errorf("Go restore of a Rust-ordered record gave %q", restored)
	}
	if got, want := rustFileops(t, bin, "restore", root), "ok "+fsx.Join(root, "y"); got != want {
		t.Errorf("Rust restore: %q, want %q", got, want)
	}
	restored, err = RestoreLatest(root)
	ok(t, err)
	if restored != fsx.Join(root, "x") || read(t, restored) != "data x" {
		t.Errorf("Go restore gave %q", restored)
	}
	if got := rustFileops(t, bin, "restore", root); got != "err No deleted entries to restore in this directory" {
		t.Errorf("Rust restore when empty: %q", got)
	}
	// Non-UTF-8 names travel between the two.
	odd := fsx.Join(root, "f\xff")
	write(t, odd, []byte("odd"))
	_, err = TrashEntry(odd)
	ok(t, err)
	if got := rustFileops(t, bin, "restore", root); !strings.HasPrefix(got, "ok ") || read(t, odd) != "odd" {
		t.Errorf("Rust restore of a non-UTF-8 name: %q", got)
	}

	// The same operations must fail with the same words.
	missing := fsx.Join(root, "missing")
	cases := []struct {
		name string
		args []string
		go_  func() string
	}{
		{"create existing file", []string{"create", fsx.Join(root, "w"), "file"}, func() string { return goAnswer("", Create(fsx.Join(root, "w"), false)) }},
		{"create existing dir", []string{"create", fsx.Join(root, "w"), "dir"}, func() string { return goAnswer("", Create(fsx.Join(root, "w"), true)) }},
		{"create reserved", []string{"create", fsx.Join(root, ".git"), "dir"}, func() string { return goAnswer("", Create(fsx.Join(root, ".git"), true)) }},
		{"move missing", []string{"move", missing, fsx.Join(root, "q")}, func() string { return goAnswer("", MoveEntry(missing, fsx.Join(root, "q"))) }},
		{"move onto existing", []string{"move", fsx.Join(root, "w"), fsx.Join(root, "dir")}, func() string { return goAnswer("", MoveEntry(fsx.Join(root, "w"), fsx.Join(root, "dir"))) }},
		{"move into itself", []string{"move", fsx.Join(root, "dir"), fsx.Join(root, "dir", "c")}, func() string {
			return goAnswer("", MoveEntry(fsx.Join(root, "dir"), fsx.Join(root, "dir", "c")))
		}},
		{"trash missing", []string{"trash", missing}, func() string { p, err := TrashEntry(missing); return goAnswer(p, err) }},
		{"destination empty", []string{"destination", root, ""}, func() string { p, err := Destination(root, ""); return goAnswer(p, err) }},
		{"destination control", []string{"destination", root, "a\tb"}, func() string { p, err := Destination(root, "a\tb"); return goAnswer(p, err) }},
		{"destination reserved", []string{"destination", root, "x/.coigue-trash"}, func() string { p, err := Destination(root, "x/.coigue-trash"); return goAnswer(p, err) }},
		{"destination missing parent", []string{"destination", root, "nope/child"}, func() string { p, err := Destination(root, "nope/child"); return goAnswer(p, err) }},
		{"destination dotdot", []string{"destination", root, "dir/.."}, func() string { p, err := Destination(root, "dir/.."); return goAnswer(p, err) }},
		{"destination ok", []string{"destination", root, "dir/new"}, func() string { p, err := Destination(root, "dir/new"); return goAnswer(p, err) }},
		{"destination through a link", []string{"destination", root, "ln/new"}, func() string { p, err := Destination(root, "ln/new"); return goAnswer(p, err) }},
		{"restore without storage", []string{"restore", fsx.Join(root, "dir")}, func() string { p, err := RestoreLatest(fsx.Join(root, "dir")); return goAnswer(p, err) }},
	}
	ok(t, os.Symlink(fsx.Join(root, "dir"), fsx.Join(root, "ln")))
	for _, c := range cases {
		if want, got := rustFileops(t, bin, c.args...), c.go_(); want != got {
			t.Errorf("%s: Rust says %q, Go says %q", c.name, want, got)
		}
	}
}
