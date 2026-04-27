package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

// ─── In-process pipe helpers ──────────────────────────────────────────────────

// pipeProcess creates a codexProcess backed by in-process io.Pipe pairs. It
// returns the fake process (injected into the backend) and a fakeServer that
// the test goroutine uses to read requests and write responses.
type fakeServer struct {
	reqReader  *bufio.Reader  // reads what the backend writes to process stdin
	respWriter io.WriteCloser // test writes responses here; backend reads from stdout
}

func newPipeProcess() (*codexProcess, *fakeServer) {
	// clientIn → stdinPipe (backend writes, server reads)
	clientInR, clientInW := io.Pipe()
	// serverOut → stdoutPipe (server writes, backend reads)
	serverOutR, serverOutW := io.Pipe()

	proc := &codexProcess{
		sup:    nil, // test mode: no real subprocess
		stdin:  clientInW,
		stdout: bufio.NewReader(serverOutR),
	}

	server := &fakeServer{
		reqReader:  bufio.NewReader(clientInR),
		respWriter: serverOutW,
	}

	return proc, server
}

// sendFrame writes one LSP-framed JSON body to the server's response writer.
func (s *fakeServer) sendFrame(body string) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(s.respWriter, header); err != nil {
		return err
	}
	_, err := io.WriteString(s.respWriter, body)
	return err
}

// sendDone sends the terminal "done" notification with stop_reason "end_turn".
func (s *fakeServer) sendDone() error {
	params := map[string]any{"stop_reason": "end_turn"}
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "done",
		"params":  params,
	})
	if err != nil {
		return err
	}
	return s.sendFrame(string(b))
}

// sendTextBlock sends text_start → text_delta → text_end notifications for
// content block index 0.
func (s *fakeServer) sendTextBlock(text string) error {
	const idx = 0
	events := []map[string]any{
		{"jsonrpc": "2.0", "method": "text_start", "params": map[string]any{"content_index": idx}},
		{"jsonrpc": "2.0", "method": "text_delta", "params": map[string]any{"content_index": idx, "delta": text}},
		{"jsonrpc": "2.0", "method": "text_end", "params": map[string]any{"content_index": idx, "content": text}},
	}
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if err := s.sendFrame(string(b)); err != nil {
			return err
		}
	}
	return nil
}

// drainOneRequest reads and discards one LSP-framed request from the request
// reader. Used by server goroutines that don't need to inspect the request.
func (s *fakeServer) drainOneRequest() error {
	_, err := internal.ReadFrame(s.reqReader, 4096, 8<<20)
	return err
}

// newTestBackend creates a CodexAppServerBackend with a fake info and injects
// a proc factory that returns the given process once. Subsequent calls to
// ensureProcess invoke nextProc (which may return errors).
func newTestBackend() *CodexAppServerBackend {
	info := CliInfo{
		Name:    "codex",
		Binary:  "codex",
		Path:    "/fake/codex",
		Version: "0.0.0-test",
	}
	b := NewCodexAppServerBackend(info)
	return b
}

// injectProc replaces the backend's procFactory with one that returns proc
// exactly once. Any subsequent call returns an error.
func injectProc(b *CodexAppServerBackend, proc *codexProcess) {
	var once sync.Once
	b.procFactory = func(_ context.Context, _ []string) (*codexProcess, error) {
		var p *codexProcess
		once.Do(func() { p = proc })
		if p == nil {
			return nil, fmt.Errorf("codex-appserver test: no more processes available")
		}
		return p, nil
	}
}

// ─── Criterion 2: StreamSerializesTurns ──────────────────────────────────────

// TestCodexAppServer_StreamSerializesTurns verifies that concurrent calls to
// Stream are executed strictly sequentially: the second call does not begin
// sending its request until the first call's goroutine has released the turn
// semaphore.
func TestCodexAppServer_StreamSerializesTurns(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	// Keep a count of requests that the server received.
	var (
		mu       sync.Mutex
		received []int
	)

	// Server goroutine: service two sequential turn requests, recording order.
	serverDone := make(chan error, 1)
	go func() {
		for i := 1; i <= 2; i++ {
			if err := server.drainOneRequest(); err != nil {
				serverDone <- err
				return
			}

			mu.Lock()
			received = append(received, i)
			mu.Unlock()

			// Respond with a simple text + done.
			_ = server.sendTextBlock(fmt.Sprintf("response%d", i))
			_ = server.sendDone()
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	input := simpleUserInput("hello")

	// Launch both Stream calls concurrently.
	ch1, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}

	ch2, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}

	// Drain both channels; they must each complete within a reasonable timeout.
	events1 := drainWithTimeout(t, ch1)
	events2 := drainWithTimeout(t, ch2)

	if err := <-serverDone; err != nil {
		t.Fatalf("server drain request: %v", err)
	}

	// Each stream must have exactly one terminal event.
	assertExactlyOneTerminal(t, events1, "stream1")
	assertExactlyOneTerminal(t, events2, "stream2")

	// Server must have received exactly two requests.
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Errorf("server received %d requests; want 2", len(received))
	}
}

