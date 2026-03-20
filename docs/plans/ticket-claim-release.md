# Plan: Claim/Release Mechanism for Multi-Agent Task Dispatch

**Status:** Planned
**Branch:** `feat/track-work`
**Date:** 2026-03-19

---

## 1. Problem Statement

attest is designed for multi-agent dispatch: multiple coding agents work on tasks in parallel within a single run. Currently, there is no mechanism to prevent two agents from working on the same task simultaneously. The `tk start` command sets status to `in_progress` but provides no exclusivity — two agents can `tk start` the same task.

**What breaks without claims:**
- Two agents implement the same task → conflicting code changes, wasted compute
- A crashed agent leaves a task stuck in `in_progress` → no one picks it up
- The engine has no way to track which agent is working on what
- `attest ready` includes `in_progress` tasks as dispatchable (wrong for multi-agent)

## 2. Existing Infrastructure (what we already have)

### 2.1 Claim struct (defined, unused)

`internal/state/types.go:148-158`:
```go
type Claim struct {
    TaskID            string    `json:"task_id"`
    WaveID            string    `json:"wave_id"`
    OwnerID           string    `json:"owner_id"`
    Backend           string    `json:"backend"`
    ClaimedAt         time.Time `json:"claimed_at"`
    LeaseExpiresAt    time.Time `json:"lease_expires_at"`
    HeartbeatAt       time.Time `json:"heartbeat_at"`
    ScopeReservations []string  `json:"scope_reservations"`
}
```

**Status:** Struct defined. Zero usage anywhere in engine, CLI, or tests. No `WriteClaim`/`ReadClaim` methods exist.

### 2.2 RunDir claim path (infrastructure only)

`internal/state/rundir.go:89-91`:
```go
func (d *RunDir) ClaimPath(taskID string) string {
    return filepath.Join(d.Root, "claims", taskID+".json")
}
```

The `claims/` directory is created in `RunDir.Init()`. The path method exists but no I/O methods use it.

### 2.3 TaskClaimed status (defined, never set)

`internal/state/types.go:138`:
```go
TaskClaimed TaskStatus = "claimed"
```

The constant exists. The status mapping in `internal/ticket/format.go:57` maps it to `"in_progress"` for tk compatibility. But no code in the engine ever sets a task to `TaskClaimed` — status transitions jump directly from `TaskPending` to other states.

### 2.4 RunStatus claim fields (defined, never populated)

`internal/state/types.go:180,406`:
- `ActiveClaims int` — count of active claims in run status
- `ClaimAgeSeconds int` — in TaskDetail
- `HeartbeatAgeSeconds int` — in TaskDetail

All zero/empty in current code.

### 2.5 ReadyFilter bug

`internal/ticket/deps.go:18-38`: Current `ReadyFilter` checks `tkStatus != tkStatusOpen && tkStatus != tkStatusInProgress` and skips those. This means `in_progress` tasks (including `TaskClaimed`, `TaskImplementing`, `TaskVerifying`, `TaskUnderReview`) are included as "ready." For single-agent serial execution this was harmless. For multi-agent dispatch it's wrong — claimed/implementing tasks must not be re-dispatched.

### 2.6 GetPendingTasks bug

`internal/engine/engine.go:381`: Only checks `tasks[i].Status != state.TaskPending`. Does not include `TaskRepairPending` which should also be dispatchable. This is a pre-existing bug.

## 3. Prior Art Research

### 3.1 beads (`steveyegge/beads`)

**Storage:** Dolt (version-controlled SQL database with cell-level merge). Issues stored in SQL tables, not flat files.

**Claim mechanism:** Atomic `bd update <id> --claim` that sets assignee + status to `in_progress` in one CAS (compare-and-swap) operation. Fails with error if already claimed. The `ClaimIssue` method in the storage layer implements the atomicity.

**Ready filter:** `bd ready` returns only `status: "open"` tasks with no active blockers. Excludes `in_progress`, `blocked`, `deferred`, `hooked`. Uses blocker-aware semantics (dependency analysis, not just status check).

