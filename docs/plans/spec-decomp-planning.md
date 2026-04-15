# Plan: Spec Decomposition & Planning Improvements

**Status:** Implemented
**Branch:** feat/spec-decomp
**Date:** 2026-03-21 (updated 2026-04-11)

---

## 1. Problem Statement

fabrikk's planning pipeline (prepare → tech-spec → plan → compile) is structurally complete and gated, but the **quality of decomposition is low**. The compiler groups up to 4 requirements per task based on text proximity, infers file paths from keywords, and produces no symbol-level detail. The result: tasks are structurally correct but informationally thin — agents implementing them must rediscover the codebase from scratch.

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

**Exception:** A task MAY contain more than one requirement only when all grouped requirements reference the same fully-qualified symbol (function, method, or type) as identified during codebase exploration (§2.3). When grouping occurs, the compiler MUST set `GroupingReason: "same_symbol"` and `GroupedRequirementIDs` on the task. Tests MUST assert that any task with more than one requirement carries `GroupingReason` and that all grouped requirement IDs map to the same symbol.

**Impact on existing tests:** Tests that assert specific task counts will change. The compiler produces more, smaller tasks. Every requirement maps to exactly one task unless the same-symbol exception applies (in which case the task carries explicit grouping metadata).

**Why:** An agent working on "Implement AT-FR-001" has one clear requirement, writes tests for that one thing, and gets reviewed on that scope. No ambiguity about what "done" means. Parallel execution improves because more tasks are independent.

### 2.2 Wave Computation with File-Conflict Analysis

**Currently:** No explicit wave computation. Tasks have `DependsOn` but no wave assignment. `GetPendingTasks()` returns all tasks with satisfied dependencies — implicit parallelism at execution time.

**Change:** Add explicit wave computation after task compilation, with file-conflict analysis.

#### Wave assignment algorithm

```go
// ComputeWaves assigns each task to a wave based on its dependency depth.
func ComputeWaves(tasks []state.Task) []Wave {
    // Build lookup map for O(1) access during depth computation
    taskMap := make(map[string]*state.Task, len(tasks))
    for i := range tasks {
        taskMap[tasks[i].TaskID] = &tasks[i]
    }

    depth := make(map[string]int) // task ID → wave number (0-indexed)

    // Compute depth (memoized, must run after cycle validation)
    for _, task := range tasks {
        computeDepth(task.TaskID, taskMap, depth)
    }

    // Group by depth, assigning WaveID as "wave-N" (e.g., "wave-0", "wave-1")
    waves := groupByDepth(tasks, depth)

    // Resolve intra-wave file conflicts (fixed-point: re-check after each move).
    // Each pass resolves at least one conflict, so termination is guaranteed
    // in O(n) passes, O(n²) total comparisons.
    for changed := true; changed; {
        changed = false
        for i := range waves {
            conflicts := detectFileConflicts(waves[i].Tasks)
            if len(conflicts) > 0 {
                waves = resolveConflicts(waves, i, conflicts)
                changed = true
                break // restart from wave 0 — earlier waves may now conflict
            }
        }
    }

    return waves
}

// computeDepth returns the wave depth for a task (0 = no dependencies).
// Memoized via the depth map. Must be called after cycle validation.
func computeDepth(taskID string, taskMap map[string]*state.Task, depth map[string]int) int {
    if d, ok := depth[taskID]; ok {
        return d
    }
    task := taskMap[taskID]
    maxDep := 0
    for _, dep := range task.DependsOn {
        d := computeDepth(dep, taskMap, depth) + 1
        if d > maxDep {
            maxDep = d
        }
    }
    depth[taskID] = maxDep
    return maxDep
}
```

#### New types

```go
// Wave groups tasks that can execute in parallel.
type Wave struct {
    WaveID string   // matches ExecutionSlice.WaveID and Task.WaveID
    Tasks  []string // task IDs
}

// FileConflict records two tasks claiming the same file.
type FileConflict struct {
    FilePath string
    TaskA    string
    TaskB    string
}
```

**Note:** `WaveID string` is a new field added to `ExecutionSlice` in this change. The wave computation populates it after task compilation.

#### Path normalization

`Scope.OwnedPaths` MUST be normalized to file-level paths before wave computation. Directory entries are expanded to the set of files they contain (non-recursive; only direct children). Conflict detection operates exclusively on normalized file paths — two tasks conflict when they claim the same normalized file path.

#### File-conflict matrix

After normalization, build a conflict matrix from `Scope.OwnedPaths`:

| File | Tasks | Conflict? |
|---|---|---|
| `internal/engine/plan.go` | task-1, task-3 | YES → serialize |
| `internal/compiler/compiler.go` | task-2 | no |
| `cmd/fabrikk/main.go` | task-4 | no |

