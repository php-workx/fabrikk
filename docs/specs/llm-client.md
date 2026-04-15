# LLM Client: Request Assembly & Agent Runtime

Technical specification for the request assembly layer that sits on top of `llmauth`.
Handles system prompt construction, message transformation, tool definitions, and
provider-specific payload building for three workflows: **planning**, **implementation**,
and **code review**.

**Date:** 2026-04-12
**Source:** `packages/ai/src/` and `packages/coding-agent/src/` in [can1357/oh-my-pi](https://github.com/can1357/oh-my-pi)
**Depends on:** `github.com/php-workx/fabrikk/llmauth` (see [`llm-auth.md`](./llm-auth.md))
**Alternative backend:** [`llm-cli.md`](./llm-cli.md) (shell out to installed CLI tools instead of calling HTTP APIs)
**Porting target:** Go package `github.com/php-workx/fabrikk/llmclient`

> **This is a porting spec.** Every section references the exact TypeScript source file
> and line number in the omp codebase at `/Users/runger/workspaces/oh-my-pi/`.
> The Go implementation should mirror the omp structure as closely as possible.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Scope: Three Workflows, Eight Required Tools](#scope-three-workflows-eight-required-tools)
- [Core Types](#core-types)
- [Tool Definitions](#tool-definitions)
- [Agent Definitions](#agent-definitions)
- [System Prompt Assembly](#system-prompt-assembly)
- [Message Transformation](#message-transformation)
- [Provider Payload Builders](#provider-payload-builders)
  - [Anthropic](#anthropic-payload-builder)
  - [OpenAI Responses](#openai-responses-payload-builder)
  - [OpenAI Codex](#openai-codex-payload-builder)
  - [Google Gemini CLI](#google-gemini-cli-payload-builder)
  - [OpenAI-Compatible (Ollama)](#openai-compatible-payload-builder)
- [Stream Event Model](#stream-event-model)
- [Prompt Caching](#prompt-caching)
- [Thinking / Reasoning](#thinking--reasoning)
- [Source Reference Map](#source-reference-map)
- [Suggested Go Package Structure](#suggested-go-package-structure)
- [Implementation Checklist](#implementation-checklist)

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                         Your Tool (plan/implement/review)        │
│                                                                  │
│  1. Pick workflow (plan / implement / review)                    │
│  2. Select agent definition (tools, model, thinking level)       │
│  3. Render system prompt from template + context                 │
│                                                                  │
│  ┌────────────────────┐    ┌─────────────────────────────────┐   │
│  │ System Prompt       │    │ Tool Registry                   │   │
│  │ (Go templates)      │    │ (8 tools with schemas)          │   │
│  └────────┬───────────┘    └──────────┬──────────────────────┘   │
│           │                           │                          │
│           ▼                           ▼                          │
│  ┌──────────────────────────────────────────────────────┐        │
│  │ Context { systemPrompt, messages, tools }             │        │
│  └─────────────────────┬────────────────────────────────┘        │
│                        │                                         │
│                        ▼                                         │
│  ┌──────────────────────────────────────────────────────┐        │
│  │ Provider Payload Builder                              │        │
│  │ (Anthropic / OpenAI / Google / Ollama)                │        │
│  │ - message transformation                              │        │
│  │ - tool schema conversion                              │        │
│  │ - system block injection (billing, cloaking, prefix)  │        │
│  │ - prompt caching placement                            │        │
│  └─────────────────────┬────────────────────────────────┘        │
│                        │                                         │
│                        ▼                                         │
│  ┌──────────────────────────────────────────────────────┐        │
│  │ llmauth.Stream(model, headers, body)                  │        │
│  │ → SSE event stream → AssistantMessageEvent            │        │
│  └──────────────────────────────────────────────────────┘        │
└──────────────────────────────────────────────────────────────────┘
```

---

## Scope: Three Workflows, Eight Required Tools

omp defines 44 tools. For plan/implement/review, you need **8 required tools** plus
**2 optional recommended tools**. The required set is 6 core tools plus 2
review-specific tools:

### Core Tools (all workflows)

| Tool | Purpose | Used by |
|------|---------|---------|
| `read` | Read files, directories, URLs | Plan, Implement, Review |
| `write` | Create or overwrite files | Implement |
| `replace` | String replacement in files (fuzzy whitespace matching) | Implement |
| `grep` | Regex search across files | Plan, Implement, Review |
| `find` | Find files by glob pattern | Plan, Implement, Review |
| `bash` | Execute shell commands (git, build, test) | Plan, Implement, Review |

### Review-Specific Tools

| Tool | Purpose | Used by |
|------|---------|---------|
| `report_finding` | Submit structured code review finding (P0-P3) | Review |
| `submit_result` | Finalize review with verdict | Review |

### Optional (recommended)

| Tool | Purpose | Used by |
|------|---------|---------|
| `ask` | Query user for clarification | Plan |
| `lsp` | Language server (definitions, references, diagnostics) | Plan, Review |

> **Port from:** Tool prompt templates at `packages/coding-agent/src/prompts/tools/*.md`
> Tool type definition at `packages/ai/src/types.ts:405`

---

## Core Types

> **Port from:** `packages/ai/src/types.ts` (lines 255-441)

### Content Types

```go
type TextContent struct {
    Type string `json:"type"` // "text"
    Text string `json:"text"`
}

type ImageContent struct {
    Type     string `json:"type"`      // "image"
    Data     string `json:"data"`      // base64
    MimeType string `json:"mimeType"` // "image/jpeg", "image/png", etc.
}

type ThinkingContent struct {
    Type             string `json:"type"`              // "thinking"
    Thinking         string `json:"thinking"`
    ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

type RedactedThinkingContent struct {
    Type string `json:"type"` // "redactedThinking"
    Data string `json:"data"`
}

type ToolCall struct {
    Type      string                 `json:"type"` // "toolCall"
    ID        string                 `json:"id"`
    Name      string                 `json:"name"`
    Arguments map[string]interface{} `json:"arguments"`
}
```

### Message Types

> **Port from:** `types.ts:314-367`

```go
type UserMessage struct {
    Role      string      `json:"role"`    // "user"
    Content   interface{} `json:"content"` // string or []ContentBlock
    Timestamp int64       `json:"timestamp"`
}

type AssistantMessage struct {
    Role         string         `json:"role"`    // "assistant"
    Content      []interface{}  `json:"content"` // TextContent | ThinkingContent | ToolCall
    API          string         `json:"api"`
    Provider     string         `json:"provider"`
    Model        string         `json:"model"`
    ResponseID   string         `json:"responseId,omitempty"`
    Usage        Usage          `json:"usage"`
    StopReason   string         `json:"stopReason"` // "stop", "length", "toolUse", "error", "aborted"
    ErrorMessage string         `json:"errorMessage,omitempty"`
    Timestamp    int64          `json:"timestamp"`
    Duration     int64          `json:"duration,omitempty"` // ms
    TTFT         int64          `json:"ttft,omitempty"`     // time to first token, ms
}

type ToolResultMessage struct {
    Role       string        `json:"role"` // "toolResult"
    ToolCallID string        `json:"toolCallId"`
    ToolName   string        `json:"toolName"`
    Content    []interface{} `json:"content"` // TextContent | ImageContent
    IsError    bool          `json:"isError"`
    Timestamp  int64         `json:"timestamp"`
}

// Message is the union: UserMessage | AssistantMessage | ToolResultMessage
type Message interface{} // use type switch in Go

type Usage struct {
    Input       int     `json:"input"`
    Output      int     `json:"output"`
    CacheRead   int     `json:"cacheRead"`
    CacheWrite  int     `json:"cacheWrite"`
    TotalTokens int     `json:"totalTokens"`
    Cost        Cost    `json:"cost"`
}

type Cost struct {
    Input      float64 `json:"input"`
    Output     float64 `json:"output"`
    CacheRead  float64 `json:"cacheRead"`
    CacheWrite float64 `json:"cacheWrite"`
    Total      float64 `json:"total"`
}
```

### Context & Tool

> **Port from:** `types.ts:405-417`

```go
type Tool struct {
    Name        string      `json:"name"`
    Description string      `json:"description"`
    Parameters  interface{} `json:"parameters"` // JSON Schema object
    Strict      bool        `json:"strict,omitempty"`
}

type Context struct {
    SystemPrompt string    `json:"systemPrompt,omitempty"`
    Messages     []Message `json:"messages"`
    Tools        []Tool    `json:"tools,omitempty"`
}
```

### Stream Events

> **Port from:** `types.ts:419-441`

```go
type AssistantMessageEvent struct {
    Type         string            // "start", "text_delta", "toolcall_end", "done", "error", etc.
    ContentIndex int               // which content block (-1 for start/done/error)
    Delta        string            // text chunk (for delta events)
    Content      string            // full content (for end events)
    ToolCall     *ToolCall         // completed tool call (for toolcall_end)
    Reason       string            // stop reason (for done/error)
    Partial      *AssistantMessage // in-progress message
    Message      *AssistantMessage // final message (for done)
    Error        *AssistantMessage // error message (for error)
}
```

Full event type list:
`start`, `text_start`, `text_delta`, `text_end`, `thinking_start`, `thinking_delta`,
`thinking_end`, `toolcall_start`, `toolcall_delta`, `toolcall_end`, `done`, `error`

---

## Tool Definitions

Each tool needs a **name**, **description** (for the system prompt), and a **JSON Schema**
for its parameters (sent to the LLM in the `tools` array).

### read

> **Port from:** `prompts/tools/read.md`, tool implementation at `tools/read.ts`

```json
{
  "name": "read",
  "description": "Read file contents, directory listings, or URL content",
  "parameters": {
    "type": "object",
    "properties": {
      "path": { "type": "string", "description": "File path, directory, or URL" },
      "sel": { "type": "string", "description": "Line selector: L50, L50-L120, or raw" }
    },
    "required": ["path"]
  }
}
```

### write

> **Port from:** `prompts/tools/write.md`

```json
{
  "name": "write",
  "description": "Create or overwrite file at specified path",
  "parameters": {
    "type": "object",
    "properties": {
      "path": { "type": "string", "description": "File path to write" },
      "content": { "type": "string", "description": "File content" }
    },
    "required": ["path", "content"]
  }
}
```

### replace

> **Port from:** `prompts/tools/replace.md`

```json
{
  "name": "replace",
  "description": "String replacement in files with fuzzy whitespace matching",
  "parameters": {
    "type": "object",
    "properties": {
      "path": { "type": "string", "description": "File path" },
      "old_text": { "type": "string", "description": "Text to find (must be unique)" },
      "new_text": { "type": "string", "description": "Replacement text" },
      "all": { "type": "boolean", "description": "Replace all occurrences" }
    },
    "required": ["path", "old_text", "new_text"]
  }
}
```

### grep

> **Port from:** `prompts/tools/grep.md`

```json
{
  "name": "grep",
  "description": "Search files using regex matching",
  "parameters": {
    "type": "object",
    "properties": {
      "pattern": { "type": "string", "description": "Regex pattern" },
      "path": { "type": "string", "description": "File, directory, or glob" },
      "glob": { "type": "string", "description": "File filter (e.g. *.ts)" },
      "type": { "type": "string", "description": "File type filter (e.g. js, py, go)" }
    },
    "required": ["pattern"]
  }
}
```

### find

> **Port from:** `prompts/tools/find.md`

```json
{
  "name": "find",
  "description": "Find files by glob pattern",
  "parameters": {
    "type": "object",
    "properties": {
      "pattern": { "type": "string", "description": "Glob pattern (e.g. src/**/*.ts)" },
      "limit": { "type": "number", "description": "Max results (default 1000)" }
    },
    "required": ["pattern"]
  }
}
```

### bash

> **Port from:** `prompts/tools/bash.md`

```json
{
  "name": "bash",
  "description": "Execute shell command",
  "parameters": {
    "type": "object",
    "properties": {
      "command": { "type": "string", "description": "Shell command to execute" },
      "cwd": { "type": "string", "description": "Working directory" },
      "timeout": { "type": "number", "description": "Timeout in seconds" }
    },
    "required": ["command"]
  }
}
```

### report_finding (review only)

> **Port from:** `tools/review.ts`, `prompts/agents/reviewer.md:106-112`

```json
{
  "name": "report_finding",
  "description": "Report a code review finding with priority and location",
  "parameters": {
    "type": "object",
    "properties": {
      "title": { "type": "string", "description": "Imperative title, ≤80 chars" },
      "body": { "type": "string", "description": "One paragraph: bug, trigger, impact" },
      "priority": { "type": "number", "description": "0=blocks release, 1=fix next, 2=fix eventually, 3=nit" },
      "confidence": { "type": "number", "description": "0.0-1.0 confidence it is real" },
      "file_path": { "type": "string", "description": "Absolute path to affected file" },
      "line_start": { "type": "number", "description": "First line (1-indexed)" },
      "line_end": { "type": "number", "description": "Last line (1-indexed, ≤10 lines from start)" }
    },
    "required": ["title", "body", "priority", "confidence", "file_path", "line_start", "line_end"]
  }
}
```

### submit_result (review only)

> **Port from:** `prompts/agents/reviewer.md:114-118`

```json
{
  "name": "submit_result",
  "description": "Finalize review with verdict",
  "parameters": {
    "type": "object",
    "properties": {
      "overall_correctness": { "type": "string", "enum": ["correct", "incorrect"] },
      "explanation": { "type": "string", "description": "1-3 sentence verdict summary" },
      "confidence": { "type": "number", "description": "0.0-1.0" }
    },
    "required": ["overall_correctness", "explanation", "confidence"]
  }
}
```

---

## Agent Definitions

Each workflow maps to an **agent definition** that specifies which tools, model,
thinking level, and system prompt variant to use.

> **Port from:** `packages/coding-agent/src/prompts/agents/*.md`

### Plan Agent

> **Port from:** `prompts/agents/plan.md`

```yaml
name: plan
tools: [read, grep, find, bash]
model: slow       # strongest available model
thinking: high
read_only: true   # MUST NOT modify files
can_spawn: [explore]
```

**Procedure** (from `plan.md:11-44`):
1. Parse requirements, list assumptions
2. Find existing patterns via grep/find, read key files, trace data flow
3. List concrete changes (files, functions, types), define sequence, identify edge cases
4. Produce executable plan: Summary, Changes, Sequence, Edge Cases, Verification, Critical Files

### Implement Agent (Task Worker)

> **Port from:** `prompts/agents/task.md`

```yaml
name: task
tools: [read, write, replace, grep, find, bash]
model: default    # balanced model
thinking: medium
read_only: false  # full read-write access
```

**Directives** (from `task.md:7-16`):
- Hyperfocus on assigned work, do not deviate
- Finish only assigned work, return minimum useful result
- Prefer narrow search then read needed ranges
- Prefer edits to existing files over creating new ones

### Review Agent

> **Port from:** `prompts/agents/reviewer.md`

```yaml
name: reviewer
tools: [read, grep, find, bash, report_finding, submit_result]
model: slow       # strongest available model
thinking: high
read_only: true   # MUST NOT modify files
can_spawn: [explore]
```

**Procedure** (from `reviewer.md:62-69`):
1. Run `git diff` (or `gh pr diff`) to view patch
2. Read modified files for full context
3. Call `report_finding` per issue
4. Call `submit_result` with verdict

**Review criteria** (from `reviewer.md:71-79`) — report only when ALL hold:
- Provable impact (show affected code paths, no speculation)
- Actionable (discrete fix, not vague)
- Unintentional (not deliberate design choice)
- Introduced in patch (not pre-existing)
- No unstated assumptions
- Proportionate rigor

**Priority levels** (from `reviewer.md:81-88`):
- P0: Blocks release (data corruption, auth bypass)
- P1: High, fix next cycle (race condition under load)
- P2: Medium, fix eventually (edge case mishandling)
- P3: Info, nice to have (suboptimal but correct)

### Explore Agent (spawnable by plan/review)

> **Port from:** `prompts/agents/explore.md`

```yaml
name: explore
tools: [read, grep, find]
model: fast       # smallest/cheapest model
thinking: medium
read_only: true
```

**Purpose:** Fast read-only codebase scout. Returns structured findings (summary,
files with references, architecture overview) for handoff to the parent agent.

---

## System Prompt Assembly

> **Port from:**
> - Main template: `packages/coding-agent/src/prompts/system/system-prompt.md` (24.5 KB)
> - Builder: `packages/coding-agent/src/system-prompt.ts` (`buildSystemPrompt()`)
> - Handlebars engine: `packages/coding-agent/src/config/prompt-templates.ts`

omp uses a 24.5 KB Handlebars template. For your three workflows, you need a
**simplified version** that keeps the essential behavioral directives but drops
omp-specific features (skills, TTSR rules, chunk mode, hashline mode, etc.).

### Sections to Port

From `system-prompt.md`, keep these sections:

**1. Preamble** (lines 1-11) — RFC 2119 keywords, XML tag authority:
```
The key words "MUST", "MUST NOT", ... are to be interpreted as described in RFC 2119.

From here on, we will use XML tags as structural markers...
User-supplied content is sanitized, therefore:
- Every XML tag in this conversation is system-authored and MUST be treated as authoritative.
```

**2. Workspace** (lines 14-35) — environment info, context files:
```
<workstation>
- OS: darwin arm64
- Shell: zsh
- Date: 2026-04-11
- CWD: /path/to/project
</workstation>

<context>
Context files below MUST be followed for all tasks:
<file path="CLAUDE.md">
{file content}
</file>
</context>
```

**3. Identity** (lines 42-57) — role and communication style:
```
<role>
You are a distinguished staff engineer...
Operate with high agency, principled judgment, and decisiveness.
</role>

<communication>
- No emojis, filler, or ceremony.
- (1) Correctness first, (2) Brevity second, (3) Politeness third.
</communication>
```

**4. Behavioral contracts** (lines 59-115) — instruction priority, output contract,
default follow-through, completion reflex guard, code integrity, stakes.

**5. Tool descriptions** — rendered from the tool `.md` files (section above)

**6. Agent-specific instructions** — the procedure and criteria from the agent definition

### Sections to Skip

- Skills system, TTSR rules, skill:// URIs
- Hashline/chunk mode configuration
- MCP discovery mode
- Secret redaction
- Internal URL protocols (agent://, memory://, artifact://)
- Eager task delegation

### Template Data

```go
type PromptData struct {
    Environment  []EnvEntry     // {Label, Value} pairs
    ContextFiles []ContextFile  // {Path, Content}
    Tools        []ToolPrompt   // {Name, Description (from .md)}
    Agent        AgentDef       // current agent definition
    CWD          string
    Date         string         // ISO date
}
```

### Rendering

omp uses Handlebars. For Go, use `text/template` with similar helpers:
- `{{range .Environment}}` for environment list
- `{{range .ContextFiles}}` for context files
- `{{range .Tools}}` for tool descriptions
- `{{if .Agent.ReadOnly}}` for conditional sections

---

## Message Transformation

> **Port from:** `packages/ai/src/providers/transform-messages.ts` (lines 21-228)

Before messages are sent to any provider, they go through transformation.
Port these three behaviors:

### 1. Tool Call ID Normalization

> **Port from:** `transform-messages.ts:96-112`

OpenAI generates tool call IDs that are 450+ characters with special characters.
Anthropic requires IDs matching `^[a-zA-Z0-9_-]+$` (max 64 chars).

```go
func NormalizeToolCallID(id string) string {
    // If already valid for Anthropic, return as-is
    // Otherwise: SHA-256 hash, take first 32 hex chars, prefix with "tc_"
    // Maintain a mapping so ToolResultMessages can reference the normalized ID
}
```

Normalization must persist the original-to-normalized mapping for the lifetime
of the transformation that will later resolve tool results. Use an explicit
`IDMapper` owned by the request transformer, or attach the mapper to the
request/session context when tool results can arrive after the initial message
conversion. `NormalizeToolCallID` first checks `IDMapper.mapping[originalID]` and
returns the existing normalized value; if absent, it generates
`tc_<sha256 first32hex>`, stores it in the same map, and returns it.
`ToolResultMessages` must resolve original provider IDs through that same mapper
instead of recomputing IDs independently. A single request-scoped mapper is
sufficient for one-shot transformation; use a session-scoped mapper if tool
results persist across requests.

### 2. Orphaned Tool Call Patching

> **Port from:** `transform-messages.ts:126-228`

If an assistant message contains tool calls but the conversation has no corresponding
`ToolResultMessage` for one or more of them, inject synthetic error results:

```go
syntheticResult := ToolResultMessage{
    Role:       "toolResult",
    ToolCallID: orphanedCall.ID,
    ToolName:   orphanedCall.Name,
    Content:    []interface{}{TextContent{Type: "text", Text: "No result provided"}},
    IsError:    true,
}
```

For aborted/errored turns, inject `"aborted"` as the error content.

### 3. Thinking Block Handling

> **Port from:** `transform-messages.ts:64-85`

- Strip signatures from aborted/errored messages (incomplete thinking)
- For same-model replay: preserve thinking blocks with signatures
- For cross-model: convert thinking to plain text or drop
- Remove empty thinking blocks

---

## Provider Payload Builders

Each provider takes the same `Context` and transforms it into a provider-specific
HTTP request body.

### Anthropic Payload Builder

> **Port from:** `packages/ai/src/providers/anthropic.ts`
> - `buildParams()` at line 1387
> - `buildAnthropicSystemBlocks()` at line 1003
> - `convertAnthropicMessages()` at line 1479
> - `convertTools()` at line 1638
> - `createClaudeBillingHeader()` at line 251
> - `generateClaudeCloakingUserId()` at line 269
> - `applyClaudeToolPrefix()` at line 287
> - `applyPromptCaching()` at line 1183

**Request body:**
```json
{
  "model": "claude-sonnet-4-20250514",
  "max_tokens": 16384,
  "stream": true,
  "system": [
    {"type": "text", "text": "<billing header>"},
    {"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK."},
    {"type": "text", "text": "<system prompt>", "cache_control": {"type": "ephemeral", "ttl": "1h"}}
  ],
  "messages": [ ... ],
  "tools": [
    {"name": "proxy_read", "description": "...", "input_schema": {"type": "object", ...}},
    {"name": "proxy_grep", "description": "...", "input_schema": {"type": "object", ...}}
  ],
  "metadata": {"user_id": "user_<64hex>_account_<uuid>_session_<uuid>"},
  "thinking": {"type": "adaptive"},
  "output_config": {"effort": "high"}
}
```

**System block construction** (port `buildAnthropicSystemBlocks()` at line 1003):

For OAuth tokens (not `claude-3-5-haiku`):
1. Billing header text block (line 1018-1019)
2. Claude Code system instruction: `"You are a Claude agent, built on Anthropic's Claude Agent SDK."` (line 1022)
3. User's system prompt with `cache_control` (line 1030-1031)

**Billing header construction** (port `createClaudeBillingHeader()` at line 251):
```
x-anthropic-billing-header: cc_version=2.1.63.{3-char-random-hex}; cc_entrypoint=cli; cch={sha256(payload)[0:5]};
```
Where `cch` = first 5 chars of SHA-256 of `JSON.stringify(params)`.

**Cloaking user ID** (port `generateClaudeCloakingUserId()` at line 269):
```
user_{crypto.randomBytes(32).hex()}_account_{uuid}_session_{uuid}
```

**Tool conversion** (port `convertTools()` at line 1638):
- OAuth mode: prefix all tool names with `proxy_`
- Exception: built-in tools `web_search`, `code_execution`, `text_editor`, `computer` (line 286)
- Schema: `input_schema: { type: "object", properties: {...}, required: [...] }`

**Tool choice** (port from line 1443-1454):
- String: `{"type": "auto"}`, `{"type": "any"}`, `{"type": "none"}`
- Specific tool: `{"type": "tool", "name": "proxy_<name>"}`

**Message conversion** (port `convertAnthropicMessages()` at line 1479):
- User messages → `{role: "user", content: string | ContentBlock[]}`
- Assistant messages → `{role: "assistant", content: [text/thinking/tool_use blocks]}`
- Tool results → batched into single `{role: "user", content: [{type: "tool_result", ...}]}`
- Tool call IDs normalized
- Tool names prefixed with `proxy_` in both assistant tool_use blocks and tool_result references
- If last message is assistant, append synthetic `{role: "user", content: "Continue."}`

**Prompt caching** (port `applyPromptCaching()` at line 1183):
- Max 4 `cache_control` breakpoints per request
- Placement priority: (1) last tool, (2) last system block, (3) penultimate user message, (4) last user message
- First breakpoint gets `ttl: "1h"` on api.anthropic.com; rest get no TTL
- Port `normalizeCacheControlTtlOrdering()` and `enforceCacheControlLimit(params, 4)`

---

### OpenAI Responses Payload Builder

> **Port from:** `packages/ai/src/providers/openai-responses.ts`
> - `buildParams()` at line 306
> - `createClient()` at line 256

**Request body:**
```json
{
  "model": "gpt-4o",
  "input": [
    {"role": "developer", "content": "<system prompt>"},
    {"role": "user", "content": "..."}
  ],
  "tools": [{"type": "function", "name": "read", "parameters": {...}, "strict": true}],
  "stream": true,
  "store": false,
  "reasoning": {"effort": "high", "summary": "auto"},
  "include": ["reasoning.encrypted_content"],
  "prompt_cache_key": "<session_id>",
  "prompt_cache_retention": "24h"
}
```

**System prompt role** (line 324-326):
- Reasoning models → `"developer"` role
- Non-reasoning → `"system"` role
- Added as first message in `input` array

**No tool prefixing.** Tool names sent as-is.

**No billing headers or cloaking.** Much simpler than Anthropic.

---

### OpenAI Codex Payload Builder

> **Port from:** `packages/ai/src/providers/openai-codex-responses.ts:1286`
> Constants: `packages/ai/src/providers/openai-codex/constants.ts`

**Request body:**
```json
{
  "model": "...",
  "instructions": "<system prompt>",
  "input": [ ... ],
  "tools": [ ... ],
  "stream": true,
  "store": false,
  "reasoning": {"effort": "high", "summary": "auto"},
  "text": {"verbosity": "medium"},
  "include": ["reasoning.encrypted_content"],
  "prompt_cache_key": "...",
  "prompt_cache_retention": "in_memory",
  "previous_response_id": "<from last response>"
}
```

**Key difference from Responses API:**
- System prompt goes in `instructions` field (not in messages)
- `previous_response_id` chains turns server-side
- `text.verbosity` controls output detail level
- `prompt_cache_retention: "in_memory"` (not "24h")

---

### Google Gemini CLI Payload Builder

> **Port from:** `packages/ai/src/providers/google-gemini-cli.ts:455` (`streamGoogleGeminiCli`)

**Request body:**
```json
{
  "project": "<gcp_project_id>",
  "model": "gemini-3.1-pro-preview",
  "request": {
    "systemInstruction": {
      "parts": [{"text": "<system prompt>"}]
    },
    "contents": [
      {"role": "user", "parts": [{"text": "..."}]},
      {"role": "model", "parts": [{"text": "..."}, {"functionCall": {...}}]}
    ],
    "tools": [{"functionDeclarations": [{"name": "read", "parameters": {...}}]}],
    "toolConfig": {"functionCallingConfig": {"mode": "AUTO"}},
    "generationConfig": {
      "maxOutputTokens": 16384,
      "thinkingConfig": {"includeThoughts": true, "thinkingLevel": "HIGH"}
    }
  }
}
```

**System prompt:** Wrapped in `systemInstruction.parts[{text}]` (not a flat string).

**Message roles:** `"user"` and `"model"` (not "assistant").

**Tool calls:** `functionCall` / `functionResponse` (not "tool_use" / "tool_result").

---

### OpenAI-Compatible Payload Builder

> **Port from:** Uses standard `openai` SDK pointed at custom base URL
> `packages/ai/src/provider-models/openai-compat.ts:605`

Same as OpenAI Responses API, but:
- Base URL set to `http://127.0.0.1:11434/v1` (Ollama) or similar
- No prompt caching headers
- No reasoning params (local models typically don't support it)
- `store: false`

---

## Stream Event Model

> **Port from:** `packages/ai/src/utils/event-stream.ts:95` (`AssistantMessageEventStream`)
> Event types: `packages/ai/src/types.ts:419-441`

All providers emit into the same event stream interface:

```
start              → stream begins, partial AssistantMessage available
text_start         → new text content block started
text_delta         → text chunk received
text_end           → text block complete
thinking_start     → thinking block started
thinking_delta     → thinking chunk
thinking_end       → thinking block complete
toolcall_start     → tool call started
toolcall_delta     → tool call JSON argument chunk
toolcall_end       → tool call complete (name, id, parsed arguments)
done               → stream finished successfully
error              → stream finished with error
```

In Go, implement as a channel of `AssistantMessageEvent` or a callback interface.

---

## Prompt Caching

### Anthropic

> **Port from:** `providers/anthropic.ts:1183` (`applyPromptCaching`)

- `cache_control: {"type": "ephemeral"}` on up to 4 content blocks
- Placement: last tool → last system block → penultimate user msg → last user msg
- TTL `"1h"` for first breakpoint on `api.anthropic.com`, no TTL for others
- Reduces input token costs by ~90% on repeated turns

### OpenAI

- `prompt_cache_key: "<session_id>"` in request body
- `prompt_cache_retention: "24h"` for standard, `"in_memory"` for Codex
- Server-side; no client placement logic needed

### Google

- No explicit client-side caching mechanism for Gemini CLI

### Ollama

- No caching

---

## Thinking / Reasoning

> **Port from:** `providers/anthropic.ts:1416-1436` (Anthropic), `providers/openai-responses.ts:335-365` (OpenAI)

Map a unified effort level to each provider's format:

| Unified Level | Anthropic | OpenAI | Google |
|---|---|---|---|
| `off` | (omit thinking) | (omit reasoning) | (omit thinkingConfig) |
| `low` | `{"type": "adaptive"}` + `effort: "low"` | `effort: "low"` | `thinkingLevel: "LOW"` |
| `medium` | `{"type": "adaptive"}` + `effort: "medium"` | `effort: "medium"` | `thinkingLevel: "MEDIUM"` |
| `high` | `{"type": "adaptive"}` + `effort: "high"` | `effort: "high"` | `thinkingLevel: "HIGH"` |
| `max` | `{"type": "adaptive"}` + `effort: "max"` | `effort: "xhigh"` | `thinkingLevel: "HIGH"` |

**Anthropic special cases** (line 1456, 1470):
- If `tool_choice` forces a specific tool → disable thinking entirely
- `max_tokens` must be ≥ `thinkingBudgetTokens + OUTPUT_FALLBACK_BUFFER`

---

## Source Reference Map

All paths relative to `/Users/runger/workspaces/oh-my-pi/`.

### System Prompt & Templates

| File | Key Symbols | Lines |
|---|---|---|
| `packages/coding-agent/src/prompts/system/system-prompt.md` | Main template (24.5 KB Handlebars) | — |
| `packages/coding-agent/src/system-prompt.ts` | `buildSystemPrompt()` | — |
| `packages/coding-agent/src/config/prompt-templates.ts` | Handlebars helpers, rendering | — |

### Agent Definitions

| File | Agent | Lines |
|---|---|---|
| `packages/coding-agent/src/prompts/agents/plan.md` | Plan agent | 1-49 |
| `packages/coding-agent/src/prompts/agents/task.md` | Implement (task worker) agent | 1-17 |
| `packages/coding-agent/src/prompts/agents/reviewer.md` | Review agent | 1-128 |
| `packages/coding-agent/src/prompts/agents/explore.md` | Explore (scout) agent | 1-61 |

### Tool Prompts

| File | Tool |
|---|---|
| `packages/coding-agent/src/prompts/tools/read.md` | read |
| `packages/coding-agent/src/prompts/tools/write.md` | write |
| `packages/coding-agent/src/prompts/tools/replace.md` | replace |
| `packages/coding-agent/src/prompts/tools/grep.md` | grep |
| `packages/coding-agent/src/prompts/tools/find.md` | find |
| `packages/coding-agent/src/prompts/tools/bash.md` | bash |

### Provider Payload Construction

| File | Key Functions | Lines |
|---|---|---|
| `packages/ai/src/providers/anthropic.ts` | `buildParams()`, `buildAnthropicSystemBlocks()`, `convertAnthropicMessages()`, `convertTools()`, `applyPromptCaching()` | 1387, 1003, 1479, 1638, 1183 |
| `packages/ai/src/providers/openai-responses.ts` | `buildParams()` | 306 |
| `packages/ai/src/providers/openai-codex-responses.ts` | `streamOpenAICodexResponses` | 1286 |
| `packages/ai/src/providers/openai-codex/constants.ts` | Header constants | 7-32 |
| `packages/ai/src/providers/google-gemini-cli.ts` | `streamGoogleGeminiCli` | 455 |
| `packages/ai/src/providers/transform-messages.ts` | `transformMessages()` | 21 |

### Types

| File | Key Types | Lines |
|---|---|---|
| `packages/ai/src/types.ts` | `Message`, `Tool`, `Context`, `AssistantMessageEvent`, `Usage` | 255-441 |

---

## Suggested Go Package Structure

`llmclient` is a Go package inside the **`fabrikk` monorepo** at
`github.com/php-workx/fabrikk`. Imported by external tools as
`github.com/php-workx/fabrikk/llmclient`. See [`llm-auth.md`](./llm-auth.md#monorepo-layout)
for the full monorepo layout and import patterns.

```
fabrikk/llmclient/
├── context.go              # Context, Message, Tool types
│                           # (types.ts:255-441)
├── agent.go                # AgentDef, workflow definitions (plan/implement/review/explore)
│                           # (prompts/agents/*.md)
├── tools.go                # Tool registry, JSON Schema definitions for 8 tools
│                           # (prompts/tools/*.md)
├── prompt.go               # System prompt template + rendering
│                           # (system-prompt.ts, system-prompt.md)
├── transform.go            # Message transformation (ID normalization, orphan patching, thinking)
│                           # (transform-messages.ts)
├── events.go               # AssistantMessageEvent types and stream channel
│                           # (types.ts:419-441, event-stream.ts:95)
│
├── payload/
│   ├── anthropic.go        # buildParams, system blocks, billing header, cloaking ID,
│   │                       # tool prefix, prompt caching placement
│   │                       # (providers/anthropic.ts — the big one)
│   ├── openai.go           # Responses API payload (developer/system role)
│   │                       # (providers/openai-responses.ts:306)
│   ├── codex.go            # Codex payload (instructions field, previous_response_id)
│   │                       # (providers/openai-codex-responses.ts)
│   ├── google.go           # Gemini CLI payload (systemInstruction.parts, thinkingConfig)
│   │                       # (providers/google-gemini-cli.ts:455)
│   └── ollama.go           # OpenAI-compatible (same as openai.go with custom baseURL)
│
└── prompt/
    ├── system.go.tmpl      # Go template (ported from system-prompt.md)
    ├── plan.go.tmpl        # Plan agent instructions
    ├── implement.go.tmpl   # Implement agent instructions
    ├── review.go.tmpl      # Review agent instructions + criteria + priority table
    └── explore.go.tmpl     # Explore agent instructions
```

**Go-specific notes:**
- Templates: Use `text/template` instead of Handlebars — simpler, native, no dependency
- JSON Schema: Use `map[string]interface{}` or a struct — no need for TypeBox
- Tool prefix: Simple `"proxy_" + name` string concatenation, with a `builtinTools` set for exceptions
- Billing header: `crypto/sha256` for hash, `crypto/rand` for random bytes
- Cloaking ID: `crypto/rand` for 32 bytes hex, `github.com/google/uuid` for UUIDs

---

## Implementation Checklist

### Core Types
- [ ] `Context`, `Message` (union), `Tool`, `Usage`, `Cost` structs
- [ ] `AssistantMessageEvent` struct + event type constants
- [ ] `TextContent`, `ThinkingContent`, `ToolCall`, `ImageContent` content blocks
- [ ] `UserMessage`, `AssistantMessage`, `ToolResultMessage` with JSON tags

### Tool Registry
- [ ] 6 core tools: `read`, `write`, `replace`, `grep`, `find`, `bash`
- [ ] 2 review tools: `report_finding`, `submit_result`
- [ ] JSON Schema for each tool's parameters
- [ ] Tool description text (from .md files, stripped of Handlebars/conditionals)

### Agent Definitions
- [ ] `plan` — read-only, strong model, high thinking
- [ ] `task` (implement) — read-write, balanced model, medium thinking
- [ ] `reviewer` — read-only, strong model, high thinking, review criteria/priorities
- [ ] `explore` — read-only, fast model, medium thinking

### System Prompt
- [ ] Go template ported from `system-prompt.md` (simplified, ~5-8 KB instead of 24 KB)
- [ ] Template data: environment, context files, tool descriptions, agent instructions
- [ ] Rendering function: `RenderSystemPrompt(data PromptData) string`

### Message Transformation
- [ ] Tool call ID normalization (Anthropic 64-char limit)
- [ ] Orphaned tool call patching (synthetic error results)
- [ ] Thinking block handling (strip/preserve/convert)
- [ ] Auto-continue: append `"Continue."` if last message is assistant

### Provider Payload Builders
- [ ] **Anthropic:** system block array (billing + instruction + prompt), `proxy_` tool prefixing, cloaking user ID, cache_control placement (4 breakpoints), thinking params
- [ ] **OpenAI Responses:** developer/system role selection, reasoning params, prompt_cache_key
- [ ] **OpenAI Codex:** instructions field, previous_response_id, text.verbosity
- [ ] **Google Gemini CLI:** systemInstruction.parts wrapper, functionDeclarations, thinkingConfig
- [ ] **Ollama:** OpenAI-compatible with custom base URL, no caching/reasoning

### Thinking / Reasoning
- [ ] Unified effort level (`off`/`low`/`medium`/`high`/`max`)
- [ ] Per-provider mapping (Anthropic adaptive, OpenAI reasoning, Google thinkingLevel)
- [ ] Anthropic: disable thinking when tool_choice forces specific tool
