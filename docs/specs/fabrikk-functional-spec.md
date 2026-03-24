# fabrikk Functional Specification

Version: 0.5
Status: Draft
Audience: Product, engineering, design, founder
Document type: Functional specification
Companion document: [attest-technical-spec.md](./attest-technical-spec.md)
Research reference: [agentops-learnings.md](../research/agentops-learnings.md)

---

## 1. Overview

### 1.1 Product summary

`attest` is a spec-driven autonomous run system for coding agents. Written in Go — stdlib only, single binary.

It takes a technical specification, runs a rigid plan review, dispatches implementation to parallel sub-agents, enforces code hygiene through automated checks, and compares the implementation against the specification through independent multi-model council review. At the end, it delivers a complete and well-tested solution — or a clear accounting of what remains open and why.

### 1.2 The problem

Coding agents are powerful, but they struggle to deliver complete, spec-conformant implementations without constant human oversight. The failure mode is not lack of capability — it is lack of structure:

- **Drift.** Agents wander from the original spec, building things that were not asked for while leaving actual requirements unfinished.
- **False completion.** Agents claim work is done when it is not. Tests are missing, edge cases are ignored, requirements are quietly dropped.
- **Lost focus.** In skill-based or prompt-driven workflows, the LLM has too much latitude. It decides what to do next, which stages to skip, and when "good enough" is good enough. The result is inconsistent quality and incomplete implementations.

These problems get worse with parallelism. Multiple agents working concurrently multiply the opportunities for drift and conflicting changes.

### 1.3 Prior art

AgentOps (skill-based agent orchestration) demonstrates that coding agents can perform plan decomposition, parallel implementation via swarm patterns, multi-persona code review, and structured pre-mortem analysis. Many of these capabilities work well.

However, the skill-based approach gives the LLM substantial room for decision-making and direction changes. Skills are prompts, not enforced protocol. This leads to incomplete implementations that require repeated human nudging — gap analysis requests, re-reviews, and manual course corrections.

`attest` takes a different approach: the orchestration skeleton is coded in Go, not expressed as LLM prompts. Agents are called into well-defined slots within a rigid pipeline. They are autonomous within their stage, but they do not control the process.

Key patterns from AgentOps that `attest` codifies in Go:

- **Verify then trust.** Agent completion claims are never accepted at face value. Every result is validated mechanically before being accepted.
- **Fresh workers per wave.** Sub-agents are ephemeral — spawned for one wave, torn down after. The orchestrator persists across waves and manages all state.
- **Disk-backed results.** Workers write results to disk files, not conversation messages. This prevents context explosion from N workers.
- **File ownership enforcement.** Pre-spawn conflict detection prevents concurrent edits to the same file.
- **Conformance checks per task.** Every task carries its own mechanically verifiable exit criteria.

### 1.4 Core approach: rigid skeleton with convergence-driven loops

`attest` combines two ideas:

**Rigid skeleton.** The pipeline stages are fixed and coded:

```
Spec Review → Work Plan → Implementation → Hygiene Gate → Council Review
```

No agent gets to skip a stage, invent a new one, or decide the workflow. Stage boundaries and exit criteria are enforced by the orchestrator in Go.

**Convergence-driven loops.** The loops within each stage are autonomous and self-correcting. They do not run a fixed number of times — they run until their exit criteria are met:

- Implementation nudge reveals gaps? Keep going.
- Code review finds critical or medium findings? Keep going.
- Same issues reappearing without progress? Escalate to a stronger reasoning path.

The approved spec is the anchor. Every loop iteration checks against the same approved requirements, not whatever the agent has drifted toward. Work is only complete when it traces back to approved requirements, passes the hygiene gate, and clears independent council review.

A global safety limit (maximum wave count) prevents infinite loops on circular dependencies or cascading failures. This does not contradict convergence-driven loops — it is the escalation backstop.

### 1.5 Core promise

`attest` does not promise perfect implementation. It promises a stronger operational contract:

- every task must trace to stable requirement IDs
- every completion claim is verified mechanically — agent self-reports are never trusted
- every council review compares implementation against the original spec, not the agent's self-assessment
- convergence loops run until exit criteria are met, not for a fixed number of rounds
- a run does not finish because one agent says "done"

## 2. Goals

### 2.1 Primary goals

1. Turn approved spec files into a durable, traceable execution contract.
2. Enforce a rigid pipeline where agents work within coded stages, not self-directed workflows.
3. Run convergence-driven loops that continue until exit criteria are met, with escalation on stall.
4. Prevent overlapping multi-agent work through file ownership enforcement and lease-based claims.
5. Use coded hygiene checks (lint, format, test, security) as a mandatory gate before council review.
6. Use multi-model council review as the substantive quality gate for spec conformance, bugs, and edge cases.
7. Lower unattended failure rates by automatically opening repair work when verification or review fails.
8. Verify then trust — never accept agent completion claims without mechanical validation.

### 2.2 Secondary goals

1. Make run state resumable — a crashed or interrupted run picks up where it left off, not from scratch.
2. Keep users inside familiar agent CLI workflows instead of forcing a new front-end.
3. Use model routing (Opus, Sonnet, Haiku) to optimize for speed and cost without sacrificing correctness.
4. Keep the product small enough that an individual developer can run it locally.

### 2.3 Future goals (post-v1)

1. Support detached background execution that continues after the initiating agent session ends.
2. Support remote/server execution.
3. Add Gemini as a council reviewer when capacity and rate-limit issues are resolved.

## 3. Non-goals

v1 will not:

- replace Claude Code, Codex CLI, or Gemini CLI as the user's primary interface
- be a generic issue tracker
- require Beads, Linear, or another external tracker as the source of truth
- guarantee deterministic planning directly from arbitrary prose without a user approval checkpoint
- guarantee perfect correctness against every spec ambiguity
- act as a general workflow engine for non-coding tasks
- hardcode iteration counts for convergence loops — exit criteria drive convergence, not counters

## 4. Personas

### 4.1 Primary persona: spec-first solo builder