Conflicts are resolved by moving the later task (by Order) to the next wave. Before placing a task into wave W, `resolveConflicts` verifies that no task already in W depends on the moved task (directly or transitively). If a dependent exists, it is cascaded forward to W+1. This preserves the dependency DAG while preventing parallel workers from touching the same files.

**Granularity note:** `OwnedPaths` are directory-level in the current compiler (e.g., `internal/compiler`). For MVP, conflict detection uses `OwnedPaths` as-is — this over-serializes tasks within the same directory but is safe. When exploration results are available (§2.3), `FilesLikelyTouched` provides file-level granularity and should be preferred for conflict detection:
```go
func conflictPaths(task state.Task) []string {
    if len(task.FilesLikelyTouched) > 0 {
        return task.FilesLikelyTouched
    }
    return task.Scope.OwnedPaths
}
```

#### Cross-wave shared file registry

After wave assignment, build a registry of files that appear in multiple waves:

| File | Wave 1 Tasks | Wave 2+ Tasks | Mitigation |
|---|---|---|---|
| `internal/state/types.go` | task-1 | task-5 | Wave 2 worktree must branch from post-Wave-1 SHA |

This registry is displayed during `fabrikk plan review` and stored in the execution plan for the orchestrator to enforce base-SHA refresh between waves.

#### False dependency removal

For each `DependsOn` entry, verify the dependency is real:

1. Does the blocked task modify files that the blocker also modifies? → **Keep**
2. Does the blocked task read output produced by the blocker? → **Keep** (checked via `RequirementIDs` overlap)
3. Does the blocked task's `FilesLikelyTouched` or `ReadOnlyPaths` overlap with the blocker's `OwnedPaths`? → **Keep** (read-after-write dependency)
4. Is the dependency only lane-based ordering (same requirement family)? → **Remove** if no overlap from rules 1-3

This increases parallelism by removing artificial serialization from the current lane-based dependency scheme.

#### Integration

- `DraftExecutionPlan` calls `removeFalseDependencies` then `ComputeWaves` after building slices from `compiler.Compile`. This ordering is required: waves must reflect pruned dependencies
- Wave assignments stored on a new `ExecutionSlice.WaveID` field (type `string`, added in this change)
- `renderExecutionPlanMarkdown` shows wave groupings
- `ReviewExecutionPlan` validates wave assignments (no intra-wave file conflicts, no intra-wave dependency edges)

### 2.3 Codebase Exploration Before Planning

**Currently:** `DraftExecutionPlan` calls `compiler.Compile(artifact)` which works from raw requirement text. No codebase exploration.

**Change:** Add a two-phase exploration step before plan drafting: deterministic structural analysis first, then LLM interpretation.

#### Hybrid approach: deterministic analysis + LLM interpretation

**Phase 1 — Deterministic structural analysis** (fast, cached, no LLM tokens):

Shell out to `code_intel.py` from the codereview skill. The script is discovered at runtime via `resolveCodeIntel()` which checks:
1. `FABRIKK_CODE_INTEL` environment variable (explicit path override)
2. `code_intel.py` on PATH
3. Common install locations: `~/.claude/skills/codereview/scripts/code_intel.py`, `~/workspaces/skill-codereview/scripts/code_intel.py`

**Security note:** The resolved script runs with the user's permissions and inherits the user's trust boundary (same model as `~/.bashrc`). In shared or CI environments, set `FABRIKK_CODE_INTEL` to an explicit, trusted path rather than relying on the search order.

The script provides:
- **Tree-sitter AST extraction** (8 languages, regex fallback) — function signatures, imports, exports with line ranges and visibility
- **Semantic similarity** — all-MiniLM-L6-v2 ONNX embeddings find related functions across files
- **Dependency graph** — import edges + cross-file call edges + semantic similarity scores
- **Complexity hotspots** — gocyclo/radon with tree-sitter fallback

All output as JSON. Tree-sitter is optional (graceful regex fallback). Results are cached by file mtime — subsequent runs only re-analyze changed files.

When `code_intel.py` is not available (no Python, script not found), fall back to pure LLM exploration (Phase 2 only).

**Phase 2 — LLM interpretation** (uses structural data as context):

Dispatch a CLI agent (via `agentcli`) with:
- The normalized requirements from the `RunArtifact`
- The structural analysis JSON from Phase 1 (if available) — injected as a `## Codebase Structure` section in the prompt
- Instructions to map requirements to code locations and return `ExplorationResult`

