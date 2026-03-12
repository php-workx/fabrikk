# Systems Architect Review

Reviewer: Systems Architect (independent round-1 review)
Document under review: attest Technical Specification v0.1
Supporting context: attest Functional Specification v0.1
Date: 2026-03-12

---

## Findings

### CRITICAL

- **[critical] Detached execution has no process model**
  - Section: 12.1, 2.1
  - Problem: The entire product thesis is "approve once, walk away, execution continues." Section 12.1 says `attest launch` transitions to detached execution and "the launching session may exit." Section 2.1 calls it a "local run engine" that "executes autonomous background work." But there is no specification of what runs. Is the run engine a daemon? A backgrounded process? A launchd/systemd service? How does it survive terminal close? Laptop lid close? Machine reboot? Who manages its lifecycle?
  - Failure mode: An implementer will pick the path of least resistance -- a goroutine or background shell process -- and it will die when the terminal closes. AT-TS-002 ("a launched run persists after the launcher exits") becomes untestable because the persistence mechanism is unspecified. Alternatively, two implementers build two incompatible process models and the rest of the system (claim heartbeats, dispatcher, reattachment) breaks.
  - Required correction: Add a section specifying the run engine process model: how it is spawned, how it detaches from the launching terminal (e.g., double-fork, `nohup`, `setsid`), where it writes its PID, how it logs, and how `attest status` / `attest resume` detect whether the engine is alive or dead. Specify what happens on host reboot (run is orphaned and must be resumable from durable state alone).

- **[critical] File-based state has no concurrency safety model**
  - Section: 3.1, 3.2, 3.3, 3.4, 7.1
  - Problem: All state is JSON files under `.attest/runs/<run-id>/`. During execution, the dispatcher reads `tasks.json` to find claimable tasks, the run engine writes task state transitions to it, and the repair reconciler appends new tasks to it. Claims are individual files but claim acquisition (7.1) requires an atomic check-and-write -- "a non-expired claim already exists" must be evaluated and the new claim written without a TOCTOU window. JSON files are not databases; partial writes corrupt them, and there is no mention of file locking, atomic rename, or any conflict resolution strategy.
  - Failure mode: Two workers attempt to claim different tasks simultaneously. Both read `tasks.json`, both see their target as claimable, both write claim files. Or worse: the run engine updates `tasks.json` while a worker reads it, getting a partial write. A single truncated JSON file can make the entire run unrecoverable.
  - Required correction: Specify the concurrency safety model for file-based state. At minimum: (1) individual claim files with atomic write-via-rename, (2) `tasks.json` write serialization through the run engine process only (workers never write it directly), (3) a defined recovery procedure for corrupted JSON files (or use append-only JSONL with periodic compaction).

- **[critical] Run and task state machines have no transition rules**
  - Section: 5.1, 5.2
  - Problem: Both state machines list states but never define the valid transitions, their triggers, or their guards. For run states: can `running` go to `preparing`? What moves a run from `running` to `blocked` vs `failed`? Can `failed` be recovered? Can `stopped` be resumed? For task states: what moves `pending` to `claimed`? What moves `repair_pending` to -- back to `pending`? To `claimed`? Can `blocked` transition to anything, and if so what unblocks it? Is `failed` terminal?
  - Failure mode: Without a transition table, every component that reads or writes state will implement its own ad-hoc rules. The dispatcher assumes blocked tasks can be retried; the status renderer assumes they cannot. The repair reconciler moves a task to `repair_pending` but the claim manager does not know which states are claimable. Run-level `failed` vs `blocked` becomes arbitrary, and `attest resume` cannot know which states it can act on.
  - Required correction: Add an explicit transition table for both run states and task states. Each transition must list: source state, target state, triggering event, and guard conditions. Mark terminal states explicitly.

