package llmcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/php-workx/fabrikk/llmclient"
)

// OpenCodeRunBackend is the per-call opencode run text fallback backend. Each
// Stream call spawns a fresh `opencode run` subprocess and reads its plain-text
// output. Structured streaming, tool events, thinking blocks, and usage
// reporting are not supported; neither is multi-turn session resumption.
//
// Model selection and the system prompt are injected via a temporary
// opencode config file. The subprocess XDG_CONFIG_HOME is set to an isolated
// temp directory so user config files are never read or modified.
//
// OpenCodeRunBackend implements [llmclient.Backend].
type OpenCodeRunBackend struct {
	CliBackend

	// streamingMode is the cached result of probePipeStreaming, populated on the
	// first Stream call. Subsequent calls reuse the cached value to avoid
	// spawning a probe subprocess on every invocation.
	streamingMode llmclient.StreamingFidelity
	streamingOnce sync.Once
}

// NewOpenCodeRunBackend constructs an OpenCodeRunBackend from the detected
// CliInfo.
func NewOpenCodeRunBackend(info CliInfo) *OpenCodeRunBackend {
	return &OpenCodeRunBackend{CliBackend: NewCliBackend("opencode-run", info)}
}

// Capabilities returns the static capabilities for this backend instance.
func (b *OpenCodeRunBackend) Capabilities() llmclient.Capabilities {
	return openCodeRunStaticCapabilities(b.info.Version)
}

// Ready checks that opencode is installed and that a user opencode config exists.
func (b *OpenCodeRunBackend) Ready(_ context.Context) llmclient.ReadyReport {
	if !b.binaryAvailable() {
		return readyMissingBinary(b.Name(), b.info.Path)
	}
	if anyPathExists(xdgConfigPath("opencode", "opencode.json")) {
		return llmclient.ReadyReport{State: llmclient.ReadyOK}
	}
	return readyNotAuthed(b.Name(), "configure a provider in ~/.config/opencode/opencode.json")
}

// Available reports whether opencode is installed and configured.
func (b *OpenCodeRunBackend) Available() bool {
	return observeAvailability(b.Name(), b.Ready(context.Background()).State == llmclient.ReadyOK)
}

// Stream spawns `opencode run <message>`, reads the plain-text response, and
// returns a channel of normalized [llmclient.Event] values.
//
// Model selection and the system prompt are written to a temporary opencode
// config file under an isolated XDG_CONFIG_HOME directory. This ensures that
// user config files (~/.config/opencode/) are never read or modified. The
// temporary directory is removed after the subprocess exits.
//
// The channel is closed after exactly one terminal event (done or error).
// Cancelling ctx terminates the subprocess and produces a StopCancelled done
// event.
//
// [VERIFY during integration]: Confirm config field names and XDG config path
// against the installed opencode version. The current config schema is inferred
// from public opencode documentation and must be validated before relying on
// model or systemPrompt injection in production.
func (b *OpenCodeRunBackend) Stream(
	ctx context.Context,
	input *llmclient.Context,
	opts ...llmclient.Option,
) (<-chan llmclient.Event, error) {
	cfg := llmclient.ApplyOptions(llmclient.DefaultRequestConfig(), opts)
	streamCtx, cancelTimeout := contextWithRequestTimeout(ctx, cfg)

	if err := checkOpenCodeRunRequiredOptions(cfg); err != nil {
		cancelTimeout()
		return nil, err
	}
	model, started := observeStreamStart(b.Name(), cfg)

	xdgConfigHome, cleanup, err := writeTempOpenCodeConfig(input, cfg)
	if err != nil {
		cancelTimeout()
		return nil, fmt.Errorf("llmcli opencode: temp config: %w", err)
	}

	b.streamingOnce.Do(func() {
		b.streamingMode = llmclient.StreamingBufferedOnly
		if probePipeStreaming(ctx, b.info.Path, nil) {
			b.streamingMode = llmclient.StreamingTextChunk
		}
	})
	streaming := b.streamingMode

	spec := processSpec{
		Command:    b.info.Path,
		Args:       buildOpenCodeRunArgs(input, cfg),
		Env:        resolveProcessEnv(cfg, map[string]string{"XDG_CONFIG_HOME": xdgConfigHome}),
		Dir:        cfg.WorkingDirectory,
		RawCapture: cfg.RawCapture,
	}

	ch, err := streamTextProcess(streamCtx, spec, openCodeRunFidelity(cfg, streaming), func() {
		defer cancelTimeout()
		cleanup()
	})
	if err != nil {
		cancelTimeout()
		cleanup()
		return nil, fmt.Errorf("llmcli opencode: %w", err)
	}

	return observeStream(b.Name(), model, started, ch), nil
}

// buildOpenCodeRunArgs constructs the argument list for `opencode run <message>`.
//
// opencode run does not accept flags for system prompt or model; those are
// supplied through the config file written by writeTempOpenCodeConfig.
func buildOpenCodeRunArgs(input *llmclient.Context, cfg llmclient.RequestConfig) []string { //nolint:gocritic // signature kept stable for existing helper tests.
	prompt := llmclient.BuildPromptFromContext(input)
	if cfg.SessionID != "" {
		prompt = llmclient.LastUserMessage(input)
	}
	return []string{"run", prompt}
}

