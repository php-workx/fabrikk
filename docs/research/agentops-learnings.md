# AgentOps Learnings for attest

Research date: 2026-03-19
Source: `/Users/runger/workspaces/agentops/skills/` (AgentOps v2.25.0)

This document extracts patterns from AgentOps that are directly applicable to attest's pipeline stages. AgentOps implements these as LLM skill prompts; attest should codify them in Go.

---

## 1. Spec Review Stage

### 1.1 Tiered discovery (from /research)

AgentOps uses a 6-tier discovery process with strict ordering:

1. Code-map (fast, authoritative paths)
2. Semantic search (conceptual matches)
3. Git history (scoped `git log` with path filters — never full-repo log)
4. Scoped grep/glob (prevents context overload)
5. Source code (read files identified by tiers 1-4)
6. Prior knowledge (search `.agents/` artifacts)

**Attest takeaway:** When the orchestrator needs to understand a codebase before generating a work plan, use this tiered approach. Codify the tier order so agents don't skip cheap sources or jump to expensive ones.

### 1.2 Spec normalization template

AgentOps's `/brainstorm` enforces a 4-question framework before any planning:

1. What problem does this solve? (concrete pain point)
2. Who benefits? (end users, devs, operators?)
3. What exists today? (current state, prior art)
4. What constraints matter? (perf, compat, security, timeline)

**Attest takeaway:** Use these as mandatory fields in the normalized run artifact. The spec review stage should extract or prompt for answers to all four before producing the artifact.

### 1.3 Contract completeness gate (from /pre-mortem and /council validate mode)

Before a spec can pass review, AgentOps checks 4 items:

1. Mutation + ack sequence is explicit, single-path, non-contradictory
2. Consume-at-most-once flow is crash-safe with atomic boundary + restart recovery
3. Status/precedence has field-level truth table + anomaly reason codes
4. Conformance includes boundary failpoint tests + deterministic replay assertions

Rules:
- Missing/contradictory item → WARN minimum
- Missing deterministic conformance coverage → WARN minimum
- Critical lifecycle invariant not mechanically verifiable → FAIL

**Attest takeaway:** Adapt this as the spec review exit criteria. Not all specs are state machines, but the principle holds: every requirement must be mechanically verifiable, or the spec fails review.

### 1.4 Scope mode commitment (from /pre-mortem)

Before review begins, classify the scope posture:

- **EXPANSION** — greenfield, dream big
- **HOLD SCOPE** — bug fix, maximum rigor
- **REDUCTION** — >15 files, minimize to MVP

The mode is committed in the review packet and prevents silent drift. An agent can't quietly expand scope mid-implementation if the committed mode is HOLD SCOPE.

**Attest takeaway:** Record scope mode in the run artifact. The orchestrator enforces it throughout the pipeline.

### 1.5 Temporal interrogation (from /pre-mortem)

For specs touching 5+ files or 3+ sequential dependencies, simulate the implementation timeline:

- Hour 1: setup — what blocks everything?
- Hour 2: core — what's the riskiest piece?
- Hour 4: integration — what breaks when pieces connect?
- Hour 6+: polish — what gets cut?

**Attest takeaway:** Use this during spec review to identify blocking dependencies and inform wave ordering in the work plan.

---

## 2. Work Plan Stage

### 2.1 Symbol-level implementation detail (from /plan)

AgentOps requires plans to contain symbol-level specs for every file:

- File inventory table listing all files + changes
- Exact function signatures and reuse points with line numbers
- Inline code blocks for new structs/types
- Named test functions with one-line descriptions
- Verification procedures with runnable bash sequences

**Attest takeaway:** The work plan should contain enough detail that a sub-agent doesn't need to rediscover the codebase. File paths, function signatures, and reuse points should be pre-identified.

### 2.2 Pre-planning baseline audit (from /plan)

Before decomposing into tasks, run quantitative commands:

```bash
# Count current state
wc -l internal/engine/*.go
grep -c "func Test" internal/engine/*_test.go
ls internal/compiler/*.go | wc -l
```

Record commands alongside results. This makes completion verifiable — you can re-run the same commands after implementation and compare.

**Attest takeaway:** Codify baseline audits as a mandatory step. The hygiene gate can re-run them to verify progress.

### 2.3 File-conflict matrix (from /plan)