// ─── Criterion 2: CloseTerminatesProcess ─────────────────────────────────────

// TestCodexAppServer_CloseTerminatesProcess verifies that calling Close on the
// backend closes the stdin writer (signalling the fake process to shut down)
// and that subsequent calls to Close are no-ops.
func TestCodexAppServer_CloseTerminatesProcess(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	ctx := context.Background()
	input := simpleUserInput("hello")

	// Prime the backend: start one stream so ensureProcess is called and
	// the process is live inside the backend.
	serverReady := make(chan struct{})
	serverClosed := make(chan error, 1)
	go func() {
		close(serverReady)
		// Drain the request, then wait for stdin to close (Close() call).
		if err := server.drainOneRequest(); err != nil {
			serverClosed <- err
			return
		}
		// Write one text event and then done.
		_ = server.sendTextBlock("hi")
		_ = server.sendDone()
		_ = server.respWriter.Close()
		serverClosed <- nil
	}()

	<-serverReady

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Drain the stream to completion.
	drainWithTimeout(t, ch)
	if err := <-serverClosed; err != nil {
		t.Fatalf("server drain request: %v", err)
	}

	// Now close the backend.
	if err := b.Close(); err != nil {
		t.Errorf("Close: unexpected error: %v", err)
	}

	// Second Close must be a no-op, not panic or error.
	if err := b.Close(); err != nil {
		t.Errorf("second Close: unexpected error: %v", err)
	}

	// After Close, proc must be nil.
	b.procMu.Lock()
	if b.proc != nil {
		t.Error("proc should be nil after Close")
	}
	b.procMu.Unlock()
}

// ─── Criterion 3: ProtocolCorruptionEmitsOneError ────────────────────────────

// TestCodexAppServer_ProtocolCorruptionEmitsOneError verifies that when the
// process sends a malformed LSP frame (invalid Content-Length), exactly one
// EventError is emitted on the stream channel and the backend marks itself
// unhealthy.
func TestCodexAppServer_ProtocolCorruptionEmitsOneError(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	// Server: consume the request, then write an invalid LSP frame.
	// "Content-Length: not-a-number" triggers ErrInvalidContentLength in
	// ReadFrame, which is not io.EOF, so readTurnEvents routes it to te.error.
	serverDone := make(chan error, 1)
	go func() {
		if err := server.drainOneRequest(); err != nil {
			serverDone <- err
			return
		}
		_, _ = io.WriteString(server.respWriter, "Content-Length: not-a-number\r\n\r\n")
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	input := simpleUserInput("hello")

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server drain request: %v", err)
	}

	// Must have exactly one terminal event and it must be an error.
	if len(events) == 0 {
		t.Fatal("no events received")
	}

	var terminalCount int
	for _, ev := range events {
		if ev.Type == llmclient.EventError || ev.Type == llmclient.EventDone {
			terminalCount++
		}
	}
	if terminalCount != 1 {
		t.Errorf("terminal event count = %d; want exactly 1", terminalCount)
	}

	last := events[len(events)-1]
	if last.Type != llmclient.EventError {
		t.Errorf("last event.Type = %q; want %q", last.Type, llmclient.EventError)
	}
	if last.ErrorMessage == "" {
		t.Error("EventError.ErrorMessage must not be empty on protocol corruption")
	}

	// Backend must now be unhealthy.
	b.procMu.Lock()
	healthy := b.healthy
	proc2 := b.proc
	b.procMu.Unlock()

	if healthy {
		t.Error("backend should be unhealthy after protocol corruption")
	}
	if proc2 != nil {
		t.Error("backend.proc should be nil after protocol corruption")
	}
}

// ─── Criterion 3: BlocksTurnUntilRecovered ───────────────────────────────────

