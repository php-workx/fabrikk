# Spec-Conformance Review

Reviewer role: Spec-conformance and verification reviewer
Document under review: attest Technical Specification v0.1
Supporting document: attest Functional Specification v0.1
Review date: 2026-03-12
Review type: Independent round-1

---

## Findings

### F-01 [critical] No run-level closure rule -- only task-level closure is gated

- **Section:** 5.3 (Closure rule), 5.1 (Run states)
- **Risk:** The spec defines a rigorous five-condition closure gate for individual tasks (verifier passes, Codex non-blocking, Gemini non-blocking, Opus synthesis clean, all requirement IDs satisfied or deferred). However, there is no corresponding closure rule for the run itself. Section 5.1 lists `completed` as a run state but never defines what conditions must hold for a run to transition to `completed`. The functional spec AT-AS-007 says "a run finishes only when every requirement in the approved artifact is verified or explicitly blocked," but the technical spec contains no mechanically enforceable equivalent.
- **False-complete path:** All individual tasks reach `done`, but some requirement IDs from `run-artifact.json` are not linked to any task at all. Since there is no run-level gate that cross-checks requirement coverage across the full task graph, the run transitions to `completed` with orphaned requirements. This is the single most likely path to false closure.
- **Required correction:** Add a section (e.g., 5.4 "Run closure rule") that specifies: a run may transition to `completed` only when (a) every requirement ID in the approved run artifact is linked to at least one task that reached `done`, or is explicitly marked as deferred with a policy reason; (b) no task is in a non-terminal state other than `failed` with an approved deferral; (c) the final run-level council checkpoint (AT-FR-023) has passed. This must be a deterministic check, not an LLM judgment.

### F-02 [critical] Deterministic verifier does not validate evidence content -- only evidence existence

- **Section:** 10.1 (Deterministic verifier)
- **Risk:** The verifier's duties are: confirm reports exist, confirm commands were run, confirm artifacts exist, confirm task links to requirement IDs, confirm claimed requirements are not missing from the evidence bundle. Every one of these is an existence check. None requires inspecting the content of the evidence. The verifier confirms "there is a test execution record" but not "the tests actually passed" or "the test covers the claimed requirement."
- **False-complete path:** A worker produces a completion report that lists test files as executed. The tests all fail, or the tests are trivial stubs that do not exercise the requirement. The deterministic verifier passes because the report exists and the requirement IDs are listed. The LLM reviewers (Codex, Gemini) are then the only gate, and their review is non-deterministic and may miss the gap, especially under token pressure or ambiguous test naming. The core promise of a "deterministic" verification layer is weakened to a structural lint pass.
- **Required correction:** Add to Section 10.1 at minimum: (a) the verifier must confirm that test commands exited with success status; (b) the verifier must confirm that each claimed requirement ID appears in at least one test output or evidence artifact by name or explicit mapping, not merely in the completion report's self-attestation. Define which evidence fields are self-reported (and therefore untrusted at the deterministic layer) versus which are independently observable (exit codes, file existence, file content hashes).

### F-03 [critical] "Intentionally deferred by policy" has no specification for the deferral mechanism

- **Section:** 5.3 (Closure rule), bullet 5
- **Risk:** The closure rule allows a task to close when linked requirement IDs are "intentionally deferred by policy." The spec never defines: who may create a deferral, what artifact records the deferral, whether deferred requirements count toward run-level coverage, what prevents a worker or synthesizer from manufacturing a deferral to avoid repair work, or whether the user must approve deferrals.
- **False-complete path:** A repair loop fails to converge. Opus synthesis decides the requirement is "intentionally deferred by policy" without user approval. The task closes. The run closes (per the missing run closure rule above). The requirement was never implemented and was never explicitly accepted as missing by a human.
- **Required correction:** Define in a new subsection (or extend Section 5.3): (a) deferrals must be recorded in a structured artifact (e.g., `deferrals.json` under the run directory); (b) each deferral must reference the requirement ID, the reason, and the approving authority; (c) only the user (via `attest approve` or a new `attest defer` command) may approve a deferral in v1; (d) the deterministic verifier must treat a deferred requirement as uncovered unless the deferral artifact exists and is user-approved.

