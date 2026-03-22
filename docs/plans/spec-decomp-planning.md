# Plan: Spec Decomposition & Planning Improvements

**Status:** Planned
**Branch:** TBD
**Date:** 2026-03-21

---

## 1. Problem Statement

attest's planning pipeline (prepare → tech-spec → plan → compile) is structurally complete and gated, but the **quality of decomposition is low**. The compiler groups up to 4 requirements per task based on text proximity, infers file paths from keywords, and produces no symbol-level detail. The result: tasks are structurally correct but informationally thin — agents implementing them must rediscover the codebase from scratch.

Three specific gaps:

1. **Tasks are too large.** Grouping up to 4 requirements per task splits agent attention. A task with 4 requirements has ambiguous "done" conditions, tests that span multiple concerns, and reviews that cover too much surface area. One requirement per task keeps agents focused.

2. **No codebase exploration before planning.** The compiler works from requirement text alone. It infers paths via keyword matching ("compiler" → `internal/compiler`), never reads actual files to find function signatures, struct definitions, or reuse points. Slices get vague `FilesLikelyTouched` instead of concrete symbol-level detail.

3. **No council review of execution plans.** The structural review catches graph issues (cycles, duplicates, missing fields) but not design issues (scope gaps, false dependencies, missing file conflicts, underspecified acceptance criteria). A multi-model council review — like the one we already have for tech specs — would catch these before implementation begins.

### Prior Art

AgentOps `/plan` (studied in `.agents/research/2026-03-21-agentops-plan-decomposition.md`) demonstrates a 10-step planning workflow with:

- **Codebase exploration** via dispatched Explore agents requesting symbol-level detail
- **Pre-planning baseline audit** grounding the plan in verified numbers (wc, grep, ls)
- **Symbol-level implementation detail** for every file: exact function signatures, struct definitions, reuse points with `file:line` references, named test functions
- **File-conflict matrix** and **cross-wave shared file registry** for wave computation
- **Conformance checks** per issue: `files_exist`, `content_check`, `tests`, `command`
- **Boundaries:** Always (non-negotiable), Ask First (needs human), Never (out-of-scope)
- **Dependency necessity validation** removing false deps that only impose logical ordering
- **Issue granularity:** 1-2 files per issue, split at 3+

## 2. Design

### 2.1 Task Granularity: One Requirement Per Task

**Change:** `maxGroupSize` from 4 to 1 in `internal/compiler/compiler.go`.

```go
// Before:
const maxGroupSize = 4

// After:
const maxGroupSize = 1
```

**Exception:** Requirements that reference the same function/struct and can't be implemented independently remain grouped. The grouping logic in `shouldStartNewGroup` already handles this via theme/proximity checks — with `maxGroupSize = 1`, the theme check becomes the sole grouping mechanism.

**Impact on existing tests:** Tests that assert specific task counts will change. The compiler produces more, smaller tasks. Coverage remains 100% — every requirement still maps to exactly one task.

**Why:** An agent working on "Implement AT-FR-001" has one clear requirement, writes tests for that one thing, and gets reviewed on that scope. No ambiguity about what "done" means. Parallel execution improves because more tasks are independent.

### 2.2 Wave Computation with File-Conflict Analysis

**Currently:** No explicit wave computation. Tasks have `DependsOn` but no wave assignment. `GetPendingTasks()` returns all tasks with satisfied dependencies — implicit parallelism at execution time.

**Change:** Add explicit wave computation after task compilation, with file-conflict analysis.

#### Wave assignment algorithm

```go
// ComputeWaves assigns each task to a wave based on its dependency depth.
func ComputeWaves(tasks []state.Task) []Wave {
    depth := make(map[string]int) // task ID → wave number (0-indexed)

    // Topological sort to compute depth
    for _, task := range tasks {
        computeDepth(task.TaskID, tasks, depth)
    }

    // Group by depth
    waves := groupByDepth(tasks, depth)

    // Validate: no file conflicts within a wave
    for i := range waves {
        conflicts := detectFileConflicts(waves[i].Tasks)
        if len(conflicts) > 0 {
            // Serialize conflicting tasks: move later ones to next wave
            waves = resolveConflicts(waves, i, conflicts)
        }
    }

    return waves
}
```

