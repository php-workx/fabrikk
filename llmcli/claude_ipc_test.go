package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ─── infrastructure ───────────────────────────────────────────────────────────

// ipcTestServer is the test-controlled side of one fake claude IPC process.
type ipcTestServer struct {
	// StdinReader reads JSONL messages the backend wrote to the process stdin.
	// Set by makeIPCProcFactory when the process starts.
	StdinReader *bufio.Reader
	stdoutW     io.WriteCloser // test writes claude stdout frames here
	sup         *supervisor
	Kill        func() // closes sup.done (simulate unexpected process death)
}

// writeLine writes one newline-terminated JSONL line to the fake process stdout.
func (s *ipcTestServer) writeLine(line string) {
	_, _ = io.WriteString(s.stdoutW, line+"\n")
}

// crashWithError simulates a process crash by closing stdout with a non-EOF
// error. Readers of the fake stdout receive the error (not io.EOF), which the
// parser propagates as a protocol error rather than treating it as a clean EOF.
func (s *ipcTestServer) crashWithError(err error) {
	if pw, ok := s.stdoutW.(*io.PipeWriter); ok {
		_ = pw.CloseWithError(err)
		return
	}
	_ = s.stdoutW.Close()
}

// writeTextTurn emits a minimal assistant frame + success result frame.
func (s *ipcTestServer) writeTextTurn(text string) {
	encoded, _ := json.Marshal(text)
	s.writeLine(fmt.Sprintf(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":%s}]}}`,
		string(encoded),
	))
	s.writeLine(`{"type":"result","subtype":"success","total_cost_usd":0.001}`)
}

// drainStdin reads one JSON line from StdinReader. Blocks until the backend
// has written its turn message.
func (s *ipcTestServer) drainStdin(t *testing.T) {
	t.Helper()
	_, err := s.StdinReader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("drainStdin: %v", err)
	}
}

// newTestServer creates an ipcTestServer backed by in-process pipe pairs.
func newTestServer() *ipcTestServer {
	stdoutR, stdoutW := io.Pipe()
	sup, kill := newFakeSupervisor(stdoutR)
	return &ipcTestServer{
		stdoutW: stdoutW,
		sup:     sup,
		Kill:    kill,
	}
}

// newFakeSupervisor returns a supervisor backed by an in-process io.Reader.
// The returned kill function closes sup.done to simulate process death.
// wait() is pre-fired so it never calls cmd.Wait() (cmd is nil in tests).
func newFakeSupervisor(stdout io.Reader) (sup *supervisor, kill func()) {
	done := make(chan struct{})
	sup = &supervisor{
		Stdout: bufio.NewReader(stdout),
		done:   done,
	}
	// Pre-fire waitOnce so wait() returns nil without touching the nil cmd.
	sup.waitOnce.Do(func() { sup.waitErr = nil })
	kill = func() {
		select {
		case <-done:
		default:
			close(done)
		}
	}
	return sup, kill
}

// makeIPCProcFactory returns a procFactory that dispenses servers in order.
// The system/init frame is written asynchronously because io.Pipe is
// synchronous and waitForClaudeIPCReady runs after the factory returns.
func makeIPCProcFactory(t *testing.T, servers ...*ipcTestServer) func(context.Context, processSpecWithStdin) (*supervisor, error) {
	t.Helper()
	var mu sync.Mutex
	idx := 0
	return func(_ context.Context, spec processSpecWithStdin) (*supervisor, error) {
		mu.Lock()
		if idx >= len(servers) {
			mu.Unlock()
			return nil, fmt.Errorf("test: no more servers (started %d)", idx+1)
		}
		srv := servers[idx]
		idx++
		mu.Unlock()

		srv.StdinReader = bufio.NewReader(spec.stdin)
		// Emit init frame concurrently so waitForClaudeIPCReady can consume it.
		go srv.writeLine(`{"type":"system","subtype":"init","session_id":"test-sid","model":"test-model"}`)
		return srv.sup, nil
	}
}

// newIPCTestPair creates a backend + single-server pair for simple tests.
func newIPCTestPair(t *testing.T) (*ClaudeIPCBackend, *ipcTestServer) {
	t.Helper()
	srv := newTestServer()
	b := NewClaudeIPCBackend(CliInfo{Name: "claude-ipc", Path: "/fake/claude"})
	b.procFactory = makeIPCProcFactory(t, srv)
	return b, srv
}

