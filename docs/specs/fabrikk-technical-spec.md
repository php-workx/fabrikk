# fabrikk Technical Specification

Version: 0.3
Status: Draft (post-review revision)
Audience: Engineering
Document type: Technical specification / build specification
Companion document: [attest-functional-spec.md](./attest-functional-spec.md)

---

## 1. Technical context

### 1.1 Language and packaging

v1 is implemented in Go.

Reasons:

- single local binary is the simplest operational shape for a CLI-native tool
- strong support for subprocess management, JSON/YAML handling, file locking, and filesystem state
- low-friction distribution across macOS and Linux
- easy integration with existing CLI tools such as `claude`, `codex`, and `gemini`

Target platform support in v1:

- macOS
- Linux

Windows support is out of scope for v1.

### 1.2 Architecture choice

`attest` uses a split architecture:

1. **Supervisor session**
   - runs inside a normal agent CLI session
   - prepares runs, asks questions, explains status
2. **Local run engine**
   - stores durable state and executes autonomous background work
3. **Worker backends**
   - Claude CLI: primary implementer and launcher path
   - Codex CLI: independent reviewer backend
   - Gemini CLI: independent reviewer backend

### 1.3 Library selection policy

v1 should prefer existing, well-maintained libraries when they reduce implementation risk without breaking the CLI-native product shape.

Rules:

- use standard libraries or proven third-party libraries for subprocess management, file locking, schema validation, retries, and state-machine support where practical
- do not build bespoke orchestration infrastructure if a focused library can provide the same capability with less risk
- do not delegate the product's source-of-truth state model to an external framework unless that framework cleanly supports repo-local durability, reattachment, and explicit control by the `attest` CLI
- treat framework adoption as justified only when it preserves the core user experience: launch and inspect from normal agent CLI sessions

### 1.4 Scope decisions for v1

Codex and Gemini are not first-class implementers in v1. They are mandatory independent review participants.

Every task must pass the full multi-model council path before it can close:

1. deterministic verifier
2. Codex review
3. Gemini review
4. Claude Opus synthesis

This is a deliberate quality-first decision. Cost optimization in v1 comes from task sizing, wave discipline, and Claude-family routing, not from skipping independent review gates.

### 1.5 Concurrency model for v1

v1 uses **wave-based parallel execution**.

Meaning:

- the run engine may dispatch multiple implementation workers concurrently
- workers are grouped into a wave
- only tasks with no unmet dependencies and no conflicting scope may enter the same wave
- each task in a wave must be pre-assigned by the engine before worker launch

Reason:

- it preserves the Mayor-and-wave execution shape that users already rely on in AgentOps `/crank`
- it allows fresh worker contexts per task without requiring one global serial lane
- it keeps overlap risk bounded through task planning, scope checks, and conservative dispatch rules
- it still preserves quality because every task passes the full council

v1 does not allow unconstrained concurrent writers. Parallel execution is permitted only inside a validated non-overlapping wave. If a wave cannot be proven safe, the engine must dispatch those tasks serially.

### 1.6 Backend closure policy for v1

v1 uses **strict review completion**, not quorum downgrade.

Meaning:

- every task closure requires successful completion of deterministic verification, Codex review, Gemini review, and Opus synthesis
- if any required review backend is unavailable after preflight and retry policy, the task enters `blocked`
- there is no automatic degraded-review closure path in v1

## 2. Architecture

### 2.1 Runtime components

v1 consists of the following major components:

1. **CLI layer**
   - user-visible commands
2. **Spec ingester**
   - reads source spec files and extracts requirement structure
3. **Clarification manager**
   - records unresolved questions and approved answers
4. **Run artifact normalizer**
   - converts source specs and clarifications into the approved run contract
5. **Task compiler**
   - compiles the approved run artifact into an initial task graph
6. **Run engine**
   - the single writer for run state and the dispatcher for worker lifecycle
7. **Deterministic verifier**
   - validates independently checkable evidence
8. **Councilflow**
   - runs bounded reviewer and synthesis workflows for one task attempt
9. **Repair reconciler**
   - reopens tasks or spawns child repair tasks from findings
10. **Status renderer**
   - summarizes run state for later sessions

### 2.2 Councilflow boundary

v1 includes a Go-native internal orchestration package:

- `internal/councilflow`

`internal/councilflow` owns:

- parallel reviewer fan-out for one task attempt
- reviewer retry and timeout handling inside that attempt
- Opus synthesis sequencing after reviewer completion
- typed council result assembly
- council-related event emission

`internal/councilflow` does not own:

- canonical run state
- task state transitions
- claims or leases
- wave planning
- detached engine lifecycle
- final closure policy

Those remain owned by the run engine and canonical `.attest/` artifacts.

### 2.3 Conceptual data flow

```text
source specs
  -> spec ingestion
  -> clarification
  -> run-artifact.json (draft)
  -> preparation council review
  -> user approval
  -> run-artifact.json (approved)
  -> initial tasks.json
  -> run engine
  -> completion-report.json
  -> verifier-result.json
  -> councilflow
     -> codex-review.json + gemini-review.json
     -> Opus synthesis / council-result.json
  -> reopen or repair task if needed
  -> requirement-coverage.json
  -> run-status.json
```

### 2.4 Run engine lifecycle

`fabrikk launch` starts a detached run engine process for one approved run.

The engine lifecycle is:

1. validate the run is approved and launchable
2. validate no other active engine exists for the same repository
3. spawn a detached engine process
4. write an engine liveness artifact
5. transition run from `launching` to `running` only after the engine heartbeat is established

The engine must write:

- `.attest/runs/<run-id>/engine.json`
- `.attest/runs/<run-id>/engine.log`
- `.attest/runs/<run-id>/events.jsonl`

Required fields:

- `run_id`
- `pid`
- `started_at`
- `heartbeat_at`
- `version`
- `state`

Heartbeat behavior:

- engine heartbeat interval: 30 seconds
- if `heartbeat_at` is older than 2 minutes, the engine is considered stale

v1 process model:

- the run engine is a separate long-lived OS process spawned by `fabrikk launch`
- it must detach from the launching terminal and continue after the parent session exits
- it must not rely on transcript memory, shell job control, or a foreground goroutine for survival
- it is the only process allowed to dispatch tasks or mutate canonical run state for that run

Host restart behavior:

