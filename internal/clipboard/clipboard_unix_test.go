//go:build unix

package clipboard

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperScript = "#!/bin/sh\ncase \"$1\" in\ncopy) cat > \"$2\";;\npaste) cat \"$2\";;\nfail) exit 7;;\nhang) exec sleep 10;;\ninvalid) printf '\\377';;\nesac\n"

func writeExecutable(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// Rust: clipboard::tests::fixtures_roundtrip_utf8_empty_failures_timeout_and_limits
func TestFixturesRoundtripUTF8EmptyFailuresTimeoutAndLimits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	writeExecutable(t, helper, helperScript, 0o700)
	store := filepath.Join(dir, "clipboard")
	for _, text := range []string{"", "é界\\literal\nsecond\r\n", "one\ntwo\n"} {
		if _, err := run(ctx, exec.Command(helper, "copy", store), &text, helperTimeout); err != nil {
			t.Fatalf("copy %q: %v", text, err)
		}
		got, err := run(ctx, exec.Command(helper, "paste", store), nil, helperTimeout)
		if err != nil || got != text {
			t.Errorf("paste after copying %q = %q, %v", text, got, err)
		}
	}
	if _, err := run(ctx, exec.Command(helper, "fail"), nil, helperTimeout); err == nil {
		t.Error("a failing helper must be an error")
	}
	if _, err := run(ctx, exec.Command(helper, "invalid"), nil, helperTimeout); err == nil {
		t.Error("invalid UTF-8 must be an error")
	}
	if _, err := run(ctx, exec.Command(helper, "hang"), nil, 30*time.Millisecond); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("hanging helper: %v, want a timeout", err)
	}
	if _, err := run(ctx, exec.Command(filepath.Join(dir, "missing")), nil, helperTimeout); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing helper: %v, want not found", err)
	}
	denied := filepath.Join(dir, "not-executable")
	writeExecutable(t, denied, "#!/bin/sh\nexit 0\n", 0o600)
	if _, err := run(ctx, exec.Command(denied), nil, helperTimeout); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("non-executable helper: %v, want permission denied", err)
	}
	big := strings.Repeat("x", limit+1)
	if _, err := run(ctx, exec.Command(helper, "copy"), &big, helperTimeout); err == nil {
		t.Error("text over 8 MiB must be an error")
	}
	if err := os.WriteFile(store, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(ctx, exec.Command(helper, "paste", store), nil, helperTimeout); err == nil {
		t.Error("output over 8 MiB must be an error")
	}
}

