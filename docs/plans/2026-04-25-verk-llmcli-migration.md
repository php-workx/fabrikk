# Verk LLM CLI Migration Implementation Plan

> **For implementation agents:** Treat this document as the migration contract.
> Preserve Verk runtime behavior first; delete local subprocess code only after
> adapter parity is pinned by tests.

## Goal

Migrate `/Users/runger/workspaces/verk` from its local Claude/Codex subprocess
adapters to `github.com/php-workx/fabrikk/llmcli` without weakening:

- worktree isolation via `runtime.BuildIsolatedProcessEnv`
- runtime names and public adapter constructors
- timeout and process-tree cancellation behavior
- stdout/stderr/result artifact paths
- worker/reviewer result normalization
- Codex retry classification and, if retained, Codex telemetry extraction

## Current Review Snapshot

This plan was reviewed after the Fabrikk LLM CLI execution-control API landed.
The prior plan is now stale in several places.

### Fabrikk Baseline Now Available

`fabrikk` now exposes the reusable controls Verk needs:

- `llmclient.WithWorkingDirectory`
- `llmclient.WithEnvironment`
- `llmclient.WithEnvironmentOverlay`
- `llmclient.WithTimeout`
- `llmclient.WithReasoningEffort`
- `llmclient.WithRawCapture`
- `llmclient.WithCodexJSONL`
- `llmcli.NewBackendByName`

Fabrikk verification already passed for the execution-control work:

```bash
go test ./llmclient ./llmcli
just pre-commit
```

Do not reimplement those API tasks in Verk. Consume them.

### Verk Baseline Reviewed

The current Verk implementation still has two full local subprocess adapters:

- `/Users/runger/workspaces/verk/internal/adapters/runtime/claude/adapter.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/codex/adapter.go`

Current focused runtime verification passes:

```bash
go test ./internal/adapters/runtime/...
```

Existing behavior that must survive the migration:

- `runtimeAdapterFor` keeps runtime names as `codex` and `claude`.
- `doctor` reports runtime names as `codex` and `claude`.
- `New()` and `NewWithCommand(command string)` remain available in both
  adapter packages.
- Claude uses `runtime.BuildIsolatedProcessEnv(os.Environ(), worktreePath)`,
  runs in `req.WorktreePath`, supports progress through stream-json tool-use
  events, and writes full stdout/stderr artifacts.
- Codex uses `runtime.BuildIsolatedProcessEnv(os.Environ(), worktreePath)`,
  routes worktree access through `codex exec -C <worktree>`, propagates model
  and reasoning effort, extracts quota/rate-limit failure messages from JSONL,
  and extracts token/activity telemetry from JSONL when available.

## Integration Findings

### Finding 1: `codex` Is Not an `llmcli` Backend Name

Verk runtime name `codex` must map to `llmcli` backend `codex-exec` for this
migration. Do not call `llmcli.SelectBackendByName("codex")`; no such backend
is registered. Keep `codex-appserver` disabled for Verk until it can satisfy
worktree, artifact, raw-output, and telemetry parity.

### Finding 2: Custom Binary Constructors Need PATH Resolution

`llmcli.NewBackendByName` intentionally bypasses detection. If Verk passes
`CliInfo{Path: "codex"}`, `Available()` will fail because `CliBackend` uses
`os.Stat(info.Path)`. `NewWithCommand("codex")` must validate the executable and
resolve bare command names with `exec.LookPath`; absolute or relative paths with
path separators can be passed through after validation.

### Finding 3: Codex Telemetry Requires Opt-In JSONL Mode

Verk currently runs Codex with `--json` and extracts token/activity telemetry
from JSONL stdout. Fabrikk `codex-exec` currently runs a plain-text fallback and
does not pass `--json` unless callers opt in.

Use `llmclient.WithCodexJSONL(true)` for Verk's Codex bridge. In that mode,
`codex-exec` passes `--json`, preserves the raw JSONL stdout stream via
`WithRawCapture`, emits normalized text events from agent-message events, and
maps `turn.completed` usage to `EventDone.Usage`.

### Finding 4: Event Accumulation Must Avoid Duplicate Text

Some backends emit text deltas and also include the final assistant message on
`EventDone`. The Verk bridge should accumulate streamed text once and use
`EventDone.Message` only when no text events were observed.

### Finding 5: `EventError` Has No Exit Code

`llmclient.EventError` carries an error string, not a subprocess exit code.
The bridge should map it to a non-zero synthetic exit code for Verk's existing
classification helpers, append the error string to captured stderr, and keep raw
stdout/stderr artifacts intact.

## Updated Migration Tasks

## Task 1: Pin Fabrikk in Verk

**Files:**

- `/Users/runger/workspaces/verk/go.mod`
- `/Users/runger/workspaces/verk/go.sum`

For local migration work:

```bash
go mod edit -replace github.com/php-workx/fabrikk=/Users/runger/workspaces/fabrikk
go get github.com/php-workx/fabrikk@v0.0.0
go mod tidy
```

