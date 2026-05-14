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

// maxCodexBodyBytes is the per-line body size cap for NDJSON framing. 8 MiB
// accommodates large tool call arguments while bounding memory allocation from
// a corrupt peer.
const maxCodexBodyBytes = 8 << 20

// ─── Persistent process wrapper ──────────────────────────────────────────────

// codexProcess wraps the bidirectional stdio connection to a codex app-server
// process. It holds an optional supervisor (nil when constructed in tests via
// in-process pipes) alongside the stdin writer and stdout reader.
type codexProcess struct {
	sup          *supervisor // nil in test mode
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	stdoutCloser io.Closer

	// threadID is set after the initialize/thread/start handshake completes.
	// It is reused for all subsequent turn/start requests on this process.
	threadID string
}

// terminate sends a termination signal to the process tree (when sup != nil)
// and closes stdin. Safe to call more than once.
func (p *codexProcess) terminate(reason error) {
	_ = p.stdin.Close()
	if p.stdoutCloser != nil {
		_ = p.stdoutCloser.Close()
	}
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
		sup:          sup,
		stdin:        stdinPipe,
		stdout:       stdoutReader,
		stdoutCloser: stdoutPipe,
	}, nil
}

// ─── JSON-RPC protocol types ──────────────────────────────────────────────────

// codexRPCFrame is the minimal decoded envelope for routing incoming JSON-RPC
// 2.0 frames over NDJSON transport.
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

// ─── Inbound notification param structs ──────────────────────────────────────

// ThreadItem type constants for codexThreadItem.Type.
const (
	codexItemTypeDynamicToolCall  = "dynamicToolCall"
	codexItemTypeMCPToolCall      = "mcpToolCall"
	codexItemTypeCommandExecution = "commandExecution"
)

// codexThreadStartedParams is the params shape for the "thread/started"
// notification. The thread.id field becomes the threadID for all subsequent
// turn/start requests.
type codexThreadStartedParams struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

// codexAgentMessageDeltaParams is the params shape for the
// "item/agentMessage/delta" notification.
type codexAgentMessageDeltaParams struct {
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// codexThreadItem is a completed item carried by "item/started" and
// "item/completed" params. It is a discriminated union on the Type field.
type codexThreadItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Text      string          `json:"text,omitempty"`      // agentMessage
	Tool      string          `json:"tool,omitempty"`      // dynamicToolCall, mcpToolCall
	Namespace string          `json:"namespace,omitempty"` // dynamicToolCall
	Server    string          `json:"server,omitempty"`    // mcpToolCall
	Command   string          `json:"command,omitempty"`   // commandExecution
	Arguments json.RawMessage `json:"arguments,omitempty"` // dynamicToolCall, mcpToolCall
	Status    string          `json:"status,omitempty"`
}

// codexItemCompletedParams is the params shape for the "item/completed"
// notification.
type codexItemCompletedParams struct {
	Item     codexThreadItem `json:"item"`
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
}

// codexItemStartedParams is the params shape for the "item/started"
// notification.
type codexItemStartedParams struct {
	Item     codexThreadItem `json:"item"`
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
}

// codexReasoningDeltaParams is the params shape for the
// "item/reasoning/textDelta" and "item/reasoning/summaryTextDelta"
// notifications.
type codexReasoningDeltaParams struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	ContentIndex int    `json:"contentIndex"`
	PartIndex    int    `json:"partIndex,omitempty"` // summaryTextDelta only
}

// codexErrorParams is the params shape for the "error" notification.
type codexErrorParams struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	WillRetry bool   `json:"willRetry"`
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
}

// ─── Outbound request helpers ─────────────────────────────────────────────────

// codexInitializeParams is the params for the "initialize" request.
type codexInitializeParams struct {
	ClientInfo   codexClientInfo `json:"clientInfo"`
	Capabilities any             `json:"capabilities"`
}

type codexClientInfo struct {
	Name    string  `json:"name"`
	Title   *string `json:"title"`
	Version string  `json:"version"`
}

