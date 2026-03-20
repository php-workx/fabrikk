# AgentOps Skill Deep Dive — What We Use, What We Don't

Research date: 2026-03-20
Source: `/Users/runger/workspaces/agentops/skills/` (AgentOps v2.25.0)

For each of the four core skills, this document describes exactly how AgentOps does it, then what attest adopts and what it skips.

---

## 1. /plan — Epic Decomposition

### How AgentOps does it

**10-step workflow:**

1. **Setup** — create `.agents/plans/` directory
2. **Check prior research** — read `.agents/research/` artifacts, search knowledge flywheel via `ao search`
3. **Load compiled prevention** — load `.agents/planning-rules/*.md` and `.agents/findings/registry.jsonl` as hard planning context. Ranked by goal-text overlap, issue-type overlap, language overlap, changed-file overlap. Cap at top 5.
4. **Explore codebase** — dispatch Explore agent requesting symbol-level detail: function signatures, struct definitions, reuse points with file:line references, test patterns
5. **Pre-planning baseline audit** — run `grep/wc/ls` to quantify current state. Record commands alongside results. This grounds the plan in verified numbers.
6. **Generate implementation detail** — symbol-level specs for EVERY file:
   - File inventory table (all files + changes, new files marked `**NEW**`)
   - Exact function signatures and reuse points with `file:line`
   - Inline code blocks for non-obvious constructs (verified with `go build`)
   - Named test functions with one-line descriptions
   - Test level classification (L0-L3 pyramid)
   - Verification procedures with runnable bash sequences
   - Data transformation mapping tables
7. **Decompose into issues** — each issue gets: title, description, dependencies, acceptance criteria, test levels, design brief (for rewrites), conformance checks (files_exist, content_check, tests, lint, command)
8. **Compute waves** — dependency-based:
   - File-conflict matrix (mandatory) — if same file in 2+ same-wave issues, serialize or merge
   - Cross-wave shared files registry (mandatory) — flag files appearing in multiple waves
   - Validate dependency necessity — remove false deps that only impose logical ordering
9. **Write plan document** — to `.agents/plans/YYYY-MM-DD-<slug>.md` with sections: Context, Files to Modify, Boundaries (Always/Ask First/Never), Baseline Audit, Implementation (symbol-level), Tests, Conformance Checks, Verification, Issues, Execution Order, Post-Merge Cleanup
10. **Create beads issues** — epic + child issues with dependencies via `bd dep add`, conformance checks embedded as fenced `validation` blocks, cross-cutting constraints in epic description

**Key details:**
- Boundaries: Always (non-negotiable), Ask First (needs human), Never (out of scope)
- Conformance checks bridge plan → implementation → verification
- Human approval gate (skippable with `--auto`)
- Ratchet progress recording

### What attest adopts from /plan

| Pattern | How attest uses it |
|---|---|
| **Baseline audit** | The compiler runs quantitative baseline commands before generating the task graph. Results stored in run artifact. |
| **File-conflict matrix** | Coded in Go. The compiler builds a conflict matrix and resolves overlaps before wave assignment. |
| **Cross-wave shared files registry** | The engine tracks shared files and refreshes base SHA between waves. |
| **Conformance checks per task** | Every task carries mechanically verifiable exit criteria (files_exist, content_check, tests, lint). Required — underspecified tasks are flagged. |
| **Boundaries (Always/Ask First/Never)** | Stored in the run artifact. "Always" constraints injected into every worker prompt. "Ask First" causes orchestrator to block. "Never" rejects out-of-scope work. |
| **Wave computation from dependency DAG** | The ready front model. Not manually declared — computed from the task graph. |
| **False dependency detection** | The compiler checks each dependency: does blocked task modify blocker's files? Does it read blocker's output? If neither → remove. |
| **Symbol-level implementation detail** | The work plan includes file inventory, function signatures, reuse points — enough for a sub-agent to implement without rediscovering the codebase. |
| **Task sizing heuristics** | 1-2 files → 1 task. 3+ independent files → split. Shared files → serialize or merge. |