### F-04 [high] Council verdict has no defined schema for "requires additional work" -- Opus synthesis is an unverifiable LLM judgment

- **Section:** 3.5 (council-result.json), 5.3 (Closure rule), 10.2 (Reviewer order)
- **Risk:** The closure rule requires that "Opus synthesis does not require additional work." But `council-result.json` defines only four loose fields: final task verdict, synthesized findings, disagreement summary, follow-up action. There is no enumerated verdict vocabulary (e.g., "pass", "fail", "repair_required", "defer"). Since the council result is an LLM-generated artifact, any implementation must parse a free-text field to decide whether the task may close. This is the opposite of deterministic gating.
- **False-complete path:** Opus synthesis produces a verdict that is ambiguously worded (e.g., "the implementation is largely complete with minor caveats"). The system interprets this as non-blocking because the word "fail" or "repair" does not appear. The task closes with unaddressed caveats.
- **Required correction:** Define an enumerated `verdict` field in `council-result.json` with a closed set of values (e.g., `pass`, `fail`, `repair_required`, `defer_to_user`). The closure rule in 5.3 must reference this field by value, not by interpretation. The Opus synthesis prompt must be required to emit one of these values. The system must treat any unrecognized verdict value as blocking.

### F-05 [high] No maximum repair loop depth or convergence bound

- **Section:** 11.1 (Repair triggers), 11.2 (Reopen vs child repair task), 9.2 (Escalation rules), 18 (Open v1 decisions)
- **Risk:** Section 9.2 says "Sonnet -> Opus when repair loops do not converge," and Section 18 lists "maximum retry counts before blocker state" as an open decision. But the repair reconciler (Section 11) has no loop bound at all. A task can cycle through `implementing -> verifying -> repair_pending -> implementing` indefinitely, consuming resources without progress. Escalation to Opus does not guarantee convergence either; the spec provides no rule for what happens after Opus also fails.
- **False-complete path:** This is not a false-complete risk per se, but it is a false-progress risk that masks non-convergence. More directly: after many cycles, the system may lower its standards implicitly (model fatigue, context drift) and accept marginal work. The spec provides no mechanism to detect or prevent this degradation.
- **Required correction:** Do not leave this as an open decision. Define in Section 11 (or a new 11.3): (a) a maximum repair attempt count per task (configurable, with a stated default); (b) when the limit is reached, the task transitions to `blocked` with a reason citing non-convergence; (c) blocked-by-non-convergence tasks require user intervention to resume or defer; (d) each repair attempt must be numbered and recorded for audit.

### F-06 [high] No requirement coverage map artifact -- traceability depends on per-task links without a cross-cutting view

- **Section:** 3.2 (run-artifact.json), 3.3 (tasks.json), 10.1 (Deterministic verifier), 13.1 (Required status fields)
- **Risk:** The run artifact contains a `requirements` field. Each task has `requirement_ids`. But the spec defines no artifact or verifier step that computes or persists the mapping from every requirement ID to the set of tasks that cover it. The status file includes an "uncovered requirement count" but does not define how it is computed or where the full mapping lives. Without a materialized coverage map, there is no way to deterministically verify that every requirement is assigned to at least one task, or that a repair task actually picks up a dropped requirement.
- **False-complete path:** During task compilation, a requirement ID is accidentally omitted from all tasks. The per-task verifier only checks that each task's linked requirements are covered -- it never checks that every run-level requirement appears somewhere. The uncovered requirement count in status is computed by an unspecified mechanism that may have a bug or rely on LLM judgment. The run completes with a missing requirement.
- **Required correction:** Add to Section 3 a `requirement-coverage.json` artifact (or equivalent section within `run-status.json`) that maps every requirement ID in the run artifact to its covering task IDs and their current status. The deterministic verifier (Section 10.1) must include a duty: "confirm every requirement ID in the approved run artifact is linked to at least one non-failed task." This check must run at task compilation time and again before run closure.