- **[critical] No git isolation model for concurrent workers**
  - Section: 8.1, 6.2, 7.1
  - Problem: Workers implement code changes in the same repository. Multiple workers can run concurrently (the entire claim/scope system assumes this). But the spec never describes how workers are isolated at the git level. Do they work on branches? Git worktrees? A shared working tree? If two workers are active simultaneously and both make commits, they will conflict on HEAD. Even if scope reservations prevent editing the same file, two workers cannot both stage, commit, and push to the same branch in the same worktree without corrupting git state.
  - Failure mode: Worker A is implementing task 1 on files in `pkg/auth/`. Worker B is implementing task 2 on files in `pkg/storage/`. Both finish and try to commit. Worker B's commit includes Worker A's unstaged changes, or worse, Worker B's `git add .` stages Worker A's half-written files. Alternatively, both workers call `git commit` at the same instant and one gets a lock error. This is not an edge case -- it is the default behavior when multiple processes share a working tree.
  - Required correction: Specify the git isolation model. The most natural fit is one git worktree per active worker (Go can create these programmatically). The spec must define: who creates worktrees, who merges completed work back to the main branch, what happens when a merge conflicts, and whether the run engine or the worker is responsible for the merge.

### HIGH

- **[high] Run engine liveness is undetectable at reattachment**
  - Section: 12.2
  - Problem: A reattaching session reads `.attest/runs/<run-id>/` and reconstructs status. But it has no way to determine whether the run engine is still alive and making progress, crashed and left stale state, or was killed and left in-flight claims that will never heartbeat again. There is no PID file, lock file, engine heartbeat timestamp, or process probe specified.
  - Failure mode: User runs `attest status` and sees tasks in `implementing` with active claims. The run engine died 3 hours ago. The user waits. Nothing happens. There is no way to distinguish "in progress" from "orphaned." `attest resume` cannot safely restart execution without knowing whether the old engine is dead, because starting a second engine would create duplicate dispatching and claim races.
  - Required correction: Specify a run engine liveness artifact (heartbeat file with a timestamp and PID, or a lock file). Define the rule for `attest resume`: if the engine is alive, attach as observer; if the engine is dead, take over as the new engine after expiring all stale claims.

- **[high] Task dependencies are absent from the task schema**
  - Section: 3.3, 6.2
  - Problem: `tasks.json` has `parent_task_id` and `created_from` but no `depends_on` or ordering field. Task compilation rule 6.2 says "avoid assigning the same primary file scope to multiple concurrent tasks," implying ordering constraints exist. But the dispatcher has no schema to express "task B cannot start until task A completes." The run artifact has `dependencies` (3.2) but these are run-level, not task-level.
  - Failure mode: The dispatcher launches tasks in arbitrary order. A task that implements function signatures runs concurrently with a task that consumes those signatures. Both claim non-overlapping file scopes (caller vs callee files), so scope reservations do not block it, but the consumer task fails because the signatures do not exist yet. The system treats this as a normal failure and enters a repair loop that was entirely avoidable with ordering.
  - Required correction: Add `depends_on: [task_id, ...]` to the task schema. Define the dispatcher rule: a task is dispatchable only when all `depends_on` tasks are `done`. Define what happens when a dependency enters `failed` or `blocked` (dependent tasks should transition to `blocked`).

- **[high] Repair loop has no depth bound or convergence definition**
  - Section: 11.1, 11.2, 9.2, 18
  - Problem: Repair tasks are auto-created when verification fails (11.1). A repair task is itself a task subject to verification and council. If the repair fails, another repair task is created. Section 9.2 says "Sonnet -> Opus when repair loops do not converge," but "converge" is undefined, no maximum repair depth is specified, and section 18 defers "maximum retry counts before blocker state" as an open decision. This is not a tuning parameter -- it is a fundamental lifecycle rule that determines whether the system can reach a terminal state.
  - Failure mode: A task with a subtle spec ambiguity fails verification, spawns a repair task, which also fails, which spawns another, indefinitely. Each iteration consumes API tokens and file system space. The run never reaches `blocked` or `failed` because no rule triggers that transition. The user returns to find 47 nested repair tasks and a large bill.
  - Required correction: Specify a maximum repair depth per task lineage (e.g., 3). Specify that exceeding the depth moves the root task to `blocked` with a structured explanation. Define "repair loop does not converge" as a concrete condition (e.g., same verifier failure class on consecutive attempts). Move this out of section 18 open decisions into a normative rule.

