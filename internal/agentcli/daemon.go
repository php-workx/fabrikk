package agentcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DaemonConfig controls how a daemon Claude process is started.
type DaemonConfig struct {
	Backend     CLIBackend
	SocketDir   string        // default: os.TempDir()
	RunID       string        // used in socket filename
	IdleTimeout time.Duration // default: 30m
}

// Daemon manages a persistent Claude process for a single run.
type Daemon struct {
	config       DaemonConfig
	process      *claudeProcess
	listener     net.Listener
	sockPath     string
	pidPath      string
	mu           sync.Mutex // protects needsRestart, process, listener, and restart operations
	needsRestart bool
	stopOnce     sync.Once
	stopCh       chan struct{} // closed by Stop to signal background goroutines
}

// NewDaemon creates a Daemon with sensible defaults applied to the config.
func NewDaemon(cfg DaemonConfig) *Daemon {
	if cfg.SocketDir == "" {
		cfg.SocketDir = os.TempDir()
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	return &Daemon{
		config:   cfg,
		sockPath: filepath.Join(cfg.SocketDir, fmt.Sprintf("fabrikk-%s.sock", cfg.RunID)),
		pidPath:  filepath.Join(cfg.SocketDir, fmt.Sprintf("fabrikk-%s.pid", cfg.RunID)),
		stopCh:   make(chan struct{}),
	}
}

// SocketPath returns the Unix socket path for this daemon.
func (d *Daemon) SocketPath() string {
	return d.sockPath
}

// cleanupStaleSocket detects and removes a stale socket file left behind by a
// previous crashed run. If a live daemon is listening on the socket, it returns
// an error instead of removing the file.
func (d *Daemon) cleanupStaleSocket() error {
	if !fileExists(d.sockPath) {
		return nil // no socket file — nothing to clean
	}

	// Socket file exists. Try to connect — if it fails, it's stale.
	conn, dialErr := net.DialTimeout("unix", d.sockPath, 2*time.Second)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("daemon already running on %s", d.sockPath)
	}

	// Stale socket — remove it.
	_ = os.Remove(d.sockPath)
	return nil
}

// fileExists reports whether the named file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// cleanupStalePID removes a PID file if the recorded process is no longer
// running. This handles the case where a previous daemon crashed without
// cleaning up after itself.
func (d *Daemon) cleanupStalePID() {
	pidData, err := os.ReadFile(d.pidPath)
	if err != nil {
		return
	}

	var pid int
	if _, scanErr := fmt.Sscanf(string(pidData), "%d", &pid); scanErr != nil || pid <= 0 {
		return
	}

	proc, findErr := os.FindProcess(pid)
	if findErr != nil {
		return
	}

	// On Unix, FindProcess always succeeds. Send signal 0 to check if alive.
	if proc.Signal(syscall.Signal(0)) != nil {
		_ = os.Remove(d.pidPath)
	}
}

// Start launches the Claude process, opens a Unix socket listener, and begins
// accepting connections. It returns once the process and listener are ready.
func (d *Daemon) Start(ctx context.Context) error {
	if err := d.cleanupStaleSocket(); err != nil {
		return err
	}
	d.cleanupStalePID()

	proc, err := startClaudeProcess(ctx, d.config)
	if err != nil {
		return fmt.Errorf("start claude process: %w", err)
	}

	ln, err := net.Listen("unix", d.sockPath)
	if err != nil {
		proc.cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		_ = proc.cmd.Wait()     // reap to avoid zombie
		return fmt.Errorf("listen on socket: %w", err)
	}

	// Set socket permissions explicitly (avoids process-wide Umask manipulation).
	if err := os.Chmod(d.sockPath, 0o600); err != nil {
		_ = ln.Close()
		proc.cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		_ = proc.cmd.Wait()     // reap to avoid zombie
		return fmt.Errorf("chmod socket: %w", err)
	}

	pidData := fmt.Sprintf("%d", proc.cmd.Process.Pid)
	if err := os.WriteFile(d.pidPath, []byte(pidData), 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(d.sockPath)
		proc.cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		_ = proc.cmd.Wait()     // reap to avoid zombie
		return fmt.Errorf("write pid file: %w", err)
	}

	// Commit state under lock.
	d.mu.Lock()
	d.process = proc
	d.listener = ln
	d.stopOnce = sync.Once{} // reset for restart
	d.stopCh = make(chan struct{})
	d.mu.Unlock()

	var activityMu sync.Mutex
	lastActivity := time.Now()
	stopCh := d.stopCh // capture by value for goroutines

	// Accept loop.
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return // listener closed
			}
			go handleDaemonConn(conn, proc, &activityMu, &lastActivity)
		}
	}()

	// Idle timeout watcher.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				activityMu.Lock()
				idle := time.Since(lastActivity)
				activityMu.Unlock()
				if idle >= d.config.IdleTimeout {
					_ = d.Stop()
					return
				}
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Context cancellation watcher — capture ln by value so it only closes
	// the listener it was created for, not a future one after restart.
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
		case <-stopCh:
			// daemon stopped explicitly — goroutine exits cleanly
		}
	}()

	return nil
}

