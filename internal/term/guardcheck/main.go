//go:build linux || darwin

// Command guardcheck is the Go counterpart of the Rust unit test
// terminal_guard_restores_after_error_and_unwind, which cannot run under
// `go test` because it needs a real terminal. tests/pty_smoke.py runs it
// inside a PTY, looking for an executable named coigue-* next to the editor
// binary (bin/deps/) that lists the Rust test name and accepts the arguments
// cargo's test harness takes:
//
//	guardcheck --list
//	guardcheck --exact tests::terminal_guard_restores_after_error_and_unwind --nocapture
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"

	"github.com/fclaude/coigue/internal/term"
)

const name = "tests::terminal_guard_restores_after_error_and_unwind"

func main() {
	switch {
	case len(os.Args) == 2 && os.Args[1] == "--list":
		fmt.Printf("%s: test\n", name)
	case len(os.Args) >= 3 && os.Args[1] == "--exact" && os.Args[2] == name:
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, "guardcheck:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: guardcheck --list | --exact", name, "[--nocapture]")
		os.Exit(2)
	}
}

// same reports whether two termios are the same terminal modes. The kernel sets
// PENDIN by itself when the input queue is retyped after a mode change (macOS
// does so after a restore), and it says nothing about the modes, so it is left
// out of the comparison; the Rust test this stands for only asked whether raw
// mode was off.
func same(a, b unix.Termios) bool {
	a.Lflag &^= unix.PENDIN
	b.Lflag &^= unix.PENDIN
	return a == b
}

// termios is the terminal's raw termios, kept only to say what differs when the
// modes are not back: xterm.State does not expose its fields.
func termios() (unix.Termios, error) {
	t, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), ioctlReadTermios)
	if err != nil {
		return unix.Termios{}, err
	}
	return *t, nil
}

// explain lists the termios fields that differ, so that a failure on a
// platform nobody can try by hand (macOS in CI) says what changed.
func explain(before, after unix.Termios) string {
	var parts []string
	add := func(name string, a, b uint64) {
		if a != b {
			parts = append(parts, fmt.Sprintf("%s %#x -> %#x", name, a, b))
		}
	}
	add("iflag", uint64(before.Iflag), uint64(after.Iflag))
	add("oflag", uint64(before.Oflag), uint64(after.Oflag))
	add("cflag", uint64(before.Cflag), uint64(after.Cflag))
	add("lflag", uint64(before.Lflag&^unix.PENDIN), uint64(after.Lflag&^unix.PENDIN))
	add("ispeed", uint64(before.Ispeed), uint64(after.Ispeed))
	add("ospeed", uint64(before.Ospeed), uint64(after.Ospeed))
	if before.Cc != after.Cc {
		parts = append(parts, fmt.Sprintf("cc %v -> %v", before.Cc, after.Cc))
	}
	if len(parts) == 0 {
		return "the termios fields are equal"
	}
	return strings.Join(parts, "; ")
}

// notRestored is the error for modes that are not back, with what differs.
func notRestored(when string, before unix.Termios) error {
	after, err := termios()
	if err != nil {
		return fmt.Errorf("terminal modes not restored after %s", when)
	}
	return fmt.Errorf("terminal modes not restored after %s: %s", when, explain(before, after))
}

// run enters the terminal and leaves it again by a returned error and by a
// panic, checking after each that the original terminal modes are back.
func run() error {
	if !xterm.IsTerminal(int(os.Stdin.Fd())) || !xterm.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("needs a terminal on stdin and stdout")
	}
	originalTermios, err := termios()
	if err != nil {
		return err
	}
	errInjected := errors.New("injected event/render error")
	err = func() error {
		t, err := term.Enter(os.Stdout)
		if err != nil {
			return err
		}
		defer t.Close()
		if raw, err := termios(); err == nil && same(raw, originalTermios) {
			return errors.New("raw mode was not enabled")
		}
		return errInjected
	}()
	if !errors.Is(err, errInjected) {
		return fmt.Errorf("unexpected result %v", err)
	}
	if restored, err := termios(); err != nil || !same(restored, originalTermios) {
		return notRestored("an error", originalTermios)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		t, err := term.Enter(os.Stdout)
		if err != nil {
			panic(err)
		}
		defer t.Close()
		panic("injected panic")
	}()
	if recovered != "injected panic" {
		return fmt.Errorf("panic not propagated: %v", recovered)
	}
	if restored, err := termios(); err != nil || !same(restored, originalTermios) {
		return notRestored("a panic", originalTermios)
	}
	return nil
}
