//go:build windows

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

// LockFile manages an exclusive lock file to prevent multiple daemon instances.
type LockFile struct {
	path string
	file *os.File
}

// Lockfile is an alias for LockFile kept for bridge-facing API readability.
type Lockfile = LockFile

// NewLockFile creates a new LockFile at the specified path.
// The lock is not acquired until Acquire is called.
func NewLockFile(path string) *LockFile {
	return &LockFile{path: path}
}

// AcquireLockfile creates and acquires a lockfile at path.
func AcquireLockfile(path string) (*LockFile, error) {
	lock := NewLockFile(path)
	if err := lock.Acquire(); err != nil {
		return nil, err
	}
	return lock, nil
}

// LockFilePath returns the default lock file path for the given base directory.
func LockFilePath(baseDir string) string {
	return filepath.Join(baseDir, "clai.lock")
}

// ReadHeldPID returns the PID recorded in lockPath when a lock file exists.
// On Windows we infer lock ownership from the lock file presence.
func ReadHeldPID(lockPath string) (pid int, held bool, err error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("open lock file: %w", err)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, _ = strconv.Atoi(pidStr)
	return pid, true, nil
}

// Acquire attempts to acquire an exclusive lock by atomically creating
// the lock file.
func (l *LockFile) Acquire() error {
	// Ensure parent directory exists.
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if os.IsExist(err) {
			stalePID, _, readErr := ReadHeldPID(l.path)
			if readErr == nil && stalePID > 0 && !isProcessAlive(stalePID) {
				// Best-effort stale lock cleanup.
				if remErr := os.Remove(l.path); remErr == nil {
					return l.retryAcquire()
				}
			}
			if stalePID > 0 {
				return fmt.Errorf("daemon already running (PID %d), lock file: %s", stalePID, l.path)
			}
		}
		return fmt.Errorf("failed to acquire lock on %s: %w", l.path, err)
	}

	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		_ = os.Remove(l.path)
		return fmt.Errorf("failed to write PID to lock file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(l.path)
		return fmt.Errorf("failed to sync lock file: %w", err)
	}

	l.file = f
	return nil
}

// retryAcquire performs a single retry after stale lock cleanup.
func (l *LockFile) retryAcquire() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to acquire lock on retry: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		_ = os.Remove(l.path)
		return fmt.Errorf("failed to write PID to lock file on retry: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(l.path)
		return fmt.Errorf("failed to sync lock file on retry: %w", err)
	}
	l.file = f
	return nil
}

// Release releases the lock and removes the lock file.
func (l *LockFile) Release() error {
	if l.file == nil {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("failed to close lock file: %w", err)
	}
	l.file = nil

	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}
	return nil
}

// Path returns the lock file path.
func (l *LockFile) Path() string {
	return l.path
}

// readPIDFromFile reads a PID from an already-open file.
func (l *LockFile) readPIDFromFile(f *os.File) int {
	if _, err := f.Seek(0, 0); err != nil {
		return 0
	}
	buf := make([]byte, 32)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return 0
	}
	pidStr := strings.TrimSpace(string(buf[:n]))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0
	}
	return pid
}

// isProcessAlive checks if a process with the given PID is running.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == windowsStillActive
}
