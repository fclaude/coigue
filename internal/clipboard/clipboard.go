// Package clipboard gives explicit desktop clipboard access through local
// utilities; never OSC 52.
package clipboard

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/fclaude/coigue/internal/fsx"
)

const (
	limit         = 8 * 1024 * 1024
	helperTimeout = 2 * time.Second
	pollInterval  = 10 * time.Millisecond
)

// Backend is one pair of clipboard utilities and their arguments.
type Backend struct {
	copy      string
	copyArgs  []string
	paste     string
	pasteArgs []string
}

func backends(macos, wayland, x11 bool) []Backend {
	if macos {
		return []Backend{{copy: "pbcopy", paste: "pbpaste"}}
	}
	var choices []Backend
	if wayland {
		choices = append(choices, Backend{
			copy:      "wl-copy",
			copyArgs:  []string{"--type", "text/plain;charset=utf-8"},
			paste:     "wl-paste",
			pasteArgs: []string{"--no-newline", "--type", "text"},
		})
	}
	if x11 {
		choices = append(choices, Backend{
			copy:      "xclip",
			copyArgs:  []string{"-selection", "clipboard", "-in"},
			paste:     "xclip",
			pasteArgs: []string{"-selection", "clipboard", "-out"},
		})
		choices = append(choices, Backend{
			copy:      "xsel",
			copyArgs:  []string{"--clipboard", "--input"},
			paste:     "xsel",
			pasteArgs: []string{"--clipboard", "--output"},
		})
	}
	return choices
}

// message is an error whose text is user-visible and copied verbatim from the
// Rust sources, including their capital letters.
type message string

func (m message) Error() string { return string(m) }

const (
	errTooLarge  = message("clipboard text exceeds 8 MiB")
	errNotUTF8   = message("clipboard does not contain UTF-8 text")
	errNoDesktop = message("No desktop clipboard available (SSH/headless); internal y/p still work")
	errMissing   = message("Clipboard utility missing: use wl-clipboard (Wayland), xclip/xsel (X11), or pbcopy/pbpaste (macOS); internal y/p still work")
)

// timedOutError is Rust's io::ErrorKind::TimedOut: errors.Is(err,
// os.ErrDeadlineExceeded) recognises it.
type timedOutError struct{}

func (timedOutError) Error() string { return "clipboard helper timed out" }

func (timedOutError) Timeout() bool { return true }

func (timedOutError) Is(target error) bool { return target == os.ErrDeadlineExceeded }