Before merge, replace the local `replace` with a real tag or pseudo-version:

```bash
go get github.com/php-workx/fabrikk@<tag-or-commit>
go mod edit -dropreplace github.com/php-workx/fabrikk
go mod tidy
```

Verify:

```bash
go list -m github.com/php-workx/fabrikk
go test ./internal/adapters/runtime/...
```

## Task 2: Add a Verk `llmcli` Bridge Package

**Create:**

- `/Users/runger/workspaces/verk/internal/adapters/runtime/llmcli/bridge.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/llmcli/artifacts.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/llmcli/normalize.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/llmcli/bridge_test.go`

**Reuse:**

- `/Users/runger/workspaces/verk/internal/adapters/runtime/prompt.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/normalize.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/cacheenv.go`
- existing Claude/Codex status derivation helpers, moved to shared bridge code

### Required Design

Create a bridge that implements `runtime.Adapter`:

```go
type Adapter struct {
    RuntimeName string
    BackendName string
    Command     string
    NewBackend  func() (llmclient.Backend, error)
    Now         func() time.Time
}
```

Backend mapping:

- Verk `claude` -> `llmcli` backend `claude`
- Verk `codex` -> `llmcli` backend `codex-exec`

Default constructors may use `llmcli.SelectBackendByName(backendName)`.
Custom command constructors must use `llmcli.NewBackendByName(backendName,
CliInfo{...})` with a resolved executable path.

For custom commands:

1. call `runtime.ValidatedExecutable(command)`
2. if the executable has no path separator, resolve it with `exec.LookPath`
3. pass the resolved path in `CliInfo.Path`
4. keep the original runtime name as `RuntimeName`

### Required Request Conversion

Worker request:

```go
llmclient.Context{
    SystemPrompt: runtime.WorkerSystemPrompt(),
    Messages: []llmclient.Message{{
        Role: llmclient.RoleUser,
        Content: []llmclient.ContentBlock{{
            Type: llmclient.ContentText,
            Text: runtime.BuildWorkerPrompt(req),
        }},
    }},
}
```

Reviewer request uses `runtime.ReviewerSystemPrompt()` and
`runtime.BuildReviewPrompt(req)`.

Options:

```go
opts := []llmclient.Option{
    llmclient.WithModel(req.Model),
    llmclient.WithWorkingDirectory(req.WorktreePath),
    llmclient.WithEnvironment(env),
    llmclient.WithTimeout(timeout),
    llmclient.WithRawCapture(capture.Append),
    llmclient.WithRequiredOptions(
        llmclient.OptionWorkingDirectory,
        llmclient.OptionEnvironment,
        llmclient.OptionTimeout,
        llmclient.OptionRawCapture,
    ),
}
```

For Codex only, include:

```go
llmclient.WithReasoningEffort(req.Reasoning)
llmclient.WithCodexJSONL(true)
llmclient.WithRequiredOptions(llmclient.OptionReasoningEffort)
llmclient.WithRequiredOptions(llmclient.OptionCodexJSONL)
```

For Claude, do not pass `WithReasoningEffort`; Claude has no verified mapping
and Fabrikk correctly reports it unsupported.

Environment:

```go
env, err := runtime.BuildIsolatedProcessEnv(os.Environ(), req.WorktreePath)
```

Never bypass this helper.

### Required Event Handling

The bridge should produce a local `commandResult` equivalent:

```go
type commandResult struct {
    stdout []byte
    stderr []byte
    text   string
    exitCode int
}
```

Rules:

- raw capture stdout/stderr becomes the artifact source of truth
- append `EventTextDelta` content to the normalized text buffer
- if no deltas were seen, use `EventTextEnd.Content`
- if no text events were seen, use text blocks from `EventDone.Message`
- on `EventError`, set `exitCode = 1` and append `ErrorMessage` to stderr
- on cancelled done events, set `exitCode = 1` and classify as retryable unless
  existing helpers identify missing context/operator input
- do not append both deltas and done-message text

Progress:

- call `OnProgress` for `EventToolCallStart` and `EventToolCallEnd` when those
  events are present
- Claude should continue to produce progress via `stream-json`
- Codex exec currently has no tool progress parity; do not invent progress from
  plain text

Artifacts:

- always write stdout, stderr, and normalized result/review JSON artifacts
- keep artifact field names compatible with the current Claude/Codex artifacts
- include `backend_name` and `fidelity` as additional diagnostic fields
- keep `Runtime` set to Verk runtime name (`claude` or `codex`), not
  `codex-exec`

### Bridge Tests

Use a fake `llmclient.Backend`; do not spawn real CLIs.

Pin:

- Worker and reviewer context construction
- model, reasoning, working directory, environment, timeout, and raw capture
  options
- required unsupported options fail before execution
- raw stdout/stderr artifact writes
- text accumulation without duplication
- `EventError` classification and stderr artifact content
- progress callback from tool-call events
- constructor mapping for `claude` and `codex`
- custom command PATH resolution for bare command names

Run:

