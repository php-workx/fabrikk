package agentcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain allows the test binary to act as a fake Claude CLI.
// When FABRIKK_TEST_CLAUDE_FIXTURE is set, the binary reads NDJSON from stdin
// and responds with stream-json messages, mimicking the Claude CLI protocol.
func TestMain(m *testing.M) {
	if os.Getenv("FABRIKK_TEST_CLAUDE_FIXTURE") == "1" {
		runFakeClaudeProcess()
		return
	}
	os.Exit(m.Run())
}

// runFakeClaudeProcess implements a minimal Claude CLI stream-json fixture.
// It reads StreamMessage NDJSON from stdin. On the first message it emits the
// init handshake (system init + result). On subsequent messages it accumulates
// all received prompts and responds with the full history joined by " | ",
// prefixed with "history: ". This simulates context retention — each response
// includes all prior prompts, proving the process maintained conversation state.
func runFakeClaudeProcess() {
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	first := true

	var history []string

	for scanner.Scan() {
		var msg StreamMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		if first {
			first = false
			// Init sequence: system init + result "ready".
			_ = enc.Encode(StreamResponse{Type: "system", Subtype: "init"})
			_ = enc.Encode(StreamResponse{Type: "result", Result: "ready"})
			continue
		}

		// Accumulate the prompt into conversation history.
		prompt := msg.Message.Content
		history = append(history, prompt)
		reply := "history: " + strings.Join(history, " | ")

		// Respond with the full history as assistant text + result.
		resp := StreamResponse{Type: "assistant"}
		resp.Message.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: reply}}
		_ = enc.Encode(resp)
		_ = enc.Encode(StreamResponse{Type: "result", Result: reply})
	}
}

// fixtureBackend returns a CLIBackend pointing to the test binary acting as a
// fake Claude. The Backend.Args include --model so that buildDaemonArgs picks
// up the model value; the rest of the daemon args are constructed internally.
func fixtureBackend(t *testing.T) CLIBackend {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("get test executable: %v", err)
	}
	return CLIBackend{
		Command: exe,
		Args:    []string{"-test.run=^$", "--model", "test"},
	}
}

// fixtureDaemonConfig returns a DaemonConfig using the fixture backend.
// It uses a short temp directory under /tmp to keep Unix socket paths under
// the ~104-byte macOS limit (t.TempDir paths can exceed this).
func fixtureDaemonConfig(t *testing.T) DaemonConfig {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fbd")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return DaemonConfig{
		Backend:   fixtureBackend(t),
		SocketDir: dir,
		RunID:     fmt.Sprintf("t%d", time.Now().UnixNano()%1e9),
	}
}

func TestDaemon_StartStop(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")

	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Socket file must exist after start.
	if _, err := os.Stat(d.SocketPath()); err != nil {
		t.Fatalf("socket file should exist after Start: %v", err)
	}

	// PID file must exist after start.
	if _, err := os.Stat(d.pidPath); err != nil {
		t.Fatalf("pid file should exist after Start: %v", err)
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Socket and PID files must be removed after stop.
	if _, err := os.Stat(d.SocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket file should be removed after Stop, got err: %v", err)
	}
	if _, err := os.Stat(d.pidPath); !os.IsNotExist(err) {
		t.Errorf("pid file should be removed after Stop, got err: %v", err)
	}
}

func TestDaemon_Query(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")

	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = d.Stop() }()

	prompt := "hello from test"
	result, err := d.Query(ctx, prompt)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if !strings.Contains(result, prompt) {
		t.Errorf("Query result = %q, should contain %q", result, prompt)
	}
}

func TestDaemon_QueryFunc_Fallback(t *testing.T) {
	// Create a daemon but do NOT start it. QueryFunc should fall back to
	// the InvokeFunc stub when the daemon socket is unreachable.
	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	// Stub InvokeFunc to capture the fallback call.
	origInvoke := InvokeFunc
	defer func() { InvokeFunc = origInvoke }()

	var fallbackCalled bool
	var fallbackPrompt string
	InvokeFunc = func(_ context.Context, _ *CLIBackend, prompt string, _ int) (string, error) {
		fallbackCalled = true
		fallbackPrompt = prompt
		return "fallback-result", nil
	}

	fn := d.QueryFunc()
	backend := fixtureBackend(t)
	result, err := fn(context.Background(), &backend, "test-prompt", 30)
	if err != nil {
		t.Fatalf("QueryFunc: %v", err)
	}

	if !fallbackCalled {
		t.Fatal("expected InvokeFunc fallback to be called when daemon is not running")
	}
	if fallbackPrompt != "test-prompt" {
		t.Errorf("fallback prompt = %q, want %q", fallbackPrompt, "test-prompt")
	}
	if result != "fallback-result" {
		t.Errorf("result = %q, want %q", result, "fallback-result")
	}
}

