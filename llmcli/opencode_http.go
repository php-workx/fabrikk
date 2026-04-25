package llmcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

// openCodeDefaultPort is the port used when WithOpenCodePort is not supplied.
const openCodeDefaultPort = 4096

// openCodeUsername is the HTTP basic-auth username the OpenCode server expects.
const openCodeUsername = "opencode"

// openCodeReadyTimeout is the maximum time to wait for the server to report
// server.connected after spawn.
const openCodeReadyTimeout = 10 * time.Second

// openCodeReadyPollInterval is the delay between readiness poll attempts.
const openCodeReadyPollInterval = 100 * time.Millisecond

// openCodeReadyAttemptTimeout is the per-attempt timeout when polling /event.
const openCodeReadyAttemptTimeout = 1 * time.Second

// maxOpenCodeSSELineBytes caps the per-line allocation in the SSE reader.
// 4 MiB is generous for event payloads while bounding memory usage.
const maxOpenCodeSSELineBytes = 4 * 1024 * 1024

// OpenCodeHTTPBackend is a persistent HTTP+SSE backend for `opencode serve`.
// It starts or reuses an opencode server process and communicates with it
// using a split-channel pattern: POST commands + GET /event SSE stream.
//
// The event schema is not yet fully mapped, so the backend is registered as
// StreamingStructuredUnknown. Tool, thinking, and usage fidelity are not
// claimed until the /doc schema mapping is pinned.
//
// OpenCodeHTTPBackend implements [llmclient.Backend].
type OpenCodeHTTPBackend struct {
	CliBackend

	mu      sync.Mutex
	srv     *supervisor // non-nil when we own the server process
	baseURL string      // e.g. "http://127.0.0.1:4096"; set after ensureServer

	// password is the HTTP basic-auth secret read from
	// OPENCODE_SERVER_PASSWORD. Empty means no auth is set.
	password string

	// schemaDiscovered is set atomically to 1 once discoverSchema succeeds
	// (or has been attempted). We record it as best-effort and continue even
	// on failure.
	schemaDiscovered atomic.Int32

	httpClient *http.Client

	closeOnce sync.Once
}

// NewOpenCodeHTTPBackend constructs an OpenCodeHTTPBackend from the detected
// CliInfo. The HTTP basic-auth password is read from OPENCODE_SERVER_PASSWORD.
func NewOpenCodeHTTPBackend(info CliInfo) *OpenCodeHTTPBackend {
	return &OpenCodeHTTPBackend{
		CliBackend: NewCliBackend("opencode-serve", info),
		password:   os.Getenv("OPENCODE_SERVER_PASSWORD"),
		httpClient: &http.Client{Timeout: 0}, // no global timeout; callers use ctx
	}
}

// newOpenCodeHTTPBackendWithClient is the test hook that allows injecting a
// custom http.Client (e.g. one pointing at an httptest.Server).
func newOpenCodeHTTPBackendWithClient(info CliInfo, client *http.Client, password string) *OpenCodeHTTPBackend {
	b := NewOpenCodeHTTPBackend(info)
	b.httpClient = client
	b.password = password
	return b
}

