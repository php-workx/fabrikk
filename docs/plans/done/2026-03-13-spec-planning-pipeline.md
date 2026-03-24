# Spec Planning Pipeline Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add an explicit spec-to-technical-spec-to-execution-plan pipeline so `attest` stops compiling tasks directly from broad prose requirements.

**Architecture:** Keep `run-artifact.json` as the normalized functional contract, add a generated-and-approved technical spec artifact plus a generated-and-approved execution plan artifact, and only compile execution tasks from the approved plan. Use specialized drafting prompts for synthesis and decomposition, then use council-style review as a critique and gating layer rather than the primary author.

**Tech Stack:** Go CLI, JSON artifacts in `.attest/runs/<run-id>/`, Markdown spec inputs in `docs/specs/`, prompt assets in `docs/prompts/`, deterministic compiler + verifier + council review checkpoints.

---

## Context To Preserve

- The current compiler works, but it is still guessing execution slices directly from long-form requirements.
- The repo already has a strong functional spec and a strong technical spec:
  - `docs/specs/attest-functional-spec.md`
  - `docs/specs/attest-technical-spec.md`
- The missing artifact is an execution plan that groups requirements into implementation slices before task compilation.
- The desired automated flow is:
  1. Functional spec is provided or approved.
  2. Agents draft a technical spec or normalize an existing one into a strict template.
  3. Council critiques the technical spec and blocks approval on ambiguity or missing operational detail.
  4. Agents derive an execution plan from the approved technical spec.
  5. Council critiques decomposition quality.
  6. Task compilation consumes the approved execution plan, not raw prose requirements.
- The council should be a critique layer, not the primary author.
- Structured outputs are required for both tech-spec generation and plan generation so the outputs can be linted mechanically.

## Proposed Artifact Model

### Artifact 1: Run Artifact

Purpose: normalized functional contract and requirement inventory.

Primary file:
- Existing: `.attest/runs/<run-id>/run-artifact.json`

Key role:
- Input to technical-spec drafting.
- Source of requirement IDs and approval boundary.

### Artifact 2: Technical Spec Artifact

Purpose: operational and architectural contract derived from the functional spec and repo context.

Primary files:
- Create: `.attest/runs/<run-id>/technical-spec.md`
- Create: `.attest/runs/<run-id>/technical-spec-review.json`
- Create: `.attest/runs/<run-id>/technical-spec-approval.json`

Required sections:
- Technical context
- Architecture
- Canonical artifacts and schemas
- CLI/API surface
- State machines and transition rules
- Verification and quality gates
- Open questions / unresolved risks

Recommended lifecycle:
- `drafted`
- `under_review`
- `approved`
- `rejected`

### Artifact 3: Execution Plan Artifact

Purpose: implementation slices that are small, testable, and dependency-aware.

Primary files:
- Create: `.attest/runs/<run-id>/execution-plan.json`
- Create: `.attest/runs/<run-id>/execution-plan.md`
- Create: `.attest/runs/<run-id>/execution-plan-review.json`
- Create: `.attest/runs/<run-id>/execution-plan-approval.json`

Required slice fields:
- `slice_id`
- `title`
- `goal`
- `requirement_ids`
- `depends_on`
- `files_likely_touched`
- `owned_paths`
- `acceptance_checks`
- `risk`
- `size`
- `notes`

Recommended lifecycle:
- `drafted`
- `under_review`
- `approved`
- `rejected`

## Recommended Approval Flow

### Technical Spec Flow

1. `attest tech-spec draft <run-id>`
2. draft persisted to `technical-spec.md`
3. `attest tech-spec review <run-id>`
4. review persisted to `technical-spec-review.json`
5. if review has blocking findings, state remains `under_review`
6. `attest tech-spec approve <run-id>` only succeeds when blocking findings are resolved or explicitly overridden
7. approval persisted to `technical-spec-approval.json`

### Execution Plan Flow

1. `attest plan draft <run-id>`
2. plan persisted to `execution-plan.json` and `execution-plan.md`
3. `attest plan review <run-id>`
4. review persisted to `execution-plan-review.json`
5. if decomposition findings are blocking, plan stays `under_review`
6. `attest plan approve <run-id>` records approval only after the review gate is green
7. task compilation is blocked until the execution plan is approved