// collectEvents drains ch until it is closed and returns all events.
func collectEvents(ch <-chan llmclient.Event) []llmclient.Event {
	var events []llmclient.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestClaudeIPC_SingleTurn verifies a complete turn: EventStart → text events
// → EventDone with StopEndTurn, sessionID from the init handshake.
func TestClaudeIPC_SingleTurn(t *testing.T) {
	b, srv := newIPCTestPair(t)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ch, err := b.Stream(context.Background(), simpleUserInput("hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	go func() {
		srv.drainStdin(t)
		srv.writeTextTurn("world")
		_ = srv.stdoutW.Close()
	}()

	events := collectEvents(ch)
	if len(events) == 0 {
		t.Fatal("no events received")
	}
	if events[0].Type != llmclient.EventStart {
		t.Errorf("first event = %q, want EventStart", events[0].Type)
	}
	if events[0].SessionID != "test-sid" {
		t.Errorf("sessionID = %q, want %q", events[0].SessionID, "test-sid")
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event = %q, want EventDone", last.Type)
	}
	if last.Reason != llmclient.StopEndTurn {
		t.Errorf("stop reason = %q, want StopEndTurn", last.Reason)
	}
}

// TestClaudeIPC_MultiTurn_SameProc verifies that a second Stream() call reuses
// the same process: same sessionID, proc still alive after both turns.
func TestClaudeIPC_MultiTurn_SameProc(t *testing.T) {
	b, srv := newIPCTestPair(t)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ctx := context.Background()

	ch1, err := b.Stream(ctx, simpleUserInput("turn1"))
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}
	go func() {
		srv.drainStdin(t)
		srv.writeTextTurn("reply1")
	}()
	events1 := collectEvents(ch1)

	ch2, err := b.Stream(ctx, simpleUserInput("turn2"))
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}
	go func() {
		srv.drainStdin(t)
		srv.writeTextTurn("reply2")
		_ = srv.stdoutW.Close()
	}()
	events2 := collectEvents(ch2)

	if len(events1) == 0 || events1[0].Type != llmclient.EventStart {
		t.Fatal("turn1: expected EventStart as first event")
	}
	if len(events2) == 0 || events2[0].Type != llmclient.EventStart {
		t.Fatal("turn2: expected EventStart as first event")
	}
	if events1[0].SessionID != events2[0].SessionID {
		t.Errorf("sessionID mismatch: turn1=%q turn2=%q — expected same proc", events1[0].SessionID, events2[0].SessionID)
	}

	b.mu.Lock()
	procAfter := b.proc
	b.mu.Unlock()
	if procAfter == nil {
		t.Error("proc is nil after two turns — expected it to stay alive")
	}
}

// TestClaudeIPC_RoutingKeyRestart_ModelChange verifies that a model change
// triggers a process restart before the second turn.
func TestClaudeIPC_RoutingKeyRestart_ModelChange(t *testing.T) {
	srv1, srv2 := newTestServer(), newTestServer()
	b := NewClaudeIPCBackend(CliInfo{Name: "claude-ipc", Path: "/fake/claude"})
	b.procFactory = makeIPCProcFactory(t, srv1, srv2)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ctx := context.Background()

	ch1, err := b.Stream(ctx, simpleUserInput("t1"), llmclient.WithModel("alpha"))
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}
	go func() {
		srv1.drainStdin(t)
		srv1.writeTextTurn("r1")
		_ = srv1.stdoutW.Close()
	}()
	collectEvents(ch1)

	ch2, err := b.Stream(ctx, simpleUserInput("t2"), llmclient.WithModel("beta"))
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}
	go func() {
		srv2.drainStdin(t)
		srv2.writeTextTurn("r2")
		_ = srv2.stdoutW.Close()
	}()
	events2 := collectEvents(ch2)

	if len(events2) == 0 || events2[0].Type != llmclient.EventStart {
		t.Fatal("expected EventStart from restarted proc")
	}
}

