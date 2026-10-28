//go:build unix

package lsp

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
)

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
