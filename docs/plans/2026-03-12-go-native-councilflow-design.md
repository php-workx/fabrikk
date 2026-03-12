# Go-Native Councilflow Design

Status: Integrated into `docs/specs/attest-technical-spec.md` and `docs/plans/2026-03-12-attest-v1-implementation-plan.md`. Keep this note as supporting rationale, not as the canonical spec.

**Goal:** Define a small Go-native orchestration layer inside `attest` that covers the useful subset of CrewAI for reviewer/council workflows without importing CrewAI's Python runtime, API-key assumptions, or state model.

**Recommendation:** Build `internal/councilflow`, not a generic `internal/flow`, in v1. Keep the scope narrow and directly aligned with `attest`'s council pipeline.

---

## 1. Why this exists

We considered two alternatives:

- embed CrewAI as an internal orchestration layer
- port or rewrite CrewAI concepts into Go

Both are too broad for what `attest` actually needs.

`attest` does not need a general-purpose agent framework. It needs a reliable way to:

- fan out bounded review tasks
- collect structured reviewer outputs
- run synthesis after reviewer completion
- persist artifacts in `.attest/`
- emit events and status updates
- do all of that with local `claude`, `codex`, and `gemini` CLI execution

That is a much smaller problem than CrewAI.

So the right move is to build a very small Go-native orchestration package for the council path only.

## 2. Non-goals

This design is explicitly **not**:

- a replacement for the `attest` run engine
- a replacement for claims, leases, or wave scheduling
- a general task-graph runtime
- a provider SDK abstraction
- a second source of truth
- a framework for arbitrary agent swarms

Anything touching canonical run lifecycle remains outside this package.

## 3. Scope boundary

`internal/councilflow` owns:

- bounded reviewer fan-out
- reviewer retry policy inside one task attempt
- synthesis sequencing
- structured council outputs
- event emission for council-related phases

`attest` core still owns:

- `run-artifact.json`
- `tasks.json`
- claim and lease semantics
- wave planning
- detached run engine lifecycle
- task state transitions
- final application of council verdicts to canonical state

This keeps the abstraction honest.

## 4. Core design

### 4.1 Package name

Use:

- `internal/councilflow`

Not:

- `internal/flow`

Reason:

- `flow` invites framework creep
- `councilflow` keeps the surface tied to the only workflow we know we need

### 4.2 Execution model

Per task attempt:

1. `attest` prepares the bounded review bundle
2. `councilflow` launches Codex and Gemini review jobs in parallel
3. `councilflow` waits for both reviewer jobs to finish or exhaust retry policy
4. `councilflow` launches Opus synthesis with the structured reviewer outputs
5. `councilflow` returns a typed result object to `attest`
6. `attest` persists canonical artifacts and performs the state transition

This is a one-shot orchestration per task attempt, not a long-lived autonomous subsystem.

### 4.3 Persistence model

`councilflow` must not own persistence.

It may:

- receive paths to write temp outputs
- emit typed outputs for the caller to persist
- emit progress events through an interface

It must not:

- mutate `tasks.json`
- decide run state
- decide task state
- write canonical state directly except through a caller-provided writer interface

## 5. Package responsibilities

### 5.1 Included responsibilities

- run reviewer jobs concurrently
- normalize retry and timeout handling
- collect reviewer artifacts
- enforce structured reviewer result contracts
- run synthesis after reviewer completion
- return a single council result object

### 5.2 Excluded responsibilities

- building the task graph
- selecting which task to review
- claim acquisition
- backend authentication setup
- deciding whether work should continue after council result

## 6. Proposed public interfaces

These are internal Go interfaces, not user-facing CLI contracts.

### 6.1 ReviewBundle

```go
type ReviewBundle struct {
    RunID              string
    TaskID             string
    AttemptID          string
    RequirementSubset  []RequirementRef
    ChangedFiles       []string
    DiffPath           string
    CompletionReport   string
    VerifierResult     string
    PriorFindings      []FindingRef
    InputBundleHash    string
    ContextBudget      int
}
```

### 6.2 ReviewerBackend

```go
type ReviewerBackend interface {
    Name() string
    Review(ctx context.Context, bundle ReviewBundle) (ReviewerResult, error)
}
```

Implementations:

- `CodexReviewer`
- `GeminiReviewer`

### 6.3 SynthesizerBackend

```go
type SynthesizerBackend interface {
    Name() string
    Synthesize(ctx context.Context, input SynthesisInput) (CouncilResult, error)
}
```

Implementation:

- `OpusSynthesizer`

### 6.4 EventSink

```go
type EventSink interface {
    Emit(ctx context.Context, event CouncilEvent) error
}
```

This bridges councilflow into `.attest/events.jsonl` without giving it file ownership.

### 6.5 ArtifactWriter

