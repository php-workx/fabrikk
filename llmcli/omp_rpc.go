package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

// OmpRPCBackend is the persistent omp RPC backend. It starts a single
// `omp --mode rpc` process and serializes turns over a bidirectional JSONL
// channel (stdin/stdout). It is the only backend that satisfies host tool
// definition, host tool approval, and tool result injection requirements.
//
// OmpRPCBackend implements [llmclient.Backend].
type OmpRPCBackend struct {
	CliBackend

	mu         sync.Mutex // protects proc
	proc       *ompRPCProc
	routingKey string
}

// ompRPCProc owns the lifecycle of a running `omp --mode rpc` process.
type ompRPCProc struct {
	sup       *supervisor
	stdinW    io.WriteCloser // write end of the process stdin pipe
	enc       *json.Encoder  // JSONL command encoder for stdinW
	sessionID string
	cancel    context.CancelFunc
	turnMu    sync.Mutex // serializes concurrent turns
}

// ompRPCGenericCmd is the envelope for simple single-field RPC commands.
type ompRPCGenericCmd struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Model string `json:"model,omitempty"`
}

// ompRPCHostToolsCmd carries the set_host_tools command payload.
type ompRPCHostToolsCmd struct {
	Type  string           `json:"type"`
	Tools []llmclient.Tool `json:"tools"`
}

// NewOmpRPCBackend constructs an OmpRPCBackend from the detected CliInfo.
func NewOmpRPCBackend(info CliInfo) *OmpRPCBackend {
	return &OmpRPCBackend{
		CliBackend: NewCliBackend("omp-rpc", info),
	}
}

// Capabilities returns the static capabilities for the omp RPC backend.
// Only omp-rpc declares HostToolDefs, HostToolApproval, and ToolResultInjection.
func (b *OmpRPCBackend) Capabilities() llmclient.Capabilities {
	return ompRPCStaticCapabilities(b.info.Version)
}