- writes detailed functional and technical specs before implementation
- already works inside Claude Code or similar tools
- wants to approve the direction once, then walk away
- expects returned work to be materially correct against the spec
- is frustrated by having to nudge agents repeatedly for gap analysis and completion

### 4.2 Secondary persona: technical lead running several agents

- needs multi-agent execution without accidental overlap
- cares about exact status, blockers, and requirement coverage
- wants an auditable path from source spec to completed code

### 4.3 Tertiary persona: quality-focused reviewer

- trusts model disagreement as a useful signal
- wants independent reviewers involved even when Claude is the main implementer
- cares more about correctness than minimal token cost

## 5. Product boundaries

### 5.1 Included in v1

- spec ingestion from one or more files
- spec normalization into a template format with stable requirement IDs and scope mode
- explicit approval boundary before autonomous execution begins
- deterministic task compilation with file-conflict detection, conformance checks, and test level classification per task
- built-in lightweight task tracking (tasks, epics/waves, claims, releases)
- parallel implementation via Claude CLI sub-processes sharing a worktree
- optional test-first mode (SPEC → TEST → RED gate → GREEN implementation)
- mandatory self-review gate baked into every worker prompt (SCAN, REVIEW, RUN)
- verify-then-trust validation of all worker results
- convergence-driven implementation loops with escalation
- coded hygiene gate (lint, format, test, security scan) with action tier classification
- council review with auto-selected intensity (quick/standard/deep) and structured 8-category review checklist
- pluggable domain-specific review checklists (SQL safety, concurrency, LLM trust boundaries)
- deep audit escalation when convergence loops stall (parallel explorers → adjudication)
- Codex as independent code reviewer with context budget rule
- Opus synthesis and final council decisions with structured remediation fields
- repair task creation from council findings routed back to implementation
- resumable run state with wave checkpoints — restart picks up where it left off
- run status inspection

### 5.2 Excluded from v1

- detached background execution (daemon mode)
- Gemini as council reviewer (future, pending capacity resolution)
- SaaS-hosted orchestration backend
- web dashboard
- org-wide policy administration
- direct code editing by Codex or Gemini as first-class implementers
- full scheduling or queue management across many machines

## 6. Core concepts

### 6.1 Task tracking

`attest` includes a built-in lightweight task tracker. This is a simplified, less invasive alternative to tools like Beads. It tracks tasks, epics (waves), and related work items, and supports claims and releases for parallel workers.

Task tracking is file-based and lives in the project-scoped `.tickets/` directory as markdown files with YAML frontmatter. Each run creates an epic ticket; tasks are children scoped via the `parent` field. The ticket store (`internal/ticket`) implements the `TaskStore` and `ClaimableStore` interfaces. It does not require git hooks, external services, or a separate CLI. The orchestrator reads and writes task state directly as part of the pipeline.

### 6.2 Source specs

Source specs are human-authored input files. They may be in any format. During spec review, `attest` normalizes them into the run artifact template format with stable requirement identifiers.

### 6.3 Run artifact

The run artifact is the approved normalized interpretation of the source specs. It is the execution contract for a specific run.

The run artifact contains:

- normalized requirements with stable IDs
- source references
- clarified assumptions
- dependencies
- risk classification
- initial task compilation inputs
- model-routing hints
- **scope mode** — one of: EXPANSION (greenfield), HOLD SCOPE (bug fix, maximum rigor), or REDUCTION (large scope, minimize to MVP). The scope mode is committed at approval time and enforced throughout the pipeline. An agent cannot silently expand scope if the committed mode is HOLD SCOPE.
- **boundaries** — three categories:
  - *Always:* non-negotiable requirements, injected as cross-cutting constraints on every task
  - *Ask First:* decisions needing human input, the orchestrator blocks on these
  - *Never:* explicit out-of-scope items, the orchestrator rejects work on these

### 6.4 Task graph and the ready front

The task graph is the decomposition of the run artifact into small isolated work items grouped into waves (epics).

Waves are not manually declared. They are computed from the dependency DAG using the **ready front model**: the ready front is the set of tasks with all dependencies satisfied. As tasks complete, blocked work automatically becomes ready, forming the next wave. This creates natural wave structure without manual phase declaration.

The initial graph compiles deterministically from the approved run artifact. The graph may evolve during execution when hygiene gate failures or council findings create repair tasks.

**Task sizing heuristics:**

- 1-2 independent files → 1 task
- 3+ independent files → split into sub-tasks (one per file group)
- Shared files between tasks → serialize (different waves) or merge (single task)

### 6.5 File-conflict matrix

Before wave assignment, the compiler builds a file-conflict matrix:

| File | Tasks |
|------|-------|
| `src/auth.go` | Task 1, Task 3 | ← CONFLICT |
| `src/config.go` | Task 2 | |

Tasks that touch the same file cannot be in the same wave. The compiler either serializes them (moves one to a later wave) or merges them (combines into a single task).

A cross-wave shared files registry tracks files that appear in multiple waves. Later waves must see changes from earlier waves — the orchestrator refreshes the base SHA between waves to ensure this.

### 6.6 Conformance checks

Every task carries mechanically verifiable conformance checks in its metadata:

| Acceptance Criteria | Conformance Check |
|---|---|
| "File X exists" | `files_exist: ["X"]` |
| "Function Y is implemented" | `content_check: {file: "src/foo.go", pattern: "func Y"}` |
| "Tests pass" | `tests: "go test ./..."` |
| "Lint passes" | `lint: "golangci-lint run ./..."` |

Rules:

- Every task MUST have at least one conformance check.
- If acceptance criteria cannot be mechanically verified → flag as underspecified during work plan review.
- Conformance checks feed both the worker's self-review gate and the hygiene gate.

### 6.7 Test level classification

Each task specifies which test levels are required, forcing agents to think about *what kind* of test rather than vaguely "write tests":

