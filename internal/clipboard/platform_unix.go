//go:build unix

package clipboard

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// searchPath lists the files execvp(3) would try for program: the program
// itself when it contains a slash, otherwise its name in each directory of
// PATH, an empty entry standing for the current directory. glibc's default
// path is used when PATH is not set at all.
func searchPath(program string) []string {
	if strings.Contains(program, "/") {
		return []string{program}
	}
	path, ok := os.LookupEnv("PATH")
	if !ok {
		path = "/bin:/usr/bin"
	}
	var candidates []string
	for _, dir := range strings.Split(path, ":") {
		if dir == "" {
			dir = "."
		}
		candidates = append(candidates, dir+"/"+program)
	}
	return candidates
}

// exitStatus is the Display of Rust's ExitStatus on Unix: "exit status: 7" or
// "signal: 9 (SIGKILL)".
func exitStatus(state *os.ProcessState) string {
	status, ok := state.Sys().(syscall.WaitStatus)
	switch {
	case !ok:
		return state.String()
	case status.Exited():
		return fmt.Sprintf("exit status: %d", status.ExitStatus())
	case status.Signaled():
		text := fmt.Sprintf("signal: %d%s", int(status.Signal()), signalString(status.Signal()))
		if status.CoreDump() {
			text += " (core dumped)"
		}
		return text
	case status.Stopped():
		return fmt.Sprintf("stopped (not terminated) by signal: %d%s", int(status.StopSignal()), signalString(status.StopSignal()))
	case status.Continued():
		return "continued (WIFCONTINUED)"
	}
	return state.String()
}

// signalString names the signals Rust names, with the space that precedes the
// parenthesis in its output. Rust also names the system-specific SIGSTKFLT,
// SIGPWR, SIGEMT and SIGINFO; those print as a bare number here.
func signalString(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGHUP:
		return " (SIGHUP)"
	case syscall.SIGINT:
		return " (SIGINT)"
	case syscall.SIGQUIT:
		return " (SIGQUIT)"
	case syscall.SIGILL:
		return " (SIGILL)"
	case syscall.SIGTRAP:
		return " (SIGTRAP)"
	case syscall.SIGABRT:
		return " (SIGABRT)"
	case syscall.SIGBUS:
		return " (SIGBUS)"
	case syscall.SIGFPE:
		return " (SIGFPE)"
	case syscall.SIGKILL:
		return " (SIGKILL)"
	case syscall.SIGUSR1:
		return " (SIGUSR1)"
	case syscall.SIGSEGV:
		return " (SIGSEGV)"
	case syscall.SIGUSR2:
		return " (SIGUSR2)"
	case syscall.SIGPIPE:
		return " (SIGPIPE)"
	case syscall.SIGALRM:
		return " (SIGALRM)"
	case syscall.SIGTERM:
		return " (SIGTERM)"
	case syscall.SIGCHLD:
		return " (SIGCHLD)"
	case syscall.SIGCONT:
		return " (SIGCONT)"
	case syscall.SIGSTOP:
		return " (SIGSTOP)"
	case syscall.SIGTSTP:
		return " (SIGTSTP)"
	case syscall.SIGTTIN:
		return " (SIGTTIN)"
	case syscall.SIGTTOU:
		return " (SIGTTOU)"
	case syscall.SIGURG:
		return " (SIGURG)"
	case syscall.SIGXCPU:
		return " (SIGXCPU)"
	case syscall.SIGXFSZ:
		return " (SIGXFSZ)"
	case syscall.SIGVTALRM:
		return " (SIGVTALRM)"
	case syscall.SIGPROF:
		return " (SIGPROF)"
	case syscall.SIGWINCH:
		return " (SIGWINCH)"
	case syscall.SIGIO:
		return " (SIGIO)"
	case syscall.SIGSYS:
		return " (SIGSYS)"
	}
	return ""
}
