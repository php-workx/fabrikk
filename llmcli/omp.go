package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

// maxOmpLineBytes is the per-line byte cap for the bounded JSONL reader.
// 4 MiB accommodates large tool inputs while bounding memory usage.
const (
	maxOmpLineBytes   = 4 * 1024 * 1024
	ompFrameTypeReady = "ready"
)

// OmpBackend is the per-call omp print-mode backend. Each Stream call spawns a
// fresh `omp -p --mode json` subprocess. omp is intended for internal fabrikk
// use only and is registered after public CLI backends.
//
// OmpBackend implements [llmclient.Backend].
type OmpBackend struct {
	CliBackend
}

// NewOmpBackend constructs an OmpBackend from the detected CliInfo.
func NewOmpBackend(info CliInfo) *OmpBackend {
	return &OmpBackend{
		CliBackend: NewCliBackend("omp", info),
	}
}

// Capabilities returns the static capabilities for this backend instance.
func (b *OmpBackend) Capabilities() llmclient.Capabilities {
	return ompPrintStaticCapabilities(b.info.Version)
}

// Stream spawns `omp -p --mode json`, parses the JSONL event stream, and
// returns a channel of normalized [llmclient.Event] values.
//
// The channel is closed after exactly one terminal event (done or error).
// Cancelling ctx terminates the subprocess and closes the channel with a
// StopCancelled done event.
func (b *OmpBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	if err := checkOmpPrintRequiredOptions(cfg); err != nil {
		cancelTimeout()
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	args := buildOmpPrintArgs(input, cfg)
	env := resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg))

	s, err := startSupervised(streamCtx, processSpec{
		Command:    b.info.Path,
		Args:       args,
		Env:        env,
		Dir:        cfg.WorkingDirectory,
		RawCapture: cfg.RawCapture,
	})
	if err != nil {
		cancelTimeout()
		return nil, fmt.Errorf("llmcli omp: start subprocess: %w", err)
	}

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	go func() {
		defer cancelTimeout()
		defer te.close() // safety guard; real terminal comes from parser or wait goroutine

		parseErr := parseOmpStream(streamCtx, s.Stdout, ch, te, ompPrintFidelity(cfg))

		// Drain stdout so cmd.Wait does not deadlock on a live reader.
		_, _ = io.Copy(io.Discard, s.Stdout)

		waitErr := s.wait()

		switch {
		case parseErr != nil &&
			!errors.Is(parseErr, context.Canceled) &&
			!errors.Is(parseErr, context.DeadlineExceeded):
			te.error(ctx, fmt.Errorf("llmcli omp: parse: %w", parseErr))
		case streamCtx.Err() != nil:
			te.done(streamCtx, nil, nil, llmclient.StopCancelled)
		case waitErr != nil:
			te.error(streamCtx, fmt.Errorf("llmcli omp: subprocess: %w; stderr: %s", waitErr, s.stderrTail()))
		default:
			// Normal exit — parseOmpStream already emitted done via te.
		}
	}()

	return observeStream(b.Name(), model, started, ch), nil
}

// buildOmpPrintArgs constructs the argument list for `omp -p --mode json ...`.
func buildOmpPrintArgs(input *llmclient.Context, cfg llmclient.RequestConfig) []string { //nolint:gocritic // RequestConfig value keeps helper tests simple and immutable.
	prompt := lastUserMessage(input)
	args := []string{"-p", prompt, "--mode", "json"}

	if input != nil && input.SystemPrompt != "" {
		args = append(args, "--system-prompt", input.SystemPrompt)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.SessionID != "" {
		args = append(args, "--resume", cfg.SessionID)
	}
	return args
}

// checkOmpPrintRequiredOptions returns ErrUnsupportedOption if any required
// option is not supported by the omp print backend.
func checkOmpPrintRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	return llmclient.EnforceRequired(cfg, ompPrintStaticCapabilities(""))
}

// ompPrintStaticCapabilities returns the capabilities advertised for the omp
// print-mode backend.
func ompPrintStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "omp",
		Version:       version,
		Streaming:     llmclient.StreamingStructured,
		ToolEvents:    true,
		MultiTurn:     true,
		Thinking:      true,
		Usage:         true,
		OllamaRouting: true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:              llmclient.OptionSupportFull,
			llmclient.OptionSession:            llmclient.OptionSupportFull,
			llmclient.OptionOllama:             llmclient.OptionSupportFull,
			llmclient.OptionWorkingDirectory:   llmclient.OptionSupportFull,
			llmclient.OptionEnvironment:        llmclient.OptionSupportFull,
			llmclient.OptionEnvironmentOverlay: llmclient.OptionSupportFull,
			llmclient.OptionTimeout:            llmclient.OptionSupportFull,
			llmclient.OptionRawCapture:         llmclient.OptionSupportFull,
		},
	}
}

