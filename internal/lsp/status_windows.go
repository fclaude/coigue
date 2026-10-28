//go:build windows

package lsp

import (
	"fmt"
	"os"
)

// exitStatusText renders a finished Windows process. Wait status here is an
// exit code; there is no signal name or core-dump flag to report.
func exitStatusText(state *os.ProcessState) string {
	return fmt.Sprintf("exit status: %d", state.ExitCode())
}
