# Spec: Public `ticket/` Package

## Context

`internal/ticket` is fabrikk's Go store for the `.tickets/` markdown+YAML format. Subprojects can't import it because it's under `internal/`. The fix is to promote the implementation to a true public package and move the ticket-relevant types out of `internal/state` alongside it. Engine-specific types (`RunArtifact`, `CouncilResult`, `Wave`, etc.) stay internal.

`go.mod` is not affected — all packages remain within the same module.

## Outcome

- `github.com/php-workx/fabrikk/ticket` — public, self-contained; owns both the types and the implementation
- `internal/state` — retains engine-only types; adds type aliases so its 34 callers need **zero import changes**
- `internal/ticket` — deleted (replaced by `ticket/`)

## Type split

### Move to `ticket/`

These types are stored in `.tickets/` files and must be accessible to public consumers:

| Type / Symbol | Currently in | Notes |
|---|---|---|
| `TaskStore` interface | `internal/state/types.go` | |
| `ClaimableStore` interface | `internal/state/types.go` | |
| `Task` struct + `DeriveTags()` method | `internal/state/types.go` | method must move with its receiver |
| `TaskStatus` type + 9 constants | `internal/state/types.go` | `TaskPending` … `TaskFailed` |
| `TaskScope` struct | `internal/state/types.go` | owned/readonly/shared paths |
| `Claim` struct | `internal/state/types.go` | exclusive worker lease |
| `LearningRef` struct | `internal/state/types.go` | referenced by `Task.LearningContext` |
| `ValidationCheck` struct | `internal/state/types.go` | referenced by `Task.ValidationChecks` |
| `ImplementationDetail` struct + `IsZero()` method | `internal/state/types.go` | method must move with its receiver |
| `FileChange` struct | `internal/state/types.go` | referenced by `ImplementationDetail.FilesToModify` |
| `ErrPartialRead` | `internal/state/types.go` line 13 | move to `ticket/errors.go`, not `ticket/types.go` |

### Stay in `internal/state`

Engine-only types — not stored in `.tickets/` files, not needed by external consumers:

`RunArtifact`, `ArtifactStatus`, `ReviewStatus`, `SourceSpec`, `Requirement`, `Clarification`, `RoutingPolicy`, `QualityGate`, structural-analysis types (`StructuralAnalysis`, `StructuralNode`, `StructuralEdge`), exploration types (`ExplorationResult`, `FileInfo`, `SymbolInfo`, `TestFileInfo`, `ReusePoint`), `RequirementCoverage`, `RunStatus`, `RunState`, `CompletionReport`, `CommandResult`, `VerifierResult`, `Check`, `Finding`, `Attempt`, `ReviewResult`, `CouncilResult`, `ReviewSummary`, `ReviewWarning`, `ReviewFinding`, `TechnicalSpecReview`, `Wave`, `FileConflict`, `SharedFileEntry`, `ExecutionSlice`, `ExecutionPlan`, `ExecutionPlanReview`, `ArtifactApproval`, `EngineInfo`, `TaskDetail`, `Event`, learning types (`LearningCategory`, `LearningQueryOpts`, `LearningEnricher`, `Boundaries`), `RunDir`, `RunDirTaskStore`, `atomicWrite`/`WriteJSON`/`WriteAtomic`.

**Note on `ExecutionSlice`:** It has fields `[]ValidationCheck` and `ImplementationDetail` — both moved types. It stays in `internal/state` and references them via the type aliases added in step 3. No changes needed to `ExecutionSlice` itself.

**Note on `rundir.go`:** Uses `Task`, `TaskStatus`, `TaskStore`, and `Event` unqualified (same package). After adding the aliases to `internal/state/types.go`, these resolve transparently via `type Task = ticket.Task` etc. `rundir.go` requires no edits.

## Implementation steps

> **Ordering is critical.** Steps 2–3 must complete before step 4. Step 3 removes `ticket/`'s dependency on `internal/state`. Step 4 adds `internal/state`'s dependency on `ticket/`. If step 4 runs before step 3, there is a circular import and the module will not compile.