### Approval Policy Defaults

- Human approval is required for both the technical spec and the execution plan.
- Council reviews are advisory for edits, but blocking findings prevent approval by default.
- Any override must be explicit, persisted, and reasoned.
- Approval artifacts must record the artifact hash they approved so later mutation invalidates approval.

## JSON Schema Drafts

### `technical-spec-review.json`

```json
{
  "schema_version": "0.1",
  "run_id": "run-123",
  "artifact_type": "technical_spec_review",
  "technical_spec_hash": "sha256:...",
  "status": "pass",
  "summary": "The draft is internally consistent.",
  "blocking_findings": [
    {
      "finding_id": "tsr-001",
      "severity": "high",
      "category": "missing_transition",
      "summary": "Run state transition from blocked to running is not fully defined.",
      "requirement_ids": [
        "AT-TS-014"
      ],
      "suggested_repair": "Define the transition trigger and guard conditions."
    }
  ],
  "warnings": [
    {
      "warning_id": "tsr-w-001",
      "summary": "Timeout policy is underspecified."
    }
  ],
  "reviewers": [
    {
      "reviewer_id": "codex",
      "pass": false,
      "summary": "State transitions are incomplete."
    }
  ],
  "reviewed_at": "2026-03-13T12:00:00Z"
}
```

### `execution-plan.json`

```json
{
  "schema_version": "0.1",
  "run_id": "run-123",
  "artifact_type": "execution_plan",
  "source_technical_spec_hash": "sha256:...",
  "status": "drafted",
  "slices": [
    {
      "slice_id": "slice-001",
      "title": "Add technical spec artifact paths",
      "goal": "Persist run-scoped technical spec artifacts.",
      "requirement_ids": [
        "AT-FR-001",
        "AT-FR-003"
      ],
      "depends_on": [],
      "files_likely_touched": [
        "internal/state/types.go",
        "internal/state/rundir.go",
        "internal/state/rundir_test.go"
      ],
      "owned_paths": [
        "internal/state"
      ],
      "acceptance_checks": [
        "go test ./internal/state"
      ],
      "risk": "low",
      "size": "small",
      "notes": "Keep backward compatibility for existing run directories."
    }
  ],
  "generated_at": "2026-03-13T12:05:00Z"
}
```

### `execution-plan-review.json`

```json
{
  "schema_version": "0.1",
  "run_id": "run-123",
  "artifact_type": "execution_plan_review",
  "execution_plan_hash": "sha256:...",
  "status": "pass",
  "summary": "Slices are independently testable and reasonably sized.",
  "blocking_findings": [
    {
      "finding_id": "epr-001",
      "severity": "high",
      "category": "oversized_slice",
      "slice_id": "slice-004",
      "summary": "Slice touches too many subsystems to be safely executable.",
      "suggested_repair": "Split council review flow from prompt validation."
    }
  ],
  "warnings": [
    {
      "warning_id": "epr-w-001",
      "slice_id": "slice-007",
      "summary": "Acceptance check is too vague."
    }
  ],
  "reviewers": [
    {
      "reviewer_id": "codex",
      "pass": true,
      "summary": "Overall decomposition is workable."
    }
  ],
  "reviewed_at": "2026-03-13T12:10:00Z"
}
```

## Prompt Contract Direction

### Technical Spec Draft Prompt

Inputs:
- `run-artifact.json`
- repo tree summary
- existing technical spec if present
- project quality gate

Output contract:
- Structured JSON block for machine validation
- Markdown technical spec for human review

Non-negotiable prompt rules:
- Preserve all requirement IDs.
- Emit explicit uncertainty instead of silently guessing.
- Separate normative requirements from implementation suggestions.
- Include state transitions, artifacts, and verification rules.
- Do not generate execution tasks.

### Technical Spec Review Prompt

Council asks:
- Is the draft internally consistent?
- Are the state transitions complete and enforceable?
- Are the artifacts sufficient for deterministic execution?
- Are any claims unverifiable?
- Which missing decisions block planning?

Output contract:
- `pass`
- `blocking_findings`
- `recommended_repairs`
- `confidence`
- `technical_spec_hash`
- `reviewers`

### Execution Plan Draft Prompt

Inputs:
- approved technical spec
- run artifact
- repo structure summary