// codexThreadStartParams is the params for the "thread/start" request.
type codexThreadStartParams struct {
	Model string `json:"model,omitempty"`
}

// codexTurnStartParams is the params for the "turn/start" request.
type codexTurnStartParams struct {
	ThreadID string           `json:"threadId"`
	Input    []codexTurnInput `json:"input"`
	Model    string           `json:"model,omitempty"`
}

type codexTurnInput struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	TextElements []any  `json:"text_elements"`
}

// codexRPCRequest is the wire format for an outbound JSON-RPC 2.0 request.
type codexRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// ─── Backend ──────────────────────────────────────────────────────────────────

// CodexAppServerBackend is a persistent Codex app-server backend. It owns a
// long-lived `codex app-server --listen stdio://` subprocess and serializes
// turns through a capacity-1 channel semaphore.
//
// A single stream turn proceeds as follows:
//  1. Acquire the turn semaphore.
//  2. Ensure the process is running and healthy; start or restart if needed.
//  3. On a fresh process: run the initialize/thread/start handshake to obtain
//     a threadID.
//  4. Write a JSON-RPC `turn/start` request frame to process stdin.
//  5. Read JSON-RPC notification frames from process stdout, mapping each to a
//     normalized llmclient.Event.
//  6. Emit exactly one terminal done or error event, then release the semaphore.
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
	return observeAvailability(b.Name(), b.Ready(context.Background()).State == llmclient.ReadyOK)
}

// Ready checks codex auth markers and persistent process liveness.
func (b *CodexAppServerBackend) Ready(_ context.Context) llmclient.ReadyReport {
	if !b.binaryAvailable() {
		return readyMissingBinary(b.Name(), b.info.Path)
	}
	if !anyPathExists(homePath(".codex", "auth.json"), homePath(".codex", "config.toml")) {
		return readyNotAuthed(b.Name(), "run `codex login`")
	}
	b.procMu.Lock()
	defer b.procMu.Unlock()
	if b.proc == nil {
		return llmclient.ReadyReport{State: llmclient.ReadyOK}
	}
	if !b.healthy || !b.proc.alive() {
		return llmclient.ReadyReport{State: llmclient.ReadyUnknown, Detail: "codex app-server process is not healthy"}
	}
	return llmclient.ReadyReport{State: llmclient.ReadyOK}
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

// Stream acquires the turn semaphore, ensures the process is running, runs the
// per-process handshake on a fresh process, writes a JSON-RPC `turn/start`
// request, and returns a channel of normalized Event values.
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

	// Fast path: reject a pre-cancelled context before consuming the semaphore
	// token (Go's select is non-deterministic when multiple cases are ready).
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

	// Ensure the process is running and healthy; start or restart if needed.
	proc, fresh, err := b.ensureProcess(streamCtx, cfg)
	if err != nil {
		b.turnCh <- struct{}{}
		cancelTimeout()
		return nil, fmt.Errorf("llmcli codex-appserver: ensure process: %w", err)
	}

	// On a fresh process, run the initialize/thread/start handshake to obtain a
	// threadID. This is synchronous and must complete before Stream returns so
	// that sessionID is available for the start event.
	if fresh {
		if err := b.runHandshake(streamCtx, proc, cfg); err != nil {
			b.restartAfterProtocolError(err)
			b.turnCh <- struct{}{}
			cancelTimeout()
			return nil, fmt.Errorf("llmcli codex-appserver: handshake: %w", err)
		}
	}

	sessionID := proc.threadID
	if cfg.SessionID != "" {
		sessionID = cfg.SessionID
	}
	fidelity := codexAppServerFidelity(cfg)

	parseFn := func(ctx context.Context, out chan<- llmclient.Event, te *terminalEmitter) error {
		id := b.nextID.Add(1)
		prompt := llmclient.LastUserMessage(input)

		turnParams := codexTurnStartParams{
			ThreadID: proc.threadID,
			Input: []codexTurnInput{
				{
					Type:         "text",
					Text:         prompt,
					TextElements: []any{},
				},
			},
		}
		if cfg.Model != "" {
			turnParams.Model = cfg.Model
		}

		req := codexRPCRequest{
			JSONRPC: "2.0",
			ID:      id,
			Method:  "turn/start",
			Params:  turnParams,
		}
		if err := internal.WriteLine(proc.stdin, req); err != nil {
			b.restartAfterProtocolError(err)
			return fmt.Errorf("write turn/start: %w", err)
		}

		// Watch for context cancellation and terminate the process so that the
		// blocking ReadLine call inside readTurnEvents unblocks via EOF.
		cancelWatchDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				b.restartAfterProtocolError(ctx.Err())
			case <-cancelWatchDone:
			}
		}()
		defer close(cancelWatchDone)

		b.readTurnEvents(ctx, proc, out, te)
		return nil
	}

	return structuredStream(
		streamCtx,
		b.Name(),
		sessionID,
		cfg.Model,
		fidelity,
		parseFn,
		func() {
			cancelTimeout()
			b.turnCh <- struct{}{}
		},
	), nil
}