---

### Step 1 — Move `internal/ticket` → `ticket/`

```bash
git mv internal/ticket ticket
```

This moves all 11 files (6 source + 5 test). After this, `ticket/*.go` still import `github.com/php-workx/fabrikk/internal/state` — that is expected and will be fixed in step 3.

---

### Step 2 — Create `ticket/types.go` with the moved types

Create a new file `ticket/types.go` in package `ticket`. Cut the following from `internal/state/types.go` and paste here:

- The `TaskStore` and `ClaimableStore` interface declarations (lines 19–39)
- `Task` struct and its `DeriveTags()` method (lines 266–337)
- `LearningRef`, `ValidationCheck`, `ImplementationDetail` (+ `IsZero()`), `FileChange` structs (lines 339–263)
- `TaskScope` struct (lines 357–362)
- `TaskStatus` type + 9 constants (lines 364–378)
- `Claim` struct (lines 381–390)

Add the imports that `ticket/types.go` needs: `"sort"`, `"strings"`, `"time"`.

Add `ErrPartialRead` to the existing `ticket/errors.go` (it already contains the 9 store-specific sentinels — this is an addition to that file, not a new file):

```go
// ErrPartialRead indicates some task files could not be read but partial results
// are available. Callers should use the returned tasks and treat the error as a warning.
var ErrPartialRead = errors.New("partial read: some task files were skipped")
```

---

### Step 3 — Remove `internal/state` dependency from `ticket/*.go`

Now that the types live in the same package, remove the `internal/state` import and drop the `state.` qualifier from all references.

**3a. Strip the `state.` prefix from all moved-type references in `ticket/` source and test files:**

```bash
find ticket/ -name "*.go" | xargs sed -i '' \
  -e 's/state\.Task\b/Task/g' \
  -e 's/state\.TaskStatus\b/TaskStatus/g' \
  -e 's/state\.TaskScope\b/TaskScope/g' \
  -e 's/state\.Claim\b/Claim/g' \
  -e 's/state\.TaskStore\b/TaskStore/g' \
  -e 's/state\.ClaimableStore\b/ClaimableStore/g' \
  -e 's/state\.LearningRef\b/LearningRef/g' \
  -e 's/state\.ValidationCheck\b/ValidationCheck/g' \
  -e 's/state\.ImplementationDetail\b/ImplementationDetail/g' \
  -e 's/state\.FileChange\b/FileChange/g' \
  -e 's/state\.TaskPending\b/TaskPending/g' \
  -e 's/state\.TaskClaimed\b/TaskClaimed/g' \
  -e 's/state\.TaskImplementing\b/TaskImplementing/g' \
  -e 's/state\.TaskVerifying\b/TaskVerifying/g' \
  -e 's/state\.TaskUnderReview\b/TaskUnderReview/g' \
  -e 's/state\.TaskRepairPending\b/TaskRepairPending/g' \
  -e 's/state\.TaskBlocked\b/TaskBlocked/g' \
  -e 's/state\.TaskDone\b/TaskDone/g' \
  -e 's/state\.TaskFailed\b/TaskFailed/g' \
  -e 's/state\.ErrPartialRead\b/ErrPartialRead/g'
```

**3b. Remove the `internal/state` import line from each `ticket/*.go` file.** Do this manually per file (six source files, four test files) or via:

```bash
find ticket/ -name "*.go" | xargs sed -i '' \
  '/\"github\.com\/php-workx\/fabrikk\/internal\/state\"/d'
```

**3c. Check for any remaining `state.` references in `ticket/`:**

```bash
grep -r 'state\.' ticket/
```

This must return empty. If it doesn't, resolve remaining qualified references before proceeding.

**3d. Verify no circular dependency yet:**

```bash
go build ./ticket/...
```

Must compile cleanly before continuing to step 4.

---

### Step 4 — Update `internal/state/types.go`

**4a.** Remove from `internal/state/types.go`:
- The 11 types/symbols listed in the "Move to `ticket/`" table
- The `"errors"` import if it is now unused (check: `grep '"errors"' internal/state/types.go`)
- The `"sort"` and `"strings"` imports if unused after removing `DeriveTags()`