// Available reports whether the backend is usable. For a persistent backend
// this checks both the binary on disk and that the server (if owned) is alive.
func (b *OpenCodeHTTPBackend) Available() bool {
	if !b.binaryAvailable() {
		return observeAvailability(b.Name(), false)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.srv != nil {
		// Verify the owned process is still running.
		select {
		case <-b.srv.done:
			return observeAvailability(b.Name(), false) // process exited
		default:
		}
	}
	return observeAvailability(b.Name(), true)
}

// Close terminates the owned server process (if any) and releases resources.
// Safe to call multiple times.
func (b *OpenCodeHTTPBackend) Close() error {
	var err error
	b.closeOnce.Do(func() {
		b.mu.Lock()
		s := b.srv
		b.mu.Unlock()
		if s != nil {
			s.terminate(nil)
			err = s.wait()
		}
	})
	return err
}

// Stream opens the SSE event stream before submitting the prompt, then drives
// the full split-channel request lifecycle:
//
//  1. Ensure the opencode server is running or reuse an external one.
//  2. Attempt schema discovery via GET /doc (best-effort, non-blocking).
//  3. Open GET /event SSE stream.
//  4. Create a session via POST /session.
//  5. Send POST /session/:id/prompt_async in a goroutine.
//  6. Parse the SSE stream and emit normalized events on the returned channel.
//
// The returned channel is closed after exactly one terminal event (done or
// error). Cancelling ctx terminates the SSE stream and the channel.
func (b *OpenCodeHTTPBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	if err := llmclient.EnforceRequired(cfg, openCodeHTTPStaticCapabilities(b.info.Version)); err != nil {
		cancelTimeout()
		return nil, err
	}

	port := openCodeDefaultPort
	if cfg.OpenCodePort != 0 {
		port = cfg.OpenCodePort
	}
	model, started := observeStreamStart(b.Name(), cfg)

	if err := b.ensureServer(streamCtx, port); err != nil {
		cancelTimeout()
		return nil, fmt.Errorf("llmcli opencode: ensure server: %w", err)
	}

	// Schema discovery is best-effort — we're schema-gated regardless.
	if b.schemaDiscovered.Load() == 0 {
		_ = b.discoverSchema(streamCtx)
		b.schemaDiscovered.Store(1)
	}

	// 1. Open SSE stream BEFORE sending the prompt.
	sseCtx, cancelSSE := context.WithCancel(streamCtx)
	sseResp, err := b.openSSEStream(sseCtx) //nolint:bodyclose // response body is owned by the stream goroutine below
	if err != nil {
		cancelSSE()
		cancelTimeout()
		return nil, fmt.Errorf("llmcli opencode: open SSE stream: %w", err)
	}

	// 2. Create a session.
	sessionID, err := b.createSession(streamCtx)
	if err != nil {
		_ = sseResp.Body.Close()
		cancelSSE()
		cancelTimeout()
		return nil, fmt.Errorf("llmcli opencode: create session: %w", err)
	}

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	// promptDone carries the result of sendPromptAsync. It is buffered so the
	// prompt goroutine never blocks even if the SSE goroutine has already moved on.
	promptDone := make(chan error, 1)

	// Goroutine: send prompt_async. On failure, cancel the SSE reader so the
	// SSE goroutine unblocks from parseOpenCodeSSE.
	go func() {
		pErr := b.sendPromptAsync(streamCtx, sessionID, input)
		// Send before cancelSSE so the SSE goroutine always sees the error.
		promptDone <- pErr
		if pErr != nil {
			cancelSSE()
		}
	}()

	// Goroutine: parse the SSE stream and emit events. Owns all terminal events.
	go func() {
		defer cancelTimeout()
		defer cancelSSE()
		defer func() { _ = sseResp.Body.Close() }()

		parseErr := parseOpenCodeSSE(sseCtx, sseResp.Body, ch, openCodeHTTPFidelity(cfg))

		// Drain so that underlying connections are cleanly released.
		_, _ = io.Copy(io.Discard, sseResp.Body)

		// Wait for the prompt goroutine to finish. This ensures we can report a
		// prompt error even when the SSE body finishes before the prompt response.
		var pErr error
		select {
		case pErr = <-promptDone:
		case <-streamCtx.Done():
			// Parent context cancelled; prompt error (if any) is not actionable.
		}

		switch {
		case pErr != nil:
			te.error(streamCtx, pErr)
		case parseErr != nil && !isContextError(parseErr):
			te.error(streamCtx, fmt.Errorf("llmcli opencode: SSE parse: %w", parseErr))
		case streamCtx.Err() != nil:
			te.done(streamCtx, nil, nil, llmclient.StopCancelled)
		default:
			// SSE stream ended cleanly with no prompt error.
			te.done(streamCtx, nil, nil, llmclient.StopEndTurn)
		}
	}()

	return observeStream(b.Name(), model, started, ch), nil
}

// ensureServer makes sure a server is available at the given port.
//
// If b.baseURL is already set (pre-configured or adopted in a previous call),
// the server is assumed ready — no spawn or port check is performed.
// If we already own a live process, it is reused. If the port is already in
// use (external instance), we adopt it. Otherwise we spawn a new process.
func (b *OpenCodeHTTPBackend) ensureServer(ctx context.Context, port int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Already have an owned live process.
	if b.srv != nil {
		select {
		case <-b.srv.done:
			// Process exited; fall through to respawn.
			b.srv = nil
		default:
			return nil
		}
	}

	// Pre-configured base URL (test injection or caller-supplied external server).
	if b.baseURL != "" {
		return nil
	}

	// Check if an external server is already listening on the port.
	if isPortInUse(port) {
		// Adopt the external server; do not spawn our own.
		b.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
		return b.waitReady(ctx)
	}

	// Spawn the server.
	//nolint:gosec // path is from exec.LookPath via CliInfo, not user input
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- b.info.Path is a detected CLI binary path and only fixed backend args are appended.
	cmd := exec.CommandContext(ctx, b.info.Path, "serve", "--port", fmt.Sprint(port))
	configureProcessGroup(cmd)

	tail := newTailWriter()
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start opencode serve: %w", err)
	}

	// Wrap in a supervisor for lifecycle management.
	s := &supervisor{
		cmd:           cmd,
		Stdout:        bufio.NewReader(bytes.NewReader(nil)), // opencode serve has no useful stdout
		stderrTailBuf: tail,
		done:          make(chan struct{}),
	}

	go func() {
		select {
		case <-ctx.Done():
			s.terminate(ctx.Err())
		case <-s.done:
		}
	}()

	b.srv = s
	b.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	return b.waitReady(ctx)
}

