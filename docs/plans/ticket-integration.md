# Task: Integrate Ticket (tk) as Unified Task Store

**Status:** Planned (post pre-mortem revision)
**Branch:** `feat/track-work`
**Date:** 2026-03-19
**Pre-mortem:** 18 findings, 4 critical — all resolved in this revision

---

## Context

Beads created friction — git hooks, auto-commits, overhead. Ticket (`wedow/ticket`) replaces it as the unified task tracking system for both agent work coordination and attest's execution engine. Currently tasks live in `.attest/runs/<run-id>/tasks.json` as a JSON array. Ticket stores tasks as individual markdown files with YAML frontmatter in `.tickets/`. Agents use `tk ready`/`tk start`/`tk close` to grab and complete work. Attest's compiler writes tasks in Ticket format. Everything is human-readable markdown.

## Architecture

**The core abstraction is a `TaskStore` interface** defined in `internal/state/`. This is attest's contract — it knows nothing about Ticket, markdown, or any specific storage format. The engine, compiler, CLI, and all internal code interact only with this interface.

**Ticket is the first implementation** of TaskStore, living in `internal/ticket/`. If Ticket needs to be replaced, only this package changes — zero modifications to engine, compiler, or CLI code.

```
┌──────────────────────────────────────────┐
│ attest engine / compiler / CLI           │
│ (only knows TaskStore interface)         │
└──────────────────┬───────────────────────┘
                   │ TaskStore interface
       ┌───────────┴───────────┐
       ▼                       ▼
┌──────────────┐    ┌──────────────────┐
│ ticket.Store │    │ (future backend) │
│ .tickets/    │    │ SQLite, API, ... │
└──────────────┘    └──────────────────┘
       ▲
       │ compatible file format
       ▼
┌──────────────┐
│ tk CLI       │
│ (human/agent)│
└──────────────┘
```

### Key Design Decisions

1. **Clean interface boundary**: `TaskStore` is the ONLY contract. No Ticket-specific types leak into the engine or CLI.

2. **Runs as epics** (pm-006 resolution): `.tickets/` is project-global. Each attest run creates an epic ticket (`type: epic`, `id: <run-id>`). Compiled tasks are children (`parent: <run-id>`). Queries filter by parent to scope to a run. This uses Ticket's native parent/child hierarchy — no custom extension needed.
   ```
   .tickets/
     run-1773550987.md       (type: epic)
     task-slice-1.md         (parent: run-1773550987)
     task-slice-2.md         (parent: run-1773550987)
     run-1773600000.md       (type: epic, different run)
     task-impl-auth.md       (parent: run-1773600000)
   ```
   `tk show run-1773550987` displays all children. Agents filter with `tk query 'select(.parent == "run-1773550987")'`.

