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

	if err := checkCodexExecRequiredOptions(cfg); err != nil {
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	workDir, cleanup, err := writeTempAgentsFile(input)
	if err != nil {
		return nil, fmt.Errorf("llmcli codex: temp AGENTS.md: %w", err)
	}

	streaming := llmclient.StreamingBufferedOnly
	if probePipeStreaming(ctx, b.info.Path, nil) {
		streaming = llmclient.StreamingTextChunk
	}

	spec := processSpec{
		Command: b.info.Path,
		Args:    buildCodexExecArgs(input, cfg),
		Dir:     workDir,
	}

	ch, err := streamTextProcess(ctx, spec, codexExecFidelity(cfg, streaming), cleanup)
	if err != nil {
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
func buildCodexExecArgs(input *llmclient.Context, cfg llmclient.RequestConfig) []string {
	prompt := lastUserMessage(input)

	// --approval-policy full-auto prevents interactive approval prompts in
	// programmatic invocations where there is no human operator at the terminal.
	args := []string{"exec", prompt, "--approval-policy", "full-auto"}

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
func checkCodexExecRequiredOptions(cfg llmclient.RequestConfig) error {
	supported := map[llmclient.OptionName]struct{}{
		llmclient.OptionModel:  {},
		llmclient.OptionOllama: {},
	}
	for name := range cfg.RequiredOptions {
		if _, ok := supported[name]; !ok {
			return fmt.Errorf("%w: %s", llmclient.ErrUnsupportedOption, name)
		}
	}
	return nil
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
			llmclient.OptionModel:  llmclient.OptionSupportFull,
			llmclient.OptionOllama: llmclient.OptionSupportFull,
		},
	}
}

func codexExecFidelity(cfg llmclient.RequestConfig, streaming llmclient.StreamingFidelity) *llmclient.Fidelity {
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		results[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		results[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	var optionResults map[llmclient.OptionName]llmclient.OptionResult
	if len(results) > 0 {
		optionResults = results
	}
	return &llmclient.Fidelity{
		Streaming:     streaming,
		ToolControl:   llmclient.ToolControlNone,
		OptionResults: optionResults,
	}
}
