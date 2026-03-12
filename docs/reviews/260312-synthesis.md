# Technical Spec Review Synthesis

Document under review: `docs/specs/attest-technical-spec.md` v0.1
Input reviews: Systems Architect (SA), Spec-Conformance (SC), Agent Workflow (AW)
Date: 2026-03-12

---

## Final Verdict

- **FAIL**
- Rationale: The spec defines the product shape and data flow clearly, but lacks the operational rules an implementer needs to build safely. Six blocking findings each represent a class of ambiguity that would force the implementer to invent core semantics -- process lifecycle, concurrency, state transitions, verification strength, run closure, and external-backend failure handling. These are not polish items; they are load-bearing gaps that would produce divergent, untestable implementations.

---

## Blocking Findings

### B-01 [critical] Detached execution has no process model or liveness contract

- Sections: 12.1, 12.2, 2.1
- Why it matters: The entire product thesis is "approve once, walk away, execution continues." Without a specified engine lifecycle, an implementer will use a backgrounded goroutine or shell process that dies on terminal close, making AT-TS-002 untestable and `attest resume` meaningless.
- Evidence from reviewers: SA critical (detached execution has no process model), AW critical (no survivable process contract), SC implicit (no run-level closure depends on engine liveness).
- Required spec change:
  1. Add a normative section "Run engine lifecycle" specifying: how the engine is spawned and detached from the launching terminal; where it writes its PID and heartbeat timestamp; how it logs; what happens on host reboot (engine is dead, run is resumable from durable state).
  2. Define a liveness artifact (e.g., `.attest/runs/<run-id>/engine.lock` with PID and last-heartbeat) that `attest status` and `attest resume` use to determine engine state.
  3. Define `attest resume` behavior for each run state (see B-06).

### B-02 [critical] File-based state has no concurrency safety or corruption recovery model

- Sections: 3.1, 3.2, 3.3, 3.4, 7.1
- Why it matters: All state is JSON files. Multiple workers and the engine read/write concurrently. Without a single-writer rule or atomic-write protocol, partial writes corrupt run state and make it unrecoverable. Claims require atomic check-and-write.
- Evidence from reviewers: SA critical (no concurrency safety), SC medium (single mutable tasks.json), AW implicit (resume depends on intact state).
- Required spec change:
  1. Designate the run engine as the sole writer of `tasks.json` and `run-status.json`. Workers write only to their own report directories.
  2. Specify atomic write-via-rename for all JSON mutations.
  3. Define a corruption detection and recovery procedure (e.g., checksum validation, last-known-good backup).
  4. Claims: the engine serializes all claim acquisitions; workers request claims via the engine, not by writing claim files directly.

### B-03 [critical] Run and task state machines lack transition rules, terminal states, and guards

- Sections: 5.1, 5.2, 5.3
- Why it matters: Both state machines list states but define no valid transitions, triggers, or guards. Every component will invent its own rules. The dispatcher, verifier, repair reconciler, and status renderer will disagree about what transitions are legal, creating silent correctness bugs.
- Evidence from reviewers: SA critical (no transition rules), SC medium (F-09, same finding), AW implicit (resume semantics depend on knowing which states are terminal vs resumable).
- Required spec change:
  1. Add explicit transition tables for both run and task state machines. Each row: source state, target state, triggering event, guard conditions.
  2. Mark terminal states (`completed`, `stopped`, `failed` for runs; `done`, `failed` for tasks).
  3. Mark resumable states and define what `attest resume` does for each.

### B-04 [critical] Verifier validates reporting, not reality

- Sections: 10.1, 3.5, 5.3
- Why it matters: Every verifier duty is an existence check on self-reported artifacts. A worker can produce a completion report claiming all tests pass while the tests actually fail or are stubs. The verifier passes because the report file exists. This defeats the entire verification pipeline.
- Evidence from reviewers: SC critical (F-02, deterministic verifier validates existence not content), SA high (verifier "deterministic" label misleading), AW high (reviewer input unbounded -- reviewers may only see self-reported summaries).
- Required spec change:
  1. The verifier must check independently observable evidence: test exit codes from command output, actual file changes cross-referenced against `git diff`, presence of expected output artifacts.
  2. Distinguish between "required evidence" (independently checkable) and "implementer summary" (informational only) in the completion report schema.
  3. The task compiler must emit a `required_evidence` field per task specifying what the verifier will independently check.
  4. Define the minimum input bundle that Codex and Gemini reviewers receive: it must include raw diffs, test output logs, and verifier results -- not just the implementer's self-reported summary.