// — Shared JSONL parser -------------------------------------------------------

// ompFrame is the envelope shared by all omp JSONL events.
type ompFrame struct {
	Type      string       `json:"type"`
	Content   string       `json:"content,omitempty"`    // text_delta, thinking_delta/end
	SessionID string       `json:"session_id,omitempty"` // ready
	ToolCall  *ompToolCall `json:"toolCall,omitempty"`   // toolcall_start/end
	Message   *ompMessage  `json:"message,omitempty"`    // done
	Error     *ompError    `json:"error,omitempty"`      // error
}

type ompToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type ompMessage struct {
	ID    string    `json:"id,omitempty"`
	Model string    `json:"model,omitempty"`
	Usage *ompUsage `json:"usage,omitempty"`
}

type ompUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

type ompError struct {
	Message string `json:"message,omitempty"`
}

// ompParseState holds mutable state shared across frames in a single omp stream.
type ompParseState struct {
	sessionID     string
	contentIndex  int
	assembledMsg  *llmclient.AssistantMessage
	startEmitted  bool
	startFidelity *llmclient.Fidelity

	// open block tracking for streaming text and thinking
	inTextBlock  bool
	textAccum    strings.Builder
	inThinkBlock bool
	thinkAccum   strings.Builder

	// pending tool call awaiting toolcall_end
	pendingToolCall *llmclient.ToolCall
	pendingToolIdx  int
}

// parseOmpStream reads JSONL frames from r and emits normalized events for
// a print-mode session. It expects a "ready" event to initialise the stream.
// Returns when r reaches EOF or when a fatal error occurs.
//
// Exactly one terminal event is emitted via te; intermediate events go to out.
func parseOmpStream(
	ctx context.Context,
	r *bufio.Reader,
	out chan<- llmclient.Event,
	te *terminalEmitter,
	startFidelity *llmclient.Fidelity,
) error {
	return parseOmpLines(ctx, r, out, te, &ompParseState{startFidelity: startFidelity})
}

// parseOmpRPCTurn reads JSONL frames from r and emits normalized events for a
// single RPC turn. The start event must have been emitted by the caller before
// this function is invoked (startEmitted=true skips the ready-event check).
func parseOmpRPCTurn(
	ctx context.Context,
	r *bufio.Reader,
	out chan<- llmclient.Event,
	te *terminalEmitter,
) error {
	return parseOmpLines(ctx, r, out, te, &ompParseState{
		startEmitted:  true,
		assembledMsg:  &llmclient.AssistantMessage{Role: "assistant"},
		startFidelity: ompRPCFidelity(llmclient.RequestConfig{}),
	})
}

// parseOmpLines is the shared parse loop used by both print and RPC parsers.
// state controls whether a ready event is expected and whether the assembled
// message slot was pre-allocated.
func parseOmpLines(
	ctx context.Context,
	r *bufio.Reader,
	out chan<- llmclient.Event,
	te *terminalEmitter,
	state *ompParseState,
) error {
	for {
		line, readErr := internal.ReadBoundedLine(r, maxOmpLineBytes)

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if !state.startEmitted {
					return io.ErrUnexpectedEOF
				}
				return nil
			}
			if errors.Is(readErr, internal.ErrLineTooLong) {
				continue // skip oversize line, keep parsing
			}
			return readErr
		}

		if len(line) == 0 {
			continue // blank separator line
		}

		var frame ompFrame
		if json.Unmarshal(line, &frame) != nil {
			continue // malformed JSON — skip and keep parsing
		}

		// In RPC mode the process may emit a "ready" event at startup; we consume
		// it during process init so ignore any that arrive mid-stream.
		if frame.Type == ompFrameTypeReady && state.startEmitted {
			continue
		}

		done, err := ompDispatchFrame(ctx, frame, out, te, state)
		if err != nil || done {
			return err
		}
	}
}