Before assigning tasks to waves, build a conflict matrix:

| File | Tasks |
|------|-------|
| `src/auth.go` | Task 1, Task 3 | ← CONFLICT: serialize or merge |
| `src/config.go` | Task 2 | |

Action on conflict: serialize (move one to later wave) or merge (combine into single task).

Also build a cross-wave shared files registry:

| File | Wave 1 Tasks | Wave 2+ Tasks | Mitigation |
|------|-------------|---------------|------------|
| `src/auth_test.go` | Task 1 | Task 5 | Wave 2 must branch from post-Wave-1 SHA |

**Attest takeaway:** This is directly implementable in Go. The compiler already produces task graphs with owned paths — add a conflict detection pass before wave assignment.

### 2.4 Conformance checks in task descriptions (from /plan)

Every task must include mechanically verifiable conformance checks:

| Acceptance Criteria | Conformance Check |
|---|---|
| "File X exists" | `files_exist: ["X"]` |
| "Function Y is implemented" | `content_check: {file: "src/foo.go", pattern: "func Y"}` |
| "Tests pass" | `tests: "go test ./..."` |
| "Endpoint returns 200" | `command: "curl -s ... \| grep 200"` |

Rules:
- Every task MUST have at least one conformance check
- If acceptance criteria cannot be mechanically verified → flag as underspecified

**Attest takeaway:** This maps directly to the hygiene gate. Each task's conformance checks become the hygiene gate's exit criteria for that task.

### 2.5 Task sizing heuristics (from /plan)

- 1-2 independent files → 1 task
- 3+ independent files → split into sub-tasks (one per file group)
- Shared files between tasks → serialize or assign to same worker

**Attest takeaway:** Use this in the compiler when decomposing requirements into tasks.

### 2.6 False dependency detection (from /plan)

For each declared dependency, verify:

1. Does the blocked task modify files that the blocker also modifies? → **Keep**
2. Does the blocked task read output produced by the blocker? → **Keep**
3. Is the dependency only logical ordering (e.g., "specs before roles")? → **Remove** (false dependencies reduce parallelism)

**Attest takeaway:** Run this check during work plan review. False dependencies reduce wave parallelism.

### 2.7 The "ready front" model (from beads)

Instead of manually declaring waves, compute them from a dependency DAG:

- **Ready front** = set of tasks with all dependencies satisfied
- As tasks close, blocked work automatically becomes ready
- This creates natural wave structure without manual phase declaration

**Attest takeaway:** This is how the engine should drive execution. The task graph + dependency tracking naturally produces waves. `ready` = all deps satisfied + not claimed.

### 2.8 Boundaries (from /plan)

Every plan includes three boundary categories:

- **Always:** non-negotiable requirements (inject as cross-cutting constraints on every task)
- **Ask First:** decisions needing human input
- **Never:** explicit out-of-scope items

**Attest takeaway:** Record these in the run artifact. The orchestrator injects "Always" constraints into every sub-agent prompt and blocks on "Ask First" items.

---

## 3. Implementation Stage

### 3.1 The Ralph pattern (from /swarm)

Core principle: **fresh context every wave, scheduler-heavy lead, worker-light workers, disk-backed state.**

- Workers are ephemeral — spawned for one wave, torn down after
- The lead (orchestrator) persists across waves and manages all state
- Workers write results to disk (JSON files), NOT in conversation context
- This prevents context explosion from N workers × conversation length

**Attest takeaway:** This is the execution model for attest. The Go orchestrator is the persistent lead. Claude CLI sub-processes are ephemeral workers. Results go to disk.

### 3.2 Worker prompt template (from /swarm)

Every worker receives a structured prompt with:

1. Worker identity and task assignment
2. File manifest (files permitted to modify)
3. Instructions: implement → write result JSON → send completion signal
4. Result file format (JSON with status, artifacts, self-review evidence)
5. Context budget rule: messages to lead must be under 100 tokens
6. Mandatory self-review gate (3 steps before completion)

**Attest takeaway:** Codify this as a Go template. The orchestrator generates the worker prompt from task metadata.

### 3.3 Mandatory self-review gate (from /swarm — the nudging mechanism)

Before a worker can claim completion, it must complete 3 steps:

1. **SCAN** for incomplete markers in newly added lines:
   ```bash
   git diff HEAD | grep '^+' | grep -v '^+++' | grep -inE 'TODO|FIXME|HACK|XXX|PLACEHOLDER|UNIMPLEMENTED|STUB|WIP|NOCOMMIT'
   ```
   Any match → NOT done, fix first.

2. **REVIEW** — brutally honest self-assessment of every modified file:
   - What changed and why
   - Gaps or incomplete implementations
   - Missing error handling or edge cases
   - Missing tests
   - Hacky code
   If issues found → fix before proceeding.

3. **RUN** verification:
   - Execute the test/lint/build command from the task description
   - Confirm exit code 0

Result file includes self-review evidence:
```json
{
  "self_review": {
    "scan_clean": true,
    "review": "<written review text>",
    "files_reviewed": ["path/a", "path/b"],
    "verification_command": "go test ./...",
    "verification_exit_code": 0
  }
}
```

**Attest takeaway:** This IS the nudging mechanism described in the functional spec. The orchestrator streams this as a follow-up prompt. The key insight: the self-review gate is baked into the worker prompt, not sent as a separate nudge. Additional nudges happen only if the self-review evidence is missing or failing.

### 3.4 Validation contract — verify then trust (from /swarm)

Agent completion claims are NOT trusted. The lead must verify:

1. Check self-review evidence exists and passes
2. Execute validation checks from task metadata:
   - `files_exist` → all expected files present?
   - `content_check` → expected patterns found?
   - `tests` → test command passes?
   - `lint` → lint command passes?
3. On validation PASS → mark task completed
4. On validation FAIL → increment retry count, send specific failure back to worker
5. Max 3 retries → mark task blocked, escalate

**Attest takeaway:** This is the bridge between implementation and the hygiene gate. The orchestrator runs these checks automatically — it's coded in Go, not left to LLM judgment.

### 3.5 File ownership enforcement (from /swarm)

Pre-spawn conflict detection:

```
for task in wave_tasks:
    for file in task.metadata.files:
        if file in all_files:
            CONFLICT: serialize or isolate
        all_files[file] = task.id
```

Display ownership table before spawning. Workers are told: "Do NOT modify files outside this manifest."

If a worker needs files outside its manifest → write blocked result + append to scope-escapes log.

**Attest takeaway:** Directly implementable in Go. The engine tracks file ownership per task and enforces it before dispatching sub-agents.

### 3.6 Git commit policy (from /swarm)

- **Workers MUST NOT commit** — the lead is the sole committer
- This prevents concurrent git index corruption
- Workers write files only, signal completion via result files
- Lead commits per-wave (default) or per-task (when attribution needed)

**Attest takeaway:** The Go orchestrator owns all git operations. Sub-agents are not given git write access.

### 3.7 Wave acceptance check (from /crank)

After each wave completes, before advancing:

1. **Completeness scan:** grep newly added lines for TODO/FIXME/STUB markers. Any found → FAIL.
2. **Inline judges:** 2 haiku-tier agents check spec compliance and error paths. Each returns PASS/WARN/FAIL.
3. **Per-wave vibe gate:** targeted code quality check on wave's changed files. CRITICAL findings → FAIL.
4. **Aggregate verdict:**
   - Evidence validation fails → FAIL
   - Both judges PASS → PASS
   - Any judge FAIL → FAIL
   - Otherwise → WARN
5. **WARN handling:** Create fix tasks, execute inline, re-check. 2 failed attempts → FAIL.

**Attest takeaway:** This is a lightweight per-wave hygiene check. The full hygiene gate and council review run after all waves, but this quick check catches obvious issues early.

### 3.8 Wave checkpoints (from /crank)

After each wave, write a checkpoint:

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

**Attest takeaway:** This enables resumability. If the process dies, restart reads the last checkpoint and knows which wave to resume from.

### 3.9 Base-SHA refresh between waves (from /swarm)

After wave N commits, update the base SHA for wave N+1:

```bash
WAVE_COMMIT_SHA=$(git rev-parse HEAD)
```

Cross-reference next wave's file manifests against current wave's changes. If overlap → next wave workers must see current wave's code (not stale pre-wave code).

**Attest takeaway:** Since attest uses a shared worktree (not per-agent worktrees), this happens naturally. But the orchestrator should still track and log it for debugging.

### 3.10 Grep-for-existing before implementing (from /implement)