```go
type ArtifactWriter interface {
    WriteReviewerResult(ctx context.Context, runID, taskID string, result ReviewerResult) error
    WriteCouncilResult(ctx context.Context, runID, taskID string, result CouncilResult) error
}
```

This allows `attest` to preserve canonical file layout while councilflow stays persistence-light.

### 6.6 Runner

```go
type Runner interface {
    RunTaskCouncil(ctx context.Context, req RunTaskCouncilRequest) (RunTaskCouncilResult, error)
}
```

## 7. Core data types

### 7.1 ReviewerResult

```go
type ReviewerResult struct {
    Backend            string
    TaskID             string
    AttemptID          string
    Verdict            string
    Confidence         string
    BlockingFindings   []Finding
    NonBlockingFindings []Finding
    Duration           time.Duration
    RetryCount         int
    BackendVersion     string
}
```

### 7.2 CouncilResult

```go
type CouncilResult struct {
    TaskID              string
    AttemptID           string
    Verdict             string
    SynthesizedFindings []Finding
    FollowUpAction      string
    DismissalRationale  string
    Duration            time.Duration
}
```

### 7.3 CouncilEvent

```go
type CouncilEvent struct {
    RunID      string
    TaskID     string
    AttemptID  string
    Phase      string
    Backend    string
    Status     string
    Timestamp  time.Time
    Summary    string
}
```

## 8. Failure handling

`councilflow` should be opinionated about mechanics, not policy.

Mechanics it should handle:

- parallel execution of reviewer backends
- timeout application
- retry counting
- cancellation propagation
- partial result collection

Policy it should not own:

- whether a failed reviewer blocks task closure
- whether a council result should reopen or block a task

That policy remains in `attest`, because it is spec-defined.

## 9. Retry model

Suggested default behavior:

- reviewer timeout per attempt: inherited from `attest` backend policy
- retries per reviewer: inherited from `attest`
- synthesis only runs once reviewer fan-out has reached terminal reviewer outcomes

`councilflow` should expose retry counts and terminal backend statuses, but it should not decide whether to downgrade or skip a reviewer. That remains a caller policy.

## 10. Concurrency model

Inside a single task attempt:

- Codex and Gemini run in parallel
- Opus synthesis runs after both reviewer lanes complete

Across task attempts:

- `councilflow` is safe to call concurrently by the run engine
- it must not rely on package-global mutable state
- all per-run/per-task state must be passed through request objects

This makes it composable with wave-based execution.

## 11. CLI backend integration

This is the main reason to build this in Go instead of using CrewAI directly.

Each backend implementation should be a thin wrapper around the local CLI:

- `claude`
- `codex`
- `gemini`

Responsibilities of each wrapper:

- construct the backend-specific subprocess invocation
- feed the bounded prompt/input bundle
- capture stdout/stderr
- parse structured JSON or constrained text output
- return typed results

This keeps the local subscription model first-class.

## 12. Why this is better than porting CrewAI

Porting CrewAI would still leave us needing to:

- remove Python/API assumptions
- replace provider-centric model integration
- redesign persistence around `.attest/`
- redesign lifecycle around `attest`'s detached engine

So a "port" would mostly be a rewrite anyway.

`councilflow` avoids that mistake by building only the part we actually want.

## 13. Recommended file layout

```text
internal/
  councilflow/
    runner.go
    types.go
    events.go
    retry.go
    runner_test.go
    backends/
      codex.go
      gemini.go
      opus.go
      parse.go
      prompts.go
```

Optional later split:

- `internal/reviewbundle`
- `internal/councilflow/backends`

But not in v1 unless the code actually grows enough to justify it.

## 14. Suggested implementation order

1. Define typed request/result structs
2. Define `ReviewerBackend`, `SynthesizerBackend`, `EventSink`, `ArtifactWriter`
3. Implement a fake backend for tests
4. Implement `Runner.RunTaskCouncil`
5. Add parallel reviewer execution and synthesis sequencing
6. Add timeout/retry wrappers
7. Add CLI-backed Codex/Gemini/Opus adapters
8. Integrate with the `attest` council CLI and caller-owned persistence layer

## 15. Acceptance criteria

The design is good enough if it allows us to:

- run Codex and Gemini reviewers in parallel for one task attempt
- emit structured results with stable finding IDs
- run Opus synthesis after reviewer completion
- write `codex-review.json`, `gemini-review.json`, `council-result.json`, and `attempt.json` through caller-owned persistence
- emit progress into `.attest/events.jsonl`
- work entirely through local CLIs instead of provider API keys

## 16. Recommendation

Build `internal/councilflow` in Go and treat it as:

- a narrow internal submodule
- purpose-built for `attest`
- CLI-subscription-native
- explicitly smaller than CrewAI

This gives us the useful orchestration behavior without dragging in the wrong runtime model.
