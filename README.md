# fabrikk

A spec-driven autonomous run system for coding agents. Written in Go — stdlib only, single binary.

Give it a spec. Approve the plan. Walk away. Come back to a complete, tested, spec-conformant implementation — or a clear accounting of exactly what remains open and why.

## The Problem

Coding agents are powerful, but they struggle to deliver complete, spec-conformant implementations without constant human oversight. The failure mode isn't lack of capability — it's lack of structure:

- **Drift.** Agents wander from the original spec, building things that weren't asked for while leaving actual requirements unfinished.
- **False completion.** Agents claim work is done when it isn't. Tests are missing, edge cases are ignored, requirements are quietly dropped.
- **Lost focus.** In skill-based or prompt-driven workflows, the LLM has too much latitude. It decides what to do next, which stages to skip, and when "good enough" is good enough. The result is inconsistent quality and incomplete implementations.

These problems get worse with parallelism. Multiple agents working concurrently multiply the opportunities for drift and conflicting changes.

## How It Works

fabrikk uses a **rigid skeleton with convergence-driven loops**. The pipeline stages are coded in Go — no agent gets to skip a stage, invent a new one, or decide the workflow:

```
                         ┌─────────────────────────────────────────┐
                         │           REPAIR LOOP                   │
                         │  Findings create repair tasks that      │
                         │  re-enter the implementation pipeline   │
                         v                                         │
┌──────────┐  ┌───────────┐  ┌────────────────┐  ┌─────────┐  ┌──────────┐
│   Spec   │─>│   Work    │─>│ Implementation │─>│ Hygiene │─>│ Council  │
│  Review  │  │   Plan    │  │  (parallel)    │  │  Gate   │  │  Review  │
└──────────┘  └───────────┘  └────────────────┘  └─────────┘  └──────────┘
     │             │               │                  │              │
  Normalize     Compile        Sub-agents          Lint, test,    Codex +
  spec into     task graph     per wave with       format,        Opus review
  run artifact  with waves     self-review gate    security       against spec
```

Each stage loops until its exit criteria are met — not for a fixed number of rounds. Same issues reappearing? Escalate to a stronger model. The approved spec is the anchor throughout.

## Key Features

### Verify Then Trust

Agent completion claims are **never accepted at face value**. Every worker result is validated mechanically:

1. Self-review evidence checked (did the worker actually SCAN, REVIEW, and RUN?)
2. Conformance checks executed (do expected files exist? do tests pass? does lint pass?)
3. Only after mechanical validation passes is the task marked complete
4. Failed validation → specific failure sent back to the worker (max 3 retries)

### Parallel Execution with Conflict Prevention

Multiple sub-agents work simultaneously within each wave. The orchestrator prevents conflicts:

- **File ownership map** built before spawning — no two workers touch the same file
- **Fresh workers per wave** — each wave gets new sub-agents with clean context
- **Workers don't commit** — the orchestrator is the sole git committer
- **Disk-backed results** — workers write JSON result files, not conversation messages

### Self-Review Gate (SCAN / REVIEW / RUN)

Every worker prompt includes a mandatory 3-step gate that must pass before claiming completion:

- **SCAN** — grep new code for TODO/FIXME/HACK/PLACEHOLDER markers. Any found? Not done.
- **REVIEW** — written self-assessment of every modified file. Gaps? Fix them first.
- **RUN** — execute the task's conformance checks and confirm exit code 0.

The gate is baked into the worker prompt. The orchestrator validates the evidence.

### Hygiene Gate

Deterministic tools run after implementation and before any LLM review. All confidence 1.0:

| Tool | What it checks |
|------|---------------|
| gofumpt | Formatting |
| golangci-lint | 25+ linter rules |
| go test -race | Tests with race detector |
| gosec | Security patterns |
| gitleaks | Secrets in code |
| trivy | Dependency vulnerabilities |

If any check fails, the task bounces back to implementation automatically. No council review tokens are spent on code that fails basic hygiene.

### Multi-Model Council Review

Independent reviewers that compare implementation against the approved spec:

- **Codex** reviews for code quality, test coverage, and error paths
- **Opus** synthesizes findings and checks requirement traceability

Reviewers receive a **mandatory 8-category checklist** to prevent satisfaction bias (stopping at ~10 findings regardless of actual issue count): resource leaks, input safety, dead code, hardcoded values, edge cases, concurrency, error handling, security.

**Domain-specific checklists** are pluggable — SQL safety, LLM trust boundaries, race conditions — loaded based on code patterns.

Every finding must include structured remediation: **fix** (what to do), **why** (root cause), **ref** (file path or requirement ID). Findings are classified mechanically into action tiers:

- **Must Fix** → creates repair task automatically
- **Should Fix** → creates repair task automatically
- **Consider** → logged, does not block

### Test-First Mode (Optional)

When enabled, forces genuine test coverage through a 4-phase pipeline:

1. **SPEC wave** — workers generate test contracts (invariants + test cases)
2. **TEST wave** — workers write failing tests from contracts (no implementation access)
3. **RED gate** — all new tests must FAIL against current code. If a test passes, it doesn't test new behavior — it's flagged for rewrite.
4. **GREEN wave** — workers implement to make tests pass. Test files are immutable — workers can't weaken tests to make them pass.

### Convergence-Driven Loops

Every stage runs until its exit criteria are met, not for a fixed count:

- Implementation nudge reveals gaps? Keep going.
- Code review finds critical or medium findings? Keep going.
- Same issues reappearing without progress? Escalate — upgrade the review mode from standard to deep, trigger parallel deep audit explorers.
- Global safety limit (50 waves) prevents infinite loops.

### Smart Model Routing

The orchestrator picks the right model for each task:

| Role | Model | Why |
|------|-------|-----|
| Planning, synthesis, council | Opus | Needs judgment |
| Implementation, repair work | Sonnet | Cost-effective, focused |
| Wave acceptance, quick checks | Haiku | Fast, cheap |

### Crash Recovery

Every wave writes a checkpoint to disk:

```json
{
  "wave": 3,
  "tasks_completed": ["task-1", "task-2"],
  "files_changed": ["file1.go", "file2.go"],
  "git_sha": "abc123",
  "acceptance_verdict": "PASS"
}
```

If the process dies — crash, terminal close, machine restart — restart picks up from the last checkpoint. Completed waves are **ratcheted** — permanent and never re-executed.

## The Pipeline in Detail

### 1. Spec Review

Ingest source specs in any format. Normalize into a run artifact with stable requirement IDs. Classify **scope mode** (EXPANSION / HOLD SCOPE / REDUCTION) and extract **boundaries** (Always / Ask First / Never). User approves before execution begins.

### 2. Work Plan

Run a quantitative **baseline audit** of the codebase. Compile a task graph from the approved artifact. Build a **file-conflict matrix** — tasks touching the same file are serialized or merged. Each task carries: requirement IDs, file ownership, conformance checks, and required **test levels** (L0 contract, L1 unit, L2 integration, L3 component).

Waves are computed from the dependency DAG — not manually declared. False dependencies (logical ordering without file or data dependencies) are detected and removed.

### 3. Implementation

Dispatch parallel Claude CLI sub-processes per wave. Each worker gets a structured prompt with its task, file manifest, conformance checks, and the self-review gate. The orchestrator validates every result (verify then trust), commits per wave, refreshes the base SHA for the next wave.

### 4. Hygiene Gate

Run the project's own deterministic tooling. All checks must pass before any LLM review. Test level coverage is verified — a task that requires L1 unit tests but produces none bounces back.

### 5. Council Review

Auto-select review intensity based on scope (quick / standard / deep). Codex and Opus review with structured checklists. Findings create repair tasks that loop back to implementation. If the loop stalls, escalate to **deep audit** — parallel explorer agents sweep per-file, then judges adjudicate.

## Quick Start

```bash
# Build
just build

# Prepare a run from a canonical AT-* spec
./bin/fabrikk prepare --spec docs/specs/my-feature-spec.md

# Prepare a free-form spec with explicit LLM normalization consent
./bin/fabrikk prepare --spec docs/specs/my-feature-spec.md --normalize always --allow-llm-normalization

# Review and approve the normalized run artifact
./bin/fabrikk review <run-id>
./bin/fabrikk artifact approve <run-id>

# If the verifier reports needs_revision, either revise the spec or approve intentionally
./bin/fabrikk artifact approve <run-id> --accept-needs-revision

# Check status anytime
./bin/fabrikk status <run-id>

# Find the next ready task
./bin/fabrikk next <run-id>
```

## Architecture

```
CLI (cmd/fabrikk/)
  |-- Engine (internal/engine/)      -- Run lifecycle orchestration
  |   |-- Compiler (internal/compiler/) -- Spec -> task graph (deterministic, no LLM)
  |   |-- Councilflow (internal/councilflow/) -- Multi-model review pipeline
  |   +-- Verifier (internal/verifier/) -- Evidence-based verification
  +-- State (internal/state/)        -- File-based types, RunDir I/O, atomic writes
```

- **Single Go CLI** — pinned project tools and dependencies are tracked in the repository
- **State lives on disk** — all run state in `.fabrikk/runs/<run-id>/` as JSON/JSONL
- **Compiler is deterministic** — task graph generation starts from the approved run artifact
- **Councilflow** — parallel fan-out to persona reviewers across multiple rounds

## Documentation

| Document | Description |
|----------|-------------|
| [Functional Spec](docs/specs/fabrikk-functional-spec.md) | Full product specification (82 requirements, 19 core concepts) |
| [Technical Spec](docs/specs/fabrikk-technical-spec.md) | Implementation architecture and design |
| [How It Works](docs/HOW-IT-WORKS.md) | Detailed pipeline walkthrough with examples |