// Available reports whether the backend can accept requests. For a not-yet-
// started backend it checks binary existence; for a running process it also
// verifies the process has not exited.
func (b *OmpRPCBackend) Available() bool {
	if !b.binaryAvailable() {
		return observeAvailability(b.Name(), false)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.proc == nil {
		return observeAvailability(b.Name(), true) // not yet started; binary is present
	}
	// Non-blocking check: if proc.sup.done is closed the process exited.
	select {
	case <-b.proc.sup.done:
		return observeAvailability(b.Name(), false)
	default:
		return observeAvailability(b.Name(), true)
	}
}

// Close terminates the omp RPC process and releases all resources. Safe to
// call multiple times; subsequent calls are no-ops.
func (b *OmpRPCBackend) Close() error {
	b.mu.Lock()
	proc := b.proc
	b.proc = nil
	b.mu.Unlock()

	if proc == nil {
		return nil
	}
	proc.cancel()
	_ = proc.stdinW.Close()
	_, _ = io.Copy(io.Discard, proc.sup.Stdout)
	_ = proc.sup.wait()
	return nil
}

// Stream sends a prompt to the omp RPC process and returns a channel of
// normalized events for this turn. Turns are strictly serialized: concurrent
// Stream calls block until the previous turn's terminal event has been emitted
// and the turn lock released.
func (b *OmpRPCBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)

	if err := checkOmpRPCRequiredOptions(cfg); err != nil {
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	proc, err := b.ensureStarted(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("llmcli omp-rpc: start process: %w", err)
	}

	// Serialize turns: acquire the per-process turn lock before sending anything.
	proc.turnMu.Lock()

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	// Pre-turn commands (model and host tools) before sending the prompt.
	if cfg.Model != "" {
		if err := sendSetModel(proc, cfg.Model); err != nil {
			proc.turnMu.Unlock()
			te.close()
			return nil, fmt.Errorf("llmcli omp-rpc: set_model: %w", err)
		}
	}
	if len(cfg.HostTools) > 0 {
		if err := sendSetHostTools(proc, cfg.HostTools); err != nil {
			proc.turnMu.Unlock()
			te.close()
			return nil, fmt.Errorf("llmcli omp-rpc: set_host_tools: %w", err)
		}
	}

	// Emit the start event before spawning the parser goroutine so that the
	// session ID is available to the caller before any streaming events arrive.
	fidelity := ompRPCFidelity(cfg)
	if !emit(ctx, ch, startEvent(proc.sessionID, fidelity)) {
		proc.turnMu.Unlock()
		te.close()
		return ch, nil
	}

	// Send the prompt command to trigger the turn.
	prompt := lastUserMessage(input)
	if err := sendPrompt(proc, prompt); err != nil {
		proc.turnMu.Unlock()
		te.error(ctx, fmt.Errorf("llmcli omp-rpc: send prompt: %w", err))
		return ch, nil
	}

	go func() {
		defer proc.turnMu.Unlock()
		defer te.close() // safety guard; real terminal comes from parser

		parseErr := parseOmpRPCTurn(ctx, proc.sup.Stdout, ch, te)

		switch {
		case parseErr != nil &&
			!errors.Is(parseErr, context.Canceled) &&
			!errors.Is(parseErr, context.DeadlineExceeded):
			te.error(ctx, fmt.Errorf("llmcli omp-rpc: parse: %w", parseErr))
		case ctx.Err() != nil:
			_ = sendAbort(proc) // best-effort; ignore send error
			te.done(ctx, nil, nil, llmclient.StopCancelled)
		default:
			// Normal — parseOmpRPCTurn already emitted done via te.
		}
	}()

	// nosemgrep: trailofbits.go.missing-unlock-before-return.missing-unlock-before-return -- proc.turnMu is intentionally released in the stream goroutine after the terminal event is emitted.
	return observeStream(b.Name(), model, started, ch), nil
}

// ensureStarted returns the running proc, starting the omp process if needed.
func (b *OmpRPCBackend) ensureStarted(ctx context.Context, cfg llmclient.RequestConfig) (*ompRPCProc, error) { //nolint:gocritic // RequestConfig value avoids mutation across persistent process setup.
	b.mu.Lock()
	defer b.mu.Unlock()
	routingKey := "default"
	if cfg.Ollama != nil {
		routingKey = ollamaEffectiveBaseURL(*cfg.Ollama) + "|" + cfg.Ollama.APIKey
	}
	if b.proc != nil && b.routingKey == routingKey {
		return b.proc, nil
	}
	if b.proc != nil {
		b.proc.cancel()
		_ = b.proc.stdinW.Close()
		_, _ = io.Copy(io.Discard, b.proc.sup.Stdout)
		_ = b.proc.sup.wait()
		b.proc = nil
	}
	proc, err := startOmpRPCProcess(ctx, b.info.Path, cfg.Ollama)
	if err != nil {
		return nil, err
	}
	b.proc = proc
	b.routingKey = routingKey
	return proc, nil
}

// startOmpRPCProcess launches `omp --mode rpc` and waits for the initial
// "ready" event that confirms the process is initialized and accepting commands.
func startOmpRPCProcess(ctx context.Context, path string, ollama *llmclient.OllamaConfig) (*ompRPCProc, error) {
	// Derive a child context so Close() can cancel the lifecycle goroutine.
	procCtx, cancel := context.WithCancel(ctx)

	stdinR, stdinW := io.Pipe()

	spec := processSpecWithStdin{
		processSpec: processSpec{
			Command: path,
			Args:    []string{"--mode", "rpc"},
			Env: func() []string {
				env := os.Environ()
				if ollama != nil {
					return applyOmpOllamaEnv(env, *ollama)
				}
				return env
			}(),
		},
		stdin: stdinR,
	}

	sup, err := startSupervisedWithStdin(procCtx, spec)
	if err != nil {
		cancel()
		_ = stdinW.Close()
		return nil, err
	}

	proc := &ompRPCProc{
		sup:    sup,
		stdinW: stdinW,
		enc:    json.NewEncoder(stdinW),
		cancel: cancel,
	}

	// Block until the process emits its "ready" event so callers know the
	// session is initialized before any commands are sent.
	sessionID, err := waitForOmpRPCReady(procCtx, sup.Stdout)
	if err != nil {
		cancel()
		_ = stdinW.Close()
		_, _ = io.Copy(io.Discard, sup.Stdout)
		_ = sup.wait()
		return nil, fmt.Errorf("omp-rpc: wait for ready: %w", err)
	}
	proc.sessionID = sessionID
	return proc, nil
}

// waitForOmpRPCReady reads lines from r until a "ready" frame is received.
// It returns the session_id from the ready frame, or an error if the context
// is cancelled or the stream ends before a ready event arrives.
func waitForOmpRPCReady(ctx context.Context, r *bufio.Reader) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		line, err := internal.ReadBoundedLine(r, maxOmpLineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", io.ErrUnexpectedEOF
			}
			if errors.Is(err, internal.ErrLineTooLong) {
				continue
			}
			return "", err
		}
		if len(line) == 0 {
			continue
		}
		var frame ompFrame
		if json.Unmarshal(line, &frame) != nil {
			continue
		}
		if frame.Type == "ready" {
			return frame.SessionID, nil
		}
	}
}