Non-negotiable prompt rules:
- Prefer independently testable slices.
- Avoid broad cross-cutting tasks unless explicitly justified.
- Usually keep slices scoped to one primary subsystem.
- Preserve requirement traceability.
- Use dependencies only when behavior actually blocks another slice.
- Emit slice sizes conservatively.
- Name likely files and owned paths, even when the plan is uncertain.
- Fail open with explicit warnings when repo structure is ambiguous.

### Execution Plan Review Prompt

Council asks:
- Are slices too large?
- Are slices independently testable?
- Is scope overlap too broad?
- Are dependencies minimal and explicit?
- Are verification steps concrete enough?
- Are any requirement IDs dropped or duplicated?
- Are acceptance checks mechanically runnable?

## Implementation Plan

### Task 1: Define New Artifact Contracts

**Files:**
- Modify: `docs/specs/attest-technical-spec.md`
- Create: `docs/specs/templates/technical-spec-template.md`
- Create: `docs/specs/templates/execution-plan-template.md`
- Modify: `internal/state/types.go`
- Modify: `internal/state/rundir.go`
- Test: `internal/state/rundir_test.go`

- [ ] Step 1: Write failing tests for new artifact file handling in `internal/state/rundir_test.go`.
- [ ] Step 2: Run `go test ./internal/state -run 'TestRunDir'` and confirm the new artifact-path expectations fail.
- [ ] Step 3: Add new state structs and/or path helpers for technical-spec and execution-plan artifacts in `internal/state/types.go` and `internal/state/rundir.go`.
- [ ] Step 4: Re-run `go test ./internal/state` and confirm the new artifact contract passes.
- [ ] Step 5: Add the technical-spec and execution-plan templates under `docs/specs/templates/`.
- [ ] Step 6: Commit with `git commit -m "feat: define planning artifact contracts"`.

### Task 2: Add Technical Spec Drafting Workflow

**Files:**
- Create: `docs/prompts/technical-spec-draft.md`
- Create: `docs/prompts/technical-spec-review.md`
- Modify: `cmd/attest/main.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/state/types.go`
- Test: `cmd/attest/main_test.go`
- Test: `cmd/attest/commands_test.go`

- [ ] Step 1: Add failing CLI tests for `attest tech-spec draft`, `attest tech-spec review`, and `attest tech-spec approve`.
- [ ] Step 2: Run `go test ./cmd/attest -run 'Test.*TechSpec'` and confirm command dispatch fails first.
- [ ] Step 3: Add prompt assets for technical-spec drafting and review.
- [ ] Step 4: Add engine methods and CLI commands that persist draft, review, and approval artifacts.
- [ ] Step 5: Re-run the focused CLI tests and make them pass.
- [ ] Step 6: Commit with `git commit -m "feat: add technical spec workflow"`.

### Task 3: Normalize Existing Technical Specs Into The New Contract

**Files:**
- Modify: `internal/compiler/compiler.go`
- Modify: `internal/compiler/compiler_test.go`
- Create: `internal/engine/techspec.go`
- Test: `internal/engine/engine_test.go`

- [ ] Step 1: Write failing tests for the case where an existing technical spec file is supplied and normalized into the run-scoped artifact.
- [ ] Step 2: Run the focused engine/compiler tests and verify the normalization case fails first.
- [ ] Step 3: Implement normalization logic that accepts an existing repo technical spec and validates required sections.
- [ ] Step 4: Re-run the tests and confirm the normalization path passes.
- [ ] Step 5: Commit with `git commit -m "feat: normalize technical spec inputs"`.

### Task 4: Add Execution Plan Drafting Workflow

**Files:**
- Create: `docs/prompts/execution-plan-draft.md`
- Create: `docs/prompts/execution-plan-review.md`
- Modify: `cmd/attest/main.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/state/types.go`
- Modify: `internal/state/rundir.go`
- Test: `cmd/attest/commands_test.go`
- Test: `internal/engine/engine_test.go`

- [ ] Step 1: Add failing tests for `attest plan draft`, `attest plan review`, and `attest plan approve`.
- [ ] Step 2: Run the focused test slice and verify the new plan workflow fails before implementation.
- [ ] Step 3: Add execution-plan state types, prompt assets, engine methods, and CLI commands.
- [ ] Step 4: Re-run the tests until the plan workflow passes.
- [ ] Step 5: Commit with `git commit -m "feat: add execution plan workflow"`.