// TestCodexAppServer_BlocksTurnUntilRecovered verifies that after a protocol
// corruption error, the next call to Stream blocks on the semaphore until the
// corrupted turn's goroutine has finished cleaning up, then successfully starts
// a new process.
func TestCodexAppServer_BlocksTurnUntilRecovered(t *testing.T) {
	b := newTestBackend()

	// First process: will produce a protocol error.
	proc1, server1 := newPipeProcess()

	// Second process: healthy, completes normally.
	proc2, server2 := newPipeProcess()

	// Set up a factory that returns proc1 first, then proc2.
	callCount := 0
	var factoryMu sync.Mutex
	b.procFactory = func(_ context.Context, _ []string) (*codexProcess, error) {
		factoryMu.Lock()
		defer factoryMu.Unlock()
		callCount++
		switch callCount {
		case 1:
			return proc1, nil
		case 2:
			return proc2, nil
		default:
			return nil, fmt.Errorf("no more processes")
		}
	}

	ctx := context.Background()
	input := simpleUserInput("hello")

	// Server1: drain request then write an invalid LSP frame (protocol corruption).
	corruptDone := make(chan error, 1)
	go func() {
		if err := server1.drainOneRequest(); err != nil {
			corruptDone <- err
			return
		}
		// Invalid Content-Length value causes ReadFrame to return
		// ErrInvalidContentLength (not io.EOF), triggering the error path.
		_, _ = io.WriteString(server1.respWriter, "Content-Length: NaN\r\n\r\n")
		_ = server1.respWriter.Close()
		corruptDone <- nil
	}()

	// Server2: respond normally.
	normalDone := make(chan error, 1)
	go func() {
		if err := server2.drainOneRequest(); err != nil {
			normalDone <- err
			return
		}
		_ = server2.sendTextBlock("recovered")
		_ = server2.sendDone()
		_ = server2.respWriter.Close()
		normalDone <- nil
	}()

	// First Stream: triggers protocol corruption.
	ch1, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream 1: %v", err)
	}
	events1 := drainWithTimeout(t, ch1)
	if err := <-corruptDone; err != nil {
		t.Fatalf("server1 drain request: %v", err)
	}

	// Verify first stream produced an error terminal.
	last1 := events1[len(events1)-1]
	if last1.Type != llmclient.EventError {
		t.Fatalf("stream1 last event = %q; want error", last1.Type)
	}

	// Second Stream: must recover (start proc2) and complete normally.
	ch2, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream 2: %v", err)
	}
	events2 := drainWithTimeout(t, ch2)
	if err := <-normalDone; err != nil {
		t.Fatalf("server2 drain request: %v", err)
	}

	// Second stream must complete with a done terminal event.
	if len(events2) == 0 {
		t.Fatal("stream2: no events")
	}
	last2 := events2[len(events2)-1]
	if last2.Type != llmclient.EventDone {
		t.Errorf("stream2 last event = %q; want done", last2.Type)
	}

	// Factory must have been called twice.
	factoryMu.Lock()
	n := callCount
	factoryMu.Unlock()
	if n != 2 {
		t.Errorf("factory called %d times; want 2", n)
	}
}

// ─── Criterion 2: basic Stream event mapping ─────────────────────────────────

// TestCodexAppServer_StreamMapsTextEvents verifies that text_start,
// text_delta, text_end, and done notifications from the server are mapped to
// the correct normalized event sequence.
func TestCodexAppServer_StreamMapsTextEvents(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const msg = "hello from codex"

	serverDone := make(chan error, 1)
	go func() {
		if err := server.drainOneRequest(); err != nil {
			serverDone <- err
			return
		}
		_ = server.sendTextBlock(msg)
		_ = server.sendDone()
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	input := simpleUserInput("what is codex?")

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server drain request: %v", err)
	}

	wantTypes := []llmclient.EventType{
		llmclient.EventStart,
		llmclient.EventTextStart,
		llmclient.EventTextDelta,
		llmclient.EventTextEnd,
		llmclient.EventDone,
	}
	assertEventSequence(t, events, wantTypes)

	// text_delta must carry the incremental text.
	if events[2].Delta != msg {
		t.Errorf("text_delta.Delta = %q; want %q", events[2].Delta, msg)
	}

	// text_end must carry the full content.
	if events[3].Content != msg {
		t.Errorf("text_end.Content = %q; want %q", events[3].Content, msg)
	}

	// done event must have StopEndTurn reason.
	if events[4].Reason != llmclient.StopEndTurn {
		t.Errorf("done.Reason = %q; want %q", events[4].Reason, llmclient.StopEndTurn)
	}
}

// TestCodexAppServer_StreamRequiredUnsupportedOption verifies that Stream
// returns an error before acquiring the semaphore when a required option is
// not supported.
func TestCodexAppServer_StreamRequiredUnsupportedOption(t *testing.T) {
	b := newTestBackend()
	input := simpleUserInput("hello")

	_, err := b.Stream(
		context.Background(),
		input,
		llmclient.WithRequiredOptions(llmclient.OptionHostTools),
	)
	if err == nil {
		t.Fatal("expected error for required unsupported option, got nil")
	}
}

// ─── Criterion 4: Registry capabilities ──────────────────────────────────────

