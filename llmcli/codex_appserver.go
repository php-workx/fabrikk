package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

// maxCodexHeaderBytes is the per-frame header size cap for the Codex
// app-server JSON-RPC framing. 4 KiB is generous for LSP headers.
const maxCodexHeaderBytes = 4096

// maxCodexBodyBytes is the per-frame body size cap. 8 MiB accommodates large
// tool call arguments while bounding memory allocation from a corrupt peer.
const maxCodexBodyBytes = 8 << 20

// ─── Persistent process wrapper ──────────────────────────────────────────────

// codexProcess wraps the bidirectional stdio connection to a codex app-server
// process. It holds an optional supervisor (nil when constructed in tests via
// in-process pipes) alongside the stdin writer and stdout reader.
type codexProcess struct {
	sup    *supervisor // nil in test mode
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

// terminate sends a termination signal to the process tree (when sup != nil)
// and closes stdin. Safe to call more than once.
func (p *codexProcess) terminate(reason error) {
	_ = p.stdin.Close()
	if p.sup != nil {
		p.sup.terminate(reason)
	}
}

// drain discards remaining stdout and waits for the process to exit. Must only
// be called after terminate or after the process has already closed stdout.
func (p *codexProcess) drain() {
	if p.sup != nil {
		_, _ = io.Copy(io.Discard, p.sup.Stdout)
		_ = p.sup.wait()
	}
}

// alive reports whether the process is still running. Always returns true when
// sup is nil (test mode — caller manages lifecycle).
func (p *codexProcess) alive() bool {
	if p.sup == nil {
		return true
	}
	select {
	case <-p.sup.done:
		return false
	default:
		return true
	}
}

// startCodexAppServer starts `codex app-server --listen stdio://` and returns a
// codexProcess with bidirectional stdio plumbing. The process is placed in its
// own process group so that context cancellation terminates the whole tree.
func startCodexAppServer(ctx context.Context, info CliInfo, env []string) (*codexProcess, error) {
	//nolint:gosec // info.Path comes from exec.LookPath via DetectAvailable
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- info.Path is a detected CLI binary path and the remaining args are fixed backend literals.
	cmd := exec.Command(info.Path, "app-server", "--listen", "stdio://")
	cmd.Env = env

	configureProcessGroup(cmd)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("llmcli codex-appserver: stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return nil, fmt.Errorf("llmcli codex-appserver: stdout pipe: %w", err)
	}

	tail := newTailWriter()
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return nil, fmt.Errorf("llmcli codex-appserver: start process: %w", err)
	}

	stdoutReader := bufio.NewReader(stdoutPipe)

	sup := &supervisor{
		cmd:           cmd,
		Stdout:        stdoutReader,
		stderrTailBuf: tail,
		done:          make(chan struct{}),
	}

	// Background goroutine: terminate the process tree when ctx is cancelled,
	// or exit early when the process exits naturally.
	go func() {
		select {
		case <-ctx.Done():
			sup.terminate(ctx.Err())
		case <-sup.done:
		}
	}()

	return &codexProcess{
		sup:    sup,
		stdin:  stdinPipe,
		stdout: stdoutReader,
	}, nil
}

// ─── JSON-RPC protocol types ──────────────────────────────────────────────────

// codexRPCFrame is the minimal decoded envelope for routing incoming JSON-RPC
// 2.0 frames.
type codexRPCFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *codexRPCError  `json:"error,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// codexEventParams is the expected shape of a notification params object.
//
// [VERIFY DURING IMPLEMENTATION] — the exact field names must be confirmed
// against a running codex app-server. Unknown notification types are silently
// skipped so that future protocol additions do not break existing streams.
type codexEventParams struct {
	Type       string `json:"type"`
	ContentIdx int    `json:"content_index"`
	Delta      string `json:"delta,omitempty"`
	Content    string `json:"content,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ─── Backend ──────────────────────────────────────────────────────────────────

