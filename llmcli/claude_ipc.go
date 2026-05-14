package llmcli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

const (
	claudeIPCInitTimeoutDefault = 10 * time.Second
	claudeIPCInitTimeoutEnv     = "FABRIKK_CLAUDE_IPC_INIT_TIMEOUT"
)

// ClaudeIPCBackend is the persistent Claude CLI backend. It keeps one
// long-lived `claude --input-format stream-json` process alive per routing
// key, amortizing process startup cost (100–500ms) across turns in a session.
//
// At most one turn runs at a time: concurrent Stream() calls block on
// proc.turnMu until the previous turn's terminal event is emitted. Mid-turn
// cancellation corrupts stdout parse state and triggers a process restart
// (new session — prior conversation history is lost).
//
// ClaudeIPCBackend implements [llmclient.Backend].
type ClaudeIPCBackend struct {
	CliBackend
	mu         sync.Mutex
	closed     bool
	proc       *claudeIPCProc
	routingKey string
	// procFactory replaces startSupervisedWithStdin in tests.
	procFactory func(ctx context.Context, spec processSpecWithStdin) (*supervisor, error)
}

// claudeIPCProc owns the lifecycle of a running persistent claude process.
type claudeIPCProc struct {
	sup       *supervisor
	stdinW    io.WriteCloser
	enc       *json.Encoder
	sessionID string
	cancel    context.CancelFunc // cancels procCtx (child of Background())
	turnMu    sync.Mutex         // serializes concurrent turns
}

// claudeIPCUserMessage is the JSONL turn request written to claude stdin.
type claudeIPCUserMessage struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

func (p *claudeIPCProc) alive() bool {
	select {
	case <-p.sup.done:
		return false
	default:
		return true
	}
}

// NewClaudeIPCBackend constructs a ClaudeIPCBackend from the detected CliInfo.
func NewClaudeIPCBackend(info CliInfo) *ClaudeIPCBackend {
	return &ClaudeIPCBackend{
		CliBackend:  NewCliBackend("claude-ipc", info),
		procFactory: startSupervisedWithStdin,
	}
}

// Capabilities returns the static capabilities for this backend instance.
func (b *ClaudeIPCBackend) Capabilities() llmclient.Capabilities {
	return claudeIPCStaticCapabilities(b.info.Version)
}

// Ready checks that the claude binary is present and authenticated. It does
// not probe the running process — liveness is checked lazily in Stream().
func (b *ClaudeIPCBackend) Ready(_ context.Context) llmclient.ReadyReport {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()

	if closed {
		return llmclient.ReadyReport{State: llmclient.ReadyUnknown, Detail: "claude IPC backend has been closed"}
	}
	if !b.binaryAvailable() {
		return readyMissingBinary(b.Name(), b.info.Path)
	}
	if anyPathExists(homePath(".claude.json"), homePath(".claude")) {
		return llmclient.ReadyReport{State: llmclient.ReadyOK}
	}
	return readyNotAuthed(b.Name(), "run `claude /login`")
}

// Available reports whether claude is installed and authenticated.
func (b *ClaudeIPCBackend) Available() bool {
	return observeAvailability(b.Name(), b.Ready(context.Background()).State == llmclient.ReadyOK)
}

// Close terminates the persistent process and releases all resources.
// Safe to call multiple times; subsequent calls are no-ops.
func (b *ClaudeIPCBackend) Close() error {
	b.mu.Lock()
	b.closed = true
	proc := b.proc
	b.proc = nil
	b.mu.Unlock()

	if proc == nil {
		return nil
	}
	proc.cancel()
	_ = proc.stdinW.Close()
	// Hold turnMu while draining to avoid concurrent reads on proc.sup.Stdout
	// with any active turn goroutine that may still be reading from it.
	proc.turnMu.Lock()
	_, _ = io.Copy(io.Discard, proc.sup.Stdout)
	proc.turnMu.Unlock()
	return proc.sup.wait()
}

