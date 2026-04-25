package llmcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/php-workx/fabrikk/llmcli/internal"
	"github.com/php-workx/fabrikk/llmclient"
)

// stdinThreshold is the byte length above which the prompt is piped via stdin
// rather than passed as the -p argument. buildClaudeArgs returns args without
// the prompt text when useStdin is true; Stream wires up the actual stdin pipe.
const stdinThreshold = 32_000

// processSpecWithStdin extends processSpec with an optional stdin reader.
// When stdin is non-nil, the subprocess receives prompt data via its standard
// input instead of through the -p argument. This avoids OS argument-length
// limits for long prompts.
type processSpecWithStdin struct {
	processSpec
	stdin io.Reader
}

// startSupervisedWithStdin is like startSupervised but also sets cmd.Stdin when
// spec.stdin is non-nil. It delegates to startSupervised for the nil-stdin case
// so all subprocess lifecycle logic remains in one place.
//
// When spec.stdin is non-nil a fresh exec.Cmd is created with the same
// plumbing as startSupervised; this duplication is intentional and isolated
// to the Claude backend because processSpec is intentionally minimal.
func startSupervisedWithStdin(ctx context.Context, spec processSpecWithStdin) (*supervisor, error) {
	if spec.stdin == nil {
		return startSupervised(ctx, spec.processSpec)
	}

	//nolint:gosec // command and args are caller-controlled
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command -- spec.Command/spec.Args come from backend-selected CLI metadata and fixed argument assembly, not shell-expanded input.
	cmd := exec.Command(spec.Command, spec.Args...)

	if spec.Env != nil {
		cmd.Env = spec.Env
	} else {
		cmd.Env = os.Environ()
	}
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}

	cmd.Stdin = spec.stdin

	configureProcessGroup(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("llmcli: stdout pipe: %w", err)
	}

	tail := newTailWriter()
	cmd.Stderr = stderrWriter(tail, spec.RawCapture)

	if err := cmd.Start(); err != nil {
		_ = stdoutPipe.Close()
		return nil, fmt.Errorf("llmcli: start process: %w", err)
	}

	s := &supervisor{
		cmd:           cmd,
		Stdout:        bufio.NewReader(stdoutReader(stdoutPipe, spec.RawCapture)),
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

	return s, nil
}

// maxClaudeLineBytes is the per-line byte cap for the bounded JSONL reader.
// 4 MiB accommodates large tool inputs while bounding memory usage.
const maxClaudeLineBytes = 4 * 1024 * 1024

// ClaudeBackend is the per-call Claude Code stream-json backend. Each Stream
// call spawns a fresh `claude -p --output-format stream-json` subprocess.
//
// ClaudeBackend implements [llmclient.Backend].
type ClaudeBackend struct {
	CliBackend
}

// NewClaudeBackend constructs a ClaudeBackend from the detected CliInfo.
func NewClaudeBackend(info CliInfo) *ClaudeBackend {
	return &ClaudeBackend{
		CliBackend: NewCliBackend("claude", info),
	}
}

// Capabilities returns the static capabilities for this backend instance.
func (b *ClaudeBackend) Capabilities() llmclient.Capabilities {
	return claudeStaticCapabilities(b.info.Version)
}

// Stream spawns `claude -p --output-format stream-json`, parses the JSONL
// event stream, and returns a channel of normalized [llmclient.Event] values.
//
// The channel is closed after exactly one terminal event (done or error).
// Cancelling ctx terminates the subprocess and closes the channel with a
// StopCancelled done event.
func (b *ClaudeBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	// Validate required options before spawning any subprocess.
	if err := checkClaudeRequiredOptions(cfg); err != nil {
		cancelTimeout()
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	prompt := lastUserMessage(input)
	useStdin := len(prompt) > stdinThreshold
	args := buildClaudeArgs(input, cfg, useStdin)
	env := resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg))

	var stdinReader io.Reader
	if useStdin {
		stdinReader = strings.NewReader(prompt)
	}

	spec := processSpecWithStdin{
		processSpec: processSpec{
			Command:    b.info.Path,
			Args:       args,
			Env:        env,
			Dir:        cfg.WorkingDirectory,
			RawCapture: cfg.RawCapture,
		},
		stdin: stdinReader,
	}

	s, err := startSupervisedWithStdin(streamCtx, spec)
	if err != nil {
		cancelTimeout()
		return nil, fmt.Errorf("llmcli claude: start subprocess: %w", err)
	}

	ch := make(chan llmclient.Event, 16)
	te := newTerminalEmitter(ch)

	go func() {
		defer cancelTimeout()
		defer te.close() // safety guard; real terminal comes from parser or wait goroutine

		parseErr := parseClaudeStream(streamCtx, s.Stdout, ch, te, claudeInitFidelity(cfg))

		// Drain stdout so cmd.Wait does not deadlock on a live reader.
		_, _ = io.Copy(io.Discard, s.Stdout)

		waitErr := s.wait()

		switch {
		case parseErr != nil &&
			!errors.Is(parseErr, context.Canceled) &&
			!errors.Is(parseErr, context.DeadlineExceeded):
			te.error(ctx, fmt.Errorf("llmcli claude: parse: %w", parseErr))
		case streamCtx.Err() != nil:
			te.done(streamCtx, nil, nil, llmclient.StopCancelled)
		case waitErr != nil:
			te.error(streamCtx, fmt.Errorf("llmcli claude: subprocess: %w; stderr: %s", waitErr, s.stderrTail()))
		default:
			// Normal exit — parseClaudeStream already emitted done via te.
		}
	}()

	return observeStream(b.Name(), model, started, ch), nil
}

