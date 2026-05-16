//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// LockFile manages an exclusive lock file to prevent multiple daemon instances.
// It uses flock(2) with LOCK_EX|LOCK_NB for non-blocking exclusive locking.
type LockFile struct {
	file *os.File
	path string
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

// ReadHeldPID returns the PID recorded in lockPath if (and only if) the file lock
// is currently held by another process. If the lock is not held (or the file does
// not exist), held will be false.
//
// This is used to recover when the PID file is stale/missing but a daemon is
// still running and holding the lock.
func ReadHeldPID(lockPath string) (pid int, held bool, err error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("open lock file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	// If we can acquire the lock, it is not held.
	flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // G115: fd fits in int
	if flockErr == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // G115: fd fits in int
		return 0, false, nil
	}
	if errors.Is(flockErr, syscall.EWOULDBLOCK) || errors.Is(flockErr, syscall.EAGAIN) {
		// Lock is held by another process. Read PID for diagnostics and control.
		if _, seekErr := f.Seek(0, 0); seekErr != nil {
			return 0, true, nil
		}
		buf := make([]byte, 32)
		n, rerr := f.Read(buf)
		if rerr != nil || n == 0 {
			return 0, true, nil
		}
		pidStr := strings.TrimSpace(string(buf[:n]))
		pid, _ := strconv.Atoi(pidStr)
		return pid, true, nil
	}
	return 0, false, fmt.Errorf("flock: %w", flockErr)
}

// Acquire attempts to acquire an exclusive non-blocking lock.
// If the lock is held by another process, it checks for stale PIDs.
// On success, the current PID is written to the lock file.
func (l *LockFile) Acquire() error {
	// Ensure parent directory exists
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	// Open or create the lock file
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire exclusive non-blocking lock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // G115: fd fits in int
	if err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return fmt.Errorf("failed to acquire lock on %s: %w", l.path, err)
		}

		// Lock is held by another process - check for stale PID
		stalePID := l.readPIDFromFile(f)
		_ = f.Close()

		if stalePID > 0 && !isProcessAlive(stalePID) {
			// Stale lock - remove and retry
			_ = os.Remove(l.path)
			return l.retryAcquire()
		}

		if stalePID > 0 {
			return fmt.Errorf("daemon already running (PID %d), lock file: %s", stalePID, l.path)
		}
		return fmt.Errorf("failed to acquire lock on %s: %w", l.path, err)
	}

	// Lock acquired - write our PID
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to truncate lock file: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to seek lock file: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write PID to lock file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to sync lock file: %w", err)
	}

	l.file = f
	return nil
}

// retryAcquire performs a single retry after removing a stale lock file.
func (l *LockFile) retryAcquire() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open lock file on retry: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // G115: fd fits in int
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to acquire lock on retry: %w", err)
	}

	// Lock acquired - write our PID
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to truncate lock file on retry: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to seek lock file on retry: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write PID to lock file on retry: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
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

	// Unlock
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil { //nolint:gosec // G115: fd fits in int
		// Best effort - continue with cleanup
		_ = err
	}

	// Close the file
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("failed to close lock file: %w", err)
	}
	l.file = nil

	// Remove the lock file
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
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds. Send signal 0 to check if alive.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