// waitReady polls GET /event until the server emits a "server.connected" SSE
// event or the deadline (openCodeReadyTimeout) is exceeded.
func (b *OpenCodeHTTPBackend) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(openCodeReadyTimeout)

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		attemptCtx, cancel := context.WithTimeout(ctx, openCodeReadyAttemptTimeout)
		req, _ := http.NewRequestWithContext(attemptCtx, http.MethodGet, b.baseURL+"/event", http.NoBody)
		b.setAuth(req)
		req.Header.Set("Accept", "text/event-stream")

		resp, err := b.httpClient.Do(req)
		if err != nil {
			cancel()
			time.Sleep(openCodeReadyPollInterval)
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized {
			_ = resp.Body.Close()
			cancel()
			return errors.New("opencode serve: 401 unauthorized — check OPENCODE_SERVER_PASSWORD")
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			cancel()
			time.Sleep(openCodeReadyPollInterval)
			continue
		}

		// Read SSE lines until we see server.connected or the attempt times out.
		connected := b.scanForServerConnected(resp.Body)
		_ = resp.Body.Close()
		cancel()

		if connected {
			return nil
		}

		if attemptCtx.Err() != nil && ctx.Err() == nil {
			// Per-attempt timeout: retry.
			time.Sleep(openCodeReadyPollInterval)
			continue
		}
	}

	return fmt.Errorf("opencode serve: readiness timeout after %s at %s", openCodeReadyTimeout, b.baseURL)
}

// scanForServerConnected reads lines from body until a "server.connected" data
// payload is seen or the body read returns an error. It returns true when the
// event is found.
func (b *OpenCodeHTTPBackend) scanForServerConnected(body io.Reader) bool {
	r := bufio.NewReader(body)
	for {
		ev, err := internal.ReadEvent(r, 4096)
		if err != nil {
			return false
		}
		if bytes.Contains(ev.Data, []byte("server.connected")) {
			return true
		}
	}
}

// discoverSchema issues GET /doc on the server and stores the result. It is
// called once per backend instance as a best-effort schema gate. Failure is
// non-fatal: the backend operates in StreamingStructuredUnknown mode regardless.
func (b *OpenCodeHTTPBackend) discoverSchema(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/doc", http.NoBody)
	if err != nil {
		return err
	}
	b.setAuth(req)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("opencode /doc returned %d", resp.StatusCode)
	}
	return nil
}

// openSSEStream opens the long-lived GET /event SSE connection and returns the
// HTTP response. The caller owns the response body and must close it.
func (b *OpenCodeHTTPBackend) openSSEStream(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/event", http.NoBody)
	if err != nil {
		return nil, err
	}
	b.setAuth(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET /event returned %d", resp.StatusCode)
	}
	return resp, nil
}

// createSession issues POST /session and returns the new session ID.
func (b *OpenCodeHTTPBackend) createSession(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/session", http.NoBody)
	if err != nil {
		return "", err
	}
	b.setAuth(req)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("POST /session returned %d", resp.StatusCode)
	}

	var session struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("decode session response: %w", err)
	}
	if session.ID == "" {
		return "", errors.New("POST /session returned empty id")
	}
	return session.ID, nil
}

// openCodePromptBody is the JSON request body for prompt_async.
type openCodePromptBody struct {
	// Text is the concatenated user messages.
	Text string `json:"text"`
}

// sendPromptAsync submits the prompt via POST /session/:id/prompt_async.
// It returns nil on a 204 No Content response, and a non-nil error otherwise
// (including network errors and unexpected status codes).
func (b *OpenCodeHTTPBackend) sendPromptAsync(
	ctx context.Context,
	sessionID string,
	input *llmclient.Context,
) error {
	body := openCodePromptBody{
		Text: lastUserMessage(input),
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal prompt body: %w", err)
	}

	url := b.baseURL + "/session/" + sessionID + "/prompt_async"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build prompt_async request: %w", err)
	}
	b.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST prompt_async: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // always drain

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("POST prompt_async returned %d (want 204)", resp.StatusCode)
	}
	return nil
}

