package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

const windowsOS = "windows"

// LifecycleConfig configures graceful shutdown behavior for long-lived hosts.
type LifecycleConfig struct {
	ShutdownTimeout time.Duration
	LockfilePath    string
	Reload          func() error
}

// Lifecycle coordinates signal handling and graceful shutdown.
type Lifecycle struct {
	cfg LifecycleConfig
}

// NewLifecycle creates a lifecycle helper.
func NewLifecycle(cfg LifecycleConfig) *Lifecycle {
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	return &Lifecycle{cfg: cfg}
}

// Run executes run until it returns, ctx is cancelled, or a shutdown signal is
// received. The child context passed to run is cancelled during shutdown.
func (lc *Lifecycle) Run(ctx context.Context, run func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(runCtx)
	}()

	sigCh := make(chan os.Signal, 4)
	notifyLifecycleSignals(sigCh)
	defer signal.Stop(sigCh)

	for {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			cancel()
			return lc.waitForRun(context.Background(), errCh)
		case sig := <-sigCh:
			if lc.handleSignal(sig, cancel) {
				return lc.waitForRun(context.Background(), errCh)
			}
		}
	}
}

func (lc *Lifecycle) handleSignal(sig os.Signal, cancel context.CancelFunc) bool {
	if isReloadSignal(sig) {
		if lc.cfg.Reload != nil {
			_ = lc.cfg.Reload()
		}
		return false
	}
	if isShutdownSignal(sig) {
		cancel()
		return true
	}
	return false
}

func (lc *Lifecycle) waitForRun(ctx context.Context, errCh <-chan error) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, lc.cfg.ShutdownTimeout)
	defer cancel()
	select {
	case err := <-errCh:
		return err
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	}
}

// DefaultLockfilePath returns a per-user lockfile path for name.
func DefaultLockfilePath(name string) string {
	if name == "" {
		name = "llmcli"
	}
	if runtime.GOOS == windowsOS {
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, name, name+".lock")
		}
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, name+".lock")
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, name, name+".lock")
	}
	return filepath.Join(os.TempDir(), name+".lock")
}

// ReadPID reads the PID from path.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, fmt.Errorf("invalid PID: %w", err)
	}
	return pid, nil
}

func notifyLifecycleSignals(ch chan<- os.Signal) {
	if runtime.GOOS == windowsOS {
		signal.Notify(ch, os.Interrupt)
		return
	}
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
}

func isShutdownSignal(sig os.Signal) bool {
	if sig == os.Interrupt {
		return true
	}
	if runtime.GOOS == windowsOS {
		return false
	}
	return sig == syscall.SIGTERM || sig == syscall.SIGINT
}

func isReloadSignal(sig os.Signal) bool {
	if runtime.GOOS == windowsOS {
		return false
	}
	return sig == syscall.SIGHUP
}