### B-05 [critical] No run-level closure rule; requirement coverage can be orphaned

- Sections: 5.1, 5.3, 3.2, 3.3
- Why it matters: Section 5.3 defines task-level closure but there is no rule for when a run transitions to `completed`. All individual tasks can reach `done` while some requirement IDs from `run-artifact.json` are never assigned to any task. The system would declare success with orphaned requirements.
- Evidence from reviewers: SC critical (F-01, no run-level closure rule), SA high (run-level acceptance gate missing), AW implicit.
- Required spec change:
  1. Add section 5.4 "Run closure rule" requiring: every requirement ID in `run-artifact.json` is linked to at least one `done` task, or explicitly deferred with user approval recorded in a durable artifact.
  2. Add a `requirement-coverage.json` artifact (or equivalent) that maps every requirement ID to its covering task(s) and their status.
  3. Define the "intentional deferral" mechanism: who creates deferrals, what artifact records them, and that user approval is required in v1.

### B-06 [critical] External-backend failure and quorum policy are contradictory and undefined

- Sections: 5.3, 14.1, 18, 8.2, 8.3
- Why it matters: Section 5.3 requires all three review backends for every task closure. Section 14.1 mentions "quorum policy allows downgrade." Section 18 defers the decision. Meanwhile, a single expired API token or rate limit on Codex/Gemini blocks every task from closing indefinitely, with no defined retry budget, timeout, or operator escalation path.
- Evidence from reviewers: AW critical (no degraded-mode policy), SC high (F-08, quorum contradicts closure rule), SA high (heartbeat/failure timing undefined).
- Required spec change:
  1. Resolve the contradiction: either v1 is strict-unanimity (and the spec must define what happens when a backend is down -- block indefinitely, or require explicit human override) or v1 has a quorum policy (and the spec must define it).
  2. Add a preflight check at `attest launch` time that validates all required backends are reachable and authenticated.
  3. Define per-review retry budget and timeout before a task enters `blocked` state.
  4. Define the operator notification and override mechanism for persistent backend failure.

---

## Non-Blocking Findings

### N-01 [high] No git isolation model for concurrent workers

- Sections: 8.1, 6.2, 7.1
- Why it matters: Multiple workers cannot safely stage, commit, and push in the same worktree. Worker A's `git add .` will capture Worker B's half-written files.
- Evidence: SA critical, others did not surface.
- Synthesis decision: Downgraded from critical to high because the spec could be implemented with serial worker execution in v1 as a fallback. However, if the intent is concurrent workers, this becomes critical.
- Suggested spec change: Specify one git worktree per active worker. Define who creates and tears down worktrees. Define merge-back procedure and conflict handling. If concurrent workers are deferred, state that explicitly and enforce serial execution.

### N-02 [high] Repair loop has no depth bound or convergence definition

- Sections: 11.1, 11.2, 9.2, 18
- Why it matters: A subtle ambiguity can trigger an unbounded repair spiral. Each iteration costs full Opus + Codex + Gemini invocations. Without a ceiling, a single task can consume the entire run budget.
- Evidence: SA high, SC high (F-05), AW high.
- Suggested spec change: Define `max_repair_depth` per task lineage (e.g., 3). Define what happens when the ceiling is hit: task moves to `blocked`, operator notified. Define "does not converge" as reaching the depth ceiling.

### N-03 [high] Task dependencies absent from schema

- Sections: 3.3, 6.2
- Why it matters: No `depends_on` field means the dispatcher has no way to express ordering constraints. Tasks that depend on shared infrastructure or sequential setup cannot be correctly scheduled.
- Evidence: SA high. Others did not surface.
- Suggested spec change: Add `depends_on: [task_id, ...]` to the task schema. Define dispatcher rule: a task is not dispatchable until all dependencies are `done`.

### N-04 [high] `attest resume` and `attest stop` have no defined semantics

- Sections: 4.1, 5.1, 12.2
- Why it matters: Both commands appear in the CLI surface but have no specified behavior. An implementer must guess.
- Evidence: SA high (resume), SA medium (stop), AW high (resume), AW medium (stop).
- Suggested spec change: Define `resume` behavior per run state. Define `stop` as: signal engine to stop dispatching new tasks, allow in-flight workers to complete or timeout, transition run to `stopped`.

### N-05 [high] Dispatcher scheduling logic unspecified

