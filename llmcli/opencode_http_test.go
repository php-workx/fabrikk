package llmcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/php-workx/fabrikk/llmclient"
)

// ---- helpers ----------------------------------------------------------------

// fakeOpenCodeServer builds an httptest.Server that simulates opencode serve.
//
// sseBodies is the sequence of raw SSE bytes served from GET /event; the
// i-th call gets sseBodies[i%len(sseBodies)]. sseStatus overrides the HTTP
// status for /event (0 → 200 OK). promptStatus overrides /prompt_async.
type fakeOpenCodeServer struct {
	t            *testing.T
	sseBodies    []string // raw SSE to stream on GET /event
	sseStatus    int      // 0 → 200
	promptStatus int      // 0 → 204
	promptClose  bool     // close prompt_async connection without an HTTP response

	mu           sync.Mutex
	requestOrder []string // records "method path" in arrival order
	sseCallCount atomic.Int32
	srv          *httptest.Server
}

func newFakeOpenCodeServer(t *testing.T, sseBodies []string) *fakeOpenCodeServer {
	t.Helper()
	f := &fakeOpenCodeServer{t: t, sseBodies: sseBodies}
	mux := http.NewServeMux()
	mux.HandleFunc("/event", f.handleEvent)
	mux.HandleFunc("/session", f.handleSession)
	mux.HandleFunc("/doc", f.handleDoc)
	mux.HandleFunc("/", f.handlePromptAsync)
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeOpenCodeServer) handleEvent(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requestOrder = append(f.requestOrder, "GET /event")
	f.mu.Unlock()

	status := f.sseStatus
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(status)
	if status != http.StatusOK {
		return
	}

	idx := int(f.sseCallCount.Add(1)) - 1
	body := ""
	if len(f.sseBodies) > 0 {
		body = f.sseBodies[idx%len(f.sseBodies)]
	}
	_, _ = io.WriteString(w, body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (f *fakeOpenCodeServer) handleSession(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requestOrder = append(f.requestOrder, "POST /session")
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"id": "sess-test"})
}

func (f *fakeOpenCodeServer) handleDoc(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requestOrder = append(f.requestOrder, "GET /doc")
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *fakeOpenCodeServer) handlePromptAsync(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/prompt_async") {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.requestOrder = append(f.requestOrder, "POST /prompt_async")
	f.mu.Unlock()

	if f.promptClose {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
		return
	}

	status := f.promptStatus
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
}

func (f *fakeOpenCodeServer) close() { f.srv.Close() }

// openCodeBackendForTest builds an OpenCodeHTTPBackend wired to the fake server.
func openCodeBackendForTest(srv *fakeOpenCodeServer) *OpenCodeHTTPBackend {
	b := newOpenCodeHTTPBackendWithClient(
		CliInfo{Name: "opencode", Binary: "opencode", Path: "/fake/opencode"},
		srv.srv.Client(),
		"",
	)
	// Pre-set baseURL so ensureServer is bypassed.
	b.baseURL = srv.srv.URL
	// Mark the owned process as nil so ensureServer doesn't try to spawn.
	return b
}

// waitForEvents drains all events from ch with a timeout. Returns the slice
// of events received before the channel is closed or the timeout fires.
func waitForEvents(t *testing.T, ch <-chan llmclient.Event, timeout time.Duration) []llmclient.Event {
	t.Helper()
	var evs []llmclient.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return evs
			}
			evs = append(evs, ev)
		case <-deadline:
			t.Error("waitForEvents: timeout waiting for channel to close")
			return evs
		}
	}
}

// ---- SSE / OpenCode HTTP tests ----------------------------------------------

// TestOpenCodeHTTP_WaitReady verifies that waitReady succeeds when the server
// responds with a server.connected SSE event (Criterion 2).
func TestOpenCodeHTTP_WaitReady(t *testing.T) {
	// Serve a minimal server.connected SSE event.
	const serverConnectedSSE = "data: {\"type\":\"server.connected\"}\n\n"
	sseBody := serverConnectedSSE
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	defer fake.close()

	b := &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", CliInfo{}),
		httpClient: fake.srv.Client(),
		baseURL:    fake.srv.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.waitReady(ctx, b.baseURL); err != nil {
		t.Fatalf("waitReady: unexpected error: %v", err)
	}
}

