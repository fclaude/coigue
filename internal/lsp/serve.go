package lsp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/fclaude/coigue/internal/fsx"
	"github.com/fclaude/coigue/internal/guard"
	"github.com/fclaude/coigue/internal/semantic"
)

// process owns a running server. Closing it kills and reaps the child on
// every exit path: after malformed output, timeouts, a dropped UI or a
// failed protocol step.
type process struct {
	cmd    *exec.Cmd
	exited chan struct{}
	stdin  *os.File
	stdout *os.File
}

// spawn starts program in root with piped stdin and stdout and stderr
// discarded. The pipes are plain files so waiting for the child can never
// close them under a reader.
func spawn(ctx context.Context, program string, args []string, root string) (*process, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	if errors.Is(cmd.Err, exec.ErrDot) {
		cmd.Err = nil // a PATH entry of "." is searched like any other
	}
	if cmd.Err != nil {
		// The child changes directory before it searches for the program,
		// so that error wins when both apply.
		if err := chdirError(root); err != nil {
			return nil, err
		}
	}
	cmd.Dir = root
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		stdinRead.Close()
		stdinWrite.Close()
		return nil, err
	}
	cmd.Stdin = stdinRead
	cmd.Stdout = stdoutWrite
	err = cmd.Start()
	stdinRead.Close()
	stdoutWrite.Close()
	if err != nil {
		stdinWrite.Close()
		stdoutRead.Close()
		return nil, err
	}
	child := &process{cmd: cmd, exited: make(chan struct{}), stdin: stdinWrite, stdout: stdoutRead}
	go func() {
		_ = cmd.Wait()
		close(child.exited)
	}()
	return child, nil
}

// chdirError is the error the child's chdir(root) would report, if any.
func chdirError(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: root, Err: syscall.ENOTDIR}
	}
	if err := syscall.Access(root, 1); err != nil {
		return &os.PathError{Op: "chdir", Path: root, Err: err}
	}
	return nil
}

// tryWait is Child::try_wait.
func (p *process) tryWait() (string, bool) {
	select {
	case <-p.exited:
		return exitStatusText(p.cmd.ProcessState), true
	default:
		return "", false
	}
}

func (p *process) close() {
	_ = p.cmd.Process.Kill()
	<-p.exited
	p.stdin.Close()
	p.stdout.Close()
}

// exitStatusText renders a wait status like Rust's ExitStatus Display.
func exitStatusText(state *os.ProcessState) string {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return state.String()
	}
	switch {
	case status.Exited():
		return fmt.Sprintf("exit status: %d", status.ExitStatus())
	case status.Signaled():
		text := fmt.Sprintf("signal: %d%s", int(status.Signal()), signalName(status.Signal()))
		if status.CoreDump() {
			text += " (core dumped)"
		}
		return text
	case status.Stopped():
		return fmt.Sprintf("stopped (not terminated) by signal: %d%s", int(status.StopSignal()), signalName(status.StopSignal()))
	case status.Continued():
		return "continued (WIFCONTINUED)"
	}
	return state.String()
}

// signalName is signal_string from the Rust standard library.
func signalName(sig syscall.Signal) string {
	names := map[syscall.Signal]string{
		syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
		syscall.SIGILL: "SIGILL", syscall.SIGTRAP: "SIGTRAP", syscall.SIGABRT: "SIGABRT",
		syscall.SIGBUS: "SIGBUS", syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL",
		syscall.SIGUSR1: "SIGUSR1", syscall.SIGSEGV: "SIGSEGV", syscall.SIGUSR2: "SIGUSR2",
		syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
		syscall.SIGCHLD: "SIGCHLD", syscall.SIGCONT: "SIGCONT", syscall.SIGSTOP: "SIGSTOP",
		syscall.SIGTSTP: "SIGTSTP", syscall.SIGTTIN: "SIGTTIN", syscall.SIGTTOU: "SIGTTOU",
		syscall.SIGURG: "SIGURG", syscall.SIGXCPU: "SIGXCPU", syscall.SIGXFSZ: "SIGXFSZ",
		syscall.SIGVTALRM: "SIGVTALRM", syscall.SIGPROF: "SIGPROF", syscall.SIGWINCH: "SIGWINCH",
		syscall.SIGIO: "SIGIO", syscall.SIGSYS: "SIGSYS",
	}
	if name, ok := names[sig]; ok {
		return " (" + name + ")"
	}
	if runtime.GOOS == "linux" {
		switch sig {
		case 16:
			return " (SIGSTKFLT)"
		case 30:
			return " (SIGPWR)"
		}
	}
	return ""
}

