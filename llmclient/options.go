package llmclient

import (
	"context"
	"errors"
	"strings"
	"time"
)

// OptionName identifies a functional option. Used in RequiredOptions,
// Capabilities.OptionSupport, and Fidelity.OptionResults.
type OptionName string

// Well-known option names corresponding to the WithXxx constructors.
const (
	OptionModel        OptionName = "model"
	OptionSession      OptionName = "session"
	OptionTemperature  OptionName = "temperature"
	OptionOllama       OptionName = "ollama"
	OptionCodexProfile OptionName = "codexProfile"
	OptionCodexJSONL   OptionName = "codexJSONL"
	OptionJSONSchema   OptionName = "jsonSchema"
	OptionHostTools    OptionName = "hostTools"
	OptionOpenCodePort OptionName = "openCodePort"

	OptionWorkingDirectory   OptionName = "workingDirectory"
	OptionEnvironment        OptionName = "environment"
	OptionEnvironmentOverlay OptionName = "environmentOverlay"
	OptionTimeout            OptionName = "timeout"
	OptionReasoningEffort    OptionName = "reasoningEffort"
	OptionRawCapture         OptionName = "rawCapture"
	OptionHostToolResponder  OptionName = "hostToolResponder"
)

// RawStream identifies which subprocess stream produced a raw captured chunk.
type RawStream string

const (
	// RawStreamStdout identifies bytes captured from subprocess stdout.
	RawStreamStdout RawStream = "stdout"
	// RawStreamStderr identifies bytes captured from subprocess stderr.
	RawStreamStderr RawStream = "stderr"
)

// RawCaptureFunc receives cloned stdout/stderr chunks from subprocess
// backends before llmcli normalizes stdout into events or trims stderr tails.
type RawCaptureFunc func(stream RawStream, data []byte)

// HostToolResponder is a callback invoked by the omp-rpc backend when the
// model requests execution of a host-defined tool. The callback receives the
// context (which is cancelled if the stream is cancelled) and the ToolCall
// describing the requested invocation. It returns the result content blocks,
// a flag indicating whether the result is an error, and any transport-level
// error that should terminate the stream. Returning (nil, true, nil) sends a
// generic refusal; returning (blocks, false, nil) sends a successful result.
type HostToolResponder func(ctx context.Context, call ToolCall) (result []ContentBlock, isError bool, err error)

// OptionResult is returned in Fidelity.OptionResults to report how the
// backend handled each option passed to Stream.
type OptionResult string

const (
	// OptionApplied means the option was fully applied without any degradation.
	OptionApplied OptionResult = "applied"

	// OptionIgnored means the option was silently ignored. Only valid for
	// best-effort options (not for options listed in RequiredOptions).
	OptionIgnored OptionResult = "ignored"

	// OptionDegraded means the option was accepted but applied with reduced
	// fidelity, e.g. temperature clamped, model aliased.
	OptionDegraded OptionResult = "degraded"

	// OptionUnsupported means the backend does not support this option at all.
	OptionUnsupported OptionResult = "unsupported"
)

// OptionPolicy controls how an unsupported option is handled when the caller
// has flagged it as required via WithRequiredOptions.
type OptionPolicy string

const (
	// OptionBestEffort means unsupported or degraded options are reported in
	// the start event's Fidelity.OptionResults but do not fail the stream.
	OptionBestEffort OptionPolicy = "best_effort"

	// OptionRequired means that if a required option cannot be applied
	// (OptionUnsupported), Stream returns ErrUnsupportedOption before spawning.
	OptionRequired OptionPolicy = "required"
)

// ErrUnsupportedOption is returned by Stream when a required option is not
// supported by the backend.
var ErrUnsupportedOption = errors.New("llmclient: required option not supported by backend")

// UnsupportedOptionError carries the backend name and the specific option names
// that caused the failure. It satisfies errors.Is(err, ErrUnsupportedOption).
type UnsupportedOptionError struct {
	Backend string
	Options []OptionName
}

func (e *UnsupportedOptionError) Error() string {
	names := make([]string, len(e.Options))
	for i, n := range e.Options {
		names[i] = string(n)
	}
	return "llmclient: backend " + e.Backend + " does not support required options: " + strings.Join(names, ", ")
}

// Is satisfies errors.Is(err, ErrUnsupportedOption).
func (e *UnsupportedOptionError) Is(target error) bool { return target == ErrUnsupportedOption }

// Unwrap returns ErrUnsupportedOption so callers using errors.Is on the sentinel still match.
func (e *UnsupportedOptionError) Unwrap() error { return ErrUnsupportedOption }

// NewUnsupportedOptionError constructs an UnsupportedOptionError for the given
// backend and option names.
func NewUnsupportedOptionError(backend string, names ...OptionName) *UnsupportedOptionError {
	return &UnsupportedOptionError{Backend: backend, Options: names}
}

// Option is a functional option applied to a Stream call. Options configure
// model selection, session continuity, temperature, and backend-specific
// features like Ollama routing and host-defined tools.
type Option func(*RequestConfig)

