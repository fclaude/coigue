//go:build unix

package lsp

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fclaude/coigue/internal/fsx"
)

// These tests cover what the Rust Drop guards and mpsc channels gave for free
// and the Rust tests did not spell out: the child is killed and reaped on
// every way out of serve, cancellation is prompt, and bounded queues report
// "full" instead of blocking.

// processGone reports whether pid no longer exists (it was reaped).
func processGone(pid int) bool {
	return syscall.Kill(pid, 0) == syscall.ESRCH
}

const pidServer = `
import os, sys, time
with open(sys.argv[1] + '.tmp', 'w') as f: f.write(str(os.getpid()))
os.replace(sys.argv[1] + '.tmp', sys.argv[1])
mode = sys.argv[2]
if mode == 'garbage':
    sys.stdout.write('garbage\r\n\r\n'); sys.stdout.flush(); time.sleep(60)
elif mode == 'exit':
    sys.exit(3)
else:
    sys.stdin.buffer.read()
    time.sleep(60)
`

func startPidServer(t *testing.T, mode string, startup time.Duration, ctx context.Context) (pid int, in *commands, done chan error) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "server.py")
	if err := os.WriteFile(script, []byte(pidServer), 0o666); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "pid")
	ctx, cancel := context.WithCancel(ctx)
	child, err := spawn(ctx, "python3", []string{"-u", script, pidFile, mode}, dir)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	started := false
	defer func() {
		if !started {
			cancel()
			child.close()
		}
	}()
	// Wait until the child has actually started before starting the protocol
	// deadline. A slow interpreter must not race the test's timeout.
	pid = readPid(t, pidFile)
	if pid != child.cmd.Process.Pid {
		t.Fatalf("server reported pid %d, spawned %d", pid, child.cmd.Process.Pid)
	}
	commands, inbox := mailbox()
	events := make(chan output, 64)
	done = make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- serveChild(ctx, child, "python3", dir, "0.1.0", inbox, events, startup)
	}()
	started = true
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return pid, commands, done
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil && len(data) > 0 {
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the server never started")
	return 0
}

func TestServeKillsAndReapsTheChildOnEveryExit(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		startup    time.Duration
		stop       bool
		wantErr    string
	}{
		{"malformed output", "garbage", time.Second, false, "invalid LSP header"},
		{"initialization timeout", "silent", 200 * time.Millisecond, false, "timed out"},
		{"stop before initialization", "silent", 5 * time.Second, true, ""},
		{"server exits", "exit", time.Second, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid, commands, done := startPidServer(t, tc.mode, tc.startup, context.Background())
			if tc.stop {
				commands.trySend(inputStop{})
			}
			select {
			case err := <-done:
				switch {
				case tc.stop:
					if err != nil {
						t.Errorf("an orderly stop returned %v", err)
					}
				case err == nil || !strings.Contains(err.Error(), tc.wantErr):
					t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("serve did not return")
			}
			if !processGone(pid) {
				t.Errorf("child %d was not reaped when serve returned", pid)
			}
		})
	}
}

func TestContextCancellationStopsServeAndKillsTheChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pid, _, done := startPidServer(t, "silent", time.Minute, ctx)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled server run reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve ignored the cancelled context")
	}
	if !processGone(pid) {
		t.Errorf("child %d survived cancellation", pid)
	}
}

