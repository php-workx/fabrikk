# attest v1 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the first runnable version of `attest` as a Claude-first autonomous spec-run system with repo-local state, wave-based parallel execution, deterministic verification, and mandatory Codex/Gemini review gates.

**Architecture:** `attest` is a local Go CLI with a durable run-state engine under `.attest/`. The first implementation focuses on spec preparation, approved run artifacts, detached run lifecycle, wave planning, task claims, a deterministic verifier, and a council pipeline where Claude implements while Codex and Gemini review.

**Tech Stack:** Go, Cobra, JSON/YAML, local file-system state, subprocess execution against `claude`, `codex`, and `gemini` CLIs.

---

### Task 1: Scaffold the repository and CLI skeleton

**Files:**
- Create: `go.mod`
- Create: `cmd/attest/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/cli/root_test.go`
- Create: `README.md`

**Step 1: Write the failing CLI test**

Create `internal/cli/root_test.go` with a test that expects `attest --help` to render the root command without panicking.

**Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/...`
Expected: FAIL because the module and root command do not exist yet.

**Step 3: Create the module and root command**

Add:

- `go.mod` with module path `github.com/runger/attest`
- `cmd/attest/main.go` calling `cli.Execute()`
- `internal/cli/root.go` defining the root Cobra command and `Execute()`

**Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/...`
Expected: PASS.

**Step 5: Commit**

```bash
git add go.mod cmd/attest/main.go internal/cli/root.go internal/cli/root_test.go README.md
git commit -m "feat: scaffold attest cli"
```

### Task 1.5: Evaluate existing orchestration libraries before building bespoke flow control

**Files:**
- Create: `docs/research/2026-03-12-orchestration-library-evaluation.md`
- Modify: `docs/specs/attest-technical-spec.md`

**Step 1: Write the evaluation checklist**

The checklist must compare candidate libraries against `attest` constraints:

- repo-local durable state
- detached execution
- resumable runs
- CLI-native control surface
- ability to preserve `attest` as source of truth
- compatibility with multi-model review flow
- wave-based parallel execution compatibility

**Step 2: Research and record findings**

Evaluate at minimum:

- CrewAI
- direct in-process Go orchestration

**Step 3: Decide adoption boundary**

Record whether CrewAI is:

- rejected for v1
- used only for internal flow wiring
- used for a narrow adapter layer

The document must state what `attest` still owns even if CrewAI is used.

It must also state whether v1 uses:

- wave-based parallel execution with scope isolation
- a serial fallback policy for unsafe waves

**Step 4: Validate the decision against the technical spec**

Run: `rg -n "CrewAI|Library selection policy|orchestration" docs/specs/attest-technical-spec.md docs/research/2026-03-12-orchestration-library-evaluation.md`
Expected: the evaluation and the technical spec agree on the chosen boundary.

**Step 5: Commit**

```bash
git add docs/research/2026-03-12-orchestration-library-evaluation.md docs/specs/attest-technical-spec.md
git commit -m "docs: evaluate orchestration library candidates"
```

### Task 2: Implement config and run directory resolution

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/runtime/paths.go`
- Create: `internal/runtime/paths_test.go`

**Step 1: Write failing tests for config resolution**

Cover:

- default run root `.attest/`
- override via env var or CLI option
- deterministic per-run directory resolution

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config ./internal/runtime`
Expected: FAIL because the packages do not exist yet.

**Step 3: Implement path and config resolution**

Implement:

- config struct for run root and backend commands
- path helpers returning run dir, report dir, claim dir

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config ./internal/runtime`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/runtime/paths.go internal/runtime/paths_test.go
git commit -m "feat: add config and run path resolution"
```

### Task 3: Define canonical schemas and persistence layer

**Files:**
- Create: `internal/state/types.go`
- Create: `internal/state/store.go`
- Create: `internal/state/store_test.go`
- Create: `schemas/run-artifact.schema.json`
- Create: `schemas/tasks.schema.json`
- Create: `schemas/run-status.schema.json`

**Step 1: Write failing state round-trip tests**

