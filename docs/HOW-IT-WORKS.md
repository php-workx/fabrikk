# How fabrikk Works

This document walks through the fabrikk pipeline in detail, explaining each stage, the mechanisms that enforce quality, and how the system self-corrects.

For the full specification, see [fabrikk-functional-spec.md](specs/fabrikk-functional-spec.md).

---

## The Core Idea

fabrikk combines two principles:

1. **Rigid skeleton.** The pipeline stages are coded in Go. No agent can skip, reorder, or invent stages.
2. **Convergence-driven loops.** Within each stage, loops run until exit criteria are met — not for a fixed number of iterations.

The result: agents are autonomous *within* their stage, but the process is mechanical. The skeleton contains the autonomy.

```
                         ┌─────────────────────────────────────────┐
                         │           REPAIR LOOP                   │
                         │  Council findings create repair tasks   │
                         │  that re-enter implementation           │
                         v                                         │
┌──────────┐  ┌───────────┐  ┌────────────────┐  ┌─────────┐  ┌──────────┐
│   Spec   │─>│   Work    │─>│ Implementation │─>│ Hygiene │─>│ Council  │
│  Review  │  │   Plan    │  │  (parallel)    │  │  Gate   │  │  Review  │
└──────────┘  └───────────┘  └────────────────┘  └─────────┘  └──────────┘
```

---

## Stage 1: Spec Review

**Input:** One or more spec files. Canonical AT-* specs stay deterministic; free-form specs require explicit LLM normalization consent.
**Output:** A normalized run artifact with stable requirement IDs.
**Gate:** `fabrikk artifact approve <run-id>` before planning continues.

The spec review stage takes human-authored specs and normalizes them into a structured run artifact. Free-form specs are converted by an LLM, independently verified against the original source, and approved only if the reviewed source, prompts, and normalized artifact still match. This artifact becomes the execution contract — the single source of truth for the entire run.

### What the run artifact contains

- **Normalized requirements** with stable IDs (AT-FR-xxx, AT-TS-xxx, etc.)
- **Source references** — tracing each requirement back to the original spec
- **Clarified assumptions** — ambiguities resolved before execution
- **Dependencies** between requirements
- **Risk classification** — which areas are high-risk
- **Scope mode** — one of:
  - EXPANSION: greenfield, dream big
  - HOLD SCOPE: bug fix or refactor, maximum rigor within accepted scope
  - REDUCTION: large scope, minimize to MVP
- **Boundaries:**
  - *Always:* non-negotiable requirements injected into every task
  - *Ask First:* decisions requiring human input (orchestrator blocks)
  - *Never:* explicit out-of-scope items (orchestrator rejects)

The scope mode is committed at artifact approval time. An agent cannot silently expand scope if the mode is HOLD SCOPE.

---

## Stage 2: Work Plan

**Input:** Approved run artifact.
**Output:** Task graph organized into waves with conformance checks.
**Gate:** Council review of the work plan.

### Baseline audit

Before decomposing into tasks, the orchestrator runs quantitative measurements:

```bash
wc -l internal/engine/*.go           # current LOC
grep -c "func Test" *_test.go        # existing test count
ls internal/compiler/*.go | wc -l    # file count
```

Commands and results are recorded. After implementation, the same commands verify progress.

### Task graph compilation

The approved artifact is compiled into a task graph. This is deterministic — no LLM involved.

Each task carries:
- **Requirement IDs** it satisfies
- **File ownership** (owned / readonly / shared paths)
- **Conformance checks** — mechanically verifiable exit criteria
- **Required test levels** (L0 contract, L1 unit, L2 integration, L3 component)

### Wave computation (the ready front model)

Waves are not manually declared. They are computed from the dependency DAG:

```
Ready front = tasks with all dependencies satisfied

Wave 1: Task A, Task C (no dependencies)
    ↓ both complete
Wave 2: Task B, Task D (depended on Wave 1)
    ↓ both complete
Wave 3: Task E (depended on Wave 2)
```

As tasks complete, blocked work automatically becomes ready.

### File-conflict matrix

Before assigning tasks to waves, the compiler builds a conflict matrix:

```
File               | Tasks
src/auth.go        | Task 1, Task 3  ← CONFLICT
src/config.go      | Task 2
src/auth_test.go   | Task 1
```

Tasks touching the same file cannot be in the same wave. Conflicts are resolved by serializing (different waves) or merging (single task).

### Conformance checks

Every task must have at least one mechanically verifiable check:

| Acceptance Criteria | Conformance Check |
|---|---|
| "File X exists" | `files_exist: ["X"]` |
| "Function Y is implemented" | `content_check: {file: "src/foo.go", pattern: "func Y"}` |
| "Tests pass" | `tests: "go test ./..."` |

Tasks without conformance checks are flagged as underspecified.

### Test level classification

Each task that modifies source code specifies which test levels are required:

| Level | Name | What It Tests |
|-------|------|---------------|
| L0 | Contract | API boundaries and external interfaces |
| L1 | Unit | Individual function behavior in isolation |
| L2 | Integration | Multiple components working together |
| L3 | Component | Subsystem end-to-end workflows |

Workers must produce tests at each required level. The hygiene gate verifies.

### Work plan review

The council reviews the plan before implementation:
- Full requirement coverage?
- No scope overlaps within waves?
- Correct wave ordering?
- Every task has conformance checks and test levels?
- Tasks small enough for a single sub-agent?
- False dependencies removed?

---

## Stage 3: Implementation

**Input:** Task graph with waves.
**Output:** Implemented code with self-review evidence.
**Gate:** Wave acceptance check after each wave.

### Test-first mode (optional)

When enabled, three phases run before implementation:

1. **SPEC wave** — workers generate test contracts (invariants + test cases) from task descriptions. Workers can read the codebase but not the implementation code for the feature they're specifying.

2. **TEST wave** — workers write failing tests from the contracts. No access to implementation code.

3. **RED gate** — the orchestrator runs the test suite. ALL new tests must FAIL. If a test passes, it tests existing behavior (not new requirements) and is worthless.

4. **GREEN wave** — standard implementation. Workers receive immutable failing tests and must make them pass. They CANNOT modify test files.

### Parallel execution

For each wave, the orchestrator:

1. **Builds a file ownership map** — verifies no conflicts before spawning
2. **Spawns Claude CLI sub-processes** — one per task, sharing the same worktree
3. **Each worker receives a structured prompt:**
   - Task assignment and description
   - FILE MANIFEST — explicit list of files permitted to modify
   - Conformance checks to satisfy
   - Cross-cutting constraints (from "Always" boundaries)
   - Mandatory self-review gate instructions

### The self-review gate

Baked into every worker prompt. Three mandatory steps before claiming completion:

**SCAN** — grep newly added lines for incomplete markers:
```bash
git diff HEAD | grep '^+' | grep -v '^+++' | \
  grep -inE 'TODO|FIXME|HACK|XXX|PLACEHOLDER|UNIMPLEMENTED|STUB|WIP|NOCOMMIT'
```
Any match → NOT done. Fix first.

**REVIEW** — brutally honest self-assessment of every modified file:
- What changed and why
- Gaps or incomplete implementations
- Missing error handling or edge cases
- Missing tests for new behavior
- Code you'd flag in someone else's PR

Issues found → fix before proceeding. Do not rationalize.

**RUN** — execute conformance checks from the task metadata. Confirm exit code 0.

### Disk-backed results

Workers write results to JSON files:
```json
{
  "type": "completion",
  "status": "done",
  "detail": "Added auth middleware with rate limiting",
  "artifacts": ["src/auth/middleware.go", "src/auth/middleware_test.go"],
  "self_review": {
    "scan_clean": true,
    "review": "Checked all 8 categories. Rate limit reset uses time.After...",
    "verification_command": "go test ./src/auth/...",
    "verification_exit_code": 0
  }
}
```

Messages to the orchestrator are under 100 tokens. The orchestrator reads result files, not conversations. This prevents context explosion with N concurrent workers.

### Verify then trust

After a worker signals completion, the orchestrator validates mechanically:

1. Self-review evidence exists and passes
2. Conformance checks from task metadata pass
3. Expected files exist

On pass → mark complete. On fail → send specific failure back, retry (max 3). After 3 failures → mark blocked, escalate.

### Wave acceptance check

After all wave tasks complete:

1. **Completeness scan** — grep new code for TODO/FIXME/STUB. Any found → FAIL.
2. **Lightweight judges** — two Haiku-tier agents check spec compliance and error paths.
3. **Verdict** — both PASS → advance. Any FAIL → create fix tasks.

### Wave checkpoint

After each wave, a checkpoint is written:
```json
{
  "wave": 3,
  "tasks_completed": ["task-1", "task-2"],
  "files_changed": ["file1.go", "file2.go"],
  "git_sha": "abc123",
  "acceptance_verdict": "PASS"
}
```

If the process dies, restart reads the last checkpoint and resumes. Completed waves are ratcheted — permanent, never re-executed.

### Git commit policy

Workers MUST NOT commit. The orchestrator is the sole committer. This prevents concurrent git index corruption. Commits happen per-wave, providing clean rollback boundaries.

After each wave commit, the base SHA is refreshed. Next wave's workers see all previous changes.

---

## Stage 4: Hygiene Gate

**Input:** Implemented code from workers.
**Output:** Pass (advance to council) or fail (bounce back to implementation).
**Gate:** All deterministic checks must pass.

The hygiene gate runs the project's own tooling with confidence 1.0:

- Linting (golangci-lint with project config)
- Formatting (gofumpt)
- Tests (go test with race detector)
- Security (gosec, gitleaks)
- Dependencies (trivy)