func TestDaemon_FilterEnv(t *testing.T) {
	env := []string{
		"CLAUDECODE_SESSION=abc",
		"PATH=/usr/bin",
		"CLAUDECODE_TOKEN=xyz",
		"HOME=/home/test",
	}
	filtered := filterEnv(env, "CLAUDECODE")

	want := map[string]bool{
		"PATH=/usr/bin":   true,
		"HOME=/home/test": true,
	}

	if len(filtered) != len(want) {
		t.Fatalf("filterEnv returned %d entries, want %d: %v", len(filtered), len(want), filtered)
	}

	for _, e := range filtered {
		if !want[e] {
			t.Errorf("unexpected entry in filtered env: %q", e)
		}
	}
}

func TestDaemon_StaleSocketCleanup(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")

	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	// Create a stale socket file. We create a real listener, grab the path,
	// then close the listener (which removes the file on some OSes) and
	// re-create the socket file manually to simulate a crash leaving it behind.
	staleLn, err := net.Listen("unix", d.SocketPath())
	if err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	_ = staleLn.Close()

	// Re-create the socket file if the OS removed it on Close.
	if _, err := os.Stat(d.SocketPath()); err != nil {
		if err := os.WriteFile(d.SocketPath(), nil, 0o600); err != nil {
			t.Fatalf("recreate stale socket file: %v", err)
		}
	}

	// The stale socket file should exist on disk.
	if _, err := os.Stat(d.SocketPath()); err != nil {
		t.Fatalf("stale socket file should exist: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should detect and remove the stale socket, then succeed.
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start with stale socket: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// Daemon should be running — socket must exist and accept connections.
	if _, err := os.Stat(d.SocketPath()); err != nil {
		t.Fatalf("socket file should exist after Start: %v", err)
	}

	result, err := d.Query(ctx, "stale-test")
	if err != nil {
		t.Fatalf("Query after stale cleanup: %v", err)
	}
	if !strings.Contains(result, "stale-test") {
		t.Errorf("Query result = %q, should contain %q", result, "stale-test")
	}
}

func TestDaemon_CrashRecovery(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")

	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = d.Stop() }()

	// Step 1: Normal query should succeed through the daemon.
	result, err := d.Query(ctx, "before-crash")
	if err != nil {
		t.Fatalf("Query before crash: %v", err)
	}
	if !strings.Contains(result, "before-crash") {
		t.Errorf("pre-crash result = %q, should contain %q", result, "before-crash")
	}

	// Step 2: Kill the Claude process to simulate a crash.
	if err := d.process.cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill process: %v", err)
	}
	// Wait for the process to actually exit.
	_ = d.process.wait()

	// Step 3: QueryFunc should fall back to one-shot after crash.
	origInvoke := InvokeFunc
	defer func() { InvokeFunc = origInvoke }()

	var fallbackCalled bool
	InvokeFunc = func(_ context.Context, _ *CLIBackend, prompt string, _ int) (string, error) {
		fallbackCalled = true
		return "fallback:" + prompt, nil
	}

	fn := d.QueryFunc()
	backend := fixtureBackend(t)
	fbResult, err := fn(ctx, &backend, "during-crash", 30)
	if err != nil {
		t.Fatalf("QueryFunc during crash: %v", err)
	}
	if !fallbackCalled {
		t.Fatal("expected fallback to be called after crash")
	}
	if fbResult != "fallback:during-crash" {
		t.Errorf("fallback result = %q, want %q", fbResult, "fallback:during-crash")
	}

	// Step 4: The needsRestart flag should be set. A direct Query call should
	// trigger restart and succeed through the daemon again.
	if !d.needsRestart {
		t.Fatal("expected needsRestart to be true after crash")
	}

	// Restore real InvokeFunc so we can tell if the daemon path is used.
	InvokeFunc = origInvoke

	result, err = d.Query(ctx, "after-restart")
	if err != nil {
		t.Fatalf("Query after restart: %v", err)
	}
	if !strings.Contains(result, "after-restart") {
		t.Errorf("post-restart result = %q, should contain %q", result, "after-restart")
	}
}