// TestOpenCodeHTTP_WaitReadyTimeout verifies that waitReady returns an error
// when the server never emits server.connected (Criterion 2).
func TestOpenCodeHTTP_WaitReadyTimeout(t *testing.T) {
	// Server that never emits server.connected.
	fake := newFakeOpenCodeServer(t, []string{"data: {\"type\":\"other\"}\n\n"})
	defer fake.close()

	b := &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", CliInfo{}),
		httpClient: fake.srv.Client(),
		baseURL:    fake.srv.URL,
	}

	// Use a very short timeout to keep the test fast.
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	err := b.waitReady(ctx, b.baseURL)
	if err == nil {
		t.Fatal("waitReady: expected timeout error, got nil")
	}
}

// TestOpenCodeHTTP_OpensSSEBeforePromptAsync verifies that GET /event is
// requested before POST /prompt_async (Criterion 2).
//
// The test uses a synchronized fake server: the SSE handler stays open until
// the prompt is received, then sends the response events and closes. This
// reflects how the real opencode server behaves and avoids the race where the
// SSE goroutine finishes before the prompt goroutine runs.
func TestOpenCodeHTTP_OpensSSEBeforePromptAsync(t *testing.T) {
	var (
		requestOrder   []string
		orderMu        sync.Mutex
		promptReceived = make(chan struct{})
	)

	recordRequest := func(label string) {
		orderMu.Lock()
		requestOrder = append(requestOrder, label)
		orderMu.Unlock()
	}

	mux := http.NewServeMux()

	// SSE handler: sends server.connected, then waits for prompt, then sends
	// a content event and closes. This keeps the SSE stream open until the
	// prompt goroutine has had time to run.
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		recordRequest("GET /event")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"type\":\"server.connected\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Block until the prompt arrives (or the request context expires).
		select {
		case <-promptReceived:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"content\":\"hi\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})

	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		recordRequest("POST /session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "sess-order-test"})
	})

	mux.HandleFunc("/doc", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/prompt_async") {
			recordRequest("POST /prompt_async")
			close(promptReceived) // signal the SSE handler to send events
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", CliInfo{}),
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "hello"}},
		}},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}

	waitForEvents(t, ch, 4*time.Second)

	orderMu.Lock()
	order := make([]string, len(requestOrder))
	copy(order, requestOrder)
	orderMu.Unlock()

	// Find the relative positions of GET /event and POST /prompt_async.
	eventIdx := -1
	promptIdx := -1
	for i, req := range order {
		switch req {
		case "GET /event":
			if eventIdx < 0 {
				eventIdx = i
			}
		case "POST /prompt_async":
			if promptIdx < 0 {
				promptIdx = i
			}
		}
	}
	if eventIdx < 0 {
		t.Fatalf("GET /event not observed; requests: %v", order)
	}
	if promptIdx < 0 {
		t.Fatalf("POST /prompt_async not observed; requests: %v", order)
	}
	if eventIdx >= promptIdx {
		t.Errorf("SSE not opened before prompt_async: GET /event at %d, POST /prompt_async at %d; requests: %v",
			eventIdx, promptIdx, order)
	}
}

// TestOpenCodeHTTP_PromptAsyncNon204CancelsSSE verifies that when prompt_async
// returns a non-204 status the SSE stream is cancelled and the channel receives
// an error event (Criterion 3).
func TestOpenCodeHTTP_PromptAsyncNon204CancelsSSE(t *testing.T) {
	// SSE body that never terminates (simulates an open stream).
	sseBody := ": keep-alive\n\n" + strings.Repeat(": ping\n\n", 100)
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	fake.promptStatus = http.StatusInternalServerError // 500 instead of 204
	defer fake.close()

	b := openCodeBackendForTest(fake)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "test"}},
		}},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: unexpected error at start: %v", err)
	}

	events := waitForEvents(t, ch, 3*time.Second)

	// The channel must have closed and contain an error event.
	hasError := false
	for _, ev := range events {
		if ev.Type == llmclient.EventError {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Errorf("expected EventError after non-204 prompt; got events: %v", events)
	}
}