**Issue struct fields related to claiming:**
- `Assignee string` — who is assigned
- `Owner string` — who owns the issue
- `Holder string` — slot-based holding
- `AgentState AgentState` — agent lifecycle state
- `LastActivity *time.Time` — last agent activity timestamp
- Gate fields: `AwaitType`, `AwaitID`, `Timeout`, `Waiters` — for blocking operations

**Stale lock cleanup:** File-based locks cleaned by age: bootstrap locks after 5 minutes, sync locks after 1 hour, startup locks after 30 seconds. All use `flock` which is released on process exit.

**Concurrency model:** All coordination delegated to Dolt's SQL transaction layer. `RunInTransaction()` wraps SQL with exponential backoff retry (up to 30s). The claim itself is a conditional UPDATE: `SET assignee = ?, status = 'in_progress' WHERE id = ? AND (assignee = '' OR assignee IS NULL)`. If `rowsAffected == 0`, return `ErrAlreadyClaimed`. This is a compare-and-swap at the database level — our equivalent is the flock-guarded read-check-write.

**Swarm/wave system:** Computes "ready fronts" (parallelizable waves) via Kahn's algorithm on the dependency DAG. Wave status is always **derived from current issue states, never stored separately** — a "computed, not stored" pattern.

**Testing:** Validates 10-way concurrent creates, 5-way same-issue update races, mixed read/write, and 8-worker high-contention stress tests.

**What beads does NOT have:**
- No lease expiry — claims are permanent until explicitly released. A crashed agent leaves the task stuck forever.
- No heartbeat — no way to detect a dead agent.
- No automatic reclamation — requires manual `bd update <id> --status open` to unstick.

### 3.2 beans (`hmans/beans`)

**Storage:** Flat markdown files with YAML frontmatter in `.beans/` directory. Very similar to our `.tickets/` approach.

**Claim mechanism:** None. Status = `in-progress` is a convention, not enforced. Multiple agents could independently set the same bean to `in-progress`.

**Concurrency strategy:** Git worktree isolation. Each agent gets its own git worktree (stored at `~/.beans/worktrees/<project>/<name>/`). Each worktree has its own copy of `.beans/` files. Changes are "dirty" until the PR merges. Conflicts resolve at merge time, not at runtime.

**In-memory concurrency:** `sync.RWMutex` on `beancore.Core` protects the in-memory bean map. All reads use `RLock`, all writes use full `Lock`.

**Optimistic concurrency:** ETag-based (FNV-1a 64-bit content hash). Mutations accept an `ifMatch` parameter. Returns `ETagMismatchError` on conflict. Detect-and-retry, not lock-and-hold.

**File writes:** Plain `os.WriteFile` — no atomic write pattern. Crash mid-write can corrupt a file.

**Signaling:** `fsnotify` file watcher with 100ms debounce. Channel-based pub/sub (buffered channels, cap 16). Slow subscribers have events dropped (non-blocking send). Used for real-time UI updates via GraphQL subscriptions.

**Known bug:** TOCTOU in mutations — read → validate → write without holding a lock across the whole operation. They've acknowledged it but not fixed it (tracked as an open bean).

**What beans does NOT have:**
- No task claiming (no assignee field, no lock, no lease)
- No atomic file writes
- No lease/heartbeat/expiry
- No partitioned work assignment (agents self-select from `--ready`)

### 3.3 Lessons adopted

| Source | Pattern | How we adopt it |
|--------|---------|-----------------|
| beads | Atomic claim = assignee + status in one operation | `ClaimTask` sets claim fields + `TaskClaimed` status under flock |
| beads | Ready filter excludes all non-open tasks | `isDispatchable` only allows `TaskPending` and `TaskRepairPending` |
| beads | Stale lock age-based cleanup | `DefaultLeaseDuration = 15 * time.Minute`, `ReclaimExpired` sweeps |
| beads | Conditional-write CAS claim (assignee + status atomically) | Our flock-guarded read-check-write is the file-based equivalent of beads' SQL CAS |
| beads | "Computed, not stored" wave status — derive from task states | We already do this in refreshRunStatus; validate this pattern continues |
| beads | Append-only event/audit trail | We already have JSONL events via AppendEvent |
| beans | ETag optimistic concurrency | We already have ETags on tasks; claim uses pessimistic flock (stronger) |
| beans | File watcher for signaling | Deferred — useful for future real-time UI, not needed for claim mechanism |