### What attest skips from /plan

| Pattern | Why skipped |
|---|---|
| **Knowledge flywheel (ao search, prior findings)** | v1 has no cross-session knowledge system. Findings from the current run are tracked, but there's no persistent learning registry across runs. Future consideration. |
| **Compiled prevention checks (.agents/planning-rules/)** | These are AgentOps-specific compiled artifacts from prior sessions. attest doesn't have this pipeline. The concept is sound (learn from past failures) but requires the full flywheel infrastructure. |
| **Beads integration (bd CLI)** | attest has its own built-in task tracker. No external dependency on beads. |
| **Ratchet recording (ao ratchet)** | attest uses wave checkpoints for the same purpose — built into the engine, not an external tool. |
| **Human approval gate** | attest has its own approval boundary (AT-FR-006). The mechanism is different — coded in Go, not via `AskUserQuestion` tool. |
| **Test level classification (L0-L3 pyramid)** | Useful but not in v1 scope. attest relies on the project's existing test structure. |
| **Data transformation mapping tables** | Domain-specific to certain plan types. Not generalized into attest. |
| **Post-merge cleanup (scaffold naming)** | Implementation detail that can be handled by the hygiene gate. |
| **Design briefs for rewrites** | Good practice but enforced by the plan's level of detail, not a separate concept in attest. |

---

## 2. /pre-mortem — Plan Validation Before Implementation

### How AgentOps does it

**Core mechanism:** Run `/council validate` on a plan to get multi-model judgment before committing to implementation.

**Modes:**
- Default (`--quick`): single-agent inline review, no spawning. ~10% of full council cost. Catches real bugs.
- `--deep`: 4 judges with plan-review perspectives (missing-requirements, feasibility, scope, spec-completeness)
- `--mixed`: 3 Claude + 3 Codex agents cross-vendor
- `--debate`: two-round adversarial review

**Key steps:**

1. **Find the plan** — most recent `.agents/plans/*.md` or explicit path
2. **Load compiled prevention** — `.agents/pre-mortem-checks/*.md` first, then `.agents/findings/registry.jsonl` fallback. Same ranked packet as /plan.
3. **Scope mode selection** — EXPANSION / HOLD SCOPE / REDUCTION. Auto-detected from plan characteristics. Committed in council packet. Mode-specific judge prompts:
   - EXPANSION: "What would make this 10x more ambitious?"
   - HOLD SCOPE: "Find every failure mode within accepted scope."
   - REDUCTION: "Find the minimum viable version."
4. **Council validation** — `/council validate <plan-path>` with perspectives
5. **Temporal interrogation** (auto on 5+ files or 3+ sequential deps):
   - Hour 1: what blocks first meaningful change?
   - Hour 2: circular dependencies?
   - Hour 4: what fails when components connect?
   - Hour 6+: what "should be quick" but historically isn't?
6. **Error & rescue map** (mandatory for external calls) — every API/DB/file-IO call gets a row showing what can fail, how it's rescued, what the user sees
7. **Test pyramid coverage check** — validates plan includes appropriate test levels
8. **Verdict:** PASS → proceed. WARN → address or accept risk. FAIL → fix plan.
9. **Persist reusable findings** — WARN/FAIL findings go to `.agents/findings/registry.jsonl` for future sessions. Compiled checks refreshed.
10. **Prediction IDs** — each finding gets `pm-YYYYMMDD-NNN` for correlation with post-implementation findings

**Mandatory for 3+ issue epics.** Crank enforces this.

### What attest adopts from /pre-mortem