// runHandshake executes the per-process initialize → initialized → thread/start
// sequence, capturing the threadID from the thread/started notification.
// Called exactly once per fresh process, while holding the turn semaphore.
func (b *CodexAppServerBackend) runHandshake(ctx context.Context, proc *codexProcess, cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig value avoids mutation.
	// 1. Send initialize request (id=1).
	initReq := codexRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: codexInitializeParams{
			ClientInfo: codexClientInfo{
				Name:    "fabrikk",
				Title:   nil,
				Version: "1.0.0",
			},
			Capabilities: nil,
		},
	}
	if err := internal.WriteLine(proc.stdin, initReq); err != nil {
		return fmt.Errorf("write initialize: %w", err)
	}

	// 2. Read initialize response (expect a result frame with our id).
	if err := b.readUntilResponse(ctx, proc, 1); err != nil {
		return fmt.Errorf("read initialize response: %w", err)
	}

	// 3. Send initialized notification (no id).
	initedNotif := codexRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}
	if err := internal.WriteLine(proc.stdin, initedNotif); err != nil {
		return fmt.Errorf("write initialized notification: %w", err)
	}

	// 4. Send thread/start request (id=2).
	threadStartParams := codexThreadStartParams{}
	if cfg.Model != "" {
		threadStartParams.Model = cfg.Model
	}
	threadStartReq := codexRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "thread/start",
		Params:  threadStartParams,
	}
	if err := internal.WriteLine(proc.stdin, threadStartReq); err != nil {
		return fmt.Errorf("write thread/start: %w", err)
	}

	// 5. Read frames until we see the thread/started notification; capture threadID.
	threadID, err := b.readUntilThreadStarted(ctx, proc)
	if err != nil {
		return fmt.Errorf("read thread/started: %w", err)
	}

	proc.threadID = threadID
	return nil
}

// readUntilResponse reads NDJSON frames until it sees a response frame with
// the given id (or a JSON-RPC error response). Notifications are skipped.
func (b *CodexAppServerBackend) readUntilResponse(ctx context.Context, proc *codexProcess, wantID int64) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := internal.ReadLine(proc.stdout, maxCodexBodyBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return fmt.Errorf("read frame: %w", err)
		}
		var frame codexRPCFrame
		if jsonErr := json.Unmarshal(line, &frame); jsonErr != nil {
			return fmt.Errorf("parse frame: %w", jsonErr)
		}
		if frame.Error != nil {
			return fmt.Errorf("RPC error %d: %s", frame.Error.Code, frame.Error.Message)
		}
		// Skip notifications (no ID).
		if len(frame.ID) == 0 || string(frame.ID) == "null" {
			continue
		}
		// Check if this is the response we want.
		var gotID int64
		if jsonErr := json.Unmarshal(frame.ID, &gotID); jsonErr == nil && gotID == wantID {
			return nil
		}
	}
}