// Stream sends the last user message to the persistent claude process and
// returns a channel of normalized events for this turn. Concurrent calls
// block on the per-process turn mutex until the previous turn completes.
//
// Mid-turn cancellation restarts the process; the next Stream() call begins
// a fresh session with no memory of prior turns.
func (b *ClaudeIPCBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	if err := checkClaudeIPCRequiredOptions(cfg); err != nil {
		cancelTimeout()
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	proc, err := b.ensureProcess(cfg, input)
	if err != nil {
		cancelTimeout()
		return nil, fmt.Errorf("llmcli claude-ipc: start process: %w", err)
	}

	// Serialize turns: acquire per-process turn lock before sending anything.
	proc.turnMu.Lock()

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	go func() {
		defer cancelTimeout()
		defer te.close() // safety guard; real terminal comes from parser

		restarted := false
		defer func() {
			if !restarted {
				proc.turnMu.Unlock()
			}
		}()

		var msg claudeIPCUserMessage
		msg.Type = "user"
		msg.Message.Role = "user"
		msg.Message.Content = llmclient.LastUserMessage(input)
		if encErr := proc.enc.Encode(msg); encErr != nil {
			proc.turnMu.Unlock()
			restarted = true
			b.restartAfterProtocolError()
			te.error(streamCtx, fmt.Errorf("claude-ipc: send: %w", encErr))
			return
		}

		fidelity := claudeIPCInitFidelity(cfg)
		parseErr := parseClaudeIPCTurn(streamCtx, proc.sup.Stdout, ch, te, fidelity, proc.sessionID)

		switch {
		case parseErr != nil &&
			!errors.Is(parseErr, context.Canceled) &&
			!errors.Is(parseErr, context.DeadlineExceeded):
			proc.turnMu.Unlock()
			restarted = true
			b.restartAfterProtocolError()
			te.error(ctx, fmt.Errorf("claude-ipc: parse: %w", parseErr))
		case streamCtx.Err() != nil:
			proc.turnMu.Unlock()
			restarted = true
			b.restartAfterProtocolError()
			te.done(streamCtx, nil, nil, llmclient.StopCancelled)
		default:
			// Normal completion — parseClaudeIPCTurn emitted done via te.
		}
	}()

	// nosemgrep: trailofbits.go.missing-unlock-before-return.missing-unlock-before-return -- proc.turnMu is intentionally released in the stream goroutine after the terminal event is emitted.
	return observeStream(b.Name(), model, started, ch), nil
}

// ensureProcess returns the running proc for the given configuration, starting
// a new process when none exists or the routing key has changed.
func (b *ClaudeIPCBackend) ensureProcess(cfg llmclient.RequestConfig, input *llmclient.Context) (*claudeIPCProc, error) { //nolint:gocritic // RequestConfig value keeps helper consistent with other option helpers.
	key := claudeIPCRoutingKey(cfg, input)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("backend is closed")
	}
	if b.proc != nil && b.proc.alive() && key == b.routingKey {
		proc := b.proc
		b.mu.Unlock()
		return proc, nil
	}
	// Stale or no process — tear down while holding the lock to avoid races
	// with concurrent Stream() calls that might also be trying to start.
	old := b.proc
	b.proc = nil
	b.routingKey = ""
	b.mu.Unlock()

	if old != nil {
		old.cancel()
		_ = old.stdinW.Close()
		// Hold turnMu while draining to avoid concurrent reads on old.sup.Stdout
		// with any active turn goroutine that may still be reading from it.
		old.turnMu.Lock()
		_, _ = io.Copy(io.Discard, old.sup.Stdout)
		old.turnMu.Unlock()
		_ = old.sup.wait()
	}

	proc, err := b.startClaudeIPCProcess(cfg, input)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		proc.cancel()
		_ = proc.stdinW.Close()
		_, _ = io.Copy(io.Discard, proc.sup.Stdout)
		_ = proc.sup.wait()
		return nil, fmt.Errorf("backend was closed during process start")
	}
	b.proc = proc
	b.routingKey = key
	b.mu.Unlock()

	return proc, nil
}

// restartAfterProtocolError clears the current proc without a graceful
// shutdown. Called after stdout parse errors or mid-turn context cancellation,
// both of which leave the output stream in an unrecoverable state.
func (b *ClaudeIPCBackend) restartAfterProtocolError() {
	b.mu.Lock()
	proc := b.proc
	b.proc = nil
	b.routingKey = ""
	b.mu.Unlock()

	if proc == nil {
		return
	}
	proc.cancel()
	_ = proc.stdinW.Close()
	_, _ = io.Copy(io.Discard, proc.sup.Stdout)
	_ = proc.sup.wait()
}