3. **Git tracking** (pm-003 resolution): `.tickets/` is committed to git (matches `tk`'s expected workflow). `.tickets/*.lock` is gitignored. This means tickets are visible in PRs and shared across branches — which is the intended behavior for agent work coordination.

4. **Status mapping**: Ticket has 3 statuses (open/in_progress/closed). Store attest's full 9-value status in a custom `attest_status` YAML field. Map to Ticket's native `status` for `tk ready`/`tk blocked` compatibility. Mapping logic lives entirely in `internal/ticket/`.

5. **Deterministic IDs**: Use attest's `task-<slug>` IDs as Ticket IDs. Filename = `<task-id>.md`. So `tk show task-slice-1` works. `GenerateID()` is only for standalone `tk`-compatible ticket creation outside of attest compile (pm-014).

6. **Direct file I/O**: Go reads/writes `.tickets/*.md` directly. No shelling out to `tk`. The `tk` CLI is for humans/agents, not for attest internals.

7. **File locking** (pm-002 resolution): Use `github.com/gofrs/flock` for cross-platform file locking (works on macOS, Linux, Windows). Not `syscall.Flock` which is Unix-only.

8. **Body preservation** (pm-013 resolution): `WriteTask` does read-modify-write on frontmatter only, preserving the markdown body (notes, descriptions, design sections). Only `WriteTasks` (bulk create from compiler) writes the full file including generated body sections.

9. **Unknown field preservation** (pm-012 resolution): `TicketFrontmatter` includes `Extra map[string]interface{} \`yaml:",inline"\`` to preserve any YAML fields not in our struct. This ensures forward compatibility with future `tk` versions.

10. **Dual-write transition**: Initially write both `tasks.json` and `.tickets/`. Drop JSON after validation.

11. **Swappable**: To replace Ticket, implement `TaskStore` in a new package. Change one constructor in `cmd/attest/main.go`.

### TaskStore Interface

```go
type TaskStore interface {
    // Run-scoped operations (filter by parent epic)
    ReadTasks(runID string) ([]Task, error)
    WriteTasks(runID string, tasks []Task) error

    // Single-task operations
    ReadTask(taskID string) (*Task, error)
    WriteTask(task *Task) error
    UpdateStatus(taskID string, status TaskStatus, reason string) error

    // Run lifecycle
    CreateRun(runID string) error  // creates epic ticket for the run
}
```

`ReadTasks(runID)` returns only tasks where `parent == runID`. This scopes all engine queries to the correct run (pm-006 resolution).

**Import constraint**: Nothing in `internal/state/` or `internal/engine/` imports `internal/ticket/`. The Ticket dependency exists only in `cmd/attest/main.go` where the store is constructed.

---

## Stages

### Stage 1: `internal/ticket` package (new, no callers yet)

Reimplement Ticket's core logic in Go (based on deep analysis of the 1510-line bash script). No shelling out to `tk`.

#### 1.1 New files

| File | Purpose |
|------|---------|
| `internal/ticket/store.go` | `Store` struct implementing `state.TaskStore`. File locking via flock for multi-agent safety. |
| `internal/ticket/format.go` | Marshal/unmarshal between `state.Task` and markdown+YAML (using `gopkg.in/yaml.v3`) |
| `internal/ticket/id.go` | ID generation matching Ticket's `{prefix}-{4char}` format, with collision detection |
| `internal/ticket/deps.go` | Dependency resolution, cycle detection (DFS with 3-state coloring), ready/blocked queries |
| `internal/ticket/format_test.go` | Round-trip tests, `tk` CLI compatibility tests |
| `internal/ticket/store_test.go` | CRUD, concurrent writes, partial ID matching |
| `internal/ticket/deps_test.go` | Cycles, ready/blocked, missing deps |

#### 1.2 Ticket file format specification

The Go adapter reads and writes this exact format, compatible with `tk` CLI:

```yaml
---
id: task-slice-1
title: "Implement auth module"
status: open              # tk-native: open|in_progress|closed
attest_status: pending    # attest-native: full 9-value status
type: implementation      # task|bug|feature|epic|chore
priority: 1               # 0-4, 0=highest
tags: [auth, high-risk]
deps: [task-slice-0]      # tk-native dependency field (directional)
links: []                 # tk-native symmetric links
parent:                   # optional parent ticket ID (organizational)
assignee:                 # optional, defaults to git user.name
external-ref:             # optional external reference
created: "2026-03-19T10:00:00Z"  # ISO 8601 UTC, immutable
# --- attest-specific fields (custom YAML, preserved by tk) ---
requirement_ids: [REQ-AUTH-1, REQ-AUTH-2]
risk_level: high          # low|medium|high
order: 2                  # position in execution plan
etag: "a1b2c3d4"         # content hash for optimistic concurrency
lineage_id: task-slice-1  # for task versioning across retries
default_model: sonnet     # haiku|sonnet|opus
isolation_mode: direct    # direct|patch_handoff
owned_paths: [internal/auth, cmd/attest]
read_only_paths: []
shared_paths: []
status_reason: ""         # explanation for blocked/failed status
required_evidence: [quality_gate_pass, test_pass]
created_from: ""          # source task ID if repair/followup
updated_at: "2026-03-19T10:00:00Z"  # last modification timestamp
# Note: parent_task_id is unified with tk-native 'parent' field (pm-017)
---
# Implement auth module

Description paragraph(s) — free-form markdown.

## Scope

Owned paths: internal/auth, cmd/attest
Read-only paths: internal/state
Isolation mode: direct

## Acceptance Criteria

- quality_gate_pass
- test_pass

## Notes

**2026-03-19T15:22:33Z**

Agent claimed task, starting implementation.
```

**Field categories:**
- **tk-native fields** (`id`, `status`, `deps`, `links`, `parent`, `created`, `type`, `priority`, `assignee`, `external-ref`, `tags`): Understood and managed by `tk` CLI. Our Go code must preserve these exactly.
- **attest-specific fields** (`attest_status`, `requirement_ids`, `risk_level`, `order`, `etag`, etc.): Custom YAML keys. `tk` preserves them as pass-through (does not modify or validate). Our Go code owns these.

**Body sections:**
- `# Title` — ticket heading (required)
- Free-form description (optional)
- `## Scope` — owned/readonly/shared paths (generated by compiler)
- `## Acceptance Criteria` — required evidence list
- `## Design` — design notes (optional, human/agent authored)
- `## Notes` — append-only timestamped notes (added via `tk add-note` or Go adapter)

#### 1.3 Status mapping

| `state.TaskStatus` | `attest_status` field | `status` field (tk-native) | `tk ready` shows? | `tk blocked` shows? |
|---|---|---|---|---|
| `TaskPending` | `pending` | `open` | Yes (if deps met) | Yes (if deps unmet) |
| `TaskClaimed` | `claimed` | `in_progress` | No | No |
| `TaskImplementing` | `implementing` | `in_progress` | No | No |
| `TaskVerifying` | `verifying` | `in_progress` | No | No |
| `TaskUnderReview` | `under_review` | `in_progress` | No | No |
| `TaskRepairPending` | `repair_pending` | `open` | Yes (if deps met) | Yes (if deps unmet) |
| `TaskBlocked` | `blocked` | `open` | No (always blocked) | Yes |
| `TaskDone` | `done` | `closed` | No | No |
| `TaskFailed` | `failed` | `closed` | No | No |

**Read logic**: When Go reads a ticket, it uses `attest_status` if present. If absent (ticket created by `tk`), it maps from `status`: open→pending, in_progress→claimed, closed→done.

**Write logic**: When Go writes a ticket, it always sets both `status` and `attest_status`. The `status` field is derived from `attest_status` via the mapping table.

#### 1.4 Logic ported from Ticket bash script (detailed)

**ID generation** (`id.go`):
```
prefix = first letter of each hyphen/underscore segment of directory name
         e.g., "my-cool-project" → "mcp"
         fallback: first 3 chars if single segment
hash   = 4 random lowercase alphanumeric chars from crypto/rand
id     = prefix + "-" + hash
```
- Check `.tickets/{id}.md` doesn't exist before writing (collision detection — Ticket doesn't do this)
- Retry up to 3 times on collision