### F-07 [high] Codex and Gemini review inputs and prompts are unspecified -- reviewers could be given incomplete context

- **Section:** 8.2 (Codex backend), 8.3 (Gemini backend), 10.2 (Reviewer order)
- **Risk:** The spec says Codex runs "task implementation review" and Gemini runs "task verification review," but never specifies what inputs are provided to these reviewers. Do they receive the full run artifact? The completion report? The changed files? The requirement text? The verifier result? If the dispatcher sends only the completion report (which is self-reported by the implementer), the reviewers are reviewing the worker's claim, not the actual work.
- **False-complete path:** The dispatcher sends the completion report and the implementer's summary to Codex and Gemini. The summary says "all tests pass" and omits a file that was changed but broke an unrelated requirement. The reviewers have no reason to look deeper because they were not given the changed file list or the repo diff. They return non-blocking verdicts. The task closes.
- **Required correction:** Add to Sections 8.2 and 8.3 (or a new subsection under Section 10) a minimum input specification for each reviewer. At minimum: (a) the relevant requirement text from the run artifact; (b) the task scope; (c) the completion report; (d) the verifier result; (e) the actual changed files or diffs; (f) any test output. Mark which of these are mandatory inputs versus optional context.

### F-08 [high] Quorum policy is an open decision but the closure rule assumes strict unanimity

- **Section:** 5.3 (Closure rule), 18 (Open v1 decisions), 14.1 (Backend failures)
- **Risk:** Section 5.3 requires that Codex review is non-blocking AND Gemini review is non-blocking for task closure. Section 18 lists "whether final council requires strict unanimity or configurable quorum" as an open decision. Section 14.1 says "review timeout -> blocking review failure unless quorum policy allows downgrade." These three sections contradict each other. The closure rule is strict unanimity. The open decisions section suggests quorum might be configurable. The failure handling section implies a quorum downgrade path already exists. An implementer must choose one interpretation, and the wrong choice directly affects whether partial review can close tasks.
- **False-complete path:** Gemini CLI is unavailable (rate limited, binary missing, network issue). The implementer reads Section 14.1's quorum downgrade clause and implements a fallback that allows closure with only Codex review. This directly violates Section 5.3's closure rule but is arguably supported by Section 14.1. A task closes with only one independent reviewer.
- **Required correction:** Resolve this contradiction in the spec itself, not in implementation planning. Either (a) state that v1 requires strict unanimity and remove the quorum downgrade language from 14.1, making backend unavailability a hard blocker; or (b) define the quorum policy explicitly (e.g., "at least 2 of 3 reviewers must return non-blocking verdicts, and the missing reviewer must be recorded as unavailable, not as silent approval"). Remove this from the open decisions list.

### F-09 [medium] State machine transitions are not enumerated -- any state can potentially reach any other state

- **Section:** 5.1 (Run states), 5.2 (Task states)
- **Risk:** The spec lists valid states for runs and tasks but does not define which transitions are legal. For tasks, the expected happy path is `pending -> claimed -> implementing -> verifying -> under_review -> done`, but nothing prevents an implementation from allowing `pending -> done` or `implementing -> done` (skipping verification entirely). The closure rule in 5.3 constrains the `done` transition, but only if the implementation enforces it at the state machine level.
- **False-complete path:** A bug in the dispatcher or a race condition allows a task to transition directly from `implementing` to `done`, bypassing the `verifying` and `under_review` states. Since the state machine has no explicit transition table, there is no compile-time or test-time check that catches this.
- **Required correction:** Add a transition table to Section 5.2 that enumerates every legal (source_state, target_state, condition) triple. At minimum, enforce that `done` is reachable only from `under_review` and only when the closure rule in 5.3 is satisfied. Include this transition table as a testable artifact.

### F-10 [medium] No specification of how "deterministic" the task compiler actually is