| Level | Name | When Required | What It Tests |
|-------|------|---------------|---------------|
| L0 | Contract | Task touches API boundaries or external interfaces | Does this satisfy the API contract? |
| L1 | Unit | Every feature/bug task | Does this function work in isolation? |
| L2 | Integration | Task crosses module boundaries | Do these components work together? |
| L3 | Component | Task affects subsystem workflows | Does the subsystem work end-to-end? |

The work plan assigns required test levels to each task. Workers must produce tests at each required level. The self-review gate verifies test presence. The hygiene gate runs the tests.

Tasks that are purely documentation, configuration, or chore work are explicitly marked as test-exempt — they skip test level requirements but still require structural conformance checks.

### 6.8 Baseline audit

Before the work plan is finalized, the orchestrator runs quantitative baseline commands against the codebase:

```bash
wc -l internal/engine/*.go
grep -c "func Test" internal/engine/*_test.go
ls internal/compiler/*.go | wc -l
```

Commands and results are recorded in the run artifact. After implementation, the same commands can be re-run to verify progress. This makes completion measurable, not subjective.

### 6.9 Claim lease

A claim lease is the exclusive right of one worker to own a task for a bounded period of time. Claims prevent overlapping work on the same task.

The orchestrator pre-assigns tasks to workers BEFORE spawning them. Workers do not race-claim tasks. Each worker only transitions its pre-assigned task from in-progress to completed.

### 6.10 Convergence loop

A convergence loop is the self-correcting mechanism within a pipeline stage. It repeats until the stage's exit criteria are met, not for a fixed number of iterations.

Exit criteria are stage-specific:

- **Implementation:** self-review gate passes (SCAN clean, REVIEW clean, RUN passes), no known gaps remain
- **Hygiene gate:** all coded checks pass (lint, format, test, security), conformance checks pass
- **Council review:** no critical, high, or medium findings remain open

If a loop is not converging — the same issues reappear, the agent produces the same broken output — the system escalates to a stronger reasoning path (e.g., Sonnet to Opus) before continuing.

A global safety limit (default: 50 waves maximum) prevents infinite loops.

### 6.11 Self-review gate

The self-review gate is baked into every worker's prompt. It is the primary nudging mechanism — workers must complete it before claiming completion. The gate has three mandatory steps:

**Step 1 — SCAN** for incomplete markers in newly added lines:

```bash
git diff HEAD | grep '^+' | grep -v '^+++' | grep -inE \
  'TODO|FIXME|HACK|XXX|PLACEHOLDER|UNIMPLEMENTED|STUB|WIP|NOCOMMIT'
```

Any match → the worker is NOT done and must fix before proceeding.

**Step 2 — REVIEW** — brutally honest self-assessment of every modified file:

- What changed and why
- Gaps or incomplete implementations
- Missing error handling or edge cases
- Missing tests for new behavior
- Code that would be flagged in someone else's PR

If issues found → fix before proceeding. Do not rationalize them away.

**Step 3 — RUN** verification using the task's conformance checks:

- Execute test/lint/build commands from the task metadata
- Confirm exit code 0

Only after all 3 steps pass may the worker write its result file.

The orchestrator may stream additional nudge prompts if the self-review evidence is missing, incomplete, or shows failures. The number of additional nudges is not fixed — the orchestrator continues until exit criteria are met or escalation is triggered.

### 6.12 Sub-agent execution

Sub-agents are Claude CLI sub-processes spawned by the orchestrator. Each sub-agent runs as a separate process with streaming enabled, sharing the same git worktree.

**Fresh workers per wave.** Each wave gets a new set of workers. Workers from the previous wave are torn down. This prevents context accumulation across waves (the "Ralph pattern"). The orchestrator persists across waves; workers are ephemeral.

**Disk-backed results.** Workers write results to JSON files on disk, not in conversation messages. Messages to the orchestrator must be minimal (under 100 tokens — verdict and file path only). This prevents N workers from flooding the orchestrator's context.

**Result file format:**

```json
{
  "type": "completion",
  "issue_id": "<task-id>",
  "status": "done",
  "detail": "<one-line summary>",
  "artifacts": ["path/to/file1", "path/to/file2"],
  "self_review": {
    "scan_clean": true,
    "review": "<written review text>",
    "files_reviewed": ["path/a", "path/b"],
    "verification_command": "go test ./...",
    "verification_exit_code": 0
  }
}
```

**File ownership enforcement.** Before spawning workers, the orchestrator builds a file ownership map from task metadata:

```
File Ownership Map (Wave 3):
┌──────────────────┬────────┬──────────┐
│ File             │ Owner  │ Conflict │
├──────────────────┼────────┼──────────┤
│ src/auth.go      │ task-1 │          │
│ src/config.go    │ task-2 │          │
└──────────────────┴────────┴──────────┘
```

Workers are instructed: "Do NOT modify files outside your manifest." If a worker needs files outside its manifest, it writes a blocked result and the orchestrator creates a follow-up task.

**Git commit policy.** Workers MUST NOT commit. The orchestrator is the sole committer. This prevents concurrent git index corruption. Workers write files only; the orchestrator commits per-wave or per-task as configured.

**Verify then trust.** After a worker signals completion, the orchestrator validates:

1. Self-review evidence exists and passes (SCAN clean, REVIEW present, RUN exit code 0)
2. Conformance checks from task metadata pass (files_exist, content_check, tests, lint)
3. On pass → mark task completed
4. On fail → send specific failure back to worker, increment retry count
5. Max 3 retries → mark task blocked, escalate

**Base-SHA refresh.** After each wave commits, the orchestrator updates the base SHA. Workers in the next wave see changes from all previous waves.

### 6.13 Wave acceptance check

After all workers in a wave complete, before advancing to the next wave:

1. **Completeness scan.** Grep newly added lines for TODO/FIXME/STUB markers. Any found → FAIL.
2. **Lightweight judges.** Two Haiku-tier agents check spec compliance and error paths. Each returns PASS/WARN/FAIL.
3. **Aggregate verdict.** Both PASS → advance. Any FAIL → create fix tasks. WARN after 2 fix attempts → FAIL.

