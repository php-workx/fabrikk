package llmcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/php-workx/fabrikk/llmclient"
)

// CodexBackend is the per-call codex exec text fallback backend. Each Stream
// call spawns a fresh `codex exec` subprocess and reads its plain-text output.
// Structured streaming, tool events, thinking blocks, and usage reporting are
// not supported; neither is multi-turn session resumption.
//
// The system prompt (if any) is written to a temporary AGENTS.md file whose
// directory is used as the subprocess working directory. The file is removed
// once the subprocess exits.
//
// CodexBackend implements [llmclient.Backend].
type CodexBackend struct {
	CliBackend
}

// NewCodexBackend constructs a CodexBackend from the detected CliInfo.
func NewCodexBackend(info CliInfo) *CodexBackend {
	return &CodexBackend{CliBackend: NewCliBackend("codex-exec", info)}
}

// Capabilities returns the static capabilities for this backend instance.
func (b *CodexBackend) Capabilities() llmclient.Capabilities {
	return codexExecStaticCapabilities(b.info.Version)
}

// Stream spawns `codex exec <prompt>`, reads the plain-text response, and
// returns a channel of normalized [llmclient.Event] values.
//
// If input.SystemPrompt is non-empty a temporary AGENTS.md is created outside
// any user config directory. The subprocess working directory is set to the
// directory containing AGENTS.md so that Codex picks it up as the system
// prompt. The temporary directory is removed after the subprocess exits.
//
// The channel is closed after exactly one terminal event (done or error).
// Cancelling ctx terminates the subprocess and produces a StopCancelled done
// event.
func (b *CodexBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	if err := checkCodexExecRequiredOptions(cfg); err != nil {
		cancelTimeout()
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	workDir := ""
	cleanup := func() {}
	if cfg.WorkingDirectory == "" {
		var err error
		workDir, cleanup, err = writeTempAgentsFile(input)
		if err != nil {
			cancelTimeout()
			return nil, fmt.Errorf("llmcli codex: temp AGENTS.md: %w", err)
		}
	}

	streaming := llmclient.StreamingBufferedOnly
	if probePipeStreaming(streamCtx, b.info.Path, nil) {
		streaming = llmclient.StreamingTextChunk
	}

	spec := processSpec{
		Command:    b.info.Path,
		Args:       buildCodexExecArgs(input, cfg),
		Env:        resolveProcessEnv(cfg, nil),
		Dir:        workDir,
		RawCapture: cfg.RawCapture,
	}

	var ch <-chan llmclient.Event
	var err error
	cleanupFn := func() {
		defer cancelTimeout()
		cleanup()
	}
	if cfg.CodexJSONL {
		ch, err = streamCodexJSONLProcess(streamCtx, spec, codexExecFidelity(cfg, streaming), cleanupFn)
	} else {
		ch, err = streamTextProcess(streamCtx, spec, codexExecFidelity(cfg, streaming), cleanupFn)
	}
	if err != nil {
		cancelTimeout()
		cleanup()
		return nil, fmt.Errorf("llmcli codex: %w", err)
	}

	return observeStream(b.Name(), model, started, ch), nil
}

// buildCodexExecArgs constructs the argument list for `codex exec <prompt>`.
//
// The system prompt is supplied via AGENTS.md in the subprocess working
// directory rather than through a flag (codex exec has no --system-prompt
// flag). writeTempAgentsFile must be called before invoking the subprocess.
func buildCodexExecArgs(input *llmclient.Context, cfg llmclient.RequestConfig) []string { //nolint:gocritic // RequestConfig value keeps helper tests simple and immutable.
	prompt := codexExecPrompt(input, cfg)

	// --approval-policy full-auto prevents interactive approval prompts in
	// programmatic invocations where there is no human operator at the terminal.
	var args []string
	if cfg.WorkingDirectory != "" {
		args = append(args, "-C", cfg.WorkingDirectory)
	}
	if cfg.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+cfg.ReasoningEffort)
	}
	args = append(args, "exec")
	if cfg.CodexJSONL {
		args = append(args, "--json")
	}
	args = append(args, prompt, "--approval-policy", "full-auto")

	model := cfg.Model
	if cfg.Ollama != nil {
		args = append(args, "--oss")
		if cfg.Ollama.Model != "" {
			model = cfg.Ollama.Model
		}
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	return args
}