- **[high] Dispatcher scheduling logic is unspecified**
  - Section: 2.1 (item 7)
  - Problem: The dispatcher "launches worker and reviewer jobs against supported CLIs." But the spec says nothing about: how many workers can run concurrently, how the dispatcher selects from multiple claimable tasks, how it respects task dependencies (which are also missing per the finding above), how it manages resource limits (API rate limits, local CPU), or what its scheduling loop looks like (polling interval, event-driven, etc.).
  - Failure mode: Without concurrency limits, the dispatcher launches all claimable tasks simultaneously, overwhelming the machine or hitting API rate limits. Without selection priority, high-priority tasks sit behind low-priority ones. Without a defined scheduling loop, the dispatcher either polls too aggressively (wasting resources) or too lazily (wasting wall-clock time).
  - Required correction: Specify: maximum concurrent workers (configurable), task selection order (priority then creation time), scheduling loop mechanism, and behavior when a backend is rate-limited or unavailable.

- **[high] Heartbeat parameters and failure detection timing are undefined**
  - Section: 7.1, 7.2
  - Problem: Claims have `lease_expires_at` and `heartbeat_at`, and expire "if the owner stops heartbeating before `lease_expires_at`." But there is no specification of: initial lease duration, heartbeat interval, lease renewal mechanics, or who checks for expiration. If the heartbeat interval is 60s and the lease is 120s, a single missed heartbeat kills the claim. If the lease is 24h, a crashed worker blocks a task for a day.
  - Failure mode: Implementers pick arbitrary values. A long lease makes the system unresponsive to worker crashes. A short lease causes healthy workers to lose claims during legitimate long-running operations (e.g., a large compilation). Without specifying who checks for expiration (the run engine on a timer? the dispatcher before each claim?), expired claims may sit indefinitely.
  - Required correction: Specify: default lease duration, heartbeat interval, lease renewal rule, who is responsible for expiration checks (run engine polling on a defined interval), and the state transition for a task whose claim expires (currently ambiguous between `repair_pending` and `blocked` per 7.3).

- **[high] `attest resume` is a command with no defined semantics**
  - Section: 4.1, 12.2
  - Problem: `attest resume <run-id>` is listed as a command. Section 12.2 says reattachment reads repo-local state. But "resume" implies an action, not just observation -- `attest status` already covers observation. What does resume do? Does it restart the run engine if it died? Does it retry blocked tasks? Does it prompt for clarification that was requested while the run was blocked? Does it work on a `stopped` run? A `failed` run? A `blocked` run? Each of these requires different behavior, and none is specified.
  - Failure mode: A user's run engine crashed. They run `attest resume`. The implementer interpreted resume as "display status" (since that is all the spec supports). The user expected the run to restart. The run stays dead. Alternatively, resume restarts the engine but does not expire stale claims from the dead engine, creating duplicate ownership.
  - Required correction: Define resume as: (1) detect whether the run engine is alive, (2) if dead, expire stale claims and restart the engine, (3) if alive, reattach as an observer. Specify which run states are resumable and which are terminal.

- **[high] Run-level acceptance is required by the functional spec but missing from the technical spec**
  - Section: 5.3, 10 (functional spec AT-FR-023, AT-AS-007)
  - Problem: The closure rule (5.3) defines when a task can transition to `done`. But there is no run-level acceptance gate. The functional spec (AT-FR-023) requires "council checkpoints at least for spec review, task review, task verification, and final run acceptance." AT-AS-007 says "A run finishes only when every requirement in the approved artifact is verified or explicitly blocked." The technical spec has no run-level council, no run-level verification step, and no definition of when a run transitions from `running` to `completed`. Is it purely when all tasks are `done`? What about requirements that were split across multiple tasks -- who verifies they compose correctly?
  - Failure mode: All tasks pass individually but the integration is broken. Task A implemented the API, task B implemented the client, both passed review independently, but the client calls the wrong endpoint. No run-level gate catches this because the spec only defines per-task verification.
  - Required correction: Define the run completion rule: all tasks `done` AND a run-level integration verification pass AND (optionally) a run-level council synthesis. Specify what the run-level verifier checks that per-task verifiers cannot (cross-task requirement coverage, integration coherence).