| Pattern | How attest uses it |
|---|---|
| **Council review of work plan** | AT-FR-053: The council reviews the work plan before implementation begins. Validates requirement coverage, scope overlaps, wave ordering, task sizing, conformance check completeness. |
| **Scope mode commitment** | Stored in run artifact as EXPANSION/HOLD/REDUCTION. Enforced throughout pipeline — agents can't silently expand scope. |
| **Temporal interrogation** | Applied during spec review to identify blocking dependencies and inform wave ordering. |
| **Mandatory for 3+ task plans** | attest always reviews the work plan — no bypass. |
| **PASS/WARN/FAIL verdict** | Consensus verdict computation (AT-FR-059). Deterministic rules. |
| **Error & rescue map concept** | Applicable when the spec describes external integrations. The council reviewer checks for unhandled failure modes. |

### What attest skips from /pre-mortem

| Pattern | Why skipped |
|---|---|
| **Knowledge flywheel search** | No cross-session knowledge system in v1. |
| **Compiled prevention checks** | AgentOps-specific infrastructure. |
| **PRODUCT.md integration** | attest doesn't have a product context concept. The spec itself is the product context. |
| **Prediction ID tracking** | Useful for measuring pre-mortem accuracy, but requires post-mortem correlation infrastructure that's not in v1 scope. |
| **Finding persistence to registry** | No cross-run learning in v1. Findings from the current run are tracked in the run directory, but don't compile into prevention checks for future runs. |
| **Debate mode (--debate)** | The adversarial two-round protocol is powerful but adds complexity. attest v1 uses a simpler consensus model. Could be added as an escalation path. |
| **Multiple council modes (--quick, --deep, --mixed)** | attest always runs the full review. No budget optimization modes in v1. |

---

## 3. /crank + /swarm — Parallel Implementation

### How AgentOps does it

**Crank** is the orchestration engine. **Swarm** is the parallel execution engine. Crank loops waves via swarm until all issues are closed.

**Architecture:**
```
Crank (orchestrator) → Swarm (executor per wave) → Workers (ephemeral per wave)
```

**The FIRE loop (per wave):**
- **Find:** `bd ready` or `TaskList()` → get unblocked tasks
- **Ignite:** `TaskCreate` + `/swarm` → spawn fresh workers
- **Reap:** Collect results + validate + sync to tracker
- **Check:** Wave acceptance check (completeness + judges + vibe)
- **Escalate:** Fix blockers or fail

**Key mechanisms:**

**1. Worker prompt template (mandatory, verbatim):**
Every worker gets:
- Worker identity and task assignment
- FILE MANIFEST — explicit list of files permitted to modify
- Result file format (JSON with status, artifacts, self-review evidence)
- CONTEXT BUDGET RULE — messages to lead under 100 tokens
- MANDATORY SELF-REVIEW GATE (3 steps: SCAN, REVIEW, RUN)
- Rules: only your task, no other tasks, no git commits, no messaging other workers

**2. Self-review gate (baked into every worker prompt):**
- SCAN: `git diff HEAD | grep '^+' | grep -v '^+++' | grep -inE 'TODO|FIXME|...'`
- REVIEW: written self-assessment of every modified file
- RUN: execute conformance check commands, confirm exit code 0
- Result includes self-review evidence JSON

**3. Pre-spawn conflict detection:**
```
for task in wave_tasks:
    for file in task.files:
        if file in all_files:
            CONFLICT → serialize or isolate
        all_files[file] = task.id
```
Display file ownership map before spawning.

**4. Verify-then-trust validation:**
- Check self-review evidence (scan_clean, review present, verification_exit_code)
- Execute conformance checks from task metadata (files_exist, content_check, tests, lint)
- On fail → retry (max 3) → blocked
- Agent completion claims are NEVER trusted

**5. Wave acceptance check:**
- Completeness scan (TODO/FIXME markers in new code)
- 2 Haiku judges (spec compliance, error paths) — parallel
- Per-wave vibe gate (CRITICAL findings → FAIL)
- Verdict gate: PASS → advance, WARN → fix attempts → FAIL, FAIL → block