- a host reboot kills the engine
- the run remains durable on disk
- the next `fabrikk resume <run-id>` must detect the dead engine, expire any stale claim, and restart the engine if the run state is resumable

### 2.5 Reattachment model

Reattachment reads repo-local state only. It must not require transcript history or in-memory process state.

`fabrikk resume <run-id>` rules:

- if the engine is alive, reattach as observer and renderer only
- if the engine is stale and the run state is resumable, expire stale claim state and restart the engine
- if the run is `blocked`, inspect blocker records before restart and do exactly one of:
  - restart automatically when the blocker was transient and no human action is required
  - return a structured blocked summary when user input, re-authentication, or artifact correction is required
- if the run is terminal, return status only and do not restart

## 3. Canonical artifacts and schemas

### 3.1 Directory layout

```text
attest/
  docs/
    specs/
    plans/
  .attest/
    runs/
      <run-id>/
        engine.json
        engine.log
        events.jsonl
        run-artifact.json
        technical-spec.md
        technical-spec-review.json
        technical-spec-approval.json
        execution-plan.json
        execution-plan.md
        execution-plan-review.json
        execution-plan-approval.json
        clarifications.json
        tasks.json
        requirement-coverage.json
        run-status.json
        claims/
          <task-id>.json
        reports/
          <task-id>/
            attempt.json
            completion-report.json
            verifier-result.json
            codex-review.json
            gemini-review.json
            council-result.json
            logs/
              <command-id>.log
```

Planning artifacts are optional during the transitional compiler phase, but once the spec-planning pipeline is enabled they become the canonical bridge between the approved run artifact and task compilation.

### 3.2 `run-artifact.json`

The run artifact is the approved normalized contract for a run.

Required fields:

- `schema_version`
- `run_id`
- `source_specs`
- `requirements`
- `assumptions`
- `clarifications`
- `dependencies`
- `risk_profile`
- `routing_policy`
- `approved_at`
- `approved_by`
- `artifact_hash`

- `quality_gate`

The artifact hash is computed at approval time. The engine must validate the hash before dispatching any work.

The `quality_gate` field specifies the project-level quality checks that must pass before any task is accepted. See section 11.2.

### 3.3 `clarifications.json`

Required fields:

- `question_id`
- `text`
- `status`
- `answer`
- `answered_by`
- `answered_at`
- `affected_requirement_ids`

This file is the canonical record for unresolved and resolved clarifications.

### 3.4 `tasks.json`

Required task fields:

- `task_id`
- `slug`
- `title`
- `task_type`
- `tags`
- `created_at`
- `updated_at`
- `order`
- `etag`
- `lineage_id`
- `requirement_ids`
- `depends_on`
- `scope`
- `priority`
- `risk_level`
- `default_model`
- `status`
- `status_reason`
- `required_evidence`
- `parent_task_id`
- `created_from`

`scope` is structured in v1 and must contain:

- `owned_paths`
- `read_only_paths`
- `shared_paths`
- `isolation_mode`

`isolation_mode` valid values:

- `direct`
- `patch_handoff`

Field rules:

- `slug` is a short human-readable identifier derived from the title for logs and task views
- `task_type` is a planner classification such as `implementation`, `repair`, `review_followup`, or `clarification_followup`
- `tags` are normalized lowercase labels for operator and agent filtering
- `created_at` and `updated_at` record canonical task lifecycle timestamps
- `order` is a stable presentation hint within the same priority band and parent lineage
- `etag` is the task content hash used for internal compare-and-swap updates and agent-facing read consistency
- `lineage_id` is stable across a root task and any repair children created from the same completion lineage

Relationship rules:

- `depends_on` is the canonical hard dependency edge used for dispatchability and `fabrikk ready`
- runtime blockers such as reviewer findings, backend outages, or human-input requirements live in `run-status.json`, not `tasks.json`
- `parent_task_id` groups repair or follow-up tasks under their originating task when a lineage is split
- canonical task ownership is expressed through claims, not through an `assignee` field on the task

### 3.5 `requirement-coverage.json`

Required fields:

- `requirement_id`
- `status`
- `covering_task_ids`
- `deferred`
- `deferred_reason`
- `deferred_by`
- `deferred_at`

Valid status values:

- `unassigned`
- `in_progress`
- `satisfied`
- `deferred`
- `blocked`

### 3.6 `claims/<task-id>.json`

Required claim fields:

- `task_id`
- `wave_id`
- `owner_id`
- `backend`
- `claimed_at`
- `lease_expires_at`
- `heartbeat_at`
- `scope_reservations`

Claim rules:

- the engine is the only component that creates, renews, or expires claim files
- workers do not mutate claim files directly
- claim files are written atomically
- `scope_reservations` must cover every `owned_path` for the task

### 3.7 Task report files

`completion-report.json`:

- `task_id`
- `attempt_id`
- `changed_files`
- `command_results`
- `artifacts_produced`
- `known_gaps`
- `implementer_summary`

`attempt.json`:

- `task_id`
- `attempt_id`
- `wave_id`
- `selected_backend`
- `selected_model_tier`
- `backend_cli_version`
- `backend_model_version`
- `prompt_template_id`
- `input_bundle_hash`
- `worker_input_contract`
- `started_at`
- `finished_at`
- `exit_status`

Each `command_results` entry must include:

- `command_id`
- `command`
- `cwd`
- `exit_code`
- `duration_ms`
- `required`
- `log_path`
- `log_hash`

`verifier-result.json`:

- `task_id`
- `attempt_id`
- `evidence_checks`
- `scope_check`
- `requirement_check`
- `pass`
- `blocking_findings`

`codex-review.json` and `gemini-review.json`:

- `task_id`
- `attempt_id`
- `verdict`
- `confidence`
- `blocking_findings`
- `non_blocking_findings`
- `finding_ids`

`council-result.json`:

- `task_id`
- `attempt_id`
- `verdict`
- `synthesized_findings`
- `dismissal_rationale`
- `follow_up_action`

Allowed council verdicts:

- `pass`
- `pass_with_findings`
- `fail`

Allowed `follow_up_action` values:

- `none`
- `reopen`
- `create_child_repair`
- `block`
- `escalate`

Every finding recorded in verifier or reviewer outputs must use a normalized finding object with:

- `finding_id`
- `severity`
- `category`
- `summary`
- `evidence_ref`
- `dedupe_key`

`dedupe_key` must remain stable when the same underlying defect is restated across retries or reviewer rounds.