func codexExecPrompt(input *llmclient.Context, cfg llmclient.RequestConfig) string { //nolint:gocritic // RequestConfig value keeps helper tests simple and immutable.
	prompt := lastUserMessage(input)
	if cfg.WorkingDirectory == "" || input == nil || input.SystemPrompt == "" {
		return prompt
	}
	return "System instructions:\n" + input.SystemPrompt + "\n\nUser request:\n" + prompt
}

// writeTempAgentsFile creates a temporary directory containing an AGENTS.md
// file with the content of input.SystemPrompt. The directory is safe to use
// as the `codex exec` working directory: Codex reads AGENTS.md from the CWD
// as the system prompt.
//
// When input is nil or has an empty SystemPrompt, an empty AGENTS.md is still
// written so that the subprocess always has a consistent working directory.
//
// The returned cleanup function removes the entire temporary directory and must
// be called exactly once after the subprocess exits.
//
// The temporary directory is created via os.MkdirTemp and is therefore placed
// in the OS-default temp location — never inside ~/.codex, ~/.config/opencode,
// or ~/.fabrikk.
func writeTempAgentsFile(input *llmclient.Context) (dir string, cleanup func(), err error) {
	tmpDir, mkErr := os.MkdirTemp("", "fabrikk-codex-*")
	if mkErr != nil {
		return "", func() {}, fmt.Errorf("create temp dir: %w", mkErr)
	}

	cleanupFn := func() { _ = os.RemoveAll(tmpDir) }

	var systemPrompt string
	if input != nil {
		systemPrompt = input.SystemPrompt
	}

	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	if writeErr := os.WriteFile(agentsPath, []byte(systemPrompt), 0o600); writeErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("write AGENTS.md: %w", writeErr)
	}

	return tmpDir, cleanupFn, nil
}

// checkCodexExecRequiredOptions returns ErrUnsupportedOption if any option in
// cfg.RequiredOptions is not supported by the codex-exec backend.
func checkCodexExecRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	return llmclient.EnforceRequired(cfg, codexExecStaticCapabilities(""))
}

// codexExecStaticCapabilities returns the static capabilities advertised for
// the codex-exec backend. version is the detected CLI version string (may be
// empty).
func codexExecStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "codex-exec",
		Version:       version,
		Streaming:     llmclient.StreamingBufferedOnly,
		OllamaRouting: true,
		OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
			llmclient.OptionModel:              llmclient.OptionSupportFull,
			llmclient.OptionOllama:             llmclient.OptionSupportFull,
			llmclient.OptionWorkingDirectory:   llmclient.OptionSupportFull,
			llmclient.OptionEnvironment:        llmclient.OptionSupportFull,
			llmclient.OptionEnvironmentOverlay: llmclient.OptionSupportFull,
			llmclient.OptionTimeout:            llmclient.OptionSupportFull,
			llmclient.OptionReasoningEffort:    llmclient.OptionSupportFull,
			llmclient.OptionCodexJSONL:         llmclient.OptionSupportFull,
			llmclient.OptionRawCapture:         llmclient.OptionSupportFull,
		},
	}
}

func codexExecFidelity(cfg llmclient.RequestConfig, streaming llmclient.StreamingFidelity) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		results[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		results[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	optionResults := mergeOptionResults(results, executionOptionResults(cfg, codexExecStaticCapabilities("")))
	return &llmclient.Fidelity{
		Streaming:     streaming,
		ToolControl:   llmclient.ToolControlNone,
		OptionResults: optionResults,
	}
}