// startClaudeIPCProcess spawns the persistent claude subprocess and waits for
// the system/init frame. procCtx is always derived from context.Background()
// so the process is not killed when a per-turn Stream context expires.
func (b *ClaudeIPCBackend) startClaudeIPCProcess(cfg llmclient.RequestConfig, input *llmclient.Context) (*claudeIPCProc, error) { //nolint:gocritic // RequestConfig value keeps helper consistent with other option helpers.
	procCtx, cancel := context.WithCancel(context.Background())

	stdinR, stdinW := io.Pipe()

	spec := processSpecWithStdin{
		processSpec: processSpec{
			Command:    b.info.Path,
			Args:       buildClaudeIPCArgs(cfg, input),
			Env:        resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg)),
			Dir:        cfg.WorkingDirectory,
			RawCapture: cfg.RawCapture,
		},
		stdin: stdinR,
	}

	spawnFn := b.procFactory
	if spawnFn == nil {
		spawnFn = startSupervisedWithStdin
	}

	sup, err := spawnFn(procCtx, spec)
	if err != nil {
		cancel()
		_ = stdinW.Close()
		return nil, fmt.Errorf("start subprocess: %w", err)
	}

	proc := &claudeIPCProc{
		sup:    sup,
		stdinW: stdinW,
		enc:    json.NewEncoder(stdinW),
		cancel: cancel,
	}

	initTimeout := claudeIPCInitTimeout()
	initCtx, cancelInit := context.WithTimeout(procCtx, initTimeout)
	defer cancelInit()

	// Kill the process if initCtx expires so that the blocking ReadBoundedLine
	// call in waitForClaudeIPCReady unblocks via EOF rather than hanging.
	stopKill := make(chan struct{})
	go func() {
		select {
		case <-initCtx.Done():
			cancel() // cancel procCtx → supervisor terminates the process
		case <-stopKill:
		}
	}()

	sessionID, err := waitForClaudeIPCReady(initCtx, sup.Stdout)
	close(stopKill) // stop the kill goroutine; no-op if it already fired

	if err != nil {
		cancel()
		_ = stdinW.Close()
		_, _ = io.Copy(io.Discard, sup.Stdout)
		_ = sup.wait()
		return nil, fmt.Errorf("init probe: %w", err)
	}
	proc.sessionID = sessionID
	return proc, nil
}

// claudeIPCInitTimeout returns the startup probe deadline from
// FABRIKK_CLAUDE_IPC_INIT_TIMEOUT or the 10-second default.
func claudeIPCInitTimeout() time.Duration {
	if v := os.Getenv(claudeIPCInitTimeoutEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return claudeIPCInitTimeoutDefault
}

// waitForClaudeIPCReady reads stdout until the system/init frame arrives and
// returns the session_id it carries. Returns an error if the stream closes
// before the init frame arrives.
//
// ctx is checked after each read error to distinguish a timeout-driven process
// kill (ctx.Err() non-nil) from an unexpected EOF. The caller is responsible
// for killing the process when ctx expires so that the blocking
// ReadBoundedLine call unblocks via EOF; see startClaudeIPCProcess.
func waitForClaudeIPCReady(ctx context.Context, r *bufio.Reader) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line, err := internal.ReadBoundedLine(r, maxClaudeLineBytes)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return "", io.ErrUnexpectedEOF
			}
			if errors.Is(err, internal.ErrLineTooLong) {
				continue // oversized line — skip and keep scanning
			}
			return "", err
		}
		if len(line) == 0 {
			continue
		}
		var frame claudeFrame
		if json.Unmarshal(line, &frame) != nil {
			continue
		}
		if frame.Type == "system" && frame.Subtype == "init" {
			return frame.SessionID, nil
		}
	}
}

