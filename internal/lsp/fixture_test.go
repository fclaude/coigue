package lsp

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// waitFixture waits for an atomically published PID before a protocol test
// starts its deadline. Setup failures still leave the caller owning the child.
func waitFixture(t *testing.T, child *process, ready string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(ready); err == nil {
			pid, err := strconv.Atoi(string(data))
			if err != nil || pid != child.cmd.Process.Pid {
				t.Fatalf("fixture readiness %q does not identify child %d", data, child.cmd.Process.Pid)
			}
			return pid
		} else if !os.IsNotExist(err) && !(runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(32))) {
			t.Fatal(err)
		}
		// A missing file or Windows ERROR_SHARING_VIOLATION (32) can briefly
		// accompany publication. Both stay within this setup deadline.
		if status, exited := child.tryWait(); exited {
			t.Fatalf("fixture exited before publishing readiness: %s", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture did not publish readiness")
	return 0
}