// RequestConfig holds all parameters derived from Option values. Backends
// copy the defaults and apply the caller's options.
type RequestConfig struct {
	// Model overrides the default model for this request.
	Model string

	// SessionID continues an existing session. Corresponds to WithSession.
	SessionID string

	// Temperature sets the sampling temperature. NaN means "use the default".
	Temperature float64

	// TemperatureSet is true when the caller explicitly set Temperature via
	// WithTemperature. Required to distinguish 0.0 from "not set".
	TemperatureSet bool

	// Ollama, when non-nil, routes this request through an Ollama instance.
	Ollama *OllamaConfig

	// CodexProfile selects a named Codex config profile.
	CodexProfile string

	// CodexJSONL requests Codex exec JSONL stdout mode for callers that need
	// raw Codex events while still receiving normalized llmclient events.
	CodexJSONL bool

	// JSONSchema requests JSON-shaped output matching this schema. Backends
	// that cannot enforce schemas natively report advisory or ignored fidelity.
	JSONSchema map[string]interface{}

	// HostTools is the list of host-defined tools injected for this request.
	HostTools []Tool

	// OpenCodePort is the port for an existing OpenCode serve process.
	OpenCodePort int

	// WorkingDirectory is the project/worktree directory for subprocess
	// backends that support per-call working-directory control.
	WorkingDirectory string

	// Environment is the complete subprocess environment when EnvironmentSet
	// is true. It is copied before storage and before subprocess execution.
	Environment []string

	// EnvironmentSet distinguishes an explicitly empty replacement environment
	// from the default "inherit os.Environ()" behavior.
	EnvironmentSet bool

	// EnvironmentOverlay is applied over Environment or os.Environ before
	// backend-required environment overrides.
	EnvironmentOverlay map[string]string

	// Timeout is a per-request deadline layered on top of the caller context.
	Timeout time.Duration

	// ReasoningEffort requests a backend-specific reasoning effort level.
	ReasoningEffort string

	// RawCapture receives cloned stdout/stderr chunks from subprocess backends.
	RawCapture RawCaptureFunc

	// HostToolResponder is called by the omp-rpc backend when the model
	// requests execution of a host-defined tool. When nil and a host_tool_call
	// arrives, the backend sends an error result to unblock the model turn.
	HostToolResponder HostToolResponder

	// RequiredOptions is the set of options that must be applied for the
	// request to proceed. If any are unsupported, Stream returns
	// ErrUnsupportedOption before spawning.
	RequiredOptions map[OptionName]struct{}
}

// DefaultRequestConfig returns a RequestConfig with sensible zero values.
// Backends should copy this and then apply the caller's options.
func DefaultRequestConfig() RequestConfig {
	return RequestConfig{
		RequiredOptions: make(map[OptionName]struct{}),
	}
}

// WithRequiredOptions marks the listed options as required. If a backend does
// not support any of the listed options, Stream will return ErrUnsupportedOption
// before spawning any subprocess.
func WithRequiredOptions(names ...OptionName) Option {
	return func(cfg *RequestConfig) {
		for _, name := range names {
			cfg.RequiredOptions[name] = struct{}{}
		}
	}
}

// WithModel overrides the model for this request. Recognized by all backends.
func WithModel(model string) Option {
	return func(cfg *RequestConfig) {
		cfg.Model = model
	}
}

// WithSession continues an existing session identified by id. Recognized by
// backends that support multi-turn sessions (e.g. Claude with --resume).
func WithSession(id string) Option {
	return func(cfg *RequestConfig) {
		cfg.SessionID = id
	}
}

// WithTemperature sets the sampling temperature for this request. Recognized
// by backends that expose temperature control.
func WithTemperature(t float64) Option {
	return func(cfg *RequestConfig) {
		cfg.Temperature = t
		cfg.TemperatureSet = true
	}
}

// WithOllama routes the request through an Ollama instance. When set, the
// backend injects the appropriate environment variables or config before
// spawning the subprocess. Recognized by Claude, Codex, OpenCode, and omp
// backends.
func WithOllama(cfg OllamaConfig) Option {
	return func(rc *RequestConfig) {
		rc.Ollama = &cfg
	}
}

// WithCodexProfile selects a named Codex config profile. No llmcli backend
// currently advertises end-to-end support; backends report it unsupported
// until runtime behavior is wired and verified.
func WithCodexProfile(name string) Option {
	return func(cfg *RequestConfig) {
		cfg.CodexProfile = name
	}
}

// WithCodexJSONL requests Codex exec JSONL stdout mode when supported.
func WithCodexJSONL(enabled bool) Option {
	return func(cfg *RequestConfig) {
		cfg.CodexJSONL = enabled
	}
}

// WithJSONSchema requests JSON-shaped output matching schema. The schema is
// copied before storage. Current llmcli backends report this as advisory
// fidelity rather than native enforcement.
func WithJSONSchema(schema map[string]interface{}) Option {
	return func(cfg *RequestConfig) {
		cfg.JSONSchema = cloneInterfaceMap(schema)
	}
}