// readUntilThreadStarted reads frames until the "thread/started" notification
// arrives and returns the thread ID from params.thread.id.
func (b *CodexAppServerBackend) readUntilThreadStarted(ctx context.Context, proc *codexProcess) (string, error) {
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		line, err := internal.ReadLine(proc.stdout, maxCodexBodyBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", io.ErrUnexpectedEOF
			}
			return "", fmt.Errorf("read frame: %w", err)
		}
		var frame codexRPCFrame
		if jsonErr := json.Unmarshal(line, &frame); jsonErr != nil {
			return "", fmt.Errorf("parse frame: %w", jsonErr)
		}
		if frame.Error != nil {
			return "", fmt.Errorf("RPC error %d: %s", frame.Error.Code, frame.Error.Message)
		}
		if frame.Method == "thread/started" {
			var p codexThreadStartedParams
			if jsonErr := json.Unmarshal(frame.Params, &p); jsonErr != nil {
				return "", fmt.Errorf("parse thread/started params: %w", jsonErr)
			}
			if p.Thread.ID == "" {
				return "", fmt.Errorf("thread/started: empty thread id")
			}
			return p.Thread.ID, nil
		}
		// Skip other notifications/responses during handshake.
	}
}

// ensureProcess returns the current healthy process (and fresh=false), or
// starts a fresh one (and fresh=true) if none is running. Caller must not hold
// procMu.
func (b *CodexAppServerBackend) ensureProcess(ctx context.Context, cfg llmclient.RequestConfig) (*codexProcess, bool, error) { //nolint:gocritic // RequestConfig value avoids mutation across persistent process setup.
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
		return b.proc, false, nil
	}

	// Tear down any stale process before starting fresh.
	if b.proc != nil {
		b.proc.terminate(nil)
		b.proc.drain()
		b.proc = nil
	}

	proc, err := b.procFactory(ctx, env)
	if err != nil {
		return nil, false, err
	}

	b.proc = proc
	b.healthy = true
	b.routingKey = routingKey

	return proc, true, nil
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

// readTurnEvents reads NDJSON notification frames from proc.stdout, mapping
// each to a normalized llmclient.Event, until a terminal "turn/completed"
// notification arrives or an error occurs.
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

	// itemIndex maps itemId → contentIndex for tracking per-item content blocks.
	itemIndex := make(map[string]int)
	nextIndex := 0

	for {
		if ctx.Err() != nil {
			b.restartAfterProtocolError(ctx.Err())
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return
		}

		line, err := internal.ReadLine(proc.stdout, maxCodexBodyBytes)
		if err != nil {
			if ctx.Err() != nil {
				b.restartAfterProtocolError(ctx.Err())
				te.done(ctx, nil, nil, llmclient.StopCancelled)
				return
			}
			if errors.Is(err, io.EOF) {
				// Process closed stdout without a turn/completed notification. Treat
				// as end-of-turn, but reset the process reference so the next turn
				// spawns a fresh process rather than reusing the now-dead one.
				b.restartAfterProtocolError(io.EOF)
				te.done(ctx, assembledMsg, nil, llmclient.StopEndTurn)
				return
			}

			protocolErr := fmt.Errorf("llmcli codex-appserver: read frame: %w", err)
			b.restartAfterProtocolError(protocolErr)
			te.error(ctx, protocolErr)
			return
		}

		var frame codexRPCFrame
		if jsonErr := json.Unmarshal(line, &frame); jsonErr != nil {
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

		done := b.dispatchCodexFrame(ctx, frame, ch, te, &nextIndex, itemIndex, assembledMsg)
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
	nextIndex *int,
	itemIndex map[string]int,
	assembledMsg *llmclient.AssistantMessage,
) (done bool) {
	if frame.Method == "" {
		return false
	}
	switch frame.Method {
	case "thread/started", "turn/started":
		return false
	case "item/agentMessage/delta":
		return b.handleAgentMessageDelta(ctx, frame, ch, te, nextIndex, itemIndex)
	case "item/started":
		return b.handleItemStarted(ctx, frame, ch, te, nextIndex, itemIndex)
	case "item/completed":
		return b.handleItemCompleted(ctx, frame, ch, te, nextIndex, itemIndex, assembledMsg)
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		return b.handleReasoningDelta(ctx, frame, ch, te)
	case "turn/completed":
		if assembledMsg != nil {
			assembledMsg.StopReason = llmclient.StopEndTurn
		}
		te.done(ctx, assembledMsg, nil, llmclient.StopEndTurn)
		return true
	case "error":
		return b.handleErrorNotification(ctx, frame, te)
	}
	return false
}

func (b *CodexAppServerBackend) handleAgentMessageDelta(
	ctx context.Context,
	frame codexRPCFrame,
	ch chan<- llmclient.Event,
	te *terminalEmitter,
	nextIndex *int,
	itemIndex map[string]int,
) bool {
	var p codexAgentMessageDeltaParams
	if len(frame.Params) > 0 {
		if err := json.Unmarshal(frame.Params, &p); err != nil {
			return false
		}
	}
	idx, seen := itemIndex[p.ItemID]
	if !seen {
		idx = *nextIndex
		itemIndex[p.ItemID] = idx
		*nextIndex++
		if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextStart, ContentIndex: idx}) {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return true
		}
	}
	if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextDelta, ContentIndex: idx, Delta: p.Delta}) {
		te.done(ctx, nil, nil, llmclient.StopCancelled)
		return true
	}
	return false
}