// CodexAppServerBackend is a persistent Codex app-server backend. It owns a
// long-lived `codex app-server --listen stdio://` subprocess and serializes
// turns through a capacity-1 channel semaphore.
//
// A single stream turn proceeds as follows:
//  1. Acquire the turn semaphore.
//  2. Ensure the process is running and healthy; start or restart if needed.
//  3. Write a JSON-RPC `turn` request frame to process stdin.
//  4. Read JSON-RPC notification frames from process stdout, mapping each to a
//     normalized llmclient.Event.
//  5. Emit exactly one terminal done or error event, then release the semaphore.
//
// Protocol corruption (malformed framing, JSON parse failure, unexpected EOF)
// emits exactly one terminal error event, terminates the subprocess, and marks
// the backend unhealthy. The next Stream call starts a fresh process before
// sending its request, satisfying the "block or restart before accepting another
// turn" requirement.
//
// CodexAppServerBackend implements [llmclient.Backend].
type CodexAppServerBackend struct {
	CliBackend

	procMu     sync.Mutex
	proc       *codexProcess // nil if not yet started or after termination
	healthy    bool          // false after protocol corruption; reset on restart
	routingKey string

	// nextID is incremented atomically for each outbound request.
	nextID atomic.Int64

	// turnCh is a capacity-1 channel semaphore. The semaphore is seeded with
	// one token on construction so the first caller acquires it immediately.
	// Only one turn may be in-flight at a time.
	turnCh chan struct{}

	// procFactory creates a new codexProcess. Tests replace this function to
	// inject in-process pipe pairs instead of spawning a real subprocess.
	// Default value is set in NewCodexAppServerBackend.
	procFactory func(ctx context.Context, env []string) (*codexProcess, error)
}

// NewCodexAppServerBackend constructs a CodexAppServerBackend that uses the
// detected CliInfo to spawn `codex app-server --listen stdio://` on demand.
func NewCodexAppServerBackend(info CliInfo) *CodexAppServerBackend {
	b := &CodexAppServerBackend{
		CliBackend: NewCliBackend("codex-appserver", info),
		turnCh:     make(chan struct{}, 1),
	}
	b.procFactory = func(ctx context.Context, env []string) (*codexProcess, error) {
		return startCodexAppServer(ctx, b.info, env)
	}
	b.turnCh <- struct{}{} // seed with one token
	return b
}

// Available reports whether the backend can accept requests. Returns true when
// the binary is on PATH. If a process has already been started, it also
// verifies the process has not exited and the backend is healthy.
func (b *CodexAppServerBackend) Available() bool {
	if !b.binaryAvailable() {
		return observeAvailability(b.Name(), false)
	}
	b.procMu.Lock()
	defer b.procMu.Unlock()
	if b.proc == nil {
		return observeAvailability(b.Name(), true) // binary exists; process starts lazily on first Stream call
	}
	return observeAvailability(b.Name(), b.healthy && b.proc.alive())
}

// Capabilities returns the static capabilities for this backend instance.
func (b *CodexAppServerBackend) Capabilities() llmclient.Capabilities {
	return codexAppServerStaticCapabilities(b.info.Version)
}

// Close terminates the persistent subprocess and releases all resources.
// Safe to call multiple times or concurrently. Blocks until the process exits.
func (b *CodexAppServerBackend) Close() error {
	b.procMu.Lock()
	defer b.procMu.Unlock()

	if b.proc == nil {
		return nil
	}

	b.proc.terminate(nil)
	b.proc.drain()
	b.proc = nil
	b.healthy = false
	b.routingKey = ""

	return nil
}