// WithHostTools injects custom tool definitions to be executed by the host
// rather than the CLI. Recognized by omp RPC backend; reported unsupported by
// others.
func WithHostTools(tools []Tool) Option {
	return func(cfg *RequestConfig) {
		cfg.HostTools = cloneTools(tools)
	}
}

// WithOpenCodePort specifies the port of an already-running OpenCode serve
// process. Recognized by the OpenCode serve backend; reported unsupported by
// others.
func WithOpenCodePort(port int) Option {
	return func(cfg *RequestConfig) {
		cfg.OpenCodePort = port
	}
}

// WithWorkingDirectory sets the project/worktree directory for backends that
// support per-request working-directory control.
func WithWorkingDirectory(dir string) Option {
	return func(cfg *RequestConfig) {
		cfg.WorkingDirectory = dir
	}
}

// WithEnvironment replaces the subprocess environment for backends that
// support per-request environment control. The input slice is copied.
func WithEnvironment(env []string) Option {
	return func(cfg *RequestConfig) {
		cfg.Environment = append([]string(nil), env...)
		cfg.EnvironmentSet = true
	}
}

// WithEnvironmentOverlay overlays key/value pairs onto the base subprocess
// environment for backends that support environment control. The input map is
// copied.
func WithEnvironmentOverlay(env map[string]string) Option {
	return func(cfg *RequestConfig) {
		cfg.EnvironmentOverlay = copyStringMap(env)
	}
}

// WithTimeout adds a per-request timeout layered on top of the caller context.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *RequestConfig) {
		cfg.Timeout = timeout
	}
}

// WithReasoningEffort requests a backend-specific reasoning effort level.
func WithReasoningEffort(level string) Option {
	return func(cfg *RequestConfig) {
		cfg.ReasoningEffort = level
	}
}

// WithRawCapture registers a synchronous callback for raw stdout/stderr chunks
// from subprocess backends.
func WithRawCapture(capture RawCaptureFunc) Option {
	return func(cfg *RequestConfig) {
		cfg.RawCapture = capture
	}
}

// WithHostToolResponder registers a callback invoked by the omp-rpc backend
// when the model requests execution of a host-defined tool. Passing nil clears
// any previously set responder.
func WithHostToolResponder(fn HostToolResponder) Option {
	return func(cfg *RequestConfig) {
		cfg.HostToolResponder = fn
	}
}

// EnforceRequired validates that every option listed in cfg.RequiredOptions is
// supported (OptionSupportFull or OptionSupportPartial) by the capabilities
// advertised in caps. If any required option maps to OptionSupportNone or is
// absent from caps.OptionSupport, ErrUnsupportedOption is returned.
//
// Backends should call EnforceRequired after ApplyOptions and before spawning
// any subprocess, so that callers using WithRequiredOptions get a fast, clean
// failure rather than a subprocess error.
func EnforceRequired(cfg RequestConfig, caps Capabilities) error { //nolint:gocritic // public API takes RequestConfig by value for copy-on-apply semantics.
	for name := range cfg.RequiredOptions {
		support, ok := caps.OptionSupport[name]
		if !ok || support == OptionSupportNone {
			return ErrUnsupportedOption
		}
	}
	return nil
}

// ApplyOptions applies opts to a copy of base and returns the resulting
// RequestConfig. Backends call this at the start of Stream.
func ApplyOptions(base RequestConfig, opts []Option) RequestConfig { //nolint:gocritic // public API intentionally copies RequestConfig values.
	cfg := base
	cfg.Environment = append([]string(nil), base.Environment...)
	cfg.EnvironmentOverlay = copyStringMap(base.EnvironmentOverlay)
	cfg.HostTools = cloneTools(base.HostTools)
	cfg.JSONSchema = cloneInterfaceMap(base.JSONSchema)
	if base.Ollama != nil {
		ollama := *base.Ollama
		cfg.Ollama = &ollama
	}
	if base.RequiredOptions == nil {
		cfg.RequiredOptions = make(map[OptionName]struct{})
	} else {
		// Shallow-copy the map so the caller's defaults are not mutated.
		cfg.RequiredOptions = make(map[OptionName]struct{}, len(base.RequiredOptions))
		for k, v := range base.RequiredOptions {
			cfg.RequiredOptions[k] = v
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func cloneTools(tools []Tool) []Tool {
	if tools == nil {
		return nil
	}
	out := make([]Tool, len(tools))
	for i := range tools {
		out[i] = tools[i]
		if tools[i].InputSchema != nil {
			out[i].InputSchema = make(map[string]interface{}, len(tools[i].InputSchema))
			for key, value := range tools[i].InputSchema {
				out[i].InputSchema[key] = value
			}
		}
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInterfaceMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = cloneInterfaceValue(value)
	}
	return out
}

func cloneInterfaceValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneInterfaceMap(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = cloneInterfaceValue(v[i])
		}
		return out
	default:
		return v
	}
}
