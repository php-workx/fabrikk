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
		sup:          nil, // test mode: no real subprocess
		stdin:        clientInW,
		stdout:       bufio.NewReader(serverOutR),
		stdoutCloser: serverOutR,
	}

	server := &fakeServer{
		reqReader:  bufio.NewReader(clientInR),
		respWriter: serverOutW,
	}

	return proc, server
}

// sendNDJSON writes body followed by a single newline to the server's response
// writer. This is the NDJSON framing used by the real codex app-server.
func (s *fakeServer) sendNDJSON(body string) error {
	_, err := io.WriteString(s.respWriter, body+"\n")
	return err
}

// readRequest reads one NDJSON line from the request reader and returns the
// raw bytes. Used to read what the backend sent.
func (s *fakeServer) readRequest() ([]byte, error) {
	return internal.ReadBoundedLine(s.reqReader, maxCodexBodyBytes)
}

// handleHandshake handles the initialize → initialized → thread/start sequence
// that the backend sends on a fresh process. It reads three outbound requests
// and sends back the expected responses including a thread/started notification.
// threadID is the mock thread id to echo back.
func (s *fakeServer) handleHandshake(threadID string) error {
	// 1. Read initialize request.
	_, err := s.readRequest()
	if err != nil {
		return fmt.Errorf("read initialize: %w", err)
	}

	// 2. Send initialize response (id=1).
	initResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"userAgent":      "codex-test",
			"codexHome":      "/tmp/.codex",
			"platformFamily": "unix",
			"platformOs":     "macos",
		},
	}
	b, err := json.Marshal(initResp)
	if err != nil {
		return err
	}
	if err := s.sendNDJSON(string(b)); err != nil {
		return fmt.Errorf("send initialize response: %w", err)
	}

	// 3. Read initialized notification.
	_, err = s.readRequest()
	if err != nil {
		return fmt.Errorf("read initialized: %w", err)
	}

	// 4. Read thread/start request.
	_, err = s.readRequest()
	if err != nil {
		return fmt.Errorf("read thread/start: %w", err)
	}

	// 5. Send thread/started notification.
	threadStarted := map[string]any{
		"jsonrpc": "2.0",
		"method":  "thread/started",
		"params": map[string]any{
			"thread": map[string]any{
				"id":        threadID,
				"sessionId": threadID,
				"status":    "active",
			},
		},
	}
	b, err = json.Marshal(threadStarted)
	if err != nil {
		return err
	}
	return s.sendNDJSON(string(b))
}

// sendDone sends the terminal "turn/completed" notification.
func (s *fakeServer) sendDone() error {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "turn/completed",
		"params": map[string]any{
			"threadId": "mock-thread",
			"turn": map[string]any{
				"id":     "turn-1",
				"status": "completed",
			},
		},
	})
	if err != nil {
		return err
	}
	return s.sendNDJSON(string(b))
}

// sendTextBlock sends item/agentMessage/delta (one delta) and item/completed
// notifications for a single text block, simulating a complete message.
func (s *fakeServer) sendTextBlock(text string) error {
	const itemID = "item-0"

	// item/agentMessage/delta
	delta := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/agentMessage/delta",
		"params": map[string]any{
			"threadId": "mock-thread",
			"turnId":   "turn-1",
			"itemId":   itemID,
			"delta":    text,
		},
	}
	b, err := json.Marshal(delta)
	if err != nil {
		return err
	}
	if err := s.sendNDJSON(string(b)); err != nil {
		return err
	}

	// item/completed
	completed := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/completed",
		"params": map[string]any{
			"threadId": "mock-thread",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type": "agentMessage",
				"id":   itemID,
				"text": text,
			},
		},
	}
	b, err = json.Marshal(completed)
	if err != nil {
		return err
	}
	return s.sendNDJSON(string(b))
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

// ─── New acceptance tests ─────────────────────────────────────────────────────