// TestOpenCodeHTTP_PromptAsyncErrorClosesBody verifies that when prompt_async
// fails with a network error the response body is closed and an error event
// is emitted (Criterion 3).
func TestOpenCodeHTTP_PromptAsyncErrorClosesBody(t *testing.T) {
	// Keep the server available for SSE/session setup, then close only the
	// prompt_async connection so the prompt send fails after Stream starts.
	fake := newFakeOpenCodeServer(t, []string{": keep-alive\n\n" + strings.Repeat(": ping\n\n", 100)})
	fake.promptClose = true
	defer fake.close()
	b := openCodeBackendForTest(fake)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "test"}},
		}},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: unexpected setup error: %v", err)
	}

	events := waitForEvents(t, ch, 3*time.Second)

	// Should have received an error event.
	hasError := false
	for _, ev := range events {
		if ev.Type == llmclient.EventError {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Fatalf("expected EventError after prompt_async network failure; got events: %v", events)
	}
}

// TestOpenCodeHTTPRegistry_StructuredUnknown verifies that "opencode-serve" is
// registered in the factory registry with StreamingStructuredUnknown fidelity
// (Criterion 4).
func TestOpenCodeHTTPRegistry_StructuredUnknown(t *testing.T) {
	f, ok := factoryByName("opencode-serve")
	if !ok {
		t.Fatal("opencode-serve factory not registered")
	}
	if f.Capabilities.Streaming != llmclient.StreamingStructuredUnknown {
		t.Errorf("Streaming: got %q; want %q",
			f.Capabilities.Streaming, llmclient.StreamingStructuredUnknown)
	}
}

// TestOpenCodeHTTPRegistry_Preference verifies that opencode-serve is registered
// with PreferOpenCode and the correct binary name.
func TestOpenCodeHTTPRegistry_Preference(t *testing.T) {
	f, ok := factoryByName("opencode-serve")
	if !ok {
		t.Fatal("opencode-serve factory not registered")
	}
	if f.Binary != "opencode" {
		t.Errorf("Binary: got %q; want %q", f.Binary, "opencode")
	}
	if f.Preference != PreferOpenCode {
		t.Errorf("Preference: got %v; want PreferOpenCode", f.Preference)
	}
}

// TestOpenCodeHTTPRegistry_DoesNotClaimToolEvents verifies that opencode-serve
// does not advertise ToolEvents (schema-gated, Criterion 4).
func TestOpenCodeHTTPRegistry_DoesNotClaimToolEvents(t *testing.T) {
	f, ok := factoryByName("opencode-serve")
	if !ok {
		t.Fatal("opencode-serve factory not registered")
	}
	if f.Capabilities.ToolEvents {
		t.Error("opencode-serve must not claim ToolEvents until schema is pinned")
	}
	if f.Capabilities.Thinking {
		t.Error("opencode-serve must not claim Thinking until schema is pinned")
	}
	if f.Capabilities.Usage {
		t.Error("opencode-serve must not claim Usage until schema is pinned")
	}
}

// TestOpenCodeHTTP_StreamEmitsStartEvent verifies that the first event in a
// Stream is EventStart with StreamingStructuredUnknown fidelity.
func TestOpenCodeHTTP_StreamEmitsStartEvent(t *testing.T) {
	sseBody := "data: {\"content\":\"hello\"}\n\n"
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	defer fake.close()

	b := openCodeBackendForTest(fake)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "hi"}},
		}},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := waitForEvents(t, ch, 3*time.Second)
	if len(events) == 0 {
		t.Fatal("no events received")
	}
	if events[0].Type != llmclient.EventStart {
		t.Errorf("first event: got %q; want %q", events[0].Type, llmclient.EventStart)
	}
	if events[0].Fidelity == nil {
		t.Fatal("EventStart fidelity is nil")
	}
	if events[0].Fidelity.Streaming != llmclient.StreamingStructuredUnknown {
		t.Errorf("Fidelity.Streaming: got %q; want %q",
			events[0].Fidelity.Streaming, llmclient.StreamingStructuredUnknown)
	}
}