- **Section:** 6.1 (Determinism boundary)
- **Risk:** Section 6.1 says "task compilation must be deterministic from the approved run artifact" and that "compiling tasks from it must produce stable task IDs and grouping under the same compiler version." This is a necessary condition for AT-TS-001 and AT-AS-001. However, the spec does not define what the compiler is. If the compiler uses an LLM call (e.g., Claude Opus for task decomposition, as suggested by the routing table in 9.1 where "spec analysis" routes to Opus), then determinism depends on LLM output being reproducible, which it is not. The spec appears to assume a non-LLM compiler but never states this explicitly.
- **False-complete path:** Not a direct false-complete risk, but a traceability risk. If the compiler is non-deterministic, different sessions may produce different task graphs from the same artifact. A repair cycle could silently change the task graph, orphaning previously verified work. The acceptance scenario AT-TS-001 would be untestable.
- **Required correction:** State explicitly whether the task compiler is (a) a purely algorithmic/rule-based transformation with no LLM calls, or (b) an LLM-assisted transformation that is expected to be seeded or cached for reproducibility. If (b), define the caching or seeding mechanism. If (a), confirm that the routing table entry for "spec analysis" (Opus) refers only to the preparation phase, not to the compilation phase.

### F-11 [medium] Repair task requirement linkage is unspecified -- child tasks may lose traceability

- **Section:** 11.2 (Reopen vs child repair task), 3.3 (tasks.json)
- **Risk:** When a child repair task is created, the spec says the parent task "remains mostly valid." The child task has `parent_task_id` and `created_from` fields but the spec does not say whether the child inherits the parent's `requirement_ids`, receives a subset, or gets new ones. If the child task does not carry the specific requirement IDs that the finding identified as uncovered, the requirement-to-task traceability chain is broken.
- **False-complete path:** A council finding says requirement REQ-042 is not adequately covered by parent task T-17. A child repair task T-17a is created. T-17a's `requirement_ids` is set to the parent's full list (or empty, or a generic "repair" tag). T-17a implements a fix but does not specifically address REQ-042. The deterministic verifier checks T-17a's linked requirements and finds them covered. REQ-042 was never re-verified because it was not explicitly linked to T-17a.
- **Required correction:** Add to Section 11.2: child repair tasks must carry the specific requirement IDs identified in the triggering finding. The repair reconciler must copy the uncovered requirement IDs from the finding into the child task's `requirement_ids` field. The verifier must confirm these specific IDs are covered.

### F-12 [medium] No artifact integrity or versioning for run-artifact.json after approval

- **Section:** 3.2 (run-artifact.json), 5.3 (Closure rule)
- **Risk:** The run artifact is approved once and then serves as the source of truth for the entire run. But the spec does not specify any mechanism to detect if the artifact is modified after approval. Since v1 is local-only (Section 15), a process bug, a concurrent write, or even a user edit could silently change the approved artifact mid-run, invalidating all verification that was performed against the original version.
- **False-complete path:** A bug in the repair reconciler writes an updated requirement list back to `run-artifact.json`, removing a requirement that was proving difficult. Subsequent verification passes because the requirement no longer exists in the artifact. The run completes with a silently narrowed scope.
- **Required correction:** Add to Section 3.2: the approved run artifact must include a content hash (e.g., SHA-256) recorded at approval time in a separate field or file. The deterministic verifier must confirm the artifact's content hash matches the approved hash before any verification step. Any mismatch must block the run.

### F-13 [medium] Completion report is self-reported by the implementer with no structural validation against reality

- **Section:** 3.5 (completion-report.json), 10.1 (Deterministic verifier)
- **Risk:** The completion report is produced by the worker (the implementer). It includes "changed files," "commands executed," "tests executed," and "known gaps." The deterministic verifier checks that the report exists and that claimed requirements appear in the evidence, but it does not independently verify any of the report's claims (e.g., that the listed files were actually changed, that the listed commands were actually run).
- **False-complete path:** A worker hallucinates or exaggerates its completion report. It claims to have run tests that it did not actually execute. It lists files as changed that it did not modify. The verifier sees a structurally complete report and passes. The LLM reviewers may or may not catch the discrepancy.
- **Required correction:** Add to Section 10.1: the verifier should cross-reference at least (a) changed files listed in the report against actual git diff or filesystem state; (b) test commands listed against actual execution logs captured by the dispatcher. Define which fields in the completion report are independently verifiable versus self-attested, and require independent verification for the verifiable ones.