// ompDispatchFrame routes a parsed frame to its type-specific handler.
// Returns (done=true, nil) when the stream has been fully processed.
func ompDispatchFrame(
	ctx context.Context,
	frame ompFrame,
	out chan<- llmclient.Event,
	te *terminalEmitter,
	state *ompParseState,
) (done bool, err error) {
	switch frame.Type {
	case ompFrameTypeReady:
		return false, ompOnReady(ctx, frame, out, state)
	case "text_delta":
		return false, ompOnTextDelta(ctx, frame, out, state)
	case "thinking_delta":
		return false, ompOnThinkingDelta(ctx, frame, out, state)
	case "thinking_end":
		return false, ompOnThinkingEnd(ctx, frame, out, state)
	case "toolcall_start":
		if err := ompCloseOpenBlocks(ctx, out, state); err != nil {
			return false, err
		}
		return false, ompOnToolCallStart(ctx, frame, out, state)
	case "toolcall_end":
		return false, ompOnToolCallEnd(ctx, frame, out, state)
	case "done":
		if err := ompCloseOpenBlocks(ctx, out, state); err != nil {
			return false, err
		}
		ompOnDone(ctx, frame, state, te)
		return true, nil
	case "error":
		ompOnError(frame, te)
		return true, nil
	}
	// Unknown types are silently ignored.
	return false, nil
}

// ompCloseOpenBlocks closes any text or thinking block that is currently open.
// Must be called before toolcall_start and done frames to maintain correct
// block boundaries.
func ompCloseOpenBlocks(ctx context.Context, out chan<- llmclient.Event, state *ompParseState) error {
	if state.inTextBlock {
		idx := state.contentIndex
		text := state.textAccum.String()
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventTextEnd, ContentIndex: idx, Content: text}) {
			return ctx.Err()
		}
		if state.assembledMsg != nil {
			state.assembledMsg.Content = append(state.assembledMsg.Content, llmclient.ContentBlock{
				Type: llmclient.ContentText,
				Text: text,
			})
		}
		state.textAccum.Reset()
		state.inTextBlock = false
		state.contentIndex++
	}
	if state.inThinkBlock {
		idx := state.contentIndex
		think := state.thinkAccum.String()
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingEnd, ContentIndex: idx, Content: think}) {
			return ctx.Err()
		}
		if state.assembledMsg != nil {
			state.assembledMsg.Content = append(state.assembledMsg.Content, llmclient.ContentBlock{
				Type: llmclient.ContentThinking,
				Text: think,
			})
		}
		state.thinkAccum.Reset()
		state.inThinkBlock = false
		state.contentIndex++
	}
	return nil
}

func ompOnReady(ctx context.Context, frame ompFrame, out chan<- llmclient.Event, state *ompParseState) error {
	state.sessionID = frame.SessionID
	state.assembledMsg = &llmclient.AssistantMessage{Role: "assistant"}
	fidelity := state.startFidelity
	if fidelity == nil {
		fidelity = ompPrintFidelity(llmclient.RequestConfig{})
	}
	if !emit(ctx, out, startEvent(state.sessionID, fidelity)) {
		return ctx.Err()
	}
	state.startEmitted = true
	return nil
}

func ompPrintFidelity(cfg llmclient.RequestConfig) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
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
	optionResults := mergeOptionResults(optResults, executionOptionResults(cfg, ompPrintStaticCapabilities("")))

	return &llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructured,
		ToolControl:   llmclient.ToolControlBuiltIn,
		OptionResults: optionResults,
	}
}

func ompOnTextDelta(ctx context.Context, frame ompFrame, out chan<- llmclient.Event, state *ompParseState) error {
	idx := state.contentIndex
	if !state.inTextBlock {
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventTextStart, ContentIndex: idx}) {
			return ctx.Err()
		}
		state.inTextBlock = true
	}
	if !emit(ctx, out, llmclient.Event{Type: llmclient.EventTextDelta, ContentIndex: idx, Delta: frame.Content}) {
		return ctx.Err()
	}
	state.textAccum.WriteString(frame.Content)
	return nil
}

func ompOnThinkingDelta(ctx context.Context, frame ompFrame, out chan<- llmclient.Event, state *ompParseState) error {
	idx := state.contentIndex
	if !state.inThinkBlock {
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingStart, ContentIndex: idx}) {
			return ctx.Err()
		}
		state.inThinkBlock = true
	}
	if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingDelta, ContentIndex: idx, Delta: frame.Content}) {
		return ctx.Err()
	}
	state.thinkAccum.WriteString(frame.Content)
	return nil
}