### 3.4 Improvements over prior art

| Gap in prior art | Our improvement |
|------------------|-----------------|
| beads: no lease expiry, crashed agents leave tasks stuck | Lease with configurable TTL (default 15min). `ReclaimExpired` resets stale claims. |
| beads: claims hidden in SQL, not visible to humans | Claim metadata in YAML frontmatter — visible via `tk show <id>` |
| beans: no file locking at all | Per-file flock via `gofrs/flock` — battle-tested in our AddDep/UpdateStatus |
| beans: no atomic writes | temp + fsync + rename + dir fsync — already implemented in `atomicWrite` |
| beans: known TOCTOU in mutations | All claim mutations wrapped in `withLock` (read + validate + write atomic) |
| Neither: no heartbeat mechanism | `RenewClaim` extends lease + updates heartbeat. Engine can detect dead agents. |

## 4. Design Decisions

### 4.1 Claims in frontmatter vs separate JSON files

**Decision:** Frontmatter fields in `.tickets/*.md` files.

**Alternatives considered:**
- Separate JSON in `.attest/runs/<run-id>/claims/<task-id>.json` (matches existing `ClaimPath`)
- In-memory only (like beans' `sync.RWMutex`)

**Rationale:**
1. Reuses existing `withLock` + `atomicWrite` + `UpdateFrontmatter` pipeline — no new I/O path
2. Claims visible via `tk show <task-id>` — humans and agents can see who holds a task
3. Crash safety from lease expiry checked at read time — no separate expiry daemon needed
4. Single source of truth — task status and claim state in one file, one lock
5. The existing `ClaimPath` and `state.Claim` struct can remain as unused infrastructure (zero cost) or be removed in a future cleanup

### 4.2 Claims NOT in state.Task

**Decision:** Claim fields exist only in `Frontmatter`, not in `state.Task`.

**Rationale:**
- Claims are operational/transient store-level state, not domain task state
- `state.Task` is the compiler's output and verifier's input — claims are irrelevant there
- The engine interacts with claims through `ClaimableStore` methods, not task fields
- `UpdateFrontmatter` already preserves fields not in `state.Task` (Links, Assignee, Extra)

### 4.3 Pessimistic locking (flock) vs optimistic concurrency (ETag)

**Decision:** Pessimistic per-file flock for claim mutations. No ETag checking.

**Rationale:**
- Claims are the one operation where contention is expected (multiple agents racing for the same task)
- Pessimistic locking guarantees exactly-one-winner without retry loops
- ETag-based CAS requires read → check → write-with-etag → retry-on-conflict, which is more complex
- Our existing `withLock` pattern is proven (used by UpdateStatus, AddDep, RemoveDep, AddNote)
- flock is released on process exit (crash safety for the lock itself)

### 4.4 Claimable statuses

**Decision:** Only `TaskPending` and `TaskRepairPending` are claimable.

**Rationale:**
- These are the only "waiting for work" states
- `TaskClaimed`/`TaskImplementing`/`TaskVerifying`/`TaskUnderReview` are already in-progress — re-claiming would be wrong
- `TaskBlocked` requires manual intervention, not claiming
- `TaskDone`/`TaskFailed` are terminal

### 4.5 Same-owner re-claim (idempotent)

**Decision:** If `ClaimedBy == ownerID`, `ClaimTask` succeeds (extends lease) instead of returning `ErrAlreadyClaimed`.

**Rationale:**
- An agent may retry a claim after a transient error (network blip, process restart)
- Idempotent claims prevent retry-storm failures
- Only claims by a different owner are rejected

## 5. Detailed Design

### 5.1 New Frontmatter fields (`internal/ticket/format.go`)

```go
// Claim fields — attest-specific, preserved by tk as pass-through.
ClaimedBy      string `yaml:"claimed_by,omitempty"`
ClaimBackend   string `yaml:"claim_backend,omitempty"`
ClaimExpires   string `yaml:"claim_expires,omitempty"`   // RFC3339 UTC
ClaimHeartbeat string `yaml:"claim_heartbeat,omitempty"` // RFC3339 UTC
```

Added to `Frontmatter` struct after the existing attest-specific fields (after `UpdatedAt`).

**UpdateFrontmatter preservation** — add to the preservation block alongside Links/Assignee/ExternalRef/Extra:
```go
fm.ClaimedBy = existingFM.ClaimedBy
fm.ClaimBackend = existingFM.ClaimBackend
fm.ClaimExpires = existingFM.ClaimExpires
fm.ClaimHeartbeat = existingFM.ClaimHeartbeat
```

This ensures engine operations like `UpdateStatus`, `WriteTask`, and `WriteTasks` do not accidentally clear active claims.

### 5.2 New sentinel errors (`internal/ticket/errors.go`)

```go
ErrAlreadyClaimed = errors.New("task already claimed by another owner")
ErrNotClaimed     = errors.New("task is not claimed")
ErrNotClaimOwner  = errors.New("caller is not the claim owner")
ErrNotClaimable   = errors.New("task status is not claimable")
```

### 5.3 Store claim methods (`internal/ticket/claim.go`)

New file. All methods follow the established `withLock` pattern from `UpdateStatus`/`AddDep`.

```go
const DefaultLeaseDuration = 15 * time.Minute
```

#### Shared helpers

```go
// marshalFrontmatterAndBody marshals a Frontmatter struct and appends the body.
// Used by claim methods that operate on *Frontmatter directly (not via state.Task).
func marshalFrontmatterAndBody(fm *Frontmatter, body string) ([]byte, error) {
    yamlData, err := yaml.Marshal(fm)
    if err != nil {
        return nil, fmt.Errorf("marshal frontmatter: %w", err)
    }
    var buf bytes.Buffer
    buf.WriteString("---\n")
    buf.Write(yamlData)
    buf.WriteString("---\n")
    buf.WriteString(body)
    return buf.Bytes(), nil
}

// readAllFrontmatter reads all .md files and returns parsed Frontmatters.
// Unlike readAll() which returns []state.Task (no claim fields), this returns
// raw Frontmatter structs that include claim metadata. Bodies are not returned
// since the sole caller (ReadClaimsForRun) only needs frontmatter.
func (s *Store) readAllFrontmatter() ([]Frontmatter, error)

```

#### ClaimInfo

```go
type ClaimInfo struct {
    TaskID      string
    ClaimedBy   string
    Backend     string
    ExpiresAt   time.Time
    HeartbeatAt time.Time
}
```

#### ClaimTask

```go
func (s *Store) ClaimTask(taskID, ownerID, backend string, lease time.Duration) error
```

**Pseudocode:**
1. `resolvedID, err := ResolveID(s.Dir, taskID)` — resolve partial ID
2. `path := filepath.Join(s.Dir, resolvedID+".md")`
3. `s.withLock(path, func() error { ... })`:
   a. `data := os.ReadFile(path)` — read raw file
   b. `fm, body := splitFrontmatterBody(data)` — parse frontmatter + body
   c. **Same-owner re-claim check (must precede status check — pm-001):**
      If `fm.ClaimedBy == ownerID`, skip status check — this is an idempotent re-claim (extend lease). Jump to step e.
   d. **Active claim by other owner check:** If `fm.ClaimedBy != ""`:
      - Parse `fm.ClaimExpires` as time. If not expired (`time.Now().Before(expires)`), return `ErrAlreadyClaimed`.
      - If expired, proceed (claim is reclaimable).
   e. **Status check:** `status := StatusFromTicket(fm.Status, fm.AttestStatus)`. If not `TaskPending` and not `TaskRepairPending`, return `ErrNotClaimable`. (Skipped for same-owner re-claim — task is already `TaskClaimed`.)
   f. **Set claim fields:**
      - `fm.ClaimedBy = ownerID`
      - `fm.ClaimBackend = backend`
      - `fm.ClaimExpires = formatTime(time.Now().Add(lease))`
      - `fm.ClaimHeartbeat = formatTime(time.Now())`
   g. **Set status:**
      - `fm.AttestStatus = string(state.TaskClaimed)`
      - `fm.Status = StatusToTicket(state.TaskClaimed)` → `"in_progress"`
   h. **Update timestamp:**
      - `fm.UpdatedAt = formatTime(time.Now())`
   i. **Re-marshal frontmatter, preserve body** via `marshalFrontmatterAndBody(fm, body)`:
   j. `atomicWrite(path, output)`

#### ReleaseClaim

```go
func (s *Store) ReleaseClaim(taskID, ownerID string, newStatus state.TaskStatus, reason string) error
```

**Pseudocode:**
1. Resolve ID, build path.
2. `s.withLock(path, func() error { ... })`:
   a. Read + parse frontmatter.
   b. If `fm.ClaimedBy == ""`, return `ErrNotClaimed`.
   c. If `fm.ClaimedBy != ownerID`, return `ErrNotClaimOwner`.
   d. Clear claim fields: `ClaimedBy = ""`, `ClaimBackend = ""`, `ClaimExpires = ""`, `ClaimHeartbeat = ""`.
   e. Set status: `fm.AttestStatus = string(newStatus)`, `fm.Status = StatusToTicket(newStatus)`.
   f. Set `fm.StatusReason` if reason is non-empty.
   g. Set `fm.UpdatedAt`.
   h. Re-marshal + atomicWrite.

#### RenewClaim

```go
func (s *Store) RenewClaim(taskID, ownerID string, lease time.Duration) error
```

**Pseudocode:**
1. Resolve ID, build path.
2. `s.withLock(path, func() error { ... })`:
   a. Read + parse frontmatter.
   b. If `fm.ClaimedBy == ""`, return `ErrNotClaimed`.
   c. If `fm.ClaimedBy != ownerID`, return `ErrNotClaimOwner`.
   d. `fm.ClaimExpires = formatTime(time.Now().Add(lease))`
   e. `fm.ClaimHeartbeat = formatTime(time.Now())`
   f. `fm.UpdatedAt = formatTime(time.Now())`
   g. Re-marshal + atomicWrite.

#### ReadClaimsForRun

```go
func (s *Store) ReadClaimsForRun(runID string) ([]ClaimInfo, error)
```

**Pseudocode:**
1. `frontmatters, _, err := s.readAllFrontmatter()` — returns `[]Frontmatter` with claim fields (pm-002: cannot use `readAll` which returns `[]state.Task` without claim info).
2. Filter: `fm.Parent == runID && fm.ClaimedBy != ""`.
3. Build `ClaimInfo` with parsed times (`parseTime(fm.ClaimExpires)`, etc.).
4. Return list.

#### ReclaimExpired

```go
func (s *Store) ReclaimExpired(runID string) ([]string, error)
```

**Pseudocode:**
1. `claims := ReadClaimsForRun(runID)`
2. `now := time.Now()`
3. For each claim where `claim.ExpiresAt.Before(now)`:
   a. `withLock` on the ticket file.
   b. Re-read frontmatter (may have been renewed since `ReadClaimsForRun`).
   c. Re-check expiry inside lock (double-check pattern — avoids TOCTOU).
   d. If still expired: clear claim fields, set status to `TaskPending`, set reason to `"claim expired"`.
   e. atomicWrite.
4. Return list of reclaimed task IDs.

### 5.4 ReadyFilter refactor (`internal/ticket/deps.go`)

Replace the current tk-status-based check with an explicit dispatchable status check:

```go
// isDispatchable returns true for task statuses that represent unclaimed,
// ready-for-work states.
func isDispatchable(s state.TaskStatus) bool {
    return s == state.TaskPending || s == state.TaskRepairPending
}
```

In `ReadyFilter`, replace:
```go
// Old:
tkStatus := StatusToTicket(t.Status)
if tkStatus != tkStatusOpen && tkStatus != tkStatusInProgress {
    continue
}
if t.Status == state.TaskBlocked {
    continue
}

// New:
if !isDispatchable(t.Status) {
    continue
}
```

`TaskBlocked` is no longer a special case — it's implicitly excluded because it's not in the dispatchable set.

**Impact on BlockedFilter:** No change needed. `BlockedFilter` uses tk-status-based filtering (open/in_progress) which is correct for its purpose — showing tasks that are stuck due to unresolved deps, regardless of claim status.

### 5.5 ClaimableStore interface (`internal/state/types.go`)

```go
// ClaimableStore extends TaskStore with exclusive claim operations for
// multi-agent dispatch. Implementations must provide crash-safe leases.
type ClaimableStore interface {
    TaskStore
    ClaimTask(taskID, ownerID, backend string, lease time.Duration) error
    ReleaseClaim(taskID, ownerID string, newStatus TaskStatus, reason string) error
    RenewClaim(taskID, ownerID string, lease time.Duration) error
}
```

The engine type-asserts `taskStore()` to `ClaimableStore` when claim operations are needed. If the assertion fails (e.g., RunDir fallback), the engine falls back to simple status updates.

### 5.6 Engine claim methods (`internal/engine/engine.go`)

#### ClaimAndDispatch

```go
func (e *Engine) ClaimAndDispatch(taskID, ownerID, backend string) error {
    cs, ok := e.taskStore().(state.ClaimableStore)
    if !ok {
        // Fallback for non-claim-aware stores (RunDir).
        return e.taskStore().UpdateStatus(taskID, state.TaskClaimed, "claimed by "+ownerID)
    }
    if err := cs.ClaimTask(taskID, ownerID, backend, ticket.DefaultLeaseDuration); err != nil {
        return fmt.Errorf("claim task %s: %w", taskID, err)
    }
    _ = e.RunDir.AppendEvent(state.Event{
        Timestamp: time.Now(),
        Type:      "task_claimed",
        RunID:     e.runID(),
        TaskID:    taskID,
        Detail:    fmt.Sprintf("claimed by %s (%s)", ownerID, backend),
    })
    return nil
}
```

**Import note:** The engine references `ticket.DefaultLeaseDuration`. This introduces a dependency from `engine` → `ticket`. However, the architecture constraint says "Nothing in `internal/engine/` imports `internal/ticket/`." To avoid this, define `DefaultLeaseDuration` as a parameter or use a constant in the `state` package instead. Alternative: pass the lease duration as a parameter to `ClaimAndDispatch`.

**Chosen approach:** Pass lease duration as parameter:
```go
func (e *Engine) ClaimAndDispatch(taskID, ownerID, backend string, lease time.Duration) error
```

#### ReleaseTask

```go
func (e *Engine) ReleaseTask(taskID, ownerID string, newStatus state.TaskStatus, reason string) error {
    cs, ok := e.taskStore().(state.ClaimableStore)
    if !ok {
        return e.taskStore().UpdateStatus(taskID, newStatus, reason)
    }
    if err := cs.ReleaseClaim(taskID, ownerID, newStatus, reason); err != nil {
        return fmt.Errorf("release task %s: %w", taskID, err)
    }
    _ = e.RunDir.AppendEvent(state.Event{
        Timestamp: time.Now(),
        Type:      "task_released",
        RunID:     e.runID(),
        TaskID:    taskID,
        Detail:    fmt.Sprintf("released by %s → %s", ownerID, newStatus),
    })
    return nil
}
```

#### SweepExpiredClaims

```go
func (e *Engine) SweepExpiredClaims() ([]string, error) {
    // Requires ticket.Store directly — type-assert to access ReclaimExpired.
    type expiredSweeper interface {
        ReclaimExpired(runID string) ([]string, error)
    }
    sweeper, ok := e.taskStore().(expiredSweeper)
    if !ok {
        return nil, nil // no-op for stores without claim support
    }
    reclaimed, err := sweeper.ReclaimExpired(e.runID())
    if err != nil {
        return nil, fmt.Errorf("sweep expired claims: %w", err)
    }
    for _, taskID := range reclaimed {
        _ = e.RunDir.AppendEvent(state.Event{
            Timestamp: time.Now(),
            Type:      "claim_expired",
            RunID:     e.runID(),
            TaskID:    taskID,
            Detail:    "claim expired, task reset to pending",
        })
    }
    return reclaimed, nil
}
```

#### GetPendingTasks fix

Update the status check from:
```go
if tasks[i].Status != state.TaskPending {
    continue
}
```
To:
```go
if tasks[i].Status != state.TaskPending && tasks[i].Status != state.TaskRepairPending {
    continue
}
```

## 6. Files to Modify

| File | Change | Lines est. |
|------|--------|-----------|
| `internal/ticket/format.go` | Add 4 claim fields to Frontmatter, preserve in UpdateFrontmatter | +12 |
| `internal/ticket/errors.go` | Add 4 claim error sentinels | +4 |
| `internal/ticket/claim.go` | **NEW** — ClaimInfo, 5 Store methods, DefaultLeaseDuration, marshalFrontmatter helper | ~180 |
| `internal/ticket/claim_test.go` | **NEW** — 15 tests | ~300 |
| `internal/ticket/deps.go` | Refactor ReadyFilter to use isDispatchable | +8, -6 |
| `internal/ticket/deps_test.go` | Add 3 tests for status exclusion | +40 |
| `internal/state/types.go` | Add ClaimableStore interface | +8 |
| `internal/engine/engine.go` | Add ClaimAndDispatch, ReleaseTask, SweepExpiredClaims; fix GetPendingTasks | +60 |
| `internal/engine/engine_test.go` | Add 2 claim-related engine tests | +60 |

**Estimated total:** ~670 new/modified lines.

## 7. Test Specifications

### 7.1 claim_test.go (new file)

| # | Test | Description |
|---|------|-------------|
| 1 | `TestClaimTaskSuccess` | Pending task → claim → status is `claimed`, frontmatter has claim fields |
| 2 | `TestClaimTaskAlreadyClaimed` | Claim by owner A → claim by owner B → `ErrAlreadyClaimed` |
| 3 | `TestClaimTaskSameOwnerReclaim` | Claim by A → re-claim by A → success (idempotent, lease extended) |
| 4 | `TestClaimExpiredTaskReclaimable` | Claim by A with short lease → wait → claim by B → success |
| 5 | `TestClaimNonPendingTask` | Claim a `TaskDone` task → `ErrNotClaimable` |
| 6 | `TestClaimRepairPendingTask` | Claim a `TaskRepairPending` task → success |
| 7 | `TestReleaseClaimSuccess` | Claim → release with matching owner → claim fields cleared, status set |
| 8 | `TestReleaseClaimWrongOwner` | Claim by A → release by B → `ErrNotClaimOwner` |
| 9 | `TestReleaseUnclaimedTask` | Release unclaimed → `ErrNotClaimed` |
| 10 | `TestRenewClaimSuccess` | Claim → renew → expiry extended, heartbeat updated |
| 11 | `TestRenewClaimWrongOwner` | Claim by A → renew by B → `ErrNotClaimOwner` |
| 12 | `TestReadClaimsForRun` | Create tasks, claim some → `ReadClaimsForRun` returns correct set |
| 13 | `TestReclaimExpired` | Claim with short lease → `ReclaimExpired` → task back to pending |
| 14 | `TestClaimPreservedThroughUpdateStatus` | Claim → `UpdateStatus` → claim fields still present |
| 15 | `TestConcurrentClaimSameTask` | 10 goroutines claim same task → exactly 1 succeeds, rest get `ErrAlreadyClaimed` |

### 7.2 deps_test.go (additions)

| # | Test | Description |
|---|------|-------------|
| 16 | `TestClaimedTaskNotReady` | `TaskClaimed` task with no deps → `ReadyFilter` excludes it |
| 17 | `TestImplementingTaskNotReady` | `TaskImplementing` task → `ReadyFilter` excludes it |
| 18 | `TestRepairPendingTaskReady` | `TaskRepairPending` with deps met → `ReadyFilter` includes it |

### 7.3 engine_test.go (additions)

| # | Test | Description |
|---|------|-------------|
| 19 | `TestClaimAndDispatch` | Wire ticket store, call ClaimAndDispatch → task is claimed |
| 20 | `TestClaimAndDispatchFallback` | Use RunDir (no ClaimableStore) → falls back to UpdateStatus |

## 8. Implementation Order

1. **Foundation** (format.go, errors.go): Add Frontmatter fields, preservation in UpdateFrontmatter, sentinel errors. No behavioral change — all existing tests pass.

2. **Claim methods** (claim.go): Implement all 5 Store methods. Requires a helper to marshal frontmatter + body without going through `state.Task` (since claim fields aren't in Task).

3. **ReadyFilter** (deps.go): Refactor to use `isDispatchable`. Update/add deps_test.go tests.

4. **Interface + Engine** (types.go, engine.go): Add ClaimableStore interface, engine claim methods, fix GetPendingTasks.

5. **Tests** (claim_test.go, engine_test.go): Full test suite.

## 9. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Clock skew across processes | Claim expiry checked against local `time.Now()` — processes with different clocks may disagree on expiry | Single-node constraint (stated scope). Future multi-node would need NTP or lease-relative timing. |
| Lease too short for long tasks | Agent's claim expires while still working → another agent reclaims and creates conflicting changes | Default 15min with heartbeat renewal. Agents must call `RenewClaim` periodically. Document this contract. |
| Lease too long for crashed agents | Crashed agent's task sits idle for 15min before reclamation | Acceptable for initial version. Can be tuned per-run or per-task later. |
| ReadyFilter change breaks existing behavior | Tasks that were previously shown as "ready" (in_progress) are now excluded | This is the correct fix. Existing single-agent workflow is unaffected because tasks go pending → done without claiming. |
| `marshalFrontmatter` helper duplicates logic | Claim methods need to marshal frontmatter without going through TaskToFrontmatter | Extract a shared `marshalFrontmatterAndBody(fm *Frontmatter, body string) []byte` helper. |
| Engine → ticket import constraint | `ClaimAndDispatch` needs `DefaultLeaseDuration` from ticket package | Pass lease as parameter; define constant in `ticket` package but reference only from `cmd/attest/` where the import is allowed. |

## 10. CLI Surface (Deferred)

No CLI commands for claiming are included in this plan (pm-004). Agents interact with claims through the engine's Go API (`ClaimAndDispatch`, `ReleaseTask`), not through CLI commands. The engine's future dispatch loop will call these methods automatically when assigning work to agents.

A future `attest claim <run-id> <task-id> --owner <agent-id>` command may be added when manual claim management is needed, but it is not required for the engine-mediated dispatch flow.

## 11. Scope Reservations (Deferred)

The `state.Claim` struct includes `ScopeReservations []string` — file-path-level conflict detection to prevent two agents from modifying the same files. This is important for full multi-agent dispatch but is out of scope for this plan. Reasons:

1. Claim exclusivity (one agent per task) is the minimum viable mechanism.
2. Scope reservations require cross-task conflict detection (checking all active claims' owned paths for overlap).
3. The current task scoping model (`OwnedPaths`, `ReadOnlyPaths`, `SharedPaths`) is enforced by the verifier, not at dispatch time.
4. Adding scope reservations is an incremental enhancement that can be built on top of the claim mechanism.

## 12. Verification

1. `go build ./...` — compiles
2. `go test -race ./internal/ticket/... ./internal/engine/... ./cmd/attest/...` — all tests pass including race detector
3. `golangci-lint run` — no lint issues
4. Manual: create a pending task via `tk create`, claim it via Go test/CLI, verify `tk show` displays claim fields
5. Manual: claim a task with 1-second lease, wait, call `ReclaimExpired`, verify task resets to pending
6. Manual: two concurrent claim attempts on same task — verify exactly one succeeds

---

## Appendix: Pre-Mortem Findings (2026-03-19)

4 findings from inline pre-mortem review. All resolved in this plan revision.

| ID | Severity | Finding | Resolution |
|----|----------|---------|------------|
| pm-001 | significant | Same-owner re-claim blocked by status check — `TaskClaimed` fails `isClaimable` before reaching same-owner check | Reordered pseudocode: check same-owner BEFORE status check. Same-owner re-claim skips status validation. |
| pm-002 | significant | `ReadClaimsForRun` cannot use `readAll()` — returns `[]state.Task` without claim fields | Added `readAllFrontmatter()` helper that returns `[]Frontmatter` with claim metadata. |
| pm-003 | moderate | `marshalFrontmatterAndBody` helper mentioned in risks but not specified | Added explicit function signature and doc to section 5.3. |
| pm-004 | moderate | No CLI surface for claiming — agents have no way to invoke claims from command line | Documented as Go-API-only (section 10). Engine dispatch loop will call claim methods. CLI command deferred. |