```bash
go test ./internal/adapters/runtime/llmcli
```

## Task 3: Pin Codex JSONL Telemetry Parity in Verk

Fabrikk now exposes `llmclient.WithCodexJSONL(true)`. Verk must use it for the
Codex bridge so raw capture remains the complete Codex JSONL stdout stream.

Bridge behavior:

- pass `WithCodexJSONL(true)` for Codex requests
- keep parsing token/activity telemetry from captured raw stdout
- continue using captured stderr and JSONL failure events for quota/rate-limit
  and missing-context classification
- assert `EventDone.Usage` is populated from `turn.completed` when available
- do not use JSONL mode for Claude

Acceptance tests:

```bash
go test ./internal/adapters/runtime/llmcli -run 'Test.*Codex.*JSON|Test.*Telemetry|Test.*Raw'
go test ./internal/adapters/runtime/codex
```

## Task 4: Migrate Verk Claude Adapter

**Modify:**

- `/Users/runger/workspaces/verk/internal/adapters/runtime/claude/adapter.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/claude/adapter_test.go`

**Delete after parity:**

- local Claude subprocess helpers and process-group files

Make `claude.Adapter` a thin wrapper around the bridge while preserving:

```go
func New() *Adapter
func NewWithCommand(command string) *Adapter
func (a *Adapter) CheckAvailability(ctx context.Context) error
func (a *Adapter) RunWorker(ctx context.Context, req runtime.WorkerRequest) (runtime.WorkerResult, error)
func (a *Adapter) RunReviewer(ctx context.Context, req runtime.ReviewRequest) (runtime.ReviewResult, error)
```

Focused tests:

- artifact capture
- worker/reviewer normalization
- `OnProgress`
- timeout classification
- missing context classification
- custom command constructor
- availability check

Run:

```bash
go test ./internal/adapters/runtime/claude
```

## Task 5: Migrate Verk Codex Adapter

**Modify:**

- `/Users/runger/workspaces/verk/internal/adapters/runtime/codex/adapter.go`
- `/Users/runger/workspaces/verk/internal/adapters/runtime/codex/adapter_test.go`

**Delete after parity:**

- local Codex subprocess helpers and process-group files

Make `codex.Adapter` a thin wrapper around the bridge using backend
`codex-exec`.

Focused tests:

- worktree path is passed through `WithWorkingDirectory` and verified by the
  fake backend as `OptionWorkingDirectory`
- model is propagated
- reasoning effort is propagated and required
- `runtime.BuildIsolatedProcessEnv` output is passed via `WithEnvironment`
- quota/rate-limit errors remain retryable and preserve useful messages
- missing-context errors remain blocked-by-operator-input
- sentinel fallback still works for `VERK_RESULT:` and `VERK_REVIEW:`
- token/activity telemetry is preserved from raw JSONL capture

Run:

```bash
go test ./internal/adapters/runtime/codex
```

## Task 6: Runtime Selection and Doctor Checks

**Modify only if needed:**

- `/Users/runger/workspaces/verk/internal/cli/shared.go`
- `/Users/runger/workspaces/verk/internal/engine/doctor.go`

The CLI and doctor can keep importing the `claude` and `codex` wrapper
packages. Do not leak `codex-exec` into user-facing runtime names.

Focused tests:

- `runtimeAdapterFor("", "")` still defaults to Codex
- `runtimeAdapterFor("claude", "codex")` returns Claude
- unsupported runtimes keep the current error shape
- doctor reports `claude` and `codex`, not `codex-exec`
- missing binaries produce actionable details

Run:

```bash
go test ./internal/cli ./internal/engine -run 'Test.*Runtime|Test.*Doctor'
```

## Task 7: Full Verk Validation

Run:

```bash
go test ./internal/adapters/runtime/...
just pre-commit
just pre-push
```

Because this changes runtime execution, `just pre-push` is required before the
migration is considered complete.

Manual smoke tests:

- run a small Verk ticket with Claude
- run a small Verk ticket with Codex
- confirm `.verk` run artifacts include normalized worker/reviewer results
- confirm stdout/stderr artifact paths exist and contain the captured process
  output
- confirm worktree-local commands execute inside the assigned worktree

## Non-Goals

- Do not migrate Verk to `codex-appserver` in this plan.
- Do not change `.verk` artifact schema except for additive diagnostics.
- Do not remove `runtime.BuildIsolatedProcessEnv`.
- Do not rename runtime values from `claude`/`codex`.
- Do not move retry classification into Fabrikk; Verk owns product semantics.

## Completion Checklist

- Fabrikk dependency is pinned or replaced intentionally.
- Bridge tests pass with fake backends.
- Claude wrapper tests pass.
- Codex wrapper tests pass.
- Codex telemetry behavior is either preserved or explicitly documented as a
  fidelity reduction.
- `go test ./internal/adapters/runtime/...` passes in Verk.
- `just pre-commit` passes in Verk.
- `just pre-push` passes in Verk.
- No local subprocess adapter code remains unless a documented parity gap still
  requires it.