func ompOnThinkingEnd(ctx context.Context, frame ompFrame, out chan<- llmclient.Event, state *ompParseState) error {
	idx := state.contentIndex

	// thinking_end carries the full thinking text as "content". Use accumulated
	// content as fallback when thinking_end omits the full text.
	fullContent := frame.Content
	if fullContent == "" {
		fullContent = state.thinkAccum.String()
	}

	if !state.inThinkBlock {
		// Synthesise start+delta when thinking_end arrives without a prior delta.
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingStart, ContentIndex: idx}) {
			return ctx.Err()
		}
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingDelta, ContentIndex: idx, Delta: fullContent}) {
			return ctx.Err()
		}
	}

	if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingEnd, ContentIndex: idx, Content: fullContent}) {
		return ctx.Err()
	}
	if state.assembledMsg != nil {
		state.assembledMsg.Content = append(state.assembledMsg.Content, llmclient.ContentBlock{
			Type: llmclient.ContentThinking,
			Text: fullContent,
		})
	}
	state.thinkAccum.Reset()
	state.inThinkBlock = false
	state.contentIndex++
	return nil
}

func ompOnToolCallStart(ctx context.Context, frame ompFrame, out chan<- llmclient.Event, state *ompParseState) error {
	idx := state.contentIndex
	tc := &llmclient.ToolCall{}
	if frame.ToolCall != nil {
		tc.ID = frame.ToolCall.ID
		tc.Name = frame.ToolCall.Name
		tc.Arguments = frame.ToolCall.Arguments
	}
	state.pendingToolCall = tc
	state.pendingToolIdx = idx
	if !emit(ctx, out, llmclient.Event{Type: llmclient.EventToolCallStart, ContentIndex: idx, ToolCall: tc}) {
		return ctx.Err()
	}
	return nil
}

func ompOnToolCallEnd(ctx context.Context, frame ompFrame, out chan<- llmclient.Event, state *ompParseState) error {
	idx := state.pendingToolIdx
	tc := state.pendingToolCall
	if tc == nil {
		tc = &llmclient.ToolCall{}
	}
	if frame.ToolCall != nil {
		if frame.ToolCall.ID != "" {
			tc.ID = frame.ToolCall.ID
		}
		if frame.ToolCall.Name != "" {
			tc.Name = frame.ToolCall.Name
		}
		if frame.ToolCall.Arguments != nil {
			tc.Arguments = frame.ToolCall.Arguments
		}
	}
	if !emit(ctx, out, llmclient.Event{Type: llmclient.EventToolCallEnd, ContentIndex: idx, ToolCall: tc}) {
		return ctx.Err()
	}
	if state.assembledMsg != nil {
		state.assembledMsg.Content = append(state.assembledMsg.Content, llmclient.ContentBlock{
			Type:       llmclient.ContentToolUse,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Arguments:  tc.Arguments,
		})
	}
	state.pendingToolCall = nil
	state.contentIndex++
	return nil
}

func ompOnDone(ctx context.Context, frame ompFrame, state *ompParseState, te *terminalEmitter) {
	msg := state.assembledMsg
	if msg == nil {
		msg = &llmclient.AssistantMessage{Role: "assistant"}
	}
	var usage *llmclient.Usage
	if frame.Message != nil {
		if frame.Message.ID != "" {
			msg.ID = frame.Message.ID
		}
		if frame.Message.Model != "" {
			msg.Model = frame.Message.Model
		}
		if frame.Message.Usage != nil {
			usage = &llmclient.Usage{
				InputTokens:  frame.Message.Usage.InputTokens,
				OutputTokens: frame.Message.Usage.OutputTokens,
			}
			msg.Usage = usage
		}
	}
	msg.StopReason = llmclient.StopEndTurn
	te.done(ctx, msg, usage, llmclient.StopEndTurn)
}

func ompOnError(frame ompFrame, te *terminalEmitter) {
	// Use a background-like context; te.error handles a pre-cancelled ctx gracefully.
	msg := "omp: unknown error"
	if frame.Error != nil && frame.Error.Message != "" {
		msg = "omp: " + frame.Error.Message
	}
	te.error(context.Background(), errors.New(msg))
}