### Task 5: Repoint Task Compilation To Execution Plans

**Files:**
- Modify: `internal/compiler/compiler.go`
- Modify: `internal/compiler/compiler_test.go`
- Modify: `internal/engine/engine.go`
- Test: `internal/engine/engine_test.go`
- Test: `cmd/attest/commands_test.go`

- [ ] Step 1: Write failing tests proving compilation consumes an approved execution plan instead of raw requirements.
- [ ] Step 2: Run `go test ./internal/compiler ./internal/engine ./cmd/attest` and verify the compiler path fails first.
- [ ] Step 3: Implement execution-plan-to-task compilation with deterministic IDs, dependencies, and coverage mapping.
- [ ] Step 4: Re-run the compiler/engine/CLI slice until it passes.
- [ ] Step 5: Commit with `git commit -m "feat: compile tasks from approved execution plans"`.

### Task 6: Add Mechanical Validation For Prompt Outputs — DONE

Validation logic is implemented inline in `internal/engine/plan.go` (not as a separate `planlint.go` — the code is small enough to colocate with the review logic).

**Implemented validators:**
- Required fields per slice: `slice_id`, `title`, `goal`, `requirement_ids`, `owned_paths`, `acceptance_checks` (blocking)
- Valid enum values: `risk` ∈ {low, medium, high}, `size` ∈ {small, medium, large} (blocking)
- Duplicate slice IDs (blocking)
- Case-colliding slice IDs (blocking)
- Unknown `depends_on` references (blocking)
- Self-dependencies (blocking)
- Dependency cycles via DFS 3-state coloring (blocking)
- Oversize slice warning: >4 requirement IDs (warning)
- Missing `files_likely_touched` (warning)
- Cross-slice requirement coverage: dropped requirements from the run artifact are blocking findings; requirements covered by multiple slices produce a warning (blocking/warning)

### Task 7: Dogfood On This Repository — DONE

The pipeline has been exercised on the attest repository itself through normal development workflow.

## Verification

Run these before claiming the planning pipeline is usable:

1. `GOCACHE=/tmp/attest-go-build GOMODCACHE=/tmp/attest-go-mod go test ./...`
2. `GOCACHE=/tmp/attest-go-build GOMODCACHE=/tmp/attest-go-mod GOLANGCI_LINT_CACHE=/tmp/attest-golangci-lint just check`
3. `go run ./cmd/attest prepare --spec docs/specs/attest-functional-spec.md`
4. `go run ./cmd/attest tech-spec draft <run-id>`
5. `go run ./cmd/attest tech-spec review <run-id>`
6. `go run ./cmd/attest plan draft <run-id>`
7. `go run ./cmd/attest plan review <run-id>`
8. `go run ./cmd/attest approve <run-id>`
9. `go run ./cmd/attest ready <run-id>`

Expected operational outcomes:
- Technical-spec drafting preserves all requirement IDs.
- Council review can block approval with structured findings.
- Execution plans are smaller and more coherent than direct requirement chunking.
- Task compilation reads approved execution slices, not raw prose adjacency.
- Dogfood runs become easier to work with through `ready`, `next`, and `progress`.

## Open Design Questions (Resolved)

- ~~Whether technical spec approval and execution-plan approval should be separate user commands or a single staged approval surface.~~ **Resolved:** Separate commands — `attest tech-spec approve` and `attest plan approve`.
- ~~Whether council review results should be stored as one merged synthesis or one file per reviewer plus a synthesis.~~ **Resolved:** Per-reviewer JSON files plus a consolidation/synthesis (implemented in the council-review-pipeline).
- ~~Whether the existing handwritten `docs/specs/attest-technical-spec.md` should be treated as the source of truth or as an input to normalization.~~ **Resolved:** Input to normalization via `--from` flag. `DraftTechnicalSpec` normalizes legacy section headings into the canonical template.
- ~~Whether the compiler should retain the current heuristic fallback when no execution plan exists, or fail closed.~~ **Resolved:** Fail closed. `Approve` requires an approved execution plan. `Compile` calls `readApprovedExecutionPlan` and fails if missing.
