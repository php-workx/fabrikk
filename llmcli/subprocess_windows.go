//go:build windows

package llmcli

import (
	"fmt"
	"os/exec"
	"time"
)

// configureProcessGroup is a no-op on Windows. Process-group isolation is not
// set here because terminateProcessTree delegates tree cleanup to taskkill,
// which handles job-object hierarchies without requiring a separate PGID.
func configureProcessGroup(_ *exec.Cmd) {}

// terminateProcessTree terminates the process identified by pid and all of its
// descendants using "taskkill /T /F /PID <pid>". The grace parameter is unused
// on Windows because taskkill /F terminates processes immediately.
func terminateProcessTree(pid int, _ time.Duration) error {
	//nolint:gosec // pid is from cmd.Process.Pid, not user input
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", pid))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("llmcli: taskkill /T /F /PID %d: %w", pid, err)
	}

	return nil
}