This is a quick per-wave check, not the full hygiene gate or council review. It catches obvious issues early and cheaply.

### 6.14 Wave checkpoint

After each wave, the orchestrator writes a checkpoint to disk:

```json
{
  "wave": 3,
  "timestamp": "2026-03-19T14:30:00Z",
  "tasks_completed": ["task-1", "task-2"],
  "tasks_failed": ["task-3"],
  "files_changed": ["file1.go", "file2.go"],
  "git_sha": "abc123",
  "acceptance_verdict": "PASS"
}
```

This enables resumability. If the process dies, restart reads the last checkpoint and knows which wave to resume from.

### 6.15 Ratchet

Progress is permanent. Completed waves cannot be un-done.

The ratchet model: `Progress = Chaos × Filter → Ratchet`. Multiple implementation attempts (chaos) pass through validation gates (filter) and are locked as permanent progress (ratchet). Repair tasks from later reviews create new tasks in new waves — they do not re-open completed waves.

### 6.16 Hygiene gate

The hygiene gate is the coded, deterministic quality check that runs after implementation and before council review. It enforces basic code quality so that council reviewers can focus on substantive concerns.

The hygiene gate runs the project's own tooling. For a Go project, this typically includes:

| Tool | What it checks | Confidence |
|------|---------------|------------|
| gofumpt | Formatting | 1.0 |
| golangci-lint | 25+ linter rules | 1.0 |
| go test -race | Test execution with race detector | 1.0 |
| gosec | Go security patterns | 1.0 |
| gitleaks | Secrets in code | 1.0 |
| trivy | Dependency vulnerabilities | 1.0 |

All checks are deterministic (confidence 1.0). If any fail, the task bounces back to implementation automatically with specific failure details. No council review tokens are spent on code that fails basic hygiene.

The hygiene gate also verifies:

- Linked requirement IDs are present in the code or commit
- Task conformance checks pass (files_exist, content_check, tests, lint)
- No incomplete markers remain (TODO/FIXME/STUB in new code)

**Action tier classification.** Hygiene gate findings are classified mechanically:

- **Must Fix:** deterministic tool failure (confidence 1.0) → task bounces back, no exceptions
- **Suppressed:** findings matching the project's suppression list are logged but do not block

Projects may provide a suppression file for known false positives that should not trigger repair work.

### 6.17 Council review modes

The council runs at multiple pipeline checkpoints (spec review, work plan review, code review). Not all checkpoints need the same rigor. The orchestrator auto-selects review intensity based on scope:

| Mode | Judges | When Used |
|------|--------|-----------|
| **Quick** | 1 (inline, no spawning) | Work plan review, small task verification. ~10% cost. |
| **Standard** | 2 (independent judges) | Default code review, spec review for small specs. |
| **Deep** | 3-4 (specialized perspectives) | Large wave code review, high-stakes spec review, final run acceptance. |

