package llmcli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// TestMain allows the test binary to double as a subprocess fixture. When
// LLMCLI_TEST_FIXTURE is set to a recognised mode name, or when
// FABRIKK_LLMCLI_TEST_VERSION / FABRIKK_LLMCLI_TEST_HANG are set, the binary
// executes the corresponding fixture behaviour and exits without running any
// tests.
//
// Note: this is the single TestMain for the llmcli package. Test files added
// by later tickets must NOT define their own TestMain; they should add new
// fixture cases here if needed.
func TestMain(m *testing.M) {
	// detect_test.go fixtures -----------------------------------------------
	// Fake version output mode: print the env-var value and exit.
	if v := os.Getenv("FABRIKK_LLMCLI_TEST_VERSION"); v != "" {
		fmt.Print(v)
		os.Exit(0)
	}
	// Hang mode: block until the process is killed (used for timeout tests).
	if os.Getenv("FABRIKK_LLMCLI_TEST_HANG") == "1" {
		select {} // blocks until killed by exec.CommandContext
	}

	// subprocess_test.go fixtures -------------------------------------------
	switch os.Getenv("LLMCLI_TEST_FIXTURE") {
	case "sleep":
		// Sleep long enough that the test can cancel and kill us first.
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "stderr_then_sleep":
		// Write the requested message to stderr, then sleep so the test can
		// verify the tail before killing us.
		msg := os.Getenv("LLMCLI_TEST_STDERR_MSG")
		_, _ = fmt.Fprintln(os.Stderr, msg)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "exit_zero":
		os.Exit(0)
	case "inspect_process":
		cwd, _ := os.Getwd()
		_, _ = fmt.Fprintf(os.Stdout, "cwd=%s\nenv=%s\n", cwd, os.Getenv("LLMCLI_TEST_ENV_VALUE"))
		_, _ = fmt.Fprint(os.Stderr, "inspect-stderr\n")
		os.Exit(0)
	case "codex_jsonl":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"item.completed","item":{"type":"command_execution"}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"item.completed","item":{"type":"agent_message","text":"VERK_RESULT: {\"status\":\"done\",\"completion_code\":\"ok\"}"}}`)
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"turn.completed","usage":{"input_tokens":11,"cached_input_tokens":3,"output_tokens":7,"total_tokens":18}}`)
		_, _ = fmt.Fprintln(os.Stderr, "jsonl-stderr")
		os.Exit(0)
	}

	os.Exit(m.Run())
}

// testExecutable returns the path of the currently-running test binary.
// Using the test binary as a subprocess fixture is cross-platform: it avoids
// shell dependencies and works on Windows where /bin/sleep is unavailable.
func testExecutable(t *testing.T) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	return exe
}

// baseEnv returns os.Environ with all LLMCLI_TEST_* variables stripped so
// fixture child processes do not accidentally inherit a fixture mode.
func baseEnv() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))

	for _, e := range env {
		if strings.HasPrefix(e, "LLMCLI_TEST_") {
			continue
		}

		filtered = append(filtered, e)
	}

	return filtered
}

// TestStreamReturnsBeforeProcessExit verifies Criterion 1: startSupervised
// returns immediately, before the subprocess has had a chance to exit.
func TestStreamReturnsBeforeProcessExit(t *testing.T) {
	exe := testExecutable(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec := processSpec{
		Command: exe,
		Env:     append(baseEnv(), "LLMCLI_TEST_FIXTURE=sleep"),
	}

	start := time.Now()
	s, err := startSupervised(ctx, spec)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("startSupervised: %v", err)
	}

	if elapsed >= time.Second {
		t.Errorf("startSupervised took %v; want < 1s (stream must return before subprocess exits)", elapsed)
	}

	// Clean up the sleeping subprocess.
	s.terminate(nil)
	_, _ = io.Copy(io.Discard, s.Stdout)
	_ = s.wait()
}

// TestSupervisor_WaitCalledOnce verifies Criterion 2: wait() may be called
// multiple times and always returns the same result; cmd.Wait() is called
// exactly once under the hood.
func TestSupervisor_WaitCalledOnce(t *testing.T) {
	exe := testExecutable(t)

	spec := processSpec{
		Command: exe,
		Env:     append(baseEnv(), "LLMCLI_TEST_FIXTURE=exit_zero"),
	}

	s, err := startSupervised(context.Background(), spec)
	if err != nil {
		t.Fatalf("startSupervised: %v", err)
	}

	// Read stdout to EOF so wait() does not block.
	_, _ = io.Copy(io.Discard, s.Stdout)

	err1 := s.wait()
	err2 := s.wait()
	err3 := s.wait()

	if err1 != err2 {
		t.Errorf("wait() inconsistency: first call=%v second call=%v", err1, err2)
	}

	if err2 != err3 {
		t.Errorf("wait() inconsistency: second call=%v third call=%v", err2, err3)
	}
}