- Sections: 2.1 (item 7)
- Why it matters: No specification of maximum concurrent workers, task selection order (priority? FIFO? dependency-ready?), or scheduling loop mechanism.
- Evidence: SA high. Others did not surface directly.
- Suggested spec change: Specify max concurrent workers (configurable, default 1 for v1 if git isolation is deferred). Specify task selection order. Specify scheduling loop trigger.

### N-06 [high] Council verdict has no defined schema; Opus synthesis authority is unconstrained

- Sections: 3.5, 5.3, 10.2, 10.3
- Why it matters: Free-text LLM output decides task closure. Ambiguous wording can be parsed as non-blocking. Opus can dismiss high-confidence Codex/Gemini blockers without evidence, undermining the multi-model disagreement value.
- Evidence: SC high (F-04, F-14), SA medium (council synthesis underspecified).
- Suggested spec change: Define enumerated verdict field: `{pass, fail, pass_with_findings}`. Require that Opus synthesis cannot dismiss a reviewer's blocking finding without citing specific counter-evidence. Record dismissal rationale in `council-result.json`.

### N-07 [high] Heartbeat parameters and lease mechanics undefined

- Sections: 7.1, 7.2
- Why it matters: No initial lease duration, heartbeat interval, or renewal mechanics. Failure detection timing is entirely unspecified.
- Evidence: SA high. Others did not surface directly.
- Suggested spec change: Specify default lease duration (e.g., 10 min), heartbeat interval (e.g., 60s), renewal rule (extends lease by duration from heartbeat time), and who checks expiration (engine polling loop).

### N-08 [high] Worker scope enforcement is honor-system only

- Sections: 3.4, 7.1
- Why it matters: Nothing prevents a worker from modifying files outside its claimed scope, undermining task isolation.
- Evidence: SA high. Others did not surface directly.
- Suggested spec change: The verifier must check that the worker's actual file changes (from `git diff`) fall within the claimed scope. Scope violations are blocking findings.

### N-09 [medium] No preflight validation at launch time

- Sections: 4.1, 8.1-8.3, 14.1
- Why it matters: `attest launch` can start a run that will immediately block because a required backend is not installed or authenticated.
- Evidence: AW medium. SC implicit in F-08 discussion.
- Suggested spec change: `attest launch` must validate: all required CLI backends are on PATH, authentication is valid, `.attest/` state is consistent.

### N-10 [medium] Run artifact immutability is not enforced

- Sections: 3.2, 5.3
- Why it matters: Post-approval edits to `run-artifact.json` silently change the contract.
- Evidence: SA medium, SC medium (F-12).
- Suggested spec change: Compute and store SHA-256 hash at approval time. Engine validates hash before dispatching.

### N-11 [medium] Multi-run isolation undefined

- Sections: 3.1
- Why it matters: Two active runs in the same repository share git state and filesystem.
- Evidence: SA high. Others did not surface.
- Suggested spec change: Enforce single active run per repository in v1. Return error if `attest launch` is called while another run is active.

### N-12 [medium] Worker prompt construction and communication protocol unspecified

- Sections: 8.1, 8.2, 8.3
- Why it matters: No specification of what the worker receives as input or how it communicates results back.
- Evidence: SA medium (two findings merged). SC high (F-07, reviewer inputs unspecified).
- Suggested spec change: Define the standard input bundle per worker role (implementer vs reviewer). Define the output contract (write to report directory, specific JSON schema).

### N-13 [medium] Repair task requirement linkage unspecified

- Sections: 11.2, 3.3
- Why it matters: When a repair task is created, it is unclear which requirement IDs it inherits and whether the parent task's coverage claim is invalidated.
- Evidence: SC medium (F-11).
- Suggested spec change: Repair tasks inherit `requirement_ids` from the parent. Parent task coverage claim is not invalidated unless the parent is reopened.

### N-14 [medium] Fresh-session run discovery requires knowing run-id

- Sections: 4.1
- Why it matters: `attest status` without arguments has no defined behavior for discovering active runs.
- Evidence: AW medium.
- Suggested spec change: `attest status` with no arguments lists all runs in the repository and their states.

### N-15 [medium] Task compiler determinism boundary is vague

- Sections: 6.1
- Why it matters: "Stable task IDs and grouping under the same compiler version" does not define what inputs are allowed to vary.
- Evidence: SC medium (F-10).
- Suggested spec change: Explicitly list the inputs to the compiler (run-artifact.json content + compiler version). State that no external state or randomness may influence output.