### F-14 [medium] Opus synthesis can silently overrule blocking reviewers

- **Section:** 10.3 (Blocking policy), 5.3 (Closure rule)
- **Risk:** Section 5.3 allows closure if "Opus synthesis does not require additional work." Section 10.3 lists "high-confidence reviewer objection not reconciled by Opus synthesis" as blocking. The word "reconciled" is doing heavy lifting here. The spec does not define what constitutes valid reconciliation versus dismissal. Opus could "reconcile" a blocking finding by explaining it away rather than by producing evidence or repair work.
- **False-complete path:** Codex flags a blocking finding: "missing null-input handling for requirement REQ-012." Opus synthesis writes: "The existing validation layer handles null inputs implicitly through the type system; the Codex concern is reconciled." The system treats this as non-blocking because Opus has "reconciled" the objection. No repair task is created. The null-input handling is not actually present.
- **Required correction:** Define in Section 10.3 what constitutes valid reconciliation. At minimum: (a) reconciliation must produce a specific evidence reference (file, test, line number) that addresses the finding; (b) if no evidence reference is provided, the finding remains blocking; (c) consider requiring that any reconciliation of a high-confidence blocking finding must either produce a repair task or be flagged for user review.

### F-15 [low] No version pinning for reviewer backends

- **Section:** 8.2 (Codex backend), 8.3 (Gemini backend)
- **Risk:** The spec does not specify whether Codex and Gemini model versions are pinned or recorded per review. If the underlying model changes mid-run (provider-side update), the same review prompt could produce different results on a retry, making repair loops non-reproducible and audit trails unreliable.
- **False-complete path:** Not a direct false-complete risk, but a reproducibility and audit risk.
- **Required correction:** Add to Section 9.3 (Routing traceability): each review attempt must record the model version string returned by the backend, if available. This is a reporting requirement, not a pinning requirement, since v1 cannot control provider-side updates.

### F-16 [low] AT-FR-023 council checkpoints are broader than what the tech spec implements

- **Section:** Technical spec Section 10.2, Functional spec AT-FR-023
- **Risk:** AT-FR-023 requires council checkpoints "at least for spec review, task review, task verification, and final run acceptance." The technical spec only specifies per-task review (Section 10.2) and does not describe a spec-level council checkpoint (during preparation) or a run-level final acceptance council. This is a functional requirement that the technical spec does not fully cover.
- **False-complete path:** Not directly exploitable for false completion, but the absence of a run-level final acceptance council means there is no cross-task synthesis step before the run closes -- only per-task reviews.
- **Required correction:** Add to Section 10 (or a new subsection) a specification for run-level council checkpoints, at minimum a final acceptance council that runs after all tasks reach terminal states and before the run transitions to `completed`.

---

## Traceability Risks

1. **Requirement-to-task mapping is implicit, not materialized.** There is no artifact that shows the full coverage map from requirement IDs to tasks. Traceability depends on the union of per-task `requirement_ids` fields, which is fragile and not verified at the run level (F-06).

2. **Repair tasks may break the traceability chain.** When a child repair task is created, the spec does not mandate that the specific uncovered requirement IDs from the finding are carried forward. A repair task could satisfy its own linked requirements without addressing the original gap (F-11).

3. **Deferral is a traceability escape hatch with no controls.** The phrase "intentionally deferred by policy" in the closure rule allows any requirement to be bypassed without a defined approval mechanism or audit trail (F-03).

4. **The task compiler's determinism claim is unverifiable without knowing whether it uses LLM calls.** If the compiler is non-deterministic, the mapping from artifact to tasks is not reproducible, which undermines traceability across sessions (F-10).

5. **Run-artifact.json can drift after approval.** Without integrity checking, the source of truth for all traceability can silently change mid-run (F-12).