// TestSupervisor_ContextCancelTerminatesProcess verifies Criterion 3 (first
// part): cancelling the parent context terminates the subprocess tree so that
// stdout reaches EOF and wait() returns within a reasonable time.
func TestSupervisor_ContextCancelTerminatesProcess(t *testing.T) {
	exe := testExecutable(t)
	ctx, cancel := context.WithCancel(context.Background())

	spec := processSpec{
		Command: exe,
		Env:     append(baseEnv(), "LLMCLI_TEST_FIXTURE=sleep"),
	}

	s, err := startSupervised(ctx, spec)
	if err != nil {
		t.Fatalf("startSupervised: %v", err)
	}

	// Trigger process-tree termination via context cancellation.
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, s.Stdout)
		_ = s.wait()
	}()

	select {
	case <-done:
		// Process was terminated as expected.
	case <-time.After(5 * time.Second):
		t.Error("process was not terminated within 5 s after context cancellation")
	}
}

// TestSupervisor_CapturesStderrTail verifies Criterion 3 (second part):
// stderr output written by the subprocess is captured in the bounded tail
// buffer and is accessible via stderrTail() after wait() returns.
func TestSupervisor_CapturesStderrTail(t *testing.T) {
	exe := testExecutable(t)
	ctx, cancel := context.WithCancel(context.Background())

	const stderrMsg = "test stderr content from fixture"
	spec := processSpec{
		Command: exe,
		Env: append(baseEnv(),
			"LLMCLI_TEST_FIXTURE=stderr_then_sleep",
			"LLMCLI_TEST_STDERR_MSG="+stderrMsg,
		),
	}

	s, err := startSupervised(ctx, spec)
	if err != nil {
		t.Fatalf("startSupervised: %v", err)
	}

	// Give the fixture time to write to stderr before we kill it.
	time.Sleep(150 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, s.Stdout)
		// wait() ensures the internal stderr-copying goroutine has flushed
		// all bytes into the tail buffer before we inspect it.
		_ = s.wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit within 5 s after context cancellation")
	}

	tail := s.stderrTail()
	if !strings.Contains(tail, stderrMsg) {
		t.Errorf("stderrTail() = %q; want it to contain %q", tail, stderrMsg)
	}
}

func TestSupervisor_AppliesDirEnvAndRawCapture(t *testing.T) {
	exe := testExecutable(t)
	dir := t.TempDir()
	wantDir, evalErr := filepath.EvalSymlinks(dir)
	if evalErr != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, evalErr)
	}

	var mu sync.Mutex
	captured := map[llmclient.RawStream][]byte{}
	capture := func(stream llmclient.RawStream, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured[stream] = append(captured[stream], data...)
	}

	spec := processSpec{
		Command: exe,
		Env: append(baseEnv(),
			"LLMCLI_TEST_FIXTURE=inspect_process",
			"LLMCLI_TEST_ENV_VALUE=from-env",
		),
		Dir:        dir,
		RawCapture: capture,
	}

	s, err := startSupervised(context.Background(), spec)
	if err != nil {
		t.Fatalf("startSupervised: %v", err)
	}
	stdoutBytes, _ := io.ReadAll(s.Stdout)
	if err := s.wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	stdout := string(stdoutBytes)
	if !strings.Contains(stdout, "cwd="+wantDir) {
		t.Errorf("stdout = %q; want cwd %q", stdout, wantDir)
	}
	if !strings.Contains(stdout, "env=from-env") {
		t.Errorf("stdout = %q; want env value", stdout)
	}
	if !strings.Contains(s.stderrTail(), "inspect-stderr") {
		t.Errorf("stderr tail = %q; want inspect-stderr", s.stderrTail())
	}

	mu.Lock()
	rawStdout := string(captured[llmclient.RawStreamStdout])
	rawStderr := string(captured[llmclient.RawStreamStderr])
	mu.Unlock()
	if !strings.Contains(rawStdout, "env=from-env") {
		t.Errorf("raw stdout = %q; want env output", rawStdout)
	}
	if !strings.Contains(rawStderr, "inspect-stderr") {
		t.Errorf("raw stderr = %q; want inspect-stderr", rawStderr)
	}
}

func TestResolveProcessEnvPrecedence(t *testing.T) {
	cfg := llmclient.DefaultRequestConfig()
	cfg.Environment = []string{"A=1", "B=base", "D=base", "B=duplicate"}
	cfg.EnvironmentSet = true
	cfg.EnvironmentOverlay = map[string]string{
		"B": "overlay",
		"C": "overlay",
	}

	env := resolveProcessEnv(cfg, map[string]string{
		"C": "override",
		"D": "override",
	})

	want := []string{"A=1", "B=overlay", "C=override", "D=override"}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Errorf("env = %v, want %v", env, want)
	}
}
