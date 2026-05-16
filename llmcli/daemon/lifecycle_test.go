package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLifecycleRunReturnsRunError(t *testing.T) {
	want := context.Canceled
	lc := NewLifecycle(LifecycleConfig{ShutdownTimeout: time.Second})

	err := lc.Run(context.Background(), func(context.Context) error {
		return want
	})
	if err != want {
		t.Fatalf("Run error = %v, want %v", err, want)
	}
}

func TestLifecycleRunCancelsOnParentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	lc := NewLifecycle(LifecycleConfig{ShutdownTimeout: time.Second})
	seenCancel := make(chan struct{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := lc.Run(ctx, func(ctx context.Context) error {
		<-ctx.Done()
		close(seenCancel)
		return nil
	})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	<-seenCancel
}

func TestDefaultLockfilePathUsesRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	got := DefaultLockfilePath("test-bridge")
	want := filepath.Join(dir, "test-bridge.lock")
	if got != want {
		t.Fatalf("DefaultLockfilePath = %q, want %q", got, want)
	}
}

func TestReadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("pid = %d, want 12345", pid)
	}
}
