# Rename `councilflow` to `specreview`

**Date:** 2026-03-19
**Status:** Draft
**Branch:** TBD (off `feat/track-work` or `main`)

## Motivation

The `councilflow` package name describes the mechanism (a council of reviewers) rather than its purpose (reviewing specs to harden them before task compilation). The actual pipeline is:

```
brainstorm → functional spec → technical spec → spec review → ingest requirements → compile task graph
```

"Spec review" names the *what*. The council is just *how* it happens. Renaming aligns the code with the mental model.

## Acceptance criteria

- Every in-scope `councilflow` reference is renamed to `specreview`.
- Repo-wide search for `councilflow` matches only the exemptions listed below.
- `just pre-commit` passes (build, test, vet, lint, fmt).

## Scope boundary

**In scope:** The `internal/councilflow` package, its public API, and all references to it across the codebase.

**Out of scope — do NOT rename:**
- `state.CouncilResult` (task-level Opus synthesis output, not spec review)
- `state.RunDir.CouncilResultPath()` (returns path for task-level `council-result.json`)
- `state.RunStatus.LastSuccessfulCouncilTime` (tracks task-level review timing)
- Comments in `state/types.go` that say "council-style" (describing the review pattern, not the package)
- Historical documentation in `docs/reviews/`, `docs/prompts/`, `docs/specs/`, `docs/plans/`
- Conceptual uses of "council" in `README.md` (e.g., "independent council", "council participants")

These describe a different concept (task-level implementation review) or are historical artifacts.

---

## Step 1: Rename directory and files

```bash
git mv internal/councilflow internal/specreview
git mv internal/specreview/council.go internal/specreview/specreview.go
git mv internal/specreview/councilflow_test.go internal/specreview/specreview_test.go
```

Files that keep their names (11 files, no rename needed):
- `types.go`, `runner.go`, `judge.go`, `annotation.go`, `approval.go`
- `personas.go`, `dynpersona.go`, `reviewmode.go`, `prompt.go`, `errors.go`
- `integration_test.go`

## Step 2: Update package declaration in all files

Every `.go` file under `internal/specreview/` (13 files):

```go
// Before
package councilflow

// After
package specreview
```

Files:
1. `specreview.go` (was `council.go`)
2. `types.go`
3. `runner.go`
4. `judge.go`
5. `annotation.go`
6. `approval.go`
7. `personas.go`
8. `dynpersona.go`
9. `reviewmode.go`
10. `prompt.go`
11. `errors.go`
12. `specreview_test.go` (was `councilflow_test.go`)
13. `integration_test.go`

## Step 3: Rename exported types and functions within the package

Now that the package is `specreview`, drop the redundant "Council" prefix. Callers will write `specreview.Config` instead of `councilflow.CouncilConfig`.

| Before (`councilflow.X`) | After (`specreview.X`) | File |
|---|---|---|
| `type CouncilConfig struct` | `type Config struct` | `specreview.go` (line 16) |
| `type CouncilResult struct` | `type Result struct` | `specreview.go` (line 44) |
| `func DefaultConfig() CouncilConfig` | `func DefaultConfig() Config` | `specreview.go` (line 30) |
| `func RunCouncil(ctx, spec, outputBaseDir, cfg CouncilConfig) (*CouncilResult, error)` | `func Run(ctx, spec, outputBaseDir, cfg Config) (*Result, error)` | `specreview.go` (line 55) |

All internal references within the package files must also be updated (e.g., `CouncilConfig` in function signatures, `CouncilResult` in return types, `RunCouncil` in calls).

Specific internal references to update:

**`specreview.go` (was `council.go`):**
- Line 1: Package doc `"Package councilflow owns the council review pipeline for technical specs."` → `"Package specreview owns the spec review pipeline for technical specs."`
- Line 43: Type doc `"CouncilResult holds the full pipeline output..."` → `"Result holds the full pipeline output..."`
- Line 54: Function doc `"RunCouncil executes the full council review pipeline..."` → `"Run executes the spec review pipeline..."`
- Line 66: Output string `"=== Council Review Round %d/%d ==="` → `"=== Spec Review Round %d/%d ==="`
- Line 222: Markdown header `"# Council Review Changelog"` → `"# Spec Review Changelog"`
- All internal uses of `CouncilConfig`, `CouncilResult`, `RunCouncil` throughout the function bodies
- Helper functions: `writeChangelog`, `printFinalSummary`, `maybeApprovePersonas`, `buildPersonaSet` — update parameter types and any `CouncilConfig`/`CouncilResult` references