The mode is auto-selected based on scope (number of files, task count, pipeline stage), but can be overridden. Escalation on stall also triggers mode upgrade (e.g., standard → deep when the convergence loop isn't making progress).

### 6.18 Council

The council is the independent multi-model review stage. The reviewer set is pluggable. In v1:

- **Codex** as independent code and test reviewer
- **Claude Opus** as synthesizer, spec-conformance reviewer, and final decision-maker

**Context budget rule.** Reviewers write ALL analysis to files. Messages to the orchestrator are MINIMAL — verdict, confidence, and file path only. The orchestrator reads reviewer output files during synthesis. This prevents N reviewers from flooding the orchestrator's context.

**Consensus verdict computation.** Simple, deterministic rules (matches AT-FR-071):

- **PASS:** All reviewers PASS
- **WARN:** Any reviewer WARN, none FAIL
- **FAIL:** Any reviewer FAIL

**Named perspectives.** Each reviewer gets a specific perspective injected into their prompt:

- Codex: code quality, test coverage, error paths
- Opus: spec conformance, architectural coherence, requirement traceability

**Structured review checklist.** Council reviewers receive a mandatory 8-category checklist to prevent satisfaction bias (stopping at ~10 findings regardless of actual issue count):

| Category | What to Check |
|----------|--------------|
| Resource Leaks | Unclosed files/connections, missing defer, goroutine leaks |
| Input Safety | Input validation, format string injection, path traversal, SQL injection |
| Dead Code | Unreachable branches, unused imports/variables/functions |
| Hardcoded Values | Magic numbers, hardcoded paths/URLs/credentials |
| Edge Cases | Nil/zero handling, empty collections, boundaries, integer overflow |
| Concurrency | Data races, missing locks, lock ordering, channel misuse |
| Error Handling | Swallowed errors, generic catch-all, missing propagation |
| Security | XSS, path traversal, CORS, CSRF, SSRF, rate limiting |

Reviewers must either report at least one finding per category or explicitly certify the category as clean. This forces comprehensive coverage rather than stopping when "enough" findings are found.

**Domain-specific checklists.** The project configuration or run artifact can specify additional domain checklists that apply. The orchestrator detects code patterns and includes relevant checklists in the council prompt:

| Trigger | Checklist |
|---------|-----------|
| SQL/ORM code detected | SQL safety checklist (injection, migration safety, N+1, transactions) |
| LLM/AI code detected | LLM trust boundary checklist (prompt injection, output validation, cost control) |
| Concurrent code detected | Race condition checklist (shared state, file races, database races) |

Domain checklists are pluggable — projects can add their own.

**Adjudication mode.** If the hygiene gate produced findings, they are passed to the council as pre-existing context. Reviewers confirm, reject, or reclassify severity rather than rediscovering the same issues.

**Deep audit escalation.** When a convergence loop is not making progress (same findings reappearing across review rounds), the orchestrator escalates to deep audit mode: parallel explorer agents sweep the codebase per-file before the council judges run. Explorers use the 8-category checklist. Judges then operate in adjudication mode over the explorer findings. This finds ~3x more issues than a single-pass review.

**Structured remediation fields.** Every council finding MUST include:

- `fix` — specific action to resolve
- `why` — root cause or rationale
- `ref` — file path, spec anchor, or requirement ID

Findings without these fields get fallback values. This ensures all findings are actionable and can be converted to repair tasks mechanically.

**Finding classification.** Council findings are classified mechanically:

- **Must Fix:** (critical OR high severity) AND confidence >= 0.80 → creates repair task
- **Should Fix:** medium severity OR (high with confidence 0.65-0.79) → creates repair task
- **Consider:** everything else above 0.65 confidence floor → logged, does not block

**Repair task creation.** Findings classified as "must fix" or "should fix" automatically produce repair tasks with the finding as context. These tasks enter the task graph and go through the full pipeline (implementation → hygiene gate → council review).

Future reviewers (e.g., Gemini for edge-case and spec review) can be added to the council without changing the pipeline structure.

### 6.19 Test-first mode (optional)

When enabled, test-first mode adds two pre-implementation phases to force genuine test coverage:

1. **SPEC wave.** Workers generate test contracts (invariants + test cases) from task descriptions. Output: contract files with `## Invariants` and `## Test Cases` sections.

2. **TEST wave.** Workers write failing tests from the contracts. Workers receive the contract but NOT the implementation code. Output: test files committed to the repository.

3. **RED gate.** The orchestrator runs the test suite. ALL new tests must FAIL against the current codebase. If a test passes, it tests existing behavior (not new requirements) and is worthless. Tests that pass are flagged for rewrite.

4. **GREEN wave.** Standard implementation wave. Workers receive the failing tests (immutable — workers MUST NOT modify test files) and implement to make them pass.

**Why this matters:** Test-first mode prevents two forms of false completion:
- Workers claiming "I wrote tests" when the tests don't actually test the new behavior (caught by RED gate)
- Workers making tests pass by weakening the tests rather than implementing correctly (prevented by immutable test files)

Test-first mode is not the default — it is enabled per-run or per-task. It is most valuable for feature tasks and bug fixes where test coverage is critical. Documentation, configuration, and chore tasks skip it.

## 7. High-level workflow

### 7.1 Spec review and approval

1. User provides one or more source spec files in any format.
2. `attest` normalizes the specs into the run artifact template with stable requirement IDs.
3. `attest` classifies scope mode (EXPANSION, HOLD SCOPE, or REDUCTION) and extracts boundaries (Always, Ask First, Never).
4. `attest` asks clarification questions if needed.
5. `attest` presents the normalized run artifact with scope mode and boundaries.
6. User reviews and approves the artifact. The scope mode is committed.

### 7.2 Work plan generation

7. `attest` runs a baseline audit — quantitative measurements of the current codebase state.
8. `attest` compiles the approved artifact into a task graph using the ready front model.
9. The compiler builds a file-conflict matrix and resolves conflicts (serialize or merge).
10. Each task carries: requirement IDs, scope metadata (owned/readonly/shared paths), conformance checks, and the "Always" boundary constraints.

### 7.3 Work plan review

11. The council reviews the work plan before implementation begins.
12. The review validates: full requirement coverage, no scope overlaps within waves, correct wave ordering and dependencies, appropriate task sizing, and that every task has at least one mechanically verifiable conformance check.
13. False dependencies are identified and removed (dependencies that are only "logical ordering" without actual file or data dependencies reduce parallelism).
14. If the review finds issues, the work plan is revised before proceeding.

### 7.4 Implementation (test-first mode, when enabled)

15. **SPEC wave:** Workers generate test contracts from task descriptions (invariants + test cases).
16. **TEST wave:** Workers write failing tests from contracts (no access to implementation code).
17. **RED gate:** Orchestrator runs test suite — all new tests must FAIL. Passes indicate tests that don't test new behavior.

### 7.5 Implementation

18. For each wave, `attest` builds a file ownership map, verifies no conflicts, and spawns Claude CLI sub-processes — one per task, sharing the same worktree. In test-first mode, workers receive immutable failing tests and must implement to make them pass.
19. Each worker receives a structured prompt: task assignment, file manifest, required test levels, conformance checks, cross-cutting constraints, and the mandatory self-review gate (SCAN, REVIEW, RUN).
20. Workers implement, self-review, and write result JSON files to disk.
21. The orchestrator validates each result: checks self-review evidence, runs conformance checks, verifies required test levels are covered. Failed validation → sends specific failure back to the worker (max 3 retries).
22. After all wave tasks complete, the wave acceptance check runs (completeness scan, lightweight judges).
23. The orchestrator commits the wave's changes and writes a wave checkpoint.
24. Process repeats for next wave (fresh workers, updated base SHA).

### 7.6 Hygiene gate

25. Coded checks run using the project's own tooling: lint, format, test, security scan.
26. Conformance checks from task metadata are verified.
27. Required test level coverage is checked — missing levels bounce back.
28. Incomplete markers (TODO/FIXME/STUB) in new code are flagged.
29. If any check fails, the task returns to the implementation loop with specific failure details and action tier classification.

### 7.7 Council review

30. The orchestrator auto-selects review intensity (quick/standard/deep) based on scope.
31. Hygiene gate findings (if any were suppressed or advisory) are passed to the council as pre-existing context (adjudication mode).
32. Reviewers receive the mandatory 8-category checklist and any applicable domain-specific checklists.
33. Codex reviews the implementation for code quality, test coverage, error paths, and correctness.
34. Opus synthesizes findings and compares implementation against the approved spec requirements, checking requirement traceability.
35. Each finding includes structured remediation fields (fix, why, ref) and is classified by action tier (must fix, should fix, consider).
36. "Must fix" and "should fix" findings create repair tasks routed back to implementation.
37. If the convergence loop stalls (same findings reappearing), the orchestrator escalates to deep audit: parallel explorers sweep per-file, then judges adjudicate.
38. The loop repeats until all requirements are verified or a true blocker is reached.

### 7.8 Run resumption

39. If the process is interrupted at any point, `attest` can be restarted.
40. The run resumes from the last wave checkpoint — completed waves are ratcheted and not repeated.
41. `attest` reports current status: phase, open tasks, uncovered requirements, blocker details.

## 8. Functional requirements

### 8.1 Spec intake and normalization

- **AT-FR-001**: The system must ingest one or more source spec files in any format in a single run preparation flow.
- **AT-FR-002**: The system must normalize source specs into a template format with stable requirement IDs.
- **AT-FR-003**: The system must extract requirements, assumptions, dependencies, ambiguities, and candidate task groupings into a normalized run artifact.
- **AT-FR-004**: The system must classify and record a scope mode (EXPANSION, HOLD SCOPE, or REDUCTION) in the run artifact. The scope mode is committed at user approval and enforced throughout the pipeline.
- **AT-FR-005**: The system must extract boundaries (Always, Ask First, Never) from the spec. "Always" constraints are injected into every task. "Ask First" items cause the orchestrator to block for user input. "Never" items cause the orchestrator to reject out-of-scope work.
- **AT-FR-006**: The system must support a user approval boundary before autonomous execution begins.
- **AT-FR-007**: The system must treat the approved run artifact, not raw prose, as the source of truth for the run.

### 8.2 Task tracking

- **AT-FR-008**: The system must include a built-in lightweight task tracker that manages tasks, epics (waves), and related work items.
- **AT-FR-009**: Task tracking must be file-based and live within the run directory, requiring no external services or git hooks.
- **AT-FR-010**: The task tracker must support claim and release operations for parallel worker coordination.

### 8.3 Task generation and isolation

- **AT-FR-011**: The system must compile an initial task graph deterministically from the approved run artifact.
- **AT-FR-012**: Each task must reference one or more requirement IDs from the approved run artifact.
- **AT-FR-013**: Each task must include scope metadata (owned, readonly, and shared paths) to prevent overlapping edits.
- **AT-FR-014**: Each task must carry mechanically verifiable conformance checks (files_exist, content_check, tests, lint). Tasks without at least one conformance check must be flagged as underspecified.
- **AT-FR-015**: Each task that modifies source code must specify required test levels (L0-L3). Workers must produce tests at each required level. Documentation, configuration, and chore tasks are explicitly marked as test-exempt.
- **AT-FR-016**: The system must compute waves from the dependency DAG using the ready front model — not manual phase declaration.
- **AT-FR-017**: The system must build a file-conflict matrix before wave assignment and resolve conflicts by serializing (different waves) or merging (single task).
- **AT-FR-018**: The system must maintain a cross-wave shared files registry and refresh the base SHA between waves so later waves see earlier waves' changes.
- **AT-FR-019**: The system must detect and remove false dependencies (dependencies that are only logical ordering without file or data dependencies).
- **AT-FR-020**: The system must run a baseline audit (quantitative measurements of the current codebase) before finalizing the work plan, and record commands and results in the run artifact.

### 8.4 Autonomous execution and resumability

- **AT-FR-021**: The system must launch autonomous runs from within a familiar agent CLI workflow.
- **AT-FR-022**: The system must write a wave checkpoint to disk after each wave completes, recording: wave number, timestamp, completed/failed tasks, changed files, git SHA, and acceptance verdict.
- **AT-FR-023**: Resuming a run must read the last wave checkpoint and continue from the next incomplete wave. Completed waves are ratcheted — permanent and not re-executed.
- **AT-FR-024**: The system must stop for a user decision only when a true blocker is reached.
- **AT-FR-025**: The system must enforce a global safety limit on total waves (default: 50) to prevent infinite loops.

### 8.5 Sub-agent execution and self-review

- **AT-FR-026**: The system must dispatch wave tasks to parallel Claude CLI sub-processes sharing the same git worktree, with fresh workers per wave.
- **AT-FR-027**: Each sub-agent must receive a structured prompt containing: task assignment, file manifest (files permitted to modify), conformance checks, cross-cutting constraints from the "Always" boundaries, and the mandatory self-review gate.
- **AT-FR-028**: Each sub-agent must work on a non-overlapping scope defined by the task's owned path metadata. The orchestrator must build and verify a file ownership map before spawning workers.
- **AT-FR-029**: Workers must write results to JSON files on disk. Messages to the orchestrator must be minimal (verdict and file path only). The orchestrator reads result files, not conversation messages.
- **AT-FR-030**: Workers MUST NOT commit to git. The orchestrator is the sole committer.
- **AT-FR-031**: The mandatory self-review gate must be baked into every worker's prompt with three steps: SCAN (grep for incomplete markers), REVIEW (written self-assessment of every modified file), and RUN (execute conformance checks, confirm exit code 0). Workers may not claim completion until all three steps pass.
- **AT-FR-032**: The orchestrator must validate every worker result before accepting it: verify self-review evidence exists and passes, execute conformance checks from task metadata, verify expected files exist.
- **AT-FR-033**: If validation fails, the orchestrator must send specific failure details back to the worker and retry (max 3 retries per task). After max retries, mark the task blocked and escalate.
- **AT-FR-034**: The orchestrator may stream additional nudge prompts into the same worker session if self-review evidence is missing, incomplete, or shows failures. The number of additional nudges is not fixed.

### 8.6 Test-first mode (optional)

- **AT-FR-035**: The system must support an optional test-first mode that adds SPEC, TEST, and RED gate phases before implementation.
- **AT-FR-036**: In test-first mode, SPEC wave workers must generate test contracts (invariants + test cases) from task descriptions without access to implementation code.
- **AT-FR-037**: In test-first mode, TEST wave workers must write failing tests from the contracts. Workers receive the contract but NOT the implementation code.
- **AT-FR-038**: The RED gate must run the test suite and verify that ALL new tests FAIL against the current codebase. Tests that pass indicate they test existing behavior, not new requirements — they must be flagged for rewrite.
- **AT-FR-039**: In GREEN wave (implementation), workers MUST NOT modify test files. Tests are immutable constraints that the implementation must satisfy.
- **AT-FR-040**: Test-first mode is enabled per-run or per-task. Documentation, configuration, and chore tasks skip it automatically.

### 8.7 Wave acceptance

- **AT-FR-041**: After all workers in a wave complete, the orchestrator must run a wave acceptance check before advancing: completeness scan (no TODO/FIXME/STUB markers in new code), and lightweight spec-compliance and error-path judges.
- **AT-FR-042**: The orchestrator must commit the wave's changes and write a wave checkpoint only after the wave acceptance check passes.
- **AT-FR-043**: After committing, the orchestrator must refresh the base SHA so the next wave's workers see all previous changes.

### 8.8 Convergence loops

- **AT-FR-044**: Each pipeline stage must loop until its exit criteria are met, not for a fixed number of iterations.
- **AT-FR-045**: Implementation loops must continue nudging sub-agents until exit criteria are satisfied.
- **AT-FR-046**: Council review loops must continue until no "must fix" or "should fix" findings remain open.
- **AT-FR-047**: If a convergence loop is not making progress (same failures reappearing), the system must escalate to a stronger reasoning path — including upgrading the council review mode (e.g., standard → deep) and triggering deep audit with parallel explorers.
- **AT-FR-048**: Escalation must be recorded for later inspection.

### 8.9 Hygiene gate

- **AT-FR-049**: A coded hygiene gate must run after implementation and before council review.
- **AT-FR-050**: The hygiene gate must run the project's own tooling: linting, formatting, test execution, and security scanning. All checks are deterministic (confidence 1.0).
- **AT-FR-051**: If any hygiene check fails, the task must return to the implementation loop automatically with specific failure details.
- **AT-FR-052**: The hygiene gate must verify that task conformance checks pass (files_exist, content_check, tests, lint) and that linked requirement IDs are present.
- **AT-FR-053**: The hygiene gate must verify that workers produced tests at each required test level (L0-L3) as specified in the task metadata. Missing required test levels → task returns to implementation.
- **AT-FR-054**: The hygiene gate must scan for incomplete markers (TODO/FIXME/STUB) in new code. Any found → task returns to implementation.
- **AT-FR-055**: A task must not reach council review until all hygiene checks pass.
- **AT-FR-056**: The system must support a project-level suppression file for known false positives that should not block progress.

### 8.10 Multi-model review and council

- **AT-FR-057**: Codex review must run for task-level implementation validation in v1.
- **AT-FR-058**: Claude Opus must synthesize reviewer findings and compare implementation against the approved spec before closure, checking requirement traceability.
- **AT-FR-059**: Reviewers must write all analysis to files. Messages to the orchestrator must be minimal (verdict, confidence, file path). The orchestrator reads files during synthesis (context budget rule).
- **AT-FR-060**: The council must support a pluggable reviewer set so that additional reviewers (e.g., Gemini) can be added without pipeline changes.
- **AT-FR-061**: The system must auto-select council review intensity based on scope: quick (1 judge, inline) for structural checks and small tasks, standard (2 judges) for default reviews, deep (3-4 judges with specialized perspectives) for large waves and final acceptance. The mode may be overridden or escalated.
- **AT-FR-062**: The system must support council checkpoints at least for spec review, work plan review, task verification, and final run acceptance. Work plan review must validate that the task graph covers all requirement IDs, that task scopes do not overlap within waves, that wave ordering respects dependencies, that every task has conformance checks and required test levels, and that tasks are small enough for a single sub-agent.
- **AT-FR-063**: Council reviewers must receive a mandatory 8-category review checklist (resource leaks, input safety, dead code, hardcoded values, edge cases, concurrency, error handling, security). Reviewers must either report findings per category or explicitly certify each category as clean.
- **AT-FR-064**: The system must support pluggable domain-specific checklists (e.g., SQL safety, LLM trust boundaries, race conditions) loaded based on code pattern detection or project configuration.
- **AT-FR-065**: Each council finding must include structured remediation fields: fix (specific action), why (root cause), and ref (file path or requirement ID).
- **AT-FR-066**: Council findings must be classified mechanically by action tier: "must fix" (critical/high, confidence >= 0.80), "should fix" (medium, or high with confidence 0.65-0.79), "consider" (everything else above 0.65 floor).
- **AT-FR-067**: "Must fix" and "should fix" findings must automatically produce repair tasks routed back to the implementation loop with the finding as context.
- **AT-FR-068**: When the hygiene gate has produced findings, these must be passed to the council as pre-existing context. Reviewers confirm, reject, or reclassify severity (adjudication mode) rather than rediscovering.
- **AT-FR-069**: When a convergence loop is not making progress, the system must escalate to deep audit mode: dispatch parallel explorer agents per-file using the 8-category checklist, then run council judges in adjudication mode over the explorer findings.
- **AT-FR-070**: The system must treat unresolved blocking findings conservatively and keep the work open.
- **AT-FR-071**: The consensus verdict must be computed deterministically: all PASS → PASS, any FAIL → FAIL, any WARN (no FAIL) → WARN.

### 8.11 Model routing

- **AT-FR-072**: The system must choose Claude model tier based on task class, not one fixed model for every step.
- **AT-FR-073**: Planning, strategic review, synthesis, and council judging must default to Opus.
- **AT-FR-074**: Implementation workers and repair work must default to Sonnet.
- **AT-FR-075**: Lightweight wave acceptance judges, quick council reviews, and scouting must default to Haiku.
- **AT-FR-076**: The system must record routing and escalation decisions for later inspection.

### 8.12 Status and explainability

- **AT-FR-077**: The system must be able to report run status at any point: current phase, current wave, completed work, open tasks, blockers.
- **AT-FR-078**: The system must explain open blockers in plain language and link them to affected requirements and tasks.
- **AT-FR-079**: The system must show uncovered requirement IDs for any incomplete run.
- **AT-FR-080**: The system must show which reviewer or hygiene check blocked a task and why, including the structured remediation fields (fix, why, ref).
- **AT-FR-081**: The system must expose first-class task views for `ready`, `blocked`, `next`, and `progress`.
- **AT-FR-082**: Task views returned to agents must support a stable machine-readable form.

## 9. User journeys

### 9.1 Journey A: Start a new run

1. User writes or selects one or more spec files.
2. User invokes `attest` to prepare a run.
3. `attest` normalizes the specs, classifies scope mode, extracts boundaries, and asks targeted clarification questions.
4. `attest` presents a normalized run artifact with stable requirement IDs, scope mode, and boundaries.
5. User approves the artifact.
6. `attest` runs a baseline audit, compiles a task graph with file-conflict detection and conformance checks, organizes into waves via the ready front model, and begins execution.

### 9.2 Journey B: Autonomous convergence

1. `attest` builds a file ownership map and dispatches wave tasks to parallel sub-agents with structured prompts.
2. Workers implement, run the mandatory self-review gate (SCAN, REVIEW, RUN), and write result JSON to disk.
3. The orchestrator validates each result (verify then trust), retrying on failure.
4. Wave acceptance check runs (completeness scan, lightweight judges). The orchestrator commits and checkpoints.
5. Hygiene gate runs the project's deterministic tooling. Failures bounce back to implementation with specific details.
6. Clean code reaches the council. Codex reviews (writing analysis to file), Opus synthesizes against the spec.
7. Council findings with structured remediation fields create repair tasks classified by action tier. "Must fix" and "should fix" re-enter the implementation loop.
8. When all requirements are verified and no actionable findings remain, the run reaches a verified terminal state.

### 9.3 Journey C: Resume after interruption

1. The process crashes or the user closes the terminal.
2. User restarts `attest` and points it at the same run.
3. `attest` reads the last wave checkpoint. Completed waves are ratcheted — permanent and not repeated.
4. `attest` resumes from the next incomplete wave and continues the pipeline.
5. `attest` reports current status: phase, wave, completed work, blockers.

### 9.4 Journey D: Inspect status

1. User asks `attest` for run status.
2. `attest` reports:
   - current pipeline stage and wave number
   - completed and open tasks by wave with file ownership
   - uncovered requirements
   - hygiene gate or council findings blocking progress (with fix/why/ref)
   - true blockers needing user input

## 10. Non-functional requirements

- **AT-NFR-001**: v1 must work from local agent CLI workflows without requiring hosted orchestration.
- **AT-NFR-002**: Run state must survive process exit, crash, and session loss via disk-based wave checkpoints and ratcheted progress.
- **AT-NFR-003**: Resuming a run must be idempotent — re-executing fabrikk against the same run produces the same outcome as continuing from the checkpoint.
- **AT-NFR-004**: The system must favor correctness over cheapest-token routing when the two are in tension.
- **AT-NFR-005**: The system must use lower-cost model tiers (Sonnet for workers, Haiku for wave judges) for routine work by default.
- **AT-NFR-006**: The system must be transparent about what is approved, verified, blocked, and assumed.
- **AT-NFR-007**: The user-facing control surface must remain understandable from conversational status output.
- **AT-NFR-008**: Worker results must be disk-backed (JSON files), not passed through conversation context, to prevent context explosion with N concurrent workers.

## 11. Acceptance scenarios

- **AT-AS-001**: Given the same approved run artifact, the system produces the same initial task graph.
- **AT-AS-002**: A task does not close when only the implementer reports success — it must pass the self-review gate, the orchestrator's verify-then-trust validation, the hygiene gate, and council review.
- **AT-AS-003**: Hygiene gate failures (lint, test, security) bounce the task back to implementation automatically with specific failure details.
- **AT-AS-004**: Council findings classified as "must fix" or "should fix" create repair tasks that re-enter the implementation loop with the finding as context.
- **AT-AS-005**: A convergence loop that is not making progress escalates to a stronger model rather than looping indefinitely.
- **AT-AS-006**: A crashed run resumes from its last wave checkpoint without re-executing completed waves.
- **AT-AS-007**: A run finishes only when every requirement in the approved artifact is verified or explicitly blocked.
- **AT-AS-008**: Codex can block closure even after the hygiene gate passes.
- **AT-AS-009**: Two tasks that touch the same file are never dispatched to concurrent workers in the same wave.
- **AT-AS-010**: A worker that claims completion without self-review evidence is sent back for retry, not accepted.
- **AT-AS-011**: The orchestrator never accepts a worker's completion claim without running the task's conformance checks.
- **AT-AS-012**: The global wave limit prevents infinite loops — the run blocks after the maximum is reached.
- **AT-AS-013**: In test-first mode, a test that passes against the current codebase before implementation (RED gate failure) is flagged for rewrite, not accepted.
- **AT-AS-014**: In test-first mode, a worker cannot make tests pass by modifying the test files — test files are immutable during the GREEN wave.
- **AT-AS-015**: A task that specifies L1 (unit) test level but produces no unit tests fails the hygiene gate.
- **AT-AS-016**: A council review that stalls (same findings reappearing) escalates to deep audit mode with parallel explorers before retrying.
- **AT-AS-017**: Council reviewers must certify each of the 8 review categories as either "finding reported" or "clean" — they cannot skip categories.

## 12. Success criteria

`attest` is successful if users can:

- approve a run once and step away
- return later and find materially spec-conformant output
- understand exactly what remains open or blocked without re-reading the entire transcript
- rely on the system to keep pushing back against false completion claims
- trust that basic code hygiene is enforced before any review cycle is spent
- trust that agent self-reports are never accepted without mechanical verification

## 13. Assumptions

- v1 is Claude-first for execution.
- Codex is the mandatory independent reviewer in v1. Gemini is a future addition pending capacity resolution.
- The council reviewer set is pluggable — adding or removing reviewers does not require pipeline changes.
- Source specs can be in any format; `attest` normalizes them into a template with stable requirement IDs.
- The user values trustworthy closure more than maximum task throughput.
- Convergence loops are driven by exit criteria, not fixed iteration counts.
- Agent completion claims are never trusted without mechanical verification.