**Partial ID matching** (`store.go`):
```
1. Try exact match: .tickets/{input}.md exists → return it
2. Glob: .tickets/*{input}*.md
3. If 1 match → return it
4. If >1 matches → return ErrAmbiguousID with list of matches
5. If 0 matches → return ErrTicketNotFound
```
- Trim whitespace from input (handles agent output quirks)
- Case-sensitive matching (filesystem dependent)

**Ready algorithm** (`deps.go`):
```
func (s *Store) ReadyTasks() ([]Task, error):
  allTasks = ReadTasks()
  statusMap = map[id]→status for all tasks
  ready = []
  for each task:
    if task.status not in {open, in_progress}: skip
    if task.attest_status == "blocked": skip  // explicitly blocked
    if len(task.deps) == 0: ready (no blockers)
    allDepsClosed = true
    for each dep in task.deps:
      if statusMap[dep] != "closed":
        allDepsClosed = false
        break
      // Note: missing dep (not in statusMap) = NOT closed = not ready
    if allDepsClosed: ready
  sort ready by: priority (ascending), then id (lexicographic)
  return ready
```

**Blocked algorithm** (`deps.go`):
```
func (s *Store) BlockedTasks() ([]Task, error):
  Same as ready, but collect tasks where:
    - status is open or in_progress
    - has ≥1 dep that is NOT closed
  For each blocked task, collect unresolved dep IDs
  sort by: priority (ascending), then id
```

**Cycle detection** (`deps.go`):
```
func (s *Store) DetectCycles() ([][]string, error):
  Build adjacency map from deps (only open/in_progress tickets)
  DFS with 3-state coloring:
    WHITE (0) = unvisited
    GRAY (1) = in current path
    BLACK (2) = fully explored
  When hitting GRAY node → backtrace to extract cycle
  Normalize: rotate cycle so lexicographic-smallest ID is first
  Deduplicate normalized cycles
  Return list of cycles (each is []string of IDs)
```

**Bidirectional links** (`store.go`) — **deferred** (tracked as att-j911):
```
func (s *Store) Link(idA, idB string) error:
  taskA = ReadTask(idA)
  taskB = ReadTask(idB)
  if idB not in taskA.Links: append, WriteTask(taskA)
  if idA not in taskB.Links: append, WriteTask(taskB)
```
- Links are symmetric: stored in both files
- Idempotent: skip if already linked
- **Status**: Stubbed with "not yet implemented" — not needed until agent coordination requires links