- **[high] Worker scope enforcement is honor-system only**
  - Section: 3.4, 7.1
  - Problem: Claims have `scope_reservations` and the claim manager rejects overlapping scopes. But nothing prevents a worker from modifying files outside its claimed scope. The worker is a subprocess running an LLM -- it will modify whatever it thinks is necessary. If Claude decides that fixing `pkg/auth/handler.go` also requires changing `pkg/config/defaults.go`, it will change both, even if `pkg/config/` is reserved by another claim.
  - Failure mode: Worker A has scope `pkg/auth/*`. Worker B has scope `pkg/config/*`. Worker A modifies `pkg/config/defaults.go` because it needs a new config key. Worker B also modifies `pkg/config/defaults.go`. Both pass their individual reviews. The merge creates a conflict, or worse, one silently overwrites the other's changes.
  - Required correction: Specify a post-implementation scope check in the verifier: if the completion report lists changed files outside the claim's `scope_reservations`, verification fails with a scope violation finding. This makes scope enforcement deterministic rather than relying on worker self-discipline.

- **[high] Verifier's "deterministic" label is misleading for requirement coverage checks**
  - Section: 10.1, 9.1
  - Problem: The verifier is listed as "non-LLM" in the routing table (9.1) and described as "deterministic." Its duties include "confirm claimed requirements are not missing from the evidence bundle." But matching a requirement like "AT-FR-009: The system must prevent multiple workers from owning the same task concurrently" to evidence in a completion report requires semantic understanding, not just checking that a file exists or a command was run. Either the verifier is less deterministic than stated (it uses an LLM), or it can only check syntactic properties (file exists, command exit code zero, requirement ID appears in report text) -- which means it is weaker than the spec implies.
  - Failure mode: The verifier checks that the completion report mentions requirement ID "AT-FR-009" and that tests passed. It marks coverage as satisfied. But the implementation is actually wrong -- it just happens to mention the ID. The verifier's "pass" gives false confidence. Alternatively, an implementer builds a genuinely deterministic verifier that can only check file existence, and the system lets garbage implementations through because requirement coverage is unchecked.
  - Required correction: Clearly delineate what the deterministic verifier can check (structural/syntactic properties) vs what it cannot (semantic correctness). Make explicit that semantic correctness is the council's job, not the verifier's. Rename the verifier duties to match: "confirm the completion report references all linked requirement IDs" rather than "confirm claimed requirements are not missing from the evidence bundle."

- **[high] No multi-run isolation in the same repository**
  - Section: 3.1
  - Problem: The directory layout puts runs under `.attest/runs/<run-id>/`. Two runs can coexist in the same repo. But if both runs are active simultaneously, their workers are modifying the same repository's working tree. Even if the tasks do not overlap (different specs, different requirements), the workers share the same git state, the same file system, and potentially the same build artifacts.
  - Failure mode: Run 1 is implementing a database migration. Run 2 is implementing a UI feature. Run 2's worker runs the test suite. The test suite fails because Run 1's migration left the database schema in a transitional state. Run 2's task enters a repair loop for a problem that has nothing to do with its own implementation.
  - Required correction: Either (1) enforce that only one run can be in `running` state per repository at a time, or (2) specify the isolation model between concurrent runs (separate worktrees, separate branches, separate build environments).

### MEDIUM

- **[medium] Council synthesis decision model is underspecified**
  - Section: 10.2, 10.3, 11.1
  - Problem: Opus synthesis is the final gate before closure. But the spec does not define: what inputs Opus receives (full task scope? diffs? all reviewer outputs?), what decisions Opus can render (pass/fail/repair/block/escalate?), how Opus communicates a decision (structured JSON or free text?), or how "Opus synthesis decides the task is incomplete or risky" (11.1) is mechanically distinguished from "high-confidence reviewer objection not reconciled by Opus synthesis" (10.3).
  - Failure mode: The council becomes a black box that produces unstructured text, and the system has to parse natural language to determine the next action. Or Opus returns ambiguous verdicts and the system cannot decide between reopening, creating a child repair task, or blocking.
  - Required correction: Define the council synthesis output schema: an enum of possible verdicts (`pass`, `repair`, `reopen`, `block`, `escalate`), required fields for each verdict type, and the mapping from verdict to task state transition.