func TestRunMessages(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	writeExecutable(t, helper, helperScript+"", 0o700)
	writeExecutable(t, filepath.Join(dir, "killed"), "#!/bin/sh\nkill -9 $$\n", 0o700)
	writeExecutable(t, filepath.Join(dir, "exit0"), "#!/bin/sh\nexit 0\n", 0o700)
	big := strings.Repeat("x", limit+1)
	exactly := strings.Repeat("é", limit/2) // exactly 8 MiB: allowed
	for _, c := range []struct {
		name  string
		cmd   *exec.Cmd
		input *string
		want  string
	}{
		{"exit status", exec.Command(helper, "fail"), nil, "clipboard helper failed (exit status: 7); check desktop access/clipboard contents"},
		{"signal", exec.Command(filepath.Join(dir, "killed")), nil, "clipboard helper failed (signal: 9 (SIGKILL)); check desktop access/clipboard contents"},
		{"invalid UTF-8", exec.Command(helper, "invalid"), nil, "clipboard does not contain UTF-8 text"},
		{"timeout", exec.Command(helper, "hang"), nil, "clipboard helper timed out"},
		{"input over the limit", exec.Command(helper, "copy"), &big, "clipboard text exceeds 8 MiB"},
		// The limit comes before the program is looked at.
		{"input over the limit, missing program", exec.Command(filepath.Join(dir, "absent")), &big, "clipboard text exceeds 8 MiB"},
	} {
		timeout := helperTimeout
		if c.name == "timeout" {
			timeout = 30 * time.Millisecond
		}
		_, err := run(ctx, c.cmd, c.input, timeout)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
	// 8 MiB exactly passes in both directions.
	store := filepath.Join(dir, "store")
	if _, err := run(ctx, exec.Command(helper, "copy", store), &exactly, helperTimeout); err != nil {
		t.Fatalf("copy of exactly 8 MiB: %v", err)
	}
	if got, err := run(ctx, exec.Command(helper, "paste", store), nil, helperTimeout); err != nil || got != exactly {
		t.Errorf("paste of exactly 8 MiB: %d bytes, %v", len(got), err)
	}
	// A successful helper that prints nothing is an empty clipboard, and a
	// copy ignores whatever the helper says.
	if got, err := run(ctx, exec.Command(filepath.Join(dir, "exit0")), nil, helperTimeout); err != nil || got != "" {
		t.Errorf("silent helper = %q, %v", got, err)
	}
	text := "x"
	if got, err := run(ctx, exec.Command(filepath.Join(dir, "exit0")), &text, helperTimeout); err != nil || got != "" {
		t.Errorf("copy through silent helper = %q, %v", got, err)
	}
}

func TestRunKillsAndReapsTheHelper(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	writeExecutable(t, helper, helperScript+"", 0o700)
	writeExecutable(t, filepath.Join(dir, "flood"), "#!/bin/sh\nhead -c 9000000 /dev/zero\nexec sleep 10\n", 0o700)

	reaped := func(name string, cmd *exec.Cmd) {
		t.Helper()
		if cmd.ProcessState == nil {
			t.Errorf("%s: helper was not waited for", name)
		} else if cmd.ProcessState.Success() || cmd.ProcessState.Exited() {
			t.Errorf("%s: helper was not killed: %v", name, cmd.ProcessState)
		}
	}

	cmd := exec.Command(helper, "hang")
	start := time.Now()
	if _, err := run(context.Background(), cmd, nil, 50*time.Millisecond); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout took %v", elapsed)
	}
	reaped("timeout", cmd)

	// Output over the limit stops a helper that is still running.
	cmd = exec.Command(filepath.Join(dir, "flood"))
	start = time.Now()
	if _, err := run(context.Background(), cmd, nil, 20*time.Second); err != errTooLarge {
		t.Fatalf("flood: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("limit took %v to be noticed", elapsed)
	}
	reaped("flood", cmd)

	// Cancelling the context kills the helper too.
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	cmd = exec.Command(helper, "hang")
	start = time.Now()
	if _, err := run(ctx, cmd, nil, 20*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("cancel took %v", elapsed)
	}
	reaped("cancel", cmd)

	// A deadline on the context is reported as such, not as the helper timeout.
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cmd = exec.Command(helper, "hang")
	if _, err := run(ctx, cmd, nil, 20*time.Second); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("context deadline: %v", err)
	}
	reaped("context deadline", cmd)

	// A context that is already done never starts the helper.
	done, cancel := context.WithCancel(context.Background())
	cancel()
	cmd = exec.Command(helper, "hang")
	if _, err := run(done, cmd, nil, 20*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
	if cmd.Process != nil {
		t.Error("helper was started with a cancelled context")
	}
}

func TestRunLeavesNoTemporaryFilesBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx := context.Background()
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	writeExecutable(t, helper, helperScript+"", 0o700)
	// While it runs, the helper sees nothing in TMPDIR: its stdin and stdout
	// are nameless.
	writeExecutable(t, filepath.Join(dir, "listing"), "#!/bin/sh\nls -A \"$TMPDIR\" | wc -l\n", 0o700)
	denied := filepath.Join(dir, "not-executable")
	writeExecutable(t, denied, "#!/bin/sh\nexit 0\n", 0o600)
	store := filepath.Join(dir, "store")
	big := strings.Repeat("x", limit+1)

	if got, err := run(ctx, exec.Command(filepath.Join(dir, "listing")), nil, helperTimeout); err != nil || strings.TrimSpace(got) != "0" {
		t.Errorf("TMPDIR seen by the helper holds %q entries (%v)", strings.TrimSpace(got), err)
	}
	text := "hello"
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"copy", func() error {
			_, err := run(ctx, exec.Command(helper, "copy", store), &text, helperTimeout)
			return err
		}},
		{"paste", func() error {
			_, err := run(ctx, exec.Command(helper, "paste", store), nil, helperTimeout)
			return err
		}},
		{"fail", func() error { _, err := run(ctx, exec.Command(helper, "fail"), nil, helperTimeout); return err }},
		{"invalid", func() error { _, err := run(ctx, exec.Command(helper, "invalid"), nil, helperTimeout); return err }},
		{"hang", func() error { _, err := run(ctx, exec.Command(helper, "hang"), nil, 30*time.Millisecond); return err }},
		{"missing", func() error {
			_, err := run(ctx, exec.Command(filepath.Join(dir, "none")), nil, helperTimeout)
			return err
		}},
		{"denied", func() error { _, err := run(ctx, exec.Command(denied), nil, helperTimeout); return err }},
		{"large", func() error { _, err := run(ctx, exec.Command(helper, "copy"), &big, helperTimeout); return err }},
		{"flood", func() error {
			if err := os.WriteFile(store, []byte(big), 0o600); err != nil {
				return err
			}
			_, err := run(ctx, exec.Command(helper, "paste", store), nil, helperTimeout)
			return err
		}},
		{"cancelled", func() error {
			c, cancel := context.WithCancel(ctx)
			cancel()
			_, err := run(c, exec.Command(helper, "hang"), nil, helperTimeout)
			return err
		}},
	} {
		err := c.call()
		name := c.name
		switch name {
		case "copy", "paste":
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		default:
			if err == nil {
				t.Errorf("%s: expected an error", name)
			}
		}
		entries, readErr := os.ReadDir(tmp)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(entries) != 0 {
			t.Errorf("%s left %d entries in TMPDIR, first %q", name, len(entries), entries[0].Name())
			for _, e := range entries {
				_ = os.Remove(filepath.Join(tmp, e.Name()))
			}
		}
	}
}