#### New types

```go
// Wave groups tasks that can execute in parallel.
type Wave struct {
    WaveID int
    Tasks  []string // task IDs
}

// FileConflict records two tasks claiming the same file.
type FileConflict struct {
    FilePath string
    TaskA    string
    TaskB    string
}
```

#### File-conflict matrix

Before assigning tasks to waves, build a conflict matrix from `Scope.OwnedPaths`:

| File | Tasks | Conflict? |
|---|---|---|
| `internal/engine` | task-1, task-3 | YES → serialize |
| `internal/compiler` | task-2 | no |
| `cmd/attest` | task-4 | no |

Conflicts are resolved by moving the later task (by Order) to the next wave. This preserves the dependency DAG while preventing parallel workers from touching the same files.

#### Cross-wave shared file registry

After wave assignment, build a registry of files that appear in multiple waves:

| File | Wave 1 Tasks | Wave 2+ Tasks | Mitigation |
|---|---|---|---|
| `internal/state/types.go` | task-1 | task-5 | Wave 2 worktree must branch from post-Wave-1 SHA |

This registry is displayed during `attest plan review` and stored in the execution plan for the orchestrator to enforce base-SHA refresh between waves.

#### False dependency removal

For each `DependsOn` entry, verify the dependency is real:

1. Does the blocked task modify files that the blocker also modifies? → **Keep**
2. Does the blocked task read output produced by the blocker? → **Keep** (checked via `RequirementIDs` overlap)
3. Is the dependency only lane-based ordering (same requirement family)? → **Remove** if no file overlap

This increases parallelism by removing artificial serialization from the current lane-based dependency scheme.

#### Integration

- `CompileExecutionPlan` calls `ComputeWaves` after task creation
- Wave assignments stored on `ExecutionSlice` (new `Wave int` field)
- `renderExecutionPlanMarkdown` shows wave groupings
- `ReviewExecutionPlan` validates wave assignments (no intra-wave file conflicts)

### 2.3 Codebase Exploration Before Planning

**Currently:** `DraftExecutionPlan` calls `compiler.Compile(artifact)` which works from raw requirement text. No codebase exploration.

**Change:** Add an optional exploration step before plan drafting. When the engine has access to the working directory, it scans the codebase to produce concrete file paths, function signatures, and reuse points.

#### Exploration scope

The exploration is **deterministic** (no LLM) and uses `grep`, `go doc`, and AST-level analysis:

1. **File discovery:** For each inferred `OwnedPaths`, verify the directory exists and list Go files
2. **Symbol extraction:** Parse Go files to extract exported function signatures and type definitions
3. **Test discovery:** Find existing test files and extract test function names
4. **Reuse point identification:** For each requirement keyword, grep for existing implementations

#### ExplorationResult type

```go
type ExplorationResult struct {
    FileInventory []FileInfo     // actual files found
    Symbols       []SymbolInfo   // functions, types, interfaces
    TestFiles     []TestFileInfo // existing tests
    ReusePpoints  []ReusePoint   // existing code to build on
}

type FileInfo struct {
    Path     string
    Exists   bool
    IsNew    bool   // needs to be created
    LineCount int
}

type SymbolInfo struct {
    FilePath  string
    Line      int
    Kind      string // "func", "type", "interface", "method"
    Signature string // e.g., "func (s *Store) Add(l *Learning) error"
}

type ReusePoint struct {
    FilePath  string
    Line      int
    Symbol    string
    Relevance string // why this is relevant to the plan
}
```

#### Integration

- New engine method: `ExploreForPlan(ctx context.Context) (*ExplorationResult, error)`
- Called from `DraftExecutionPlan` before compilation
- Results populate `ExecutionSlice.FilesLikelyTouched` with verified paths
- Results populate a new `ExecutionSlice.ImplementationDetail` field (symbol-level specs)
- The exploration result is persisted to the run directory for the council review to reference

#### What we skip from AgentOps

AgentOps dispatches an LLM-powered Explore agent. We use deterministic Go AST parsing and grep instead. This keeps the compiler pure while still producing concrete symbol-level detail. The LLM-based exploration can be added as an optional flag (`--deep-explore`) for complex specs.