// spawnErrorText is the io::Error text Rust's Command::spawn reports. Its
// program search follows execvp: an entry that exists but cannot be executed
// (no permission, or a directory) is "Permission denied", everything else not
// found "No such file or directory".
func spawnErrorText(program string, err error) string {
	if program == "" {
		return ioErrorText(syscall.ENOENT)
	}
	if errors.Is(err, exec.ErrNotFound) {
		errno := syscall.ENOENT
		for _, dir := range strings.Split(os.Getenv("PATH"), ":") {
			if dir == "" {
				dir = "."
			}
			if _, statErr := os.Stat(fsx.Join(dir, program)); statErr == nil {
				errno = syscall.EACCES
			}
		}
		return ioErrorText(errno)
	}
	return ioErrorText(err)
}

type incomingMessage struct {
	value any
	err   error
}

// writeMessages is the stdin writer: it ends when the queue is closed or a
// write fails, closing the pipe either way.
func writeMessages(tx *outbox, stdin *os.File) {
	defer close(tx.dead)
	defer stdin.Close()
	for value := range tx.messages {
		if writeMessage(stdin, value) != nil {
			return
		}
	}
}

// readMessages is the stdout reader: one message at a time until the first
// error, which it delivers like any message.
func readMessages(stdout *os.File, incoming chan<- incomingMessage, quit <-chan struct{}) {
	defer close(incoming)
	reader := bufio.NewReader(stdout)
	for {
		value, err := readMessage(reader)
		select {
		case incoming <- incomingMessage{value: value, err: err}:
		case <-quit:
			return
		}
		if err != nil {
			return
		}
	}
}

// startSession runs a server in its own goroutine. The returned session is the
// UI's side of it: commands in, events out, and a finished signal that closes
// once the child is gone.
func startSession(ctx context.Context, program string, args []string, root, version string) *session {
	input, in := mailbox()
	events := make(chan output, channelCapacity)
	finished := make(chan struct{})
	guard.Go(func() {
		defer close(finished)
		defer close(events)
		if err := serve(ctx, program, args, root, version, in, events, startupTimeout); err != nil {
			trySend(events, outputFailed{fmt.Sprintf("%s: %s; :lsp-restart to retry", program, err)})
		}
	})
	return &session{input: input, output: events, finished: finished}
}

// The initialize request: exactly what the client supports, nothing more.
func initializeRequest(program, rootURI, rootName, version string) object {
	return object{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": object{
		"processId":  os.Getpid(),
		"clientInfo": object{"name": "coigue", "version": version},
		"rootUri":    rootURI,
		"workspaceFolders": []any{
			object{"uri": rootURI, "name": rootName},
		},
		"initializationOptions": initializationOptions(program),
		"capabilities": object{
			"general": object{"positionEncodings": []string{"utf-16"}},
			"workspace": object{
				"symbol":           object{"dynamicRegistration": false},
				"configuration":    true,
				"workspaceFolders": true,
				"applyEdit":        false,
				"semanticTokens":   object{"refreshSupport": true},
			},
			"textDocument": object{
				"definition":     object{"dynamicRegistration": false, "linkSupport": true},
				"references":     object{"dynamicRegistration": false},
				"documentSymbol": object{"dynamicRegistration": false, "hierarchicalDocumentSymbolSupport": true},
				"semanticTokens": object{
					"dynamicRegistration":     false,
					"requests":                object{"full": true},
					"tokenTypes":              semantic.Types,
					"tokenModifiers":          semantic.Modifiers,
					"formats":                 []string{"relative"},
					"overlappingTokenSupport": false,
					"multilineTokenSupport":   false,
					"serverCancelSupport":     true,
					"augmentsSyntaxTokens":    true,
				},
				"synchronization":    object{"dynamicRegistration": false, "didSave": true},
				"publishDiagnostics": object{"versionSupport": true},
			},
		},
	}}
}

func initializationOptions(program string) any {
	if program == "gopls" {
		return object{"semanticTokens": true}
	}
	return nil
}

