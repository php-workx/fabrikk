# LLM CLI Backend — Shell-Out Alternative

Technical specification for `llmcli`, a client backend that shells out to existing
AI coding CLI tools (`claude`, `codex`, `opencode`, `omp`) instead of calling HTTP
APIs directly.

**Date:** 2026-04-12
**Porting target:** Go package `github.com/php-workx/fabrikk/llmcli`
**Related specs:** [`llm-auth.md`](./llm-auth.md) (HTTP auth), [`llm-client.md`](./llm-client.md) (HTTP request assembly)

> **This is an alternative backend, not a replacement.** `llmcli` is swappable with
> `llmauth` + `llmclient` behind a common `Context → Stream` interface. Use it when
> the user already has a CLI tool installed and authenticated, and you don't want
> to reimplement auth/streaming in your own process.

---

## Table of Contents

- [Overview](#overview)
- [When to Use the CLI Backend](#when-to-use-the-cli-backend)
- [Architecture](#architecture)
- [Unified Interface](#unified-interface)
- [Event Types](#event-types)
- [CLI Tool Detection](#cli-tool-detection)
- [Supported CLI Tools](#supported-cli-tools)
  - [Claude Code CLI](#claude-code-cli)
  - [OpenAI Codex CLI](#openai-codex-cli)
  - [OpenCode CLI](#opencode-cli)
  - [omp CLI](#omp-cli)
- [Context → CLI Mapping](#context--cli-mapping)
- [Conversation State Model](#conversation-state-model)
- [Routing Through Ollama (Open-Source Models)](#routing-through-ollama-open-source-models)
- [Streaming Contract](#streaming-contract)
- [Event Stream Normalization](#event-stream-normalization)
- [Event Sequencing Rules](#event-sequencing-rules)
- [Backend Method Semantics](#backend-method-semantics)
- [Concurrency Model](#concurrency-model)
- [Persistent-Process Lifecycle](#persistent-process-lifecycle)
- [Subprocess Lifecycle](#subprocess-lifecycle)
- [Process Group and Signal Handling](#process-group-and-signal-handling)
- [Limitations](#limitations)
- [Backend Selection — Worked Example](#backend-selection--worked-example)
- [Environment Variable Reference](#environment-variable-reference)
- [Minimum CLI Version Requirements](#minimum-cli-version-requirements)
- [Cost and Usage Tracking](#cost-and-usage-tracking)
- [Metrics and Observability](#metrics-and-observability)
- [Testing Strategy](#testing-strategy)
- [Suggested Go Package Structure](#suggested-go-package-structure)
- [Implementation Checklist](#implementation-checklist)

---

## Overview

`llmcli` is a client backend that provides the same streaming LLM interface as
`llmclient`, but instead of making HTTP requests it **spawns a subprocess** of an
already-installed CLI tool and parses its output.

**Key properties:**

- ✅ **No auth code.** The CLI tool handles its own auth (OAuth, API keys, config files).
- ✅ **No provider HTTP auth code.** Direct-provider HTTP payloads, retries, and
  auth remain delegated to the CLI. OpenCode `serve` is the exception: it uses
  local HTTP/SSE transport and is schema-gated until `/doc` is mapped.
- ✅ **No identity headers.** The CLI tool sends whatever it sends.
- ✅ **No `llmauth` dependency.** This package is entirely standalone.
- ❌ **Limited event fidelity.** Some CLI tools only emit plain text; tool calls,
     thinking blocks, and token counts may not be available.
- ❌ **Cold start cost.** Spawning a subprocess per request is slower than HTTP.
- ❌ **Fragile output parsing.** Plain-text modes can't be structurally parsed.

---

## When to Use the CLI Backend

Use `llmcli` when:

- **The user already has a CLI installed and logged in.** Reuse their existing
  session instead of asking them to re-authenticate.
- **You want to avoid bundling auth logic.** Your tool stays simpler; the CLI is
  the "auth broker."
- **You're prototyping.** Running `claude -p "..."` is the fastest way to a working
  demo before investing in `llmauth`.
- **The user is behind corporate SSO.** The CLI may already be configured with
  enterprise credentials that are hard to replicate programmatically.
- **As a fallback.** Try `llmclient` + `llmauth` first; if no API key/OAuth
  credentials are available, detect installed CLIs and offer them.

**Do not use `llmcli` when:**

- You need full event fidelity (per-token deltas, thinking blocks, tool call JSON
  streaming, exact token usage).
- You need low latency per request (subprocess spawn overhead: 100-500ms).
- You need to run many concurrent requests (process-per-request doesn't scale).
- You need structured tool calling and the CLI doesn't expose it.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                    Your Tool (plan/implement/review)              │
│                                                                   │
│  Context { systemPrompt, messages, tools }                        │
│                    │                                              │
│                    ▼                                              │
│  ┌──────────────────────────────────────────────────────┐         │
│  │ Backend Interface                                    │         │
│  │   Stream(Context) → EventStream                      │         │
│  └──┬────────────────────────────────────┬──────────────┘         │
│     │                                    │                        │
│     ▼                                    ▼                        │
│  ┌──────────┐                    ┌──────────────┐                 │
│  │llmclient │                    │  llmcli      │                 │
│  │  (HTTP)  │                    │  (subprocess)│                 │
│  │+ llmauth │                    │              │                 │
│  └────┬─────┘                    └──────┬───────┘                 │
│       │                                 │                         │
│       ▼                                 ▼                         │
│  ┌──────────────┐         ┌────────────────────────────────┐      │
│  │ Anthropic API│         │  claude / codex / opencode /    │      │
│  │ OpenAI API   │         │  omp subprocesses               │      │
│  │ Google API   │         │                                 │      │
│  └──────────────┘         └────────────────────────────────┘      │
└──────────────────────────────────────────────────────────────────┘
```

Both backends emit the same `Event` stream, so the caller can
pick one at runtime based on what's available.

---

## Unified Interface

`llmcli` and `llmclient` share the same Go interface, defined in
[`llm-client.md`](./llm-client.md):

```go
type Backend interface {
    // Stream processes a single request, returning a channel of events.
    // For per-call backends (Claude -p, Codex exec, omp print), each call
    // spawns a fresh subprocess. For persistent backends (omp RPC, Codex
    // app-server, OpenCode serve), it reuses the existing process.
    //
    // Stream returns immediately after spawning (per-call) or sending the
    // request (persistent). Events arrive on the channel as they are produced.
    // The channel is closed when the turn completes or an error occurs.
    Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error)

    // Name returns a stable identifier for this backend: "claude", "codex",
    // "codex-appserver", "opencode-run", "opencode-serve", "omp", "omp-rpc".
    Name() string

    // Available returns true if the backend can accept requests right now.
    // For per-call backends, this checks that the binary is on PATH.
    // For persistent backends, this also checks that the subprocess is alive.
    Available() bool

    // Close releases all resources held by the backend.
    // For persistent backends (omp RPC, Codex app-server, OpenCode serve),
    // this kills the subprocess. For per-call backends, this is a no-op.
    // Idempotent: safe to call multiple times.
    Close() error
}
```

**Functional options** solve the "unified interface vs backend-specific settings"
problem. Universal options are recognized by all backends; backend-specific
options are accepted only when the backend can either apply them or report an
explicit degradation. Backends **MUST NOT silently ignore** an option that the
caller marked as required.

```go
// Option is a functional option for Stream() calls.
type Option func(*requestConfig)

type OptionName string
type OptionResult string
type OptionSupport string

// OptionPolicy controls whether unsupported options are best-effort or fatal.
type OptionPolicy string

const (
    OptionBestEffort OptionPolicy = "best_effort" // report ignored/degraded in start metadata
    OptionRequired   OptionPolicy = "required"    // return ErrUnsupportedOption before streaming
)

const (
    OptionApplied     OptionResult = "applied"
    OptionIgnored     OptionResult = "ignored"
    OptionDegraded    OptionResult = "degraded"
    OptionUnsupported OptionResult = "unsupported"
)

// WithRequiredOptions makes unsupported selected options fail the request.
func WithRequiredOptions(names ...OptionName) Option { ... }

// --- Universal options (recognized by all backends) ---

// WithModel overrides the model for this request.
func WithModel(model string) Option { ... }

// WithSession continues an existing session instead of starting a new one.
func WithSession(id string) Option { ... }

// WithTemperature sets the sampling temperature.
func WithTemperature(t float64) Option { ... }

// --- Backend-specific options ---

// WithOllama routes the request through an Ollama instance instead of the
// direct provider. Recognized by: Claude, Codex, OpenCode backends.
func WithOllama(cfg OllamaConfig) Option { ... }

// WithCodexProfile selects a Codex config profile. Recognized by: Codex only.
func WithCodexProfile(name string) Option { ... }

// WithHostTools injects custom tool definitions that the host executes.
// Recognized by: omp RPC only.
func WithHostTools(tools []Tool) Option { ... }

// WithOpenCodePort sets the port for an existing OpenCode server.
// Recognized by: OpenCode serve only.
func WithOpenCodePort(port int) Option { ... }
```

**How each backend processes options:**

```go
func (b *ClaudeBackend) Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error) {
    cfg := defaultRequestConfig()
    for _, opt := range opts {
        opt(&cfg)
    }
    if unsupported := b.unsupportedRequiredOptions(cfg); len(unsupported) > 0 {
        return nil, ErrUnsupportedOption{Backend: b.Name(), Options: unsupported}
    }
    // ... build args from cfg, spawn subprocess ...
}
```

This means callers can write backend-agnostic code:

```go
// Works with ANY backend — unsupported best-effort options are reported in EventStart metadata.
stream, err := backend.Stream(ctx, input, WithModel("opus"), WithOllama(ollamaCfg))
```

When an option is best-effort and cannot be applied, the backend emits that fact
on the `start` event in `Event.Fidelity.OptionResults`. Callers that cannot
tolerate degradation use `WithRequiredOptions(...)` or select by probed
capabilities before calling `Stream()`.

**Option recognition matrix:**

| Option | Claude | Codex exec | Codex app-server | OpenCode run | OpenCode serve | omp print | omp RPC |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| `WithModel` | ✅ | ✅ | ✅ | ⚠️ config | ⚠️ config | ✅ | ✅ |
| `WithSession` | ✅ `--resume` | ❌ | ✅ thread | ❌ | ✅ session | ✅ `--resume` | ✅ |
| `WithOllama` | ✅ env vars | ✅ `--oss`/profile | ✅ env vars | ✅ config | ✅ config | ✅ env vars | ✅ env vars |
| `WithCodexProfile` | ❌ | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ |
| `WithHostTools` | ❌ | ❌ | ❌ until protocol verified | ❌ | ❌ | ❌ | ✅ |
| `WithOpenCodePort` | ❌ | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| `WithTemperature` | ❌ | ❌ | ❌ | ❌ | ❌ | ⚠️ config | ⚠️ config |

**Option result contract:**

| Result | Meaning |
|---|---|
| `applied` | Backend applied the option as requested |
| `ignored` | Backend does not support the option and it was best-effort |
| `degraded` | Backend applied a weaker equivalent, such as config-file model selection instead of a direct flag |
| `unsupported` | Backend cannot apply the option; returned as an error when required |

Required unsupported options fail before the subprocess is spawned. Best-effort
ignored/degraded options are reported in `EventStart` metadata and should also be
logged at `warn`.

**Context** (same type as `llmclient`):
```go
type Context struct {
    SystemPrompt string     // may or may not be used depending on CLI
    Messages     []Message  // conversation history
    Tools        []Tool     // often ignored — most CLIs don't accept tool defs
}
```

**Event stream** — same `Event` type as `llmclient` (see [Event Types](#event-types)
below). CLIs with structured output (Claude stream-json, omp `--mode json`) can
emit all event types. CLIs with plain-text output degrade to `text_delta` + `done`.

**Model names are passed through verbatim.** The `WithModel("claude-sonnet-4-20250514")`
option sends that exact string to the CLI's `--model` flag. The caller is
responsible for knowing each backend's model naming convention. See per-backend
sections for valid model name examples.

---

## CLI Tool Detection

Detect available CLIs at startup using `exec.LookPath`:

```go
type CliInfo struct {
    Name    string
    Binary  string
    Path    string  // resolved absolute path from LookPath
    Version string  // captured from `--version` output
}

func DetectAvailable() []CliInfo {
    candidates := []struct{ name, bin string }{
        {"claude", "claude"},
        {"codex", "codex"},
        {"opencode", "opencode"},
        {"omp", "omp"},
    }
    var out []CliInfo
    for _, c := range candidates {
        path, err := exec.LookPath(c.bin)
        if err != nil {
            continue
        }
        out = append(out, CliInfo{
            Name:    c.name,
            Binary:  c.bin,
            Path:    path,
            Version: probeVersion(path),
        })
    }
    return out
}
```

**Priority order** (configurable, default below):
1. `claude` — stream-json is structurally parseable, widely available
2. `codex` (app-server) — JSON-RPC, full fidelity
3. `opencode` (serve) — HTTP+SSE, supports multi-process sharing
4. `codex-exec` / `opencode-run` — text fallbacks
5. `omp` — **INTERNAL USE ONLY** (fabrikk maintainers' dev environment; richest JSON but not publicly distributed)

The caller should pick whichever is both available and meets its feature needs.

### Backend selection

`SelectBackend` picks the highest-priority backend that meets the caller's
requirements:

```go
// BackendPreference influences which CLI is chosen when multiple are available.
type BackendPreference int

const (
    PreferAuto    BackendPreference = iota  // pick best available (default priority order)
    PreferClaude                              // prefer Claude Code stream-json
    PreferCodex                               // prefer Codex (app-server if available, else exec)
    PreferOpenCode                            // prefer OpenCode (serve if available, else run)
    PreferOmp                                 // prefer omp (RPC if available, else print)
)

// Requirements describes what features the caller needs from a backend.
// Backends that don't meet requirements are filtered out before preference applies.
type Requirements struct {
    NeedsToolEvents       bool                // requires structured toolcall events, even if built-in CLI tools execute them
    NeedsHostToolDefs     bool                // requires host-defined tool schemas
    NeedsHostToolApproval bool                // requires host-side approval before tool execution
    NeedsToolResults      bool                // requires host to send tool results back to the model
    NeedsMultiTurn        bool                // requires session continuity
    MinStreaming          StreamingFidelity   // minimum streaming fidelity required
    NeedsThinking         bool                // requires thinking_* events
    NeedsUsage            bool                // requires token usage reporting
    Model                 string              // optional requested model; strict selection probes availability
    MinContextTokens      int                 // optional context-window requirement
    NeedsOllama           bool                // requires Ollama routing to work for this backend/model
    StrictProbe           bool                // run expensive capability probes before returning a backend
    PreferredOrder        []BackendPreference // optional ordered fallback list
}

type StreamingFidelity string

const (
    StreamingBufferedOnly       StreamingFidelity = "buffered_only"        // no incremental output
    StreamingTextChunk          StreamingFidelity = "text_chunk"           // incremental plain text only
    StreamingStructured         StreamingFidelity = "structured"           // normalized structured events
    StreamingStructuredUnknown  StreamingFidelity = "structured_unknown"   // transport streams, schema not mapped yet
)

// SelectBackend returns the highest-priority backend that meets the requirements.
// Returns ErrNoBackendAvailable if no installed CLI meets the requirements.
func SelectBackend(ctx context.Context, req Requirements) (Backend, error) {
    detected := DetectAvailable()
    candidates := filterByStaticRequirements(detected, req)
    if req.StrictProbe {
        candidates = filterByProbe(ctx, candidates, req)
    }
    return pickByPreference(candidates, req.PreferredOrder)
}

// MustSelectBackend panics if no backend is available. Convenience for tests.
func MustSelectBackend(ctx context.Context, req Requirements) Backend {
    b, err := SelectBackend(ctx, req)
    if err != nil {
        panic(err)
    }
    return b
}

// SelectBackendByName forces a specific backend by name.
// Returns ErrBackendNotFound if the named CLI is not on PATH.
func SelectBackendByName(name string) (Backend, error) { ... }
```

**Default priority order** (when `PreferAuto`):
1. `claude` — stream-json, block-level streaming, widely available
2. `codex-app-server` — JSON-RPC, full fidelity if available
3. `opencode-serve` — HTTP+SSE, supports multi-process sharing
4. `codex-exec` — text output, no structured events
5. `opencode-run` — text output, no structured events
6. `omp` — **INTERNAL USE ONLY** — JSONL, host tools, full fidelity (not publicly distributed)
7. `omp-rpc` — **INTERNAL USE ONLY** — bidirectional JSONL, host tools (not publicly distributed)

**Feature-based filtering** (`Requirements`):

| Requirement | Filters out |
|---|---|
| `NeedsToolEvents` | `codex-exec`, `opencode-run`; `opencode-serve` until event schema is pinned |
| `NeedsHostToolDefs` | Everything except backends with a verified host-tool-definition protocol |
| `NeedsHostToolApproval` | Everything except `omp-rpc` |
| `NeedsToolResults` | Everything except `omp-rpc` unless Codex app-server host-result injection is verified |
| `NeedsMultiTurn` | `codex-exec`, `opencode-run` |
| `MinStreaming=structured` | `codex-exec`, `opencode-run`, and `opencode-serve` until its SSE schema is mapped |
| `MinStreaming=text_chunk` | Buffered modes; `codex-exec`/`opencode-run` only pass if a pipe-mode probe proves incremental output |
| `NeedsThinking` | Text-only modes and `opencode-serve` until mapped |
| `NeedsUsage` | Text-only modes and `opencode-serve` until mapped |
| `NeedsOllama` | Backends whose routing probe cannot verify the requested model/provider route |

**Tool capability split:** `ToolEvents` means the backend can report that a tool
was used. It does not mean the host controls that tool. `HostToolDefs`,
`HostToolApproval`, and `ToolResultInjection` are separate capabilities because
Claude, Codex, and OpenCode may execute built-in tools inside the CLI subprocess
before `llmcli` sees the event. Only `omp-rpc` is currently specified as
supporting host-defined tools with host-side approval and result injection.

**Runtime probing:** `Available()` and `DetectAvailable()` are static, cheap
checks. They do not prove auth state, model availability, app-server support,
context window size, Ollama routing, or pipe-mode streaming behavior. Strict
selection uses `Probe(ctx)`/`Capabilities(ctx)` before returning a backend:

```go
type Capabilities struct {
    Backend              string
    Version              string
    Streaming            StreamingFidelity
    ToolEvents           bool
    HostToolDefs         bool
    HostToolApproval     bool
    ToolResultInjection  bool
    MultiTurn            bool
    Thinking             bool
    Usage                bool
    Models               []string
    MaxContextTokens     int
    OllamaRouting        bool
    OptionSupport        map[OptionName]OptionSupport
    ProbeWarnings        []string
}

func (b *ClaudeBackend) Capabilities(ctx context.Context) (Capabilities, error) { ... }
```

Probes may be expensive and may fail due to auth or network state. A failed
strict probe excludes the backend from `SelectBackend`. In non-strict mode, the
backend may remain a candidate, but the returned backend must still report
degradations through `EventStart` metadata.

All CLI backends emit the same `Event` type, matching the one defined in
[`llm-client.md`](./llm-client.md). Every event is a discriminated union on
`Type` — consumers switch on `Type` and access the relevant fields.

```go
type EventType string

const (
    EventStart         EventType = "start"          // session initialized
    EventTextStart      EventType = "text_start"      // text content block begins
    EventTextDelta      EventType = "text_delta"      // incremental text chunk
    EventTextEnd        EventType = "text_end"        // text content block ends
    EventThinkingStart  EventType = "thinking_start"  // thinking block begins
    EventThinkingDelta  EventType = "thinking_delta"  // incremental thinking chunk
    EventThinkingEnd    EventType = "thinking_end"    // thinking block ends
    EventToolCallStart  EventType = "toolcall_start"   // tool call begins (name + partial args)
    EventToolCallDelta  EventType = "toolcall_delta"  // incremental tool call arguments
    EventToolCallEnd    EventType = "toolcall_end"     // tool call complete (full args)
    EventDone           EventType = "done"             // turn complete
    EventError          EventType = "error"            // unrecoverable error
)

type StopReason string

const (
    StopEndTurn      StopReason = "end_turn"       // model finished normally
    StopMaxTokens    StopReason = "max_tokens"     // hit token limit
    StopToolUse      StopReason = "tool_use"       // model wants to call a tool
    StopCancelled    StopReason = "cancelled"      // user cancelled
)

// Event is the unified event type emitted by all backends (both llmclient and llmcli).
// Consumers range over the channel and switch on Type.
type Event struct {
    Type         EventType `json:"type"`

    // Content block index — tracks which content block this event belongs to.
    // Incremented for each new content block within a turn (text, thinking, tool call).
    ContentIndex int       `json:"contentIndex,omitempty"`

    // Delta is the incremental text or arguments chunk for delta events.
    Delta        string    `json:"delta,omitempty"`

    // Content is the full, accumulated text of the content block (set on *_end events).
    Content      string    `json:"content,omitempty"`

    // ToolCall carries the parsed tool call on toolcall_end events.
    ToolCall     *ToolCall `json:"toolCall,omitempty"`

    // Partial carries an incomplete AssistantMessage during streaming (optional).
    Partial      *AssistantMessage `json:"partial,omitempty"`

    // Message carries the complete AssistantMessage on the done event.
    Message      *AssistantMessage `json:"message,omitempty"`

    // ErrorMessage carries a human-readable error description on error events.
    ErrorMessage string     `json:"errorMessage,omitempty"`

    // Reason is set on done events to indicate why the turn ended.
    Reason       StopReason `json:"reason,omitempty"`

    // Usage carries token usage metadata when available.
    Usage        *Usage     `json:"usage,omitempty"`

    // SessionID is set on the start event. The caller stores it for multi-turn
    // continuation (see Conversation State Model).
    SessionID    string    `json:"sessionId,omitempty"`

    // Fidelity is set on the start event. It tells the caller which requested
    // options/capabilities were applied, ignored, or degraded for this stream.
    Fidelity     *Fidelity `json:"fidelity,omitempty"`
}

type Fidelity struct {
    Streaming     StreamingFidelity          `json:"streaming"`
    ToolControl   ToolControlMode            `json:"toolControl"`
    OptionResults map[OptionName]OptionResult `json:"optionResults,omitempty"`
    Warnings      []string                   `json:"warnings,omitempty"`
}

type ToolControlMode string

const (
    ToolControlNone       ToolControlMode = "none"
    ToolControlBuiltIn    ToolControlMode = "built_in_cli_tools" // events observed after/while CLI owns execution
    ToolControlHost       ToolControlMode = "host_controlled"    // host defines, approves, executes, and returns results
)
```

### Supporting types

```go
// ToolCall represents a model's request to invoke a tool.
type ToolCall struct {
    ID        string                 `json:"id"`        // unique tool call ID (e.g., "toolu_01ABC")
    Name      string                 `json:"name"`      // tool name (e.g., "Read", "Bash")
    Arguments map[string]interface{} `json:"arguments"` // parsed arguments
}

// AssistantMessage is the complete response from a turn. Carried by the done event.
type AssistantMessage struct {
    ID      string         `json:"id"`
    Role    string         `json:"role"` // always "assistant"
    Content []ContentBlock `json:"content"`
    Model   string         `json:"model,omitempty"`
    StopReason StopReason  `json:"stopReason,omitempty"`
    Usage   *Usage         `json:"usage,omitempty"`
}

// ContentBlock is a discriminated union: text, thinking, or tool use.
type ContentBlock struct {
    Type     ContentType `json:"type"` // "text" | "thinking" | "tool_use"

    // Text content (for type "text" and "thinking")
    Text     string      `json:"text,omitempty"`

    // Tool use fields (for type "tool_use")
    ToolCallID string                 `json:"toolCallId,omitempty"`
    ToolName   string                 `json:"toolName,omitempty"`
    Arguments  map[string]interface{} `json:"arguments,omitempty"`
}

type ContentType string

const (
    ContentText      ContentType = "text"
    ContentThinking  ContentType = "thinking"
    ContentToolUse   ContentType = "tool_use"
)

// Usage carries token usage metadata (when available from the CLI).
type Usage struct {
    InputTokens  int `json:"inputTokens"`
    OutputTokens int `json:"outputTokens"`
    CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
    CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}
```

### Message type (Context input)

```go
// Message is a single message in the conversation history.
// The Role field discriminates between user, assistant, and tool result messages.
type Message struct {
    Role      MessageRole   `json:"role"` // "user" | "assistant" | "toolResult" | "developer"
    Content   []ContentBlock `json:"content"`

    // ToolResult-specific fields (set when Role == "toolResult")
    ToolCallID string `json:"toolCallId,omitempty"`
    IsError    bool   `json:"isError,omitempty"`
}

type MessageRole string

const (
    RoleUser       MessageRole = "user"
    RoleAssistant  MessageRole = "assistant"
    RoleToolResult MessageRole = "toolResult"
    RoleDeveloper  MessageRole = "developer"
)
```

### Event flow per backend fidelity

| Backend | start | text_delta | thinking_delta | toolcall_end | done | error |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| Claude stream-json | ✅ | ✅ (block-level) | ✅ | ✅ | ✅ usage | ✅ |
| Codex app-server | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| omp print/json | ✅ | ✅ | ✅ | ✅ | ✅ usage | ✅ |
| omp RPC | ✅ | ✅ | ✅ | ✅ | ✅ usage | ✅ |
| OpenCode serve | ✅ | ⚠️ schema-gated | ⚠️ schema-gated | ⚠️ schema-gated | ⚠️ schema-gated | ✅ |
| Codex exec | synthesized | ⚠️ probe text streaming | ❌ | ❌ | ✅ exit code | ✅ stderr |
| OpenCode run | synthesized | ⚠️ probe text streaming | ❌ | ❌ | ✅ exit code | ✅ stderr |

---

## Supported CLI Tools

### Claude Code CLI

Anthropic's official CLI. Best structured-output support of the non-omp options.

#### Invocation

```bash
claude -p "<user prompt>" \
    --system-prompt "<system prompt>" \
    --model claude-sonnet-4-20250514 \
    --output-format stream-json
```

**Flags:**
- `-p` / `--print` — non-interactive mode (process prompt and exit)
- `--system-prompt "..."` — set system prompt
- `--model <id>` — model selection (exact or fuzzy match)
- `--output-format text|json|stream-json` — always use `stream-json` for programmatic consumption
- `--continue` — resume the most recent session
- `--resume <session-id>` — resume a specific session
- `--json-schema '{...}'` — constrain output to a JSON schema (optional)

**Input options:**
- As argument: `claude -p "What does this do?"`
- Via stdin: `cat file.txt | claude -p "Summarize this"`
- Both: `cat file.txt | claude -p "Analyze this output"` (stdin appended to prompt)

#### Output: `stream-json` format

Each line of stdout is a complete JSON object. **Parse as JSONL — one event per
line, emitted incrementally as Claude generates the response.** This is true
streaming: you'll see the first `system` event almost immediately, `assistant`
events as the model generates each content block, and `result` only at the end.

**Wire format example:**
```jsonl
{"type":"system","subtype":"init","session_id":"abc123","tools":[...],"model":"claude-sonnet-4-20250514"}
{"type":"assistant","message":{"id":"msg_01","role":"assistant","content":[{"type":"text","text":"I'll check the file."}],"model":"...","stop_reason":null}}
{"type":"assistant","message":{"id":"msg_01","content":[{"type":"tool_use","id":"toolu_01","name":"Read","input":{"path":"/tmp/foo.txt"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"file contents..."}]}}
{"type":"assistant","message":{"id":"msg_02","content":[{"type":"text","text":"The file says..."}]}}
{"type":"result","subtype":"success","total_cost_usd":0.0123,"duration_ms":4521,"num_turns":2,"session_id":"abc123"}
```

**Event types to handle:**

| stream-json event | Meaning | Map to Event |
|---|---|---|
| `{"type":"system","subtype":"init"}` | Session started, model + tools listed | `start` (capture `session_id`) |
| `{"type":"assistant","message":{content:[{type:"text",...}]}}` | Text content block from the model | `text_start` + `text_delta` + `text_end` (or single `text_delta`) |
| `{"type":"assistant","message":{content:[{type:"thinking",...}]}}` | Thinking/reasoning block | `thinking_start` + `thinking_delta` + `thinking_end` |
| `{"type":"assistant","message":{content:[{type:"tool_use",...}]}}` | Tool call from the model | `toolcall_end` (with parsed `ToolCall`) |
| `{"type":"user","message":{content:[{type:"tool_result",...}]}}` | Tool execution result (Claude executes its own tools) | (informational; surface in UI but not in event stream) |
| `{"type":"result","subtype":"success",...}` | Turn complete | `done` (with usage from `total_cost_usd`, `duration_ms`) |
| `{"type":"result","subtype":"error_max_turns"}` | Hit max turn limit | `error` |
| `{"type":"result","subtype":"error_during_execution"}` | Runtime error | `error` |

**Note on assistant events:** Claude Code emits each content block as a separate
JSONL line, but the content within the block is the **complete** text/tool call,
not a delta. So unlike the HTTP API (which streams character-by-character),
stream-json gives you block-level granularity. The block-level streaming is still
real-time — you see each block as it completes — but text doesn't trickle in
character by character.

For UIs that want character-level rendering, fall back to `--output-format text`
mode and read raw bytes (degraded mode, no structured events).

**Capturing session ID:** The first event (`system` / `init`) contains
`session_id`. Store it so you can `--resume <session_id>` on the next turn for
multi-turn conversations.

#### Tool support

Claude Code has **its own built-in tools** (Read, Write, Edit, Bash, Grep, etc.)
and will execute them inside the subprocess. You cannot inject your own tool
definitions via the CLI. If you need your own tools, use `llmclient` + `llmauth`.

This means `llmcli` with Claude Code is useful for:
- Read-only analysis (planning, review)
- Tasks where Claude Code's own tools are sufficient
- NOT for tasks that need tools Claude Code doesn't provide

#### Limitations

- Requires Claude Code installed and logged in (`claude login`)
- Cannot pass conversation history as structured messages — only via `--resume`
- Cannot override Claude Code's built-in tool set

---

### OpenAI Codex CLI

OpenAI's official CLI. Has two modes: simple `exec` (text output) and
`app-server` (JSON-RPC, production-grade).

#### Simple mode: `codex exec`

```bash
codex exec "<prompt>" --model gpt-5.4
```

- Output: plain text on stdout
- No `--system-prompt` flag — uses `AGENTS.md` in the current directory as system prompt
- No JSON output format
- Approval policy: `--approval-policy suggest|auto-edit|full-auto`

**Use when:** quick one-shot prompts, no need for tool call events.

**Context mapping:**
- `Context.SystemPrompt` → write to `AGENTS.md` in a temp directory before invoking
- `Context.Messages` → last user message becomes the prompt argument
- `Context.Tools` → ignored

**Event mapping:** single `text_delta` with accumulated stdout, followed by `done`.

#### Programmatic mode: `codex app-server`

```bash
codex app-server --listen stdio://
```

Starts a JSON-RPC server over stdin/stdout. Much richer than `exec`:
- Thread/session API for multi-turn conversations
- Streaming events
- Tool use events
- Structured error reporting

**Alternative listen modes:**
- `stdio://` — for subprocess communication (recommended for `llmcli`)
- `ws://127.0.0.1:8080` — WebSocket, useful for standalone daemon mode

**JSON-RPC framing over stdio** [VERIFY DURING IMPLEMENTATION]:

The Codex app-server communicates over stdio using JSON-RPC 2.0. The framing
style must be verified by running `codex app-server --listen stdio://` and
inspecting the first bytes on stdout. Two likely framing styles:

1. **LSP-style** (most likely for JSON-RPC over stdio): Each message is preceded
   by a `Content-Length` header:
   ```
   Content-Length: 123\r\n\r\n{"jsonrpc":"2.0",...}
   ```
2. **Newline-delimited JSON**: Each message is a single JSON object on one line:
   ```
   {"jsonrpc":"2.0",...}\n
   ```

**Verification command:**
```bash
echo '{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}' | codex app-server --listen stdio:// 2>/dev/null | head -c 200
```

If the response starts with `Content-Length:`, use LSP-style framing. If it
starts with `{`, use newline-delimited JSON.

**Parser for LSP-style framing** (most likely):
```go
const maxCodexFrameBytes = 8 << 20

func parseLSPFrame(r *bufio.Reader) ([]byte, error) {
    contentLength := -1
    for {
        line, err := r.ReadString('\n')
        if err != nil { return nil, err }
        line = strings.TrimRight(line, "\r\n")
        if line == "" { break }
        if strings.HasPrefix(line, "Content-Length: ") {
            if contentLength != -1 {
                return nil, fmt.Errorf("duplicate Content-Length")
            }
            n, err := strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
            if err != nil || n <= 0 || n > maxCodexFrameBytes {
                return nil, fmt.Errorf("invalid Content-Length: %q", line)
            }
            contentLength = n
        }
    }
    if contentLength == -1 {
        return nil, fmt.Errorf("missing Content-Length")
    }
    body := make([]byte, contentLength)
    _, err := io.ReadFull(r, body)
    return body, err
}
```

Malformed LSP framing is a protocol-corruption error. Missing, duplicate,
non-numeric, negative, zero, or oversized `Content-Length` values must fail the
active turn with a terminal `error` event and close/restart the persistent
Codex app-server process before accepting another turn. Never allocate a body
buffer directly from an unvalidated advertised length.

**JSON-RPC methods** (from the Python SDK reference):
- `thread_start(opts)` — begin a new conversation thread
- `turn(thread_id, message)` — send a user message, stream events back
- `run(thread_id)` — continue an in-progress turn

**Events** arrive as JSON-RPC notifications on stdout. Parse them and emit
`Event`.

**Python SDK** (reference, not for porting): `codex_app_server` package with
`Codex()` context manager.

**Use when:** you need full fidelity, tool use events, or multi-turn sessions.

#### Limitations

- Simple `exec` mode has no structured output
- `app-server` mode is newer — verify availability on the user's installed version
- Requires `codex` installed and logged in
- System prompt via `AGENTS.md` file is awkward for ephemeral prompts

---

### OpenCode CLI

Go-based terminal agent. Has **two distinct modes** with very different
streaming characteristics:

1. **`opencode run`** — one-shot CLI invocation, plain text output, no streaming
2. **`opencode serve`** — persistent HTTP server with **SSE streaming via a
   split-channel architecture** (POST commands + SSE events on parallel connections)

The HTTP server mode is the recommended path for `llmcli` integration once its
event schema is pinned. It avoids per-request cold starts and provides an SSE
transport, but it is **not full-fidelity** until the `/doc` schema or SDK source
has been captured and mapped to `Event`.

#### Mode 1: `opencode run` (one-shot, no streaming)

```bash
opencode run "<message>"
```

**No CLI flags for:**
- System prompt (must use `opencode.json` config file [VERIFY: check if `--config` flag exists])
- Model (must use config file)
- Output format (stdout is plain text)

**Event mapping:** single `text_delta` chunk per stdout buffer flush, then `done`
on exit. Effectively partial streaming via raw bytes — no structured events.

> **TTY detection caveat:** Many CLI tools detect whether stdout is a TTY and
> buffer output differently when piped. When `llmcli` spawns `opencode run`
> as a subprocess, stdout is a pipe (not a TTY), which may cause the CLI to
> buffer the entire response before writing. Test with:
> `opencode run "count to 5" | cat` — if all lines appear at once, the CLI
> is buffering and should be treated as "buffered" (❌), not "partial" (⚠️).
> [VERIFY DURING IMPLEMENTATION: test both `opencode run` and `codex exec`
> to confirm streaming behavior when stdout is piped.]

#### Mode 2: `opencode serve` (HTTP server, schema-gated streaming)

> **Source:** https://opencode.ai/docs/server

Start the server:
```bash
opencode serve --port 4096 --hostname 127.0.0.1
```

**Flags:**
- `--port <number>` — listen port (default `4096`)
- `--hostname <string>` — bind address (default `127.0.0.1`)
- `--cors <origin>` — enable CORS for browser clients
- `--mdns` — advertise via mDNS for local discovery

**Authentication:** HTTP Basic auth, controlled by environment variables:
- `OPENCODE_SERVER_PASSWORD` — required to enable auth (no auth if unset)
- `OPENCODE_SERVER_USERNAME` — username (default `opencode`)

**Architecture: split-channel streaming**

Unlike a typical SSE-on-the-response pattern, OpenCode separates the **command
channel** from the **event channel**:

```
   ┌──────────────────┐    POST /session/:id/prompt_async    ┌─────────────┐
   │                  │ ─────────────────────────────────►   │             │
   │  llmcli client   │           (returns 204)              │  opencode   │
   │                  │                                      │   server    │
   │                  │    GET /event  (SSE, long-lived)     │             │
   │                  │ ◄─────────────────────────────────── │             │
   └──────────────────┘  server.connected, then bus events   └─────────────┘
```

**The pattern:**
1. Open the SSE event stream with `GET /event` and keep it open for the session
2. Send a message with `POST /session/:id/prompt_async` (returns `204` immediately)
3. Receive events on the SSE stream as the model generates the response
4. Correlate events to your request via session ID

**Key endpoints:**

| Method | Path | Purpose | Streaming |
|---|---|---|---|
| `POST` | `/session` | Create a new session, returns `{"id": "..."}` | N/A |
| `GET` | `/event` | SSE stream of bus events | ✅ Yes (SSE) |
| `GET` | `/global/event` | SSE stream of global events | ✅ Yes (SSE) |
| `POST` | `/session/:id/message` | Send message and **block** until complete | ❌ No (blocking) |
| `POST` | `/session/:id/prompt_async` | Send message **non-blocking** (returns 204) | ❌ N/A — returns immediately |
| `POST` | `/session/:id/command` | Run command (blocking) | ❌ No |
| `POST` | `/session/:id/shell` | Run shell (blocking) | ❌ No |
| `DELETE` | `/session/:id` | Destroy session [SCHEMA TBD] | N/A |

> **Note:** Session creation (`POST /session`) and destruction (`DELETE /session/:id`)
> endpoints are marked `[SCHEMA TBD]` — verify against `GET /doc` on a running
> `opencode serve` instance during implementation. The response schema for session
> creation likely includes at minimum `{"id": "<session-uuid>"}`.
| `GET` | `/tui/control/next` | Wait for next control request (TUI integration) | ✅ Long-poll |
| `POST` | `/tui/control/response` | Respond to a control request | ❌ No |

**Critical:** Use `prompt_async` (not `message`) when consuming events via SSE.
Otherwise the POST blocks until the entire response is complete, defeating the
streaming purpose.

**Event format:** The first event on `/event` is `server.connected`. After that,
the server emits bus events as JSON objects in standard SSE framing
(`data: {...}\n\n`). The exact event schema is **not documented** on the docs
page — clients need to introspect the OpenAPI spec served at `/doc` (or read
the SDK source) to discover the precise event types and fields.

> **GATE before full-fidelity implementation:** Hit `GET /doc` on a running `opencode serve`
> instance and dump the OpenAPI schema. Map the bus event types to
> `Event` (text deltas, tool calls, completions). Until that mapping is captured,
> treat OpenCode HTTP mode as `StreamingStructuredUnknown`: it can be used for
> transport experiments, but it must not satisfy `NeedsToolEvents`,
> `NeedsThinking`, `NeedsUsage`, or `MinStreaming=StreamingStructured`.

#### Context mapping (HTTP mode)

- `Context.SystemPrompt` → set via session config endpoint or pre-write to `opencode.json`
- `Context.Messages` → serialize to the `prompt_async` request body
- `Context.Tools` → ❌ not supported via the public API; OpenCode uses its built-in tools

#### Lifecycle (HTTP mode)

```go
// 1. Spawn the server once (or reuse an existing one on the configured port).
//    See Persistent-Process Lifecycle for lazy-start and crash-recovery patterns.
cmd := exec.Command("opencode", "serve", "--port", "4096")
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
if err := cmd.Start(); err != nil {
    return nil, fmt.Errorf("start opencode serve: %w", err)
}
supervisor := NewProcessSupervisor(cmd, ProcessSupervisorConfig{
    GracePeriod: 5 * time.Second,
    KillTree:    true,
})
supervisor.Start(ctx)

// 2. Wait for it to be ready (poll SSE endpoint until server.connected event)
if err := waitForOpenCodeServer(ctx, "http://localhost:4096",
    os.Getenv("OPENCODE_SERVER_PASSWORD")); err != nil {
    return nil, fmt.Errorf("opencode serve readiness: %w", err)
}

// 3. Authenticate and open the SSE stream BEFORE sending any messages
sseReq, err := http.NewRequestWithContext(ctx, "GET", "http://localhost:4096/event", nil)
if err != nil {
    return nil, err
}
sseReq.SetBasicAuth("opencode", os.Getenv("OPENCODE_SERVER_PASSWORD"))
sseReq.Header.Set("Accept", "text/event-stream")
sseResp, err := http.DefaultClient.Do(sseReq)
if err != nil {
    return nil, fmt.Errorf("open opencode SSE stream: %w", err)
}
if sseResp.StatusCode != http.StatusOK {
    sseResp.Body.Close()
    return nil, fmt.Errorf("open opencode SSE stream returned %d", sseResp.StatusCode)
}
defer sseResp.Body.Close()

// 4. Create a session [SCHEMA TBD — verify against GET /doc during implementation]
//    Expected: POST /session with empty body returns {"id": "..."}
createReq, err := http.NewRequestWithContext(ctx, "POST", "http://localhost:4096/session", nil)
if err != nil {
    return nil, err
}
createReq.SetBasicAuth("opencode", os.Getenv("OPENCODE_SERVER_PASSWORD"))
createResp, err := http.DefaultClient.Do(createReq)
if err != nil {
    return nil, fmt.Errorf("create opencode session: %w", err)
}
defer createResp.Body.Close()
if createResp.StatusCode != http.StatusOK && createResp.StatusCode != http.StatusCreated {
    return nil, fmt.Errorf("create opencode session returned %d", createResp.StatusCode)
}
var session struct{ ID string `json:"id"` }
if err := json.NewDecoder(createResp.Body).Decode(&session); err != nil {
    return nil, fmt.Errorf("decode opencode session: %w", err)
}
sessionID := session.ID
if sessionID == "" {
    return nil, fmt.Errorf("create opencode session returned empty id")
}

// 5. Send the message asynchronously. The POST result must be checked; otherwise
// the SSE reader can wait forever for events from a prompt the server rejected.
go func() {
    body := buildPromptBody(input)
    postReq, err := http.NewRequestWithContext(ctx, "POST",
        "http://localhost:4096/session/"+sessionID+"/prompt_async",
        bytes.NewReader(body))
    if err != nil {
        cancelSSE(fmt.Errorf("build opencode prompt_async request: %w", err))
        return
    }
    postReq.SetBasicAuth("opencode", os.Getenv("OPENCODE_SERVER_PASSWORD"))
    postResp, err := http.DefaultClient.Do(postReq)
    if err != nil {
        cancelSSE(fmt.Errorf("opencode prompt_async: %w", err))
        return
    }
    defer postResp.Body.Close()
    if postResp.StatusCode != http.StatusNoContent {
        cancelSSE(fmt.Errorf("opencode prompt_async returned %d", postResp.StatusCode))
        return
    }
}()

// 6. Parse the SSE stream and emit events on the channel
parseSSE(ctx, sseResp.Body, events)
```

#### Limitations (HTTP mode)

- Event schema is undocumented at the time of writing — requires introspection
  of `/doc` (OpenAPI spec) or SDK source before claiming full fidelity
- Cannot inject custom tool definitions
- Server must be spawned and kept alive across requests (or use an existing one)
- Multiple `llmcli` instances must coordinate on the port (or each spawn their own)
- HTTP basic auth is the only auth mechanism — no API keys, no JWT

#### Recommendation

- **Prefer HTTP mode** (`opencode serve`) over `opencode run` for transport
  streaming, but do not use it for tool/thinking/usage requirements until the
  event schema is mapped
- Keep the server alive across the session lifetime; don't spawn per request
- If multiple fabrikk tools (`plan`, `verk`, `vakt`) are running concurrently,
  share a single `opencode serve` instance via a known port

#### Server readiness check

When spawning `opencode serve`, the caller must wait until the server is ready
before sending requests. Poll the SSE endpoint with authentication:

```go
func waitForOpenCodeServer(ctx context.Context, baseURL, password string) error {
    deadline := time.Now().Add(10 * time.Second)
    pollInterval := 100 * time.Millisecond

    for time.Now().Before(deadline) {
        if ctx.Err() != nil {
            return ctx.Err()
        }

        attemptCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
        req, _ := http.NewRequestWithContext(attemptCtx, "GET", baseURL+"/event", nil)
        if password != "" {
            req.SetBasicAuth("opencode", password)
        }
        req.Header.Set("Accept", "text/event-stream")

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
            cancel()
            time.Sleep(pollInterval)
            continue
        }

        if resp.StatusCode == 401 {
            resp.Body.Close()
            cancel()
            return fmt.Errorf("opencode serve returned 401: check OPENCODE_SERVER_PASSWORD")
        }
        if resp.StatusCode != 200 {
            resp.Body.Close()
            cancel()
            time.Sleep(pollInterval)
            continue
        }

        // Read until we see server.connected (first SSE event). The per-attempt
        // context deadline prevents an open event stream from blocking readiness
        // forever.
        reader := bufio.NewReader(resp.Body)
        for {
            line, err := reader.ReadString('\n')
            if err != nil {
                break
            }
            line = strings.TrimRight(line, "\r\n")
            if strings.HasPrefix(line, "data: ") &&
                strings.Contains(line, "server.connected") {
                resp.Body.Close()
                cancel()
                return nil
            }
        }
        resp.Body.Close()
        if attemptCtx.Err() != nil && ctx.Err() == nil {
            cancel()
            time.Sleep(pollInterval)
            continue
        }
        cancel()
    }

    return fmt.Errorf("opencode serve readiness timeout after 10s at %s", baseURL)
}
```

**Port conflict detection:** Before spawning, check if the configured port is
already in use. If it is, try connecting to see if it's an existing opencode
instance — if yes, reuse it; if not, fail with a clear error:

```go
func isPortInUse(port int) bool {
    addr := fmt.Sprintf("127.0.0.1:%d", port)
    conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
    if err != nil { return false }
    conn.Close()
    return true
}
```

---

### omp CLI

> **NOTE:** omp is a private CLI used by the fabrikk maintainers' personal dev
> environment. It is **not publicly distributed** and most users will never have
> it installed. This section exists for internal continuity — the JSONL patterns
> and host tool injection model are useful references for implementing similar
> features in other backends. External users should skip this section and use
> Claude, Codex, or OpenCode instead.

oh-my-pi's CLI. Has the richest programmatic interface of the four — JSONL event
stream and full bidirectional RPC mode.

#### Print mode: `omp -p`

```bash
omp -p "<prompt>" \
    --system-prompt "<sys>" \
    --model opus \
    --mode json \
    --thinking high
```

**Flags:**
- `-p` / `--print` — non-interactive; also auto-enabled when stdin is piped
- `--system-prompt "..."` — override system prompt
- `--append-system-prompt "..."` — append to existing system prompt
- `--model <id>` — fuzzy matching: `--model opus`, `--model "openai/gpt-5.2"`
- `--smol <m>` / `--slow <m>` / `--plan <m>` — role-based model assignment
- `--mode text|json|rpc|acp` — output mode (use `json` or `rpc` for programmatic)
- `--thinking low|med|high|...` — reasoning effort
- `--continue` / `--resume <id>` — session continuation
- `--no-session` — ephemeral, no session persistence
- `--tools read,grep,bash` — restrict tool set
- `--no-tools` — disable all tools
- `--no-lsp` — disable LSP tools

**Mode: `json`** — JSONL events on stdout. Each line is one event.

**Mode: `rpc`** — bidirectional JSONL over stdin/stdout. Send commands on stdin,
receive events and responses on stdout. Richest interface.

#### Input options

- Argument: `omp -p "What does this do?"`
- Piped stdin: `cat data.txt | omp "summarize this"` (auto-enables print mode)
- File argument: `omp @prompt.md @image.png "question"` (`@`-prefix loads files)

#### RPC mode (recommended for `llmcli`)

```bash
omp --mode rpc
```

**Protocol:** JSONL over stdio. Send JSON commands on stdin; receive JSON
events and responses on stdout.

**Commands:**
- `{"type": "prompt", "text": "..."}` — send a user message
- `{"type": "get_state"}` — query session state
- `{"type": "set_model", "model": "opus"}`
- `{"type": "steer", "text": "..."}` — inject guidance mid-turn
- `{"type": "abort"}` — cancel current generation
- `{"type": "compact"}` — compact conversation context
- `{"type": "get_messages"}` — retrieve conversation history
- `{"type": "set_host_tools", "tools": [...]}` — **inject custom tool definitions**

**Events** (emitted on stdout):
- `{"type": "ready"}` — emitted after initialization, before accepting commands
- `{"type": "text_delta", "content": "..."}`
- `{"type": "toolcall_start"}`, `{"type": "toolcall_end", "toolCall": {...}}`
- `{"type": "thinking_delta"}`, `{"type": "thinking_end"}`
- `{"type": "done", "message": {...}}`
- `{"type": "error", "error": {...}}`

#### Host tools via RPC

This is unique to omp: `set_host_tools` lets you inject tool definitions that
omp will ask the host (your process) to execute. When omp wants to call one of
your tools, it emits a tool call event, waits for a response on stdin, and
incorporates the result into the conversation.

**Reference implementation:** `packages/coding-agent/src/modes/rpc/rpc-client.ts`
in the omp codebase. This is a TypeScript client for the same RPC protocol you'd
be implementing in Go — useful as a reference but not for direct porting.

#### Context mapping

- `Context.SystemPrompt` → `--system-prompt` flag (print mode) or leave to omp defaults (RPC mode + user's config)
- `Context.Messages` → last user message as arg/command (print mode); full history via session or sequential RPC `prompt` commands
- `Context.Tools` → RPC `set_host_tools` command (full support) or `--tools` flag to restrict built-ins

---

## Context → CLI Mapping

Summary of how to map the unified `Context` type to each CLI's invocation:

| Context field | Claude Code | Codex exec | Codex app-server | OpenCode run/serve | omp print | omp RPC |
|---|---|---|---|---|---|---|
| `SystemPrompt` | `--system-prompt` | `AGENTS.md` file | thread_start opts | `opencode.json` | `--system-prompt` | config / ignored |
| `Messages` (last) | `-p` arg | exec arg | `turn()` message | `run` arg | `-p` arg | `prompt` command |
| `Messages` (history) | `--resume` | ❌ unsupported | thread state | ❌ unsupported | `--resume` | sequential `prompt` |
| `Tools` | ❌ built-in only | ❌ unsupported | ❌ host tools until protocol verified | ❌ unsupported until schema pinned | `--tools` (restrict built-ins) | ✅ `set_host_tools` |

**Recommendation:** For external users, select by required capabilities and
prefer the pinned structured backends. For internal host-tool injection where
the host defines, approves, executes, and returns tool results, use **omp RPC**.

---

## Conversation State Model

The fundamental design question for CLI backends: when the caller provides
`Context.Messages` with 10 messages, what does the backend actually do?

The answer differs per backend. This section specifies the exact semantics.

### Session management approach

`llmcli` uses **caller-managed sessions** — the `Context` carries an optional
`SessionID` field, and the caller is responsible for storing and reusing it:

```go
type Context struct {
    SystemPrompt string     // may or may not be used depending on CLI
    Messages     []Message  // conversation history
    Tools        []Tool     // often ignored — most CLIs don't accept tool defs
    SessionID    string     // if set, continue an existing session; if empty, start fresh
}
```

The `start` event includes `SessionID` — the caller captures it for subsequent
calls:

```go
var sessionID string
for event := range stream {
    if event.Type == llmcli.EventStart {
        sessionID = event.SessionID  // capture for multi-turn
    }
    // ... handle other events ...
}

// Next turn — continue the same session
stream2, _ := backend.Stream(ctx, &llmclient.Context{
    SystemPrompt: "...",
    Messages:     []llmclient.Message{{Role: "user", Content: ...}},
}, llmcli.WithSession(sessionID))
```

Backends that don't support sessions silently ignore `WithSession`.

### Per-backend message handling

| Backend | `len(Messages) == 1` | `len(Messages) > 1` with `SessionID` | `len(Messages) > 1` without `SessionID` |
|---|---|---|---|
| Claude `-p` | Last user message → `-p` arg | Send last message + `--resume <id>` | Concat all messages into single prompt (degraded) |
| Codex exec | Last user message → exec arg | ❌ No session support | Last message only (history lost) |
| Codex app-server | Message → `turn()` on new thread | Message → `turn()` on existing thread | Send all messages sequentially via `turn()` |
| OpenCode run | Last user message → run arg | ❌ No session support | Last message only (history lost) |
| OpenCode serve | Message → `prompt_async` on new session | Message → `prompt_async` on existing `session_id` | Send all messages to same session |
| omp print | Last user message → `-p` arg | Send last message + `--resume <id>` | Concat all messages into single prompt (degraded) |
| omp RPC | `prompt` command on new session | `prompt` command on existing session | Send each message as sequential `prompt` commands |

### Session ID lifecycle

1. **First call:** `SessionID` is empty. Backend starts a fresh session.
2. **Start event:** The `start` event includes `SessionID`. Caller stores it.
3. **Subsequent calls:** Caller passes `WithSession(sessionID)`. Backend uses
   `--resume <id>` (Claude, omp print), thread state (Codex app-server), or
   session path (OpenCode serve) to continue.
4. **New conversation:** Caller passes no `WithSession(...)`. Backend starts
   fresh, returns a new `SessionID` in the `start` event.

### Where session IDs come from

| Backend | Source of SessionID |
|---|---|
| Claude stream-json | `{"type":"system","subtype":"init","session_id":"abc123"}` — first event |
| Codex app-server | `thread_start()` response — includes `thread_id` |
| OpenCode serve | Session created via POST endpoint — response includes `session_id` |
| omp print | `--resume <id>` reuses prior session; `start` event includes `session_id` |
| omp RPC | `{"type":"ready","session_id":"..."}` — first event after init |

### Fallback for backends without session support

Codex exec and OpenCode run have **no session mechanism**. When the caller
provides multiple messages and no session:

```go
// Fallback: concatenate all messages into a single prompt
func concatMessages(messages []Message) string {
    var sb strings.Builder
    for i, msg := range messages {
        if i > 0 {
            sb.WriteString("\n\n")
        }
        sb.WriteString(fmt.Sprintf("[%s]: %s", msg.Role, contentToString(msg.Content)))
    }
    return sb.String()
}
```

This loses tool call structure but preserves conversation context as text.
Document this degradation clearly in error logs:

```go
if len(input.Messages) > 1 && cfg.sessionID == "" && !b.supportsSessions {
    log.Warn("backend %s does not support multi-turn sessions; concatenating messages", b.Name())
}
```

---

## Routing Through Ollama (Open-Source Models)

> **Source:** https://docs.ollama.com/integrations/

Each of the supported CLI harnesses (Claude Code, Codex CLI, OpenCode) can be
configured to route its API calls through **Ollama** instead of the upstream
provider. This unlocks a powerful pattern: **use the harness you like (Claude
Code's stream-json, Codex's app-server, OpenCode's HTTP server) with any
open-source model that Ollama supports** — locally or via Ollama Cloud.

### Why this matters for `llmcli`

For our purposes, this is essentially free functionality. The CLI harnesses do
all the work; `llmcli` just needs to set the right environment variables (or
config files) when spawning the subprocess. The streaming output format,
event parsing, and Go integration are **completely unchanged** — we still
parse Claude Code's stream-json the same way, regardless of whether the bytes
came from Anthropic's servers or from a local Qwen model running through Ollama.

The user gets:
- The agent loop, system prompt handling, and tool execution of their preferred CLI
- Any Ollama-supported model: GLM, Kimi, MiniMax, Qwen, gpt-oss, etc.
- Local mode (free, private, offline) or Ollama Cloud (cheap, hosted)
- No code changes in `llmcli` — purely a runtime configuration option

### Ollama API surface

Ollama runs **three compatibility layers** in parallel on `localhost:11434`:

| Endpoint | Compatible with | Used by |
|---|---|---|
| `http://localhost:11434` | Anthropic Messages API | Claude Code |
| `http://localhost:11434/v1` | OpenAI Responses / Chat API | Codex CLI, OpenCode |
| `http://localhost:11434/api/...` | Native Ollama API | Direct Ollama clients |

Ollama Cloud (hosted) is reachable at `https://ollama.com` with the same
compatibility layers, gated by `OLLAMA_API_KEY`.

### Configuration per harness

Each harness has its own routing mechanism. None of them require code changes
in the harness itself — Ollama just speaks the harness's expected API format.

#### Claude Code → Ollama

Pure environment variables, no config file edits:

```bash
export ANTHROPIC_AUTH_TOKEN=ollama
export ANTHROPIC_API_KEY=""
export ANTHROPIC_BASE_URL=http://localhost:11434
claude --model glm-5:cloud --output-format stream-json -p "..."
```

`ollama launch claude --model glm-5:cloud` is sugar for the above.

**Critical property:** Claude Code's `--output-format stream-json` works
unchanged. Claude Code handles the upstream API in Anthropic format and
re-emits its own stream-json events regardless of who's behind the API. Our
JSONL parser doesn't need to know or care that the actual model is Qwen.

**Recommended models** (from the Ollama docs): `kimi-k2.5:cloud`, `glm-5:cloud`,
`minimax-m2.7:cloud`, `qwen3.5:cloud`, `glm-4.7-flash`, `qwen3.5`.

**Note:** Claude Code requires at least 64K context window. Choose models
accordingly.

#### Codex CLI → Ollama

**Recommended: `--oss` flag (simplest)**

The `--oss` flag is the preferred routing mechanism for `llmcli` — it's a
single flag injection with no file system management:

```bash
codex --oss --model gpt-oss:120b
```

For Ollama Cloud, set `OLLAMA_API_KEY` and the model name to a `:cloud` variant.

**Fallback: persistent config profile** (needed for custom base URLs or
Ollama Cloud routing that `--oss` doesn't support):

```toml
# ~/.codex/config.toml (or a temp copy with XDG_CONFIG_HOME override)
[model_providers.ollama-launch]
name = "Ollama"
base_url = "http://localhost:11434/v1"
```
```bash
codex --profile ollama-launch
```

The `--profile` approach requires writing a temp config file and managing
`XDG_CONFIG_HOME` to avoid clobbering the user's config — only use it if
`--oss` doesn't meet your needs (e.g., custom base URLs).

`ollama launch codex` is the convenience wrapper.

**Recommended models:** `gpt-oss:120b` (local), `gpt-oss:120b-cloud` (Ollama Cloud).
Same 64K context recommendation applies.

#### OpenCode → Ollama

Edit `~/.config/opencode/opencode.json` with a provider block using the
`@ai-sdk/openai-compatible` provider, base URL `http://localhost:11434/v1`. Or
use `ollama launch opencode --config` to set it up interactively.

For Ollama Cloud, set `OLLAMA_API_KEY` and use base URL `https://ollama.com/v1`.

**Recommended models:** `qwen3-coder` (local), `glm-4.7:cloud` (cloud).

### `llmcli` integration

The `WithOllama` functional option provides the `OllamaConfig`. When set,
the backend injects the appropriate environment variables / writes the
appropriate config before spawning the subprocess.

```go
type OllamaConfig struct {
    // BaseURL defaults to "http://localhost:11434" for local, or "https://ollama.com" for cloud.
    BaseURL string

    // Model is the Ollama model name (e.g. "glm-5:cloud", "qwen3.5", "gpt-oss:120b").
    // Cloud models use the ":cloud" suffix.
    Model string

    // APIKey is required for Ollama Cloud, empty for local.
    APIKey string
}
```

Claude backend applies Ollama routing via environment variables:

```go
func (b *ClaudeBackend) buildEnv(cfg *requestConfig) []string {
    env := os.Environ()
    if cfg.ollama != nil {
        baseURL := cfg.ollama.BaseURL
        if baseURL == "" {
            baseURL = "http://localhost:11434"
        }
        env = append(env,
            "ANTHROPIC_BASE_URL="+baseURL,
            "ANTHROPIC_AUTH_TOKEN=ollama",
            "ANTHROPIC_API_KEY=",
        )
        if cfg.ollama.APIKey != "" {
            // For Ollama Cloud
            env = append(env, "OLLAMA_API_KEY="+cfg.ollama.APIKey)
        }
    }
    return env
}

func (b *ClaudeBackend) buildArgs(cfg *requestConfig, input *Context) []string {
    args := []string{"-p", lastUserMessage(input), "--output-format", "stream-json"}
    model := cfg.model
    if cfg.ollama != nil && cfg.ollama.Model != "" {
        model = cfg.ollama.Model  // override with Ollama model name
    }
    if model != "" {
        args = append(args, "--model", model)
    }
    if cfg.sessionID != "" {
        args = append(args, "--resume", cfg.sessionID)
    }
    return args
}
```

Caller usage:
```go
backend := llmcli.NewClaudeBackend()
stream, err := backend.Stream(ctx, &llmclient.Context{
    SystemPrompt: "...",
    Messages:     []llmclient.Message{...},
}, llmcli.WithOllama(llmcli.OllamaConfig{
    Model: "glm-5:cloud",
    // BaseURL defaults to localhost:11434
}))
```

The same pattern applies to the Codex backend (set `OPENAI_BASE_URL` env var
or write the `[model_providers.ollama-launch]` profile to a temp config) and
the OpenCode backend (write the provider block to a temp `opencode.json`).

### Detection: is Ollama available?

Probe `http://localhost:11434/api/tags` (the native Ollama endpoint). If it
returns a 200 with a JSON model list, Ollama is running locally:

```go
func IsOllamaAvailable() bool {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:11434/api/tags", nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return false
    }
    defer resp.Body.Close()
    return resp.StatusCode == 200
}

// List available local models
func OllamaLocalModels(ctx context.Context) ([]string, error) {
    req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:11434/api/tags", nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    var data struct {
        Models []struct{ Name string } `json:"models"`
    }
    json.NewDecoder(resp.Body).Decode(&data)
    names := make([]string, len(data.Models))
    for i, m := range data.Models {
        names[i] = m.Name
    }
    return names, nil
}
```

### Three deployment modes

| Mode | Where the model runs | API key needed | Best for |
|---|---|---|---|
| **Local Ollama** | Your machine | None | Privacy, offline, free, GPU-bound |
| **Ollama Cloud** | ollama.com | `OLLAMA_API_KEY` | Cheap hosted, no GPU needed, larger models |
| **Direct provider** | Anthropic / OpenAI / etc. | Provider's key/OAuth | Best models, paid subscriptions |

`llmcli` should support all three transparently. The caller picks based on
their `OllamaConfig` (or absence thereof). Defaults (no Ollama config) →
direct provider, same as today.

### Limitations to be aware of

- **Model capabilities vary.** Open-source models may not support tool calling,
  vision, or large context windows. Test before relying on them for production
  workflows.
- **Streaming behavior is the harness's, not Ollama's.** Stream-json events
  still arrive at block-level granularity (not character-level deltas), even
  through Ollama. The wire format is unchanged.
- **Context windows differ.** Many open-source models have smaller context
  windows than Claude/GPT-4. The harnesses recommend at least 64K — pick
  models accordingly.
- **Tool use is a coin flip.** Claude Code's tool format may not be handled
  correctly by all open-source models routed through Ollama's Anthropic
  compatibility layer. Verify per-model.
- **Per-harness config persistence.** Codex and OpenCode store routing config
  in dotfiles (`~/.codex/config.toml`, `~/.config/opencode/opencode.json`). If
  `llmcli` writes to these, it should use a temp config and `--profile` /
  `--config` flag to avoid clobbering the user's settings.

---

## Streaming Contract

This is a **streaming** backend, not a request/response backend. The whole point
of supporting CLI tools like Claude Code's `--output-format stream-json` is to
deliver events to the caller **as they arrive from the model**, so the caller can:

- Display tokens to the user as they're generated (real-time UI)
- React to tool calls before the turn finishes (e.g., approve before execution)
- Show progress indicators (thinking → text → tool use → text → done)
- Cancel mid-turn based on partial output

### The contract

Every CLI backend MUST satisfy these properties:

1. **`Stream()` returns immediately** after spawning the subprocess. It does NOT
   wait for the subprocess to finish.
2. **The returned channel emits events as they arrive on stdout.** Each upstream
   line/frame is parsed as soon as it arrives. One upstream frame may expand into
   several normalized events when needed to satisfy canonical sequencing. No
   batching, no buffering until EOF.
3. **The consumer can read from the channel concurrently** with the subprocess
   producing output. There is no "two-phase" run-then-read API.
4. **Each stream emits exactly one terminal event.** The terminal event is either
   `done` or `error`; never both. The channel closes after the terminal event.
   A channel close without a terminal event is a transport bug.
5. **Cancellation propagates immediately.** Cancelling the parent `context.Context`
   kills the subprocess tree; the terminal event is either `error` with a
   cancellation cause or `done` with `Reason: StopCancelled`, consistently for
   the backend.

### Critical anti-pattern

**NEVER do this:**
```go
// WRONG — defeats streaming entirely
output, _ := io.ReadAll(stdout)  // blocks until subprocess exits
for _, line := range bytes.Split(output, []byte("\n")) {
    parseEvent(line)  // all events arrive at once, after the model is done
}
```

`io.ReadAll(stdout)` blocks until EOF. By the time you start parsing, the entire
turn has completed and the streaming benefit is gone. The user sees nothing for
30 seconds, then everything at once.

**DO this:**
```go
// CORRECT — streams events as they arrive
reader := bufio.NewReader(stdout)
for {
    line, err := readBoundedJSONLLine(reader, maxEventBytes)
    if err == io.EOF {
        break
    }
    if err != nil {
        emitTerminalError(events, err)
        return
    }
    event, err := parseEvent(line)
    if err != nil {
        continue                  // skip malformed lines, don't abort
    }
    select {
    case events <- event:         // emit immediately
    case <-ctx.Done():
        return                    // cancellation
    }
}
```

The line reader blocks until **one full line** is available, then returns. Each
iteration processes one upstream event in real time as the subprocess writes it.
Do not use `bufio.Scanner` with a fixed 1MB cap for JSONL that may contain tool
results or large payloads. Use `bufio.Reader` or equivalent with a configurable
maximum event size and deterministic drain-on-oversize behavior.

### Which CLIs actually stream

Not every CLI emits events incrementally. Some buffer the entire response and
print it at the end, even though they technically use a streaming output format.

| CLI / Mode | Streams events? | Notes |
|---|---|---|
| `claude -p --output-format stream-json` | ✅ Yes (JSONL) | One JSON per line, emitted as model generates |
| `claude -p --output-format json` | ❌ No | Single JSON object printed at end |
| `claude -p --output-format text` | ⚠️ Partial | Text streams to stdout but no event structure |
| `omp -p --mode json` | ✅ Yes (JSONL) | JSONL events, real-time |
| `omp --mode rpc` | ✅ Yes (JSONL) | Bidirectional, real-time |
| `codex app-server` | ✅ Yes (JSON-RPC) | JSON-RPC notifications stream |
| `codex exec` | ⚠️/❌ Probe required | Text-only; must prove incremental output when stdout is piped |
| `opencode run` (CLI) | ⚠️/❌ Probe required | Text-only; must prove incremental output when stdout is piped |
| `opencode serve` (HTTP + SSE) | ⚠️ Schema TBD | SSE transport streams, but full event fidelity is not available until event schema is mapped |

**Structured streaming CLIs** (✅) get the full `Event` stream
with text deltas, tool calls, thinking blocks, and usage. These are the ones to
prefer for any UI that wants to show real-time progress.

**Partial streaming CLIs** (⚠️) emit text incrementally but without structure.
The backend may produce `text_delta` events from the streamed bytes (chunk on
newline or every N bytes) only after a runtime probe proves stdout is incremental
when piped. They cannot produce `toolcall_*`, `thinking_*`, or usage events.
These are best treated as fallbacks.

**Buffered CLIs** (❌) defeat the streaming model entirely; avoid them or treat
them as a single `text_start`, `text_delta`, `text_end`, `done` sequence where
the delta contains the complete buffered response.

### Reading partial text output as a stream

For partial-streaming CLIs (⚠️), the backend should emit incremental events only
after the capability probe proves the CLI streams stdout through a pipe:

```go
// For text-only CLIs that passed the pipe-streaming probe, read in chunks and
// emit text_delta per chunk.
reader := bufio.NewReader(stdout)
events <- Event{Type: "start"}
events <- Event{Type: "text_start", ContentIndex: 0}

buf := make([]byte, 4096)
var accumulated strings.Builder
for {
    n, err := reader.Read(buf)
    if n > 0 {
        chunk := string(buf[:n])
        accumulated.WriteString(chunk)
        select {
        case events <- Event{Type: "text_delta", ContentIndex: 0, Delta: chunk}:
        case <-ctx.Done():
            return
        }
    }
    if err == io.EOF {
        break
    }
    if err != nil {
        events <- Event{Type: "error", ErrorMessage: err.Error()}
        return
    }
}

events <- Event{Type: "text_end", ContentIndex: 0, Content: accumulated.String()}
events <- Event{Type: "done", Reason: StopEndTurn}
```

This delivers character-by-character (or chunk-by-chunk) output to the consumer
even when the underlying CLI doesn't structure its events.

### Consumer pattern

How a `plan`/`verk`/`vakt` tool consumes the event stream:

```go
backend, err := llmcli.SelectBackend(ctx, llmcli.Requirements{
    MinStreaming: llmcli.StreamingStructured,
    StrictProbe:  true,
})
if err != nil {
    return err
}
stream, err := backend.Stream(ctx, &llmclient.Context{
    SystemPrompt: "...",
    Messages:     []llmclient.Message{...},
})
if err != nil {
    return err
}

for event := range stream {
    switch event.Type {
    case llmcli.EventStart:
        ui.ShowSpinner("Thinking...")
    case llmcli.EventThinkingDelta:
        ui.AppendThinking(event.Delta)
    case llmcli.EventTextDelta:
        ui.AppendOutput(event.Delta)        // print tokens as they arrive
    case llmcli.EventToolCallEnd:
        // NOTE: Tool approval only works with omp RPC backend (host tool injection).
        // For other backends, tools execute internally — toolcall_end is informational.
        ui.ShowToolCall(event.ToolCall)
    case llmcli.EventDone:
        ui.Finish(event.Message)
        return nil
    case llmcli.EventError:
        return fmt.Errorf("backend error: %s", event.ErrorMessage)
    }
}
```

**Tool execution model:** Most CLI backends (Claude, Codex, OpenCode) execute
their own built-in tools internally. When you receive a `toolcall_end` event from
those backends, the tool has **already been executed** — cancelling the context
at that point is too late. Only the omp RPC backend supports host-side tool
execution via `set_host_tools`, where the host decides whether to execute a tool
and sends the result back. See [omp RPC mode](#omp-cli) for details.

This is the same loop regardless of which backend is in use — `llmcli` with omp
in RPC mode, `llmcli` with Claude Code stream-json, `llmclient` over HTTP. The
streaming contract makes them interchangeable.

---

## Event Stream Normalization

All CLI backends must emit the same `Event` types defined in
[Event Types](#event-types) above:
`start`, `text_start`, `text_delta`, `text_end`, `thinking_*`, `toolcall_*`,
`done`, `error`.

### Claude Code stream-json → events

See the full event mapping table in the [Claude Code CLI](#claude-code-cli) section
above. Each JSONL line is parsed immediately. A single upstream JSONL object may
expand into multiple normalized events when needed to satisfy
`*_start`/`*_delta`/`*_end` sequencing. For example, a complete Claude text block
expands into `text_start`, one full-block `text_delta`, and `text_end`.

### omp `--mode json` → events

Mostly direct mapping — omp's event types already match the `Event`
shape since they both derive from the same upstream type (`types.ts:419-441`).
If an upstream frame omits start/end boundaries, the normalizer must synthesize
them so consumers see the canonical sequence.

### Codex app-server JSON-RPC → events

Parse JSON-RPC notifications and map:
- `thread_started` → `start`
- `message_delta` → `text_start` if needed, then `text_delta`
- `tool_call` → `toolcall_start` if needed, one or more `toolcall_delta`, then `toolcall_end`
- `turn_complete` → `done`
- `turn_error` → `error`

The app-server reader must correlate notifications to the active turn before
emitting events. If Codex app-server later proves it can host-define tools and
accept host tool results, document that protocol before enabling
`NeedsHostToolDefs`, `NeedsHostToolApproval`, or `NeedsToolResults`.

### Plain text (OpenCode, Codex exec) → events

Single-shot degraded mode:
1. Emit `{type: "start"}` when subprocess starts
2. Emit `{type: "text_start", contentIndex: 0}`
3. Stream stdout as successive `{type: "text_delta", contentIndex: 0, delta: chunk}` events when pipe-mode output is incremental
4. On process exit: emit `{type: "text_end", contentIndex: 0}` with accumulated text
5. Emit exactly one terminal event: `{type: "done", reason: "end_turn"}` on exit code 0, or `{type: "error"}` on non-zero

If a text-only CLI buffers until exit, the backend emits one full-response
`text_delta` before `text_end` and reports `StreamingBufferedOnly` in
`EventStart.Fidelity.Streaming`.

---

## Concurrency Model

What happens when `Stream()` is called twice in parallel on the same backend
instance?

```go
backend := llmcli.NewOmpRPCBackend()
go backend.Stream(ctx, input1)  // call 1
go backend.Stream(ctx, input2)  // call 2 — what happens?
```

The answer depends on the backend type.

### Per-call backends (concurrent-safe)

Claude `-p`, Codex `exec`, omp print, and OpenCode `run` spawn a fresh
subprocess per `Stream()` call. They are **unlimited concurrent-safe** — create
one instance, share it across goroutines, call `Stream()` as many times as you
want in parallel.

| Backend | Concurrent calls | Mechanism |
|---|---|---|
| Claude stream-json | ✅ Unlimited | Fresh `claude -p` subprocess per call |
| Codex exec | ✅ Unlimited | Fresh `codex exec` subprocess per call |
| omp print | ✅ Unlimited | Fresh `omp -p` subprocess per call |
| OpenCode run | ✅ Unlimited | Fresh `opencode run` subprocess per call |

### Persistent backends (serialized per instance)

omp RPC, Codex app-server, and OpenCode serve reuse a persistent process across
calls. They are **NOT concurrent-safe per instance** — the stdio channel (or
HTTP connection) is single-ordered.

| Backend | Concurrent calls | Mechanism |
|---|---|---|
| omp RPC | ⚠️ Serialized | Single stdio channel; calls queue through a cancellable turn worker |
| Codex app-server | ⚠️ Serialized | Single stdio channel; calls queue through a cancellable turn worker |
| OpenCode serve | ✅ Concurrent | Single HTTP server; multiple sessions via `session_id` |

**For omp RPC and Codex app-server:** If you need N parallel streams, create N
backend instances. Each instance spawns its own subprocess.

```go
// Parallel omp RPC calls — create separate instances
backends := make([]llmcli.Backend, parallelism)
for i := range backends {
    backends[i] = llmcli.NewOmpRPCBackend()
}
```

**For OpenCode serve:** A single server process handles concurrent requests via
different session IDs. `Stream()` calls are safe to invoke concurrently.

### Implementation: cancellable turn queue

Persistent stdio backends that serialize calls use a backend-owned turn queue,
not a mutex held across the public `Stream()` call:

```go
func (b *ompRPCBackend) Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error) {
    events := make(chan Event, 32)
    req := turnRequest{ctx: ctx, input: input, events: events}
    select {
    case b.turns <- req:
        return events, nil
    case <-ctx.Done():
        close(events)
        return nil, ctx.Err()
    }
}
```

One worker goroutine owns the persistent protocol stream and processes queued
turns one at a time. It owns the stdio writer/reader for the active turn until a
terminal event is emitted. Waiting in the queue is cancellable: if a request's
context is cancelled before the worker starts it, the worker emits no events and
closes that request's channel. The worker must not hold a lock while sending to a
slow consumer; it should select on the request context for each event send.

For protocols that support multiple in-flight turns with correlation IDs, the
backend may run more than one active turn, but the spec for that backend must
state the exact notification correlation rule. Without a documented correlation
rule, the backend is serialized per process.

---

## Persistent-Process Lifecycle

Per-call backends (Claude `-p`, Codex exec, omp print, OpenCode run) spawn a
fresh subprocess for each `Stream()` call. The subprocess is born and dies with
the call — no lifecycle management needed beyond what's in
[Subprocess Lifecycle](#subprocess-lifecycle) below.

Persistent backends (omp RPC, Codex app-server, OpenCode serve) keep a
long-lived process alive across calls. This section specifies how.

### Initialization: lazy start on first `Stream()`

Persistent backends do NOT spawn their subprocess in `New()`. Instead, they
start on first use:

```go
func (b *ompRPCBackend) Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error) {
    if err := b.ensureRunning(ctx); err != nil {
        return nil, fmt.Errorf("start omp RPC subprocess: %w", err)
    }
    // ... enqueue turn request for the protocol worker ...
}
```

**Why lazy?** Avoids spawning processes that might never be used. Error messages
from a failed spawn surface at the point of use, not at construction time.

### Crash recovery

If the persistent subprocess dies unexpectedly between calls, the next
`Stream()` call detects it and respawns:

```go
func (b *ompRPCBackend) ensureRunning(ctx context.Context) error {
    b.mu.Lock()
    defer b.mu.Unlock()

    if b.proc != nil && b.proc.State() == processRunning {
        return nil
    }
    return b.startSubprocess(ctx)
}
```

`cmd.ProcessState == nil` is **not** a liveness check; it only means `Wait()` has
not been called. Each persistent subprocess has exactly one supervisor goroutine
that calls `Wait()` once, records the exit state, closes/invalidates stdio, and
notifies the turn worker. `ensureRunning()` uses that recorded state, not
`ProcessState`, to decide whether to respawn.

The caller usually does not see a respawn between turns. If the process dies
during an active turn, the backend emits that turn's terminal `error` event,
marks the process dead, and lets the next turn respawn.

### Shutdown: `Close()` kills the subprocess

The `Close()` method on the `Backend` interface terminates the persistent
subprocess:

```go
func (b *ompRPCBackend) Close() error {
    return b.supervisor.Close(5 * time.Second)
}
```

For per-call backends, `Close()` is a no-op (no persistent process to kill).

`Close()` must not call `cmd.Wait()` directly if the supervisor owns the process.
It asks the supervisor to terminate the process tree, waits for the supervisor's
single `Wait()` result, and closes the turn queue so new calls fail fast.

### OpenCode serve: shared server lifecycle

OpenCode serve is a special case — it's an HTTP server, not a stdio subprocess.
Multiple `llmcli` instances can share one server on a known port.

```go
type openCodeServeBackend struct {
    port       int
    ownServer  bool  // true if we spawned it, false if reusing existing
    cmd        *exec.Cmd
    supervisor *ProcessSupervisor
}

func (b *openCodeServeBackend) ensureRunning(ctx context.Context) error {
    // Check if a server is already running on our port
    if b.isServerRunning() {
        b.ownServer = false
        return nil
    }
    // Spawn our own. The supervisor, not exec.CommandContext, owns cancellation
    // and the only cmd.Wait() call.
    b.cmd = exec.Command("opencode", "serve", "--port", strconv.Itoa(b.port))
    if err := b.cmd.Start(); err != nil {
        return err
    }
    b.supervisor = NewProcessSupervisor(b.cmd, ProcessSupervisorConfig{
        GracePeriod: 5 * time.Second,
        KillTree:    true,
    })
    b.supervisor.Start(ctx)
    b.ownServer = true
    return b.waitForReady(ctx)
}

func (b *openCodeServeBackend) Close() error {
    if b.ownServer && b.supervisor != nil {
        return b.supervisor.Close(5 * time.Second)
    }
    return nil  // Don't kill a server we didn't start
}
```

---

## Subprocess Lifecycle

Regardless of CLI, the subprocess management pattern is the same. **`Stream()`
returns immediately** after `cmd.Start()`; the goroutine streams events as they
arrive on stdout, satisfying the [Streaming Contract](#streaming-contract).

```go
func (c *cliBackend) Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error) {
    cfg := defaultRequestConfig()
    for _, opt := range opts {
        opt(&cfg)
    }
    args := c.buildArgs(&cfg, input)
    cmd := exec.Command(c.info.Path, args...)

    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    stderr, _ := cmd.StderrPipe()

    if err := cmd.Start(); err != nil {
        return nil, err
    }
    supervisor := NewProcessSupervisor(cmd, ProcessSupervisorConfig{
        GracePeriod: 2 * time.Second,
        KillTree:    true,
    })
    supervisor.Start(ctx) // owns cancellation, process-tree termination, and the sole cmd.Wait()

    // Buffered channel — small buffer absorbs scheduling jitter without blocking
    // the parser, but stays small enough that backpressure from a slow consumer
    // propagates to the subprocess via the OS pipe buffer.
    events := make(chan Event, 32)

    // Capture stderr concurrently so it doesn't fill the pipe buffer and block
    // the subprocess. Store only a bounded tail so logging cannot grow without
    // limit. The supervisor waits for this goroutine before reading it.
    stderrTail := NewRingBuffer(64 << 10)
    stderrDone := make(chan error, 1)
    go func() {
        _, err := io.Copy(stderrTail, stderr)
        stderrDone <- err
    }()

    // Write stdin if needed (for RPC mode, this runs concurrently and may
    // continue across the lifetime of the process).
    if c.needsStdin {
        go func() {
            defer stdin.Close()
            c.writeStdin(stdin, input)
        }()
    } else {
        stdin.Close()  // signal EOF immediately for non-RPC modes
    }

    // The parser goroutine — this is where streaming happens.
    go func() {
        defer close(events)
        terminalSent := false
        emitTerminal := func(event Event) {
            if terminalSent {
                return
            }
            terminalSent = true
            select {
            case events <- event:
            case <-ctx.Done():
            }
        }

        // Parse stdout incrementally and emit events one at a time.
        // c.parseStdout MUST emit on the channel inside the read loop — NOT
        // after reading everything. It returns when stdout reaches EOF, parser
        // error, or context cancellation.
        parseErr := c.parseStdout(ctx, stdout, events)

        // The supervisor is the only owner of cmd.Wait().
        exit := supervisor.Wait()
        <-stderrDone

        switch {
        case parseErr != nil:
            emitTerminal(Event{Type: "error", ErrorMessage: parseErr.Error()})
        case ctx.Err() != nil:
            emitTerminal(Event{Type: "done", Reason: StopCancelled})
        case exit.Err != nil:
            emitTerminal(Event{Type: "error", ErrorMessage: stderrTail.String()})
        default:
            emitTerminal(Event{Type: "done", Reason: StopEndTurn})
        }
    }()

    return events, nil
}
```

**JSONL parser** (used by Claude Code stream-json, omp `--mode json`):

```go
// parseJSONL is the streaming JSONL parser. It blocks until a full line is
// available, then maps that upstream frame into one or more normalized Events.
// Use a bounded reader, not bufio.Scanner with a fixed 1MB cap: tool result
// payloads can exceed that and Scanner stops permanently with ErrTooLong.
func (c *cliBackend) parseJSONL(ctx context.Context, r io.Reader, events chan<- Event) error {
    reader := bufio.NewReader(r)
    for {
        line, err := readBoundedLine(reader, c.maxEventBytes)
        if err == io.EOF {
            return
        }
        if err != nil {
            // Oversize lines must be drained before returning so the process can
            // be reaped. The caller emits the terminal error.
            c.drainOversizeLine(reader)
            return fmt.Errorf("read JSONL event: %w", err)
        }
        if len(bytes.TrimSpace(line)) == 0 {
            continue  // skip blank lines
        }

        normalized, err := c.mapLineToEvents(line)
        if err != nil {
            // Malformed line — log but don't abort the stream. CLIs occasionally
            // emit non-JSON debug output that we should tolerate.
            continue
        }

        for _, event := range normalized {
            select {
            case events <- event:  // emit immediately, satisfying the streaming contract
            case <-ctx.Done():
                return ctx.Err()
            }
        }
    }
}
```

**Key concerns:**

- **Streaming guarantee:** `bufio.Scanner.Scan()` blocks until ONE line is available,
  but a fixed-size scanner is not the required implementation. The required
  property is line/frame-at-a-time parsing with immediate event emission.
  **Never** call `io.ReadAll(stdout)` — see [Streaming Contract](#streaming-contract).
- **Backpressure:** Use a small channel buffer (32) so a slow consumer creates
  backpressure that propagates through the OS pipe buffer back to the subprocess.
  This prevents unbounded memory growth if the model generates faster than the UI
  can render.
- **Cancellation:** Use the process supervisor to kill the subprocess tree when
  the context is cancelled. Do not combine `exec.CommandContext` default kill
  behavior with separate process-group goroutines that also call `cmd.Wait()`.
- **stderr:** Drain stderr concurrently into a bounded tail buffer. Pipe buffers
  are small (~64KB on Linux); if you don't drain, the subprocess can block
  writing stderr, deadlocking the whole pipeline. Wait for the drain goroutine
  before reading the captured tail. Surface stderr only on non-zero exit.
- **Stdin:** For non-RPC modes, close stdin immediately after writing input so
  the CLI knows there's no more input coming. For RPC modes, keep stdin open and
  write commands as they're requested by the consumer.
- **Process cleanup:** Exactly one owner calls `cmd.Wait()`: the supervisor.
  Parser, cancellation, and `Close()` paths wait on the supervisor result instead
  of calling `Wait()` themselves.
- **Buffer size:** Tool call JSON payloads can exceed 1MB. Use a configurable
  `maxEventBytes`, reject oversize events deterministically, drain the oversize
  line, and still reap the process.
- **Partial reads:** JSON events may span multiple reads; always parse line-by-line.

---

## Event Sequencing Rules

A single assistant turn may produce multiple content blocks (text, thinking, tool
calls). This section specifies exactly how they're sequenced on the event channel.

### Canonical sequencing

For each content block in the assistant's response:

1. Emit `text_start` (or `thinking_start`, `toolcall_start`) with a new `ContentIndex`
2. Emit zero or more `text_delta` (or `thinking_delta`, `toolcall_delta`) events
   with the **same** `ContentIndex`
3. Emit `text_end` (or `thinking_end`, `toolcall_end`) with the **full accumulated**
   content for that block
4. After all content blocks complete, emit `done` with the final `AssistantMessage`

**Rules:**
- `ContentIndex` increments per content block, never resets within a turn
- Multiple deltas for the same block share the same `ContentIndex`
- `text_end.Content` contains the **full accumulated text** — consumers don't
  need to track deltas themselves
- `toolcall_end.ToolCall` contains the **full parsed tool call** — not partial JSON
- After all blocks complete, emit `done` with the final `AssistantMessage`

### Worked example

A turn that produces "Hello" → tool call Read("/tmp/foo") → "Done":

```jsonl
{"type":"start","sessionId":"ses_01"}
{"type":"text_start","contentIndex":0}
{"type":"text_delta","contentIndex":0,"delta":"Hello"}
{"type":"text_end","contentIndex":0,"content":"Hello"}
{"type":"toolcall_start","contentIndex":1}
{"type":"toolcall_delta","contentIndex":1,"delta":"{\"path\":"}
{"type":"toolcall_delta","contentIndex":1,"delta":"\"/tmp/foo\"}"}
{"type":"toolcall_end","contentIndex":1,"toolCall":{"id":"toolu_01","name":"Read","arguments":{"path":"/tmp/foo"}}}
{"type":"text_start","contentIndex":2}
{"type":"text_delta","contentIndex":2,"delta":"Done"}
{"type":"text_end","contentIndex":2,"content":"Done"}
{"type":"done","reason":"end_turn","message":{"id":"msg_01","role":"assistant","content":[...3 blocks...]}}
```

### Claude block-level streaming caveat

Since Claude Code emits each content block as a complete unit (not character-by-character),
the sequence collapses to a single delta per block:

```jsonl
{"type":"text_start","contentIndex":0}
{"type":"text_delta","contentIndex":0,"delta":"<full block text>"}
{"type":"text_end","contentIndex":0,"content":"<full block text>"}
```

This is still real-time (each block arrives as it completes), but there's no
character-by-character streaming within a block.

### Consumer simplification

Consumers who don't need incremental rendering can ignore start/delta events and
only handle `*_end` and `done`:

```go
for event := range stream {
    switch event.Type {
    case llmcli.EventTextEnd:
        // event.Content has the full text — no accumulation needed
        fmt.Println(event.Content)
    case llmcli.EventToolCallEnd:
        // event.ToolCall has the full parsed call
        handleToolCall(event.ToolCall)
    case llmcli.EventDone:
        // event.Message has the complete response
        return nil
    }
}
```

---

## Backend Method Semantics

### `Name()` — stable backend identifier

`Name()` returns a stable, lowercase, kebab-case identifier. Used for telemetry,
logging, explicit backend selection, and switch statements.

| Backend | `Name()` returns |
|---|---|
| Claude Code stream-json | `"claude"` |
| Codex exec | `"codex-exec"` |
| Codex app-server | `"codex-app-server"` |
| OpenCode run | `"opencode-run"` |
| OpenCode HTTP+SSE | `"opencode-serve"` |
| omp print mode | `"omp"` |
| omp RPC mode | `"omp-rpc"` |

### `Available()` — runtime readiness check

`Available()` reports whether the backend is ready to accept `Stream()` calls.
It is **fast** (no subprocess spawn, no network call) and **may use cached state**.

- **Per-call backends:** returns `true` if the CLI binary is on `$PATH`
  (checked via `exec.LookPath`, memoized after first call).
- **Persistent backends:** returns `true` if the binary is on `$PATH` **AND**
  the persistent process is running (or can be lazily spawned on first `Stream()`).

A backend that returns `Available() == true` is **NOT** guaranteed to succeed —
the CLI may still fail at spawn time (auth, network, version mismatch, etc.).
`Available()` is intended for runtime selection ("which backends can I try?"),
not for health checking.

```go
func (b *ClaudeBackend) Available() bool {
    if b.cachedAvailable != nil {
        return *b.cachedAvailable
    }
    _, err := exec.LookPath("claude")
    avail := err == nil
    b.cachedAvailable = &avail
    return avail
}
```

For callers who need a stronger guarantee, use an optional `HealthCheck`:

```go
// HealthCheck spawns a minimal request and verifies the CLI responds.
// This is expensive — only call it when diagnostics are needed, not on every call.
func (b *ClaudeBackend) HealthCheck(ctx context.Context) error {
    // Spawn "claude --version" and verify it exits cleanly
}
```

---

## Process Group and Signal Handling

On Unix, `cmd.Process.Kill()` only kills the immediate child process. Most CLI
tools spawn their own subprocesses (bash for tool execution, file I/O workers,
etc.). If the parent context is cancelled, those orphans keep running — file
edits in progress, network requests in flight, etc.

The fix is **process groups**: put the CLI subprocess in its own process group
at spawn time, then kill the entire group on cancellation.

### Unix: process groups

```go
cmd := exec.Command("claude", args...)
cmd.SysProcAttr = &syscall.SysProcAttr{
    Setpgid: true,  // create a new process group
}
if err := cmd.Start(); err != nil {
    return nil, err
}

// The supervisor owns context cancellation and the only cmd.Wait() call.
supervisor := NewProcessSupervisor(cmd, ProcessSupervisorConfig{KillTree: true})
go func() {
    <-ctx.Done()
    supervisor.Terminate(2 * time.Second)
}()
```

### Windows: job objects

On Windows, use job objects to group child processes, or `taskkill /F /T`:

```go
//go:build windows
func killProcessTree(pid int) error {
    return exec.Command("taskkill", "/F", "/T", "/PID",
        strconv.Itoa(pid)).Run()
}
```

### Cross-platform helper

Wrap in a `KillProcessTree(cmd *exec.Cmd) error` function in `internal/subprocess.go`,
with build-constrained files:

- `internal/subprocess_unix.go` — `//go:build !windows`, uses `syscall.Kill(-pid, SIGTERM)`
- `internal/subprocess_windows.go` — `//go:build windows`, uses `taskkill /F /T`

**Warning:** Never rely on `cmd.Process.Kill()` alone for CLI backends. It only
kills the direct child and leaves orphaned subprocess trees running.

### Cancellation grace period

When the parent context is cancelled, give the subprocess a brief grace period
to clean up (flush buffers, close connections) before force-killing:

```go
exit := supervisor.Terminate(gracePeriod)
// Terminate sends SIGTERM to the process group, waits for the supervisor's
// single Wait() result, then sends SIGKILL to the process group if the grace
// period expires.
```

For persistent backends (omp RPC, Codex app-server), the grace period on `Close()`
should be configurable but defaults to 5 seconds. For per-call backends, a shorter
2-second grace period is sufficient since there's no state to preserve.

---

## Limitations

### General CLI backend limitations

- **Latency:** Subprocess spawn adds 100-500ms per request. Use persistent modes
  (omp RPC, codex app-server, opencode serve) where possible.
- **Concurrency:** Process-per-request scales poorly. For high throughput, stay
  with HTTP via `llmclient`.
- **Windows support:** CLI tools may have different binary names or installation
  paths on Windows. Use `exec.LookPath("claude.exe")` as a fallback.
- **PATH dependency:** The user's `$PATH` must include the CLI binaries. If your
  tool runs in a restricted environment, CLI detection may fail.
- **Version drift:** CLI output formats change across versions. Consider probing
  `--version` and gating on minimum versions.

### Feature matrix

| Feature | Claude stream-json | Codex exec | Codex app-server | opencode run | opencode serve | omp print | omp RPC |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| System prompt | ✅ | ⚠️ (AGENTS.md) | ✅ | ⚠️ (config) | ⚠️ (config) | ✅ | ⚠️ |
| Model selection | ✅ | ✅ | ✅ | ⚠️ (config) | ⚠️ (config) | ✅ | ✅ |
| Streaming events | ✅ JSONL | ⚠️ probe text | ✅ JSON-RPC | ⚠️ probe text | ⚠️ SSE schema TBD | ✅ JSONL | ✅ JSONL |
| Tool call events | ✅ built-in only | ❌ | ✅ built-in/verify host protocol | ❌ | ❌ until schema pinned | ✅ | ✅ |
| Host tool definitions | ❌ | ❌ | ⚠️ verify protocol | ❌ | ❌ | ❌ | ✅ |
| Host tool approval/result injection | ❌ | ❌ | ⚠️ verify protocol | ❌ | ❌ | ❌ | ✅ **PRIMARY DIFFERENTIATOR** |
| Multi-turn | `--resume` | ❌ | ✅ | ❌ | ✅ sessions | `--resume` | ✅ |
| Token usage | ✅ | ❌ | ✅ | ❌ | ❌ until schema pinned | ✅ | ✅ |
| Thinking blocks | ✅ block-level | ❌ | ✅ | ❌ | ❌ until schema pinned | ✅ | ✅ |
| Persistent process | ❌ per-call | ❌ per-call | ✅ stdio session | ❌ per-call | ✅ HTTP server | ❌ per-call | ✅ stdio session |

> **Host tool injection** is the single most important differentiator between backends.
> Only omp RPC mode allows the **host process** to define and execute custom tools
> (via `set_host_tools`). All other backends use the CLI's built-in tools, which
> execute **inside the CLI subprocess** — the host cannot intercept or approve them.
> If you need custom tool execution with host-side approval logic, omp RPC is the
> only option.

**Streaming wire format per backend:**
- **Claude stream-json** — JSONL on subprocess stdout (block-level granularity)
- **Codex app-server** — JSON-RPC notifications on subprocess stdio
- **opencode serve** — SSE over HTTP (split-channel: POST commands + GET event stream)
- **omp print/RPC** — JSONL on subprocess stdout (event-level granularity)

**Key takeaway:** only backends with pinned schemas and verified incremental
pipe behavior satisfy `StreamingStructured`. For widely-available full fidelity
(structured tool events, all event types), use **Claude Code stream-json**.
For HTTP-based deployment with multi-process clients, **OpenCode serve** is the
only long-lived HTTP server, but it remains schema-gated until `/doc` is mapped.
For host tool injection (where the host executes tools), only **omp RPC** supports
this — but it's for internal use only.

---

## Cost and Usage Tracking

Some CLI backends report token usage and cost in their done/result events. The
`Event.Usage` field captures this when available.

### Per-backend usage availability

| Backend | Token usage | Cost | Source |
|---|---|---|---|
| Claude stream-json | ✅ | ✅ `total_cost_usd` | `result` event |
| Codex app-server | ✅ | ⚠️ TBD | JSON-RPC notifications |
| omp print/json | ✅ | ✅ | `done` event |
| omp RPC | ✅ | ✅ | `done` event |
| OpenCode serve | ⚠️ TBD | ⚠️ TBD | SSE events |
| Codex exec | ❌ | ❌ | No structured output |
| OpenCode run | ❌ | ❌ | No structured output |

### Usage struct

```go
type Usage struct {
    InputTokens      int `json:"inputTokens"`
    OutputTokens     int `json:"outputTokens"`
    CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
    CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}
```

### Cost tracking

For backends that report cost (Claude, omp), extract from the done event:

```go
case llmcli.EventDone:
    if event.Message != nil && event.Message.Usage != nil {
        log.Printf("tokens: in=%d out=%d",
            event.Message.Usage.InputTokens,
            event.Message.Usage.OutputTokens)
    }
```

For backends that don't report cost, the caller can estimate from token counts
using known pricing per model, but `llmcli` does not perform this estimation —
it only surfaces what the CLI provides.

---

## Metrics and Observability

`llmcli` should emit structured metrics for monitoring and debugging.

### Recommended metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `llmcli_stream_duration_seconds` | histogram | `backend`, `model`, `success` | Total time from Stream() call to channel close |
| `llmcli_stream_events_total` | counter | `backend`, `event_type` | Count of events emitted by type |
| `llmcli_stream_errors_total` | counter | `backend`, `error_type` | Count of errors (spawn, parse, timeout, exit) |
| `llmcli_subprocess_spawn_duration_seconds` | histogram | `backend` | Time from cmd.Start() to first event on channel |
| `llmcli_subprocess_exit_code` | gauge | `backend` | Last exit code of the subprocess |
| `llmcli_backend_available` | gauge | `backend` | 1 if Available() returns true, 0 otherwise |
| `llmcli_tokens_total` | counter | `backend`, `direction` (input/output) | Token usage when reported by the CLI |

### Logging

Each backend should log at these levels:

- **Debug:** Full JSONL line content, subprocess spawn args, environment variable changes
- **Info:** Backend selection, session start/end, token counts
- **Warn:** CLI not on PATH, version mismatch, TTY detection issue, message concatenation fallback
- **Error:** Subprocess spawn failure, non-zero exit code, JSON parse failure, context cancellation

### Structured log fields

```go
type StreamLogFields struct {
    Backend    string `json:"backend"`     // e.g., "claude", "omp-rpc"
    SessionID  string `json:"session_id"`  // from EventStart
    Model      string `json:"model"`       // from --model or detected
    Duration   int64  `json:"duration_ms"` // total stream duration
    ExitCode   int    `json:"exit_code"`   // subprocess exit code
    EventCount int    `json:"event_count"` // total events emitted
}
```

---

## Testing Strategy

### Unit tests (no subprocess dependency)

- JSONL parsing: feed crafted JSONL lines to `parseJSONL()`, verify event mapping
- Event normalization: verify each backend's `mapLineToEvent()` produces correct `Event` types
- Context → CLI args: verify `buildArgs()` produces correct flags for each backend
- Ollama environment injection: verify `buildEnv()` sets correct env vars
- Functional option processing: verify `WithModel()`, `WithSession()`, `WithOllama()` apply correctly

### Integration tests (require CLI installed)

- Spawn each CLI with a simple prompt, verify events arrive on the channel
- Verify streaming: events arrive before subprocess exit
- Verify cancellation: cancel context mid-stream, subprocess is killed
- Verify session continuity: first call returns `SessionID`, second call with `WithSession()` continues

### Test isolation

Integration tests that spawn real CLIs are slow and flaky. Mitigate with:

- Tag integration tests with `//go:build integration` — they only run with `-tags=integration`
- Use short, deterministic prompts: `"respond with exactly: pong"`
- Set timeouts aggressively (5s per test)
- Skip tests when the CLI is not installed (`Available() == false`)
- Mock subprocess output for unit tests:

```go
// Mock subprocess for unit testing
type mockBackend struct {
    responses [][]byte  // pre-recorded JSONL lines
}

func (m *mockBackend) Stream(ctx context.Context, input *Context, opts ...Option) (<-chan Event, error) {
    events := make(chan Event, len(m.responses))
    go func() {
        defer close(events)
        for _, resp := range m.responses {
            event, _ := mapLineToEvent(resp)
            events <- event
        }
    }()
    return events, nil
}
```

### Test cases per backend

| Test | Claude | Codex exec | Codex app-server | OpenCode run | OpenCode serve | omp print | omp RPC |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| Basic prompt | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| System prompt | ✅ | ⚠️ AGENTS.md | ✅ | ⚠️ config | ⚠️ config | ✅ | ⚠️ |
| Model selection | ✅ | ✅ | ✅ | ⚠️ config | ⚠️ config | ✅ | ✅ |
| Streaming events | ✅ JSONL | ⚠️ probe text | ✅ JSON-RPC | ⚠️ probe text | ⚠️ schema-gated | ✅ JSONL | ✅ JSONL |
| Tool call events | ✅ built-in | ❌ | ✅ built-in / verify host protocol | ❌ | ❌ until schema pinned | ✅ | ✅ |
| Host tool injection | ❌ | ❌ | ❌ until protocol verified | ❌ | ❌ | ❌ | ✅ |
| Multi-turn (session) | ✅ resume | ❌ | ✅ thread | ❌ | ✅ session | ✅ resume | ✅ |
| Ollama routing | ✅ env vars | ✅ --oss | ✅ env vars | ✅ config | ✅ config | ✅ env vars | ✅ env vars |
| Cancellation | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Error handling | ✅ | ✅ exit code | ✅ | ✅ exit code | ✅ | ✅ | ✅ |

---

## Suggested Go Package Structure

`llmcli` is a Go package inside the **`fabrikk` monorepo** at
`github.com/php-workx/fabrikk`. Imported by external tools as
`github.com/php-workx/fabrikk/llmcli`. See [`llm-auth.md`](./llm-auth.md#monorepo-layout)
for the full monorepo layout and import patterns.

Unlike `llmclient` + `llmauth`, this package has **no SQLite dependency** and
**no `~/.fabrikk` directory access** — each spawned CLI tool handles its own
credential storage.

```
fabrikk/llmcli/
├── backend.go         # Backend interface, Option type, requestConfig, CliBackend base
├── types.go           # Event, ToolCall, Message, Context, OllamaConfig types
├── detect.go          # exec.LookPath detection, version probing, priority ordering
├── events.go          # Event normalization helpers (shared across CLIs)
├── subprocess.go      # Lifecycle helpers (spawn, cancel, stderr capture)
├── select.go          # SelectBackend / priority ordering / PreferOmp constants
│
├── claude.go          # Claude Code stream-json backend (subprocess + JSONL)
├── codex.go           # Codex exec backend (subprocess + text)
├── codex_appserver.go # Codex app-server backend (subprocess + JSON-RPC)
├── opencode_run.go    # OpenCode CLI run backend (subprocess + text, fallback)
├── opencode_http.go   # OpenCode HTTP/SSE backend (long-lived server, split-channel)
├── omp.go             # omp print mode backend (subprocess + JSONL)
├── omp_rpc.go         # omp RPC mode backend (subprocess + bidirectional JSONL)
│
└── internal/
    ├── jsonrpc.go     # Tiny JSON-RPC 2.0 client (for codex app-server)
    ├── jsonl.go       # Bounded JSONL line reader with oversize drain support
    ├── sse.go         # SSE parser (for opencode HTTP backend)
    ├── opencode_api.go # OpenAPI types for opencode /event schema (TBD)
    ├── subprocess_unix.go  # Process group kill (//go:build !windows)
    └── subprocess_windows.go  # Job object / taskkill (//go:build windows)
```

**Notes:**

- Each CLI backend implements the shared `Backend` interface from `llmclient`.
  `llmcli` should import the interface type, not redefine it.
- Keep JSONL parsers small, but use bounded long-line handling rather than
  `bufio.Scanner` with a fixed 1MB cap; tool result payloads can be larger.
- JSON-RPC 2.0 for codex app-server: a minimal implementation is ~100 lines; don't
  pull in a full JSON-RPC library.
- For omp RPC mode, study `packages/coding-agent/src/modes/rpc/rpc-client.ts` as a
  reference implementation before writing the Go equivalent.

---

## Backend Selection — Worked Example

Here's a complete example showing how a `plan` tool selects and uses a backend:

```go
// The tool needs tool calls but NOT host tools, and wants streaming.
// Claude Code stream-json is the best widely-available option.
backend, err := llmcli.SelectBackend(ctx, llmcli.Requirements{
    NeedsToolEvents: true,
    MinStreaming:    llmcli.StreamingStructured,
    StrictProbe:     true,
})
if err != nil {
    // No suitable backend installed — fall back to llmclient HTTP
    return fmt.Errorf("no CLI backend available: %w", err)
}

fmt.Printf("Using backend: %s\n", backend.Name())

// First call — start a new session
stream, err := backend.Stream(ctx, &llmclient.Context{
    SystemPrompt: "You are a code reviewer. Analyze the following code for bugs.",
    Messages: []llmclient.Message{
        {Role: "user", Content: []llmclient.ContentBlock{
            {Type: "text", Text: "// code here...\nfunc add(a, b int) int { return a + b }"},
        }},
    },
})
if err != nil {
    return err
}

var sessionID string
for event := range stream {
    switch event.Type {
    case llmcli.EventStart:
        sessionID = event.SessionID
        fmt.Println("Session started:", sessionID)
    case llmcli.EventTextDelta:
        fmt.Print(event.Delta)
    case llmcli.EventToolCallEnd:
        fmt.Printf("\n[Tool call: %s(%v)]\n", event.ToolCall.Name, event.ToolCall.Arguments)
    case llmcli.EventDone:
        fmt.Println("\n--- Done ---")
    case llmcli.EventError:
        return fmt.Errorf("error: %s", event.ErrorMessage)
    }
}

// Second call — continue the same session
stream2, err := backend.Stream(ctx, &llmclient.Context{
    Messages: []llmclient.Message{
        {Role: "user", Content: []llmclient.ContentBlock{
            {Type: "text", Text: "What about edge cases with integer overflow?"},
        }},
    },
}, llmcli.WithSession(sessionID))
// ... consume events as before ...

// When done, clean up persistent processes
defer backend.Close()
```

### Selection logic flow

```
SelectBackend(NeedsToolEvents=true, MinStreaming=StreamingStructured, StrictProbe=true)
  1. DetectAvailable() → [claude, codex] (omp not installed, opencode not installed)
  2. filterByStaticRequirements():
     - NeedsToolEvents=true → filter out codex-exec (text only)
     - MinStreaming=Structured → filter out buffered/text-only modes
  3. filterByProbe():
     - Verify installed CLI supports the required mode and structured output
     → candidates: [claude, codex-app-server]
  4. pickByPreference(PreferAuto):
     - Priority order: claude > codex-app-server
     → returns ClaudeBackend
```

### Ollama routing example

```go
// Route through Ollama to use a local open-source model
stream, err := backend.Stream(ctx, &llmclient.Context{
    SystemPrompt: "You are a helpful assistant.",
    Messages:     []llmclient.Message{...},
}, llmcli.WithOllama(llmcli.OllamaConfig{
    Model:   "qwen3.5",        // local model
    BaseURL: "http://localhost:11434",
}))
// The Claude backend will inject ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN
// environment variables, and override --model with the Ollama model name.
```

---

## Environment Variable Reference

Each CLI backend uses different environment variables for configuration and auth.
This section lists the variables that `llmcli` may need to set when spawning
subprocesses.

### Claude Code

| Variable | Purpose | Set by |
|---|---|---|
| `ANTHROPIC_API_KEY` | Direct API key auth | User (pre-existing) |
| `ANTHROPIC_BASE_URL` | Override API endpoint (for Ollama routing) | `llmcli` via `WithOllama` |
| `ANTHROPIC_AUTH_TOKEN` | Auth token (set to `ollama` for Ollama routing) | `llmcli` via `WithOllama` |
| `CLAUDE_CODE_USE_BEDROCK` | Route through AWS Bedrock | User (pre-existing) |
| `CLAUDE_CODE_USE_VERTEX` | Route through Google Vertex AI | User (pre-existing) |

### OpenAI Codex CLI

| Variable | Purpose | Set by |
|---|---|---|
| `OPENAI_API_KEY` | Direct API key auth | User (pre-existing) |
| `OPENAI_BASE_URL` | Override API endpoint (for Ollama routing) | `llmcli` via `WithOllama` |
| `CODEX_HOME` | Config directory override | `llmcli` (temp config for `--profile`) |

### OpenCode

| Variable | Purpose | Set by |
|---|---|---|
| `OPENCODE_SERVER_PASSWORD` | HTTP basic auth password for `opencode serve` | User (pre-existing) |
| `OPENCODE_SERVER_USERNAME` | HTTP basic auth username (default: `opencode`) | User (pre-existing) |

### omp

| Variable | Purpose | Set by |
|---|---|---|
| `ANTHROPIC_API_KEY` | Direct API key auth (for Anthropic models) | User (pre-existing) |
| `OPENAI_API_KEY` | Direct API key auth (for OpenAI models) | User (pre-existing) |
| `OMP_CONFIG` | Config file path override | `llmcli` (temp config for routing) |

### Ollama

| Variable | Purpose | Set by |
|---|---|---|
| `OLLAMA_API_KEY` | API key for Ollama Cloud | User / `llmcli` via `WithOllama` |
| `OLLAMA_HOST` | Override Ollama server address (default: `http://localhost:11434`) | User (pre-existing) |

---

## Minimum CLI Version Requirements

`llmcli` depends on specific CLI features that may not be available in older
versions. The following table lists the minimum version for each backend:

| CLI | Minimum Version | Required Feature | How to Check |
|---|---|---|---|
| Claude Code | 1.0.0+ | `--output-format stream-json`, `--system-prompt` | `claude --version` |
| Codex CLI | 0.1.0+ | `exec` subcommand, `--model` flag | `codex --version` |
| Codex CLI (app-server) | 0.2.0+ | `app-server --listen stdio://` | `codex app-server --help` |
| OpenCode | 0.3.0+ | `serve` subcommand, SSE events | `opencode --version` |
| omp | 14.0.0+ | `--mode json`, `--mode rpc`, `set_host_tools` | `omp --version` |

> **Note:** Version numbers above are approximate. Verify during implementation
> by running `--version` and `--help` on each CLI to confirm the required flags
> exist. Add version checks to `probeVersion()` that compare against these
> minimums and warn if a CLI is too old.

**Implementation:**

```go
type VersionConstraint struct {
    MinVersion  string   // e.g., "1.0.0"
    RequiredFor  []string // features that need this version
}

var versionConstraints = map[string]VersionConstraint{
    "claude": {MinVersion: "1.0.0", RequiredFor: []string{"stream-json", "system-prompt"}},
    "codex":  {MinVersion: "0.1.0", RequiredFor: []string{"exec", "model-flag"}},
    "omp":    {MinVersion: "14.0.0", RequiredFor: []string{"mode-json", "mode-rpc", "host-tools"}},
}

func (c *CliInfo) MeetsMinimum() bool {
    constraint, ok := versionConstraints[c.Name]
    if !ok { return true }  // unknown CLI, allow by default
    return semver.Compare(c.Version, constraint.MinVersion) >= 0
}
```

---

## Implementation Checklist

### Detection & registry

- [ ] `DetectAvailable()` — scan for `claude`, `codex`, `opencode`, `omp` via `exec.LookPath`
- [ ] `probeVersion(path)` — capture `--version` output
- [ ] `Capabilities(ctx)` probe per backend mode, including actual pipe streaming behavior where static flags are insufficient
- [ ] Priority ordering is configurable; default to pinned/structured protocols before degraded modes: Claude stream-json, Codex app-server, OpenCode serve once schema is pinned, text fallbacks, omp internal backends where explicitly enabled
- [ ] Fallback chain only falls back to a backend that still satisfies required capabilities; never downgrade `NeedsToolEvents`, `NeedsHostToolDefs`, or `MinStreaming` silently

### Backend interface

- [ ] Shared `Backend` interface (same as `llmclient`)
- [ ] `CliBackend` base struct with common subprocess lifecycle
- [ ] Per-CLI concrete types: `ClaudeBackend`, `CodexBackend`, `OpenCodeBackend`, `OmpBackend`
- [ ] Runtime backend selection: caller can force one or auto-detect
- [ ] Functional options report `OptionApplied`, `OptionIgnored`, `OptionDegraded`, or `OptionUnsupported`
- [ ] Required options fail fast when unsupported; best-effort options may degrade only with an explicit result

### Claude Code CLI

- [ ] Argument builder (`-p`, `--system-prompt`, `--model`, `--output-format stream-json`, `--resume`)
- [ ] stream-json parser (JSONL, one event per line)
- [ ] Event mapping (system→start, assistant→text_delta, tool_use→toolcall, result→done)
- [ ] Session ID capture and storage for `--resume`
- [ ] Stdin piping for long prompts

### Codex CLI

- [ ] Simple `exec` mode (text output, plain text_delta stream)
- [ ] `AGENTS.md` temporary file writer for system prompts in exec mode
- [ ] `app-server --listen stdio://` mode (optional, advanced)
- [ ] Minimal JSON-RPC 2.0 client for app-server
- [ ] Thread API calls (`thread_start`, `turn`) for multi-turn
- [ ] LSP/JSON-RPC frame parser rejects missing, duplicate, non-numeric, negative, zero, or oversized `Content-Length`
- [ ] Protocol corruption in app-server mode emits one terminal `error`, closes/restarts the persistent process, and does not accept another turn until recovered

### OpenCode CLI

**Mode 1: `opencode run` (fallback, no streaming)**
- [ ] Spawn `opencode run "<message>"` per request
- [ ] Probe whether stdout is incremental when piped; if not, classify as `StreamingBufferedOnly`
- [ ] Read stdout in chunks when probe proves incremental output; otherwise emit buffered degraded output with fidelity metadata
- [ ] Config file write for system prompt / model override
- [ ] Degraded event mode (text deltas only, no tool calls / thinking / usage)

**Mode 2: `opencode serve` (HTTP + SSE, schema-gated streaming — preferred transport)**
- [ ] Spawn `opencode serve --port <port>` once per session, keep alive
- [ ] Detect existing running server on configured port (skip spawn if present)
- [ ] HTTP basic auth via `OPENCODE_SERVER_PASSWORD` / `OPENCODE_SERVER_USERNAME` env vars
- [ ] Wait for server ready with a per-attempt read deadline while polling `/event` until `server.connected` arrives
- [ ] **Open SSE stream first** (`GET /event`), then send messages
- [ ] Use `POST /session/:id/prompt_async` (NOT `/message`) — async, returns 204
- [ ] Check `prompt_async` request errors and non-204 status, close the response body, and cancel the SSE reader on failure
- [ ] SSE parser using standard `text/event-stream` framing (`data:` prefix, blank-line separator)
- [ ] Session ID management — create session via API, store ID, reuse for multi-turn
- [ ] **Discover event schema** by hitting `GET /doc` (OpenAPI spec) on a running server
- [ ] Map opencode bus events to `Event` types before enabling tool/thinking/usage or full-fidelity selection
- [ ] Treat HTTP mode as `StreamingStructuredUnknown` until the event schema is pinned
- [ ] Graceful server shutdown on context cancel (kill process or `DELETE /session/:id`)
- [ ] Multi-instance coordination (sharing one `opencode serve` across `plan`/`verk`/`vakt`)

### omp CLI

- [ ] Print mode (`omp -p --mode json`)
- [ ] JSONL event parser (reuse types from `llmclient`)
- [ ] RPC mode (`omp --mode rpc`) with bidirectional stdio
- [ ] RPC command sender (prompt, set_model, abort, compact, set_host_tools)
- [ ] `set_host_tools` support for host-side tool execution
- [ ] Session management (`--resume`, `--continue`)

### Ollama routing

- [ ] `OllamaConfig` struct with `BaseURL`, `Model`, `APIKey` fields
- [ ] `IsOllamaAvailable()` probe via `GET http://localhost:11434/api/tags`
- [ ] `OllamaLocalModels()` lister using the same endpoint
- [ ] **Claude Code backend:** inject `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN=ollama`, `ANTHROPIC_API_KEY=""` env vars; override `--model` flag with Ollama model name
- [ ] **Codex backend:** write temp `config.toml` with `[model_providers.ollama-launch]` block, use `--profile ollama-launch` flag (avoid clobbering user's `~/.codex/config.toml`)
- [ ] **OpenCode backend:** write temp `opencode.json` with `@ai-sdk/openai-compatible` provider block, use `--config` flag (avoid clobbering user's config)
- [ ] Ollama Cloud support: when `OllamaConfig.APIKey` is set, route through `https://ollama.com` instead of localhost, set `OLLAMA_API_KEY` env var
- [ ] Document warnings about open-source model limitations (tool use, context window, capability differences)

### Streaming contract enforcement

- [ ] `Stream()` returns immediately after `cmd.Start()` (does NOT wait for exit)
- [ ] Events are emitted on the channel as each JSONL/frame/chunk is parsed (NOT batched)
- [ ] Channel uses small buffer (32) for backpressure propagation
- [ ] **No `io.ReadAll(stdout)`** anywhere in the parser path
- [ ] JSONL parser uses a bounded long-line reader with deterministic oversize drain behavior; do not use `bufio.Scanner` with a fixed 1MB cap
- [ ] One upstream JSONL/RPC/SSE frame may expand into multiple normalized events; each emitted event uses `select` on context for cancellation
- [ ] Concurrent stderr drain into a bounded tail buffer, with completion synchronized before terminal error construction
- [ ] Exactly one terminal event (`done` or `error`) is emitted, and the events channel closes only after that terminal event
- [ ] Partial-streaming CLIs (text-only) emit `text_delta` per stdout chunk only if probe proves incremental output; otherwise report `StreamingBufferedOnly`
- [ ] Test: spawn a slow CLI, verify events arrive at consumer before subprocess exits

### Subprocess infrastructure

- [ ] `exec.Command` plus a process supervisor that owns context cancellation, process-tree termination, and the only `cmd.Wait()` call
- [ ] Stdin/stdout/stderr pipe setup
- [ ] Stderr capture (concurrent drain into bounded tail buffer)
- [ ] Graceful process-tree termination on context cancel
- [ ] Zombie process cleanup via supervisor wait result; no parser, cancellation, or `Close()` path calls `cmd.Wait()` directly
- [ ] Persistent backends use a cancellable turn queue; queued turns can be cancelled before acquiring the stdio worker
- [ ] Persistent backend liveness uses supervisor-recorded exit state, not `cmd.ProcessState == nil`
- [ ] Windows compatibility (`.exe` suffix, different paths)

### Error handling

- [ ] Binary not found → return specific `ErrCliNotFound`
- [ ] Spawn failure → wrap with CLI name context
- [ ] stderr output → emit as error event
- [ ] Non-zero exit → map to `Event{Type: "error"}`
- [ ] Partial/truncated JSONL → surface parse errors
- [ ] Timeout → cancel via context and emit the specified single terminal cancellation event (`done` with `StopCancelled` unless the backend contract chooses terminal `error`)

### Process group and signal handling

- [ ] Supervisor config sets `Setpgid: true` on Unix subprocess spawns
- [ ] Supervisor kills the process group on context cancel (`syscall.Kill(-pid, SIGTERM)` then `SIGKILL`)
- [ ] Cancellation grace period: 5s for persistent, 2s for per-call backends
- [ ] Cross-platform: `internal/subprocess_unix.go` and `internal/subprocess_windows.go`

### Cost and usage tracking

- [ ] Extract `Usage` from done events where available (Claude, omp)
- [ ] Surface cost when reported by the CLI (Claude `total_cost_usd`, omp)
- [ ] No estimation for backends that don't report usage

### Metrics

- [ ] `llmcli_stream_duration_seconds` histogram (backend, model, success labels)
- [ ] `llmcli_stream_events_total` counter (backend, event_type labels)
- [ ] `llmcli_stream_errors_total` counter (backend, error_type labels)
- [ ] `llmcli_subprocess_spawn_duration_seconds` histogram (backend label)
- [ ] `llmcli_backend_available` gauge (backend label)

### Testing

- [ ] Unit tests for JSONL parsing (crafted input, no subprocess)
- [ ] Unit tests for `buildArgs()` / `buildEnv()` per backend
- [ ] Unit tests for functional options (`WithModel`, `WithSession`, `WithOllama`)
- [ ] Unit tests for event normalization (`mapLineToEvent()`)
- [ ] Unit tests for capability probing and fallback refusal when required capabilities are missing
- [ ] Unit tests for unsupported required options and explicit best-effort degradation reports
- [ ] Unit tests for exactly-one-terminal-event invariants, including cancellation and parser error paths
- [ ] Unit tests for long JSONL payloads, oversize JSONL drain, and parser recovery/failure behavior
- [ ] Unit tests for OpenCode readiness timeout/read deadline and `prompt_async` error/non-204 handling
- [ ] Unit tests for Codex app-server frame validation, including missing/duplicate/invalid/oversized `Content-Length`
- [ ] Unit tests for persistent turn queue cancellation and supervisor liveness after subprocess exit
- [ ] Mock backend for unit testing (pre-recorded JSONL responses)
- [ ] Integration tests tagged `//go:build integration` (require CLI installed)
- [ ] Integration test: basic prompt, streaming events, cancellation, session continuity
- [ ] Skip integration tests when CLI not installed (`Available() == false`)