func TestSpawnErrorsReadLikeRustIOErrors(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	root := t.TempDir()
	try := func() string {
		child, err := spawn(context.Background(), "rust-analyzer", nil, root)
		if err == nil {
			child.close()
			return ""
		}
		return spawnErrorText("rust-analyzer", err)
	}
	if got, want := try(), "No such file or directory (os error 2)"; got != want {
		t.Errorf("missing program: %q, want %q", got, want)
	}
	program := filepath.Join(bin, "rust-analyzer")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := try(), "Permission denied (os error 13)"; got != want {
		t.Errorf("program without execute permission: %q, want %q", got, want)
	}
	if err := os.WriteFile(program, []byte("not a program"), 0o755); err != nil || os.Chmod(program, 0o755) != nil {
		t.Fatal(err)
	}
	if got, want := try(), "Exec format error (os error 8)"; got != want {
		t.Errorf("program in no format: %q, want %q", got, want)
	}
	// A working directory that does not exist is reported the same way.
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil || os.Chmod(program, 0o755) != nil {
		t.Fatal(err)
	}
	_, err := spawn(context.Background(), "rust-analyzer", nil, filepath.Join(root, "absent"))
	if err == nil {
		t.Fatal("spawned in a missing directory")
	}
	if got, want := spawnErrorText("rust-analyzer", err), "No such file or directory (os error 2)"; got != want {
		t.Errorf("missing directory: %q, want %q", got, want)
	}
}

// Rust: std::process::ExitStatus Display, from unix.rs.
func TestExitStatusTextMatchesRustDisplay(t *testing.T) {
	for _, tc := range []struct{ script, want string }{
		{"exit 7", "exit status: 7"},
		{"exit 0", "exit status: 0"},
		{"kill -9 $$", "signal: 9 (SIGKILL)"},
		{"kill -15 $$", "signal: 15 (SIGTERM)"},
		{"kill -11 $$", "signal: 11 (SIGSEGV)"},
	} {
		cmd := exec.Command("sh", "-c", tc.script)
		_ = cmd.Run()
		if got := exitStatusText(cmd.ProcessState); got != tc.want && !(tc.script == "kill -11 $$" && strings.HasPrefix(got, tc.want)) {
			t.Errorf("%q: %q, want %q", tc.script, got, tc.want)
		}
	}
	if runtime.GOOS == "linux" {
		if got := signalName(16); got != " (SIGSTKFLT)" {
			t.Errorf("signal 16 = %q", got)
		}
		if got := signalName(30); got != " (SIGPWR)" {
			t.Errorf("signal 30 = %q", got)
		}
	}
	if got := signalName(syscall.Signal(63)); got != "" {
		t.Errorf("an unknown signal has name %q", got)
	}
}

func TestClientCloseWaitsBoundedlyAndIsIdempotent(t *testing.T) {
	s := newSmoke(t, "normal")
	path := s.write("close.rs", "text")
	s.sync(path, 0, 0, 0, "text")
	s.wait("the warning", 4*time.Second, s.warning("rust-analyzer"))
	start := time.Now()
	s.client.Close()
	if elapsed := time.Since(start); elapsed > time.Second+500*time.Millisecond {
		t.Errorf("Close took %v", elapsed)
	}
	s.assertReaped()
	start = time.Now()
	s.client.Close()
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("the second Close took %v", elapsed)
	}
	// A closed client ignores further syncs.
	s.sync(path, 0, 1, 0, "text again")
	if s.client.session != nil {
		t.Error("Sync after Close started a session")
	}
}

func TestClientContextBoundsTheServers(t *testing.T) {
	s := newSmoke(t, "normal")
	path := s.write("bound.rs", "text")
	s.sync(path, 0, 0, 0, "text")
	s.wait("the warning", 4*time.Second, s.warning("rust-analyzer"))
	pid := 0
	for _, entry := range s.records("rust-analyzer") {
		if v, ok := asI64(index(entry, "pid")); ok {
			pid = int(v)
		}
	}
	s.cancel()
	s.wait("the failure", 4*time.Second, func() bool { _, failed := s.client.Failure(); return failed })
	if !processGone(pid) && !waitGone(pid) {
		t.Errorf("server %d survived the cancelled context", pid)
	}
}