// Stream acquires the turn semaphore, ensures the process is running, writes a
// JSON-RPC `turn` request, and returns a channel of normalized Event values.
//
// The channel is closed after exactly one terminal event (done or error).
// Cancelling ctx terminates the in-flight read loop and emits StopCancelled.
func (b *CodexAppServerBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	if err := checkCodexAppServerRequiredOptions(cfg); err != nil {
		cancelTimeout()
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	// Fast path: a pre-cancelled context must be rejected before we consume
	// the semaphore token, because Go's select is non-deterministic when
	// multiple cases are ready simultaneously.
	if err := streamCtx.Err(); err != nil {
		cancelTimeout()
		return nil, err
	}

	// Acquire turn semaphore — blocks if another turn is in-flight.
	select {
	case <-b.turnCh:
	case <-streamCtx.Done():
		cancelTimeout()
		return nil, streamCtx.Err()
	}

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	go func() {
		defer cancelTimeout()
		defer func() { b.turnCh <- struct{}{} }() // always release semaphore
		defer te.close()                          // safety guard

		proc, err := b.ensureProcess(streamCtx, cfg)
		if err != nil {
			te.error(streamCtx, fmt.Errorf("llmcli codex-appserver: ensure process: %w", err))
			return
		}

		cancelWatchDone := make(chan struct{})
		go func() {
			select {
			case <-streamCtx.Done():
				b.restartAfterProtocolError(streamCtx.Err())
			case <-cancelWatchDone:
			}
		}()
		defer close(cancelWatchDone)

		id := b.nextID.Add(1)
		prompt := lastUserMessage(input)

		params := map[string]any{
			"message": prompt,
		}
		if cfg.SessionID != "" {
			params["session_id"] = cfg.SessionID
		}
		if cfg.Model != "" {
			params["model"] = cfg.Model
		}

		if err := internal.WriteRequest(proc.stdin, id, "turn", params); err != nil {
			b.restartAfterProtocolError(err)
			te.error(streamCtx, fmt.Errorf("llmcli codex-appserver: write turn request: %w", err))
			return
		}

		// Emit start event. The session ID is echoed from options; the server
		// may include a canonical session ID in a future notification.
		fidelity := codexAppServerFidelity(cfg)
		if !emit(streamCtx, ch, startEvent(cfg.SessionID, fidelity)) {
			te.done(streamCtx, nil, nil, llmclient.StopCancelled)
			return
		}

		b.readTurnEvents(streamCtx, proc, ch, te)
	}()

	return observeStream(b.Name(), model, started, ch), nil
}

// ensureProcess returns the current healthy process, or starts a fresh one if
// none is running. Caller must not hold procMu.
func (b *CodexAppServerBackend) ensureProcess(ctx context.Context, cfg llmclient.RequestConfig) (*codexProcess, error) { //nolint:gocritic // RequestConfig value avoids mutation across persistent process setup.
	b.procMu.Lock()
	defer b.procMu.Unlock()

	overrides := map[string]string(nil)
	routingKey := "default"
	if cfg.Ollama != nil {
		overrides = codexAppServerOllamaEnvOverrides(*cfg.Ollama)
		routingKey = ollamaOpenAIBaseURL(*cfg.Ollama) + "|" + cfg.Ollama.APIKey
	}
	env := resolveProcessEnv(cfg, overrides)
	if cfg.EnvironmentSet || len(cfg.EnvironmentOverlay) > 0 {
		routingKey += "|env:" + strings.Join(env, "\x00")
	}

	if b.proc != nil && b.healthy && b.proc.alive() && b.routingKey == routingKey {
		return b.proc, nil
	}

	// Tear down any stale process before starting fresh.
	if b.proc != nil {
		b.proc.terminate(nil)
		b.proc.drain()
		b.proc = nil
	}

	proc, err := b.procFactory(ctx, env)
	if err != nil {
		return nil, err
	}

	b.proc = proc
	b.healthy = true
	b.routingKey = routingKey

	return proc, nil
}

// restartAfterProtocolError terminates the current process, marks the backend
// unhealthy, and clears the process reference. The next call to ensureProcess
// will start a fresh process, satisfying the "block or restart before accepting
// another turn" acceptance criterion.
//
// Called whenever a framing or JSON parse error is detected on stdout. Safe to
// call while holding no locks.
func (b *CodexAppServerBackend) restartAfterProtocolError(err error) {
	b.procMu.Lock()
	defer b.procMu.Unlock()

	if b.proc == nil {
		return
	}

	b.proc.terminate(err)
	b.proc.drain()
	b.proc = nil
	b.healthy = false
}

// readTurnEvents reads JSON-RPC notification frames from proc.stdout, mapping
// each to a normalized llmclient.Event, until a terminal "done" notification
// arrives or an error occurs.
//
// Exactly one terminal event is emitted via te. On protocol error,
// restartAfterProtocolError is called before te.error.
func (b *CodexAppServerBackend) readTurnEvents(
	ctx context.Context,
	proc *codexProcess,
	ch chan<- llmclient.Event,
	te *terminalEmitter,
) {
	assembledMsg := &llmclient.AssistantMessage{Role: "assistant"}
	contentIndex := 0

	for {
		if ctx.Err() != nil {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return
		}

		body, err := internal.ReadFrame(proc.stdout, maxCodexHeaderBytes, maxCodexBodyBytes)
		if err != nil {
			if ctx.Err() != nil {
				te.done(ctx, nil, nil, llmclient.StopCancelled)
				return
			}
			if errors.Is(err, io.EOF) {
				// Process closed stdout without a done notification — treat as
				// successful end of turn.
				te.done(ctx, assembledMsg, nil, llmclient.StopEndTurn)
				return
			}

			protocolErr := fmt.Errorf("llmcli codex-appserver: read frame: %w", err)
			b.restartAfterProtocolError(protocolErr)
			te.error(ctx, protocolErr)
			return
		}

		var frame codexRPCFrame
		if jsonErr := json.Unmarshal(body, &frame); jsonErr != nil {
			protocolErr := fmt.Errorf("llmcli codex-appserver: parse frame JSON: %w", jsonErr)
			b.restartAfterProtocolError(protocolErr)
			te.error(ctx, protocolErr)
			return
		}

		// JSON-RPC error response.
		if frame.Error != nil {
			protocolErr := fmt.Errorf("llmcli codex-appserver: RPC error %d: %s",
				frame.Error.Code, frame.Error.Message)
			b.restartAfterProtocolError(protocolErr)
			te.error(ctx, protocolErr)
			return
		}

		done := b.dispatchCodexFrame(ctx, frame, ch, te, &contentIndex, assembledMsg)
		if done {
			return
		}
	}
}

// dispatchCodexFrame routes an incoming JSON-RPC frame to its handler based on
// the Method field. Returns true when a terminal event has been emitted (done
// or error). Returns false to continue reading.
func (b *CodexAppServerBackend) dispatchCodexFrame(
	ctx context.Context,
	frame codexRPCFrame,
	ch chan<- llmclient.Event,
	te *terminalEmitter,
	contentIndex *int,
	assembledMsg *llmclient.AssistantMessage,
) (done bool) {
	// Frames without a Method are responses to our requests (e.g. to turn).
	// They are not streaming notifications; skip them here.
	if frame.Method == "" {
		return false
	}

	var params codexEventParams
	if len(frame.Params) > 0 {
		// Parse failure → skip unknown notification gracefully.
		if err := json.Unmarshal(frame.Params, &params); err != nil {
			return false
		}
	}

	switch frame.Method {
	case "done":
		reason := llmclient.StopEndTurn
		if params.StopReason != "" {
			reason = llmclient.StopReason(params.StopReason)
		}
		if assembledMsg != nil {
			assembledMsg.StopReason = reason
		}
		te.done(ctx, assembledMsg, nil, reason)
		return true

	case "text_start":
		idx := *contentIndex
		if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextStart, ContentIndex: idx}) {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return true
		}

	case "text_delta":
		idx := *contentIndex
		if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextDelta, ContentIndex: idx, Delta: params.Delta}) {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return true
		}

	case "text_end":
		idx := *contentIndex
		if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextEnd, ContentIndex: idx, Content: params.Content}) {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return true
		}
		if assembledMsg != nil {
			assembledMsg.Content = append(assembledMsg.Content, llmclient.ContentBlock{
				Type: llmclient.ContentText,
				Text: params.Content,
			})
		}
		*contentIndex++

	default:
		// Unknown notification type — skip gracefully.
	}

	return false
}