**`types.go`:**
- Line 17: `"Verdict represents the council review outcome."` → `"Verdict represents the spec review outcome."`
- Line 20: `"Verdict constants aligned with agentops council schema."` → `"Verdict constants aligned with agentops spec review schema."`
- Line 34: `"Persona defines a review perspective for the council pipeline."` → `"Persona defines a review perspective for the spec review pipeline."`

**`judge.go`:**
- Line 302: Prompt string `"You are the **Judge/Editor** — the most critical role in the council review pipeline."` → `"You are the **Judge/Editor** — the most critical role in the spec review pipeline."`

**`errors.go`:**
- Line 5: `"Sentinel errors for council pipeline error classification."` → `"Sentinel errors for spec review pipeline error classification."`

**`runner.go`, `dynpersona.go`, `approval.go`, `prompt.go`, `reviewmode.go`, `annotation.go`, `personas.go`:**
- Update any references to `CouncilConfig` in function signatures/parameters → `Config`
- Update any comments mentioning "council" where it refers to the pipeline

## Step 4: Update imports (2 files)

**`cmd/attest/main.go` line 13:**
```go
// Before
"github.com/runger/attest/internal/councilflow"

// After
"github.com/runger/attest/internal/specreview"
```

**`internal/engine/techspec.go` line 11:**
```go
// Before
"github.com/runger/attest/internal/councilflow"

// After
"github.com/runger/attest/internal/specreview"
```

## Step 5: Update `internal/engine/techspec.go` call sites

| Line | Before | After |
|---|---|---|
| 169 | `// CouncilReviewTechnicalSpec runs the multi-persona council review pipeline.` | `// ReviewTechnicalSpec runs the multi-persona spec review pipeline.` |
| 171 | `func (e *Engine) CouncilReviewTechnicalSpec(ctx context.Context, cfg councilflow.CouncilConfig) (*councilflow.CouncilResult, error)` | `func (e *Engine) ReviewTechnicalSpec(ctx context.Context, cfg specreview.Config) (*specreview.Result, error)` |
| 182 | `councilDir := filepath.Join(e.RunDir.Root, "council")` | `reviewDir := filepath.Join(e.RunDir.Root, "spec-review")` |
| 183 | `result, err := councilflow.RunCouncil(ctx, string(data), councilDir, cfg)` | `result, err := specreview.Run(ctx, string(data), reviewDir, cfg)` |
| 185 | `return nil, fmt.Errorf("council review: %w", err)` | `return nil, fmt.Errorf("spec review: %w", err)` |
| 188 | comment: `"council-improved version"` | `"review-improved version"` |
| 195 | `return nil, fmt.Errorf("refresh structural review after council: %w", err)` | `return nil, fmt.Errorf("refresh structural review after spec review: %w", err)` |
| 201 | `Type: "technical_spec_council_reviewed"` | `Type: "technical_spec_reviewed"` |
| 231 | `councilflow.ParseAnnotations(string(data))` | `specreview.ParseAnnotations(string(data))` |
| 233 | `councilflow.FormatAnnotationsAsFindings(annotations)` | `specreview.FormatAnnotationsAsFindings(annotations)` |

