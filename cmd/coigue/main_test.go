package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// build compiles the command once per test into a temporary directory, the way
// cargo provides CARGO_BIN_EXE_coigue to the Rust integration tests.
func build(t *testing.T, ldflags string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "coigue")
	args := []string{"build", "-o", binary}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	cmd := exec.Command("go", append(args, "github.com/fclaude/coigue/cmd/coigue")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binary
}

func run(t *testing.T, binary string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	// No stdin and no controlling terminal: nothing here may need one.
	cmd.Stdin = nil
	err := cmd.Run()
	code = 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// Rust: tests/version.rs::version_exits_without_a_terminal
// Parity: CLI-01, CLI-02.
func TestVersionExitsWithoutATerminal(t *testing.T) {
	binary := build(t, "")
	stdout, stderr, code := run(t, binary, "--version")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "coigue 0.2.2") || !strings.Contains(stdout, "main build") || !strings.Contains(stdout, "commit") {
		t.Fatalf("stdout = %q", stdout)
	}
	if stdout != "coigue 0.2.2 (main build development; commit unknown)\n" {
		t.Fatalf("default build identity = %q", stdout)
	}
}

// The build identity is fixed at link time (CLI-02).
func TestVersionIdentityIsInjectedAtLinkTime(t *testing.T) {
	binary := build(t, "-X github.com/fclaude/coigue/internal/version.PackageRevision=r42 "+
		"-X github.com/fclaude/coigue/internal/version.SourceCommit=abc1234")
	stdout, _, code := run(t, binary, "--version")
	if code != 0 || stdout != "coigue 0.2.2 (main build r42; commit abc1234)\n" {
		t.Fatalf("exit %d stdout %q", code, stdout)
	}
}

// Rust: tests/version.rs::help_exits_without_a_terminal
// Parity: CLI-03.
func TestHelpExitsWithoutATerminal(t *testing.T) {
	binary := build(t, "")
	for _, flag := range []string{"--help", "-h"} {
		stdout, stderr, code := run(t, binary, flag)
		if code != 0 || stderr != "" {
			t.Fatalf("%s: exit %d stderr %q", flag, code, stderr)
		}
		if !strings.Contains(stdout, "Usage: coigue [FILE]") || !strings.Contains(stdout, "--version") || strings.ContainsRune(stdout, 0x1b) {
			t.Fatalf("%s: stdout = %q", flag, stdout)
		}
		const want = "Usage: coigue [FILE]\n\nWithout a file, welcome opens in the current directory.\n" +
			"Ctrl-O / - / :explore browse; Ctrl-P / :open find files; F1 / :help guide; :q quit.\n\n" +
			"  --version   Show source version, package build, and commit\n  --help, -h  Show this help\n"
		if stdout != want {
			t.Fatalf("%s: help text differs:\n%q", flag, stdout)
		}
	}
}

// Parity: CLI-04, CLI-06 (and ODD-01).
func TestArgumentContractWithoutATerminal(t *testing.T) {
	binary := build(t, "")
	for _, args := range [][]string{{"--help", "x"}, {"-h", "x"}} {
		stdout, _, code := run(t, binary, args...)
		if code != 0 || !strings.HasPrefix(stdout, "Usage: coigue [FILE]") {
			t.Errorf("%v: exit %d stdout %q", args, code, stdout)
		}
	}
	stdout, _, code := run(t, binary, "--version", "x")
	if code != 0 || !strings.HasPrefix(stdout, "coigue 0.2.2") {
		t.Errorf("--version x: exit %d stdout %q", code, stdout)
	}
	for _, args := range [][]string{{"a", "b"}, {"x", "--version"}} {
		stdout, stderr, code := run(t, binary, args...)
		if code != 1 || stderr != "coigue: usage: coigue [file]\n" || stdout != "" {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

// Parity: CLI-09. A file that cannot be opened fails before the terminal is touched.
func TestUnreadableFileFailsBeforeTheTerminalIsEntered(t *testing.T) {
	binary := build(t, "")
	dir := t.TempDir()
	bad := filepath.Join(dir, "binary")
	if err := os.WriteFile(bad, []byte{0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := run(t, binary, bad)
	if code != 1 || stdout != "" || stderr != "coigue: file is not UTF-8\n" {
		t.Errorf("invalid UTF-8: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	stdout, stderr, code = run(t, binary, dir)
	if code != 1 || stdout != "" || stderr != "coigue: Is a directory (os error 21)\n" {
		t.Errorf("directory: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}