// Stop shuts down the daemon gracefully: closes the listener, sends EOF to the
// Claude process via stdin close, waits for it to exit (with timeout), and
// removes the socket and PID files. If the process does not exit within 5
// seconds, it is forcefully killed. Safe to call multiple times (idempotent
// via sync.Once).
func (d *Daemon) Stop() error {
	d.stopOnce.Do(func() {
		// Signal background goroutines to exit.
		close(d.stopCh)

		d.mu.Lock()
		ln := d.listener
		proc := d.process
		d.listener = nil
		d.process = nil
		d.mu.Unlock()

		// Close listener first to stop accepting new connections.
		if ln != nil {
			_ = ln.Close()
		}

		if proc != nil {
			// Try graceful shutdown: close stdin so Claude receives EOF.
			if proc.stdin != nil {
				_ = proc.stdin.Close()
			}

			// Wait for process to exit, with timeout.
			done := make(chan error, 1)
			go func() {
				done <- proc.wait()
			}()

			select {
			case <-done:
				// Process exited gracefully.
			case <-time.After(5 * time.Second):
				// Force kill after timeout.
				_ = proc.cmd.Process.Kill()
				<-done // Wait for Kill to complete.
			}
		}

		// Clean up files.
		_ = os.Remove(d.sockPath)
		_ = os.Remove(d.pidPath)
	})
	return nil
}

// Query connects to the daemon's Unix socket, sends a prompt, and returns
// the response. If the daemon crashed on a previous call, it attempts a
// restart before querying. On failure, it marks the daemon for restart on
// the next call.
func (d *Daemon) Query(ctx context.Context, prompt string) (string, error) {
	// If daemon needs restart from a previous crash, attempt it while holding
	// d.mu to prevent concurrent callers from querying a dead socket.
	d.mu.Lock()
	if d.needsRestart {
		d.needsRestart = false
		// Hold the lock during restart so concurrent callers wait rather than
		// hitting the dead socket.
		restartErr := d.restartLocked(ctx)
		if restartErr != nil {
			d.needsRestart = true
			d.mu.Unlock()
			return "", fmt.Errorf("daemon restart failed: %w", restartErr)
		}
		d.mu.Unlock()
	} else {
		d.mu.Unlock()
	}

	// Try the query via socket.
	result, err := d.queryViaSocket(ctx, prompt)
	if err != nil {
		// Mark for restart on next query.
		d.mu.Lock()
		d.needsRestart = true
		d.mu.Unlock()
		return "", err
	}
	return result, nil
}

// queryViaSocket connects to the daemon's Unix socket, sends a prompt, and
// returns the response. If the context has a deadline it is used as the
// connection deadline; otherwise a 60-second default is applied.
func (d *Daemon) queryViaSocket(ctx context.Context, prompt string) (string, error) {
	conn, err := net.Dial("unix", d.sockPath)
	if err != nil {
		return "", fmt.Errorf("dial daemon socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(60 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "", fmt.Errorf("set connection deadline: %w", err)
	}

	if err := json.NewEncoder(conn).Encode(DaemonRequest{Prompt: prompt}); err != nil {
		return "", fmt.Errorf("send daemon request: %w", err)
	}

	var resp DaemonResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return "", fmt.Errorf("read daemon response: %w", err)
	}

	if resp.Error != "" {
		return "", fmt.Errorf("daemon error: %s", resp.Error)
	}

	return resp.Result, nil
}