The LLM doesn't waste tokens rediscovering file structure — it interprets requirements against the structural data:
1. **Map requirements to symbols** — which existing functions/types each requirement touches
2. **Identify reuse points** — semantic similarity suggests related code the plan should build on
3. **Flag gaps** — requirements that don't map to any existing symbol need new files
4. **Suggest task grouping** — dependency graph edges inform wave computation

When Phase 1 is unavailable, `buildExplorationPrompt` produces a qualitatively different prompt:

- **With structural data** (`sa != nil`): A reasoning prompt that provides the structural JSON inline and asks the agent to map requirements to symbols. No tool use needed — pure interpretation.
- **Without structural data** (`sa == nil`): A tool-exploration prompt that instructs the agent to use Grep/Glob/Read with an explicit discovery strategy (directory listing → grep for requirement keywords → read key files). Includes tool-use instructions and a longer timeout. Both templates end with the same output schema instruction (`ExplorationResult` JSON).

#### Why hybrid, not pure LLM or pure AST

- Pure LLM: wastes tokens on Grep/Read to rediscover what AST parsing finds instantly
- Pure AST: can't match requirements to code by intent, only by name
- Hybrid: deterministic analysis provides the facts, LLM provides the judgment

#### Types

```go
// StructuralAnalysis holds the output of code_intel.py graph analysis.
// Populated by Phase 1 (deterministic). Nil when code_intel.py is unavailable.
type StructuralAnalysis struct {
    Nodes []StructuralNode `json:"nodes"` // functions, types, methods
    Edges []StructuralEdge `json:"edges"` // imports, calls, semantic similarity
}

type StructuralNode struct {
    ID       string `json:"id"`       // "path/to/file.go::FunctionName"
    Kind     string `json:"kind"`     // "function", "class", "struct", "method"
    File     string `json:"file"`
    Line     int    `json:"line"`
    Exported bool   `json:"exported"`
}

type StructuralEdge struct {
    From  string  `json:"from"`
    To    string  `json:"to"`
    Type  string  `json:"type"`  // "imports", "calls", "semantic_similarity"
    Score float64 `json:"score"` // 0.0-1.0, only for semantic_similarity edges
}

// ExplorationResult holds the combined output of Phases 1+2.
// Populated by the LLM agent using structural data as context.
type ExplorationResult struct {
    FileInventory []FileInfo     `json:"file_inventory"`
    Symbols       []SymbolInfo   `json:"symbols"`
    TestFiles     []TestFileInfo `json:"test_files"`
    ReusePoints   []ReusePoint   `json:"reuse_points"`
}

type FileInfo struct {
    Path      string `json:"path"`
    Exists    bool   `json:"exists"`
    IsNew     bool   `json:"is_new"`
    LineCount int    `json:"line_count"`
    Language  string `json:"language"` // "go", "python", "typescript", etc.
}

type SymbolInfo struct {
    FilePath  string `json:"file_path"`
    Line      int    `json:"line"`
    Kind      string `json:"kind"`      // "func", "type", "interface", "method", "class", "trait"
    Signature string `json:"signature"` // e.g., "func (s *Store) Add(l *Learning) error"
}

type TestFileInfo struct {
    Path      string   `json:"path"`       // e.g., "internal/learning/store_test.go"
    TestNames []string `json:"test_names"` // e.g., ["TestStoreAdd", "TestStoreQuery"]
    Covers    string   `json:"covers"`     // the source file this test covers
}

type ReusePoint struct {
    FilePath  string `json:"file_path"`
    Line      int    `json:"line"`
    Symbol    string `json:"symbol"`
    Relevance string `json:"relevance"` // why this is relevant to the plan
}
```

#### Data flow

```
DraftExecutionPlan
  │
  ├─ Phase 1: runStructuralAnalysis(ctx)
  │    └─ Shell out to code_intel.py graph --root <workdir> --json
  │    └─ Returns *StructuralAnalysis (nil if unavailable)
  │
  ├─ Phase 2: exploreForPlan(ctx, artifact, structuralAnalysis)
  │    ├─ Build prompt: requirements + structural JSON (if available)
  │    ├─ Dispatch via agentcli.InvokeFunc (or daemon QueryFunc)
  │    ├─ Parse JSON response into *ExplorationResult
  │    └─ On error: log warning to stderr, set result = nil, continue
  │
  ├─ Validate: validateExplorationResult(explorationResult)
  │    ├─ os.Stat every FileInfo.Path: flip Exists/IsNew if filesystem disagrees
  │    ├─ os.Stat every SymbolInfo.FilePath: drop entries with nonexistent files
  │    └─ Skipped when explorationResult is nil
  │
  ├─ Compile: compiler.Compile(artifact)
  │    └─ Produces compiled tasks from requirements
  │
  ├─ Enrich + prune: enrich compiled tasks with exploration results, then remove false dependencies
  │    └─ Populates FilesLikelyTouched / ImplementationDetail before wave assignment
  │
  ├─ Wave assignment: compiler.ComputeWaves(compiled.Tasks)
  │    └─ Materializes execution slices with WaveID from the compiled task graph
  │
  └─ Persist: write execution plan and exploration result to the run directory
```