## 4. Interfaces

### 4.1 Single-writer rule

The run engine is the sole writer for:

- `tasks.json`
- `run-status.json`
- `requirement-coverage.json`
- `claims/*.json`
- `events.jsonl`

Workers may write only to:

- their own report directory
- their own command log files

`run-status.json` is an authoritative derived view updated by the engine on every state transition and every material heartbeat update.

### 4.2 Atomic writes

All JSON mutations must use:

1. write to temp file
2. fsync
3. atomic rename into place

### 4.3 Corruption handling

If a canonical JSON artifact fails to parse:

- the run moves to `failed`
- `run-status.json` must record `state_corruption`
- `fabrikk explain` must surface the corrupted file path

v1 does not attempt automatic repair of corrupted canonical artifacts.

### 4.4 Repository isolation

Only one active run may exist per repository in v1.

If another run is active, `fabrikk launch` must fail with a clear operator error.

Inside a run, multiple active workers are allowed only when:

- they belong to the same dispatch wave
- their `owned_paths` are disjoint
- no task in the wave depends on another task in the same wave

### 4.5 Schema compatibility

The binary must refuse to operate on any run artifact or state file whose `schema_version` is newer than it supports.

For older supported schema versions, the binary may:

- handle them directly
- migrate them before execution begins

If migration is required, it must happen before dispatching any worker.

### CLI surface

### 5.1 Core commands

v1 command surface:

- `fabrikk prepare --spec <path> [--spec <path>...] [--normalize auto|always|never] [--allow-llm-normalization]`
- `fabrikk review <run-id>`
- `fabrikk artifact approve <run-id> [--accept-needs-revision]`
- `fabrikk tech-spec draft|review|approve <run-id>`
- `fabrikk plan draft|review|approve <run-id>`
- `fabrikk approve <run-id>`
- `fabrikk tasks <run-id>`
- `fabrikk ready <run-id>`
- `fabrikk blocked <run-id>`
- `fabrikk next <run-id>`
- `fabrikk progress <run-id>`
- `fabrikk status [<run-id>]`
- `fabrikk report <run-id> <task-id> --from <path>`
- `fabrikk retry <run-id> <task-id>`
- `fabrikk verify <run-id> <task-id>`
- `fabrikk learn ...`
- `fabrikk context <run-id> <task-id>`

### 5.2 Command semantics

`fabrikk review <run-id>`

- renders a human-readable approval summary for a prepared run
- does not run model review

The approval summary must include:

- source spec fingerprint
- requirement count and requirement IDs
- assumptions introduced during normalization
- unresolved and resolved clarifications
- dependency highlights
- risk classification
- proposed task breakdown preview

`fabrikk artifact approve <run-id> [--accept-needs-revision]`

- records explicit approval for an LLM-normalized run artifact
- refuses stale source specs, changed prompt artifacts, changed normalized candidates, and failing reviews
- allows `needs_revision` only when the operator passes `--accept-needs-revision`

`fabrikk approve <run-id>`

- records final approval after technical spec and execution plan gates are satisfied
- compiles approved planning artifacts into dispatchable task state

`fabrikk status`

- with no `run-id`, list all runs in the repository
- with `run-id`, render the current run summary

`fabrikk tasks <run-id>`

- renders the task read model for one run
- must support both human-readable and `--json` output
- is the primary low-token agent query surface in v1 instead of GraphQL
- must support filter flags for at least:
  - `--status`
  - `--task-type`
  - `--priority`
  - `--tag`
  - `--requirement-id`
  - `--wave`
  - `--limit`
  - `--ready`
  - `--blocked`

`fabrikk ready <run-id>`

- renders only dispatchable tasks
- is a convenience wrapper over `fabrikk tasks --ready`
- must support `--json`
- orders tasks by priority, then `order`, then stable `task_id`

`fabrikk blocked <run-id>`

- renders only non-dispatchable tasks with unresolved blockers
- is a convenience wrapper over `fabrikk tasks --blocked`
- must group blockers by reason in human-readable mode
- must support `--json`

`fabrikk next <run-id>`

- returns the single highest-priority dispatchable task
- if no task is ready, it must point the operator to the highest-impact blocker instead of returning an empty success
- must support `--json`

`fabrikk progress <run-id>`

- renders counts by task state and requirement coverage state
- must include a completion percentage derived from task closure and requirement coverage
- must support `--json`

`fabrikk explain <run-id>`

- explains why the run is not finished
- includes current gate, blocker, and next action

`fabrikk resume <run-id>`

- observer if engine is alive
- stale-engine recovery if engine is dead and run is resumable
- if the run is `blocked`, surface blocker details and indicate whether automatic restart is possible or human input is required

`fabrikk stop <run-id>`

- tells the engine to stop dispatching new work
- allows in-flight workers to finish within the current lease window by default
- expires remaining active claims after the grace window ends
- transitions the run to `stopped`

### 5.3 Read-model rules

Task and status read commands are part of the product contract, not presentation-only helpers.

Rules:

- every read command that can be used by an agent must support `--json`
- JSON output must use stable field names and include task `etag` values when tasks are returned
- human-readable commands may group, sort, or summarize data, but the JSON mode must stay structurally predictable
- `fabrikk ready`, `fabrikk blocked`, `fabrikk next`, and `fabrikk progress` are convenience projections over canonical run state and must not introduce separate hidden state
- v1 intentionally prefers filtered CLI read models over a general GraphQL layer

### Run lifecycle

### 6.1 Run states

Valid run states:

- `preparing`
- `awaiting_clarification`
- `awaiting_approval`
- `approved`
- `launching`
- `running`
- `blocked`
- `failed`
- `completed`
- `stopped`

### 6.2 Run transition table

| Source | Target | Trigger | Guard |
|---|---|---|---|
| `preparing` | `awaiting_clarification` | unresolved clarification found | at least one open clarification |
| `preparing` | `awaiting_approval` | run artifact created | no open clarifications |
| `awaiting_clarification` | `awaiting_approval` | all clarifications resolved | all required answers present |
| `awaiting_approval` | `approved` | explicit user approval | artifact hash recorded |
| `approved` | `launching` | `fabrikk launch` | no other active run in repo |
| `launching` | `running` | engine heartbeat established | `engine.json` written |
| `running` | `blocked` | blocker condition reached | retry policy exhausted or user input needed |
| `running` | `completed` | run closure rule passes | section 6.6 satisfied |
| `running` | `failed` | unrecoverable engine or state error | explicit failure reason recorded |
| `running` | `stopped` | `fabrikk stop` | engine stops dispatching |
| `blocked` | `running` | blocker resolved and engine alive/restarted | run is resumable |
| `stopped` | `launching` | `fabrikk resume` | run is resumable |