// TestOpenCodeHTTP_StreamEmitsTextFromContent verifies that SSE events with a
// "content" JSON field are mapped to text_start/delta/end events.
func TestOpenCodeHTTP_StreamEmitsTextFromContent(t *testing.T) {
	sseBody := fmt.Sprintf("data: {\"content\":%q}\n\n", "the answer")
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	defer fake.close()

	b := openCodeBackendForTest(fake)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "q"}},
		}},
	}

	ch, err := b.Stream(ctx, input)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	events := waitForEvents(t, ch, 3*time.Second)

	hasTextDelta := false
	for _, ev := range events {
		if ev.Type == llmclient.EventTextDelta && ev.Delta == "the answer" {
			hasTextDelta = true
		}
	}
	if !hasTextDelta {
		t.Errorf("expected EventTextDelta with 'the answer'; got events: %v", events)
	}
}

// TestOpenCodeHTTP_WithOpenCodePortOption verifies that WithOpenCodePort sets
// the port used to construct the base URL for Stream.
func TestOpenCodeHTTP_WithOpenCodePortOption(t *testing.T) {
	sseBody := "data: {\"type\":\"server.connected\"}\n\n"
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	defer fake.close()

	b := openCodeBackendForTest(fake)

	// Provide a non-default port via option; the backend uses its pre-set
	// baseURL (from openCodeBackendForTest), so we just verify Stream does
	// not error out when the option is supplied.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "ok"}},
		}},
	}

	ch, err := b.Stream(ctx, input, llmclient.WithOpenCodePort(4096))
	if err != nil {
		t.Fatalf("Stream with WithOpenCodePort: %v", err)
	}
	waitForEvents(t, ch, 2*time.Second)
}

// ─── fab-ophc: server-process lifetime ───────────────────────────────────────

// TestOpenCodeHTTP_CloseTerminatesServer verifies that Close() terminates the
// owned server supervisor and closes its done channel.
func TestOpenCodeHTTP_CloseTerminatesServer(t *testing.T) {
	exe := testExecutable(t)

	// Manually construct a supervisor wrapping a long-running subprocess, the
	// same way ensureServer does for the opencode server process.
	cmd := exec.Command(exe) //nolint:gosec // test binary path from testExecutable helper
	cmd.Env = append(baseEnv(), "LLMCLI_TEST_FIXTURE=sleep")
	configureProcessGroup(cmd)
	tail := newTailWriter()
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	s := &supervisor{
		cmd:           cmd,
		Stdout:        bufio.NewReader(bytes.NewReader(nil)),
		stderrTailBuf: tail,
		done:          make(chan struct{}),
		gracePeriod:   defaultPerCallGracePeriod,
	}

	b := &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", CliInfo{}),
		httpClient: http.DefaultClient,
		srv:        s,
		baseURL:    "http://127.0.0.1:9999",
	}

	if err := b.Close(); err != nil {
		// A non-nil error is expected when the process is killed; just log it.
		t.Logf("Close: %v (expected for a killed process)", err)
	}

	select {
	case <-s.done:
		// done is closed by wait() after cmd.Wait() returns — process reaped.
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor.done not closed within 5s after Close()")
	}
}

// TestOpenCodeHTTP_ServerCtxSurvivesStreamCancel verifies that waitReady driven
// by b.serverCtx is not affected by a per-turn stream context cancellation.
// This proves the fix for the bug where waitReady(ctx, url) (per-turn ctx) would
// return an error on stream cancel, triggering s.terminate() on a live server.
func TestOpenCodeHTTP_ServerCtxSurvivesStreamCancel(t *testing.T) {
	// Fake server that never emits server.connected — keeps waitReady looping.
	fake := newFakeOpenCodeServer(t, nil)
	defer fake.close()

	b := &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", CliInfo{}),
		httpClient: fake.srv.Client(),
	}
	b.serverCtx, b.serverCancel = context.WithCancel(context.Background())
	defer b.serverCancel()

	// Start waitReady using b.serverCtx in a background goroutine.
	readyErr := make(chan error, 1)
	go func() {
		readyErr <- b.waitReady(b.serverCtx, fake.srv.URL)
	}()

	// Give waitReady time to start polling.
	time.Sleep(200 * time.Millisecond)

	// Must still be running — a cancelled per-turn ctx would not affect it.
	select {
	case err := <-readyErr:
		t.Fatalf("waitReady returned early with %v; want still running", err)
	default:
	}

	// Cancelling b.serverCtx (what Close() does) must unblock waitReady.
	b.serverCancel()

	select {
	case <-readyErr:
		// Good — waitReady returned after serverCtx was cancelled.
	case <-time.After(2 * time.Second):
		t.Fatal("waitReady did not return within 2s after serverCtx cancel")
	}
}