**Prompt size management:** The structural analysis JSON for a typical project (50 files, 200 functions) is ~15KB — well within context limits. For larger projects (500+ files), the prompt builder truncates to the top 100 functions by: (1) keyword overlap between function name/signature and requirement text, (2) structural centrality (in-degree in the call graph — most-called functions are most likely relevant). Full analysis is written to `<run-dir>/structural-analysis.json` with a note in the prompt that the agent can Read this file for complete data.

#### Implementation

```go
// runStructuralAnalysis shells out to code_intel.py for deterministic codebase analysis.
// Returns nil (not error) when the tool is unavailable — caller proceeds with LLM-only exploration.
// structuralAnalysisTimeout bounds the code_intel.py subprocess.
// Distinct from the LLM exploration timeout (180s) — Phase 1 is deterministic and should be faster.
const structuralAnalysisTimeout = 60 * time.Second

func (e *Engine) runStructuralAnalysis(ctx context.Context) *StructuralAnalysis {
    codeIntelPath := resolveCodeIntel()
    if codeIntelPath == "" {
        return nil
    }

    ctx, cancel := context.WithTimeout(ctx, structuralAnalysisTimeout)
    defer cancel()

    cmd := exec.CommandContext(ctx, "python3", codeIntelPath, "graph",
        "--root", e.WorkDir, "--json")
    // Cap subprocess output at 10MB to prevent OOM from pathological inputs.
    // Typical structural analysis JSON is ~15KB; 10MB is generous.
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        fmt.Fprintf(os.Stderr, "  code_intel: %v (falling back to LLM-only exploration)\n", err)
        return nil
    }
    if err := cmd.Start(); err != nil {
        fmt.Fprintf(os.Stderr, "  code_intel: %v (falling back to LLM-only exploration)\n", err)
        return nil
    }
    const maxOutputBytes = 10 << 20 // 10MB
    out, err := io.ReadAll(io.LimitReader(stdout, maxOutputBytes))
    if err != nil {
        fmt.Fprintf(os.Stderr, "  code_intel: read error: %v (falling back to LLM-only exploration)\n", err)
        return nil
    }
    if err := cmd.Wait(); err != nil {
        fmt.Fprintf(os.Stderr, "  code_intel: %v (falling back to LLM-only exploration)\n", err)
        return nil
    }

    var analysis StructuralAnalysis
    if err := json.Unmarshal(out, &analysis); err != nil {
        fmt.Fprintf(os.Stderr, "  code_intel: parse error: %v\n", err)
        return nil
    }
    return &analysis
}

// exploreForPlan dispatches an LLM agent to map requirements to code using structural context.
func (e *Engine) exploreForPlan(ctx context.Context, artifact *state.RunArtifact, sa *StructuralAnalysis) (*ExplorationResult, error) {
    prompt := buildExplorationPrompt(artifact, sa) // includes structural JSON when sa != nil
    backend := agentcli.BackendFor(agentcli.BackendClaude, "")

    timeout := 180
    if sa == nil {
        timeout = 360 // fallback LLM exploration needs more time for tool-based discovery
    }

    invoke := agentcli.InvokeFunc
    if e.InvokeFn != nil {
        invoke = e.InvokeFn
    }
    raw, err := invoke(ctx, &backend, prompt, timeout)
    if err != nil {
        fmt.Fprintf(os.Stderr, "  explore: %v (proceeding without exploration)\n", err)
        return nil, nil // advisory — compiler proceeds without exploration
    }

    // Extract JSON using the councilflow pattern (runner.go:280-301):
    // (1) try ExtractFromCodeFence + json.Valid, (2) find first '{',
    // (3) call ExtractJSONBlock with that offset.
    jsonStr := extractExplorationJSON(raw)
    var result ExplorationResult
    if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
        fmt.Fprintf(os.Stderr, "  explore: could not parse LLM response: %v (proceeding without exploration)\n", err)
        return nil, nil // advisory — compiler proceeds without exploration
    }
    return &result, nil
}

// validateExplorationResult checks LLM-provided paths against the filesystem.
// Flips Exists/IsNew flags where the filesystem disagrees with the LLM,
// and drops SymbolInfo entries pointing to nonexistent files.
func (e *Engine) validateExplorationResult(result *ExplorationResult) {
    if result == nil {
        return
    }

    for i := range result.FileInventory {
        fi := &result.FileInventory[i]
        _, err := os.Stat(filepath.Join(e.WorkDir, fi.Path))
        exists := err == nil
        fi.Exists = exists
        fi.IsNew = !exists
    }

    valid := result.Symbols[:0]
    for _, sym := range result.Symbols {
        if _, err := os.Stat(filepath.Join(e.WorkDir, sym.FilePath)); err == nil {
            valid = append(valid, sym)
        }
    }
    result.Symbols = valid
}
```