Cover:

- writing and reading `run-artifact.json`
- writing and reading `tasks.json`
- writing and reading `run-status.json`

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/state`
Expected: FAIL because state types and store do not exist.

**Step 3: Implement canonical state types and store**

Implement:

- strongly typed Go structs for run artifact, task, claim, and run status
- atomic JSON write helper using temp file + rename
- JSON read helper with schema version checks

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/state`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/state/types.go internal/state/store.go internal/state/store_test.go schemas/run-artifact.schema.json schemas/tasks.schema.json schemas/run-status.schema.json
git commit -m "feat: add run state schemas and store"
```

### Task 4: Build spec ingestion and normalized run-artifact generation

**Files:**
- Create: `internal/spec/loader.go`
- Create: `internal/spec/loader_test.go`
- Create: `internal/spec/normalize.go`
- Create: `internal/spec/normalize_test.go`
- Create: `internal/cli/prepare.go`
- Create: `testdata/specs/minimal-functional.md`

**Step 1: Write failing tests for spec parsing**

Cover:

- loading one or more spec files
- extracting stable requirement IDs
- building a normalized run artifact with source references
- failing when no requirement IDs are found

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/spec`
Expected: FAIL because the packages do not exist.

**Step 3: Implement spec loader and prepare command**

Implement:

- markdown loading
- requirement ID extraction by explicit ID markers
- `attest prepare --spec ...` writing a draft `run-artifact.json`
- status state set to `awaiting_approval`

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/spec ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/spec/loader.go internal/spec/loader_test.go internal/spec/normalize.go internal/spec/normalize_test.go internal/cli/prepare.go testdata/specs/minimal-functional.md
git commit -m "feat: add spec ingestion and run artifact preparation"
```

### Task 5: Implement approval and review commands

**Files:**
- Create: `internal/cli/review.go`
- Create: `internal/cli/approve.go`
- Create: `internal/approval/approval.go`
- Create: `internal/approval/approval_test.go`

**Step 1: Write failing approval tests**

Cover:

- approving a prepared run
- rejection when the run is not in `awaiting_approval`
- persisted approval metadata

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/approval ./internal/cli`
Expected: FAIL because approval logic does not exist.

**Step 3: Implement review and approval flow**

Implement:

- `attest review <run-id>` human-readable summary
- `attest approve <run-id>` state transition to `approved`
- persisted `approved_at` and `approved_by`

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/approval ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/cli/review.go internal/cli/approve.go internal/approval/approval.go internal/approval/approval_test.go
git commit -m "feat: add run review and approval flow"
```

### Task 6: Implement deterministic task compilation

**Files:**
- Create: `internal/tasks/compiler.go`
- Create: `internal/tasks/compiler_test.go`
- Create: `internal/cli/compile.go`

**Step 1: Write failing compiler tests**

Cover:

- stable task IDs from the same approved artifact
- requirement-to-task linking
- consistent ordering
- bounded task scopes
- wave grouping for non-overlapping tasks
- serial fallback marking for overlapping tasks

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tasks`
Expected: FAIL because the task compiler does not exist.

**Step 3: Implement the compiler**

Implement:

- deterministic grouping rules
- task IDs derived from stable input
- structured scope manifests
- wave assignment or serial fallback markers
- persisted `tasks.json`
- compile command callable after approval

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tasks ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/tasks/compiler.go internal/tasks/compiler_test.go internal/cli/compile.go
git commit -m "feat: add deterministic task compilation"
```

### Task 7: Implement claims and lease management

**Files:**
- Create: `internal/claims/manager.go`
- Create: `internal/claims/manager_test.go`
- Create: `internal/cli/claim.go`

**Step 1: Write failing claim tests**

Cover:

- exclusive task claims
- lease renewal
- lease expiry and reassignment
- rejection of duplicate active claim
- rejection of overlapping `owned_paths` across active wave claims

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/claims`
Expected: FAIL because claim management does not exist.

**Step 3: Implement claims**

Implement:

- per-task claim files
- lease timestamps
- renewal and expiry behavior
- scope reservation checks across active claims
- `attest claim <run-id> <task-id>`

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/claims ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/claims/manager.go internal/claims/manager_test.go internal/cli/claim.go
git commit -m "feat: add task claims and leases"
```

### Task 8: Implement backend adapters and model routing policy

**Files:**
- Create: `internal/backends/types.go`
- Create: `internal/backends/claude.go`
- Create: `internal/backends/codex.go`
- Create: `internal/backends/gemini.go`
- Create: `internal/router/router.go`
- Create: `internal/router/router_test.go`

**Step 1: Write failing routing tests**

Cover:

- Opus selection for planning and synthesis
- Sonnet selection for implementation
- Haiku selection for scouting
- escalation to Opus after repeated failure

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/router`
Expected: FAIL because the router does not exist.

**Step 3: Implement adapters and router**

Implement:

- backend command templates
- task classification to model tier
- escalation rules
- routing trace recording

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/router`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/backends/types.go internal/backends/claude.go internal/backends/codex.go internal/backends/gemini.go internal/router/router.go internal/router/router_test.go
git commit -m "feat: add backend adapters and model routing"
```

### Task 9: Implement detached launch and worker execution

**Files:**
- Create: `internal/run/launcher.go`
- Create: `internal/run/launcher_test.go`
- Create: `internal/run/worker.go`
- Create: `internal/cli/launch.go`

**Step 1: Write failing run-launch tests**

Cover:

- launching an approved run
- transition to `running`
- spawning detached worker execution metadata
- no launch before approval
- wave selection for disjoint-scope tasks
- serial fallback when overlap prevents a safe wave

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/run`
Expected: FAIL because the launcher does not exist.

**Step 3: Implement detached launch**

Implement:

- launch command
- persisted run state changes
- wave planner and worker dispatch loop skeleton
- reattachable run metadata

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/run ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/run/launcher.go internal/run/launcher_test.go internal/run/worker.go internal/cli/launch.go
git commit -m "feat: add detached run launch flow"
```

### Task 10: Implement deterministic verification

**Files:**
- Create: `internal/verify/verifier.go`
- Create: `internal/verify/verifier_test.go`
- Create: `internal/cli/verify.go`

**Step 1: Write failing verification tests**

Cover:

- missing report fails
- missing evidence fails
- uncovered requirement fails
- valid report passes

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/verify`
Expected: FAIL because the verifier does not exist.

**Step 3: Implement verifier**

Implement:

- report existence checks
- command/evidence checks
- requirement coverage checks
- persisted verifier results

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/verify ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/verify/verifier.go internal/verify/verifier_test.go internal/cli/verify.go
git commit -m "feat: add deterministic verifier"
```

### Task 11: Implement `internal/councilflow` for reviewer fan-out and Opus synthesis

**Files:**
- Create: `internal/councilflow/types.go`
- Create: `internal/councilflow/runner.go`
- Create: `internal/councilflow/runner_test.go`
- Create: `internal/councilflow/backends/codex.go`
- Create: `internal/councilflow/backends/gemini.go`
- Create: `internal/councilflow/backends/opus.go`
- Create: `internal/cli/council.go`

**Step 1: Write failing council tests**

Cover:

- Codex and Gemini reviewer lanes run in parallel for one task attempt
- reviewer results are normalized into typed outputs
- Opus synthesis produces final verdict after both reviewers complete
- blocking disagreement keeps work open
- `attempt.json` metadata can be written from the councilflow result

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/councilflow`
Expected: FAIL because councilflow logic does not exist.

**Step 3: Implement council pipeline**

Implement:

- typed request/result structs for one task-attempt council run
- parallel task review dispatch to Codex and Gemini
- Opus synthesis after both reviewer lanes reach terminal outcomes
- event emission hooks for council progress
- caller-owned artifact persistence using the returned typed results

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/councilflow ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/councilflow/types.go internal/councilflow/runner.go internal/councilflow/runner_test.go internal/councilflow/backends/codex.go internal/councilflow/backends/gemini.go internal/councilflow/backends/opus.go internal/cli/council.go
git commit -m "feat: add councilflow review pipeline"
```