// TestCodexAppServer_NDJSONFraming verifies that the backend communicates over
// plain NDJSON (newline-delimited JSON) with no Content-Length framing.
// A mock that emits NDJSON produces correct events on the stream channel.
func TestCodexAppServer_NDJSONFraming(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const (
		mockThreadID = "thread-ndjson-test"
		wantText     = "hello from codex NDJSON"
	)

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake(mockThreadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		// Read turn/start.
		if _, err := server.readRequest(); err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}
		if err := server.sendTextBlock(wantText); err != nil {
			serverDone <- fmt.Errorf("sendTextBlock: %w", err)
			return
		}
		if err := server.sendDone(); err != nil {
			serverDone <- fmt.Errorf("sendDone: %w", err)
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("hello"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	// Must contain EventStart, EventTextStart, EventTextDelta, EventTextEnd, EventDone.
	assertContainsEventType(t, events, llmclient.EventStart)
	assertContainsEventType(t, events, llmclient.EventTextDelta)
	assertContainsEventType(t, events, llmclient.EventDone)

	// Verify terminal is EventDone.
	last := events[len(events)-1]
	if last.Type != llmclient.EventDone {
		t.Errorf("last event = %v; want EventDone", last.Type)
	}
}

// TestCodexAppServer_Handshake verifies that on a fresh process the backend
// sends initialize → initialized → thread/start in that order, and that the
// thread id from the thread/started notification is echoed in the turn/start
// request's threadId field.
//
//nolint:gocognit // sequential protocol handshake test; each step is necessary
func TestCodexAppServer_Handshake(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const mockThreadID = "thread-handshake-abc"

	type rpcMsg struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id,omitempty"`
		Params json.RawMessage `json:"params,omitempty"`
	}

	capturedMethods := make(chan string, 10)
	capturedTurnStart := make(chan rpcMsg, 1)

	serverDone := make(chan error, 1)
	go func() {
		// 1. Read initialize — must be method "initialize" with id=1.
		raw, err := server.readRequest()
		if err != nil {
			serverDone <- fmt.Errorf("read initialize: %w", err)
			return
		}
		var msg rpcMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			serverDone <- fmt.Errorf("unmarshal initialize: %w", err)
			return
		}
		capturedMethods <- msg.Method

		// Send initialize response.
		resp := `{"jsonrpc":"2.0","id":1,"result":{"userAgent":"test"}}`
		if err := server.sendNDJSON(resp); err != nil {
			serverDone <- err
			return
		}

		// 2. Read initialized notification.
		raw, err = server.readRequest()
		if err != nil {
			serverDone <- fmt.Errorf("read initialized: %w", err)
			return
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			serverDone <- err
			return
		}
		capturedMethods <- msg.Method

		// 3. Read thread/start.
		raw, err = server.readRequest()
		if err != nil {
			serverDone <- fmt.Errorf("read thread/start: %w", err)
			return
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			serverDone <- err
			return
		}
		capturedMethods <- msg.Method

		// Send thread/started notification with the mock thread id.
		notif := fmt.Sprintf(`{"jsonrpc":"2.0","method":"thread/started","params":{"thread":{"id":%q,"sessionId":%q,"status":"active"}}}`,
			mockThreadID, mockThreadID)
		if err := server.sendNDJSON(notif); err != nil {
			serverDone <- err
			return
		}

		// 4. Read turn/start — capture it for assertion.
		raw, err = server.readRequest()
		if err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			serverDone <- err
			return
		}
		capturedMethods <- msg.Method
		capturedTurnStart <- msg

		// Send a minimal text block and done.
		if err := server.sendTextBlock("ok"); err != nil {
			serverDone <- err
			return
		}
		if err := server.sendDone(); err != nil {
			serverDone <- err
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ch, err := b.Stream(context.Background(), simpleUserInput("test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	// Collect captured methods (drain channel).
	close(capturedMethods)
	var methods []string
	for m := range capturedMethods {
		methods = append(methods, m)
	}

	// Verify ordering: initialize → initialized → thread/start → turn/start.
	wantOrder := []string{"initialize", "initialized", "thread/start", "turn/start"}
	if len(methods) != len(wantOrder) {
		t.Fatalf("captured methods = %v; want %v", methods, wantOrder)
	}
	for i, want := range wantOrder {
		if methods[i] != want {
			t.Errorf("methods[%d] = %q; want %q", i, methods[i], want)
		}
	}

	// Verify that turn/start params.threadId matches the mock thread id.
	ts := <-capturedTurnStart
	var tsParams struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(ts.Params, &tsParams); err != nil {
		t.Fatalf("unmarshal turn/start params: %v", err)
	}
	if tsParams.ThreadID != mockThreadID {
		t.Errorf("turn/start.threadId = %q; want %q", tsParams.ThreadID, mockThreadID)
	}
}

// TestCodexAppServer_AgentMessageDelta verifies that item/agentMessage/delta
// notifications are mapped to EventTextStart + EventTextDelta events with a
// per-item contentIndex, and that item/completed emits EventTextEnd.
//
//nolint:gocognit,cyclop // multi-item delta sequencing test; verification logic is necessarily detailed
func TestCodexAppServer_AgentMessageDelta(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const (
		mockThreadID = "thread-delta-test"
		itemID1      = "item-1"
		itemID2      = "item-2"
		delta1a      = "hello "
		delta1b      = "world"
		fullText1    = "hello world"
		delta2       = "second item"
	)

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake(mockThreadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		// Read turn/start.
		if _, err := server.readRequest(); err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}

		// Emit two deltas for item 1.
		if err := server.sendNDJSON(mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/agentMessage/delta",
			"params":  map[string]any{"threadId": mockThreadID, "turnId": "t1", "itemId": itemID1, "delta": delta1a},
		})); err != nil {
			serverDone <- err
			return
		}
		if err := server.sendNDJSON(mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/agentMessage/delta",
			"params":  map[string]any{"threadId": mockThreadID, "turnId": "t1", "itemId": itemID1, "delta": delta1b},
		})); err != nil {
			serverDone <- err
			return
		}
		// Complete item 1.
		if err := server.sendNDJSON(mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/completed",
			"params": map[string]any{
				"threadId": mockThreadID, "turnId": "t1",
				"item": map[string]any{"type": "agentMessage", "id": itemID1, "text": fullText1},
			},
		})); err != nil {
			serverDone <- err
			return
		}

		// Emit one delta for item 2.
		if err := server.sendNDJSON(mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/agentMessage/delta",
			"params":  map[string]any{"threadId": mockThreadID, "turnId": "t1", "itemId": itemID2, "delta": delta2},
		})); err != nil {
			serverDone <- err
			return
		}
		// Complete item 2.
		if err := server.sendNDJSON(mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/completed",
			"params": map[string]any{
				"threadId": mockThreadID, "turnId": "t1",
				"item": map[string]any{"type": "agentMessage", "id": itemID2, "text": delta2},
			},
		})); err != nil {
			serverDone <- err
			return
		}

		if err := server.sendDone(); err != nil {
			serverDone <- err
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("multi-item"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	// Collect text-related events.
	var textStarts []llmclient.Event
	var textDeltas []llmclient.Event
	var textEnds []llmclient.Event
	for _, ev := range events {
		switch ev.Type {
		case llmclient.EventTextStart:
			textStarts = append(textStarts, ev)
		case llmclient.EventTextDelta:
			textDeltas = append(textDeltas, ev)
		case llmclient.EventTextEnd:
			textEnds = append(textEnds, ev)
		}
	}

	// Two items → two TextStart events.
	if len(textStarts) != 2 {
		t.Errorf("TextStart count = %d; want 2", len(textStarts))
	}

	// Item 1 has 2 deltas, item 2 has 1 delta → 3 total.
	if len(textDeltas) != 3 {
		t.Errorf("TextDelta count = %d; want 3", len(textDeltas))
	}

	// Two items → two TextEnd events.
	if len(textEnds) != 2 {
		t.Errorf("TextEnd count = %d; want 2", len(textEnds))
	}

	// Verify the two items have different content indices (0 and 1).
	if len(textStarts) == 2 {
		idx0 := textStarts[0].ContentIndex
		idx1 := textStarts[1].ContentIndex
		if idx0 == idx1 {
			t.Errorf("both items have the same ContentIndex %d; want distinct indices", idx0)
		}
	}

	// Verify item 1 deltas.
	if len(textDeltas) >= 2 {
		if textDeltas[0].Delta != delta1a {
			t.Errorf("delta[0] = %q; want %q", textDeltas[0].Delta, delta1a)
		}
		if textDeltas[1].Delta != delta1b {
			t.Errorf("delta[1] = %q; want %q", textDeltas[1].Delta, delta1b)
		}
	}

	// Verify TextEnd for item 1 carries full text.
	if len(textEnds) >= 1 {
		if textEnds[0].Content != fullText1 {
			t.Errorf("textEnd[0].Content = %q; want %q", textEnds[0].Content, fullText1)
		}
	}
}

// mustMarshal marshals v to a JSON string or panics. Used only in tests.
func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustMarshal: %v", err))
	}
	return string(b)
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

	// Keep a count of turn/start requests that the server received.
	var (
		mu       sync.Mutex
		received []int
	)

	// Server goroutine: handle handshake once, then service two sequential
	// turn/start requests.
	serverDone := make(chan error, 1)
	go func() {
		const threadID = "thread-serialize"
		if err := server.handleHandshake(threadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}

		for i := 1; i <= 2; i++ {
			if _, err := server.readRequest(); err != nil { // turn/start
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

	// Server must have received exactly two turn/start requests.
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Errorf("server received %d requests; want 2", len(received))
	}
}

// TestCodexAppServer_HandshakeFailureReleasesSemaphore verifies that when the
// per-process handshake fails (e.g. unexpected EOF before initialize response),
// the turn semaphore is released so subsequent Stream calls can still proceed.
func TestCodexAppServer_HandshakeFailureReleasesSemaphore(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	// Server drains the initialize request (so the backend write doesn't block)
	// then closes the response pipe, giving the backend an unexpected EOF.
	go func() {
		_, _ = server.readRequest() // consume initialize
		_ = server.respWriter.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := b.Stream(ctx, simpleUserInput("hi"))
	if err == nil {
		t.Fatal("Stream should have returned an error after handshake failure")
	}

	// Semaphore must have been released: a subsequent procFactory call must not
	// hang. Inject a second process that handles the full flow.
	proc2, server2 := newPipeProcess()
	injectProc(b, proc2)

	serverDone := make(chan error, 1)
	go func() {
		if err := server2.handleHandshake("thread-recovery"); err != nil {
			serverDone <- err
			return
		}
		if _, err := server2.readRequest(); err != nil {
			serverDone <- err
			return
		}
		if err := server2.sendDone(); err != nil {
			serverDone <- err
			return
		}
		_ = server2.respWriter.Close()
		serverDone <- nil
	}()

	ch, err := b.Stream(ctx, simpleUserInput("retry"))
	if err != nil {
		t.Fatalf("Stream after handshake recovery: %v", err)
	}
	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
	assertExactlyOneTerminal(t, events, "recovery stream")
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
		const threadID = "thread-close-test"
		if err := server.handleHandshake(threadID); err != nil {
			serverClosed <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		// Drain the turn/start request.
		if _, err := server.readRequest(); err != nil {
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
// process sends a malformed NDJSON frame (truncated JSON), exactly one
// EventError is emitted on the stream channel and the backend marks itself
// unhealthy.
func TestCodexAppServer_ProtocolCorruptionEmitsOneError(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	// Server: complete handshake, consume turn/start, then write malformed JSON.
	serverDone := make(chan error, 1)
	go func() {
		const threadID = "thread-corrupt"
		if err := server.handleHandshake(threadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server.readRequest(); err != nil { // turn/start
			serverDone <- err
			return
		}
		// Write malformed JSON line — this is not valid JSON.
		_, _ = io.WriteString(server.respWriter, "{this is not valid json\n")
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

	// Server1: handle handshake then write malformed JSON after turn/start.
	corruptDone := make(chan error, 1)
	go func() {
		if err := server1.handleHandshake("thread-corrupt-1"); err != nil {
			corruptDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server1.readRequest(); err != nil { // turn/start
			corruptDone <- err
			return
		}
		// Write malformed NDJSON to trigger protocol error.
		_, _ = io.WriteString(server1.respWriter, "{bad json\n")
		_ = server1.respWriter.Close()
		corruptDone <- nil
	}()

	// Server2: respond normally.
	normalDone := make(chan error, 1)
	go func() {
		if err := server2.handleHandshake("thread-normal-2"); err != nil {
			normalDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server2.readRequest(); err != nil { // turn/start
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

// TestCodexAppServer_EOFWithoutDone_ResetsProcess verifies that when the server
// closes stdout without sending a "turn/completed" frame, the backend clears its
// process reference so the next turn spawns a fresh process (not the dead one).
func TestCodexAppServer_EOFWithoutDone_ResetsProcess(t *testing.T) {
	b := newTestBackend()

	proc1, server1 := newPipeProcess()
	proc2, server2 := newPipeProcess()

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

	// Turn 1: server handles handshake and turn/start then closes stdout without done.
	eofDone := make(chan error, 1)
	go func() {
		if err := server1.handleHandshake("thread-eof-1"); err != nil {
			eofDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server1.readRequest(); err != nil { // turn/start
			eofDone <- err
			return
		}
		_ = server1.respWriter.Close() // EOF without done
		eofDone <- nil
	}()

	ch1, err := b.Stream(ctx, simpleUserInput("turn1"))
	if err != nil {
		t.Fatalf("Stream turn1: %v", err)
	}
	drainChannel(ch1)
	if err := <-eofDone; err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("server1: %v", err)
	}

	// Turn 2: server responds normally.
	normalDone := make(chan error, 1)
	go func() {
		if err := server2.handleHandshake("thread-eof-2"); err != nil {
			normalDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server2.readRequest(); err != nil { // turn/start
			normalDone <- err
			return
		}
		_ = server2.sendTextBlock("ok")
		_ = server2.sendDone()
		_ = server2.respWriter.Close()
		normalDone <- nil
	}()

	ch2, err := b.Stream(ctx, simpleUserInput("turn2"))
	if err != nil {
		t.Fatalf("Stream turn2: %v", err)
	}
	events := drainWithTimeout(t, ch2)
	if err := <-normalDone; err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("server2: %v", err)
	}

	// Verify second turn succeeded (has text events).
	assertContainsEventType(t, events, llmclient.EventTextDelta)

	// Factory must have been called twice — once for proc1, once for proc2.
	factoryMu.Lock()
	count := callCount
	factoryMu.Unlock()
	if count != 2 {
		t.Errorf("procFactory called %d times, want 2 (EOFed proc should be discarded)", count)
	}
}

// TestCodexAppServer_StreamMapsTextEvents verifies that item/agentMessage/delta,
// item/completed, and turn/completed notifications from the server are mapped to
// the correct normalized event sequence.
func TestCodexAppServer_StreamMapsTextEvents(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const msg = "hello from codex"

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake("thread-text-test"); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server.readRequest(); err != nil { // turn/start
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

	if !caps.Thinking {
		t.Error("Thinking should be true")
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
// ─── fab-o2la: Ollama routing ─────────────────────────────────────────────────

// TestCodexAppServer_Ollama_InjectsEnv verifies that when WithOllama is passed,
// the env slice forwarded to procFactory contains OPENAI_BASE_URL.
func TestCodexAppServer_Ollama_InjectsEnv(t *testing.T) {
	b := newTestBackend()

	factoryCalled := make(chan []string, 1)
	b.procFactory = func(_ context.Context, env []string) (*codexProcess, error) {
		factoryCalled <- env
		return nil, fmt.Errorf("test: env captured, aborting")
	}

	ollamaCfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434", Model: "llama3"}
	// procFactory returns an error, so Stream itself returns an error (ensureProcess
	// runs synchronously before the stream goroutine starts).
	ch, _ := b.Stream(context.Background(), simpleUserInput("hi"), llmclient.WithOllama(ollamaCfg))
	if ch != nil {
		drainChannel(ch)
	}

	var capturedEnv []string
	select {
	case capturedEnv = <-factoryCalled:
	default:
		t.Fatal("procFactory was not called")
	}

	found := false
	for _, kv := range capturedEnv {
		if strings.HasPrefix(kv, "OPENAI_BASE_URL=") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("OPENAI_BASE_URL not found in env passed to procFactory; got %v", capturedEnv)
	}
}

// TestCodexAppServerRegistry_StaticCapabilities_Ollama verifies that the
// registered capabilities declare OllamaRouting=true.
func TestCodexAppServerRegistry_StaticCapabilities_Ollama(t *testing.T) {
	f, ok := factoryByName("codex-appserver")
	if !ok {
		t.Fatal("codex-appserver not registered")
	}
	if !f.Capabilities.OllamaRouting {
		t.Error("OllamaRouting should be true for codex-appserver")
	}
}

// TestCheckCodexAppServerRequiredOptions_Ollama verifies that requesting
// OptionOllama as a required option is accepted (backend supports it fully).
func TestCheckCodexAppServerRequiredOptions_Ollama(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithOllama(llmclient.OllamaConfig{BaseURL: "http://localhost:11434"}),
		llmclient.WithRequiredOptions(llmclient.OptionOllama),
	})
	if err := checkCodexAppServerRequiredOptions(cfg); err != nil {
		t.Errorf("checkCodexAppServerRequiredOptions rejected OptionOllama: %v", err)
	}
}

// ─── fab-cpcx: CodexProfile unsupported ──────────────────────────────────────

// TestCodexAppServer_CodexProfile_Unsupported verifies that when CodexProfile
// is set, the fidelity OptionResults marks it as OptionUnsupported.
func TestCodexAppServer_CodexProfile_Unsupported(t *testing.T) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), []llmclient.Option{
		llmclient.WithCodexProfile("my-profile"),
	})
	f := codexAppServerFidelity(cfg)
	got := f.OptionResults[llmclient.OptionCodexProfile]
	if got != llmclient.OptionUnsupported {
		t.Errorf("OptionResults[OptionCodexProfile] = %v, want OptionUnsupported", got)
	}
}

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

	// Server: handle handshake, drain turn/start request, then hang (never responds).
	// Pipe write errors after context cancellation are expected and tolerated.
	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake("thread-cancel-test"); err != nil {
			// Pipe errors are expected when the backend terminates the process on
			// context cancellation — treat them as clean shutdown.
			_ = server.respWriter.Close()
			serverDone <- nil
			return
		}
		if _, err := server.readRequest(); err != nil { // turn/start
			// Tolerate pipe errors from cancellation.
			_ = server.respWriter.Close()
			serverDone <- nil
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

// ─── fab-citr: tool-call and reasoning event mapping ─────────────────────────

// TestCodexAppServer_ToolCallMapping verifies that item/started with a
// dynamicToolCall item emits EventToolCallStart and the matching item/completed
// emits EventToolCallEnd with the parsed tool name and ID.
func TestCodexAppServer_ToolCallMapping(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const (
		mockThreadID = "thread-toolcall-test"
		callID       = "call1"
		toolName     = "echo"
	)

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake(mockThreadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server.readRequest(); err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}

		// item/started — dynamicToolCall
		started := mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/started",
			"params": map[string]any{
				"threadId": mockThreadID,
				"turnId":   "turn-1",
				"item": map[string]any{
					"type":      "dynamicToolCall",
					"id":        callID,
					"tool":      toolName,
					"namespace": nil,
					"arguments": map[string]any{"msg": "hi"},
					"status":    "inProgress",
				},
			},
		})
		if err := server.sendNDJSON(started); err != nil {
			serverDone <- fmt.Errorf("send item/started: %w", err)
			return
		}

		// item/completed — dynamicToolCall (status completed)
		completed := mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/completed",
			"params": map[string]any{
				"threadId": mockThreadID,
				"turnId":   "turn-1",
				"item": map[string]any{
					"type":      "dynamicToolCall",
					"id":        callID,
					"tool":      toolName,
					"arguments": map[string]any{"msg": "hi"},
					"status":    "completed",
				},
			},
		})
		if err := server.sendNDJSON(completed); err != nil {
			serverDone <- fmt.Errorf("send item/completed: %w", err)
			return
		}

		if err := server.sendDone(); err != nil {
			serverDone <- fmt.Errorf("sendDone: %w", err)
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("run echo"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	var toolStarts []llmclient.Event
	var toolEnds []llmclient.Event
	for _, ev := range events {
		switch ev.Type {
		case llmclient.EventToolCallStart:
			toolStarts = append(toolStarts, ev)
		case llmclient.EventToolCallEnd:
			toolEnds = append(toolEnds, ev)
		}
	}

	if len(toolStarts) != 1 {
		t.Fatalf("EventToolCallStart count = %d; want 1", len(toolStarts))
	}
	if len(toolEnds) != 1 {
		t.Fatalf("EventToolCallEnd count = %d; want 1", len(toolEnds))
	}

	start := toolStarts[0]
	if start.ToolCall == nil {
		t.Fatal("EventToolCallStart.ToolCall is nil")
	}
	if start.ToolCall.ID != callID {
		t.Errorf("ToolCallStart.ID = %q; want %q", start.ToolCall.ID, callID)
	}
	if start.ToolCall.Name != toolName {
		t.Errorf("ToolCallStart.Name = %q; want %q", start.ToolCall.Name, toolName)
	}

	end := toolEnds[0]
	if end.ToolCall == nil {
		t.Fatal("EventToolCallEnd.ToolCall is nil")
	}
	if end.ToolCall.ID != callID {
		t.Errorf("ToolCallEnd.ID = %q; want %q", end.ToolCall.ID, callID)
	}
}

// TestCodexAppServer_MCPToolCallMapping verifies that item/started with a
// mcpToolCall item emits EventToolCallStart with a name containing the tool
// field, and the matching item/completed emits EventToolCallEnd.
func TestCodexAppServer_MCPToolCallMapping(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const (
		mockThreadID = "thread-mcptool-test"
		callID       = "mcp1"
		serverName   = "fs"
		toolName     = "read_file"
	)

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake(mockThreadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server.readRequest(); err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}

		// item/started — mcpToolCall
		started := mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/started",
			"params": map[string]any{
				"threadId": mockThreadID,
				"turnId":   "turn-1",
				"item": map[string]any{
					"type":      "mcpToolCall",
					"id":        callID,
					"server":    serverName,
					"tool":      toolName,
					"arguments": map[string]any{"path": "/tmp/x"},
					"status":    "inProgress",
				},
			},
		})
		if err := server.sendNDJSON(started); err != nil {
			serverDone <- fmt.Errorf("send item/started: %w", err)
			return
		}

		// item/completed — mcpToolCall
		completed := mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/completed",
			"params": map[string]any{
				"threadId": mockThreadID,
				"turnId":   "turn-1",
				"item": map[string]any{
					"type":      "mcpToolCall",
					"id":        callID,
					"server":    serverName,
					"tool":      toolName,
					"arguments": map[string]any{"path": "/tmp/x"},
					"status":    "completed",
				},
			},
		})
		if err := server.sendNDJSON(completed); err != nil {
			serverDone <- fmt.Errorf("send item/completed: %w", err)
			return
		}

		if err := server.sendDone(); err != nil {
			serverDone <- fmt.Errorf("sendDone: %w", err)
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("read a file"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	var toolStarts []llmclient.Event
	var toolEnds []llmclient.Event
	for _, ev := range events {
		switch ev.Type {
		case llmclient.EventToolCallStart:
			toolStarts = append(toolStarts, ev)
		case llmclient.EventToolCallEnd:
			toolEnds = append(toolEnds, ev)
		}
	}

	if len(toolStarts) != 1 {
		t.Fatalf("EventToolCallStart count = %d; want 1", len(toolStarts))
	}
	if len(toolEnds) != 1 {
		t.Fatalf("EventToolCallEnd count = %d; want 1", len(toolEnds))
	}

	start := toolStarts[0]
	if start.ToolCall == nil {
		t.Fatal("EventToolCallStart.ToolCall is nil")
	}
	if !strings.Contains(start.ToolCall.Name, toolName) {
		t.Errorf("ToolCallStart.Name = %q; want it to contain %q", start.ToolCall.Name, toolName)
	}
	if start.ToolCall.ID != callID {
		t.Errorf("ToolCallStart.ID = %q; want %q", start.ToolCall.ID, callID)
	}
}

// TestCodexAppServer_ReasoningMapping verifies that item/reasoning/textDelta
// notifications are mapped to EventThinkingDelta events with the correct delta
// and contentIndex.
func TestCodexAppServer_ReasoningMapping(t *testing.T) {
	b := newTestBackend()

	proc, server := newPipeProcess()
	injectProc(b, proc)

	const (
		mockThreadID  = "thread-reasoning-test"
		reasoningItem = "reasoning1"
		thinkingText  = "let me think"
	)

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake(mockThreadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server.readRequest(); err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}

		// item/reasoning/textDelta
		delta := mustMarshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "item/reasoning/textDelta",
			"params": map[string]any{
				"threadId":     mockThreadID,
				"turnId":       "turn-1",
				"itemId":       reasoningItem,
				"delta":        thinkingText,
				"contentIndex": 0,
			},
		})
		if err := server.sendNDJSON(delta); err != nil {
			serverDone <- fmt.Errorf("send textDelta: %w", err)
			return
		}

		if err := server.sendDone(); err != nil {
			serverDone <- fmt.Errorf("sendDone: %w", err)
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("think about it"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	var thinkingDeltas []llmclient.Event
	for _, ev := range events {
		if ev.Type == llmclient.EventThinkingDelta {
			thinkingDeltas = append(thinkingDeltas, ev)
		}
	}

	if len(thinkingDeltas) != 1 {
		t.Fatalf("EventThinkingDelta count = %d; want 1", len(thinkingDeltas))
	}
	if thinkingDeltas[0].Delta != thinkingText {
		t.Errorf("ThinkingDelta.Delta = %q; want %q", thinkingDeltas[0].Delta, thinkingText)
	}
}

// TestCodexAppServer_ObserverFires verifies that, because Stream delegates to
// structuredStream, the DefaultObserver hooks fire correctly:
//   - OnStreamStart once
//   - OnEventEmitted at least twice (EventStart + EventDone minimum)
//   - OnStreamEnd once
func TestCodexAppServer_ObserverFires(t *testing.T) {
	spy := &spyObserver{}
	orig := DefaultObserver
	DefaultObserver = spy
	t.Cleanup(func() { DefaultObserver = orig })

	b := newTestBackend()
	proc, server := newPipeProcess()
	injectProc(b, proc)

	const mockThreadID = "thread-observer-test"

	serverDone := make(chan error, 1)
	go func() {
		if err := server.handleHandshake(mockThreadID); err != nil {
			serverDone <- fmt.Errorf("handleHandshake: %w", err)
			return
		}
		if _, err := server.readRequest(); err != nil {
			serverDone <- fmt.Errorf("read turn/start: %w", err)
			return
		}
		if err := server.sendTextBlock("observer test text"); err != nil {
			serverDone <- fmt.Errorf("sendTextBlock: %w", err)
			return
		}
		if err := server.sendDone(); err != nil {
			serverDone <- fmt.Errorf("sendDone: %w", err)
			return
		}
		_ = server.respWriter.Close()
		serverDone <- nil
	}()

	ctx := context.Background()
	ch, err := b.Stream(ctx, simpleUserInput("observe me"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := drainWithTimeout(t, ch)
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}

	spy.mu.Lock()
	starts := spy.starts
	ends := spy.ends
	eventTypes := spy.eventTypes
	spy.mu.Unlock()

	if starts != 1 {
		t.Errorf("OnStreamStart called %d time(s), want 1", starts)
	}
	if len(ends) != 1 {
		t.Errorf("OnStreamEnd called %d time(s), want 1", len(ends))
	}
	if len(eventTypes) < 2 {
		t.Errorf("OnEventEmitted called %d time(s), want >= 2 (EventStart + EventDone minimum)", len(eventTypes))
	}
	_ = events
}