Terminal run states:

- `completed`
- `failed`

### 6.3 Task states

Valid task states:

- `pending`
- `claimed`
- `implementing`
- `verifying`
- `under_review`
- `repair_pending`
- `blocked`
- `done`
- `failed`

### 6.4 Task transition table

| Source | Target | Trigger | Guard |
|---|---|---|---|
| `pending` | `claimed` | engine grants claim | dependencies are done and wave-safety rules pass |
| `claimed` | `implementing` | worker starts | claim active |
| `implementing` | `verifying` | completion report written | required report exists |
| `verifying` | `under_review` | verifier passes | section 11.3 satisfied |
| `verifying` | `repair_pending` | verifier fails | blocking verifier finding |
| `under_review` | `done` | council passes | no blocking findings remain and `follow_up_action = none` |
| `under_review` | `repair_pending` | council fails | any blocking review finding |
| `repair_pending` | `pending` | engine schedules next attempt | repair depth within limit |
| `repair_pending` | `blocked` | repair ceiling exceeded | max repair depth hit |
| `claimed` | `blocked` | claim expires unrecovered | engine cannot safely continue |
| `implementing` | `failed` | unrecoverable worker protocol error | invalid report contract |
| `done` | `verifying` | cross-task regression detected | later task's changes overlap this task's `read_only_paths` or `shared_paths` |

Terminal task states:

- `done` (may revert to `verifying` if cross-task regression is detected per section 11.6)
- `failed`

### 6.5 Task closure rule

A task can transition to `done` only when:

- deterministic verifier passes
- Codex review is non-blocking
- Gemini review is non-blocking
- Opus synthesis returns `pass` or `pass_with_findings`
- all linked requirement IDs for the task are satisfied by evidence or explicitly deferred upstream

There is no reduced-review closure path in v1. Every task, including repair tasks, must pass the same full council sequence.

### 6.6 Run closure rule

A run can transition to `completed` only when:

- every requirement in `run-artifact.json` is `satisfied` or `deferred` in `requirement-coverage.json`
- all tasks are terminal
- final regression/integration verification passes
- final run-level council summary is non-blocking

Deferrals:

- require explicit user approval via `fabrikk defer <run-id> <requirement-id> --reason <text>`
- must be recorded in `requirement-coverage.json`
- must include a reason, timestamp, and `deferred_by` set to the approving user
- the engine, workers, Opus synthesis, and all automated components are prohibited from creating or approving deferrals
- any deferral without a user-initiated approval record is treated as `unassigned` by the run closure check

### Task compilation

### 7.1 Determinism boundary

Task compilation must be deterministic from:

- `run-artifact.json`
- compiler version

No external state or randomness may affect output.

In v1, post-approval task compilation is rule-based and non-LLM. LLM assistance is allowed only before approval, during spec ingestion and normalization.

### 7.2 Compilation-time coverage check

After task compilation, the engine must compute `requirement-coverage.json` and verify that every requirement ID in the approved run artifact is assigned to at least one task.

If any requirement has status `unassigned` after compilation:

- the run must not proceed to dispatching
- the engine must report the unassigned requirement IDs
- the run enters `blocked` until the task graph is corrected or the user defers the requirement

This check must also run again before run closure (section 6.6).

### 7.3 Task sizing rules

Task compiler rules:

- prefer one subsystem or behavior slice per task
- keep tasks small enough for bounded context execution
- assign explicit `owned_paths` so same-wave overlap can be checked mechanically
- place tasks in the same wave only when `owned_paths` are disjoint and dependencies do not intersect
- move tasks with uncertain or overlapping scope into different waves unless `patch_handoff` is required explicitly
- create child repair tasks when a finding is narrower than the parent
- reopen the parent task when the finding invalidates the parent completion claim

### 7.4 Dependencies

Tasks must include `depends_on`.

A task is dispatchable only when every task in `depends_on` is `done`.

If a dependency enters `blocked` or `failed`, dependent tasks remain `blocked`.

### 7.5 Wave planning

The run engine dispatches work in waves.

A wave is the maximal set of dispatchable tasks that satisfy all of:

- every dependency is `done`
- every task has disjoint `owned_paths`
- no task in the wave is marked high-risk for serialized execution
- required backends are healthy enough to start the tasks

The engine must prefer parallel wave execution by default.

The engine must fall back to serial dispatch for tasks when:

- scope overlap cannot be ruled out confidently
- the task touches globally sensitive files such as schema, migration, or shared configuration roots
- a prior attempt in the same lineage ended in scope violation

### 7.6 Scheduler rules

The dispatcher must use:

- a configurable maximum concurrent worker count
- task selection ordered by priority, then stable task ID
- an engine scheduling loop that reevaluates dispatchable tasks on a fixed interval

Default v1 values:

- `max_concurrent_workers = 4`
- scheduler interval = 15 seconds

When a required backend is rate-limited or unhealthy, the engine must reduce dispatch or block the affected tasks rather than flooding retries.

### Claim and lease semantics

### 8.1 Claim acquisition

Only one active claim may exist per task.

Claim acquisition fails when:

- a non-expired claim already exists
- the task is not in a claimable state
- dependencies are incomplete
- any active claim already reserves one of the task's `owned_paths`

### 8.2 Lease mechanics

Defaults:

- lease duration: 15 minutes
- heartbeat interval: 60 seconds
- engine expiry check interval: 30 seconds

The engine renews the lease when valid worker heartbeat is observed.

### 8.3 Scope enforcement

Scope reservations are path-based in v1.

The verifier must compare actual changed files against the claimed scope.

If files outside the claim scope are changed:

- verifier fails
- task moves to `repair_pending` or `blocked` based on severity

Backend execution policy:

- prefer backend-native isolated workers when available
- `direct` isolation mode is allowed only when the backend provides an isolated workspace/session boundary for that worker
- concurrent direct writes into one shared working tree are not allowed
- otherwise use `patch_handoff`, where the worker produces a patch artifact and the engine applies it only after scope validation
- worktrees are optional, not required, in v1

### 8.4 Failure policy

Workers fail closed:

- timeout -> claim expires and task moves to `repair_pending` or `blocked`
- malformed output -> verification failure
- missing report -> verification failure

### 8.5 Worker lifecycle protocol

For every worker attempt, the engine must:

1. create the claim and attempt record
2. spawn the worker subprocess
3. monitor subprocess liveness until exit or timeout
4. update claim heartbeat only from engine-observed worker liveness
5. wait for subprocess exit before reading report artifacts

Worker input contracts:

- implementer input: task scope, linked requirement text, task-specific file manifest, relevant run-artifact excerpt, prior blocking findings for the same task lineage
- reviewer input: linked requirement subset, diff or snapshot delta, completion report, verifier result, prior blocking findings for the same task lineage

### Execution backends

### 9.1 Claude backend

Claude CLI is the primary implementer lane in v1.

Claude-native teams or subagents are the preferred worker mechanism when the runtime supports them.

Supported Claude roles:

- Opus for planning, synthesis, and high-risk review
- Sonnet for implementation and repair
- Haiku for scouting and lightweight extraction

Dispatch policy:

- spawn fresh workers per task, not one long-lived worker context for the whole run
- the supervisor or run engine acts as the Mayor and pre-assigns work before spawning
- workers do not self-claim tasks

### 9.2 Codex backend

Codex CLI is the primary independent code and test reviewer backend in v1.

Codex review is mandatory for every task closure attempt.

### 9.3 Gemini backend

Gemini CLI is the primary independent spec and edge-case reviewer backend in v1.

Gemini review is mandatory for every task closure attempt.

### 9.4 CrewAI decision

CrewAI is not used in v1.

v1 uses direct Go-native orchestration in `internal/councilflow` for reviewer fan-out, synthesis sequencing, and council result assembly. This decision was finalized during Phase 1 implementation.

Reasons:

- Go-native orchestration preserves full control over canonical `.attest/` run state, closure rules, and engine lifecycle
- the councilflow package is small enough that a framework adds dependency risk without reducing implementation complexity
- subprocess management for Claude, Codex, and Gemini backends is straightforward in Go and does not benefit from CrewAI's agent abstractions

### 9.5 Roam structural intelligence layer

Roam (`roam-code`) is a structural intelligence engine that pre-indexes codebases into a semantic graph (SQLite). It provides dependency-aware context queries, blast-radius analysis, call-graph traversal, and anti-pattern detection via CLI or MCP server.

Roam is an optional enhancement in v1. When present, it provides semantic analysis that improves several pipeline stages beyond what path-based heuristics can achieve. When absent, the engine falls back to path-based scope enforcement and file-list-based worker context.

v1 integration points:

- **Task compilation**: query the Roam graph to derive `owned_paths`, `read_only_paths`, and `shared_paths` from actual code dependencies instead of heuristic path assignment
- **Worker input bundles**: use Roam context queries to produce bounded, structural context (definition + callers + callees + affected tests) instead of raw file dumps, respecting the context budget requirement in section 11.4
- **Wave planning**: check semantic disjointness through subgraph independence rather than path-prefix overlap, enabling more aggressive parallel dispatch when code is decoupled
- **Verification**: detect structural regressions where a changed function's callers or dependents were not updated, complementing the path-based scope check in section 11.3
- **Cross-task regression detection**: upgrade the path-based check in section 11.6 to semantic dependency overlap when the graph is available
- **Quality evidence**: use Roam's anti-pattern detection (algorithmic complexity, code smells) and health scoring as additional verifier evidence checks, catching structural quality issues before council review

v1 policy:

- detect Roam availability during `fabrikk prepare` or `fabrikk launch` preflight, similar to quality gate detection
- if Roam is available, run `roam index` during preflight and store the graph path in the run artifact
- if Roam is not available, warn the operator and proceed with path-based fallback for all affected pipeline stages
- Roam must not own or mutate canonical `.attest/` run state
- Roam queries must be treated as advisory input to the engine's own decisions, not as authoritative overrides of the spec's scope and closure rules

### Model routing policy

### 10.1 Default routing table

| Task class | Default lane |
|---|---|
| spec analysis | Claude Opus |
| clarification synthesis | Claude Opus |
| repo scouting | Claude Haiku |
| file manifest discovery | Claude Haiku |
| implementation | Claude Sonnet |
| repair task | Claude Sonnet |
| deterministic verification | non-LLM |
| review synthesis | Claude Opus |
| independent code review | Codex |
| independent spec review | Gemini |

### 10.2 Escalation rules

Escalation rules:

- Haiku -> Sonnet when discovery is ambiguous or conflicting
- Sonnet -> Opus when the same task fails verifier twice
- Sonnet -> Opus when repair loops do not converge
- Sonnet -> Opus when the task affects architecture or multi-task coordination

### 10.3 Routing traceability

Every task attempt must record:

- selected backend
- selected model tier
- backend model version (the actual model version string returned by the backend, if available)
- escalation cause
- retry count
- backend CLI version
- prompt/template ID
- input bundle hash

Routing traceability is stored in `reports/<task-id>/attempt.json`.

## 5. Verification

AT-FR-023 requires council checkpoints at least for: spec review, task review, task verification, and final run acceptance. This section specifies all four.

### 11.1 Preparation council (spec review checkpoint)

After the run artifact is drafted but before it is presented for user approval, the system must run a preparation council review.

The preparation council:

- sends the draft run artifact, source specs, and extracted clarifications to Codex and Gemini for independent review
- Codex reviews for requirement completeness, internal consistency, and task-decomposition feasibility
- Gemini reviews for spec ambiguities, missing edge cases, and untestable requirements
- Opus synthesizes the preparation findings

The preparation council result must be:

- stored as `.attest/runs/<run-id>/reports/preparation-council.json`
- presented alongside the run artifact when the user runs `fabrikk review <run-id>`

If the preparation council produces blocking findings, `fabrikk review` must surface them prominently. The user may still approve the artifact, but the blocking findings become part of the approval record.

### 11.2 Project quality gate

Every project using `attest` must declare a quality gate: a single command that runs the project's own language-specific checks.

The quality gate is specified in the run artifact's `quality_gate` field:

- `command`: the shell command to run (e.g., `just check`, `make dev`, `./scripts/ci.sh`)
- `timeout_seconds`: maximum allowed execution time
- `required`: whether failure is blocking (default: `true`)

The quality gate command is expected to cover the project's standard development checks, such as:

- formatting (e.g., `rustfmt`, `gofmt`, `black`)
- linting (e.g., `clippy`, `golangci-lint`, `ruff`)
- type checking (e.g., `cargo check`, `go vet`, `mypy`)
- unit tests (e.g., `cargo test`, `go test ./...`, `pytest`)
- any project-specific checks the developer normally runs before committing

`attest` does not mandate which tools a project uses. It requires that the project expose one entry point that runs all of them. This is the project's own contract for "my code is clean."

During preparation, `fabrikk prepare` must detect a quality gate if one is present:

- look for `Justfile` (target `check` or `dev`), `Makefile` (target `check` or `dev`), or `.attest/quality-gate.sh`
- if found, propose the detected command in the draft run artifact
- if not found, warn the user that no quality gate was detected and require explicit confirmation to proceed without one

### 11.3 Deterministic verifier

The deterministic verifier runs before model review.

Verifier duties:

- run the project quality gate command and require exit code 0
- validate required reports exist
- validate required commands were executed by checking raw command logs
- validate exit codes and required artifact presence
- validate actual changed files against task scope
- validate task links to requirement IDs
- validate `required_evidence` declared by the task compiler

The quality gate runs first. If formatting, linting, type checking, or tests fail, the task fails verification immediately — no model review tokens are spent on code that does not pass the project's own checks.

The verifier must not trust implementer summary text as proof.

The deterministic verifier is limited to structural and syntactic checks. Semantic correctness and cross-artifact interpretation are handled by model review and run-level integration gates.

### 11.4 Reviewer input bundle

Codex and Gemini must receive a bounded review bundle containing:

- linked requirement subset
- changed files
- actual diff or snapshot delta
- completion report
- verifier result
- prior blocking findings for this task only

The review bundle must also include:

- a stable input bundle hash
- an explicit context budget

If the review bundle would exceed the configured context budget, the system must shrink the bundle or escalate the review model tier. It must not silently send unbounded context.

The review bundle is constructed by `attest` and executed by `internal/councilflow`. `internal/councilflow` must not mutate the bundle after dispatch except for adding runtime metadata such as attempt timing, backend version, and terminal execution status.

### 11.5 Reviewer order

Per task review order:

1. deterministic verifier
2. Codex review
3. Gemini review
4. Opus synthesis

This sequence is universal for v1 task closure.

Within one task attempt, Codex and Gemini review lanes may run in parallel under `internal/councilflow`. Opus synthesis starts only after both reviewer lanes reach terminal outcomes for that attempt.

### 11.6 Cross-task regression detection

When a task completes and its changes are merged, the engine must check whether the changed files overlap with the `read_only_paths` or `shared_paths` of any previously completed task in the same run.

If overlap is detected:

- the engine must flag the affected earlier task for re-verification
- the earlier task transitions from `done` to `verifying`
- the earlier task runs through the full verification and council sequence again
- if re-verification fails, the earlier task enters `repair_pending`

This check is file-path-based in v1 and does not require semantic analysis.

### 11.7 Run-level integration review

Before a run can transition to `completed`, the engine must perform a run-level integration review covering:

- cross-task interface compatibility: the run-level reviewer receives the combined diffs and requirement coverage map and checks for interface mismatches between tasks
- regression verification: the engine runs the project's full test suite (if defined in `required_evidence` at the run level) and records pass/fail
- remaining blocked or deferred requirement impact

The run-level council summary in section 6.6 consumes this review.

### 11.8 Blocking policy

Blocking findings include:

- uncovered linked requirement
- missing evidence
- scope violation
- contradictory code/spec interpretation
- major edge-case omission
- high-confidence reviewer objection
- reviewer absence after retry budget exhaustion

Opus synthesis may merge or summarize findings, but it may not dismiss a high-confidence blocking finding from Codex or Gemini without explicit counter-evidence recorded in `dismissal_rationale`.

### Repair reconciliation

### 12.1 Repair triggers

Repair work is auto-created by default when:

- verifier fails
- Codex returns blocking findings
- Gemini returns blocking findings
- Opus synthesis returns `fail`

Before creating repair work, the reconciler must compare new findings against prior findings for the same task lineage by `dedupe_key`.

If a blocking finding is only a restatement of an already-open finding, the system must attach it to the existing repair lineage instead of creating duplicate repair work.

### 12.2 Reopen vs child repair task

Reopen the parent task when:

- the original completion claim is substantially invalid
- the gap is within the task's main scope

Create a child repair task when:

- the issue is narrow and isolated
- the parent task remains mostly valid

Child repair task requirement linkage:

- the child must carry only the specific requirement IDs identified as uncovered or inadequately covered in the triggering finding
- the repair reconciler must extract the affected requirement IDs from the finding and set them as the child's `requirement_ids`
- the child must not inherit the parent's full `requirement_ids` list
- the verifier checks the child's linked requirements against the child's evidence, ensuring the specific gap is addressed

### 12.3 Repair depth limit

Each task lineage has `max_repair_depth = 3` in v1.

When the ceiling is reached:

- the root task moves to `blocked`
- the run records `non_convergent_repair_loop`
- user attention is required

### 12.4 Execution-time clarifications

Clarifications may also be raised during execution by:

- the verifier
- Codex review
- Gemini review
- Opus synthesis

Execution-time clarifications must be appended to `clarifications.json` with a status of `pending`.

If an execution-time clarification blocks safe progress:

- the affected task moves to `blocked`
- the run moves to `blocked` when no other dispatchable work remains
- `fabrikk explain` must surface the exact clarification question and affected requirement IDs

### Observability

### 13.1 Required status fields

`run-status.json` must include:

- current run state
- active wave ID
- active task states
- current gate
- active backend/model
- task counts by state
- active claims
- uncovered requirement count
- open blockers
- retry count
- next automatic action
- last transition time
- last successful council timestamp

For every active or blocked task, `run-status.json` must also include a task-detail record with:

- `task_id`
- `current_gate`
- `active_backend`
- `active_model`
- `claim_age_seconds`
- `heartbeat_age_seconds`
- `retry_count`
- `next_automatic_action`
- `blocking_finding_id`
- `blocking_finding_summary`
- `human_input_required`

### 13.2 Explainability requirements

`fabrikk explain <run-id>` must answer:

- why the run is not finished
- whether the engine is alive, stale, or stopped
- which tasks are active or blocked in the current wave
- which requirements remain uncovered
- which reviewer blocked progress
- what the next action is