// restartLocked stops and re-starts the daemon. Caller must hold d.mu.
// The lock is temporarily released during Stop/Start to allow those methods
// to acquire it internally, then re-acquired before return.
func (d *Daemon) restartLocked(ctx context.Context) error {
	d.mu.Unlock() // release for Stop/Start which may lock internally
	fmt.Fprintln(os.Stderr, "  daemon: crash detected, restarting...")
	_ = d.Stop()
	err := d.Start(ctx)
	d.mu.Lock() // re-acquire before returning to caller
	if err != nil {
		fmt.Fprintf(os.Stderr, "  daemon: restart failed: %v\n", err)
		return err
	}
	fmt.Fprintln(os.Stderr, "  daemon: restarted successfully (context from prior queries lost)")
	return nil
}

// QueryFunc returns an InvokeFn that tries the daemon first and falls back
// to one-shot Invoke on error. This is the key integration point — callers
// get daemon-or-fallback transparently. When the daemon fails, it logs the
// event and the daemon will attempt restart on the next call.
//
// The returned function uses timeoutSec to derive a context deadline when
// the caller's ctx has none (matching the contract of one-shot Invoke).
// The backend argument is only used for the one-shot fallback path — the
// daemon always uses the backend it was configured with.
func (d *Daemon) QueryFunc() InvokeFn {
	return func(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error) {
		// Derive deadline from timeoutSec when ctx has none, so the daemon
		// path respects the same timeout contract as one-shot Invoke.
		if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeoutSec > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()
		}

		result, err := d.Query(ctx, prompt)
		if err == nil {
			return result, nil
		}
		// Daemon failed — fall back to one-shot and log.
		fmt.Fprintf(os.Stderr, "  daemon query failed (falling back to one-shot): %v\n", err)
		return InvokeFunc(ctx, backend, prompt, timeoutSec)
	}
}

// IsDaemonRunning checks whether a daemon is listening on the given Unix
// socket path by attempting a connection. Intended for CLI status commands,
// health checks, and multi-run coordination.
func IsDaemonRunning(sockPath string) bool {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// handleDaemonConn services a single connection on the daemon socket.
// It reads a DaemonRequest, queries the Claude process, and writes back
// a DaemonResponse.
func handleDaemonConn(c net.Conn, claude *claudeProcess, activityMu *sync.Mutex, lastActivity *time.Time) {
	defer func() { _ = c.Close() }()
	defer func() {
		if r := recover(); r != nil {
			_ = json.NewEncoder(c).Encode(DaemonResponse{Error: fmt.Sprintf("internal error: %v", r)})
		}
	}()

	activityMu.Lock()
	*lastActivity = time.Now()
	activityMu.Unlock()

	var req DaemonRequest
	if err := json.NewDecoder(c).Decode(&req); err != nil {
		_ = json.NewEncoder(c).Encode(DaemonResponse{Error: fmt.Sprintf("decode request: %s", err)})
		return
	}

	result, err := claude.query(req.Prompt)
	if err != nil {
		_ = json.NewEncoder(c).Encode(DaemonResponse{Error: err.Error()})
		return
	}

	_ = json.NewEncoder(c).Encode(DaemonResponse{Result: result})
}

// claudeProcess manages a long-running Claude CLI process using stream-json I/O.
type claudeProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	scanner  *bufio.Scanner
	mu       sync.Mutex
	waitOnce sync.Once
	waitErr  error
}

// wait calls cmd.Wait exactly once and caches the result. This is safe to call
// from multiple goroutines and avoids "os: process already finished" panics
// when the process has already been waited on (e.g., after crash recovery).
func (c *claudeProcess) wait() error {
	c.waitOnce.Do(func() {
		c.waitErr = c.cmd.Wait()
	})
	return c.waitErr
}