// setAuth attaches HTTP basic auth to req when a password is configured.
func (b *OpenCodeHTTPBackend) setAuth(req *http.Request) {
	if b.password != "" {
		req.SetBasicAuth(openCodeUsername, b.password)
	}
}

// isPortInUse returns true when a TCP connection to 127.0.0.1:port succeeds
// within a short timeout.
func isPortInUse(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// isContextError reports whether err is or wraps a context cancellation or
// deadline exceeded error.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// — SSE stream parser ---------------------------------------------------------

// parseOpenCodeSSE reads SSE events from body, emits normalized llmclient
// events to out, and returns when the stream ends or ctx is cancelled.
//
// Since the OpenCode event schema is schema-gated (StreamingStructuredUnknown),
// this parser:
//   - Emits one EventStart carrying StreamingStructuredUnknown fidelity.
//   - Emits text deltas for any event whose data contains a recognizable text
//     payload (best-effort; silently skips unrecognized events).
//   - Returns nil on clean EOF; the caller is responsible for emitting the
//     terminal event so it can fold in any prompt error that may have arrived.
//
// It does NOT emit tool, thinking, or usage events until the schema is pinned.
func parseOpenCodeSSE(
	ctx context.Context,
	body io.Reader,
	out chan<- llmclient.Event,
	fidelity *llmclient.Fidelity,
) error {
	r := bufio.NewReader(body)
	startEmitted := false
	contentIndex := 0

	for {
		ev, err := internal.ReadEvent(r, maxOpenCodeSSELineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		// Check context cancellation between events.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Emit start on first event.
		if !startEmitted {
			startFidelity := fidelity
			if startFidelity == nil {
				startFidelity = &llmclient.Fidelity{
					Streaming:   llmclient.StreamingStructuredUnknown,
					ToolControl: llmclient.ToolControlNone,
				}
			}
			if !emit(ctx, out, startEvent("", startFidelity)) {
				return ctx.Err()
			}
			startEmitted = true
		}

		// Skip empty payloads (e.g. server.connected heartbeats).
		if len(ev.Data) == 0 {
			continue
		}

		// Best-effort text extraction: try to unmarshal a JSON object with a
		// "content" or "text" string field. Unknown schemas are silently skipped.
		text := extractOpenCodeText(ev.Data)
		if text == "" {
			continue
		}

		evs := textSequence(contentIndex, text)
		for i := range evs {
			if !emit(ctx, out, evs[i]) {
				return ctx.Err()
			}
		}
		contentIndex++
	}
}

func openCodeHTTPStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend: "opencode-serve",
		Version: version,
		// StreamingStructuredUnknown: SSE transport is confirmed, but the
		// event schema is not yet pinned. Full-fidelity events are not claimed
		// until /doc schema mapping is verified by tests.
		Streaming: llmclient.StreamingStructuredUnknown,
		MultiTurn: true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionOpenCodePort: llmclient.OptionSupportFull,
			llmclient.OptionTimeout:      llmclient.OptionSupportFull,
		},
	}
}

func openCodeHTTPFidelity(cfg llmclient.RequestConfig) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.OpenCodePort != 0 {
		results[llmclient.OptionOpenCodePort] = llmclient.OptionApplied
	}
	return &llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructuredUnknown,
		ToolControl:   llmclient.ToolControlNone,
		OptionResults: mergeOptionResults(results, executionOptionResults(cfg, openCodeHTTPStaticCapabilities(""))),
	}
}

// openCodeTextPayload is the minimal JSON shape we try to extract text from.
// It covers the most common patterns seen in OpenCode SSE events.
type openCodeTextPayload struct {
	Content string `json:"content"`
	Text    string `json:"text"`
}

// extractOpenCodeText tries to find a text string in a raw SSE data payload.
// Returns empty string when the payload is not recognized.
func extractOpenCodeText(data []byte) string {
	// Fast path: skip obviously non-JSON payloads.
	if len(data) == 0 || data[0] != '{' {
		return ""
	}
	var p openCodeTextPayload
	if json.Unmarshal(data, &p) != nil {
		return ""
	}
	if p.Content != "" {
		return p.Content
	}
	return p.Text
}