**6. Wave checkpoints:**
```json
{
  "wave": 3, "timestamp": "...",
  "tasks_completed": ["task-1", "task-2"],
  "tasks_failed": ["task-3"],
  "files_changed": ["file1.go"],
  "git_sha": "abc123",
  "acceptance_verdict": "PASS"
}
```

**7. Git commit policy:**
- Workers MUST NOT commit — lead is sole committer
- Per-wave commits (default) or per-task commits (opt-in)
- One commit per wave provides clean rollback boundary

**8. Base-SHA refresh:**
- After wave commit, capture new HEAD
- Next wave workers see all previous changes
- Cross-wave overlap detection

**9. Global limits:** MAX_EPIC_WAVES = 50

**10. Fresh workers per wave (Ralph pattern):**
- New sub-agents for each wave
- Previous workers torn down
- TaskList as persistent state, workers are ephemeral

**11. Scope-escape protocol:**
- Worker needs files outside manifest → write blocked result
- Append to `.agents/swarm/scope-escapes.jsonl`
- Lead reviews between waves, creates follow-up tasks

**12. Model routing:**
- Lead/orchestrator: Opus
- Workers: Sonnet (3-5x cheaper)
- Wave acceptance judges: Haiku

### What attest adopts from /crank + /swarm

| Pattern | How attest uses it |
|---|---|
| **Ralph pattern (fresh workers per wave)** | AT-FR-025: Fresh workers per wave. Workers are ephemeral Claude CLI sub-processes. Orchestrator persists. |
| **Worker prompt template** | AT-FR-026: Structured prompt with task assignment, file manifest, conformance checks, cross-cutting constraints, self-review gate. |
| **Mandatory self-review gate (SCAN/REVIEW/RUN)** | AT-FR-030: Baked into every worker prompt. Workers can't claim completion without evidence. |
| **Disk-backed results** | AT-FR-028: Workers write JSON result files. Messages to orchestrator under 100 tokens. Orchestrator reads files, not conversations. |
| **File ownership enforcement** | AT-FR-027: Pre-spawn conflict detection. File ownership map built and verified before spawning. |
| **Verify-then-trust** | AT-FR-031: Orchestrator validates every result — checks self-review evidence, runs conformance checks. Never accepts claims without validation. |
| **Workers don't commit** | AT-FR-029: Orchestrator is sole git committer. Prevents concurrent index corruption. |
| **Wave acceptance check** | AT-FR-034: Completeness scan + lightweight judges after each wave. Quick quality catch before advancing. |
| **Wave checkpoints** | AT-FR-021: JSON checkpoint after each wave with tasks, files, SHA, verdict. Enables resumability. |
| **Base-SHA refresh** | AT-FR-036: After wave commit, refresh SHA for next wave. |
| **Global wave limit** | AT-FR-024: Default 50. Safety backstop against infinite loops. |
| **Pre-spawn conflict detection** | AT-FR-016/AT-FR-027: File-conflict matrix at compile time + ownership map at spawn time. |
| **Model routing** | AT-FR-060-064: Opus for orchestration/synthesis, Sonnet for workers, Haiku for wave judges. |
| **Scope-escape protocol** | Workers write blocked result if they need files outside manifest. Orchestrator creates follow-up tasks. |
| **Retry with max 3** | AT-FR-032: Failed validation → specific failure sent back, max 3 retries, then blocked. |
| **FIRE loop structure** | The engine's wave execution loop: find ready → spawn → reap results → check acceptance → advance or escalate. |

### What attest skips from /crank + /swarm

