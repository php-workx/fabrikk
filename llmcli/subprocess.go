package llmcli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// defaultStderrTailBytes is the max bytes retained from subprocess stderr for
// error reporting. Kept small enough to remain in a single log line while
// large enough to capture a meaningful tail.
const defaultStderrTailBytes = 4096

// defaultGracePeriod is the time allowed between SIGTERM and SIGKILL when
// terminating a subprocess tree. Processes that handle SIGTERM cleanly will
// typically exit well within this window.
const defaultGracePeriod = 3 * time.Second

// processSpec describes a subprocess to start. It contains the minimum
// information needed to launch the subprocess and plumb its I/O.
type processSpec struct {
	// Command is the executable name or path.
	Command string

	// Args are the command-line arguments passed after the command.
	Args []string

	// Env is the complete environment for the subprocess.
	// If nil, the parent process environment (os.Environ()) is used.
	Env []string

	// Dir is the working directory for the subprocess.
	// An empty string inherits the parent working directory.
	Dir string
}

// supervisor owns the lifecycle of a single subprocess. It holds the exclusive
// cmd.Wait() call (guarded by a sync.Once), captures a bounded stderr tail for
// terminal error messages, and drives process-tree termination when the context
// is cancelled.
//
// Usage contract: the caller must read Stdout to EOF (or call terminate) before
// calling wait() for the first time, to avoid cmd.Wait() blocking on a live
// stdout reader.
type supervisor struct {
	cmd    *exec.Cmd
	Stdout *bufio.Reader // caller reads events from this; read to EOF before wait()

	stderrTailBuf *tailWriter

	// done is closed by wait() after cmd.Wait() returns. The background
	// lifecycle goroutine uses this to exit when the process ends naturally.
	done chan struct{}

	waitOnce sync.Once
	waitErr  error
}

// startSupervised starts the subprocess described by spec, configures stdout
// and stderr plumbing, launches a background goroutine for context-driven
// termination, and returns before the subprocess exits (Criterion 1).
//
// On success the caller owns s.Stdout and must read it to EOF before calling
// s.wait(). On failure all resources are released before returning.
func startSupervised(ctx context.Context, spec processSpec) (*supervisor, error) {
	//nolint:gosec // command and args are caller-controlled; callers must vet them
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- spec.Command/spec.Args are assembled from detected CLI binaries and explicit backend-owned args, not shell input.
	cmd := exec.Command(spec.Command, spec.Args...)

	if spec.Env != nil {
		cmd.Env = spec.Env
	} else {
		cmd.Env = os.Environ()
	}
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}

	// Platform-specific: place the subprocess in its own process group so
	// terminateProcessTree can kill the whole tree, not just the root process.
	configureProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("llmcli: stdout pipe: %w", err)
	}

	tail := newTailWriter()
	cmd.Stderr = tail // Go's exec package starts a copying goroutine for this.

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close() // release pipe resources on failure
		return nil, fmt.Errorf("llmcli: start process: %w", err)
	}

	s := &supervisor{
		cmd:           cmd,
		Stdout:        bufio.NewReader(stdoutPipe),
		stderrTailBuf: tail,
		done:          make(chan struct{}),
	}

	// Background goroutine: terminate the process tree when ctx is done, or
	// exit early when wait() has already been called (s.done is closed).
	go func() {
		select {
		case <-ctx.Done():
			s.terminate(ctx.Err())
		case <-s.done:
			// Process exited and wait() was called; nothing to do.
		}
	}()

	return s, nil
}

// wait calls cmd.Wait() exactly once and caches the result. Subsequent calls
// return the cached value without blocking (Criterion 2). The first call also
// closes s.done, which stops the background lifecycle goroutine.
//
// Precondition: s.Stdout must have been read to EOF before the first call,
// otherwise cmd.Wait() may block waiting for the stdout pipe to drain.
func (s *supervisor) wait() error {
	s.waitOnce.Do(func() {
		s.waitErr = s.cmd.Wait()
		close(s.done)
	})

	return s.waitErr
}

// terminate sends SIGTERM to the subprocess tree (then SIGKILL after the grace
// period, asynchronously). It is safe to call more than once and from any
// goroutine. The reason parameter is reserved for future diagnostic use.
func (s *supervisor) terminate(_ error) {
	if s.cmd.Process == nil {
		return
	}

	_ = terminateProcessTree(s.cmd.Process.Pid, defaultGracePeriod)
}

// stderrTail returns the tail of stderr captured from the subprocess. For a
// complete view, call stderrTail() after wait() returns; cmd.Wait() ensures
// the internal stderr-copying goroutine has flushed all bytes to the buffer.
func (s *supervisor) stderrTail() string {
	return s.stderrTailBuf.String()
}

// tailWriter is a bounded io.Writer that retains only the last max bytes
// written to it (tail semantics). It is safe for concurrent use because
// Go's exec package drives the stderr copy from a separate goroutine.
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newTailWriter() *tailWriter {
	return &tailWriter{
		// Preallocate conservatively; the buffer grows on demand up to size.
		buf: make([]byte, 0, min(defaultStderrTailBytes, 512)),
		max: defaultStderrTailBytes,
	}
}

// Write appends p to the tail buffer, evicting old bytes if the buffer would
// exceed max. It always returns len(p), nil.
func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(p) >= w.max {
		// p alone fills the entire allowed window; take the last max bytes.
		w.buf = append(w.buf[:0], p[len(p)-w.max:]...)
		return len(p), nil
	}

	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		// Evict oldest bytes: shift the last max bytes to the front.
		// copy handles the overlapping region correctly (src > dst).
		copy(w.buf, w.buf[len(w.buf)-w.max:])
		w.buf = w.buf[:w.max]
	}

	return len(p), nil
}

// String returns the current tail content as a string. Safe to call
// concurrently with Write.
func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return string(w.buf)
}