6. **No cross-task regression check.** A late task can invalidate an early task's verified work without triggering re-verification. The traceability chain shows REQ-X as covered by Task-A (done), but Task-B broke Task-A's implementation after it closed.

---

## Verification Gaps

1. **The deterministic verifier checks structure, not substance.** It confirms that evidence exists but does not confirm that evidence supports the claim. Test exit codes, actual file changes, and actual command execution are not verified deterministically (F-02, F-13).

2. **Council verdict parsing is undefined.** The system relies on interpreting free-text LLM output to decide whether a task may close. There is no enumerated verdict field, making the council gate non-deterministic in practice (F-04).

3. **No run-level verification gate.** Per-task verification is specified (if incomplete in substance). Run-level verification -- confirming that all requirements across all tasks are covered -- is not specified at all (F-01).

4. **Reviewer inputs are unspecified.** The quality of LLM reviews depends entirely on what context is provided, but the spec does not define what inputs the reviewers receive. Self-reported completion reports may be the only input (F-07).

5. **State transitions are not constrained.** Without an explicit transition table, the verification pipeline can be bypassed by invalid state transitions that skip the `verifying` or `under_review` states (F-09).

6. **No maximum repair loop bound in the spec itself.** The system can cycle indefinitely without converging, and the open decision in Section 18 defers the safeguard to implementation time (F-05).

7. **Opus can "reconcile" blocking findings without evidence.** The spec defines reconciliation as a concept but does not require that reconciliation be backed by evidence references, creating a path for Opus to dismiss legitimate blocking findings (F-14).

8. **Quorum contradiction.** The closure rule, failure handling, and open decisions sections give contradictory guidance on whether all reviewers are mandatory, creating ambiguity about whether partial review can close a task (F-08).

---

## What Is Strong

1. **The core closure rule (Section 5.3) is the right shape.** Requiring deterministic verification AND two independent model reviews AND Opus synthesis before task closure is a strong multi-gate design. The problems are in the details of each gate, not in the architecture of requiring all four.

2. **The fail-closed policy (Section 7.3, 14.2) is correctly oriented.** Workers fail closed. Timeouts, malformed output, and missing reports all result in verification failure, not silent success. The conservative recovery principle ("when unsure, keep work open") is the right default.

3. **Routing traceability (Section 9.3) is well-specified.** Recording backend, model tier, escalation cause, and retry count for every attempt provides a solid audit trail for understanding how decisions were made.

4. **Reattachment from durable state (Section 12.2) is correctly designed.** Requiring that reattachment works from repo-local state alone, without the original transcript or process state, makes the system resilient to session loss and directly supports the detached execution model.

5. **The separation of deterministic verification from LLM review (Section 10.2) is architecturally sound.** Running the deterministic verifier first and only then invoking LLM reviewers means structural failures are caught cheaply before expensive model calls. The ordering is correct.

6. **The escalation rules (Section 9.2) address the right failure modes.** Escalating from Sonnet to Opus on repeated verifier failure, non-convergent repair loops, and architectural impact tasks provides a sensible resource allocation model.

7. **Acceptance scenarios (Section 17) directly test the most important properties.** AT-TS-003 (no evidence, no closure), AT-TS-004/005 (reviewer veto power), and AT-TS-007 (reattachment from state alone) are the right things to test. The gap is that they do not test run-level closure or requirement coverage completeness.

8. **The claim lease model (Section 7) is well-defined.** Single-owner exclusivity, heartbeat-based expiry, and scope reservations provide a solid foundation for preventing overlapping work.

9. **Multi-model disagreement as a first-class signal (Section 1.3, 6.6).** Using Codex and Gemini as mandatory independent reviewers from different model families is a structurally sound approach to preventing single-model blind spots. The design correctly treats disagreement as information rather than noise.

10. **The product thesis (Functional Spec 1.2) correctly identifies the core problem.** Targeting false closure rather than planning quality is the right focus. The spec's overall architecture is oriented around making false completion harder, which is the correct design center.