// buildClaudeArgs constructs the argument list for `claude -p ...`.
//
// When useStdin is true the prompt is omitted from the -p value (empty string)
// because the caller will pipe it via stdin. When useStdin is false the full
// prompt text is passed as the -p argument.
func buildClaudeArgs(input *llmclient.Context, cfg llmclient.RequestConfig, useStdin bool) []string { //nolint:gocritic // internal tests and callers use RequestConfig values consistently.
	prompt := lastUserMessage(input)

	var args []string
	if useStdin {
		// Empty -p triggers non-interactive mode; prompt arrives via stdin.
		args = []string{"-p", ""}
	} else {
		args = []string{"-p", prompt}
	}

	args = append(args, "--output-format", "stream-json")

	// System prompt.
	if input != nil && input.SystemPrompt != "" {
		args = append(args, "--system-prompt", input.SystemPrompt)
	}

	// Model selection; Ollama model name overrides cfg.Model.
	model := cfg.Model
	if cfg.Ollama != nil && cfg.Ollama.Model != "" {
		model = cfg.Ollama.Model
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	// Session resume.
	if cfg.SessionID != "" {
		args = append(args, "--resume", cfg.SessionID)
	}

	return args
}

// buildClaudeEnv constructs the subprocess environment. base is typically
// os.Environ(). When cfg.Ollama is non-nil, the Anthropic API endpoint and
// auth env vars are injected to route traffic through Ollama.
func buildClaudeEnv(base []string, cfg llmclient.RequestConfig) []string { //nolint:gocritic // kept value-based to match existing helper tests.
	return applyEnvOverridesLast(base, claudeOllamaEnvOverrides(cfg))
}

func claudeOllamaEnvOverrides(cfg llmclient.RequestConfig) map[string]string { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	if cfg.Ollama == nil {
		return nil
	}
	overrides := map[string]string{
		"ANTHROPIC_BASE_URL":   ollamaEffectiveBaseURL(*cfg.Ollama),
		"ANTHROPIC_AUTH_TOKEN": "ollama",
		"ANTHROPIC_API_KEY":    "",
	}
	if cfg.Ollama.APIKey != "" {
		overrides["OLLAMA_API_KEY"] = cfg.Ollama.APIKey
	}
	return overrides
}

// checkClaudeRequiredOptions returns ErrUnsupportedOption if any option in
// cfg.RequiredOptions is not supported by the Claude backend.
func checkClaudeRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	return llmclient.EnforceRequired(cfg, claudeStaticCapabilities(""))
}