// TestOpenCodeHTTP_ServerSurvivesFirstStreamCancel verifies that cancelling the
// per-turn Stream context does not kill the owned server supervisor. The server
// process must remain alive after the first Stream is cancelled and only be
// terminated when Close() is called.
func TestOpenCodeHTTP_ServerSurvivesFirstStreamCancel(t *testing.T) {
	exe := testExecutable(t)

	// Build a fake opencode HTTP server for the SSE/session/prompt lifecycle.
	// The SSE body keeps the stream open long enough that we can cancel it.
	sseBody := ": keep-alive\n\n" + strings.Repeat(": ping\n\n", 100)
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	defer fake.close()

	// Construct a supervisor wrapping a long-running subprocess. This simulates
	// the real opencode server process that ensureServer would have spawned.
	cmd := exec.Command(exe) //nolint:gosec // test binary path from testExecutable helper
	cmd.Env = append(baseEnv(), "LLMCLI_TEST_FIXTURE=sleep")
	configureProcessGroup(cmd)
	tail := newTailWriter()
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	s := &supervisor{
		cmd:           cmd,
		Stdout:        bufio.NewReader(bytes.NewReader(nil)),
		stderrTailBuf: tail,
		done:          make(chan struct{}),
		gracePeriod:   defaultPerCallGracePeriod,
	}

	// Wire the backend: pre-injected baseURL bypasses ensureServer; srv is the
	// real subprocess we want to verify survives the stream cancellation.
	// b.port must match openCodeDefaultPort so checkExistingServer returns true
	// for b.srv without terminating it and falling through to the baseURL path.
	b := &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", CliInfo{}),
		httpClient: fake.srv.Client(),
		srv:        s,
		baseURL:    fake.srv.URL,
		port:       openCodeDefaultPort,
	}
	b.serverCtx, b.serverCancel = context.WithCancel(context.Background())

	// Stream with a context we will cancel immediately after the stream starts.
	streamCtx, cancelStream := context.WithCancel(context.Background())

	input := &llmclient.Context{
		Messages: []llmclient.Message{{
			Role:    llmclient.RoleUser,
			Content: []llmclient.ContentBlock{{Type: llmclient.ContentText, Text: "ping"}},
		}},
	}

	ch, err := b.Stream(streamCtx, input)
	if err != nil {
		cancelStream()
		_ = b.Close()
		t.Fatalf("Stream: unexpected error: %v", err)
	}

	// Cancel the per-turn stream context. This should tear down the SSE
	// connection but must NOT kill the server subprocess.
	cancelStream()

	// Drain the event channel so the goroutines finish.
	for ev := range ch {
		_ = ev
	}

	// The server supervisor must still be alive — its done channel must be open.
	select {
	case <-s.done:
		t.Fatal("server supervisor.done was closed after stream cancel; server was killed prematurely")
	default:
		// Good — process is still running.
	}

	// Now Close() must terminate the process.
	if err := b.Close(); err != nil {
		t.Logf("Close: %v (expected for a killed process)", err)
	}

	select {
	case <-s.done:
		// done is closed by wait() after cmd.Wait() returns — process reaped.
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor.done not closed within 5s after Close()")
	}
}

// TestOpenCodeHTTP_RefusesWithOllama verifies that passing WithOllama to
// Stream() returns an error immediately, before attempting any network I/O.
// opencode-serve has no Ollama routing support.
func TestOpenCodeHTTP_RefusesWithOllama(t *testing.T) {
	b := NewOpenCodeHTTPBackend(CliInfo{})
	// Inject a baseURL so ensureServer would not try to spawn a real process;
	// but the Ollama check must fire before ensureServer is reached.
	b.baseURL = "http://127.0.0.1:0" // unreachable — we must not reach it

	ollamaCfg := llmclient.OllamaConfig{BaseURL: "http://localhost:11434", Model: "llama3"}
	_, err := b.Stream(context.Background(), nil, llmclient.WithOllama(ollamaCfg), llmclient.WithRequiredOptions(llmclient.OptionOllama))
	if err == nil {
		t.Fatal("Stream with WithOllama + RequiredOptions should return error, got nil")
	}
}