// TestClaudeIPC_RoutingKeyRestart_SystemPromptChange verifies that a system
// prompt change triggers a process restart.
func TestClaudeIPC_RoutingKeyRestart_SystemPromptChange(t *testing.T) {
	srv1, srv2 := newTestServer(), newTestServer()
	b := NewClaudeIPCBackend(CliInfo{Name: "claude-ipc", Path: "/fake/claude"})
	b.procFactory = makeIPCProcFactory(t, srv1, srv2)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ctx := context.Background()
	msg := []llmclient.Message{{
		Role:    llmclient.RoleUser,
		Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "hi"}},
	}}

	ch1, err := b.Stream(ctx, &llmclient.Context{SystemPrompt: "prompt-A", Messages: msg})
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}
	go func() {
		srv1.drainStdin(t)
		srv1.writeTextTurn("r1")
		_ = srv1.stdoutW.Close()
	}()
	collectEvents(ch1)

	ch2, err := b.Stream(ctx, &llmclient.Context{SystemPrompt: "prompt-B", Messages: msg})
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}
	go func() {
		srv2.drainStdin(t)
		srv2.writeTextTurn("r2")
		_ = srv2.stdoutW.Close()
	}()
	events2 := collectEvents(ch2)

	if len(events2) == 0 || events2[0].Type != llmclient.EventStart {
		t.Fatal("expected EventStart from restarted proc")
	}
}

// TestClaudeIPC_ProtocolError_EmitsErrorAndClearsProc verifies that closing
// stdout mid-turn emits EventError and clears the proc reference.
func TestClaudeIPC_ProtocolError_EmitsErrorAndClearsProc(t *testing.T) {
	b, srv := newIPCTestPair(t)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ch, err := b.Stream(context.Background(), simpleUserInput("hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	go func() {
		srv.drainStdin(t)
		// Use a concrete error (not plain close) so the parser sees a non-EOF
		// read error and propagates it as a protocol error rather than emitting
		// a synthetic done event.
		srv.crashWithError(fmt.Errorf("process crashed"))
	}()

	events := collectEvents(ch)
	last := events[len(events)-1]
	if last.Type != llmclient.EventError {
		t.Errorf("last event = %q, want EventError", last.Type)
	}

	b.mu.Lock()
	proc := b.proc
	b.mu.Unlock()
	if proc != nil {
		t.Error("proc is non-nil after protocol error — expected cleared")
	}
}

// TestClaudeIPC_MidTurnCancel_RestartsProc verifies that cancelling a Stream
// context clears proc. The stop reason is non-deterministic (StopCancelled
// or StopEndTurn, depending on which select branch fires first) so only the
// proc-cleared invariant is asserted — that is the core behaviour under test.
//
// After cancellation restartAfterProtocolError drains proc.sup.Stdout.
// Closing srv.stdoutW unblocks that drain so the goroutine can exit.
func TestClaudeIPC_MidTurnCancel_RestartsProc(t *testing.T) {
	b, srv := newIPCTestPair(t)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := b.Stream(ctx, simpleUserInput("hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Drain stdin (unblocks proc.enc.Encode), cancel the context, then close
	// stdout so restartAfterProtocolError's io.Copy drain returns immediately.
	go func() {
		srv.drainStdin(t)
		cancel()
		_ = srv.stdoutW.Close()
	}()

	events := collectEvents(ch)
	if len(events) == 0 {
		t.Fatal("no events received")
	}
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event = %q, want EventDone", last.Type)
	}

	b.mu.Lock()
	proc := b.proc
	b.mu.Unlock()
	if proc != nil {
		t.Error("proc is non-nil after cancellation — expected cleared")
	}
}

// TestClaudeIPC_Close_TerminatesProcess verifies that Close() clears the proc,
// sets the closed flag, and causes subsequent Stream() calls to fail.
func TestClaudeIPC_Close_TerminatesProcess(t *testing.T) {
	b, srv := newIPCTestPair(t)

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	go func() {
		srv.drainStdin(t)
		srv.writeTextTurn("reply")
		_ = srv.stdoutW.Close()
	}()
	collectEvents(ch)

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	b.mu.Lock()
	proc, closed := b.proc, b.closed
	b.mu.Unlock()
	if proc != nil {
		t.Error("proc still set after Close()")
	}
	if !closed {
		t.Error("closed flag not set after Close()")
	}

	_, err = b.Stream(ctx, simpleUserInput("after-close"))
	if err == nil {
		t.Error("Stream after Close() returned nil error, want error")
	}
}

// TestClaudeIPC_Ready_AfterClose verifies that Ready() returns ReadyUnknown
// once the backend has been closed.
func TestClaudeIPC_Ready_AfterClose(t *testing.T) {
	b := NewClaudeIPCBackend(CliInfo{Name: "claude-ipc", Path: "/fake/claude"})
	_ = b.Close()

	report := b.Ready(context.Background())
	if report.State != llmclient.ReadyUnknown {
		t.Errorf("Ready after Close: state = %v, want ReadyUnknown", report.State)
	}
}

// TestClaudeIPC_ConcurrentStream_Serialized verifies that two concurrent
// Stream() calls are serialized: server receives both requests in order and
// the second turn's send does not begin until the first is done.
//
// The server goroutine is started after ch1 = b.Stream() so that
// srv.StdinReader (set inside procFactory during Stream) is guaranteed
// visible without a data race.
func TestClaudeIPC_ConcurrentStream_Serialized(t *testing.T) {
	b, srv := newIPCTestPair(t)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	ctx := context.Background()
	input := simpleUserInput("hello")

	var (
		mu       sync.Mutex
		received []int
	)

	// ch1 triggers process start and sets srv.StdinReader (synchronous inside Stream).
	ch1, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}

	// Start server goroutine after ch1 = b.Stream() so srv.StdinReader is visible
	// (Go happens-before: writes before go statement are visible to the goroutine).
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for i := 1; i <= 2; i++ {
			srv.drainStdin(t)
			mu.Lock()
			received = append(received, i)
			mu.Unlock()
			srv.writeTextTurn(fmt.Sprintf("reply%d", i))
		}
		_ = srv.stdoutW.Close()
	}()

	// ch2 blocks on turnMu until ch1's goroutine releases it (after turn 1 done).
	ch2, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}

	collectEvents(ch1)
	collectEvents(ch2)
	<-serverDone

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0] != 1 || received[1] != 2 {
		t.Errorf("server received turns out of order: %v", received)
	}
}

