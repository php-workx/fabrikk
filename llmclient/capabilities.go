package llmclient

// StreamingFidelity describes how incrementally a backend delivers events.
type StreamingFidelity string

const (
	// StreamingBufferedOnly means the backend buffers the entire response and
	// delivers it as a single done event. No incremental output.
	StreamingBufferedOnly StreamingFidelity = "buffered_only"

	// StreamingTextChunk means the backend delivers incremental plain-text
	// chunks but does not provide structured events (no tool call streaming,
	// no thinking blocks, no content-block boundaries).
	StreamingTextChunk StreamingFidelity = "text_chunk"

	// StreamingStructured means the backend delivers fully normalized
	// structured events: text_start/delta/end, thinking_start/delta/end,
	// toolcall_start/delta/end, and done with usage.
	StreamingStructured StreamingFidelity = "structured"

	// StreamingStructuredUnknown means the transport streams but the event
	// schema has not been fully mapped yet (e.g. OpenCode serve before its
	// event schema is pinned). Treat conservatively: prefer buffered_only
	// semantics until the schema is confirmed.
	StreamingStructuredUnknown StreamingFidelity = "structured_unknown"
)

// ToolControlMode describes which party owns tool execution for a stream.
type ToolControlMode string

const (
	// ToolControlNone means the backend does not expose tool events. Tool
	// calls are opaque (executed internally by the CLI) or unavailable.
	ToolControlNone ToolControlMode = "none"

	// ToolControlBuiltIn means the CLI owns tool execution. Tool call events
	// (toolcall_start/end) are observable but the host cannot intercept them.
	ToolControlBuiltIn ToolControlMode = "built_in_cli_tools"

	// ToolControlHost means the host defines, approves, executes, and returns
	// tool results. Full structured tool-call streaming is required.
	ToolControlHost ToolControlMode = "host_controlled"
)

// OptionSupport indicates the degree to which a backend supports a given
// option. Used in Capabilities.OptionSupport to advertise static support
// before a request is made.
type OptionSupport string

const (
	// OptionSupportFull means the option is fully applied without degradation.
	OptionSupportFull OptionSupport = "full"

	// OptionSupportPartial means the option is accepted but may be degraded
	// (e.g. temperature clamped, model aliased). The start event carries the
	// actual applied value in Fidelity.OptionResults.
	OptionSupportPartial OptionSupport = "partial"

	// OptionSupportNone means the option is not supported by this backend.
	// Required options will cause Stream to return an error before spawning.
	OptionSupportNone OptionSupport = "none"
)

// Capabilities describes the static and probed capabilities of a backend.
// Returned by backend Probe/Capabilities calls during backend selection.
type Capabilities struct {
	// Backend is the stable backend name, e.g. "claude", "codex".
	Backend string `json:"backend"`

	// Version is the detected CLI version string, e.g. "1.2.3".
	Version string `json:"version,omitempty"`

	// Streaming describes the event fidelity delivered by this backend.
	Streaming StreamingFidelity `json:"streaming"`

	// ToolEvents reports whether the backend emits structured tool-call events.
	ToolEvents bool `json:"toolEvents"`

	// HostToolDefs reports whether the backend accepts host-defined tool
	// definitions (WithHostTools option).
	HostToolDefs bool `json:"hostToolDefs"`

	// HostToolApproval reports whether the host can approve or reject tool
	// calls before the CLI executes them.
	HostToolApproval bool `json:"hostToolApproval"`

	// ToolResultInjection reports whether the host can inject tool results
	// back into the conversation mid-turn.
	ToolResultInjection bool `json:"toolResultInjection"`

	// MultiTurn reports whether the backend supports session resumption
	// (WithSession option).
	MultiTurn bool `json:"multiTurn"`

	// Thinking reports whether the backend surfaces model thinking blocks.
	Thinking bool `json:"thinking"`

	// Usage reports whether the backend delivers token usage in done events.
	Usage bool `json:"usage"`

	// Models is the list of model identifiers this backend can route to.
	// Empty means the backend does not enumerate its supported models.
	Models []string `json:"models,omitempty"`

	// MaxContextTokens is the context window size in tokens, or 0 if unknown.
	MaxContextTokens int `json:"maxContextTokens,omitempty"`

	// OllamaRouting reports whether the backend supports the WithOllama option.
	OllamaRouting bool `json:"ollamaRouting"`

	// OptionSupport maps each known OptionName to its support level for this
	// backend. Options not present in the map are treated as OptionSupportNone.
	OptionSupport map[OptionName]OptionSupport `json:"optionSupport,omitempty"`

	// ProbeWarnings carries any non-fatal warnings discovered during probing,
	// e.g. version too old, TTY detection issue, model not reachable.
	ProbeWarnings []string `json:"probeWarnings,omitempty"`
}

// Fidelity is set on the EventStart event. It tells the caller which
// capabilities are active for the current stream and how each requested option
// was resolved.
type Fidelity struct {
	// Streaming describes the event fidelity for this stream.
	Streaming StreamingFidelity `json:"streaming"`

	// ToolControl describes which party owns tool execution for this stream.
	ToolControl ToolControlMode `json:"toolControl"`

	// OptionResults maps each option that was passed to Stream to its
	// resolution: applied, ignored, degraded, or unsupported.
	OptionResults map[OptionName]OptionResult `json:"optionResults,omitempty"`

	// Warnings carries any non-fatal warnings for this stream, e.g.
	// temperature clamped, model aliased, host tool ignored.
	Warnings []string `json:"warnings,omitempty"`
}