Before implementing new functions, grep the entire codebase for existing implementations. This prevents utility duplication — a documented failure where `estimateTokens` was duplicated when a 5-second grep would have found the existing implementation.

**Attest takeaway:** Include this as a step in the worker prompt template. "Before creating new functions, grep for existing implementations."

### 3.11 Global wave limit (from /crank)

Hard limit of 50 waves across the entire epic. Prevents infinite loops on circular dependencies or cascading failures.

**Attest takeaway:** Codify a maximum iteration count in the orchestrator as a safety valve. This doesn't contradict convergence-driven loops — it's the escalation backstop.

---

## 4. Hygiene Gate Stage

### 4.1 Three-stage architecture (from /vibe)

AgentOps's code quality pipeline runs three sequential stages:

1. **Complexity analysis** — deterministic tools (gocyclo for Go, radon for Python) find hotspots
2. **Proactive audit** — systematic code sweep with 8-category checklist
3. **Multi-model council** — judges validate against spec and coding standards

**Attest takeaway:** The hygiene gate should run stages 1 and parts of 2 (the deterministic parts). Stage 3 is the council review — a separate pipeline stage.

### 4.2 Deterministic checks always run first (from /vibe and /codereview)

| Tool | What it checks | Confidence |
|------|---------------|------------|
| gocyclo | Cyclomatic complexity per function | 1.0 |
| golangci-lint | 25+ linter rules | 1.0 |
| gofumpt | Formatting | 1.0 |
| go test | Test execution | 1.0 |
| gosec | Go security patterns | 1.0 |
| gitleaks | Secrets in code | 1.0 |
| trivy | Dependency vulnerabilities | 1.0 |

These run before any LLM-based review. If they fail, bounce back to implementation immediately.

**Attest takeaway:** This IS the hygiene gate. All of these are runnable with `just pre-commit` and `just check` in the attest project. The orchestrator runs these commands and parses exit codes.

### 4.3 Constraint tests as mechanical override (from /vibe)

AgentOps supports "constraint tests" — special Go tests that check mechanical invariants:

```bash
go test ./internal/constraints/ -run TestConstraint
```

If these fail → automatic FAIL verdict, no council needed. Constraint tests catch: ghost references, TOCTOU races, dead code, naming violations.

**Attest takeaway:** The target project can define its own constraint tests. The hygiene gate runs them with confidence 1.0.

### 4.4 The 8-category audit checklist (from /bug-hunt)

For each file in scope, check all 8 categories (no skipping):

| Category | What to check |
|----------|--------------|
| Resource Leaks | Unclosed files/connections, missing defer, goroutine leaks |
| String Safety | Input validation, format string injection, path traversal, SQL injection |
| Dead Code | Unreachable branches, unused imports/variables/functions |
| Hardcoded Values | Magic numbers, hardcoded paths/URLs/credentials |
| Edge Cases | Nil/zero handling, empty collections, boundaries, integer overflow |
| Concurrency | Data races, missing locks, lock ordering, channel misuse |
| Error Handling | Swallowed errors, generic catch-all, missing propagation |
| HTTP/Web Security | XSS, path traversal, CORS, CSRF, SSRF, rate limiting |

Coverage certification per file: report at least 1 finding OR explicitly certify all 8 clean.