**Deps** (`store.go`):
```
func (s *Store) AddDep(id, depID string) error:
  task = ReadTask(id)
  if depID not in task.Deps: append, WriteTask(task)
  // Deps are directional: only stored in the dependent ticket
  // depID ticket is NOT modified
```
- Validate depID exists (warn if not — Ticket doesn't do this)

**Notes** (`store.go`):
```
func (s *Store) AddNote(id, text string) error:
  Read file content
  If no "## Notes" section: append "\n## Notes\n"
  Append: "\n**{ISO-8601-UTC}**\n\n{text}\n"
  Write file atomically
```

**Atomic writes** (`store.go`):
```
func atomicWrite(path string, data []byte) error:
  tmp = os.CreateTemp(dir, ".attest-ticket-*")  // random suffix, safer than PID
  Write data to tmp
  fsync tmp
  os.Rename(tmp, path)
  fsync parent directory (best-effort)
```
- Uses `os.CreateTemp` for unique temp files (avoids PID-reuse collisions)
- Cleanup on every failure path (remove tmp on write/sync/close/rename errors)

**File locking** (`store.go`):
```
func (s *Store) withLock(path string, fn func() error) error:
  lockPath = path + ".lock"
  f = os.OpenFile(lockPath, O_CREATE|O_RDWR, 0644)
  syscall.Flock(f.Fd(), LOCK_EX)  // blocking exclusive lock
  defer syscall.Flock(f.Fd(), LOCK_UN)
  defer f.Close()
  return fn()
```
- Per-file locking (not global) — allows concurrent operations on different tickets
- Ticket bash script has NO locking — this is our improvement
- Lock files are lightweight: created alongside ticket files, cleaned up on close

#### 1.5 Improvements over Ticket bash script

| Area | Ticket (bash) | Our Go implementation |
|------|---------------|----------------------|
| YAML parsing | Regex-based (`sed`/`awk`), silently corrupts on malformed input | `gopkg.in/yaml.v3` with proper error handling |
| Concurrency | No locking, last-writer-wins race condition | Per-file flock, optimistic concurrency via ETag |
| ID collision | Not detected, silent overwrite | Check before write, retry up to 3 times |
| Dep validation | Can reference non-existent IDs | Warn on missing dep targets at creation |
| Performance | Shell fork per operation, regex per field | Native Go, single file read per operation |
| Error handling | Silent degradation on corrupt files | Structured errors with context |
| Type safety | String-based, no validation | Go structs with typed fields |

#### 1.6 Go function signatures

**store.go:**
```go
package ticket

type Store struct {
    Dir string // path to .tickets/ directory
}

func NewStore(dir string) *Store

// TaskStore interface implementation
func (s *Store) ReadTasks(runID string) ([]state.Task, error)   // filter by parent=runID
func (s *Store) WriteTasks(runID string, tasks []state.Task) error  // create epic + children
func (s *Store) ReadTask(taskID string) (*state.Task, error)    // partial ID match
func (s *Store) WriteTask(task *state.Task) error               // frontmatter-only update (preserves body)
func (s *Store) UpdateStatus(taskID string, status state.TaskStatus, reason string) error
func (s *Store) CreateRun(runID string) error                   // creates epic ticket

// Ticket-specific operations (beyond TaskStore)
func (s *Store) ReadyTasks(runID string) ([]state.Task, error)  // open + deps satisfied, scoped to run
func (s *Store) BlockedTasks(runID string) ([]state.Task, error) // open + deps unmet, scoped to run
func (s *Store) AddDep(id, depID string) error
func (s *Store) RemoveDep(id, depID string) error
func (s *Store) Link(idA, idB string) error
func (s *Store) Unlink(idA, idB string) error
func (s *Store) AddNote(id, text string) error
func (s *Store) DetectCycles(runID string) ([][]string, error)  // scoped to run
```

**Note** (pm-010 resolution): `WriteTasks` and `WriteTask` auto-create `.tickets/` via `os.MkdirAll` internally. No separate `Init()` call required.

**Note** (pm-011 resolution): ETag computation always uses canonical JSON serialization (`computeETag` from `compiler.go`), never YAML hashing. The ETag stored in YAML is the same value that would be in `tasks.json`.

**format.go:**
```go
// Frontmatter holds all YAML fields (both tk-native and attest-custom).
// (Named Frontmatter in implementation, not TicketFrontmatter — shorter.)
type Frontmatter struct {
    // tk-native fields
    ID          string   `yaml:"id"`
    Title       string   `yaml:"title,omitempty"`
    Status      string   `yaml:"status"`
    Deps        []string `yaml:"deps,omitempty"`
    Links       []string `yaml:"links,omitempty"`
    Created     string   `yaml:"created"`
    Type        string   `yaml:"type,omitempty"`
    Priority    int      `yaml:"priority"`
    Assignee    string   `yaml:"assignee,omitempty"`
    ExternalRef string   `yaml:"external-ref,omitempty"`
    Parent      string   `yaml:"parent,omitempty"`
    Tags        []string `yaml:"tags,omitempty"`

    // attest-specific fields
    AttestStatus     string   `yaml:"attest_status,omitempty"`
    RequirementIDs   []string `yaml:"requirement_ids,omitempty"`
    RiskLevel        string   `yaml:"risk_level,omitempty"`
    Order            int      `yaml:"order"`           // NO omitempty: 0 is valid (pm-009)
    ETag             string   `yaml:"etag,omitempty"`
    LineageID        string   `yaml:"lineage_id,omitempty"`
    DefaultModel     string   `yaml:"default_model,omitempty"`
    IsolationMode    string   `yaml:"isolation_mode,omitempty"`
    OwnedPaths       []string `yaml:"owned_paths,omitempty"`
    ReadOnlyPaths    []string `yaml:"read_only_paths,omitempty"`
    SharedPaths      []string `yaml:"shared_paths,omitempty"`
    StatusReason     string   `yaml:"status_reason,omitempty"`
    RequiredEvidence []string `yaml:"required_evidence,omitempty"`
    CreatedFrom      string   `yaml:"created_from,omitempty"`
    UpdatedAt        string   `yaml:"updated_at,omitempty"`

    // Catch-all for unknown fields (pm-012: forward compatibility with future tk versions)
    Extra map[string]interface{} `yaml:",inline"`
}
```

**Note on `parent` field** (pm-017 resolution): The tk-native `parent` field serves as both organizational hierarchy AND attest's run-scoping mechanism. The separate `parent_task_id` field from `state.Task` maps to `parent`. There is no separate `parent_task_id` YAML field — they are unified.

**Note on time formatting** (pm-018 resolution): All time fields use `time.RFC3339` format, always UTC. Conversion helpers:
```go
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }
func parseTime(s string) time.Time   // returns zero time on parse failure (pragmatic — callers don't check)
```

func MarshalTicket(task *state.Task) ([]byte, error)    // Task → markdown bytes
func UnmarshalTicket(data []byte) (*state.Task, error)  // markdown bytes → Task
func TaskToFrontmatter(task *state.Task) *Frontmatter
func FrontmatterToTask(fm *Frontmatter) *state.Task
func StatusToTicket(s state.TaskStatus) string           // attest → tk status
func StatusFromTicket(tkStatus, attestStatus string) state.TaskStatus  // tk → attest
```

**id.go:**
```go
func GenerateID(dir string) (string, error)  // prefix-XXXX with collision check
func ResolveID(dir, partial string) (string, error)  // partial match with ambiguity detection
```

**deps.go:**
```go
func ReadyFilter(tasks []state.Task) []state.Task     // open + all deps closed
func BlockedFilter(tasks []state.Task) []state.Task   // open + any dep unresolved
func DetectCycles(tasks []state.Task) [][]string       // DFS cycle detection
```

#### 1.7 Error types

```go
var (
    ErrTicketNotFound = errors.New("ticket not found")
    ErrAmbiguousID    = errors.New("ambiguous ticket ID")
    ErrIDCollision    = errors.New("ticket ID collision after retries")
    ErrCorruptYAML    = errors.New("corrupt ticket YAML")
    ErrCycleDetected  = errors.New("dependency cycle detected")
)
```

#### 1.8 Test specifications

**format_test.go:**
- `TestMarshalUnmarshalRoundTrip` — state.Task → markdown → state.Task = identical
- `TestMarshalProducesTkCompatibleFormat` — write file, run `tk show <id>`, verify no errors
- `TestUnmarshalTkCreatedTicket` — run `tk create`, read with Go, verify all fields
- `TestStatusMapping` — all 9 attest statuses map correctly to 3 tk statuses and back
- `TestPreserveUnknownYAMLFields` — write ticket with extra fields, read back, fields preserved
- `TestEmptyOptionalFields` — omitted fields don't produce empty YAML entries
- `TestNotesAppend` — notes accumulate under ## Notes section correctly
- `TestMalformedYAML` — returns ErrCorruptYAML, does not panic

**store_test.go:**
- `TestCreateAndRead` — write task, read it back, identical
- `TestPartialIDMatch` — create "abc-1234", resolve with "1234", "abc-1", "abc-1234"
- `TestAmbiguousID` — create "abc-1234" and "abc-1235", resolve "abc-123" → error
- `TestNotFoundID` — resolve "nonexistent" → ErrTicketNotFound
- `TestUpdateStatus` — update status, verify both attest_status and status fields change
- `TestConcurrentWrites` — 10 goroutines updating different tickets simultaneously
- `TestConcurrentWriteSameTicket` — 10 goroutines updating same ticket (flock test)
- `TestWriteTasksCreatesDirectory` — WriteTasks creates .tickets/ if missing
- `TestAtomicWriteSurvivesCrash` — kill write mid-operation, verify no corruption

**deps_test.go:**
- `TestReadyNoDeps` — ticket with no deps is ready
- `TestReadyAllDepsClosed` — ticket with all deps closed is ready
- `TestBlockedOpenDep` — ticket with open dep is blocked
- `TestBlockedMissingDep` — ticket referencing non-existent dep is blocked (not ready)
- `TestCycleDetectionSimple` — A→B→A detected
- `TestCycleDetectionComplex` — A→B→C→A detected
- `TestCycleIgnoresClosed` — A→B(closed)→C→A: no cycle (B is closed)
- `TestSelfLoop` — A→A detected as cycle
- `TestNoCycles` — linear chain: no cycles
- `TestCycleNormalization` — cycle [C,A,B] normalized to [A,B,C]

---

### Stage 2: TaskStore interface + engine wiring

Define backend-agnostic interface. Wire into engine with fallback to JSON. **This is the abstraction layer that protects against Ticket lock-in.**

#### 2.1 Modified files

**`internal/state/types.go`** — Add TaskStore interface:
```go
// TaskStore abstracts task persistence. Implementations include
// RunDir (JSON, current) and ticket.Store (Ticket format, new).
type TaskStore interface {
    ReadTasks() ([]Task, error)
    WriteTasks(tasks []Task) error
    ReadTask(taskID string) (*Task, error)
    WriteTask(task *Task) error
    UpdateStatus(taskID string, status TaskStatus, reason string) error
}
```

**`internal/engine/engine.go`** — Add TaskStore field:
```go
type Engine struct {
    RunDir    *state.RunDir
    WorkDir   string
    TaskStore state.TaskStore // nil = fall back to RunDir
}

func (e *Engine) taskStore() state.TaskStore {
    if e.TaskStore != nil {
        return e.TaskStore
    }
    return e.RunDir  // RunDir already has ReadTasks/WriteTasks
}
```

Replace call sites (pm-001: corrected counts — 11 engine + 6 CLI = 17 total):

**Engine-internal (11 call sites → `e.taskStore().*`):**
| Current call | Location |
|---|---|
| `e.RunDir.ReadTasks()` | engine.go: lines 265, 329, 349, 378, 428, 477, 617 (7 reads) |
| `e.RunDir.WriteTasks(...)` | engine.go: lines 184, 289, 367, 504 (4 writes) |

**CLI-level (6 call sites → receive TaskStore parameter):**
| Current call | Location |
|---|---|
| `runDir.ReadTasks()` | main.go: line 603 (cmdReport) |
| `runDir.ReadTasks()` | tasks.go: lines 171, 201, 243, 289, 358 (cmdTasks/Ready/Blocked/Next/Progress) |

**`cmd/attest/tasks.go`** — Wire through engine's TaskStore:
- `cmdTasks`, `cmdReady`, `cmdBlocked`, `cmdNext`, `cmdProgress` currently call `runDir.ReadTasks()` directly
- Change to accept engine (or TaskStore) parameter instead of runDir
- All filtering/display code unchanged (same `[]state.Task` input)

#### 2.2 Backward compatibility

`RunDir` needs `ReadTasks(runID)`, `WriteTasks(runID, tasks)`, `ReadTask`, `WriteTask`, `UpdateStatus`, and `CreateRun` to satisfy `TaskStore`. Add these to `rundir.go` as wrappers:
- `ReadTasks(runID)`: ignores runID, reads `tasks.json` as before
- `WriteTasks(runID, tasks)`: ignores runID, writes `tasks.json` as before
- `ReadTask(id)`: reads all, finds by ID, returns pointer
- `WriteTask(task)`: reads all, replaces matching ID, writes all
- `UpdateStatus(id, status, reason)`: reads all, mutates, writes all
- `CreateRun(runID)`: no-op (runs are already directories)

**Note** (pm-008): These RunDir wrappers are NOT concurrent-safe (read-modify-write on single JSON file). Comment on each method: "Not concurrent-safe. Use ticket.Store for concurrent access."

**Note** (pm-005): Existing runs with `tasks.json` continue to work via RunDir fallback. The engine checks: if `ticket.Store` has an epic for the run, use it. Otherwise fall back to `RunDir`. No migration required for old runs.

---

### Stage 3: CLI integration + dual-write

Compiler writes `.tickets/` alongside `tasks.json`.

**Modified files:**
- `cmd/attest/main.go` — In `cmdApprove`, after engine creation:
  ```go
  ticketStore := ticket.NewStore(filepath.Join(wd, ".tickets"))
  eng.TaskStore = ticketStore
  ```
- Engine's `Compile()` already calls `taskStore().WriteTasks()` (from Stage 2), so Ticket files are created automatically
- For dual-write: `Compile()` calls both `e.RunDir.WriteTasks()` AND `e.taskStore().WriteTasks()`

**Validation**: After compile, run `tk ready` and `attest ready <run-id>` — both should show the same tasks.

---

### Stage 4: Per-ticket updates

Replace read-all/mutate-one/write-all with per-file operations.

**Modified functions in `internal/engine/engine.go`:**

| Function | Current pattern | New pattern |
|---|---|---|
| `UpdateTaskStatus()` | ReadTasks → find → mutate → WriteTasks | `taskStore().UpdateStatus(id, status, reason)` |
| `persistVerifiedTask()` | ReadTasks → find/append → WriteTasks | `taskStore().ReadTask(id)` → mutate → `WriteTask()` |
| `RetryTask()` | ReadTasks → find → mutate → WriteTasks | `taskStore().UpdateStatus(id, TaskPending, "")` |

**Benefit**: Individual file operations are naturally concurrent-safe (one writer per file via flock). Multi-agent dispatch becomes possible.

---

### Stage 5: Drop tasks.json

Ticket is sole source of truth. Add `attest migrate-tasks <run-id>` for existing runs.

**Modified files:**
- `internal/state/rundir.go` — Mark `Tasks()`, `ReadTasks()`, `WriteTasks()` as deprecated
- `cmd/attest/main.go` — Always construct and use `ticket.Store`
- New command: `attest migrate-tasks <run-id>` reads `tasks.json` and writes `.tickets/*.md`

---

## Files to Modify (complete)

| File | Stage | Change |
|------|-------|--------|
| `internal/ticket/store.go` | 1 | NEW — Store struct, CRUD, file locking |
| `internal/ticket/format.go` | 1 | NEW — Markdown+YAML marshal/unmarshal, status mapping |
| `internal/ticket/id.go` | 1 | NEW — ID generation, partial matching, collision detection |
| `internal/ticket/deps.go` | 1 | NEW — Dependency resolution, cycle detection, ready/blocked |
| `internal/ticket/format_test.go` | 1 | NEW — 8 tests: round-trip, tk compat, status mapping |
| `internal/ticket/store_test.go` | 1 | NEW — 9 tests: CRUD, partial ID, concurrency |
| `internal/ticket/deps_test.go` | 1 | NEW — 10 tests: ready, blocked, cycles, edge cases |
| `go.mod` | 1 | ADD `gopkg.in/yaml.v3` dependency |
| `internal/state/types.go` | 2 | ADD `TaskStore` interface |
| `internal/state/rundir.go` | 2 | ADD `ReadTask`, `WriteTask`, `UpdateStatus` wrappers |
| `internal/engine/engine.go` | 2,4 | ADD TaskStore field, replace 23 call sites, refactor 3 functions |
| `cmd/attest/tasks.go` | 2 | Wire 5 commands through TaskStore |
| `cmd/attest/main.go` | 3,5 | Construct ticket.Store, set on engine |

## Dependencies

```
go get gopkg.in/yaml.v3       # YAML parsing (first external dependency for this project)
go get github.com/gofrs/flock # cross-platform file locking (pm-002)
```

Note (pm-004): These are the project's first external dependencies. `go.sum` should be committed. No vendoring for now.

## Git Configuration

Add to `.gitignore`:
```
# Ticket lock files
.tickets/*.lock
```

`.tickets/` itself is NOT gitignored — tickets are version-controlled artifacts (pm-003 decision).

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| `tk` changes YAML format | Go parser breaks | Pin to known format, compatibility tests, `Extra` catch-all field (pm-012) |
| Agent runs `tk start` without `attest_status` | Status desync | ReadTask derives from `attest_status` if present, falls back to `status` |
| Concurrent agents write same ticket | Data loss | Per-file flock locking via `github.com/gofrs/flock` (pm-002) |
| Performance at 100+ tickets | Slow ReadTasks | Cache in memory with directory mtime invalidation |
| YAML field ordering changes | Diffs are noisy | Use ordered YAML emission (field order from struct tags) |
| Multi-run task contamination | Wrong tasks in queries | Parent-based scoping: runs are epics, tasks are children (pm-006) |
| Existing runs break after integration | No tasks found | RunDir fallback for runs without epic ticket (pm-005) |
| Body content destroyed on WriteTask | Notes/descriptions lost | WriteTask updates frontmatter only, preserves body (pm-013) |
| ETag mismatch after YAML round-trip | Spurious concurrency failures | ETag always computed via canonical JSON, not YAML (pm-011) |
| `tk dep cycle` confused by `attest_status` | Misleading cycle reports | Document interaction, add tests for edge cases (pm-007) |

## Stage Acceptance Criteria (pm-015)

| Stage | Merge independently? | Done when |
|-------|---------------------|-----------|
| 1 | Yes (with Stage 2) | All 27 tests pass, `tk show` reads Go-written files |
| 2 | Yes (with Stage 1) | All existing engine tests pass unchanged |
| 3 | Yes | `attest approve` produces both formats, `tk ready` matches `attest ready` |
| 4 | Yes | Single-task updates work, flock tests pass |
| 5 | Yes | `just pre-commit` passes with `tasks.json` methods removed |

**Minimum shippable unit**: Stages 1+2 together (interface + implementation). Stage 1 alone is dead code.

**Stage 3 dual-write validation** (pm-016): After compile, automatically compare task counts and IDs between `tasks.json` and `.tickets/`. Log warning on divergence.

## Verification

1. **Stage 1**: Write task via Go → `tk show` reads it. Create via `tk create` → Go reads it. Round-trip: Task → markdown → Task = identical.
2. **Stage 2**: All existing tests pass (engine falls back to JSON via RunDir).
3. **Stage 3**: `attest approve` writes both formats. `tk ready` matches `attest ready`.
4. **Stage 4**: `attest verify` updates single ticket file. `tk show` reflects status.
5. **Stage 5**: `just pre-commit` passes. All task commands work from `.tickets/` only.
6. **End-to-end**: prepare → compile → `tk ready` → agent `tk start` → verify → `tk show` = closed.

---

## Appendix: Pre-Mortem Findings (2026-03-19)

18 findings from inline pre-mortem review. All critical/significant findings resolved in this plan revision.

| ID | Severity | Finding | Resolution |
|---|---|---|---|
| pm-001 | critical | Call site count wrong (11, not 23) | Corrected in Stage 2 tables |
| pm-002 | critical | `syscall.Flock` not portable | Use `github.com/gofrs/flock` |
| pm-003 | critical | `.tickets/` git tracking undecided | Committed to git, `.tickets/*.lock` gitignored |
| pm-006 | critical | Project-global `.tickets/` vs run-scoped tasks | Runs as epics, tasks as children, filter by parent |
| pm-004 | significant | First external dependency needs `go get` | Explicit step added, `go.sum` committed |
| pm-005 | significant | Existing runs break after Stage 3 | RunDir fallback for runs without epic ticket |
| pm-007 | significant | `attest_status` vs `status` in cycle detection | Documented, test cases added |
| pm-011 | significant | ETag round-trip through YAML differs | Always compute via canonical JSON |
| pm-012 | significant | Unknown YAML fields dropped | `Extra map[string]interface{}` catch-all |
| pm-013 | significant | Body destroyed on WriteTask | Frontmatter-only update, preserve body |
| pm-014 | significant | `GenerateID()` dead code for attest | Clarified: only for standalone tk compat |
| pm-008 | significant | RunDir TaskStore wrapper not concurrent-safe | Documented limitation |
| pm-009 | moderate | `omitempty` on `Order` drops zero | Removed `omitempty` from `Order` |
| pm-010 | moderate | Who calls `Init()`? | WriteTasks auto-creates directory |
| pm-015 | moderate | No stage acceptance criteria | Added criteria table |
| pm-016 | moderate | Dual-write has no consistency check | Auto-compare after compile |
| pm-017 | moderate | `parent` vs `parent_task_id` confusion | Unified: tk `parent` = attest parent |
| pm-018 | moderate | Time format undefined | `time.RFC3339`, always UTC |
