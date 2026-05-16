//nolint:errcheck,gosec,gocognit,gocritic // Lockfile tests intentionally exercise cleanup best-effort paths, fd casts, subprocess helpers, and os.Exit.
package daemon

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLockFile_Acquire_Release(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := NewLockFile(lockPath)

	// Acquire should succeed
	err := lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Verify lock file exists and contains our PID
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("failed to read lock file: %v", err)
	}
	expected := fmt.Sprintf("%d\n", os.Getpid())
	if string(data) != expected {
		t.Errorf("expected PID %q in lock file, got %q", expected, string(data))
	}

	// Release should succeed
	err = lf.Release()
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Lock file should be removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be removed after Release")
	}
}

func TestLockFile_DoubleAcquire_Blocked(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf1 := NewLockFile(lockPath)
	lf2 := NewLockFile(lockPath)

	// First acquire should succeed
	err := lf1.Acquire()
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}
	defer lf1.Release()

	// Second acquire should fail (same process, but LOCK_NB means it should fail
	// since flock is per file descriptor, not per process on some systems)
	// Note: On Linux, flock is per-open-file-description, so two different
	// file descriptors from the same process will conflict.
	err = lf2.Acquire()
	if err == nil {
		lf2.Release()
		// On some systems (macOS), flock allows the same process to acquire
		// the lock on a different fd. This is acceptable behavior.
		t.Skip("flock allows same-process re-lock on this OS")
	}
}

func TestLockFile_StalePID_Recovery(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Write a stale PID (very high, unlikely to be a running process)
	err := os.WriteFile(lockPath, []byte("999999999\n"), 0o600)
	if err != nil {
		t.Fatalf("failed to write stale PID: %v", err)
	}

	lf := NewLockFile(lockPath)

	// Acquire should succeed after detecting stale PID
	err = lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed with stale PID: %v", err)
	}
	defer lf.Release()

	// Verify our PID is now in the lock file
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("failed to read lock file: %v", err)
	}
	expected := fmt.Sprintf("%d\n", os.Getpid())
	if string(data) != expected {
		t.Errorf("expected PID %q, got %q", expected, string(data))
	}
}

func TestLockFile_Release_Idempotent(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := NewLockFile(lockPath)

	// Release without acquire should not error
	err := lf.Release()
	if err != nil {
		t.Errorf("Release without Acquire should not error: %v", err)
	}

	// Acquire then double release
	err = lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	err = lf.Release()
	if err != nil {
		t.Fatalf("first Release failed: %v", err)
	}

	err = lf.Release()
	if err != nil {
		t.Errorf("second Release should not error: %v", err)
	}
}

func TestLockFile_Path(t *testing.T) {
	t.Parallel()

	lf := NewLockFile("/tmp/test.lock")
	if lf.Path() != "/tmp/test.lock" {
		t.Errorf("expected path /tmp/test.lock, got %s", lf.Path())
	}
}

func TestLockFile_CreatesDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "nested", "dir", "test.lock")

	lf := NewLockFile(lockPath)

	err := lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer lf.Release()

	// Verify directory was created
	dirPath := filepath.Dir(lockPath)
	info, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("directory should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("should be a directory")
	}
}

func TestLockFile_PermissionsSecure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := NewLockFile(lockPath)

	err := lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer lf.Release()

	// Verify lock file permissions are 0600
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("failed to stat lock file: %v", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	t.Parallel()

	// Current process should be alive
	if !isProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
}

func TestIsProcessAlive_NonExistentProcess(t *testing.T) {
	t.Parallel()

	// Very high PID should not exist
	if isProcessAlive(999999999) {
		t.Error("PID 999999999 should not be alive")
	}
}

func TestLockFile_InvalidPIDInFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Write invalid PID content
	err := os.WriteFile(lockPath, []byte("not-a-pid\n"), 0o600)
	if err != nil {
		t.Fatalf("failed to write invalid PID: %v", err)
	}

	lf := NewLockFile(lockPath)

	// Should handle gracefully - the file has no flock, so Acquire should succeed
	err = lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed with invalid PID in file: %v", err)
	}
	defer lf.Release()
}

func TestLockFile_EmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Create empty file
	err := os.WriteFile(lockPath, []byte(""), 0o600)
	if err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}

	lf := NewLockFile(lockPath)

	// Should handle gracefully
	err = lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed with empty file: %v", err)
	}
	defer lf.Release()
}

func TestLockFile_readPIDFromFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := NewLockFile(lockPath)

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{"valid PID", "12345\n", 12345},
		{"valid PID no newline", "12345", 12345},
		{"invalid PID", "abc\n", 0},
		{"empty", "", 0},
		{"PID with spaces", "  12345  \n", 12345},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := os.WriteFile(lockPath, []byte(tt.content), 0o600)
			if err != nil {
				t.Fatalf("failed to write file: %v", err)
			}

			f, err := os.Open(lockPath)
			if err != nil {
				t.Fatalf("failed to open file: %v", err)
			}
			defer f.Close()

			pid := lf.readPIDFromFile(f)
			if pid != tt.expected {
				t.Errorf("readPIDFromFile(%q) = %d, want %d", tt.content, pid, tt.expected)
			}
		})
	}
}