// filterEnv strips environment variables whose name matches the given prefix.
// This prevents the daemon's Claude child process from inheriting parent
// session state (e.g., CLAUDECODE_* variables).
func filterEnv(env []string, prefix string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// extractModelFromArgs finds "--model" in an args slice and returns the
// following value. Returns "" if the flag is not present.
func extractModelFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// buildDaemonArgs constructs the CLI arguments for a daemon Claude process.
// It does NOT pass Backend.Args directly because those contain flags like -p
// that are incompatible with --input-format stream-json.
func buildDaemonArgs(cfg DaemonConfig) []string {
	model := extractModelFromArgs(cfg.Backend.Args)
	if model == "" {
		model = "opus"
	}
	return []string{
		"--print",
		"--verbose",
		"--model", model,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
	}
}

// startClaudeProcess starts a Claude CLI process and completes the stream-json
// initialization handshake. The returned claudeProcess is ready to accept
// queries via the query method.
func startClaudeProcess(ctx context.Context, cfg DaemonConfig) (*claudeProcess, error) {
	args := buildDaemonArgs(cfg)

	cmd := exec.CommandContext(ctx, cfg.Backend.Command, args...) //nolint:gosec // command is from trusted CLIBackend config
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start claude: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if err := sendInitMessage(stdin); err != nil {
		cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		_ = cmd.Wait()     // reap to avoid zombie
		return nil, err
	}

	if err := waitForInit(scanner); err != nil {
		cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		_ = cmd.Wait()     // reap to avoid zombie
		return nil, err
	}

	if err := waitForResult(scanner); err != nil {
		cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
		_ = cmd.Wait()     // reap to avoid zombie
		return nil, err
	}

	return &claudeProcess{cmd: cmd, stdin: stdin, scanner: scanner}, nil
}

// sendInitMessage sends the initial user message that triggers Claude startup.
func sendInitMessage(stdin io.Writer) error {
	msg := StreamMessage{Type: "user"}
	msg.Message.Role = "user"
	msg.Message.Content = "Ready"

	initBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal init message: %w", err)
	}

	if _, err := stdin.Write(append(initBytes, '\n')); err != nil {
		return fmt.Errorf("send init message: %w", err)
	}
	return nil
}

// waitForInit reads stream lines until the system init message is received.
func waitForInit(scanner *bufio.Scanner) error {
	for scanner.Scan() {
		line := scanner.Text()

		var resp StreamResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.Type == "system" && resp.Subtype == "init" {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error during init: %w", err)
	}
	return fmt.Errorf("claude initialization failed: unexpected end of stream")
}

// waitForResult reads stream lines until a result message is received.
func waitForResult(scanner *bufio.Scanner) error {
	for scanner.Scan() {
		line := scanner.Text()

		var resp StreamResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.Type == "result" {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("unexpected end of stream before result message")
}

// query sends a prompt to the Claude process and returns the accumulated response.
// It is mutex-serialized so only one query runs at a time.
func (c *claudeProcess) query(prompt string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	msg := StreamMessage{Type: "user"}
	msg.Message.Role = "user"
	msg.Message.Content = prompt

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}

	if _, err := c.stdin.Write(append(msgBytes, '\n')); err != nil {
		return "", fmt.Errorf("write to claude: %w", err)
	}

	var result string
	gotResult := false
	for c.scanner.Scan() {
		var resp StreamResponse
		if err := json.Unmarshal([]byte(c.scanner.Text()), &resp); err != nil {
			continue
		}

		if resp.Type == "assistant" {
			result += extractResponseText(resp)
		}

		if resp.Type == "result" {
			gotResult = true
			if resp.Result != "" {
				result = resp.Result
			}
			break
		}
	}

	if err := c.scanner.Err(); err != nil {
		return "", fmt.Errorf("scanner error: %w", err)
	}
	if !gotResult {
		return "", fmt.Errorf("stream ended before result frame")
	}

	return strings.TrimSpace(result), nil
}