- **[medium] `attest stop` behavior is undefined**
  - Section: 4.1, 5.1
  - Problem: `attest stop <run-id>` exists as a command and `stopped` exists as a run state, but there is no specification of: whether stop is graceful or immediate, what happens to in-flight workers and their claims, whether a stopped run can be resumed (and if so via what command -- `attest resume`? `attest launch`?), and what state in-flight tasks transition to.
  - Failure mode: `attest stop` kills the run engine but workers continue running detached. They complete work and write reports but no one processes them. Or stop expires all claims immediately, and workers writing mid-claim get their output discarded. A user who stops a run to fix a blocker cannot resume it because no transition from `stopped` is defined.
  - Required correction: Define: stop sends a signal to the run engine, the engine stops dispatching new work, in-flight workers are allowed to complete (or given a grace period), claims for un-started work are released, and `attest resume` can restart a stopped run.

- **[medium] Scope reservation conflict detection is deferred but load-bearing**
  - Section: 3.4, 7.1, 18
  - Problem: Claim acquisition fails "when the requested scope conflicts with another active scope reservation" (7.1), and claims contain `scope_reservations` (3.4). But the scope model -- what constitutes a reservation, what constitutes a conflict, and what granularity is used -- is deferred entirely to section 18. This is not a cosmetic decision. It directly determines whether AT-FR-010 ("prevent obviously overlapping task execution") can be implemented.
  - Failure mode: Without a scope model, an implementer either skips scope checking (making it a no-op and breaking isolation) or invents an ad-hoc model (e.g., directory-level locking that is too coarse, causing unnecessary serialization).
  - Required correction: Define at minimum: scope reservations are path globs, conflict is defined as overlapping path coverage, and the claim manager rejects claims with overlapping globs against any active claim's reservations.

- **[medium] `tasks.json` is a single mutable file for the entire task graph**
  - Section: 3.3
  - Problem: All tasks live in one JSON file. During execution, repair tasks are appended, task states are updated, and the dispatcher reads from it. Beyond the concurrency issues (covered in the critical finding above), this creates a scaling problem: a run with many tasks produces a large file that must be fully rewritten on every state change, and diffs become unreadable.
  - Failure mode: A run with 50 tasks and multiple repair iterations produces a `tasks.json` that is rewritten hundreds of times. Each rewrite risks corruption. Git-based inspection of changes becomes impractical. Recovery from partial corruption loses all task state, not just the affected task.
  - Required correction: Consider splitting to `tasks/<task-id>.json` matching the pattern already used for claims and reports. If `tasks.json` is retained as the canonical form, specify that individual task state files are the authoritative source and `tasks.json` is a computed index.

- **[medium] No specification for worker prompt construction**
  - Section: 8.1, 8.2, 8.3
  - Problem: Workers are CLI backends (Claude, Codex, Gemini) that receive some instruction and produce reports. But the spec never defines how a task is translated into a prompt or command for the worker. What does the Claude CLI receive? The task scope? The full run artifact? The requirement text? File paths? This is the interface between the orchestrator and all execution -- if it is left implicit, each backend adapter will invent its own mapping and the reports will be structurally inconsistent.
  - Failure mode: The Claude backend receives rich context and produces good reports. The Codex review backend receives only the diff and misses spec context. Review quality varies by backend not because of model capability, but because of prompt construction differences that were never specified.
  - Required correction: Define the input contract for each worker role: implementer receives [task scope, requirement text, file manifest, run artifact excerpt]; reviewer receives [task scope, completion report, changed files, requirement text]. Define the expected output schema reference for each role.

- **[medium] Run artifact immutability is not enforced**
  - Section: 3.2, 5.3
  - Problem: The run artifact is "the approved normalized contract" and the source of truth for the run. But nothing prevents a process -- or the user -- from modifying `run-artifact.json` after approval. There is no checksum, no hash stored at approval time, no read-only flag. The `approved_at` and `approved_by` fields record when it was approved but not what was approved.
  - Failure mode: A worker subprocess or a user accidentally edits the run artifact mid-run. Task compilation is supposed to be deterministic from the artifact -- but the artifact has silently changed. New tasks compiled from the modified artifact will not match existing task IDs. The run enters an inconsistent state where some tasks reference requirements that no longer exist in the artifact.
  - Required correction: Compute and store a SHA-256 hash of the run artifact at approval time. The run engine should verify the hash at startup and on every task compilation. If the hash does not match, the run should enter `blocked` with a clear integrity violation message.