### 2.4 Conformance Checks Per Task

**Currently:** Tasks get `RequiredEvidence` (coarse: `test_pass`, `quality_gate_pass`, `file_exists`). No per-task `content_check` or `files_exist` validation blocks.

**Change:** Add a `ValidationChecks` field to `ExecutionSlice` and `Task` with typed assertions.

#### Types

```go
type ValidationCheck struct {
    Type    string // "files_exist", "content_check", "tests", "command", "lint"
    Config  map[string]string // type-specific configuration
}
```

Examples:

```go
// files_exist: verify file was created
ValidationCheck{Type: "files_exist", Config: map[string]string{"paths": "internal/learning/index.go"}}

// content_check: verify function exists
ValidationCheck{Type: "content_check", Config: map[string]string{"file": "internal/learning/store.go", "pattern": "func.*AssembleContext"}}

// tests: run specific test
ValidationCheck{Type: "tests", Config: map[string]string{"command": "go test -run TestAssembleContext ./internal/learning/..."}}
```

#### Derivation

The compiler derives validation checks from:

1. **Exploration results:** If a file is marked `IsNew`, add `files_exist` check
2. **Requirement text:** If requirement mentions "test", add `tests` check with inferred test command
3. **Symbol extraction:** If a new function is expected, add `content_check` with function signature pattern
4. **Quality gate:** Always include the project's quality gate command

#### Integration

- `ValidationChecks` field on `ExecutionSlice` and `Task`
- `ReviewExecutionPlan` validates that every slice has at least one check (warning if missing)
- The verifier can run these checks in addition to its existing pipeline
- `MarshalTicket` renders validation checks in a `## Validation` section

### 2.5 Council Review of Execution Plans

**Currently:** `ReviewExecutionPlan` runs deterministic structural checks only. No multi-model council review.

**Change:** Wire council review for execution plans, similar to `CouncilReviewTechnicalSpec`.

#### Review perspectives

| Persona | Focus | Backend |
|---|---|---|
| Scope Reviewer | Are slices well-scoped? File conflicts? Scope gaps? | codex |
| Dependency Reviewer | Are dependencies correct? False deps? Missing deps? | codex |
| Completeness Reviewer | Does every requirement have a covering slice? Acceptance criteria sufficient? | claude |
| Feasibility Reviewer | Are slices implementable? Too large? Missing context? | claude |

#### Integration

- New engine method: `CouncilReviewExecutionPlan(ctx context.Context, cfg councilflow.CouncilConfig) (*councilflow.CouncilResult, error)`
- Called from `attest plan review --council` (opt-in initially, default later)
- The council receives: execution plan markdown + exploration results + tech spec
- Council findings become `ReviewFinding` entries on the `ExecutionPlanReview`
- Blocking council findings prevent plan approval

#### MVP mode (default)

- 1 round, 4 reviewers, `--skip-approval` for personas
- Structural review runs first; if it fails, council is skipped
- Council runs only if structural review passes

### 2.6 Boundaries on RunArtifact

**Currently:** No boundary concept. No mechanism to inject cross-cutting constraints.

**Change:** Add `Boundaries` to `RunArtifact`.

```go
type Boundaries struct {
    Always   []string `json:"always,omitempty"`    // Non-negotiable constraints
    AskFirst []string `json:"ask_first,omitempty"` // Need human input
    Never    []string `json:"never,omitempty"`     // Explicit out-of-scope
}
```

- `Always` constraints are injected into every task's `Constraints` field during enrichment
- `AskFirst` items cause the engine to block and request human input
- `Never` items are validated during verification — if a worker touches out-of-scope paths, verification fails
- Boundaries can be set during `attest prepare` or via a `attest boundaries` command

### 2.7 Implementation Detail on Execution Slices

**Currently:** Slices have `Title`, `Goal`, `Notes` (prose) but no structured implementation guidance.

**Change:** Add `ImplementationDetail` to `ExecutionSlice`.