// parseClaudeIPCTurn pre-emits EventStart with the cached sessionID then
// delegates to parseClaudeStreamFromState with pre-initialized state. Used for
// ALL IPC turns because the init frame is consumed once at startup by
// waitForClaudeIPCReady — no init frame arrives between turns.
func parseClaudeIPCTurn(
	ctx context.Context,
	r *bufio.Reader,
	out chan<- llmclient.Event,
	te *terminalEmitter,
	fidelity *llmclient.Fidelity,
	sessionID string,
) error {
	if !emit(ctx, out, startEvent(sessionID, fidelity)) {
		return ctx.Err()
	}
	state := &claudeParseState{
		startEmitted:  true,
		sessionID:     sessionID,
		startFidelity: fidelity,
		assembledMsg:  &llmclient.AssistantMessage{Role: "assistant"},
	}
	return parseClaudeStreamFromState(ctx, r, out, te, state)
}

// buildClaudeIPCArgs constructs the argument list for the persistent process.
func buildClaudeIPCArgs(cfg llmclient.RequestConfig, input *llmclient.Context) []string { //nolint:gocritic // RequestConfig value keeps helper consistent with other option helpers.
	// -p "" triggers non-interactive mode; --input-format stream-json keeps
	// the process alive to accept subsequent turn messages over stdin.
	args := []string{
		"-p", "",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--no-session-persistence",
	}

	if input != nil && input.SystemPrompt != "" {
		args = append(args, "--system-prompt", input.SystemPrompt)
	}

	model := cfg.Model
	if cfg.Ollama != nil && cfg.Ollama.Model != "" {
		model = cfg.Ollama.Model
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	return args
}

// claudeIPCRoutingKey returns a stable key identifying the process
// configuration. A key change in ensureProcess triggers a process restart.
func claudeIPCRoutingKey(cfg llmclient.RequestConfig, input *llmclient.Context) string { //nolint:gocritic // RequestConfig value keeps helper consistent with other option helpers.
	systemPrompt := ""
	if input != nil {
		systemPrompt = input.SystemPrompt
	}
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(systemPrompt)))[:8]
	ollama := ""
	if cfg.Ollama != nil {
		ollama = ollamaEffectiveBaseURL(*cfg.Ollama) + ":" + cfg.Ollama.Model
	}
	return cfg.Model + "\x00" + h + "\x00" + cfg.WorkingDirectory + "\x00" + ollama
}

// claudeIPCInitFidelity returns the Fidelity for an IPC turn.
func claudeIPCInitFidelity(cfg llmclient.RequestConfig) *llmclient.Fidelity { //nolint:gocritic // RequestConfig value keeps helper consistent with other option helpers.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		results[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		results[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	optionResults := mergeOptionResults(results, executionOptionResults(cfg, claudeIPCStaticCapabilities("")))
	return populateJSONSchemaFidelity(&llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructured,
		ToolControl:   llmclient.ToolControlBuiltIn,
		OptionResults: optionResults,
	}, cfg)
}

// checkClaudeIPCRequiredOptions returns ErrUnsupportedOption for required
// options not supported by this backend.
func checkClaudeIPCRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig value keeps helper consistent with other option helpers.
	return llmclient.EnforceRequired(cfg, claudeIPCStaticCapabilities(""))
}

// claudeIPCStaticCapabilities returns the static capabilities for the
// claude-ipc backend.
func claudeIPCStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "claude-ipc",
		Version:       version,
		Streaming:     llmclient.StreamingStructured,
		ToolEvents:    true,
		MultiTurn:     true,
		Thinking:      true,
		Usage:         true,
		OllamaRouting: true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:              llmclient.OptionSupportFull,
			llmclient.OptionSession:            llmclient.OptionSupportNone,
			llmclient.OptionOllama:             llmclient.OptionSupportFull,
			llmclient.OptionWorkingDirectory:   llmclient.OptionSupportFull,
			llmclient.OptionEnvironment:        llmclient.OptionSupportFull,
			llmclient.OptionEnvironmentOverlay: llmclient.OptionSupportFull,
			llmclient.OptionTimeout:            llmclient.OptionSupportFull,
			llmclient.OptionJSONSchema:         llmclient.OptionSupportPartial,
			llmclient.OptionRawCapture:         llmclient.OptionSupportFull,
		},
	}
}