- **[medium] Cross-task findings have no mechanism**
  - Section: 10, 11
  - Problem: The verification and council pipeline is entirely per-task. A reviewer examines one task's output, produces findings for that task, and the repair reconciler acts on that task. But integration problems -- where task A and task B are individually correct but incompatible together -- have no path to detection. No reviewer is asked to examine cross-task interactions. No finding type maps to "these two tasks conflict."
  - Failure mode: Task A defines an API with field `user_id: string`. Task B consumes that API expecting `userId: number`. Both pass individual review because each is internally consistent. The integration fails at runtime. The system has no mechanism to catch this because no reviewer ever sees both tasks' outputs together.
  - Required correction: Define a cross-task integration review stage triggered when dependent tasks are both done. The integration reviewer receives the outputs of both tasks and checks interface compatibility. This can be part of the run-level acceptance gate described in the run-level acceptance finding above.

- **[medium] Clarification during execution is implied but unspecified**
  - Section: 12.3, 2.1 (item 3)
  - Problem: Section 12.3 says a run enters `blocked` when "clarifying input is required from the user." The clarification manager (2.1, item 3) is described as a preparation-time component. But if clarification can be needed during execution (after the run is approved and launched), there must be a mechanism for: a worker or the council to request clarification, the run engine to block the relevant task or run, the clarification to be surfaced at reattachment, and the user's answer to be recorded and fed back to the blocked work. None of this is specified.
  - Failure mode: During execution, Opus synthesis determines that requirement AT-FR-042 is ambiguous and cannot be resolved without user input. The system has no structured way to capture this. The task either fails (repair loop) or blocks (but the user sees "blocked" with no explanation of what clarification is needed). On reattachment, `attest explain` says "blocked" but cannot tell the user what question needs answering.
  - Required correction: Extend the clarification manager to support execution-time clarifications. Define: a structured clarification request format, which components can generate them (council, verifier, worker), how they are persisted (in the run directory), how they block the relevant task/run, and how `attest explain` surfaces pending clarification questions.

- **[medium] Worker-to-engine communication protocol is undefined**
  - Section: 8.1, 8.2, 8.3
  - Problem: Workers are CLI subprocesses. The spec does not define: how the engine knows a worker is still running (does it call `Wait()` on the subprocess? poll for a report file? check heartbeats?), how a worker reports incremental progress vs final completion, or what the timeout mechanism is. If the engine spawns a subprocess and calls `Wait()`, the Claude CLI hangs indefinitely (network timeout, rate limit). The claim expires, but the engine is blocked on `Wait()`. No other tasks can be dispatched if the engine is single-threaded-per-worker.
  - Failure mode: The engine blocks on `Wait()` for a hung subprocess. The claim expires. The engine cannot reassign the task because it is still waiting. Or: the engine polls for report files but the worker is still writing them -- the engine reads a partial report.
  - Required correction: Specify the worker lifecycle contract: the engine spawns the subprocess, monitors it with a timeout (derived from the claim lease), and on exit checks for the report file. Missing report + non-zero exit = failed attempt (already in 14.1). Missing report + zero exit = also failed attempt. Add: the engine must not read the report until the subprocess has exited.

### LOW

- **[low] `run-status.json` update frequency and authority are undefined**
  - Section: 13.1
  - Problem: `run-status.json` is the observability artifact, but the spec does not say who writes it or how often it is updated. Is it a computed view regenerated on every state change? Written by the run engine on a timer? Computed on-demand by `attest status`?
  - Failure mode: If it is written periodically, a reattaching session sees stale data. If it is computed on-demand, the latency of `attest status` depends on the size of the run. Minor, but affects user experience during reattachment.
  - Required correction: Specify whether `run-status.json` is an authoritative artifact updated on every state change, or a computed view generated by `attest status`.

