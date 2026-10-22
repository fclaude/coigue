//go:build unix

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
	"reflect"

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

func mode() (*xterm.State, error) { return xterm.GetState(int(os.Stdin.Fd())) }

// run enters the terminal and leaves it again by a returned error and by a
// panic, checking after each that the original terminal modes are back.
func run() error {
	if !xterm.IsTerminal(int(os.Stdin.Fd())) || !xterm.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("needs a terminal on stdin and stdout")
	}
	original, err := mode()
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
		if raw, _ := mode(); reflect.DeepEqual(raw, original) {
			return errors.New("raw mode was not enabled")
		}
		return errInjected
	}()
	if !errors.Is(err, errInjected) {
		return fmt.Errorf("unexpected result %v", err)
	}
	if restored, _ := mode(); !reflect.DeepEqual(restored, original) {
		return errors.New("terminal modes not restored after an error")
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
	if restored, _ := mode(); !reflect.DeepEqual(restored, original) {
		return errors.New("terminal modes not restored after a panic")
	}
	return nil
}
