# CrewAI Fit Analysis for `attest`

Date: 2026-03-12
Status: Research note
Scope: Evaluate whether CrewAI can accelerate `attest` implementation without taking over canonical state, detached lifecycle, or CLI-native control.

## Summary

CrewAI is a plausible fit for selected internal orchestration layers in `attest`, but not for the core runtime.

The strongest CrewAI capabilities for our use case are:

- Flow state and persistence
- role-based crews and hierarchical delegation
- structured outputs with Pydantic
- callbacks, event listeners, and streaming
- built-in human feedback support in Flows

The main reasons not to let CrewAI become the core of `attest` are:

- it is Python-first while `attest` v1 is specified in Go
- its LLM integrations are API-centric, not CLI-subscription-centric
- its persistence and control model are not the same as `.attest/` canonical artifacts
- it would blur the boundary between `attest` as source of truth and an embedded agent framework

Recommendation:

- keep `attest` as the run engine, state owner, and CLI control plane
- evaluate CrewAI only as a narrow adapter for reviewer/council subflows
- do not use CrewAI for task ledger ownership, claims, run lifecycle, or detached execution in v1

## Official Capabilities Relevant to `attest`

## Direct Package Inspection

I also inspected the currently published `crewai` package on PyPI directly.

Observed package facts:

- current PyPI version seen during this spike: `0.11.2`
- the published wheel depends on:
  - `langchain`
  - `langchain-openai`
  - `openai`
- the default `Agent.llm` is `ChatOpenAI(...)`
- the package is built around provider/model objects, not local CLI wrappers

Most important detail:

- in the published wheel inspected during this spike, there is no visible `BaseLLM` implementation inside the package itself
- `Agent.llm` is typed as `Any`, which means custom integration is probably still possible, but through custom LangChain-compatible objects or another adapter path, not through an obvious built-in local-CLI integration layer

Implication:

- CrewAI is not only provider-oriented in docs; the currently published package surface also defaults hard toward OpenAI-style integrations
- local CLI subscription execution would require custom integration work

### 1. Flows

CrewAI Flows are event-driven workflows with explicit state, branching, listeners, and persistence-friendly execution.

Useful implications for `attest`:

- a council pipeline can be modeled as a Flow
- a review or approval subflow can have clear state transitions
- long-running reviewer steps can be resumed if CrewAI persistence is used

Potential fit:

- reviewer orchestration
- synthesis subflow
- human approval subflow prototypes

Poor fit:

- owning overall run lifecycle
- owning `.attest/runs/<run-id>/` state

### 2. Persistence and resume

CrewAI documents `@persist` for Flows and explicitly says this supports resuming if the process crashes or when waiting for human input.

Useful implications for `attest`:

- CrewAI could reduce effort for a narrow persisted review flow
- reviewer or approval subflows could survive restarts

Important limitation:

- CrewAI persistence is Flow-centric and database-oriented
- `attest` requires repo-local canonical artifacts and deterministic auditability under `.attest/`

Conclusion:

- useful as internal plumbing
- not acceptable as canonical run-state ownership

### 3. Structured outputs

CrewAI strongly encourages structured outputs via Pydantic and JSON.

Useful implications for `attest`:

- reviewer outputs could be normalized cleanly
- council verdicts can be constrained into enums and typed payloads
- repair recommendations can be generated into structured objects

This is one of the strongest areas of fit.

### 4. Hierarchical process / manager pattern

CrewAI supports a hierarchical process with a manager LLM or manager agent delegating and validating work.

Useful implications for `attest`:

- this maps conceptually to our Mayor / supervisor model
- it could help implement internal review delegation

Limitations:

- CrewAI’s manager semantics do not replace `attest`’s wave planner, claim system, or file-scope safety rules
- letting CrewAI directly manage task ownership would weaken our spec-specific closure logic

Conclusion:

- use as inspiration or bounded subflow logic
- do not use as the authoritative dispatcher

### 5. Async execution and streaming

CrewAI supports async kickoff and streaming output.

Useful implications for `attest`:

- parallel reviewer calls could be orchestrated more easily
- live review progress could be streamed back into logs or status renderers

This is attractive for reviewer fan-out, especially Codex + Gemini + synthesis preparation.

### 6. Event listeners and callbacks

CrewAI exposes event listeners plus task and step callbacks.

Useful implications for `attest`:

- better instrumentation if we embed CrewAI in a narrow layer
- easier hooks for logging, tracing, and metrics
- possible bridge from CrewAI events into `.attest/events.jsonl`

This is useful if we want to preserve our own observability while borrowing CrewAI execution.

### 7. Human feedback in Flows

CrewAI documents `@human_feedback` for Flow review gates.

Useful implications for `attest`:

- approval/review subflows could use a built-in pause-and-continue pattern
- clarification checkpoints could be prototyped quickly

Limitation:

- this is a better fit for local synchronous review or Enterprise deployments than for our repo-local CLI-first engine

Conclusion:

- promising for optional approval/clarification helpers
- not required for v1 core

## Capability Gaps Against `attest`

### 1. CLI-subscription-native model execution

This is the biggest mismatch.

CrewAI officially connects through provider SDKs and LiteLLM-style model integrations. The docs emphasize provider/API configuration, custom `BaseLLM`, and API endpoints. This does not naturally map to:

- Claude CLI subscription use
- Codex CLI subscription use
- Gemini CLI subscription use

It may be possible to build a custom `BaseLLM` wrapper that shells out to local CLIs, but that is non-trivial and would become custom infrastructure anyway.

Implication:

- CrewAI does not directly solve our strongest adoption constraint

### 1.1 Current evidence on API keys vs local CLI subscriptions

CrewAI's official docs are provider- and API-oriented by default:

- the CrewAI CLI prompts for provider selection and API key entry during setup
- provider configuration examples are expressed through API keys, base URLs, and SDK-style model objects
- the documented extension path for unsupported providers is a custom `BaseLLM` implementation

Implication for `attest`:

- CrewAI does not appear to be prepared out of the box to use local subscription-backed CLIs like `claude`, `codex`, or `gemini`
- if we want that behavior, we would need to build a custom adapter layer that shells out to those CLIs and maps their I/O into CrewAI's LLM interface

This makes the real v1 question narrower:

- not "can CrewAI run multi-agent workflows?" because it clearly can
- but "is building and maintaining a CLI-shelling `BaseLLM` bridge worth the complexity just for the council layer?"

### 2. Canonical repo-local state

`attest` is designed around:

- `run-artifact.json`
- `tasks.json`
- claims
- reports
- `run-status.json`
- `events.jsonl`

CrewAI has its own concepts of flows, crews, state, tasks, and persistence. If we let it own the primary control plane, we would either:

- duplicate state, or
- surrender `attest`’s auditability boundary

Neither is acceptable for v1.

### 3. Deterministic closure contract

`attest` has very specific rules around:

- requirement linkage
- deterministic verifier
- mandatory Codex and Gemini review
- Opus synthesis
- repair deduplication
- blocked vs done semantics

CrewAI gives orchestration primitives, but these closure semantics remain our job.

### 4. Git/workspace safety

CrewAI does not remove the need for:

- wave planning
- file-scope ownership
- overlap checks
- shared-repo write safety

Those are still `attest` responsibilities.

## Best Potential Uses in `attest`

### Recommended use case 1: Council subflow adapter

This is the best fit.

Shape:

1. `attest` creates the bounded review packet.
2. `attest` calls a CrewAI Flow or Crew for:
   - Codex review
   - Gemini review
   - Opus synthesis preparation
3. CrewAI returns structured outputs.
4. `attest` writes canonical review artifacts and makes the state transition decision.

Benefits:

- less custom orchestration code for fan-out/fan-in review stages
- easier structured outputs
- optional callbacks/listeners for logs and tracing

Constraints:

- CrewAI never writes canonical `.attest/` state directly
- `attest` still owns verdict application

### Recommended use case 2: Approval / clarification helper flow

CrewAI Flow plus human feedback could help prototype:

- artifact approval
- clarification questions
- revise/approve loops

This is secondary to the council use case.

### Possible use case 3: Internal synthesis helper

If we want a strongly typed synthesis step, CrewAI structured outputs may help normalize:

- deduped findings
- severity ranking
- reopen vs child repair recommendation

This is useful but not enough alone to justify the dependency.

## Bad Uses

Do not use CrewAI for:

- the detached run engine
- canonical `.attest` state ownership
- claims / leases
- task compilation
- wave scheduling
- repo mutation authority
- final closure policy enforcement

## Practical Integration Options

### Option A: No CrewAI in v1

Pros:

- simplest architecture
- no Python sidecar
- no mismatch with CLI-native backend constraint

Cons:

- more council orchestration code to write ourselves

### Option B: CrewAI only for reviewer/council subflows

Pros:

- best balance of leverage and control
- limits architectural blast radius
- lets us borrow Flow/structured-output/event features

Cons:

- adds a Python runtime and integration boundary
- still requires custom adapters for our CLI-based model execution story

### Option C: CrewAI as broad internal orchestrator

Pros:

- faster initial prototyping for agent flows

Cons:

- too much overlap with `attest` responsibilities
- increases risk of split-brain state
- weak fit for CLI-subscription execution

Recommendation: reject Option C.

## Recommended Decision

Adopt this decision for v1:

- `attest` remains the only owner of canonical state and lifecycle
- CrewAI is optional
- if adopted in v1, it is limited to reviewer/council subflows
- use structured outputs and callbacks if they materially reduce implementation complexity
- do not attempt to force core detached execution or claim management into CrewAI

## Follow-up Research Needed

Before adopting CrewAI even for the council layer, validate:

1. Whether a custom CrewAI `BaseLLM` adapter can reliably shell out to local `claude`, `codex`, and `gemini` CLIs.
2. Whether CrewAI Flow persistence can be used without introducing a second source of truth.
3. Whether the Python sidecar cost is worth the reduced orchestration code.
4. Whether reviewer fan-out is easier to implement directly in Go than via a Python bridge.

## Bottom Line

CrewAI likely has value for `attest`, but only at the edges.

The strongest fit is:

- typed reviewer flows
- council fan-out/fan-in
- optional approval / clarification helpers

The weakest fit is:

- anything that touches core run state, detached execution, or task ownership

So the right posture is:

- borrow orchestration logic where it saves real effort
- keep `attest` itself as the durable system of record