func waitGone(pid int) bool {
	for i := 0; i < 100; i++ {
		if processGone(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestZeroValueClientDoesNothingUntilEnabled(t *testing.T) {
	var c Client
	called := false
	c.Sync("/tmp/x.rs", 0, 0, 0, func() string { called = true; return "" })
	c.Poll(nil)
	if called || c.session != nil || c.stamp != nil || c.Status != "" {
		t.Fatal("a client that was never enabled acted")
	}
	if err := c.Navigate(Definition, "", 0, 0, nil); err == nil ||
		err.Error() != "No language server available; use project search or install the server for this file" {
		t.Errorf("Navigate without a server: %v", err)
	}
	if e, w := c.Counts(); e != 0 || w != 0 {
		t.Error("counts")
	}
	if _, ok := c.Failure(); ok {
		t.Error("failure")
	}
	c.Close()
	c.Close()
}

func TestNavigateWithoutAnOpenDocumentSaysSo(t *testing.T) {
	input, _ := mailbox()
	var c Client
	c.session = &session{input: input}
	if err := c.Navigate(Symbols, "", 0, 0, nil); err == nil || err.Error() != "No document open" {
		t.Errorf("err = %v", err)
	}
}

func TestFullMailboxIsReportedNotWaitedFor(t *testing.T) {
	// A real directory with no symlinks: Sync canonicalizes the path it is given, and
	// /tmp is a symlink on macOS.
	dir, err := fsx.Canonicalize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.rs")
	input, in := mailbox()
	events := make(chan output, 1)
	var c Client
	c.Enable(context.Background())
	c.session = &session{input: input, output: events, finished: make(chan struct{})}
	c.stamp = &stamp{path: path}
	c.key = &languageKey{languageRust, dir}
	// Saves are never coalesced, so each Sync adds one command.
	for i := 0; i < queueLimit; i++ {
		input.trySend(inputDocument{document{uri: "file:///x" + strconv.Itoa(i), saveEvent: true}})
	}
	if err := c.Navigate(Definition, "", 0, 0, nil); err == nil || err.Error() != "Language server busy; retry navigation" {
		t.Errorf("Navigate with a full mailbox: %v", err)
	}
	if c.navigationPending != nil {
		t.Error("a refused request is pending")
	}
	if c.navigationSerial != 1 {
		t.Errorf("serial = %d: the Rust client spends a serial on a refused request", c.navigationSerial)
	}
	c.stamp = nil
	c.Sync(path, 0, 0, 0, func() string { return "" })
	if message, failed := c.Failure(); !failed || message != "LSP: server cannot keep up; :lsp-restart to retry" {
		t.Errorf("Sync with a full mailbox: %q, %v", message, failed)
	}
	// Stop is exempt from the capacity.
	if err := input.trySend(inputStop{}); err != nil {
		t.Errorf("Stop was refused: %v", err)
	}
	n := 0
	for {
		if _, ok := in.pop(); !ok {
			break
		}
		n++
	}
	if n != queueLimit+2 {
		t.Errorf("%d commands queued, want %d (32 saves, Stop from the failure, Stop above)", n, queueLimit+2)
	}
}

func TestDocumentVersionWrapsToOne(t *testing.T) {
	input, _ := mailbox()
	var c Client
	c.Enable(context.Background())
	c.session = &session{input: input, output: make(chan output), finished: make(chan struct{})}
	c.key = &languageKey{languageRust, "/tmp"}
	c.version = math.MaxInt32
	dir := t.TempDir()
	c.Sync(filepath.Join(dir, "v.rs"), 0, 0, 0, func() string { return "" })
	if c.version != 1 {
		t.Fatalf("version after overflow = %d, want 1", c.version)
	}
}

func TestPollHandlesAtMostSixtyFourEventsAndKeepsTheRest(t *testing.T) {
	events := make(chan output, 100)
	for i := 0; i < 100; i++ {
		events <- outputReady{}
	}
	var c Client
	c.key = &languageKey{languageGo, "/tmp"}
	c.session = &session{input: &commands{}, output: events, finished: make(chan struct{})}
	c.Poll(nil)
	if len(events) != 100-pollBound {
		t.Fatalf("%d events left, want %d", len(events), 100-pollBound)
	}
	if c.Status != "gopls" {
		t.Errorf("status = %q", c.Status)
	}
	c.Poll(nil)
	if len(events) != 0 {
		t.Errorf("%d events left after two polls", len(events))
	}
}

func TestSendNeverBlocksAndNoticesADeadWriter(t *testing.T) {
	tx := newOutbox(2)
	for i := 0; i < 2; i++ {
		if err := send(tx, object{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := send(tx, object{}); err != errPipeStalled {
		t.Errorf("a full queue: %v", err)
	}
	dead := newOutbox(8)
	close(dead.dead)
	if err := send(dead, object{}); err != errPipeStalled {
		t.Errorf("a dead writer: %v", err)
	}
	events := make(chan output, 1)
	trySend(events, outputReady{})
	trySend(events, outputReady{}) // dropped, not blocked
	if len(events) != 1 {
		t.Errorf("%d events queued", len(events))
	}
}

func TestInitializeRequestCarriesTheClientVersionAndLanguageOptions(t *testing.T) {
	for _, tc := range []struct {
		program, version string
		wantOptions      string
	}{
		{"rust-analyzer", "9.9.9", `null`},
		{"gopls", "0.1.0", `{"semanticTokens":true}`},
	} {
		request := initializeRequest(tc.program, "file:///tmp/", "tmp", tc.version)
		params := index(request, "params")
		expectJSON(t, index(index(params, "clientInfo"), "version"), `"`+tc.version+`"`)
		expectJSON(t, index(params, "initializationOptions"), tc.wantOptions)
		expectJSON(t, index(index(params, "workspaceFolders"), "0"), `null`)
	}
}

func TestClientVersionDefaultsToTheRustPackageVersion(t *testing.T) {
	var c Client
	if got := c.clientVersion(); got != "0.1.0" {
		t.Errorf("default version = %q", got)
	}
	c.Version = "2.0.0"
	if got := c.clientVersion(); got != "2.0.0" {
		t.Errorf("version = %q", got)
	}
}

// Rust: Command::spawn errors as serve reports them, for programs found,
// missing, not executable, directories or in no executable format, searched
// along several PATHs and started in existing and missing directories.
// testdata/spawn_golden.tsv: PATH, program, working directory, result
// ("ok" when the program started), with <S> for a scratch directory laid out
// by buildSpawnTree; produced by running the real serve in src/lsp.rs.
func buildSpawnTree(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The directory is called "spawn" because the golden file has a program
	// path that climbs out of it and back in.
	base := filepath.Join(resolved, "spawn")
	for _, dir := range []string{"bin", "bin/adir", "bin2"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []struct {
		name, content string
		mode          os.FileMode
	}{
		{"bin/ok-prog", "#!/bin/sh\nexit 0\n", 0o755},
		{"bin/noexec", "#!/bin/sh\n", 0o644},
		{"bin/badfmt", "garbage", 0o755},
		{"bin2/badfmt", "garbage", 0o644},
		{"bin2/noexec", "#!/bin/sh\nexit 0\n", 0o755},
	} {
		path := filepath.Join(base, file.name)
		if err := os.WriteFile(path, []byte(file.content), file.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, file.mode); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func TestSpawnErrorsAgreeWithRustGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/spawn_golden.tsv")
	if err != nil {
		t.Fatal(err)
	}
	base := buildSpawnTree(t)
	t.Chdir(base)
	cases := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range cases {
		fields := strings.Split(line, "\t")
		path, program, root, want := fields[0], fields[1], fields[2], fields[3]
		path = strings.ReplaceAll(path, "<S>", base)
		program = strings.ReplaceAll(program, "<S>", base)
		root = strings.ReplaceAll(root, "<S>", base)
		t.Setenv("PATH", path)
		got := "ok"
		child, err := spawn(context.Background(), program, nil, root)
		if err != nil {
			got = "unavailable (" + spawnErrorText(program, err) + ")"
		} else {
			child.close()
		}
		if got != want {
			t.Errorf("PATH=%q program %q in %q: %q, Rust %q", strings.ReplaceAll(path, base, "<S>"),
				strings.ReplaceAll(program, base, "<S>"), strings.ReplaceAll(root, base, "<S>"), got, want)
		}
	}
	if len(cases) < 200 {
		t.Fatalf("only %d cases", len(cases))
	}
}