**Attest takeaway:** This is too expensive for the hygiene gate (it's LLM-based), but it's valuable context for the council review. The council should receive the 8-category checklist as a review framework.

### 4.5 Action tier classification (from /codereview)

Findings are classified mechanically (not subjectively):

- **Must Fix:** (critical OR high) AND confidence >= 0.80
- **Should Fix:** medium severity OR (high with confidence 0.65-0.79)
- **Consider:** everything else above 0.65 floor

**Attest takeaway:** Use this classification in the council review stage. It determines whether a finding creates repair work (must fix) or is advisory (consider).

### 4.6 Suppression list (from /vibe)

False positives can be suppressed via:

- Default suppression list (formatting, naming, known-safe patterns)
- Project-level overrides (`.agents/vibe-suppressions.jsonl`)

**Attest takeaway:** Support a suppression file in the run directory. Projects can declare known false positives that shouldn't trigger repair work.

---

## 5. Council Review Stage

### 5.1 Context budget rule (from /council)

**The most important pattern for multi-model review:**

Judges write ALL analysis to files. Messages to the lead are MINIMAL:

```json
{"type": "verdict", "verdict": "PASS", "confidence": "HIGH", "file": ".agents/council/judge-1.md"}
```

The lead reads judge output files during consolidation. This prevents N judges × full-analysis from flooding the orchestrator's context window.

**Attest takeaway:** Codify this in the orchestrator. Codex review output goes to a file. Opus reads the file for synthesis. No large payloads in inter-process communication.

### 5.2 Consensus verdict computation (from /council)

Simple, deterministic rules:

- **PASS:** All judges PASS (or majority PASS, none FAIL)
- **WARN:** Any judge WARN, none FAIL
- **FAIL:** Any judge FAIL

**Attest takeaway:** Implement this in Go. It's a pure function.

### 5.3 Named perspective presets (from /council)

Instead of generic "Judge 1, Judge 2", use named perspectives:

| Preset | Perspectives |
|--------|-------------|
| code-review | error-paths, api-surface, spec-compliance |
| security-audit | vulnerability, attack-surface, data-flow |
| plan-review | gaps, reality, scope, completeness |

Each judge gets a perspective description injected into their prompt.

**Attest takeaway:** The council should assign specific perspectives to reviewers. Codex gets "code quality, test coverage, error paths." Opus gets "spec conformance, architectural coherence."

### 5.4 Explorer-judge architecture (from /codereview)

Two-phase review:

1. **Explorers** (parallel, narrow, deep) — each checks one concern across files
2. **Judge** (single, broad, synthesizing) — validates explorer findings, adds cross-cutting issues

Explorers: correctness, security, reliability, test adequacy, error handling, API/contract, concurrency, spec verification.

The judge is adversarial — it validates that explorer findings actually exist, challenges severity ratings, and groups findings by root cause.

**Attest takeaway:** For large changesets, spawn multiple explorers before the council synthesis. Each explorer focuses on one concern. This finds 3x more issues than a single-pass review.

### 5.5 Adjudication mode (from /council)

When explorers have already swept files, judges shift from discovery to adjudication:

1. CONFIRM or REJECT each sweep finding
2. RECLASSIFY severity where wrong
3. ADD cross-file findings explorers couldn't see
4. Do NOT repeat sweep findings verbatim

**Attest takeaway:** If the hygiene gate produces findings, pass them to the council as pre-existing context. The council confirms/rejects/reclassifies rather than rediscovering.

### 5.6 Structured remediation fields (from /council)

Every finding MUST include:

- `fix` — specific action to resolve
- `why` — root cause or rationale
- `ref` — file path, spec anchor, or doc reference

Findings without these fields get fallback values to ensure all findings are actionable.

**Attest takeaway:** Require this structure in council output. It feeds directly into repair task creation.

### 5.7 Debate protocol with anti-anchoring (from /council --debate)

For high-stakes disagreements, two-round adversarial review:

**Round 1:** Independent assessment (no communication between judges)

**Round 2:** Each judge must:
1. Restate own R1 position BEFORE reviewing others (prevents anchoring)
2. Steel-man the strongest opposing argument
3. Challenge with SPECIFIC evidence (file:line, factual error)
4. Acknowledge points that strengthen analysis
5. Revise or confirm with specific reasoning

Verdict change requires specific technical evidence. "Judge 2 made a good point" is NOT sufficient.

**Convergence detection:** If R1 had disagreement and R2 is unanimous → flag for potential anchoring.

**Attest takeaway:** This is the escalation path when Codex and Opus disagree. Round 2 with evidence requirements prevents rubber-stamp consensus.

### 5.8 Repair work creation (from /post-mortem)

Council findings that require code changes produce repair tasks:

```json
{
  "title": "Add input validation for spec paths",
  "type": "tech-debt",
  "severity": "high",
  "source": "council-finding",
  "description": "...",
  "evidence": "which finding motivates this"
}
```

These route back to the implementation loop with the finding as context.

**Attest takeaway:** The orchestrator creates repair tasks from council findings with severity >= "should fix". These enter the task graph and go through the full pipeline (implementation → hygiene → council).

---

## 6. Cross-Cutting Patterns

### 6.1 Resumability via checkpoints (from /crank, /implement, /ratchet)

State is persisted after every major transition:

- Task status changes → written to disk immediately
- Wave completion → checkpoint JSON with SHA, tasks, verdict
- Stage completion → ratchet chain entry (append-only JSONL)
- Implementation checkpoints → notes on the task record

Resumption reads the last checkpoint and continues from there.

**Attest takeaway:** The `RunDir` already supports this pattern. Ensure every stage transition writes state atomically before proceeding.

### 6.2 The ratchet model (from /ratchet)

Progress is permanent. You can't un-ratchet.

```
Progress = Chaos × Filter → Ratchet
```

Each pipeline stage is a ratchet step:
- research → plan → implement → vibe → post-mortem
- Each step outputs an artifact
- Each step records completion in `chain.jsonl`
- Gates prevent backsliding

**Attest takeaway:** Directly applicable. Once a wave passes the hygiene gate and council review, it's ratcheted. Repair tasks from later reviews don't re-open completed waves — they create new tasks in new waves.

### 6.3 Model routing in practice (from all skills)

| Role | Model | Why |
|------|-------|-----|
| Orchestrator/lead | Opus | Coordination, synthesis, judgment |
| Implementation workers | Sonnet | Cost-effective, 3-5x cheaper |
| Inline wave acceptance judges | Haiku | Fast, cheap, sufficient for completeness scan |
| Explorers (pre-sweep) | Sonnet | Deep per-file analysis |
| Council judges | Opus (thorough) or Sonnet (balanced) | Configurable by review importance |
| Quick validation | Haiku | 60s timeout, low cost |

**Attest takeaway:** This maps directly to AT-FR-039 through AT-FR-043. The orchestrator routes by task class automatically.

### 6.4 Knowledge flywheel (from /athena, /forge, /flywheel)

AgentOps compounds knowledge across sessions:

1. Sessions produce transcripts
2. Forge mines transcripts → candidates (Tier 0)
3. High-confidence candidates auto-promote to Tier 1 (learnings)
4. Citations increase during injection in future sessions
5. Post-mortem updates confidence based on citations
6. Pattern-quality learnings promote to Tier 2 (patterns)
7. Stale/uncited learnings archive automatically

**Attest takeaway:** Not directly needed for v1, but the principle matters: run results should feed back into future run planning. A run that discovers "this test framework is flaky" should inform the next run's spec review.

### 6.5 Finding persistence and institutional memory (from /pre-mortem, /post-mortem)

Findings are persisted to a registry with dedup keys:

```json
{
  "dedup_key": "missing-input-validation-spec-paths",
  "pattern": "what to look for",
  "detection_question": "how to spot it",
  "applicable_when": ["go", "cli", "file-io"],
  "confidence": 0.85
}
```

These get compiled into prevention checks that run automatically in future pre-mortems.

**Attest takeaway:** Store council findings in a registry within the run directory. If a finding keeps reappearing across repair cycles, escalate its severity.

### 6.6 Session handoff pattern (from /handoff)

At session end:
```
COMPLETED: X
IN PROGRESS: Y
NEXT: Z
BLOCKER: none
KEY DECISION: rationale
```

At session start: read notes to reconstruct context without scrolling history.

**Attest takeaway:** If the process is interrupted, the run state serves as the handoff. The status command (AT-FR-044) should produce this format.

---

## Summary: What attest should codify in Go

| AgentOps Pattern | attest Implementation |
|---|---|
| 6-tier discovery | Spec review: structured codebase analysis |
| Contract completeness gate | Spec review: exit criteria for approval |
| Scope mode commitment | Run artifact: recorded, enforced throughout |
| File-conflict matrix | Compiler: conflict detection before wave assignment |
| Conformance checks per task | Task metadata: hygiene gate uses these |
| Ready front model | Engine: compute waves from dependency DAG |
| Ralph pattern (fresh workers) | Engine: ephemeral Claude CLI sub-processes |
| Mandatory self-review gate | Worker prompt: baked into every sub-agent |
| Verify-then-trust | Engine: validate results before accepting |
| Deterministic checks first | Hygiene gate: run tools, parse exit codes |
| Context budget rule | Council: judge output → files, not messages |
| Consensus verdict computation | Council: pure function in Go |
| Action tier classification | Council: mechanical severity → repair routing |
| Wave checkpoints | Engine: JSON checkpoint after each wave |
| Ratchet model | Engine: completed waves are permanent |
| Model routing by role | Engine: Opus/Sonnet/Haiku by task class |
