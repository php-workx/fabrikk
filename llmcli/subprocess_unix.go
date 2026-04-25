//go:build !windows

package llmcli

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// configureProcessGroup sets Setpgid on cmd so the subprocess starts in its
// own process group with PGID == PID. terminateProcessTree then sends signals
// to -PGID to reach the entire tree.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Setpgid = true
}

// terminateProcessTree sends SIGTERM to the process group identified by pid.
// After the grace period it escalates to SIGKILL in a background goroutine, so
// this function returns as soon as SIGTERM is sent. The escalation is cancelled
// when reaped is closed. Errors are ignored when the process group is already
// gone (ESRCH).
//
// Background: we cannot poll kill(-pgid, 0) to detect exit because zombie
// processes remain in their process group until reaped by cmd.Wait(). Sending
// SIGKILL asynchronously covers stuck processes without blocking the caller.
func terminateProcessTree(pid int, grace time.Duration, reaped <-chan struct{}) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if isProcessNotFound(err) {
			return nil
		}

		return fmt.Errorf("llmcli: SIGTERM process group -%d: %w", pid, err)
	}

	// Escalate to SIGKILL in the background after the grace period.
	// In the common case the process exits from SIGTERM first; this goroutine
	// then sends SIGKILL to a dead process group (ESRCH, ignored).
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-timer.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL) // best-effort; ESRCH expected when already reaped
		case <-reaped:
		}
	}()

	return nil
}

// isProcessNotFound reports whether err indicates the target process or group
// no longer exists (syscall.ESRCH: "no such process").
func isProcessNotFound(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