```go
type ImplementationDetail struct {
    FilesToModify []FileChange  `json:"files_to_modify,omitempty"`
    SymbolsToAdd  []string      `json:"symbols_to_add,omitempty"`  // "func (s *Store) Maintain() error"
    SymbolsToUse  []string      `json:"symbols_to_use,omitempty"`  // "withLock at store.go:380"
    TestsToAdd    []string      `json:"tests_to_add,omitempty"`    // "TestMaintain_DedupMerge"
}

type FileChange struct {
    Path   string `json:"path"`
    Change string `json:"change"` // "Add Maintain method", "Modify GarbageCollect"
    IsNew  bool   `json:"is_new,omitempty"`
}
```

- Populated by the exploration step (§2.3)
- Rendered in the execution plan markdown for human review
- Rendered in ticket body for worker agents
- Council reviewers see this detail and can validate feasibility

## 3. Files to Modify

| File | Change | Est. lines |
|------|--------|-----------|
| `internal/compiler/compiler.go` | Change `maxGroupSize` to 1, add `ComputeWaves`, `detectFileConflicts`, `resolveConflicts`, false dep removal | +120 |
| `internal/compiler/compiler_test.go` | Tests for wave computation, conflict detection, single-requirement tasks | +150 |
| `internal/compiler/explore.go` | **NEW** — Deterministic codebase exploration (file discovery, symbol extraction, test discovery) | ~200 |
| `internal/compiler/explore_test.go` | **NEW** — Exploration tests | ~100 |
| `internal/state/types.go` | Add `Wave`, `FileConflict`, `ValidationCheck`, `Boundaries`, `ImplementationDetail`, `FileChange` types. Add `Wave` field to `ExecutionSlice`. Add `ValidationChecks` to `Task`. Add `Boundaries` to `RunArtifact`. | +50 |
| `internal/engine/plan.go` | Add `ExploreForPlan`, `CouncilReviewExecutionPlan`. Update `DraftExecutionPlan` to call exploration + wave computation. Update `ReviewExecutionPlan` to validate waves and run optional council. | +150 |
| `internal/engine/plan_test.go` | Tests for exploration integration, council review wiring, wave validation | +100 |
| `internal/ticket/format.go` | Render `ValidationChecks` and `ImplementationDetail` in ticket body | +30 |
| `cmd/attest/main.go` | Add `--council` flag to `plan review`, add `boundaries` command | +20 |

**Estimated total:** ~920 new/modified lines.

## 4. Implementation Order

### Wave 1: Foundation (no dependencies between these)

1. **Task granularity** — Change `maxGroupSize` to 1. Update tests. This is a one-line change with test adjustments.

2. **Validation checks type** — Add `ValidationCheck` to `state/types.go` and `ExecutionSlice`. Wire through `MarshalTicket`. No behavioral change yet — just the type and rendering.

3. **Boundaries type** — Add `Boundaries` to `RunArtifact` in `state/types.go`. No behavioral enforcement yet — just the type.

### Wave 2: Wave computation (depends on Wave 1)

4. **Wave computation** — Implement `ComputeWaves`, `detectFileConflicts`, `resolveConflicts` in `compiler.go`. Add `Wave` field to `ExecutionSlice`. Update `DraftExecutionPlan` to call wave computation. Update `renderExecutionPlanMarkdown` to show wave groupings.

5. **False dependency removal** — Implement dependency necessity validation. Remove lane-based deps where no file overlap exists.

6. **Wave validation in review** — Update `ReviewExecutionPlan` to validate no intra-wave file conflicts. Add cross-wave shared file registry to review output.

### Wave 3: Exploration (depends on Wave 1)

7. **Codebase exploration** — Implement `ExploreForPlan` in `compiler/explore.go`. Go AST parsing for symbol extraction. File discovery and test file scanning.

8. **Implementation detail** — Add `ImplementationDetail` to `ExecutionSlice`. Populate from exploration results. Render in ticket body.

9. **Conformance check derivation** — Derive `ValidationChecks` from exploration results (files_exist for new files, content_check for new functions, tests for test files).

### Wave 4: Council review (depends on Waves 2-3)

10. **Council review wiring** — Implement `CouncilReviewExecutionPlan`. Wire personas (scope, dependency, completeness, feasibility). MVP mode: 1 round, 4 reviewers.