// — RPC command helpers -------------------------------------------------------

// sendPrompt sends a prompt command to the omp RPC process. The omp process
// begins generating a response immediately after receiving this command.
func sendPrompt(proc *ompRPCProc, text string) error {
	return proc.enc.Encode(ompRPCGenericCmd{Type: "prompt", Text: text})
}

// sendSetModel sends a set_model command to switch the active model.
func sendSetModel(proc *ompRPCProc, model string) error {
	return proc.enc.Encode(ompRPCGenericCmd{Type: "set_model", Model: model})
}

// sendAbort sends an abort command to cancel the current generation mid-turn.
// Best-effort: the caller should not rely on the process having received it.
func sendAbort(proc *ompRPCProc) error {
	return proc.enc.Encode(ompRPCGenericCmd{Type: "abort"})
}

// sendCompact sends a compact command to compress the conversation context.
// This frees context window space without losing conversational continuity.
func sendCompact(proc *ompRPCProc) error {
	return proc.enc.Encode(ompRPCGenericCmd{Type: "compact"})
}

// sendSetHostTools injects custom tool definitions for host-side execution.
// omp will emit toolcall_start/end events for these tools and wait for the
// host to provide results (unique to the omp RPC protocol).
func sendSetHostTools(proc *ompRPCProc, tools []llmclient.Tool) error {
	return proc.enc.Encode(ompRPCHostToolsCmd{Type: "set_host_tools", Tools: tools})
}

// — Static capabilities and fidelity -----------------------------------------

// ompRPCStaticCapabilities returns the capabilities advertised for the omp RPC
// backend. This is the only backend with HostToolDefs, HostToolApproval, and
// ToolResultInjection set to true.
func ompRPCStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:             "omp-rpc",
		Version:             version,
		Streaming:           llmclient.StreamingStructured,
		ToolEvents:          true,
		HostToolDefs:        true,
		HostToolApproval:    true,
		ToolResultInjection: true,
		MultiTurn:           true,
		Thinking:            true,
		Usage:               true,
		OllamaRouting:       true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:     llmclient.OptionSupportFull,
			llmclient.OptionSession:   llmclient.OptionSupportFull,
			llmclient.OptionHostTools: llmclient.OptionSupportFull,
			llmclient.OptionOllama:    llmclient.OptionSupportFull,
		},
	}
}

// ompRPCFidelity constructs the Fidelity for an omp RPC turn, reflecting which
// options were applied (model and host tools when present).
func ompRPCFidelity(cfg llmclient.RequestConfig) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		results[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.SessionID != "" {
		results[llmclient.OptionSession] = llmclient.OptionApplied
	}
	if len(cfg.HostTools) > 0 {
		results[llmclient.OptionHostTools] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		results[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	return &llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructured,
		ToolControl:   llmclient.ToolControlHost,
		OptionResults: mergeOptionResults(results, executionOptionResults(cfg, ompRPCStaticCapabilities(""))),
	}
}

// checkOmpRPCRequiredOptions returns ErrUnsupportedOption if any required
// option is not supported by the omp RPC backend.
func checkOmpRPCRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	supported := map[llmclient.OptionName]struct{}{
		llmclient.OptionModel:     {},
		llmclient.OptionSession:   {},
		llmclient.OptionHostTools: {},
		llmclient.OptionOllama:    {},
	}
	for name := range cfg.RequiredOptions {
		if _, ok := supported[name]; !ok {
			return fmt.Errorf("%w: %s", llmclient.ErrUnsupportedOption, name)
		}
	}
	return nil
}
