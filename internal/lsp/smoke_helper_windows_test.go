//go:build windows

package lsp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Unix fixture runs Python scripts by their shebang. Windows instead runs a
// hard link to this test executable, which launches the same script under Python.
// A job object keeps the Python child tied to the wrapper even when it is killed.
func TestMain(m *testing.M) {
	python := os.Getenv("COIGUE_LSP_FAKE_PYTHON")
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if python != "" && (name == "rust-analyzer" || name == "gopls" || name == "marksman") {
		os.Exit(runFakeServer(python))
	}
	os.Exit(m.Run())
}

func runFakeServer(python string) int {
	errorsFile, err := os.OpenFile(filepath.Join(os.Getenv("COIGUE_LSP_LOG"), "fake-errors.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 1
	}
	defer errorsFile.Close()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		fmt.Fprintln(errorsFile, err)
		return 1
	}
	defer windows.CloseHandle(job)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		fmt.Fprintln(errorsFile, err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(errorsFile, err)
		return 1
	}
	script := strings.TrimSuffix(exe, ".exe") + ".py"
	cmd := exec.Command(python, append([]string{script}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, errorsFile
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(errorsFile, err)
		return 1
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, handle)
		windows.CloseHandle(handle)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		fmt.Fprintln(errorsFile, err)
		return 1
	}
	if err := cmd.Wait(); err != nil {
		return 1
	}
	return 0
}