// serve runs one server from spawn to shutdown. It returns nil only for an
// orderly stop; every other end is an error that the UI shows. Whatever
// happens, the child is killed and reaped before it returns.
func serve(ctx context.Context, program string, args []string, root, version string, in *inbox, events chan<- output, startup time.Duration) error {
	child, err := spawn(ctx, program, args, root)
	if err != nil {
		return fmt.Errorf("unavailable (%s)", spawnErrorText(program, err))
	}
	defer child.close()
	writer := newOutbox(channelCapacity)
	incoming := make(chan incomingMessage, channelCapacity)
	quit := make(chan struct{})
	defer close(quit)
	defer close(writer.messages)
	guard.Go(func() { writeMessages(writer, child.stdin) })
	guard.Go(func() { readMessages(child.stdout, incoming, quit) })
	rootURI, ok := directoryURI(root)
	if !ok {
		return errors.New("invalid workspace path")
	}
	rootName, _ := fsx.FileName(root)
	if err := send(writer, initializeRequest(program, rootURI, displayPath(rootName), version)); err != nil {
		return err
	}
	started := time.Now()
	var stopping time.Time // zero while running
	proto := &protocol{gopls: program == "gopls"}
	lastSync := time.Now()
	for {
		if status, exited := child.tryWait(); exited {
			if !stopping.IsZero() {
				return nil
			}
			return fmt.Errorf("server exited (%s)", status)
		}
		if !stopping.IsZero() && time.Since(stopping) >= shutdownTimeout {
			return nil
		}
		if !proto.ready && time.Since(started) >= startup && stopping.IsZero() {
			return errors.New("initialization timed out")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if command, ok := in.recvTimeout(tick); ok && stopping.IsZero() {
			switch command := command.(type) {
			case inputDocument:
				doc := command.document
				immediate := doc.saveEvent || proto.sent == nil ||
					proto.sent.uri != doc.uri || proto.sent.generation != doc.generation
				proto.desired = &doc
				if immediate {
					if err := proto.flush(writer); err != nil {
						return err
					}
					lastSync = time.Now()
				}
			case inputNavigate:
				if err := proto.flush(writer); err != nil {
					return err
				}
				if err := proto.navigation.queue(command.request, writer); err != nil {
					return err
				}
			case inputCancelNavigation:
				if err := proto.navigation.cancel(writer); err != nil {
					return err
				}
			case inputStop:
				stopping = time.Now()
				if proto.ready {
					if err := proto.flush(writer); err != nil {
						return err
					}
					if err := proto.close(writer); err != nil {
						return err
					}
					err := send(writer, object{"jsonrpc": "2.0", "id": 2, "method": "shutdown", "params": nil})
					if err != nil {
						return err
					}
				} else if err := send(writer, notify("exit", nil)); err != nil {
					return err
				}
			}
		}
		if stopping.IsZero() && time.Since(lastSync) >= changeInterval {
			if err := proto.flush(writer); err != nil {
				return err
			}
			lastSync = time.Now()
		}
	messages:
		for range pollBound {
			var message incomingMessage
			select {
			case m, ok := <-incoming:
				if !ok {
					return errors.New("server pipe closed")
				}
				message = m
			default:
				break messages
			}
			if message.err != nil {
				if !stopping.IsZero() {
					return nil
				}
				return message.err
			}
			value := message.value
			if _, isMessage := get(value, "method"); isMessage {
				if _, isRequest := get(value, "id"); isRequest {
					if err := proto.reply(value, rootURI, writer); err != nil {
						return err
					}
				} else if equalsString(index(value, "method"), "textDocument/publishDiagnostics") && stopping.IsZero() {
					if publication, ok := decodePublication(index(value, "params")); ok &&
						proto.sent != nil && publication.uri == proto.sent.uri {
						// Diagnostics are replaceable; avoid blocking the process
						// supervisor if the editor is busy (e.g. saving to disk).
						trySend(events, outputDiagnostics{
							generation:  proto.sent.generation,
							version:     proto.sent.version,
							publication: publication,
						})
					}
				}
			} else if equalsInt(index(value, "id"), 1) && !proto.ready && stopping.IsZero() {
				if failure, ok := get(value, "error"); ok {
					return fmt.Errorf("initialize failed: %s", marshal(failure))
				}
				result := index(value, "result")
				if !isObject(index(result, "capabilities")) {
					return errors.New("invalid initialize response")
				}
				if err := proto.initialize(result, writer); err != nil {
					return err
				}
				trySend(events, outputReady{})
			} else if equalsInt(index(value, "id"), 2) && !stopping.IsZero() {
				if err := send(writer, notify("exit", nil)); err != nil {
					return err
				}
			} else if stopping.IsZero() {
				proto.tokens.response(value, events)
				proto.navigation.response(value, events)
			}
		}
		if stopping.IsZero() && proto.ready && proto.desired == nil && proto.sent != nil {
			if err := proto.tokens.request(proto.sent, writer, events); err != nil {
				return err
			}
		}
		if stopping.IsZero() && proto.ready {
			if err := proto.navigation.tick(proto.sent, writer, events); err != nil {
				return err
			}
		}
	}
}