// TestClaudeIPCRegistry_StaticCapabilities verifies key capability fields on
// the static capability descriptor.
func TestClaudeIPCRegistry_StaticCapabilities(t *testing.T) {
	caps := claudeIPCStaticCapabilities("v1.0")

	if caps.Backend != "claude-ipc" {
		t.Errorf("Backend = %q, want %q", caps.Backend, "claude-ipc")
	}
	if !caps.MultiTurn {
		t.Error("MultiTurn must be true")
	}
	if !caps.OllamaRouting {
		t.Error("OllamaRouting must be true")
	}
	if caps.Streaming != llmclient.StreamingStructured {
		t.Errorf("Streaming = %v, want StreamingStructured", caps.Streaming)
	}
	if got := caps.OptionSupport[llmclient.OptionSession]; got != llmclient.OptionSupportNone {
		t.Errorf("OptionSession = %v, want OptionSupportNone", got)
	}
	if got := caps.OptionSupport[llmclient.OptionModel]; got != llmclient.OptionSupportFull {
		t.Errorf("OptionModel = %v, want OptionSupportFull", got)
	}
}

// TestClaudeIPC_WaitForReady_EOFBeforeInit verifies that closing stdout before
// the init frame returns io.ErrUnexpectedEOF.
func TestClaudeIPC_WaitForReady_EOFBeforeInit(t *testing.T) {
	pr, pw := io.Pipe()
	_ = pw.Close()

	_, err := waitForClaudeIPCReady(context.Background(), bufio.NewReader(pr))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("got %v, want io.ErrUnexpectedEOF", err)
	}
}