Additionally:
- **Conformance checks** from task metadata are verified
- **Test level coverage** — missing required levels (L0-L3) bounce back
- **Incomplete markers** (TODO/FIXME/STUB) in new code → bounce back
- **Requirement IDs** must be linked

If any check fails, the task returns to implementation with specific failure details. No LLM review tokens are spent on code that fails basic hygiene.

Projects may provide a **suppression file** for known false positives.

---

## Stage 5: Council Review

**Input:** Code that passed the hygiene gate.
**Output:** PASS / WARN / FAIL verdict with structured findings.
**Gate:** No "must fix" or "should fix" findings remain.

### Review modes

The orchestrator auto-selects review intensity:

| Mode | Judges | When Used |
|------|--------|-----------|
| Quick | 1 (inline) | Work plan review, small tasks |
| Standard | 2 (independent) | Default code review |
| Deep | 3-4 (specialized) | Large waves, final acceptance |

### Reviewers

- **Codex** — independent code and test reviewer (code quality, error paths, test coverage)
- **Opus** — synthesizer and spec-conformance reviewer (requirement traceability, architectural coherence)

### The 8-category review checklist

Reviewers receive a mandatory checklist to prevent satisfaction bias:

| Category | What to Check |
|----------|--------------|
| Resource Leaks | Unclosed files/connections, missing defer, goroutine leaks |
| Input Safety | Validation, injection, path traversal |
| Dead Code | Unreachable branches, unused functions |
| Hardcoded Values | Magic numbers, hardcoded paths/credentials |
| Edge Cases | Nil/zero, empty collections, boundaries, overflow |
| Concurrency | Data races, missing locks, channel misuse |
| Error Handling | Swallowed errors, generic catch-all, missing propagation |
| Security | XSS, CORS, CSRF, SSRF, rate limiting |

Reviewers must report findings per category OR explicitly certify it clean. They cannot skip categories.

### Domain-specific checklists

Pluggable checklists loaded based on code patterns:
- SQL/ORM code → SQL safety checklist
- LLM/AI code → LLM trust boundary checklist
- Concurrent code → race condition checklist

### Structured findings

Every finding must include:
- **fix** — specific action to resolve
- **why** — root cause or rationale
- **ref** — file path or requirement ID

### Action tier classification

Findings are classified mechanically:

| Tier | Criteria | Action |
|------|----------|--------|
| Must Fix | Critical/high + confidence >= 0.80 | Repair task created automatically |
| Should Fix | Medium, or high with lower confidence | Repair task created automatically |
| Consider | Everything else above 0.65 floor | Logged, does not block |

### Adjudication mode

If the hygiene gate produced findings, they're passed to the council as pre-existing context. Reviewers confirm, reject, or reclassify rather than rediscovering.

### Deep audit escalation

When a convergence loop stalls (same findings reappearing), the orchestrator escalates:
1. Dispatch parallel explorer agents per-file using the 8-category checklist
2. Merge findings into a sweep manifest
3. Council judges operate in adjudication mode over the explorer findings

This finds ~3x more issues than a single-pass review.

### Consensus verdict

Deterministic computation:
- All PASS → **PASS**
- Any FAIL → **FAIL**
- Any WARN (no FAIL) → **WARN**

### Repair loop

"Must fix" and "should fix" findings automatically create repair tasks. These tasks enter the task graph and go through the full pipeline: implementation → hygiene gate → council review. The loop continues until no actionable findings remain or a true blocker is reached.

---

## Model Routing

The orchestrator picks the right model for each role:

| Role | Model | Rationale |
|------|-------|-----------|
| Planning, synthesis, council judging | Opus | Needs deep judgment |
| Implementation workers, repair work | Sonnet | Cost-effective, 3-5x cheaper |
| Wave acceptance judges, quick checks | Haiku | Fast, cheap, sufficient for completeness scans |

Escalation on stall can upgrade a worker from Sonnet to Opus.

---

## Resumability

All run state is persisted to disk. The system survives:
- Process crashes
- Terminal closes
- Machine restarts

On restart, the orchestrator reads the last wave checkpoint and continues from there. Completed waves are **ratcheted** — permanent progress that is never re-executed. Repair tasks create new waves; they don't re-open completed ones.

---

## The Principles

### Verify then trust
Agent completion claims are never trusted. Every result is validated mechanically before acceptance. This is the single most important principle in fabrikk.

### The spec is the anchor
Every review iteration checks against the same approved spec. Not the agent's self-assessment. Not whatever the agent drifted toward. The original, approved spec.

### Agents fill slots, they don't control the process
The rigid skeleton is coded in Go. Agents are autonomous within their stage — they decide how to implement, what tests to write, what to flag in review. But they don't decide the workflow, skip stages, or declare themselves done.

### Exit criteria, not iteration counts
Convergence loops run until done, not for 3 rounds. But a global safety limit (50 waves) prevents infinite loops.

### Deterministic before LLM
The hygiene gate runs mechanical tools (lint, test, security) before spending any LLM review tokens. Fast, cheap, confidence 1.0.