| Pattern | Why skipped |
|---|---|
| **Beads integration (bd CLI)** | attest has its own task tracker. |
| **TaskList as bridging mechanism** | attest manages tasks directly in Go. No need to bridge between two systems. |
| **Knowledge flywheel (ao search/forge)** | No cross-session knowledge in v1. |
| **Test-first mode (SPEC/TEST/RED/GREEN waves)** | Powerful but complex. Not in v1 scope. Could be a future mode. |
| **Multiple spawn backends (Claude teams, Codex sub-agents, background tasks, inline)** | attest uses Claude CLI sub-processes directly. Single backend, hardcoded in Go. |
| **Worktree isolation (multi-epic dispatch)** | attest uses shared worktree with file ownership enforcement. Worktree-per-worker is an optimization for multi-epic scenarios that attest doesn't support in v1. |
| **Post-merge naming cleanup** | Handled by the hygiene gate (linting catches naming issues). |
| **Verb disambiguation for worker prompts** | Good practice, can be included in the worker prompt template without being a separate concept. |
| **Per-task commits (--per-task-commits)** | attest defaults to per-wave commits. Per-task could be added later. |
| **Context briefing via ao** | No knowledge flywheel. Workers get their context from the structured prompt. |
| **Crank checkpoint recovery hooks (PostCompact)** | attest handles resumability through wave checkpoints, not Claude-specific hooks. |
| **Spec consistency gate (scripts/spec-consistency-gate.sh)** | AgentOps-specific validation. attest's work plan review covers this. |
| **Cross-cutting validation (SDD patterns)** | The concept is adopted (boundaries → constraints), but the SDD-specific machinery is not. |

---

## 4. /vibe — Code Validation

### How AgentOps does it

**Three-stage pipeline:**

1. **Complexity analysis** — deterministic tools (gocyclo for Go, radon for Python). Grades: A(1-5) through F(31+).
2. **Bug hunt audit** — systematic file-by-file sweep with 8-category checklist (resource leaks, string safety, dead code, hardcoded values, edge cases, concurrency, error handling, HTTP security). Or deep audit with up to 8 parallel explorers.
3. **Council validation** — multi-model judgment (2-4 judges with perspectives).

**Pre-council steps (skipped in --quick mode):**
- Load domain-specific checklists (SQL safety, LLM trust boundary, race condition, etc.)
- Load compiled prevention checks (`.agents/pre-mortem-checks/`)
- Load prior findings from next-work backlog
- Run constraint tests (`go test ./internal/constraints/`) — CRITICAL override
- Metadata verification (file existence, line counts, cross-references)
- Deterministic validation (OL-specific, exit codes 0/1/2)
- Optional Codex review (--mixed only)
- Knowledge flywheel search
- Bug hunt or deep audit sweep
- Test pyramid inventory
- Product context (PRODUCT.md)
- Load spec for spec-compliance judge

**Council validation:**
- With spec: code-review preset (error-paths, api-surface, spec-compliance)
- Without spec: 2 independent judges
- Modes: --quick (inline), --deep (3 judges), --mixed (cross-vendor), --debate (adversarial)

**Post-council:**
- Suppressions applied (default + project-level)
- Pre-mortem prediction correlation
- Ratchet recording
- Finding persistence to registry

**Report structure:**
- Complexity analysis table
- Council verdict (PASS/WARN/FAIL)
- CRITICAL findings (blocks ship)
- INFORMATIONAL findings (include in PR body)
- Recommendation
- Decision gate

### What attest adopts from /vibe