// parseToolArguments unmarshals raw JSON arguments into a map. Returns an empty
// map if raw is nil or invalid JSON — forward-compat: bad args are tolerated.
func parseToolArguments(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// toolCallName returns the canonical tool name for a codexThreadItem, following
// the same mapping used by handleItemStarted.
func toolCallName(item codexThreadItem) string {
	switch item.Type {
	case codexItemTypeDynamicToolCall:
		return item.Tool
	case codexItemTypeMCPToolCall:
		if item.Server != "" {
			return item.Server + "/" + item.Tool
		}
		return item.Tool
	case codexItemTypeCommandExecution:
		return codexItemTypeCommandExecution
	default:
		return ""
	}
}

// isToolCallType reports whether the ThreadItem type is a tool-call variant
// that maps to EventToolCallStart / EventToolCallEnd.
func isToolCallType(t string) bool {
	return t == codexItemTypeDynamicToolCall || t == codexItemTypeMCPToolCall || t == codexItemTypeCommandExecution
}

func (b *CodexAppServerBackend) handleItemStarted(
	ctx context.Context,
	frame codexRPCFrame,
	ch chan<- llmclient.Event,
	te *terminalEmitter,
	nextIndex *int,
	itemIndex map[string]int,
) bool {
	var p codexItemStartedParams
	if len(frame.Params) > 0 {
		if err := json.Unmarshal(frame.Params, &p); err != nil {
			return false
		}
	}
	if !isToolCallType(p.Item.Type) {
		// Unknown item type — skip silently for forward compatibility.
		return false
	}

	idx, seen := itemIndex[p.Item.ID]
	if !seen {
		idx = *nextIndex
		itemIndex[p.Item.ID] = idx
		*nextIndex++
	}

	var args map[string]any
	if p.Item.Type == codexItemTypeCommandExecution {
		args = map[string]any{"command": p.Item.Command}
	} else {
		args = parseToolArguments(p.Item.Arguments)
	}

	name := toolCallName(p.Item)
	tc := &llmclient.ToolCall{
		ID:        p.Item.ID,
		Name:      name,
		Arguments: args,
	}
	if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventToolCallStart, ContentIndex: idx, ToolCall: tc}) {
		te.done(ctx, nil, nil, llmclient.StopCancelled)
		return true
	}
	return false
}