#### Integration

- `DraftExecutionPlan` calls `runStructuralAnalysis` then `exploreForPlan` before `compiler.Compile`. Exploration errors are logged and continued (result set to nil) — the compiler produces valid tasks without exploration data
- `exploreForPlan` calls `validateExplorationResult` before returning: for each `FileInfo` where `Exists=true`, `os.Stat` the file and set `Exists=false` if missing; for each `SymbolInfo` and `ReusePoint`, verify the file exists and drop entries with nonexistent paths; log warnings for corrected entries
- After `compiler.Compile` produces initial slices, `DraftExecutionPlan` enriches them with exploration data:
  - Populates `ExecutionSlice.FilesLikelyTouched` with verified paths
  - Populates `ExecutionSlice.ImplementationDetail` (§2.7) with symbol-level specs
- The compiler remains decoupled from exploration types — enrichment is a separate pass
- The exploration result is persisted to `<run-dir>/exploration.json` for council review

#### Testing

- `exploreForPlan` and `CouncilReviewExecutionPlan` (§2.5) MUST accept an injected backend interface (the existing `agentcli.InvokeFunc` / `QueryFunc` pattern) so callers can substitute stub implementations
- Unit and integration tests MUST use fixture-backed stub responses loaded from checked-in JSON files in `testdata/`
- Live model calls are allowed only in manual system tests and are not required for CI pass/fail

#### Future: native Go integration

The codereview skill is being migrated from Python to Go. Once complete:
- `code_intel.py` subprocess call becomes a native Go library import
- No Python dependency needed
- Tighter integration with the compiler — structural analysis feeds directly into task compilation

Additional optional accelerators:
- **Roam** (`roam-code`) — if `roam` is in PATH, use `roam search` / `roam context` / `roam impact` for pre-indexed structural analysis with 27-language support
- When neither `code_intel.py` nor Roam is available, pure LLM exploration remains the fallback

### 2.4 Conformance Checks Per Task

**Currently:** Tasks get `RequiredEvidence` (coarse: `test_pass`, `quality_gate_pass`, `file_exists`). No per-task `content_check` or `files_exist` validation blocks.

**Change:** Add a `ValidationChecks` field to `ExecutionSlice` and `Task` with typed assertions.

#### Types

```go
// ValidationCheck defines a mechanically verifiable assertion for a task.
// Only built-in allowlisted tools may be emitted by the compiler or verifier.
// Requirement text, persona output, and exploration output must never be interpolated
// into command arguments without validation against the allowlist.
type ValidationCheck struct {
    Type    string   `json:"type"`              // "files_exist", "content_check", "tests", "command", "lint"
    // Type-specific fields:
    Paths   []string `json:"paths,omitempty"`   // files_exist: paths that must exist
    File    string   `json:"file,omitempty"`    // content_check: file to search
    Pattern string   `json:"pattern,omitempty"` // content_check: regex pattern
    Tool    string   `json:"tool,omitempty"`    // tests/command/lint: executable name (must be in allowlist)
    Args    []string `json:"args,omitempty"`    // tests/command/lint: argument array (no shell interpretation)
}
```

Examples:

```go
// files_exist: verify file was created
ValidationCheck{Type: "files_exist", Paths: []string{"internal/learning/index.go"}}

// content_check: verify function exists
ValidationCheck{Type: "content_check", File: "internal/learning/store.go", Pattern: "func.*AssembleContext"}

// tests: run specific test
ValidationCheck{Type: "tests", Tool: "go", Args: []string{"test", "-run", "TestAssembleContext", "./internal/learning/..."}}
```

#### Derivation

The compiler derives validation checks deterministically from exploration results:

1. **New files:** If `FileInfo.IsNew == true`, emit `files_exist` check with the file path
2. **New symbols:** If `ImplementationDetail.SymbolsToAdd` is non-empty, emit `content_check` for each symbol's signature regex against its target file
3. **New tests:** If `ImplementationDetail.TestsToAdd` is non-empty, emit `tests` check with `Tool: "go", Args: ["test", "-run", <TestName>, "./<package>/..."]`
4. **Quality gate:** Always emit `command` check with the project's configured quality gate command