11. **CLI integration** — Add `--council` flag to `attest plan review`. Default to MVP mode (1 round). Display findings.

### Wave 5: Boundaries (depends on Wave 1)

12. **Boundary enforcement** — `Always` constraints injected into task `Constraints`. `Never` items validated during verification. `AskFirst` blocks engine progression.

13. **CLI command** — `attest boundaries set --always "..." --never "..."` or parse from tech spec `## Boundaries` section.

## 5. Design Decisions

### Deterministic exploration, not LLM-based

AgentOps dispatches an LLM Explore agent for codebase understanding. We use Go's `go/parser` and `go/ast` packages for symbol extraction, plus `filepath.Walk` and `grep` for file discovery. This keeps the compiler deterministic and avoids LLM costs during planning. An LLM-based `--deep-explore` flag can be added later for complex multi-language projects.

### Wave computation at planning time, not execution time

Currently, waves are implicit (computed at execution time by `GetPendingTasks`). We move wave computation to planning time so:
- The plan document shows explicit wave groupings for human review
- The council can validate wave assignments
- File conflicts are caught before any work begins
- The orchestrator knows the expected number of waves upfront

### Council review as opt-in initially

`attest plan review` defaults to structural-only (current behavior). `attest plan review --council` adds multi-model review. This lets us iterate on council personas and prompts without breaking the existing workflow. Once stabilized, council becomes default (like tech-spec review).

### MVP review mode: 1 round, skip persona approval

Following user preference: MVP mode with a single review round and auto-approved personas. Deeper reviews available via `--mode standard --round 2` or `--mode production --round 3`.

## 6. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Single-requirement tasks produce too many tasks | Task queue is large, overhead per task | Task count increases but each task is faster to complete. Net time is similar or better due to parallelism. |
| Go AST exploration doesn't work for non-Go projects | Exploration produces no symbols for Python/JS/etc. | Exploration is optional — falls back to keyword-inferred paths. `--deep-explore` flag can dispatch LLM for other languages. |
| Wave computation is NP-hard in worst case | Conflict resolution takes too long | Practical task graphs are sparse DAGs. Greedy conflict resolution (move later task to next wave) is O(n²) worst case, fast in practice. |
| Council review adds latency to planning | Slower plan cycle | MVP mode (1 round) takes ~3-5 minutes. Structural review alone takes <1 second. User chooses when to invest in council review. |
| False dependency removal breaks execution ordering | Tasks execute in wrong order | Only remove deps where no file overlap exists. Conservative: if uncertain, keep the dependency. |
| Boundaries enforcement is too rigid | Blocks legitimate out-of-scope work | `Never` boundaries produce verification warnings, not hard failures. `AskFirst` can be overridden with `--force`. |

## 7. Verification

1. `go build ./...` — compiles
2. `go test -race ./internal/compiler/... ./internal/engine/... ./cmd/attest/...`
3. `golangci-lint run`
4. Manual: `attest plan draft <run-id>` with a spec containing 10+ requirements → verify single-requirement tasks
5. Manual: verify wave groupings in execution plan markdown
6. Manual: verify file-conflict matrix in review output
7. Manual: `attest plan review --council <run-id>` → verify council findings
8. Manual: verify exploration results populate `ImplementationDetail` on slices
9. Manual: verify `ValidationChecks` render in ticket body
10. Manual: compile with exploration → verify `## Validation` section in tickets

## 8. Relationship to Memory System

This plan complements the agent memory system (docs/plans/agent-memory-system.md):

- **Phase 2 extraction:** Council review of execution plans will produce findings that feed into the learning store (same extraction logic as tech-spec council review)
- **Phase 1 enrichment:** Tasks compiled from the improved plan will be enriched with learnings from the store (existing behavior, unaffected)
- **Prevention checks:** Mature learnings compiled into prevention checks (Phase 3) will be loaded as context during council review of execution plans
- **Boundaries:** `Always` constraints from boundaries can reference learnings (e.g., "Use withLock for all state mutations — see lrn-abc123")

The planning improvements and memory system form a feedback loop: better plans → fewer implementation issues → fewer findings → better learnings → better future plans.