// openCodeConfig is the subset of the opencode configuration that llmcli
// controls for per-call ephemeral configuration.
//
// [VERIFY during integration]: Confirm exact field names against the opencode
// config schema for the installed version. The names here are inferred from
// public opencode documentation.
type openCodeConfig struct {
	Model        string                            `json:"model,omitempty"`
	Instructions []string                          `json:"instructions,omitempty"`
	Providers    map[string]openCodeOllamaProvider `json:"providers,omitempty"`
}

// writeTempOpenCodeConfig creates an isolated XDG config directory containing
// an opencode config file at <tmpDir>/opencode/opencode.json. Setting
// XDG_CONFIG_HOME to the returned xdgConfigHome path in the subprocess
// environment causes opencode to read the ephemeral config instead of the
// user's real config.
//
// The returned cleanup function removes the entire temporary directory and must
// be called exactly once after the subprocess exits.
//
// The temporary directory is created via os.MkdirTemp and is placed in the
// OS-default temp location — never inside ~/.config/opencode, ~/.codex, or
// ~/.fabrikk.
//
//nolint:gocritic // RequestConfig value keeps config generation side-effect-free.
func writeTempOpenCodeConfig(
	input *llmclient.Context,
	cfg llmclient.RequestConfig,
) (xdgConfigHome string, cleanup func(), err error) {
	tmpDir, mkErr := os.MkdirTemp("", "fabrikk-opencode-*")
	if mkErr != nil {
		return "", func() {}, fmt.Errorf("create temp dir: %w", mkErr)
	}

	cleanupFn := func() { _ = os.RemoveAll(tmpDir) }

	// opencode reads its config from $XDG_CONFIG_HOME/opencode/opencode.json.
	openCodeDir := filepath.Join(tmpDir, "opencode")
	if dirErr := os.Mkdir(openCodeDir, 0o700); dirErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("create opencode config dir: %w", dirErr)
	}

	ocCfg := openCodeConfig{
		Model: cfg.Model,
	}
	if input != nil && input.SystemPrompt != "" {
		// opencode does not accept a 'systemPrompt' config key. System
		// instructions must be written to a file and referenced via the
		// 'instructions' array.
		promptPath := filepath.Join(tmpDir, "system-prompt.md")
		if writeErr := os.WriteFile(promptPath, []byte(input.SystemPrompt), 0o600); writeErr != nil {
			cleanupFn()
			return "", func() {}, fmt.Errorf("write system prompt file: %w", writeErr)
		}
		ocCfg.Instructions = []string{promptPath}
	}
	if cfg.Ollama != nil {
		ocCfg.Providers = buildOpenCodeOllamaConfigJSON(*cfg.Ollama).Providers
		model := cfg.Model
		if cfg.Ollama.Model != "" {
			model = cfg.Ollama.Model
		}
		if model != "" {
			ocCfg.Model = "ollama/" + model
		} else {
			ocCfg.Model = ""
		}
	}

	data, jsonErr := json.Marshal(ocCfg)
	if jsonErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("marshal opencode config: %w", jsonErr)
	}

	configPath := filepath.Join(openCodeDir, "opencode.json")
	if writeErr := os.WriteFile(configPath, data, 0o600); writeErr != nil {
		cleanupFn()
		return "", func() {}, fmt.Errorf("write opencode config: %w", writeErr)
	}

	return tmpDir, cleanupFn, nil
}

// buildOpenCodeRunEnv constructs the subprocess environment with XDG_CONFIG_HOME
// overridden to xdgConfigHome. Any existing XDG_CONFIG_HOME entry in base is
// replaced so that opencode reads the ephemeral config rather than the user's
// real config directory.
func buildOpenCodeRunEnv(base []string, xdgConfigHome string) []string {
	env := make([]string, 0, len(base)+1)
	for _, e := range base {
		if !strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
			env = append(env, e)
		}
	}
	return append(env, "XDG_CONFIG_HOME="+xdgConfigHome)
}

// checkOpenCodeRunRequiredOptions returns ErrUnsupportedOption if any option
// in cfg.RequiredOptions is not supported by the opencode-run backend.
func checkOpenCodeRunRequiredOptions(cfg llmclient.RequestConfig) error { //nolint:gocritic // RequestConfig is passed by value throughout option helpers.
	return llmclient.EnforceRequired(cfg, openCodeRunStaticCapabilities(""))
}

// openCodeRunStaticCapabilities returns the static capabilities advertised for
// the opencode-run backend. version is the detected CLI version string (may be
// empty).
func openCodeRunStaticCapabilities(version string) llmclient.Capabilities {
	return llmclient.Capabilities{
		Backend:       "opencode-run",
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
			llmclient.OptionJSONSchema:         llmclient.OptionSupportPartial,
			llmclient.OptionRawCapture:         llmclient.OptionSupportFull,
		},
	}
}

func openCodeRunFidelity(cfg llmclient.RequestConfig, streaming llmclient.StreamingFidelity) *llmclient.Fidelity { //nolint:gocritic // RequestConfig is passed by value throughout fidelity helpers.
	results := make(map[llmclient.OptionName]llmclient.OptionResult)
	if cfg.Model != "" {
		results[llmclient.OptionModel] = llmclient.OptionApplied
	}
	if cfg.Ollama != nil {
		results[llmclient.OptionOllama] = llmclient.OptionApplied
	}
	optionResults := mergeOptionResults(results, executionOptionResults(cfg, openCodeRunStaticCapabilities("")))
	return populateJSONSchemaFidelity(&llmclient.Fidelity{
		Streaming:     streaming,
		ToolControl:   llmclient.ToolControlNone,
		OptionResults: optionResults,
	}, cfg)
}