Rules are evaluated in order; all matching rules emit checks (they are additive, not exclusive). When no exploration result is available, only rule 4 applies.

#### Integration

- `ValidationChecks` field on `ExecutionSlice` and `Task`
- `ReviewExecutionPlan` validates that every slice has at least one check (warning if missing)
- The verifier can run these checks in addition to its existing pipeline. Checks are executed via `exec.Command(tool, args...)` with no shell interpretation; `Tool` must appear in a hardcoded allowlist (e.g., `go`, `python3`, `npm`, `make`)
- `MarshalTicket` renders validation checks in a `## Validation` section

### 2.5 Council Review of Execution Plans

**Currently:** `ReviewExecutionPlan` runs deterministic structural checks only. No multi-model council review.

**Change:** Wire council review for execution plans, similar to `CouncilReviewTechnicalSpec`.

#### Review perspectives

| Persona | Focus | Backend |
|---|---|---|
| Scope Reviewer | Are slices well-scoped? File conflicts? Scope gaps? | claude (or codex if available) |
| Dependency Reviewer | Are dependencies correct? False deps? Missing deps? | claude (or codex if available) |
| Completeness Reviewer | Does every requirement have a covering slice? Acceptance criteria sufficient? | claude |
| Feasibility Reviewer | Are slices implementable? Too large? Missing context? | claude |

**Note:** Persona backends fall back to Claude via `BackendFor` when a backend is disabled in `fabrikk.yaml`. This is transparent — the review proceeds with the same personas regardless of backend availability.

#### Integration

- New engine method: `CouncilReviewExecutionPlan(ctx context.Context, cfg councilflow.CouncilConfig) (*councilflow.CouncilResult, error)`
- Called from `fabrikk plan review --council` (opt-in initially, default later)
- The council receives: execution plan markdown + exploration results + tech spec
- Council findings become `ReviewFinding` entries on the `ExecutionPlanReview`
- Blocking council findings prevent plan approval
- Custom personas can be injected via `--persona <path>` (same as tech-spec review)

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
- `AskFirst` items cause `DraftExecutionPlan` to return a `*HumanInputRequiredError` (defined in `internal/engine/`) with the pending items. The CLI inspects the error via `errors.As` and prints them, exiting with the following format:

  ```
  Plan draft requires human input for the following items:
    - <item1>
    - <item2>
  Add answers to the ## Boundaries section of your tech spec and re-run:
    fabrikk plan draft <run-id>
  ```

  No interactive blocking inside the engine.

  ```go
  // HumanInputRequiredError signals that DraftExecutionPlan cannot proceed
  // without human answers to AskFirst boundary items.
  type HumanInputRequiredError struct {
      Items []string // the AskFirst items needing answers
  }

  func (e *HumanInputRequiredError) Error() string {
      return fmt.Sprintf("human input required for %d items", len(e.Items))
  }
  ```
- `Never` items are validated during verification — if a worker touches out-of-scope paths, verification produces a warning finding. MVP: warning finding only (advisory, not blocking). Post-MVP: the compiler SHOULD check Never paths against task `Scope.OwnedPaths` at compilation time and flag conflicts, preventing assignment of tasks that would violate boundaries.
- Boundaries can be set via `fabrikk boundaries set <run-id> --always "..." --never "..."` or parsed from a `## Boundaries` section in the tech spec with `--from-tech-spec`

### 2.7 Implementation Detail on Execution Slices

**Currently:** Slices have `Title`, `Goal`, `Notes` (prose) but no structured implementation guidance.

**Change:** Add `ImplementationDetail` to `ExecutionSlice` and `Task`. `CompileExecutionPlan` propagates the field from slice to task during compilation.

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
- `CompileExecutionPlan` copies `ImplementationDetail` from `ExecutionSlice` to `Task` during task compilation
- `MarshalTicket` renders `ImplementationDetail` from `Task` in ticket body for worker agents
- Council reviewers see this detail and can validate feasibility

## 3. Files to Modify