func TestDaemon_ContextRetention(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")

	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Query 1: establish context.
	r1, err := d.Query(ctx, "remember alpha")
	if err != nil {
		t.Fatalf("Query 1: %v", err)
	}
	if !strings.Contains(r1, "alpha") {
		t.Fatalf("query 1 result = %q, should contain %q", r1, "alpha")
	}

	// Query 2: same daemon — fixture should retain context from query 1.
	r2, err := d.Query(ctx, "remember beta")
	if err != nil {
		t.Fatalf("Query 2: %v", err)
	}
	if !strings.Contains(r2, "alpha") {
		t.Errorf("query 2 result = %q, should contain %q (context retained from query 1)", r2, "alpha")
	}
	if !strings.Contains(r2, "beta") {
		t.Errorf("query 2 result = %q, should contain %q", r2, "beta")
	}

	// Stop the daemon — context is lost when the process exits.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Start a fresh daemon (new fixture process, no prior context).
	cfg2 := fixtureDaemonConfig(t)
	d2 := NewDaemon(cfg2)

	if err := d2.Start(ctx); err != nil {
		t.Fatalf("Start fresh daemon: %v", err)
	}
	defer func() { _ = d2.Stop() }()

	// Query 3: same prompt as query 2, but fresh process — no context from query 1.
	r3, err := d2.Query(ctx, "remember beta")
	if err != nil {
		t.Fatalf("Query 3: %v", err)
	}
	if !strings.Contains(r3, "beta") {
		t.Errorf("query 3 result = %q, should contain %q", r3, "beta")
	}
	if strings.Contains(r3, "alpha") {
		t.Errorf("query 3 result = %q, should NOT contain %q (context lost on restart)", r3, "alpha")
	}
}

func TestDaemon_ConcurrentQuery(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")
	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = d.Stop() }()

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			result, err := d.Query(ctx, fmt.Sprintf("concurrent-%d", n))
			if err != nil {
				errs <- fmt.Errorf("query %d: %w", n, err)
				return
			}
			if !strings.Contains(result, fmt.Sprintf("concurrent-%d", n)) {
				errs <- fmt.Errorf("query %d: unexpected result %q", n, result)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestDaemon_SocketPathTooLong(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")
	cfg := fixtureDaemonConfig(t)
	// Create a very long RunID to exceed Unix socket path limit (~104 bytes on macOS)
	cfg.RunID = strings.Repeat("x", 200)
	d := NewDaemon(cfg)
	err := d.Start(context.Background())
	if err == nil {
		_ = d.Stop()
		t.Fatal("expected error for socket path too long, got nil")
	}
	// The error should come from net.Listen, not a panic
	t.Logf("got expected error: %v", err)
}

func TestDaemon_AlreadyRunning(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")
	cfg := fixtureDaemonConfig(t)

	// Start daemon A
	a := NewDaemon(cfg)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("start A: %v", err)
	}

	// Try to start daemon B on the same socket
	b := NewDaemon(cfg)
	err := b.Start(context.Background())
	if err == nil {
		_ = b.Stop()
		_ = a.Stop()
		t.Fatal("expected error when daemon already running, got nil")
	}
	if !strings.Contains(err.Error(), "daemon already running") {
		_ = a.Stop()
		t.Fatalf("expected 'daemon already running' error, got: %v", err)
	}

	// Stop A, now B should be able to start
	_ = a.Stop()
	if err := b.Start(context.Background()); err != nil {
		t.Fatalf("start B after A stopped: %v", err)
	}
	defer func() { _ = b.Stop() }()

	// Verify B works
	result, err := b.Query(context.Background(), "hello from B")
	if err != nil {
		t.Fatalf("query B: %v", err)
	}
	if !strings.Contains(result, "hello from B") {
		t.Errorf("unexpected result from B: %q", result)
	}
}

func TestDaemon_GracefulShutdown(t *testing.T) {
	t.Setenv("FABRIKK_TEST_CLAUDE_FIXTURE", "1")

	cfg := fixtureDaemonConfig(t)
	d := NewDaemon(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify daemon is working with a query.
	result, err := d.Query(ctx, "graceful-test")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.Contains(result, "graceful-test") {
		t.Errorf("Query result = %q, should contain %q", result, "graceful-test")
	}

	// Record the process so we can check it exited.
	proc := d.process

	// Stop should close stdin (sending EOF) and the fixture process should
	// exit naturally when its scanner sees EOF.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify the process is no longer running. The wait() call in Stop should
	// have completed. A second call must be safe (idempotent via sync.Once).
	waitErr := proc.wait()
	// The fixture exits with 0 on EOF; we accept any exit as long as wait
	// does not panic or block.
	_ = waitErr

	// Verify socket file is removed.
	if _, err := os.Stat(d.SocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket file should be removed after Stop, got err: %v", err)
	}

	// Verify PID file is removed.
	if _, err := os.Stat(d.pidPath); !os.IsNotExist(err) {
		t.Errorf("pid file should be removed after Stop, got err: %v", err)
	}

	// Calling Stop again must be safe (idempotent).
	if err := d.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