For each blocked or active task, `fabrikk explain` must render:

- the current gate
- active backend and model
- claim age and last heartbeat age
- retry count
- blocking finding reference
- whether human action is required

### 13.3 Event log

`events.jsonl` is the append-only run event stream for debugging and audit.

The engine must append an event for:

- run state transitions
- task state transitions
- claim acquisition, renewal, expiry, and release
- worker dispatch and worker exit
- verifier completion
- reviewer completion
- clarification creation and resolution

## 6. Requirement traceability

This section records how the requirement families in the functional spec map to the mechanisms defined in this technical specification. The mapping is intentionally high-signal rather than exhaustive line-by-line duplication.

- `AT-FR-*` requirements map primarily to the canonical artifact model in section 3, the interface and state-management rules in section 4, and the verification/repair pipeline in section 5.
- `AT-AS-*` requirements map primarily to the CLI surface, review and approval flow, run lifecycle, explainability, and status rendering rules in section 4.
- `AT-TS-*` requirements map primarily to deterministic task compilation, lifecycle/state-machine behavior, verification gates, observability, test strategy, and acceptance scenarios in sections 4 and 5.
- When the execution-plan artifact is enabled, every plan slice and compiled task must carry explicit requirement IDs, and those IDs remain the canonical linkage into `requirement-coverage.json`.

## 7. Open questions and risks

### 14.1 Preflight checks

Before `fabrikk launch`, the system must validate:

- required CLIs are on `PATH`
- required authentication is available
- a trivial health-check invocation succeeds for every mandatory backend
- required model tiers for the planned run are reachable
- canonical run artifacts parse cleanly
- no other active run exists in the repository
- the quality gate command is executable and completes successfully on the current working tree (catches broken toolchains before detached execution begins)

### 14.2 Backend failures

If a required review backend is unavailable:

- retry up to 3 times
- then mark the task `blocked`
- then mark the run `blocked` if no other dispatchable work remains

There is no automatic quorum downgrade in v1.

### 14.3 Conservative recovery

When unsure, the system keeps work open rather than claiming success.

### Security and trust boundaries

v1 is a local orchestration product, not a secure sandbox.

It does not attempt to:

- prevent a determined local user from editing run state
- guarantee that model backends faithfully execute prompts
- provide cryptographic proof of code correctness

v1 does aim to make false closure operationally harder and more visible.

### Test strategy

The implementation must include coverage for:

- stable task compilation from the approved artifact
- wave planning with disjoint-scope tasks running in parallel
- serial fallback when scope overlap or risk rules prevent safe wave execution
- single-writer state mutation
- artifact hash validation
- claim expiry and recovery
- detached run continuity
- scheduler ordering and concurrency limits
- worker lifecycle timeout and report-read ordering
- verifier failure paths
- reviewer disagreement handling
- finding deduplication across repeated reviewer rounds
- execution-time clarifications blocking and recovery
- repair task creation rules
- run-level closure
- status reattachment and explanation
- model routing and escalation behavior
- persona-based spec review orchestration, including independent review, synthesis, and targeted follow-up
- library-adapter behavior where external orchestration libraries are used, ensuring they do not bypass `attest` state and closure rules
- compilation-time requirement coverage validation blocking dispatch when requirements are unassigned
- deferral mechanism restricted to user-initiated approvals only
- child repair task requirement linkage carrying only the specific uncovered IDs from the triggering finding
- cross-task regression detection re-verifying earlier tasks when later tasks modify overlapping paths
- preparation council review running before approval and surfacing findings in `fabrikk review`
- project quality gate detection during preparation
- quality gate execution as the first verifier step, blocking model review on failure
- quality gate preflight validation at launch time

### Acceptance scenarios

- **AT-TS-001**: Preparing the same approved run artifact twice yields the same initial tasks.
- **AT-TS-002**: A launched run persists after the launcher exits and can be resumed after engine death.
- **AT-TS-003**: A task with no independent verifier evidence cannot close.
- **AT-TS-004**: Codex can block a task that passed deterministic verification.
- **AT-TS-005**: Gemini can block a task that passed deterministic verification.
- **AT-TS-006**: Opus synthesis cannot silently clear a specialist blocker without recorded counter-evidence.
- **AT-TS-007**: Reattaching later reproduces correct status from repo-local state alone.
- **AT-TS-008**: Given a technical spec plus review personas, the system can run independent persona reviews, consolidate the outputs, and produce a final action list without losing reviewer attribution.
- **AT-TS-009**: A run cannot complete while any requirement remains `unassigned` in `requirement-coverage.json`.
- **AT-TS-010**: A review backend outage blocks task closure after retry exhaustion.
- **AT-TS-011**: Two tasks with disjoint `owned_paths` and no dependencies may run in the same wave.
- **AT-TS-012**: Two tasks with overlapping `owned_paths` must not run in the same wave.
- **AT-TS-013**: `fabrikk review` renders a human-readable approval summary with source fingerprint, assumptions, clarifications, risk summary, and task preview.
- **AT-TS-014**: `fabrikk resume` on a blocked run distinguishes transient blockers from blockers that require human action.
- **AT-TS-015**: A reviewer restating the same blocking issue does not create duplicate repair tasks when the `dedupe_key` matches an existing open finding.
- **AT-TS-016**: The scheduler dispatches higher-priority ready tasks before lower-priority ready tasks.
- **AT-TS-017**: A worker report is not read until the worker subprocess has exited.
- **AT-TS-018**: An execution-time clarification blocks the affected task and is surfaced by `fabrikk explain`.
- **AT-TS-019**: Only the user can create a requirement deferral; automated components cannot manufacture deferrals to bypass closure.
- **AT-TS-020**: After task compilation, any requirement ID not assigned to at least one task blocks the run from dispatching.
- **AT-TS-021**: A child repair task carries only the specific uncovered requirement IDs from the triggering finding, not the parent's full requirement set.
- **AT-TS-022**: When a later task's changes overlap an earlier completed task's `read_only_paths` or `shared_paths`, the earlier task is flagged for re-verification.
- **AT-TS-023**: Each task attempt records the backend model version string when available from the provider.
- **AT-TS-024**: The preparation council review runs before user approval and its findings are visible in `fabrikk review`.
- **AT-TS-025**: A task whose implementation fails the project quality gate (e.g., `just check` returns non-zero) cannot proceed to model review.
- **AT-TS-026**: `fabrikk prepare` detects a Justfile or Makefile with a `check` target and proposes it as the quality gate in the draft run artifact.
- **AT-TS-027**: `fabrikk launch` preflight runs the quality gate on the current working tree and blocks launch if it fails.