**4b.** Add at the top of `internal/state/types.go`:

```go
import "github.com/php-workx/fabrikk/ticket"

// Type aliases — preserve existing callers; no import changes required.
type (
    Task                 = ticket.Task
    TaskStatus           = ticket.TaskStatus
    TaskScope            = ticket.TaskScope
    Claim                = ticket.Claim
    TaskStore            = ticket.TaskStore
    ClaimableStore       = ticket.ClaimableStore
    LearningRef          = ticket.LearningRef
    ValidationCheck      = ticket.ValidationCheck
    ImplementationDetail = ticket.ImplementationDetail
    FileChange           = ticket.FileChange
)

// Constant redeclarations — type aliases do not cover constants.
const (
    TaskPending       = ticket.TaskPending
    TaskClaimed       = ticket.TaskClaimed
    TaskImplementing  = ticket.TaskImplementing
    TaskVerifying     = ticket.TaskVerifying
    TaskUnderReview   = ticket.TaskUnderReview
    TaskRepairPending = ticket.TaskRepairPending
    TaskBlocked       = ticket.TaskBlocked
    TaskDone          = ticket.TaskDone
    TaskFailed        = ticket.TaskFailed
)

var ErrPartialRead = ticket.ErrPartialRead
```

**4c.** Verify `internal/state` compiles:

```bash
go build ./internal/state/...
```

---

### Step 5 — Update callers of `internal/ticket` → `ticket`

The following Go files import `internal/ticket` and need the path updated:

- `cmd/fabrikk/tasks.go`
- `cmd/fabrikk/commands_test.go`
- `internal/engine/engine_test.go`

```bash
find . -name "*.go" | xargs sed -i '' \
  's|github\.com/php-workx/fabrikk/internal/ticket|github.com/php-workx/fabrikk/ticket|g'
```

Confirm no references remain:

```bash
grep -r 'internal/ticket' . --include='*.go'
```

Must return empty.

---

### Step 6 — Format and quality gate

```bash
gofumpt -l -w -extra ticket/ internal/state/types.go
just check
```

`just check` runs the full quality gate (lint + race tests + vuln scan). All checks must pass.

---

## Critical files summary

| File | Change |
|---|---|
| `ticket/types.go` | **New** — moved types from `internal/state` |
| `ticket/errors.go` | **Modified** — add `ErrPartialRead` |
| `ticket/store.go` | **Modified** — remove `internal/state` import, unqualify type refs |
| `ticket/format.go` | **Modified** — remove `internal/state` import, unqualify type refs |
| `ticket/claim.go` | **Modified** — remove `internal/state` import, unqualify type refs |
| `ticket/deps.go` | **Modified** — remove `internal/state` import, unqualify type refs |
| `ticket/*_test.go` (4 files) | **Modified** — remove `internal/state` import, unqualify type refs |
| `internal/state/types.go` | **Modified** — remove moved types, add aliases + constant redeclarations |
| `cmd/fabrikk/tasks.go` | **Modified** — update import path |
| `cmd/fabrikk/commands_test.go` | **Modified** — update import path |
| `internal/engine/engine_test.go` | **Modified** — update import path |

**Unchanged:** `internal/state/rundir.go`, `internal/state/atomic.go`, all engine/compiler/verifier source files (they use `state.Task` etc. which resolve transparently via the new aliases).

## Verification

1. `go build ./ticket/...` — ticket package compiles in isolation (end of step 3)
2. `go build ./internal/state/...` — state package compiles with aliases (end of step 4)
3. `go build ./...` — full module compiles
4. `go test ./...` — all tests pass; no logic was changed
5. `grep -r 'internal/ticket' . --include='*.go'` — empty; old path fully retired
6. `grep -r 'state\.' ticket/ --include='*.go'` — empty; no residual internal/state dependency in public package
7. `go doc ./ticket/` — exported API is visible and correct
8. `just check` — full quality gate passes
