//go:build !unix

package clipboard

import (
	"fmt"
	"os"
	"os/exec"
)

// searchPath is exec.LookPath: without execvp there is no search to imitate.
func searchPath(program string) []string {
	if path, err := exec.LookPath(program); err == nil {
		return []string{path}
	}
	return []string{program}
}

func exitStatus(state *os.ProcessState) string {
	return fmt.Sprintf("exit status: %d", state.ExitCode())
}