Note: the variable `councilDir` also becomes `reviewDir` (line 182 and anywhere else it's referenced in that function).

## Step 6: Update `internal/engine/engine.go`

**Line 28:**
```go
// Before
// Serial execution, foreground, no council, no detached mode.

// After
// Serial execution, foreground, no spec review, no detached mode.
```

## Step 7: Update `cmd/attest/main.go` call sites

| Line | Before | After |
|---|---|---|
| 85 | `"review --from <path>  (one-step council review)"` | `"review --from <path>  (one-step spec review)"` |
| 190 | usage string mentions `--council` | Remove `--council` from usage string |
| 362 | `mode := councilflow.ReviewStandard` | `mode := specreview.ReviewStandard` |
| 367-368 | `case "--council":` block | **Remove entirely** (deprecated backward-compat flag) |
| 382 | `m := councilflow.ReviewMode(flags[i+1])` | `m := specreview.ReviewMode(flags[i+1])` |
| 383 | `councilflow.ValidReviewModes[m]` | `specreview.ValidReviewModes[m]` |
| 406 | `"structural review failed — fix before running council"` | `"structural review failed — fix before running spec review"` |
| 410 | `cfg := councilflow.DefaultConfig()` | `cfg := specreview.DefaultConfig()` |
| 416 | `result, err := eng.CouncilReviewTechnicalSpec(ctx, cfg)` | `result, err := eng.ReviewTechnicalSpec(ctx, cfg)` |
| 421 | `fmt.Printf("\nCouncil verdict: %s\n", result.OverallVerdict)` | `fmt.Printf("\nSpec review verdict: %s\n", result.OverallVerdict)` |

## Step 8: Update `internal/compiler/compiler.go`

| Line | Before | After |
|---|---|---|
| 520 | `add("internal/councilflow")` | `add("internal/specreview")` |

**Leave unchanged** (keyword detection for requirement content, not code references):
- Line 325: `containsAny(text, "council", "review", "synthesis", "strateg")`
- Line 519: `containsAny(text, "codex", "gemini", "opus", "council", "reviewer", "review")`
- Line 561: `containsAny(text, "codex", "gemini", "opus", "council", "review", "reviewer", "synthesis")`

These detect what a *requirement is about* based on its text content. Specs may still use the word "council", so these heuristics should stay.

## Step 9: Update test files

**`internal/specreview/specreview_test.go` (was `councilflow_test.go`):**
- All `CouncilConfig` → `Config`
- All `CouncilResult` → `Result`
- All `RunCouncil` → `Run`

**`internal/specreview/integration_test.go`:**
- Line 181: `func TestRunCouncilEmptyReviewAborts` → `func TestRunEmptyReviewAborts`
- Line 190: `cfg := CouncilConfig{...}` → `cfg := Config{...}`
- Line 191: `RunCouncil(...)` → `Run(...)`
- Line 193: Error check string `"RunCouncil should fail"` → `"Run should fail"`
- Line 330: `cfg := CouncilConfig{...}` → `cfg := Config{...}`
- Line 331: `RunCouncil(...)` → `Run(...)`
- Line 408: `cfg := CouncilConfig{...}` → `cfg := Config{...}`
- Line 409: `RunCouncil(...)` → `Run(...)`

**`internal/engine/engine_test.go` lines 390, 439:**
- Leave unchanged: `"## 11. Council checkpoints and verification pipeline"` is inline spec fixture text, not a code reference.

**`internal/state/rundir_test.go` line 64:**
- Leave unchanged: tests `CouncilResultPath` which is out of scope.

## Step 10: Update `CLAUDE.md`

Architecture diagram:
```
// Before
│   ├─ Councilflow (internal/councilflow/) — Multi-model review pipeline

// After
│   ├─ Specreview (internal/specreview/) — Multi-model spec review pipeline
```

Description paragraph:
```
// Before
**Councilflow:** Parallel fan-out to persona reviewers...

// After
**Spec review:** Parallel fan-out to persona reviewers...
```

## Canonical artifacts and exemptions

**In scope:** `internal/councilflow/` directory and package name, all Go source and test files referencing `councilflow`, CLI strings, runtime output path (`council/` → `spec-review/`), compiler scope path (`"internal/councilflow"` literal), `CLAUDE.md`.

**Exemptions (must match only these after rename):**
- `"## 11. Council checkpoints and verification pipeline"` — inline spec fixture text in `engine_test.go`
- `"council"` in `containsAny()` calls at `compiler.go:325,519,561` — domain keyword matchers for requirement classification
- `state.CouncilResult`, `CouncilResultPath()`, `LastSuccessfulCouncilTime` — task-level review, different concept
- `state/types.go` "council-style" comments — describes the review pattern abstractly
- `README.md` "independent council" / "council participants" — conceptual product language
- `docs/reviews/*`, `docs/prompts/*`, `docs/specs/*`, `docs/plans/*` — historical artifacts

## Runtime state considerations

The runtime output directory changes from `council/` to `spec-review/` (created in `techspec.go`). This is the directory where spec review round outputs, changelogs, and reviewer artifacts are written during a run.

- **New runs:** Will use `spec-review/` automatically.
- **Existing runs:** Any prior runs have a `council/` directory under `.attest/runs/<run-id>/`. These are ephemeral working artifacts, not durable state. No migration needed — old runs are not re-executed.

## JSON field impact

No JSON serialization fields change. The `councilflow` package types use field names like `"rounds"`, `"overall_verdict"`, `"timings"` — none contain "council" in their JSON tags. The out-of-scope `state.CouncilResult` keeps its `json` tags unchanged.

## Verification

```bash
just pre-commit                          # build, test, vet, lint, fmt — catches broken imports/refs
rg -n 'councilflow|CouncilConfig|CouncilResult|RunCouncil' --type go  # must match only exemptions
```

Done when both pass. No additional manual verification needed.