### Task 12: Implement repair reconciliation and task reopening

**Files:**
- Create: `internal/repair/reconcile.go`
- Create: `internal/repair/reconcile_test.go`
- Create: `internal/cli/repair.go`

**Step 1: Write failing repair tests**

Cover:

- reopen parent task on invalidated completion
- create child repair task for narrow follow-up
- preserve requirement linkage

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/repair`
Expected: FAIL because repair reconciliation does not exist.

**Step 3: Implement repair logic**

Implement:

- mapping findings to reopen vs child task
- updating `tasks.json`
- marking runs blocked when automatic repair is not safe

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/repair ./internal/tasks`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/repair/reconcile.go internal/repair/reconcile_test.go internal/cli/repair.go
git commit -m "feat: add repair task reconciliation"
```

### Task 13: Implement status, explain, and resume commands

**Files:**
- Create: `internal/status/render.go`
- Create: `internal/status/render_test.go`
- Create: `internal/cli/status.go`
- Create: `internal/cli/explain.go`
- Create: `internal/cli/resume.go`

**Step 1: Write failing status tests**

Cover:

- summary of task counts by state
- blocker explanation output
- uncovered requirement rendering
- later-session resume from repo-local state

**Step 2: Run the tests to verify they fail**

Run: `go test ./internal/status ./internal/cli`
Expected: FAIL because status rendering does not exist.

**Step 3: Implement status and resume flow**

Implement:

- `attest status`
- `attest explain`
- `attest resume`
- human-readable summaries from `run-status.json` and task reports

**Step 4: Run the tests to verify they pass**

Run: `go test ./internal/status ./internal/cli`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/status/render.go internal/status/render_test.go internal/cli/status.go internal/cli/explain.go internal/cli/resume.go
git commit -m "feat: add status and resume commands"
```

### Task 14: Add end-to-end run tests

**Files:**
- Create: `integration/run_lifecycle_test.go`
- Create: `testdata/runs/minimal/`
- Modify: `README.md`

**Step 1: Write the end-to-end failing test**

Cover:

- prepare -> approve -> compile -> launch
- worker completion report
- verifier result
- Codex and Gemini review stubs
- council result
- repaired task or successful completion

**Step 2: Run the test to verify it fails**

Run: `go test ./integration/...`
Expected: FAIL because the end-to-end lifecycle is not yet wired together.

**Step 3: Implement missing integration glue**

Wire the existing modules together until the full lifecycle passes under test fixtures and stubbed backend execution.

**Step 4: Run the full test suite**

Run: `go test ./...`
Expected: PASS.

**Step 5: Commit**

```bash
git add integration/run_lifecycle_test.go testdata/runs/minimal README.md
git commit -m "test: add attest lifecycle integration coverage"
```

### Task 15: Write operator and contributor documentation

**Files:**
- Create: `docs/runbook.md`
- Create: `docs/backend-setup.md`
- Modify: `README.md`

**Step 1: Write the documentation gaps test or checklist**

Create a documentation checklist covering:

- installing `attest`
- required CLIs
- preparing a run
- approving and launching a run
- status and resume
- backend limitations

**Step 2: Generate the docs**

Write:

- runbook for daily usage
- backend setup for Claude, Codex, and Gemini

**Step 3: Validate docs against the current CLI surface**

Run: `rg -n "attest (prepare|approve|launch|status|resume|verify|council)" README.md docs/`
Expected: each implemented command is documented.

**Step 4: Commit**

```bash
git add docs/runbook.md docs/backend-setup.md README.md
git commit -m "docs: add attest operator documentation"
```

## Final Verification

Run:

```bash
go test ./...
```

Expected:

- all unit and integration tests pass
- run artifact, task compilation, verification, review, repair, and resume flows are covered

Run:

```bash
git status --short
```

Expected:

- no unexpected modifications

Run:

```bash
attest --help
```

Expected:

- root command renders without panic and lists the major workflow commands