func (b *CodexAppServerBackend) handleItemCompleted(
	ctx context.Context,
	frame codexRPCFrame,
	ch chan<- llmclient.Event,
	te *terminalEmitter,
	nextIndex *int,
	itemIndex map[string]int,
	assembledMsg *llmclient.AssistantMessage,
) bool {
	var p codexItemCompletedParams
	if len(frame.Params) > 0 {
		if err := json.Unmarshal(frame.Params, &p); err != nil {
			return false
		}
	}

	switch {
	case p.Item.Type == "agentMessage":
		idx, seen := itemIndex[p.Item.ID]
		if !seen {
			idx = *nextIndex
			itemIndex[p.Item.ID] = idx
			*nextIndex++
		}
		if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventTextEnd, ContentIndex: idx, Content: p.Item.Text}) {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return true
		}
		if assembledMsg != nil {
			assembledMsg.Content = append(assembledMsg.Content, llmclient.ContentBlock{
				Type: llmclient.ContentText,
				Text: p.Item.Text,
			})
		}

	case isToolCallType(p.Item.Type):
		idx, seen := itemIndex[p.Item.ID]
		if !seen {
			idx = *nextIndex
			itemIndex[p.Item.ID] = idx
			*nextIndex++
		}
		var args map[string]any
		if p.Item.Type == codexItemTypeCommandExecution {
			args = map[string]any{"command": p.Item.Command}
		} else {
			args = parseToolArguments(p.Item.Arguments)
		}
		tc := &llmclient.ToolCall{
			ID:        p.Item.ID,
			Name:      toolCallName(p.Item),
			Arguments: args,
		}
		if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventToolCallEnd, ContentIndex: idx, ToolCall: tc}) {
			te.done(ctx, nil, nil, llmclient.StopCancelled)
			return true
		}
	}

	return false
}

func (b *CodexAppServerBackend) handleReasoningDelta(
	ctx context.Context,
	frame codexRPCFrame,
	ch chan<- llmclient.Event,
	te *terminalEmitter,
) bool {
	var p codexReasoningDeltaParams
	if len(frame.Params) > 0 {
		if err := json.Unmarshal(frame.Params, &p); err != nil {
			return false
		}
	}
	if !emit(ctx, ch, llmclient.Event{Type: llmclient.EventThinkingDelta, ContentIndex: p.ContentIndex, Delta: p.Delta}) {
		te.done(ctx, nil, nil, llmclient.StopCancelled)
		return true
	}
	return false
}

func (b *CodexAppServerBackend) handleErrorNotification(
	ctx context.Context,
	frame codexRPCFrame,
	te *terminalEmitter,
) bool {
	var p codexErrorParams
	if len(frame.Params) > 0 {
		_ = json.Unmarshal(frame.Params, &p)
	}
	msg := p.Error.Message
	if msg == "" {
		msg = "codex app-server: unknown error"
	}
	protocolErr := fmt.Errorf("llmcli codex-appserver: %s", msg)
	b.restartAfterProtocolError(protocolErr)
	te.error(ctx, protocolErr)
	return true
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

	return populateJSONSchemaFidelity(&llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructured,
		ToolControl:   llmclient.ToolControlBuiltIn,
		OptionResults: optionResults,
	}, cfg)
}

// codexAppServerStaticCapabilities returns the static capabilities for the
// Codex app-server backend. version is the detected binary version string.
func codexAppServerStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "codex-appserver",
		Version:       version,
		Streaming:     llmclient.StreamingStructured,
		ToolEvents:    true,
		Thinking:      true,
		MultiTurn:     true,
		OllamaRouting: true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:              llmclient.OptionSupportFull,
			llmclient.OptionSession:            llmclient.OptionSupportFull,
			llmclient.OptionOllama:             llmclient.OptionSupportFull,
			llmclient.OptionEnvironment:        llmclient.OptionSupportFull,
			llmclient.OptionEnvironmentOverlay: llmclient.OptionSupportFull,
			llmclient.OptionTimeout:            llmclient.OptionSupportFull,
			llmclient.OptionJSONSchema:         llmclient.OptionSupportPartial,
		},
	}
}