### Implementation phasing and self-hosting

`attest` must be built in phases where each phase dogfoods the output of the previous one. This is not optional — it is the primary validation strategy for the product.

### 18.1 Phasing rationale

`attest` cannot use itself to build itself from scratch. But it can be structured so that each implementation phase produces a working subset that verifies the next phase's work. This makes the bootstrapping genuine: attest's own spec is the first spec it runs, and attest's own quality gate is the first gate it enforces.

### 18.2 Phase 0: Project skeleton and quality gate

Build the Go project structure and the project quality gate before any product code.

Deliverables:

- Go module, directory layout matching the canonical package structure
- `Justfile` with a `check` target that runs: `go vet`, `golangci-lint`, `gofumpt --extra -l`, `go test ./...`
- CI-equivalent local toolchain so that `just check` is the single entry point for "my code is clean"

This phase is built manually. No fabrikk engine exists yet. The quality gate is the foundation that every subsequent phase builds on.

Exit criteria:

- `just check` passes on an empty project
- the quality gate contract matches section 11.2

### 18.3 Phase 1: Minimal engine loop

Build the minimum viable verification loop using manual discipline and the quality gate.

Deliverables:

- spec ingester: reads the fabrikk technical spec, extracts requirement IDs
- task compiler: deterministic, rule-based, non-LLM
- file-based state: `tasks.json`, `claims/`, `reports/`, atomic writes
- single-worker dispatcher: serial execution, Claude Sonnet as implementer
- quality gate runner: invokes `just check` as the first verifier step
- deterministic verifier: exit codes, file change validation, scope check, requirement linkage
- requirement coverage computation after task compilation

No council, no Codex/Gemini review, no repair loop, no detached execution in this phase. Workers run in foreground. The verification pipeline is: quality gate -> deterministic verifier -> manual human review (replacing council).

This phase is built manually. Every piece of code must pass `just check`.

Exit criteria:

- the engine can ingest the fabrikk spec, compile tasks, dispatch one task to Sonnet, run the quality gate and verifier, and accept or reject the result
- AT-TS-001 (deterministic compilation) is testable
- AT-TS-003 (no evidence, no closure) is testable
- AT-TS-025 (quality gate blocks model review) is testable

### 18.4 Phase 2: Council (self-hosted inflection point)

Use the Phase 1 engine to build the council pipeline.

The Phase 1 engine runs against the fabrikk spec with tasks scoped to:

- `internal/councilflow` package
- Codex review integration
- Gemini review integration
- Opus synthesis integration
- council verdict schema and closure gate enforcement

Each task is dispatched to Sonnet, verified by the quality gate and deterministic verifier, then manually reviewed (since the council doesn't exist yet). When the council implementation passes manual review and `just check`, it is merged and enabled.

This is the self-hosting inflection point. From this phase forward, all subsequent implementation tasks are reviewed by the council.

Exit criteria:

- the full per-task review sequence (verifier -> Codex -> Gemini -> Opus) runs and produces structured verdicts
- AT-TS-004 (Codex can block), AT-TS-005 (Gemini can block), AT-TS-006 (Opus counter-evidence) are testable
- the Phase 1 engine can dispatch tasks and route them through the council for acceptance

### 18.5 Phase 3: Repair and convergence

Use the Phase 2 engine (with council) to build the repair reconciler.

Tasks in this phase:

- repair task creation from blocking findings
- child repair task requirement linkage (specific uncovered IDs only)
- repair depth limit enforcement
- finding deduplication across rounds
- escalation rules (Sonnet -> Opus on repeated failure)

Each task is verified by the quality gate, deterministic verifier, and council. Repair itself doesn't exist yet for this phase's own tasks, so any council rejection requires manual re-dispatch. When repair passes council review and is merged, it is enabled for subsequent phases.

Exit criteria:

- blocking findings create repair tasks automatically
- repair tasks carry only the specific uncovered requirement IDs
- AT-TS-015 (dedupe), AT-TS-021 (repair linkage) are testable
- repair depth limit blocks after 3 attempts

### 18.6 Phase 4: Detached execution and reattachment

Use the Phase 3 engine (with council and repair) to build the detached lifecycle.

Tasks in this phase:

- engine process detachment from launching terminal
- engine liveness artifact and heartbeat
- `fabrikk resume` with stale-engine recovery
- `fabrikk stop` with graceful shutdown
- `fabrikk status` with per-task operational detail
- `fabrikk explain` with blocker diagnostics

Each task runs through the full pipeline: quality gate, verifier, Codex, Gemini, Opus, repair if needed.

Exit criteria:

- AT-TS-002 (run persists after launcher exits) is testable
- AT-TS-007 (reattachment from state alone) is testable
- AT-TS-014 (resume distinguishes transient vs human blockers) is testable

### 18.7 Phase 5: Remaining pipeline

Use the full engine to build:

- preparation council (spec review before approval)
- cross-task regression detection
- run-level integration review
- execution-time clarifications
- deferral mechanism
- wave-based parallel execution

Each feature is implemented, reviewed, and repaired by the system it extends.

Exit criteria:

- all acceptance scenarios AT-TS-001 through AT-TS-027 pass
- the fabrikk spec itself has been used as the input spec for the entire build
- the full v1 feature set is implemented and verified by its own pipeline

### 18.8 Dogfooding contract

Throughout all phases:

- the fabrikk technical spec is the canonical input spec
- every implementation task must trace to requirement IDs from this spec
- `just check` is the quality gate for every task, in every phase
- no task is accepted without passing the verification gates available in the current phase
- when a new verification capability is completed and merged, it must be enabled for all subsequent tasks immediately — capabilities are never deferred once they pass their own review

### Open v1 decisions

These decisions should be finalized during implementation planning, not left implicit:

- whether Roam is adopted for structural intelligence, and if so, which pipeline stages use it first
- exact CLI flag surface and config layout
- run directory location override support
- whether stop supports graceful-only behavior or also force-stop behavior in v1

## 8. Approval

- Drafted by: Engineering
- Reviewed by: pending planning review
- Approved by: pending
- Approved artifact hash: pending