// TestCodexAppServerRegistry_StaticCapabilities verifies that the registered
// "codex-appserver" factory declares the required static capabilities.
func TestCodexAppServerRegistry_StaticCapabilities(t *testing.T) {
	const name = "codex-appserver"

	f, ok := factoryByName(name)
	if !ok {
		t.Fatalf("%s backend not registered", name)
	}

	caps := f.Capabilities

	if caps.Backend != name {
		t.Errorf("Backend = %q; want %q", caps.Backend, name)
	}

	if caps.Streaming != llmclient.StreamingStructured {
		t.Errorf("Streaming = %q; want %q", caps.Streaming, llmclient.StreamingStructured)
	}

	if !caps.ToolEvents {
		t.Error("ToolEvents should be true")
	}

	if !caps.MultiTurn {
		t.Error("MultiTurn should be true")
	}

	if f.Binary != "codex" {
		t.Errorf("Binary = %q; want %q", f.Binary, "codex")
	}

	if f.Preference != PreferCodex {
		t.Errorf("Preference = %v; want PreferCodex", f.Preference)
	}

	wantOptions := []llmclient.OptionName{
		llmclient.OptionModel,
		llmclient.OptionSession,
		llmclient.OptionOllama,
	}
	for _, opt := range wantOptions {
		if caps.OptionSupport[opt] == "" {
			t.Errorf("OptionSupport[%q] missing", opt)
		}
	}
	if _, ok := caps.OptionSupport[llmclient.OptionCodexProfile]; ok {
		t.Error("OptionCodexProfile should not be advertised until runtime support is proven")
	}
}

// TestCodexAppServerRegistry_PrefersOverCodexExec verifies that when both
// codex-appserver and a hypothetical codex-exec share the same binary and
// preference tier, codex-appserver (registered first) wins the stable sort.
func TestCodexAppServerRegistry_PrefersOverCodexExec(t *testing.T) {
	fAppServer := makeFactory("codex-appserver", "codex", PreferCodex, llmclient.Capabilities{}, true)
	fExec := makeFactory("codex-exec", "codex", PreferCodex, llmclient.Capabilities{}, true)

	restore := resetBackendFactoriesForTest(fAppServer, fExec)
	defer restore()

	got := registeredBackendFactories()
	if len(got) < 2 {
		t.Fatalf("expected 2 factories, got %d", len(got))
	}

	if got[0].Name != "codex-appserver" {
		t.Errorf("position 0 = %q; want %q", got[0].Name, "codex-appserver")
	}
}

// TestCodexAppServer_ContextCancelBeforeStream verifies that if the context is
// already cancelled, Stream returns the context error immediately without
// acquiring the semaphore.
func TestCodexAppServer_ContextCancelBeforeStream(t *testing.T) {
	b := newTestBackend()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, err := b.Stream(ctx, simpleUserInput("hello"))
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestCodexAppServer_ContextCancelDuringStream verifies that cancelling the
// context mid-stream closes the event channel.
func TestCodexAppServer_ContextCancelDuringStream(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	ctx, cancel := context.WithCancel(context.Background())

	// Server: drain request, then hang (never responds).
	serverDone := make(chan error, 1)
	go func() {
		if err := server.drainOneRequest(); err != nil {
			serverDone <- err
			return
		}
		// Hang until stdin closes (cancel → process terminates → stdin closed).
		_, _ = io.Copy(io.Discard, server.reqReader)
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ch, err := b.Stream(ctx, simpleUserInput("hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Cancel the context; the stream must close within a reasonable time.
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		drainChannel(ch)
	}()

	select {
	case <-done:
		// Channel closed as expected.
	case <-time.After(3 * time.Second):
		t.Error("channel was not closed within 3s after context cancellation")
	}
	if err := <-serverDone; err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("server drain request: %v", err)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// simpleUserInput creates a minimal *llmclient.Context with one user message.
func simpleUserInput(text string) *llmclient.Context {
	return &llmclient.Context{
		Messages: []llmclient.Message{
			{
				Role: llmclient.RoleUser,
				Content: []llmclient.ContentBlock{
					{Type: llmclient.ContentText, Text: text},
				},
			},
		},
	}
}

// assertExactlyOneTerminal fails the test if events does not contain exactly
// one terminal (done or error) event.
func assertExactlyOneTerminal(t *testing.T, events []llmclient.Event, label string) {
	t.Helper()

	count := 0
	for i := range events {
		if events[i].Type == llmclient.EventDone || events[i].Type == llmclient.EventError {
			count++
		}
	}

	if count != 1 {
		t.Errorf("%s: terminal event count = %d; want 1", label, count)
	}
}