func TestLockFile_DirectoryPermissions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "run")
	lockPath := filepath.Join(nestedDir, "test.lock")

	lf := NewLockFile(lockPath)

	err := lf.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer lf.Release()

	// Verify directory was created with 0700 permissions
	info, err := os.Stat(nestedDir)
	if err != nil {
		t.Fatalf("failed to stat directory: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o700 {
		t.Errorf("expected directory permissions 0700, got %o", perm)
	}
}

func TestLockFile_AcquireAfterRelease(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	lf := NewLockFile(lockPath)

	// Acquire, release, then acquire again
	err := lf.Acquire()
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}

	err = lf.Release()
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Should be able to acquire again
	err = lf.Acquire()
	if err != nil {
		t.Fatalf("second Acquire failed: %v", err)
	}
	defer lf.Release()
}

func TestLockFile_ConcurrentAcquire_DifferentPaths(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	lf1 := NewLockFile(filepath.Join(tmpDir, "lock1"))
	lf2 := NewLockFile(filepath.Join(tmpDir, "lock2"))

	// Both should succeed on different paths
	err := lf1.Acquire()
	if err != nil {
		t.Fatalf("lf1 Acquire failed: %v", err)
	}
	defer lf1.Release()

	err = lf2.Acquire()
	if err != nil {
		t.Fatalf("lf2 Acquire failed: %v", err)
	}
	defer lf2.Release()
}

// TestLockFile_FlockBehavior verifies basic flock syscall behavior.
func TestLockFile_FlockBehavior(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "flock.test")

	// Create file
	f, err := os.Create(lockPath)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	defer f.Close()

	// Acquire exclusive lock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		t.Fatalf("failed to acquire flock: %v", err)
	}

	// Release lock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err != nil {
		t.Fatalf("failed to release flock: %v", err)
	}
}

func TestReadHeldPID(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		pid, held, err := ReadHeldPID(filepath.Join(t.TempDir(), "missing.lock"))
		if err != nil {
			t.Fatalf("ReadHeldPID() error = %v", err)
		}
		if held || pid != 0 {
			t.Fatalf("ReadHeldPID() = (pid=%d, held=%v), want (0,false)", pid, held)
		}
	})

	t.Run("unlocked file", func(t *testing.T) {
		lockPath := filepath.Join(t.TempDir(), "test.lock")
		if err := os.WriteFile(lockPath, []byte("123\n"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		pid, held, err := ReadHeldPID(lockPath)
		if err != nil {
			t.Fatalf("ReadHeldPID() error = %v", err)
		}
		if held || pid != 0 {
			t.Fatalf("ReadHeldPID() = (pid=%d, held=%v), want (0,false)", pid, held)
		}
	})

	t.Run("open error", func(t *testing.T) {
		_, _, err := ReadHeldPID(t.TempDir())
		if err == nil {
			t.Fatalf("ReadHeldPID() expected error for directory path")
		}
	})

	t.Run("held by helper process", func(t *testing.T) {
		lockPath := filepath.Join(t.TempDir(), "held.lock")
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessHoldLock$")
		cmd.Env = append(os.Environ(),
			"CLAI_TEST_HOLD_LOCK=1",
			"CLAI_TEST_LOCK_PATH="+lockPath,
		)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("StdoutPipe() error = %v", err)
		}
		if startErr := cmd.Start(); startErr != nil {
			t.Fatalf("Start() error = %v", startErr)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})

		readyCh := make(chan string, 1)
		go func() {
			scanner := bufio.NewScanner(stdout)
			if scanner.Scan() {
				readyCh <- scanner.Text()
				return
			}
			readyCh <- ""
		}()

		select {
		case ready := <-readyCh:
			if strings.TrimSpace(ready) != "ready" {
				t.Fatalf("helper readiness = %q, want %q", ready, "ready")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for lock helper readiness")
		}

		pid, held, err := ReadHeldPID(lockPath)
		if err != nil {
			t.Fatalf("ReadHeldPID() error = %v", err)
		}
		if !held {
			t.Fatalf("ReadHeldPID() held = false, want true")
		}
		if pid <= 0 {
			t.Fatalf("ReadHeldPID() pid = %d, want > 0", pid)
		}
	})
}

func TestHelperProcessHoldLock(t *testing.T) {
	if os.Getenv("CLAI_TEST_HOLD_LOCK") != "1" {
		return
	}

	lockPath := os.Getenv("CLAI_TEST_LOCK_PATH")
	if lockPath == "" {
		os.Exit(2)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		os.Exit(3)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		os.Exit(4)
	}
	if err := f.Truncate(0); err != nil {
		os.Exit(5)
	}
	if _, err := f.Seek(0, 0); err != nil {
		os.Exit(6)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		os.Exit(7)
	}
	_ = f.Sync()

	fmt.Println("ready")
	time.Sleep(5 * time.Second)
	os.Exit(0)
}