### N-16 [low] `run-status.json` update frequency undefined

- Evidence: SA low. Suggested spec change: Define update trigger (after every state transition).

### N-17 [low] No schema versioning strategy

- Evidence: SA low. Suggested spec change: Define migration rules when `schema_version` changes.

### N-18 [low] Routing traceability has no storage location

- Evidence: SA low, AW low. Suggested spec change: Store in per-task report directory.

### N-19 [low] No event log or audit stream

- Evidence: SA low, AW low. Suggested spec change: Defer to implementation planning; note as desirable.

### N-20 [low] Reviewer findings have no stable IDs

- Evidence: AW low. Suggested spec change: Assign finding IDs in review schemas for traceability across repair iterations.

---

## Disagreements

### D-01 Git isolation: critical vs deferrable

- Reviewer positions: SA rated this critical. SC and AW did not surface it.
- Synthesis decision: If the spec intends concurrent workers (which the dispatcher and claim system imply), this is critical. If v1 can enforce serial execution, it is deferrable. The spec must state which.
- Follow-up: Spec author must clarify concurrency intent and either add git isolation model or enforce serial execution.

### D-02 Verifier scope: existence checks vs content validation

- Reviewer positions: SC rated "existence-only verification" as critical. SA rated verifier misleading label as high. AW focused on reviewer input quality.
- Synthesis decision: Elevated to critical (B-04). The verifier must check at least some independently observable evidence, not just file existence. The exact boundary between deterministic and semantic checks must be defined.
- Follow-up: None. Included in blocking findings.

### D-03 Quorum policy: strict unanimity vs degraded mode

- Reviewer positions: SC found contradiction between 5.3, 14.1, and 18. AW rated degraded-mode absence as critical. SA noted heartbeat timing gaps.
- Synthesis decision: Elevated to critical (B-06). The contradiction must be resolved in the spec, not deferred to implementation.
- Follow-up: None. Included in blocking findings.

### D-04 Council review cost: risk-gate reviews vs full council on every task

- Reviewer positions: AW argued the per-task full-council loop is too expensive and should be risk-gated. The spec (1.4) explicitly chose full council as a v1 quality decision.
- Synthesis decision: The spec author made a deliberate design decision. AW's concern about cost is valid but is a product decision, not a spec defect. No spec change required. However, the repair depth bound (N-02) partially mitigates runaway cost.
- Follow-up: None required. Cost optimization is an explicit v2 item per section 1.4.

---

## Required Spec Edits Before Implementation

These edits correspond to the blocking findings (B-01 through B-06). The spec should not proceed to implementation planning until all six are addressed.

1. **Add section: "Run engine lifecycle"** (B-01) -- process model, spawn/detach mechanism, PID/liveness artifact, log destination, host-reboot behavior, `attest resume` detection logic.
2. **Add section: "State mutation model"** (B-02) -- single-writer rule, atomic write-via-rename, corruption detection, claim serialization through engine.
3. **Replace state lists with transition tables** (B-03) -- run and task state machines with source, target, trigger, guard, and terminal/resumable annotations.
4. **Strengthen verifier to check independent evidence** (B-04) -- test exit codes, git diff cross-reference, required-evidence field per task, minimum reviewer input bundle.
5. **Add section 5.4: "Run closure rule"** (B-05) -- requirement coverage completeness, deferral mechanism with user approval, requirement-coverage artifact.
6. **Resolve backend failure and quorum policy** (B-06) -- pick unanimity or quorum, define retry/timeout/block behavior, add preflight check, define operator override.

---

## Items Safe To Defer To Implementation Planning

These items are real but do not block spec correctness. They can be resolved during implementation design.

- Git worktree isolation model (N-01) -- but only if serial execution is enforced in v1
- Repair depth ceiling value (N-02) -- the spec must require a ceiling; the exact number can be tuned
- Heartbeat timing parameters (N-07) -- the spec must require heartbeats; exact durations can be configured
- Worker prompt construction details (N-12)
- Schema versioning strategy (N-17)
- Event log / audit stream design (N-19)
- CrewAI adoption decision (section 18)
- Exact CLI flag surface and config layout (section 18)
- Scope reservation granularity: path-based vs logical tags (section 18)
- Maximum retry counts before blocker state (section 18)
- `run-status.json` update frequency (N-16)
- Reviewer finding stable IDs (N-20)