// lastUserMessage returns the concatenated text of the last user-role message
// in input, falling back to SystemPrompt when no user messages are present.
// Returns empty string when input is nil.
func lastUserMessage(input *llmclient.Context) string {
	if input == nil {
		return ""
	}
	for i := len(input.Messages) - 1; i >= 0; i-- {
		msg := input.Messages[i]
		if msg.Role != llmclient.RoleUser {
			continue
		}
		var parts []string
		for _, block := range msg.Content {
			if block.Type == llmclient.ContentText && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return input.SystemPrompt
}

// claudeStaticCapabilities returns the capabilities advertised for the Claude
// backend. version is the detected CLI version string (may be empty).
func claudeStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "claude",
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

// — JSONL event parser --------------------------------------------------------

// claudeFrame is the minimal envelope shared by all stream-json JSONL lines.
type claudeFrame struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`

	// system/init fields
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`

	// assistant / user wrapper
	Message *claudeMessage `json:"message,omitempty"`

	// result fields
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
}

type claudeMessage struct {
	ID      string          `json:"id,omitempty"`
	Role    string          `json:"role,omitempty"`
	Content []claudeContent `json:"content,omitempty"`
	Model   string          `json:"model,omitempty"`
}

type claudeContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`   // tool_use block id
	Name  string          `json:"name,omitempty"` // tool name
	Input json.RawMessage `json:"input,omitempty"`
}

// claudeParseState holds mutable state shared across all frames in a single
// stream parse. It is created once per Stream call.
type claudeParseState struct {
	sessionID     string
	contentIndex  int
	assembledMsg  *llmclient.AssistantMessage
	startEmitted  bool
	startFidelity *llmclient.Fidelity
}

// parseClaudeStream reads JSONL frames from r and emits normalized events to
// out. It returns when r reaches EOF or when a fatal read/context error occurs.
// Exactly one terminal event is emitted via te; intermediate events go to out.
//
// Parser paths MUST NOT use bufio.Scanner; ReadBoundedLine is used instead.
func parseClaudeStream(
	ctx context.Context,
	r *bufio.Reader,
	out chan<- llmclient.Event,
	te *terminalEmitter,
	startFidelity *llmclient.Fidelity,
) error {
	state := &claudeParseState{startFidelity: startFidelity}

	for {
		line, readErr := internal.ReadBoundedLine(r, maxClaudeLineBytes)

		if readErr != nil {
			skip, retErr := claudeHandleReadError(readErr, state.startEmitted)
			if skip {
				continue
			}
			return retErr
		}

		if len(line) == 0 {
			continue // blank separator line
		}

		var frame claudeFrame
		if json.Unmarshal(line, &frame) != nil {
			continue // malformed JSON — skip and keep parsing
		}

		done, err := claudeDispatchFrame(ctx, frame, out, te, state)
		if err != nil || done {
			return err
		}
	}
}

// claudeHandleReadError interprets an error from ReadBoundedLine.
// Returns (skip=true, nil) when the line should be skipped and the loop should
// continue. Returns (skip=false, err) when the loop should return err (which
// may be nil for normal EOF).
func claudeHandleReadError(err error, startEmitted bool) (skip bool, retErr error) {
	if errors.Is(err, io.EOF) {
		if !startEmitted {
			return false, io.ErrUnexpectedEOF
		}
		return false, nil
	}
	if errors.Is(err, internal.ErrLineTooLong) {
		return true, nil // skip oversize line, keep going
	}
	return false, err
}

// claudeDispatchFrame routes a parsed frame to its type-specific handler.
// Returns (done=true, nil) when the result frame has been processed (stream is
// finished). Returns (false, err) on context cancellation.
func claudeDispatchFrame(
	ctx context.Context,
	frame claudeFrame,
	out chan<- llmclient.Event,
	te *terminalEmitter,
	state *claudeParseState,
) (done bool, err error) {
	switch frame.Type {
	case "system":
		return false, claudeOnSystemFrame(ctx, frame, out, state)
	case "assistant":
		return false, claudeOnAssistantFrame(ctx, frame, out, state)
	case "result":
		handleClaudeResultFrame(ctx, frame, state.assembledMsg, te)
		return true, nil
	}
	// "user" (tool results) and unknown types are informational — skip.
	return false, nil
}

// claudeOnSystemFrame handles a system/init frame: emits the start event and
// initialises the assembled message. Non-init subtypes are silently ignored.
func claudeOnSystemFrame(
	ctx context.Context,
	frame claudeFrame,
	out chan<- llmclient.Event,
	state *claudeParseState,
) error {
	if frame.Subtype != "init" {
		return nil
	}
	state.sessionID = frame.SessionID
	if !emit(ctx, out, startEvent(state.sessionID, state.startFidelity)) {
		return ctx.Err()
	}
	state.startEmitted = true
	state.assembledMsg = &llmclient.AssistantMessage{
		Role:  "assistant",
		Model: frame.Model,
	}
	return nil
}

// claudeInitFidelity returns the Fidelity advertised on the start event for a
// Claude stream-json session.
func claudeInitFidelity(cfg llmclient.RequestConfig) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		results[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.SessionID != "" {
		results[llmclient.OptionSession] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		results[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	optionResults := mergeOptionResults(results, executionOptionResults(cfg, claudeStaticCapabilities("")))
	return &llmclient.Fidelity{
		Streaming:     llmclient.StreamingStructured,
		ToolControl:   llmclient.ToolControlBuiltIn,
		OptionResults: optionResults,
	}
}

// claudeOnAssistantFrame handles an assistant frame: updates the assembled
// message header and emits events for each content block.
func claudeOnAssistantFrame(
	ctx context.Context,
	frame claudeFrame,
	out chan<- llmclient.Event,
	state *claudeParseState,
) error {
	if frame.Message == nil {
		return nil
	}
	msg := frame.Message
	claudeUpdateAssembledHeader(state.assembledMsg, msg)

	for i := range msg.Content {
		if !handleClaudeContentBlock(ctx, msg.Content[i], out, &state.contentIndex, state.assembledMsg) {
			return ctx.Err()
		}
	}
	return nil
}

// claudeUpdateAssembledHeader copies the message ID and model from msg into
// assembled when those fields are first seen (ID) or when a newer value
// arrives (Model). assembled may be nil when no system/init frame arrived yet.
func claudeUpdateAssembledHeader(assembled *llmclient.AssistantMessage, msg *claudeMessage) {
	if assembled == nil {
		return
	}
	if assembled.ID == "" && msg.ID != "" {
		assembled.ID = msg.ID
	}
	if msg.Model != "" {
		assembled.Model = msg.Model
	}
}

// handleClaudeContentBlock processes a single Claude content block and emits
// the appropriate normalized events. Returns false if ctx was cancelled.
func handleClaudeContentBlock(
	ctx context.Context,
	block claudeContent,
	out chan<- llmclient.Event,
	contentIndex *int,
	assembledMsg *llmclient.AssistantMessage,
) bool {
	idx := *contentIndex

	switch block.Type {
	case "text":
		evs := textSequence(idx, block.Text)
		for i := range evs {
			if !emit(ctx, out, evs[i]) {
				return false
			}
		}
		if assembledMsg != nil {
			assembledMsg.Content = append(assembledMsg.Content, llmclient.ContentBlock{
				Type: llmclient.ContentText,
				Text: block.Text,
			})
		}
		*contentIndex++

	case "thinking":
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingStart, ContentIndex: idx}) {
			return false
		}
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingDelta, ContentIndex: idx, Delta: block.Text}) {
			return false
		}
		if !emit(ctx, out, llmclient.Event{Type: llmclient.EventThinkingEnd, ContentIndex: idx, Content: block.Text}) {
			return false
		}
		if assembledMsg != nil {
			assembledMsg.Content = append(assembledMsg.Content, llmclient.ContentBlock{
				Type: llmclient.ContentThinking,
				Text: block.Text,
			})
		}
		*contentIndex++

	case "tool_use":
		var args map[string]interface{}
		if len(block.Input) > 0 {
			_ = json.Unmarshal(block.Input, &args)
		}
		tc := &llmclient.ToolCall{
			ID:        block.ID,
			Name:      block.Name,
			Arguments: args,
		}
		if !emit(ctx, out, llmclient.Event{
			Type:         llmclient.EventToolCallStart,
			ContentIndex: idx,
			ToolCall:     tc,
		}) {
			return false
		}
		if !emit(ctx, out, llmclient.Event{
			Type:         llmclient.EventToolCallEnd,
			ContentIndex: idx,
			ToolCall:     tc,
		}) {
			return false
		}
		if assembledMsg != nil {
			assembledMsg.Content = append(assembledMsg.Content, llmclient.ContentBlock{
				Type:       llmclient.ContentToolUse,
				ToolCallID: block.ID,
				ToolName:   block.Name,
				Arguments:  args,
			})
		}
		*contentIndex++
	}

	return true
}

// handleClaudeResultFrame processes a Claude result frame and fires exactly one
// terminal event via te. Always returns nil to signal that the parser is done.
func handleClaudeResultFrame(
	ctx context.Context,
	frame claudeFrame,
	assembledMsg *llmclient.AssistantMessage,
	te *terminalEmitter,
) {
	switch frame.Subtype {
	case "success":
		reason := llmclient.StopEndTurn
		if assembledMsg != nil {
			assembledMsg.StopReason = reason
		}
		// Claude reports cost, not token counts. Emit a non-nil Usage so
		// callers can detect that usage metadata arrived.
		var usage *llmclient.Usage
		if frame.TotalCostUSD > 0 {
			usage = &llmclient.Usage{}
		}
		te.done(ctx, assembledMsg, usage, reason)

	case "error_max_turns":
		te.error(ctx, errors.New("claude: max turns reached"))

	default:
		// Covers error_during_execution and any future error subtypes.
		te.error(ctx, fmt.Errorf("claude: result error subtype %q", frame.Subtype))
	}
}