| Pattern | How attest uses it |
|---|---|
| **Deterministic tools first** | AT-FR-043: The hygiene gate runs gofumpt, golangci-lint, go test, gosec, gitleaks, trivy. All confidence 1.0. Failures bounce back before any LLM review. |
| **Complexity analysis** | gocyclo integrated into the hygiene gate. Functions above threshold are flagged. |
| **Constraint tests as CRITICAL override** | The project can define constraint tests. Hygiene gate runs them — failure overrides everything. |
| **Suppression list** | AT-FR-048: Project-level suppression file for known false positives. |
| **Spec-compliance judge perspective** | AT-FR-050: Opus compares implementation against the approved spec, checking requirement traceability. The spec is always available (it's the run artifact). |
| **Council with named perspectives** | Codex: code quality, test coverage, error paths. Opus: spec conformance, architectural coherence. |
| **Context budget rule** | AT-FR-051: Reviewers write analysis to files. Messages to orchestrator are verdict + file path only. |
| **Consensus verdict computation** | AT-FR-059: All PASS → PASS, any FAIL → FAIL, any WARN (no FAIL) → WARN. |
| **Structured remediation fields** | AT-FR-054: Every finding must have fix, why, ref. |
| **Action tier classification** | AT-FR-055: Must fix (critical/high, confidence >= 0.80), should fix (medium), consider (rest above 0.65). Mechanical, not subjective. |
| **Adjudication mode** | AT-FR-057: Hygiene gate findings passed to council as pre-existing context. Reviewers confirm/reject/reclassify. |
| **Repair task creation from findings** | AT-FR-056: "Must fix" and "should fix" findings automatically produce repair tasks. |
| **Completeness scan (TODO/FIXME markers)** | AT-FR-046: Hygiene gate scans for incomplete markers in new code. |
| **Metadata verification** | The hygiene gate verifies file existence, conformance checks pass, requirement IDs are present. |

### What attest skips from /vibe

| Pattern | Why skipped |
|---|---|
| **Bug hunt 8-category checklist** | LLM-based, expensive. The council reviewers cover this territory. Could be added as an optional deep-review mode. |
| **Deep audit sweep (8 parallel explorers)** | Powerful for large codebases but adds complexity and cost. Not in v1. |
| **Domain-specific checklists (SQL, LLM, concurrency)** | Domain-specific. attest is domain-agnostic — it uses the project's own linting/testing tools. |
| **Knowledge flywheel integration** | No cross-session knowledge in v1. |
| **Compiled prevention checks** | AgentOps-specific. |
| **Pre-mortem prediction correlation** | No prediction tracking in v1. |
| **PRODUCT.md / developer-experience perspective** | attest's council focuses on spec conformance, not product/DX concerns. |
| **Codex review as pre-processing (--mixed)** | In attest, Codex IS the independent reviewer, not a pre-processing step. |
| **OL-specific deterministic validation** | Olympus-specific. |
| **Test pyramid inventory (L0-L3, BF1-BF5)** | Useful framework but not codified into v1. The project's own test suite is the authority. |
| **Ratchet recording** | attest uses wave checkpoints instead. |
| **Finding persistence to registry** | No cross-run learning in v1. |
| **Multiple council modes (--quick, --deep, --mixed, --debate)** | attest runs one review mode. No budget optimization. |
| **Crank checkpoint detection** | attest manages its own checkpoints in Go. |

---

## Summary: What attest codifies vs. what's AgentOps-specific

### Codified in Go (the rigid skeleton)

These patterns are implemented as coded logic in the attest binary, not left to LLM prompts:

1. File-conflict matrix and wave computation from dependency DAG
2. Conformance checks per task (required, enforced)
3. Pre-spawn file ownership enforcement
4. Verify-then-trust validation of all worker results
5. Deterministic hygiene gate (tools, exit codes, action tiers)
6. Consensus verdict computation (PASS/WARN/FAIL)
7. Wave checkpoints and ratcheted progress
8. Global wave limit (50)
9. Base-SHA refresh between waves
10. Model routing by task class
11. Scope mode enforcement throughout pipeline
12. Boundary injection (Always/Ask First/Never)

### Expressed as prompts (agent autonomy within stages)

These patterns are injected into agent prompts but not mechanically enforced:

1. Worker prompt template (self-review gate is prompt-based, validation is mechanical)
2. Nudging (follow-up prompts to workers with incomplete self-review)
3. Council perspectives (Codex gets code-review focus, Opus gets spec-conformance focus)
4. Context budget rule (workers are told to write to files, enforced by result validation)

### Deferred to post-v1

1. Cross-session knowledge flywheel
2. Test-first mode (SPEC/TEST/RED/GREEN waves)
3. Multiple spawn backends
4. Worktree isolation per worker
5. Deep audit sweep (parallel explorers)
6. Debate mode (adversarial review rounds)
7. Pre-mortem prediction tracking
8. Domain-specific checklists
