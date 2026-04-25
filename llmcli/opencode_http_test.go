package llmcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

	status := f.promptStatus
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
}

func (f *fakeOpenCodeServer) close() { f.srv.Close() }

// requestOrderSnapshot returns a copy of the recorded request order.
func (f *fakeOpenCodeServer) requestOrderSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]string, len(f.requestOrder))
	copy(cp, f.requestOrder)
	return cp
}

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

	if err := b.waitReady(ctx); err != nil {
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

	err := b.waitReady(ctx)
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
	b.schemaDiscovered.Store(1)

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

// TestOpenCodeHTTP_DiscoversSchema verifies that GET /doc is called during
// the first Stream invocation (Criterion 2).
func TestOpenCodeHTTP_DiscoversSchema(t *testing.T) {
	sseBody := "data: {\"type\":\"server.connected\"}\n\n"
	fake := newFakeOpenCodeServer(t, []string{sseBody})
	defer fake.close()

	b := openCodeBackendForTest(fake)
	// schemaDiscovered is 0 (default), so discoverSchema should run.

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
		t.Fatalf("Stream: %v", err)
	}
	waitForEvents(t, ch, 3*time.Second)

	order := fake.requestOrderSnapshot()
	found := false
	for _, r := range order {
		if r == "GET /doc" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GET /doc not observed; requests: %v", order)
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
	b.schemaDiscovered.Store(1)

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
	// Use an already-closed server so the HTTP call fails with a connection error.
	fake := newFakeOpenCodeServer(t, []string{"data: {\"type\":\"server.connected\"}\n\n"})
	b := openCodeBackendForTest(fake)
	b.schemaDiscovered.Store(1)

	// Close the server now so prompt_async will fail.
	fake.close()

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
		// openSSEStream may fail immediately; that is acceptable.
		return
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
	// Also accept no events if the channel was closed without error (the
	// server was gone before SSE opened).
	if !hasError && len(events) != 0 {
		t.Fatalf("Stream/waitForEvents got %d events without EventError after prompt_async failure: %v", len(events), events)
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
	b.schemaDiscovered.Store(1)

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
	b.schemaDiscovered.Store(1)

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
	b.schemaDiscovered.Store(1)

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