- **[low] No versioning strategy for schema evolution**
  - Section: 3.2 (`schema_version` field)
  - Problem: `run-artifact.json` has `schema_version` but there is no specification of what happens when the binary version and the schema version diverge. Can a newer `attest` binary resume a run created by an older version? What about the reverse?
  - Failure mode: A user updates `attest` mid-run. The new binary reads the old `tasks.json` and misinterprets a field. Low risk in v1 but worth a one-line rule.
  - Required correction: Specify: the binary must refuse to operate on a run whose `schema_version` is higher than it supports, and must handle older versions by either supporting them or refusing with an explicit error.

- **[low] Routing traceability has no defined storage location**
  - Section: 9.3
  - Problem: Every task attempt must record selected backend, model tier, escalation cause, and retry count. But there is no artifact for this. It is not in `tasks.json` (which has no `attempts` field), not in the claim file (which is per-claim, not per-attempt), and not in any report file.
  - Failure mode: Routing traceability data is lost or scattered across log files, making the escalation path unreconstructable for debugging or audit.
  - Required correction: Add an `attempts` array to the task schema, or define a `tasks/<task-id>/attempts/` directory with per-attempt metadata files.

- **[low] No log or event stream for the run engine**
  - Section: 13
  - Problem: The observability section defines `run-status.json` and the `attest explain` command, but there is no structured event log or run engine log specified. When debugging why a task failed or a claim expired, the only artifacts are the final-state JSON files, not the sequence of events that led there.
  - Failure mode: A task enters `blocked` and the user cannot determine what happened between `implementing` and `blocked` because no intermediate events were recorded. The status file shows the current state but not the path to it.
  - Required correction: Specify a structured event log (append-only JSONL) in the run directory. Each state transition, claim event, dispatch event, and verification event is appended with a timestamp. This is low-cost to implement and high-value for debugging.

---

## Open Questions

1. **What is the run engine process model?** Daemon, long-running goroutine, systemd unit, launchd plist? This is the single biggest unresolved architectural question. The answer determines how heartbeats work, how reattachment works, and whether AT-TS-002 is achievable.

2. **Can two `attest` processes operate on the same run simultaneously?** If yes, how is coordination achieved? If no, what enforces exclusion? This is distinct from the worker concurrency question -- this is about two `attest` binaries (e.g., user runs `attest status` while the engine is running).

3. **What happens to in-progress work when the host machine reboots?** The spec says state survives process exit, but does not address cold start recovery. Are all in-flight claims treated as expired? Is the run resumable from durable state alone with no special recovery procedure?

4. **How are API credentials for Codex and Gemini managed?** The spec says the user's existing subscriptions are used, but does not address credential discovery, validation, or failure when credentials are missing or expired mid-run.

5. **What is the council quorum rule?** Section 18 defers this, but the closure rule (5.3) requires both Codex and Gemini to be non-blocking. If one backend is unavailable, can the run progress at all? This determines whether a Codex outage blocks all progress indefinitely.

6. **How does the system handle spec mutations after approval?** If the user edits the source spec files on disk after a run is approved and launched, do workers see the old or new content? The run artifact is supposed to be the source of truth, but source spec files are on disk and workers have file system access.

7. **What is the task compilation algorithm?** Section 6.1 requires determinism from the approved artifact, but the spec does not describe the algorithm. Is it rule-based? Template-based? LLM-assisted-then-frozen? If it is LLM-assisted, the determinism guarantee depends on temperature=0 and model version pinning, neither of which is mentioned.

8. **How does `attest prepare` handle specs that lack requirement IDs?** AT-FR-002 requires stable requirement IDs, but section 6.1 of the functional spec says the product "should not require fully rigid prose structures on day one." If the user's spec has no IDs, does `attest prepare` generate them? If so, the "deterministic from approved artifact" guarantee starts from generated IDs, which shifts the stability question upstream.

---

## Assumptions The Spec Is Making

1. **Only one run engine process exists per run at any time.** Nothing enforces this, and the reattachment model could accidentally start a second.

2. **Workers are short-lived subprocesses, not persistent agents.** The claim-with-heartbeat model implies workers are processes the run engine spawns and monitors, but this is never stated. If workers are long-lived agents with their own state, the heartbeat and lease model changes fundamentally.