func TestCreateUnlinkedFallback(t *testing.T) {
	// The fallback of tempFile, used where O_TMPFILE is unavailable.
	dir := t.TempDir()
	file, err := createUnlinked(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("named file remains: %v", entries)
	}
	if _, err := file.WriteString("data"); err != nil {
		t.Fatal(err)
	}
	if info, err := file.Stat(); err != nil || info.Size() != 4 || info.Mode().Perm() != 0o600 {
		t.Errorf("fallback file: %v, %v", info, err)
	}
	if _, err := createUnlinked(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("createUnlinked in a missing directory: %v", err)
	}
	// Where tempFile cannot make a file, the error says so as the Rust one
	// does; a missing temporary directory reads as "not found".
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	if _, err := tempFile(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("tempFile with a missing TMPDIR: %v", err)
	}
}

func TestTempFilesArePrivateAndUsable(t *testing.T) {
	file, err := tempFile()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("temporary file is accessible to others: %v", info.Mode())
	}
}

// Fake clipboard helpers on PATH, like tests/pty_smoke.py's fixtures: the
// editor looks the programs up when it copies or pastes.

const fakeHelper = `#!/bin/sh
name=${0##*/}
if [ "$COIGUE_CLIPBOARD_FAIL" = 1 ]; then exit 7; fi
{
  printf '%s' "$name"
  for a in "$@"; do printf ' [%s]' "$a"; done
  printf ' lc_ctype=%s\n' "$LC_CTYPE"
} >> "$COIGUE_CLIPBOARD_LOG"
case "$name" in
wl-copy|pbcopy) /bin/cat > "$COIGUE_CLIPBOARD_STORE";;
wl-paste|pbpaste) /bin/cat "$COIGUE_CLIPBOARD_STORE";;
*) case "$*" in
   *-in*|*--input*) /bin/cat > "$COIGUE_CLIPBOARD_STORE";;
   *) /bin/cat "$COIGUE_CLIPBOARD_STORE";;
   esac;;
esac
`

type fixture struct {
	dir   string // the only PATH entry
	store string
	log   string
}