| File | Change | Est. lines |
|------|--------|-----------|
| `internal/compiler/compiler.go` | Change `maxGroupSize` to 1, add `ComputeWaves`, `detectFileConflicts`, `resolveConflicts`, false dep removal, propagate `ValidationChecks` and `ImplementationDetail` from slice to task in `CompileExecutionPlan` | +130 |
| `internal/compiler/compiler_test.go` | Tests for wave computation, conflict detection, single-requirement tasks | +150 |
| `internal/engine/explore.go` | Hybrid codebase exploration: `runStructuralAnalysis` (code_intel.py dispatch), `exploreForPlan` (LLM agent with structural context), `extractExplorationJSON` (councilflow JSON extraction pattern), `validateExplorationResult` (filesystem validation), `resolveCodeIntel`, `buildExplorationPrompt` (two templates: structural-interpretation and tool-exploration) | ~240 |
| `internal/engine/explore_test.go` | Exploration tests (stubbed agent responses, structural analysis parsing) | ~120 |
| `internal/state/types.go` | Add `StructuralAnalysis`, `StructuralNode`, `StructuralEdge`, `ExplorationResult`, `FileInfo`, `SymbolInfo`, `TestFileInfo`, `ReusePoint`, `FileConflict`, `ValidationCheck`, `Boundaries`, `ImplementationDetail`, `FileChange` types. Add `ValidationChecks` and `ImplementationDetail` to `Task`. Add `WaveID string` to `ExecutionSlice`. Add `Boundaries` to `RunArtifact`. | +90 |
| `internal/engine/plan.go` | Update `DraftExecutionPlan` to call exploration + wave computation. Add `CouncilReviewExecutionPlan`. Update `ReviewExecutionPlan` to validate waves and run optional council. | +150 |
| `internal/engine/engine_test.go` | Tests for exploration integration, boundary enforcement, plan enrichment, and wave validation | +180 |
| `internal/engine/plan_council_test.go` | Tests for execution-plan council review wiring and verdict handling | +120 |
| `internal/ticket/format.go` | Render `ValidationChecks` and `ImplementationDetail` in ticket body | +30 |
| `cmd/fabrikk/main.go` | Add `--council` flag to `plan review`, add `boundaries` command | +20 |

**Estimated total:** ~970 new/modified lines.

## 4. Implementation Order

### Wave 1: Foundation (no dependencies between these)

1. **Task granularity** — Change `maxGroupSize` to 1. Update tests. This is a one-line change with test adjustments.

2. **Validation checks type** — Add `ValidationCheck` to `state/types.go` and `ExecutionSlice`. Wire through `MarshalTicket`. No behavioral change yet — just the type and rendering.

3. **Boundaries type** — Add `Boundaries` to `RunArtifact` in `state/types.go`. No behavioral enforcement yet — just the type.

### Wave 2: Wave computation (depends on Wave 1)

4. **Wave computation** — Implement `ComputeWaves`, `detectFileConflicts`, `resolveConflicts` in `compiler.go`. Populate `ExecutionSlice.WaveID`. Update `DraftExecutionPlan` to call wave computation. Update `renderExecutionPlanMarkdown` to show wave groupings.

5. **False dependency removal** — Implement dependency necessity validation. Remove lane-based deps where no file overlap exists.

6. **Wave validation in review** — Update `ReviewExecutionPlan` to validate no intra-wave file conflicts. Add cross-wave shared file registry to review output.

### Wave 3: Exploration (depends on Wave 1)

7. **Structural analysis** — Implement `runStructuralAnalysis` and `resolveCodeIntel` in `engine/explore.go`. Shell out to `code_intel.py`, parse `StructuralAnalysis` JSON. Graceful fallback when unavailable.

8. **LLM exploration** — Implement `exploreForPlan` and `buildExplorationPrompt` in `engine/explore.go`. Build prompt with requirements + structural context. Dispatch via `agentcli.InvokeFunc`. Parse `ExplorationResult`.

9. **Implementation detail** — Add `ImplementationDetail` to `ExecutionSlice`. Populate from exploration results. Render in ticket body.

10. **Conformance check derivation** — Derive `ValidationChecks` from exploration results (files_exist for new files, content_check for new functions, tests for test files).

### Wave 4: Council review (depends on Waves 2-3)

11. **Council review wiring** — Implement `CouncilReviewExecutionPlan`. Wire personas (scope, dependency, completeness, feasibility). MVP mode: 1 round, 4 reviewers.

12. **CLI integration** — Add `--council` flag to `fabrikk plan review`. Default to MVP mode (1 round). Display findings.

### Wave 5: Boundaries (depends on Wave 2)

13. **Boundary enforcement** — `Always` constraints injected into task `Constraints`. `Never` items validated during verification. `AskFirst` returns `*HumanInputRequiredError` from `DraftExecutionPlan`.

14. **CLI command** — `fabrikk boundaries set <run-id> --always "..." --never "..."` or parse from tech spec `## Boundaries` section with `--from-tech-spec`.

## 5. Design Decisions

### Hybrid exploration: deterministic analysis + LLM interpretation