// TestClaudeIPC_WaitForReady_ContextCancelled verifies that a pre-cancelled
// context causes waitForClaudeIPCReady to return context.Canceled immediately.
func TestClaudeIPC_WaitForReady_ContextCancelled(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: ctx.Done() is already closed

	_, err := waitForClaudeIPCReady(ctx, bufio.NewReader(pr))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// TestClaudeIPC_Timeout_ClearsProc verifies that a per-turn timeout (via
// llmclient.WithTimeout) triggers restartAfterProtocolError so that proc is nil
// after the timed-out turn, and a subsequent Stream() call succeeds using a
// fresh process.
//
// The server goroutine for turn 1 delays indefinitely to force the timeout.
// After asserting proc == nil, a second server handles turn 2 normally.
func TestClaudeIPC_Timeout_ClearsProc(t *testing.T) {
	srv1, srv2 := newTestServer(), newTestServer()
	b := NewClaudeIPCBackend(CliInfo{Name: "claude-ipc", Path: "/fake/claude"})
	b.procFactory = makeIPCProcFactory(t, srv1, srv2)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	// Turn 1: very short timeout so it fires before the server responds.
	ch1, err := b.Stream(context.Background(), simpleUserInput("t1"), llmclient.WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}

	// Server 1: drain stdin, then delay. After the backend declares the turn
	// done (proc cleared) close stdout so restartAfterProtocolError's drain exits.
	go func() {
		srv1.drainStdin(t)
		// Give the timeout time to fire and restartAfterProtocolError to run.
		time.Sleep(200 * time.Millisecond)
		_ = srv1.stdoutW.Close()
	}()

	events1 := collectEvents(ch1)
	if len(events1) == 0 {
		t.Fatal("no events from turn 1")
	}
	last1 := events1[len(events1)-1]
	if last1.Type != llmclient.EventDone {
		t.Errorf("turn 1 last event = %q, want EventDone", last1.Type)
	}

	b.mu.Lock()
	procAfter1 := b.proc
	b.mu.Unlock()
	if procAfter1 != nil {
		t.Error("proc is non-nil after timeout — expected cleared")
	}

	// Turn 2: normal turn using the fresh server2.
	ch2, err := b.Stream(context.Background(), simpleUserInput("t2"))
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}
	go func() {
		srv2.drainStdin(t)
		srv2.writeTextTurn("ok")
		_ = srv2.stdoutW.Close()
	}()
	events2 := collectEvents(ch2)
	if len(events2) == 0 || events2[0].Type != llmclient.EventStart {
		t.Error("turn 2: expected EventStart from fresh proc")
	}
	last2 := events2[len(events2)-1]
	if last2.Type != llmclient.EventDone || last2.Reason != llmclient.StopEndTurn {
		t.Errorf("turn 2 last event = %q reason = %q, want EventDone/StopEndTurn", last2.Type, last2.Reason)
	}
}

// TestClaudeIPC_LargePrompt_NoArgLimit verifies that a user prompt larger than
// the per-call backend's 32 KB stdin threshold is sent without truncation via the
// JSON encoder, and that the backend emits a normal successful turn.
//
// Unlike the per-call backend which must switch from argv to stdin at 32 KB, the
// IPC backend always sends user messages via the JSON encoder — there is no argv
// size concern regardless of prompt length.
func TestClaudeIPC_LargePrompt_NoArgLimit(t *testing.T) {
	b, srv := newIPCTestPair(t)
	defer b.Close() //nolint:errcheck // best-effort shutdown in test cleanup

	// Build a prompt larger than the per-call 32 KB threshold.
	largeText := strings.Repeat("x", 40_000)
	largeInput := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: largeText}},
		}},
	}

	ch, err := b.Stream(context.Background(), largeInput)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	receivedLenCh := make(chan int, 1)
	go func() {
		line, readErr := srv.StdinReader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			t.Errorf("drainStdin: %v", readErr)
		}
		receivedLenCh <- len(line)
		srv.writeTextTurn("ok")
		_ = srv.stdoutW.Close()
	}()

	events := collectEvents(ch)
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone || last.Reason != llmclient.StopEndTurn {
		t.Errorf("last event = %q reason = %q, want EventDone/StopEndTurn", last.Type, last.Reason)
	}
	// The JSON line must be at least as long as the large text itself.
	if receivedLen := <-receivedLenCh; receivedLen < len(largeText) {
		t.Errorf("stdin line length = %d, want >= %d (no truncation)", receivedLen, len(largeText))
	}
}

// TestClaudeIPC_ParseIPCTurn_EmitsStartFirst verifies that parseClaudeIPCTurn
// always emits EventStart as the very first event, carrying the given sessionID.
func TestClaudeIPC_ParseIPCTurn_EmitsStartFirst(t *testing.T) {
	pr, pw := io.Pipe()
	r := bufio.NewReader(pr)

	ch := make(chan llmclient.Event, 32)
	te := newTerminalEmitter(ch)

	go func() {
		_, _ = io.WriteString(pw, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`+"\n")
		_, _ = io.WriteString(pw, `{"type":"result","subtype":"success","total_cost_usd":0.0}`+"\n")
		_ = pw.Close()
	}()

	if err := parseClaudeIPCTurn(context.Background(), r, ch, te, nil, "my-session"); err != nil {
		t.Fatalf("parseClaudeIPCTurn: %v", err)
	}

	var events []llmclient.Event
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) == 0 {
		t.Fatal("no events received")
	}
	if events[0].Type != llmclient.EventStart {
		t.Errorf("first event = %q, want EventStart", events[0].Type)
	}
	if events[0].SessionID != "my-session" {
		t.Errorf("sessionID = %q, want %q", events[0].SessionID, "my-session")
	}
}
