package guard

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The re-executed child panics in a guarded goroutine; the parent checks that
// the cleanup ran before the process died.
func TestCleanupRunsBeforeAGoroutinePanicKillsTheProcess(t *testing.T) {
	if os.Getenv("GUARD_CHILD") == "1" {
		OnPanic(func() { os.Stdout.WriteString("cleaned-up\n") })
		Go(func() { panic("boom") })
		select {}
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCleanupRunsBeforeAGoroutinePanicKillsTheProcess")
	cmd.Env = append(os.Environ(), "GUARD_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("child survived a panic")
	}
	if !strings.Contains(string(out), "cleaned-up") || !strings.Contains(string(out), "boom") {
		t.Fatalf("output = %q", out)
	}
	if strings.Index(string(out), "cleaned-up") > strings.Index(string(out), "panic:") {
		t.Fatalf("cleanup ran after the panic message: %q", out)
	}
}