// Private temporary files avoid pipe deadlocks with large text or clipboard owners
// that fork. They are unlinked on every exit path, including helper errors/timeouts.
//
// cmd is started with its stdin, stdout and stderr taken over; input is the
// text to send (copy) or nil to read the helper's output (paste). Cancelling
// ctx kills the helper like the timeout does.
func run(ctx context.Context, cmd *exec.Cmd, input *string, timeout time.Duration) (string, error) {
	if input != nil && len(*input) > limit {
		return "", errTooLarge
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	source, err := tempFile()
	if err != nil {
		return "", err
	}
	defer source.Close()
	if input != nil {
		if _, err := source.WriteString(*input); err != nil {
			return "", err
		}
		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
	}
	output, err := tempFile()
	if err != nil {
		return "", err
	}
	defer output.Close()
	cmd.Stdin = source
	cmd.Stderr = nil
	if input != nil {
		cmd.Stdout = nil
	} else {
		cmd.Stdout = output
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	kill := func() {
		_ = cmd.Process.Kill()
		<-done
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var waited error
wait:
	for {
		if input == nil && outputTooLarge(output) {
			kill()
			return "", errTooLarge
		}
		select {
		case waited = <-done:
			break wait
		case <-ticker.C:
		case <-deadline.C:
			// A helper that finished in the meantime wins, as in Rust where
			// try_wait is asked before the deadline is.
			select {
			case waited = <-done:
				break wait
			default:
			}
			kill()
			return "", timedOutError{}
		case <-ctx.Done():
			select {
			case waited = <-done:
				break wait
			default:
			}
			kill()
			return "", ctx.Err()
		}
	}
	if waited != nil {
		var failed *exec.ExitError
		if !errors.As(waited, &failed) {
			return "", waited
		}
		return "", errors.New("clipboard helper failed (" + exitStatus(failed.ProcessState) + "); check desktop access/clipboard contents")
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(output, limit+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", errTooLarge
	}
	if !utf8.Valid(data) {
		return "", errNotUTF8
	}
	return string(data), nil
}

// outputTooLarge is output.metadata().is_ok_and(|m| m.len() > LIMIT).
func outputTooLarge(output *os.File) bool {
	info, err := output.Stat()
	return err == nil && info.Size() > limit
}

// tempFile returns an open, private, already unlinked temporary file in
// os.TempDir (or one that never had a name where the kernel allows it).
func tempFile() (*os.File, error) {
	file, err := openUnnamed()
	if !errors.Is(err, errNoUnnamed) {
		return file, err
	}
	return createUnlinked(os.TempDir())
}

// createUnlinked creates a file in dir and removes its name at once.
func createUnlinked(dir string) (*os.File, error) {
	file, err := os.CreateTemp(dir, ".coigue-clipboard-*")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(file.Name()); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// errNoUnnamed says that openUnnamed has no way to make an unnamed file here.
var errNoUnnamed = errors.New("unnamed temporary files are not available")

// helperError is the failure of one helper program, "{program}: {error}".
type helperError struct {
	program string
	err     error
}

func (e *helperError) Error() string { return e.program + ": " + osErrorText(e.err) }

func (e *helperError) Unwrap() error { return e.err }

// osErrorText renders err the way Rust's io::Error does ("Permission denied
// (os error 13)"); Go's *PathError and *exec.Error would add
// "fork/exec /path/to/helper: " in front of it.
func osErrorText(err error) string { return fsx.Describe(err) }

func transfer(ctx context.Context, input *string) (string, error) {
	present := func(name string) bool { return os.Getenv(name) != "" }
	choices := backends(
		runtime.GOOS == "darwin",
		present("WAYLAND_DISPLAY"),
		present("DISPLAY"),
	)
	if len(choices) == 0 {
		return "", errNoDesktop
	}
	for _, backend := range choices {
		program, args := backend.copy, backend.copyArgs
		if input == nil {
			program, args = backend.paste, backend.pasteArgs
		}
		result, err := runProgram(ctx, program, args, input)
		// Rust oddity preserved: any "not found", not only a missing program (a
		// TMPDIR that does not exist, say), moves on to the next backend and ends
		// as "Clipboard utility missing".
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", &helperError{program, err}
		}
		return result, nil
	}
	return "", errMissing
}

// runProgram is run on Command::new(program).args(args), whose spawn is
// execvp: the directories of PATH are tried in order, and a file that cannot
// be executed does not stop the search. A candidate that does not exist or
// cannot be run for want of a file on its way (a script whose interpreter is
// missing) is passed over; one that is not permitted to run is passed over
// too but remembered, so that when nothing else is found the failure is
// "permission denied" and not "not found" (exec.LookPath reports the latter).
// Any other failure ends the search.
func runProgram(ctx context.Context, program string, args []string, input *string) (string, error) {
	denied := false
	for _, path := range searchPath(program) {
		cmd := &exec.Cmd{Path: path, Args: append([]string{program}, args...)}
		if runtime.GOOS == "darwin" {
			cmd.Env = append(os.Environ(), "LC_CTYPE=en_US.UTF-8")
		}
		result, err := run(ctx, cmd, input, helperTimeout)
		var errno syscall.Errno
		if !errors.As(err, &errno) {
			return result, err
		}
		switch errno {
		case syscall.EACCES:
			denied = true
		case syscall.ENOENT, syscall.ENOTDIR, syscall.ESTALE, syscall.ENODEV, syscall.ETIMEDOUT:
		default:
			return result, err
		}
	}
	if denied {
		return "", syscall.EACCES
	}
	return "", syscall.ENOENT
}

// Copy puts text on the desktop clipboard through the first available
// utility: wl-copy, xclip or xsel (Linux, by WAYLAND_DISPLAY and DISPLAY) or
// pbcopy (macOS). It fails when there is none, and never falls back to a
// terminal escape sequence.
func Copy(ctx context.Context, text string) error {
	_, err := transfer(ctx, &text)
	return err
}

// Paste returns the text on the desktop clipboard, which may be empty.
func Paste(ctx context.Context) (string, error) {
	return transfer(ctx, nil)
}