We use a two-phase approach for codebase exploration before planning:
1. **Phase 1:** Shell out to `code_intel.py` (tree-sitter AST + semantic embeddings) for deterministic structural analysis — fast, cached, language-agnostic across 8 languages
2. **Phase 2:** Dispatch an LLM agent with the structural data as context — the agent maps requirements to code by intent, not just by name

This is better than either approach alone. The LLM doesn't waste tokens rediscovering file structure. The deterministic analysis doesn't miss intent-level connections.

When `code_intel.py` is unavailable (no Python), Phase 2 runs standalone using tool-based exploration (Grep/Glob/Read). The codereview skill is being migrated to Go — once complete, the subprocess call becomes a native library import.

### Wave computation at planning time, not execution time

Currently, waves are implicit (computed at execution time by `GetPendingTasks`). We move wave computation to planning time so:
- The plan document shows explicit wave groupings for human review
- The council can validate wave assignments
- File conflicts are caught before any work begins
- The orchestrator knows the expected number of waves upfront

### Council review as opt-in initially

`fabrikk plan review` defaults to structural-only (current behavior). `fabrikk plan review --council` adds multi-model review. This lets us iterate on council personas and prompts without breaking the existing workflow. Once stabilized, council becomes default (like tech-spec review).

### MVP review mode: 1 round, skip persona approval

Following user preference: MVP mode with a single review round and auto-approved personas. Deeper reviews available via `--mode standard --round 2` or `--mode production --round 3`.

## 6. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Single-requirement tasks produce too many tasks | Task queue is large, overhead per task | Task count increases but each task is faster to complete. Net time is similar or better due to parallelism. |
| LLM exploration produces inconsistent results | Agent may miss symbols or hallucinate file paths | `validateExplorationResult` runs os.Stat on all claimed paths: flips Exists/IsNew flags and drops SymbolInfo entries with nonexistent files. Exploration is advisory — the compiler still produces valid tasks without it. |
| `code_intel.py` not available on all systems | Phase 1 skipped, LLM explores without structural context | Graceful fallback: Phase 2 runs standalone. Slower but functional. |
| Wave computation is NP-hard in worst case | Conflict resolution takes too long | Practical task graphs are sparse DAGs. Greedy conflict resolution (move later task to next wave) is O(n²) worst case, fast in practice. |
| Council review adds latency to planning | Slower plan cycle | MVP mode (1 round) takes ~3-5 minutes. Structural review alone takes <1 second. User chooses when to invest in council review. |
| False dependency removal breaks execution ordering | Tasks execute in wrong order | Only remove deps where no file overlap exists. Conservative: if uncertain, keep the dependency. |
| Boundaries enforcement is too rigid | Blocks legitimate out-of-scope work | `Never` boundaries produce verification warnings, not hard failures. `AskFirst` returns an error — the user provides input and re-runs. |
| Structural analysis JSON too large for prompt | Context overflow on large projects (500+ files) | Truncate to top 100 functions by keyword overlap with requirement text and call-graph in-degree. Full analysis written to `<run-dir>/structural-analysis.json`; prompt notes the agent can Read this file. |

## 7. Verification

1. `go build ./...` — compiles
2. `go test -race ./internal/compiler/... ./internal/engine/... ./cmd/fabrikk/...`
3. `golangci-lint run`
4. Manual: `fabrikk plan draft <run-id>` with a spec containing 10+ requirements → verify single-requirement tasks
5. Manual: verify wave groupings in execution plan markdown
6. Manual: verify file-conflict matrix in review output
7. Manual: `fabrikk plan review <run-id> --council` → verify council findings
8. Manual: verify exploration results populate `ImplementationDetail` on slices
9. Manual: verify `ValidationChecks` render in ticket body
10. Manual: compile with exploration → verify `## Validation` section in tickets
11. Manual: run with `code_intel.py` unavailable → verify graceful fallback to LLM-only

## 8. Relationship to Memory System

This plan complements the agent memory system (docs/plans/done/agent-memory-system.md):

- **Phase 2 extraction:** Council review of execution plans will produce findings that feed into the learning store (same extraction logic as tech-spec council review)
- **Phase 1 enrichment:** Tasks compiled from the improved plan will be enriched with learnings from the store (existing behavior, unaffected)
- **Prevention checks:** Mature learnings compiled into prevention checks (Phase 3) will be loaded as context during council review of execution plans
- **Boundaries:** `Always` constraints from boundaries can reference learnings (e.g., "Use withLock for all state mutations — see lrn-abc123")

The planning improvements and memory system form a feedback loop: better plans → fewer implementation issues → fewer findings → better learnings → better future plans.