// ─── Options ──────────────────────────────────────────────────────────────────

// checkCodexAppServerRequiredOptions returns ErrUnsupportedOption if any option
// in cfg.RequiredOptions is not supported by the Codex app-server backend.
func checkCodexAppServerRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	return llmclient.EnforceRequired(cfg, codexAppServerStaticCapabilities(""))
}

// codexAppServerFidelity returns the Fidelity for the start event, reflecting
// which options were actually applied.
func codexAppServerFidelity(cfg llmclient.RequestConfig) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
	optResults := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		optResults[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.SessionID != "" {
		optResults[llmclient.OptionSession] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		optResults[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	if cfg.CodexProfile != "" {
		optResults[llmclient.OptionCodexProfile] = llmclient.OptionUnsupported
	}
	optionResults := mergeOptionResults(optResults, executionOptionResults(cfg, codexAppServerStaticCapabilities("")))

	return &llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructured,
		ToolControl:   llmclient.ToolControlBuiltIn,
		OptionResults: optionResults,
	}
}

// codexAppServerStaticCapabilities returns the static capabilities for the
// Codex app-server backend. version is the detected binary version string.
func codexAppServerStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "codex-appserver",
		Version:       version,
		Streaming:     llmclient.StreamingStructured,
		ToolEvents:    true,
		MultiTurn:     true,
		OllamaRouting: true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:              llmclient.OptionSupportFull,
			llmclient.OptionSession:            llmclient.OptionSupportFull,
			llmclient.OptionOllama:             llmclient.OptionSupportFull,
			llmclient.OptionEnvironment:        llmclient.OptionSupportFull,
			llmclient.OptionEnvironmentOverlay: llmclient.OptionSupportFull,
			llmclient.OptionTimeout:            llmclient.OptionSupportFull,
		},
	}
}