func newFixture(t *testing.T, programs ...string) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{dir: dir, store: filepath.Join(dir, "store-file"), log: filepath.Join(dir, "log-file")}
	for _, name := range programs {
		writeExecutable(t, filepath.Join(dir, name), fakeHelper, 0o700)
	}
	t.Setenv("PATH", dir)
	t.Setenv("COIGUE_CLIPBOARD_STORE", f.store)
	t.Setenv("COIGUE_CLIPBOARD_LOG", f.log)
	t.Setenv("COIGUE_CLIPBOARD_FAIL", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	return f
}

func (f *fixture) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func skipOnMac(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS always uses pbcopy/pbpaste")
	}
}

func TestCopyPasteThroughWaylandHelpers(t *testing.T) {
	skipOnMac(t)
	f := newFixture(t, "wl-copy", "wl-paste", "xclip", "xsel")
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	ctx := context.Background()
	if err := Copy(ctx, "aé界"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(f.store); string(data) != "aé界" {
		t.Errorf("store = %q", data)
	}
	got, err := Paste(ctx)
	if err != nil || got != "aé界" {
		t.Errorf("Paste = %q, %v", got, err)
	}
	want := []string{
		"wl-copy [--type] [text/plain;charset=utf-8] lc_ctype=" + os.Getenv("LC_CTYPE"),
		"wl-paste [--no-newline] [--type] [text] lc_ctype=" + os.Getenv("LC_CTYPE"),
	}
	if calls := f.calls(t); !slices.Equal(calls, want) {
		t.Errorf("helper calls:\n got %q\nwant %q", calls, want)
	}
}

func TestCopyPasteThroughX11Helpers(t *testing.T) {
	skipOnMac(t)
	f := newFixture(t, "wl-copy", "wl-paste", "xclip", "xsel")
	t.Setenv("DISPLAY", ":0")
	ctx := context.Background()
	if err := Copy(ctx, "one\ntwo"); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "one\ntwo" {
		t.Errorf("Paste = %q, %v", got, err)
	}
	lc := " lc_ctype=" + os.Getenv("LC_CTYPE")
	want := []string{"xclip [-selection] [clipboard] [-in]" + lc, "xclip [-selection] [clipboard] [-out]" + lc}
	if calls := f.calls(t); !slices.Equal(calls, want) {
		t.Errorf("helper calls:\n got %q\nwant %q", calls, want)
	}
}

func TestMissingUtilityFallsThroughToTheNextBackend(t *testing.T) {
	skipOnMac(t)
	f := newFixture(t, "xsel")
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	t.Setenv("DISPLAY", ":0")
	ctx := context.Background()
	// wl-copy and xclip are not installed; xsel is the third choice.
	if err := Copy(ctx, "é界"); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "é界" {
		t.Errorf("Paste = %q, %v", got, err)
	}
	lc := " lc_ctype=" + os.Getenv("LC_CTYPE")
	want := []string{"xsel [--clipboard] [--input]" + lc, "xsel [--clipboard] [--output]" + lc}
	if calls := f.calls(t); !slices.Equal(calls, want) {
		t.Errorf("helper calls:\n got %q\nwant %q", calls, want)
	}
	// With Wayland only, the missing wl-clipboard is the end of the line.
	t.Setenv("DISPLAY", "")
	err := Copy(ctx, "x")
	if err == nil || err.Error() != string(errMissing) {
		t.Errorf("Copy without any helper: %v", err)
	}
	if _, err := Paste(ctx); err == nil || err.Error() != "Clipboard utility missing: use wl-clipboard (Wayland), xclip/xsel (X11), or pbcopy/pbpaste (macOS); internal y/p still work" {
		t.Errorf("Paste without any helper: %v", err)
	}
}

func TestNoDesktopSession(t *testing.T) {
	skipOnMac(t)
	newFixture(t, "wl-copy", "wl-paste", "xclip", "xsel")
	want := "No desktop clipboard available (SSH/headless); internal y/p still work"
	if err := Copy(context.Background(), "x"); err == nil || err.Error() != want {
		t.Errorf("Copy = %v", err)
	}
	if _, err := Paste(context.Background()); err == nil || err.Error() != want {
		t.Errorf("Paste = %v", err)
	}
}

func TestHelperFailureAndTextErrors(t *testing.T) {
	skipOnMac(t)
	f := newFixture(t, "wl-copy", "wl-paste")
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	ctx := context.Background()

	t.Setenv("COIGUE_CLIPBOARD_FAIL", "1")
	wantFail := "wl-copy: clipboard helper failed (exit status: 7); check desktop access/clipboard contents"
	if err := Copy(ctx, "abc"); err == nil || err.Error() != wantFail {
		t.Errorf("Copy = %v, want %q", err, wantFail)
	}
	if _, err := Paste(ctx); err == nil || err.Error() != strings.Replace(wantFail, "wl-copy", "wl-paste", 1) {
		t.Errorf("Paste = %v", err)
	}
	t.Setenv("COIGUE_CLIPBOARD_FAIL", "")

	// The clipboard holds bytes that are not UTF-8.
	if err := os.WriteFile(f.store, []byte{'a', 0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Paste(ctx); err == nil || err.Error() != "wl-paste: clipboard does not contain UTF-8 text" {
		t.Errorf("Paste of invalid UTF-8 = %v", err)
	}
	// An empty clipboard is not an error.
	if err := os.WriteFile(f.store, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "" {
		t.Errorf("Paste of nothing = %q, %v", got, err)
	}
	// Text over 8 MiB is refused before any helper runs.
	if err := os.Remove(f.log); err != nil {
		t.Fatal(err)
	}
	err := Copy(ctx, strings.Repeat("x", limit+1))
	if err == nil || err.Error() != "wl-copy: clipboard text exceeds 8 MiB" {
		t.Errorf("Copy of 8 MiB + 1 = %v", err)
	}
	if calls := f.calls(t); len(calls) != 0 {
		t.Errorf("helpers ran: %q", calls)
	}
	// ... and the check is not hidden by a missing program.
	if err := os.Remove(filepath.Join(f.dir, "wl-copy")); err != nil {
		t.Fatal(err)
	}
	if err := Copy(ctx, strings.Repeat("x", limit+1)); err == nil || err.Error() != "wl-copy: clipboard text exceeds 8 MiB" {
		t.Errorf("Copy of 8 MiB + 1 without wl-copy = %v", err)
	}
	if err := Copy(ctx, strings.Repeat("x", limit)); err == nil || err.Error() != string(errMissing) {
		t.Errorf("Copy of 8 MiB without wl-copy = %v", err)
	}
}

func TestContextCancelsTheHelper(t *testing.T) {
	skipOnMac(t)
	f := newFixture(t, "wl-paste")
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	writeExecutable(t, filepath.Join(f.dir, "wl-paste"), "#!/bin/sh\nexec /bin/sleep 10\n", 0o700)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Paste(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || err.Error() != "wl-paste: context deadline exceeded" {
		t.Errorf("Paste = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("cancellation was not prompt")
	}
	done, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := Paste(done); !errors.Is(err, context.Canceled) {
		t.Errorf("Paste with cancelled context = %v", err)
	}
	if err := Copy(done, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("Copy with cancelled context = %v", err)
	}
}

func TestLookupFollowsExecvp(t *testing.T) {
	skipOnMac(t)
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	t.Setenv("DISPLAY", "")
	t.Setenv("COIGUE_CLIPBOARD_FAIL", "")
	store := filepath.Join(second, "store")
	t.Setenv("COIGUE_CLIPBOARD_STORE", store)
	t.Setenv("COIGUE_CLIPBOARD_LOG", filepath.Join(second, "log"))
	ctx := context.Background()

	// A non-executable file is passed over when a later directory has the program.
	writeExecutable(t, filepath.Join(first, "wl-copy"), fakeHelper, 0o600)
	writeExecutable(t, filepath.Join(second, "wl-copy"), fakeHelper, 0o700)
	// A directory of that name is not a program either.
	if err := os.Mkdir(filepath.Join(first, "wl-paste"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(second, "wl-paste"), fakeHelper, 0o700)
	t.Setenv("PATH", first+":"+second)
	if err := Copy(ctx, "found"); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "found" {
		t.Errorf("Paste = %q, %v", got, err)
	}

	// When nothing executable exists, the answer is "permission denied", not
	// "not found": the editor must not claim the utility is missing.
	t.Setenv("PATH", first)
	err := Copy(ctx, "x")
	if err == nil || err.Error() != "wl-copy: Permission denied (os error 13)" || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Copy with only a non-executable wl-copy = %v", err)
	}
	// Empty and missing directories in PATH are ignored; an empty entry is the current directory.
	t.Setenv("PATH", filepath.Join(first, "nope")+"::"+second)
	t.Chdir(first)
	if err := Copy(ctx, "again"); err != nil {
		t.Errorf("Copy with an empty PATH entry: %v", err)
	}
	t.Setenv("PATH", "")
	t.Chdir(second)
	if err := Copy(ctx, "cwd"); err != nil {
		t.Errorf("Copy with PATH empty (current directory): %v", err)
	}
	if data, _ := os.ReadFile(store); string(data) != "cwd" {
		t.Errorf("store = %q", data)
	}
}

func TestSearchPath(t *testing.T) {
	t.Setenv("PATH", "/a:/b/c::rel:.")
	got := searchPath("tool")
	want := []string{"/a/tool", "/b/c/tool", "./tool", "rel/tool", "./tool"}
	if !slices.Equal(got, want) {
		t.Errorf("searchPath(tool) = %q, want %q", got, want)
	}
	// A program with a slash is not searched for.
	if got := searchPath("./anything/here"); !slices.Equal(got, []string{"./anything/here"}) {
		t.Errorf("searchPath with a slash = %q", got)
	}
	// An empty PATH is the current directory; an unset one is the system default.
	t.Setenv("PATH", "")
	if got := searchPath("tool"); !slices.Equal(got, []string{"./tool"}) {
		t.Errorf("searchPath with an empty PATH = %q", got)
	}
	if err := os.Unsetenv("PATH"); err != nil {
		t.Fatal(err)
	}
	if got := searchPath("tool"); !slices.Equal(got, []string{"/bin/tool", "/usr/bin/tool"}) {
		t.Errorf("searchPath with PATH unset = %q", got)
	}
}

func TestRunProgramClassifiesFailuresLikeExecvp(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("PATH", first+":"+second)
	t.Setenv("COIGUE_CLIPBOARD_STORE", filepath.Join(second, "store"))
	t.Setenv("COIGUE_CLIPBOARD_LOG", filepath.Join(second, "log"))
	t.Setenv("COIGUE_CLIPBOARD_FAIL", "")
	ctx := context.Background()
	call := func() (string, error) { return runProgram(ctx, "tool", nil, nil) }

	// Nothing named tool anywhere: not found.
	if _, err := call(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no tool: %v", err)
	}
	// A directory and a plain file are passed over, but remembered.
	if err := os.Mkdir(filepath.Join(first, "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(second, "tool"), "plain text\n", 0o644)
	if _, err := call(); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("only unexecutable candidates: %v, want permission denied", err)
	}
	// A script whose interpreter is missing is passed over like a missing file.
	if err := os.Remove(filepath.Join(first, "tool")); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(second, "tool"), "#!/nonexistent/interpreter\n", 0o755)
	if _, err := call(); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("broken interpreter: %v, want not found", err)
	}
	// ... and the search goes on to a good one in a later directory.
	writeExecutable(t, filepath.Join(first, "tool2"), "#!/nonexistent/interpreter\n", 0o755)
	writeExecutable(t, filepath.Join(second, "tool2"), "#!/bin/sh\nprintf found\n", 0o755)
	if got, err := runProgram(ctx, "tool2", nil, nil); err != nil || got != "found" {
		t.Errorf("tool2 = %q, %v", got, err)
	}
	// A file that cannot be executed at all ends the search with its own error.
	writeExecutable(t, filepath.Join(first, "tool3"), "no shebang\n", 0o755)
	writeExecutable(t, filepath.Join(second, "tool3"), "#!/bin/sh\nprintf found\n", 0o755)
	if _, err := runProgram(ctx, "tool3", nil, nil); !errors.Is(err, syscall.ENOEXEC) {
		t.Errorf("tool3: %v, want exec format error", err)
	}
	// A program given with a slash is run as it is.
	if got, err := runProgram(ctx, filepath.Join(second, "tool2"), nil, nil); err != nil || got != "found" {
		t.Errorf("tool2 by path = %q, %v", got, err)
	}
}

func TestMacOSSetsLCCTYPEOnly(t *testing.T) {
	// The helpers are told to use UTF-8 on macOS only; elsewhere the
	// environment is passed through untouched.
	f := newFixture(t, "wl-paste", "pbpaste")
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	t.Setenv("LC_CTYPE", "sentinel")
	if err := os.WriteFile(f.store, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Paste(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "sentinel"
	if runtime.GOOS == "darwin" {
		want = "en_US.UTF-8"
	}
	calls := f.calls(t)
	if len(calls) != 1 || !strings.HasSuffix(calls[0], " lc_ctype="+want) {
		t.Errorf("helper calls = %q, want LC_CTYPE %q", calls, want)
	}
}

// The PTY suite (tests/pty_smoke.py) puts these Python helpers alone on PATH.
func TestPTYSuiteFixtureHelpers(t *testing.T) {
	skipOnMac(t)
	out, err := exec.Command("python3", "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Skip("python3 is not available")
	}
	python := strings.TrimSpace(string(out))
	script := "#!" + python + "\n" + `import os, pathlib, sys
if os.environ.get('COIGUE_CLIPBOARD_FAIL') == '1': sys.exit(7)
name = pathlib.Path(sys.argv[0]).name
copy = name in ('wl-copy', 'pbcopy') or '-in' in sys.argv or '--input' in sys.argv
store = pathlib.Path(os.environ['COIGUE_CLIPBOARD_STORE'])
if copy: store.write_bytes(sys.stdin.buffer.read())
else: sys.stdout.buffer.write(store.read_bytes())
`
	helpers := t.TempDir()
	store := filepath.Join(t.TempDir(), "fixture-clipboard")
	for _, name := range []string{"wl-copy", "wl-paste", "pbcopy", "pbpaste", "xclip", "xsel"} {
		writeExecutable(t, filepath.Join(helpers, name), script, 0o700)
	}
	t.Setenv("PATH", helpers)
	t.Setenv("WAYLAND_DISPLAY", "coigue-fixture")
	t.Setenv("DISPLAY", "")
	t.Setenv("COIGUE_CLIPBOARD_STORE", store)
	t.Setenv("COIGUE_CLIPBOARD_FAIL", "")
	ctx := context.Background()

	if err := Copy(ctx, "aé界"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(store); string(data) != "aé界" {
		t.Errorf("store = %q", data)
	}
	if got, err := Paste(ctx); err != nil || got != "aé界" {
		t.Errorf("Paste = %q, %v", got, err)
	}
	// External Unicode and multi-line text come back unchanged.
	if err := os.WriteFile(store, []byte("界\r\nnext\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "界\r\nnext\n" {
		t.Errorf("Paste = %q, %v", got, err)
	}
	// X11 only, with xclip removed: the fixture xsel takes over.
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "coigue-fixture")
	if err := os.Remove(filepath.Join(helpers, "xclip")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, []byte("é界"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "é界" {
		t.Errorf("Paste through xsel = %q, %v", got, err)
	}
	// An empty clipboard pastes as the empty string.
	if err := os.WriteFile(store, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Paste(ctx); err != nil || got != "" {
		t.Errorf("Paste of nothing = %q, %v", got, err)
	}
	// COIGUE_CLIPBOARD_FAIL=1 makes the helper exit with status 7.
	t.Setenv("COIGUE_CLIPBOARD_FAIL", "1")
	if err := Copy(ctx, "abc"); err == nil || err.Error() != "xsel: clipboard helper failed (exit status: 7); check desktop access/clipboard contents" {
		t.Errorf("Copy = %v", err)
	}
	if _, err := Paste(ctx); err == nil || !strings.HasPrefix(err.Error(), "xsel: clipboard helper failed (exit status: 7)") {
		t.Errorf("Paste = %v", err)
	}
}