3. **File I/O is fast and reliable.** The file-based state model assumes no NFS, no network drives, no disk-full conditions. A disk-full condition during a `tasks.json` write would corrupt run state with no recovery path.

4. **Claude CLI, Codex CLI, and Gemini CLI all accept structured input and produce structured output.** The actual CLI interfaces of these tools are not referenced. If any of them produce only free-text output, the report parsing problem becomes much harder than the spec implies.

5. **A single local machine has enough resources to run the run engine, multiple Claude workers, Codex reviews, and Gemini reviews concurrently.** No resource budgeting is discussed. A machine with 8GB RAM running multiple CLI sessions may struggle.

6. **The user's existing CLI tools are already authenticated and configured.** The spec has no provision for credential validation at launch time. A run that launches successfully and then discovers Codex is not authenticated after the user has walked away will block with no useful diagnostic.

7. **The task compiler is a non-LLM component.** Section 6.1 says task compilation must be deterministic. If an LLM is involved in compilation, determinism requires temperature=0, model pinning, and identical prompt construction -- none of which are mentioned.

8. **Codex and Gemini CLIs can be invoked programmatically with structured prompts and will return structured output.** The spec assumes these tools expose a subprocess-friendly interface, but this is an empirical question that depends on each vendor's CLI design.

---

## What Is Strong And Ready

1. **The approval boundary is mechanically clean.** The split between free-form preparation and frozen run artifact is the right architecture. The determinism rule applies only post-approval, which is realistic and avoids the trap of demanding deterministic behavior during exploratory planning.

2. **The closure rule (5.3) is the strongest part of the spec.** Requiring deterministic verification AND Codex non-blocking AND Gemini non-blocking AND Opus synthesis is a defensible multi-gate model that directly addresses the false-closure thesis. This is the core innovation and it is well-defined.

3. **The "fail closed" principle (7.3, 14.2) is stated clearly.** Timeout, malformed output, and missing reports all map to failure, not success. This is the right default for an autonomous system. The spec never takes the shortcut of treating silence as success.

4. **The artifact-per-task report structure (3.5) is well-decomposed.** Separate files for completion, verification, and each reviewer's output makes the system auditable and debuggable. Each artifact has a clear owner and purpose.

5. **The escalation rules (9.2) encode a real operational heuristic.** Haiku -> Sonnet -> Opus escalation on repeated failure is a pragmatic strategy for controlling cost while maintaining quality. The triggers are concrete enough to implement.

6. **Reattachment from repo-local state (12.2) is the right constraint.** No transcript dependency, no remote state, no session affinity -- this is the correct foundation for a detached system. The fact that the spec explicitly forbids dependence on the original transcript shows good architectural judgment.

7. **The separation of Codex/Gemini as reviewers-only in v1 (1.3) is a sound scope decision.** It avoids the multi-model execution coordination problem entirely while still capturing disagreement value. This is the right scope cut for v1.

8. **The conceptual data flow (2.2) is clean and linear.** The pipeline from spec ingestion through verification through council is easy to reason about and test in isolation. The absence of feedback loops in the data flow (repair creates new tasks, it does not re-enter the pipeline mid-flow) is a deliberate and correct simplification.

9. **The acceptance scenarios (section 17) are testable as written.** AT-TS-001 through AT-TS-008 each map to a concrete property that an integration test can verify. The spec does not rely on subjective "quality" criteria for acceptance.

---

## Summary Assessment

The spec is architecturally sound in its policy design -- the approval boundary, closure rule, multi-model review gates, and fail-closed defaults are all strong. The primary weakness is a systematic gap between policy intent and execution mechanism. The spec says what should be true but does not specify how it becomes true when real processes run concurrently on real files. The four critical findings (process model, concurrency safety, state transitions, git isolation) are all instances of this single pattern and would each independently prevent a reliable v1 implementation.

The recommended path forward: resolve the four critical findings first, as they determine the shape of nearly every other component. Then address the high findings, which mostly follow from the critical ones (engine liveness detection follows from process model, task dependencies follow from state transitions, repair bounds follow from state transitions, etc.). The medium findings are important but can be resolved during implementation planning without blocking architecture decisions.
