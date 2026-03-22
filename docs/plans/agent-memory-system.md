# Plan: Agent Memory System for attest

**Status:** Simplification in progress
**Branch:** `feat/agent-memory`
**Date:** 2026-03-22 (revised from 2026-03-20)

---

## 1. Problem Statement

After implementing the ticket-based task store with claims, we identified three gaps in agent effectiveness:

1. **Learnings lost between sessions.** What an agent learns implementing Wave 3 (e.g., "use withLock for atomic read-check-write") isn't available when implementing Wave 4. Each session re-discovers the same patterns.

2. **New tasks don't benefit from past experience.** The compiler produces tasks with requirements, scopes, and dependencies — but no guidance on what approaches worked or failed for similar tasks in previous runs.

3. **Session continuity is expensive.** When a session ends mid-task, the next session burns context reconstructing where we left off by re-reading code, commits, and ticket states.

## 2. Prior Art

Six open-source memory frameworks were studied:

| System | Key Pattern | Relevance to attest |
|--------|-------------|---------------------|
| **memU** | Memory as human-readable markdown files, file-system-inspired hierarchy | High — matches our .tickets/ approach |
| **Zep/Graphiti** | Temporal knowledge graph with bi-temporal fact tracking | Partial — too heavy (requires graph DB) |
| **mem0** | Universal memory layer: user/session/agent tiers, vector semantic search | Low — Python-centric, requires vector DB + LLM |
| **Cognee** | Knowledge graph + vector search, local-first | Partial — local-first aligns but scope too broad |
| **Letta** | OS-inspired three-tier memory, agents self-edit memory blocks | Partial — self-editing requires LLM compute |
| **Memobase** | User-centric profile + event timeline, sub-100ms SQL queries | High — profile + timeline maps to our project learnings + task history |

### What we adopt

- **memU:** Memory as readable files (already our pattern)
- **Memobase:** Profile + event timeline, SQL-fast retrieval

### What we explicitly skip

- Vector databases, graph databases, embedding pipelines
- LLM-assisted memory extraction (our extraction is deterministic or human-initiated)
- Semantic search (tag-based retrieval is sufficient for a single-project tool)
- External services or APIs
- Citation counting and maturity progression (see §11 rationale)

## 3. Design

### 3.1 Learning Store

#### Storage layout

```
.attest/learnings/
    index.jsonl              # Learning entries, atomic-rewrite on mutation (primary store)
    tags.json                # Inverted tag index (rebuilt from index.jsonl)
    handoffs/
        <timestamp>.json     # Session handoff artifacts
    latest-handoff.json      # Copy of most recent handoff
    prevention/
        review/              # Injected during council tech-spec review
            <learning-id>.md
        planning/            # Injected during execution plan review
            <learning-id>.md
```

**Why JSONL:** A single `index.jsonl` gives atomic-rewrite via temp-file + rename (crash-safe), sub-millisecond scans for hundreds of entries, and a single file to back up.

#### Learning type

```go
package learning

type Category string

const (
    CategoryPattern     Category = "pattern"       // Reusable approach that worked
    CategoryAntiPattern Category = "anti_pattern"   // Approach that failed
    CategoryTooling     Category = "tooling"        // Build/lint/test insight
    CategoryCodebase    Category = "codebase"       // Project-specific knowledge
    CategoryProcess     Category = "process"        // Workflow/dispatch insight
)

type Learning struct {
    ID           string    `json:"id"`             // "lrn-" + 8-char hash
    CreatedAt    time.Time `json:"created_at"`
    Tags         []string  `json:"tags"`           // lowercase, normalized
    Category     Category  `json:"category"`
    Content      string    `json:"content"`        // the actual learning
    Summary      string    `json:"summary"`        // one-line for display
    SourceTask   string   `json:"source_task,omitempty"`
    SourceRun    string   `json:"source_run,omitempty"`
    SourcePaths  []string `json:"source_paths,omitempty"` // OwnedPaths at creation time
    Source       string   `json:"source,omitempty"`       // "manual", "council", "verifier", "rejection"
    Confidence   float64  `json:"confidence"`             // 0-1, set at creation, not mutated
    SupersededBy string   `json:"superseded_by,omitempty"`
    Expired      bool     `json:"expired"`                // soft-delete
    AttachCount  int      `json:"attach_count"`           // times attached to a task
    SuccessCount int      `json:"success_count"`          // times the attached task passed verification
    LastAttachedAt *time.Time `json:"last_attached_at,omitempty"`
}
```

**Removed fields (from prior design):** `Utility`, `CitedCount`, `LastCitedAt`, `Maturity`. See §11 for rationale.

**Effectiveness score** (computed, not stored):
```go
effectiveness = SuccessCount / AttachCount  (falls back to Confidence when AttachCount == 0)
```

This is the primary quality signal: a learning attached to tasks that pass verification is more valuable than one attached to tasks that fail.

**Source-based initial confidence:**

| Source | Confidence | Rationale |
|---|---|---|
| Verifier failure | 0.9 | Deterministic, mechanically verified |
| Council finding (accepted by judge) | 0.8 | Multi-model consensus + judge validation |
| Council finding (not judged) | 0.6 | Single reviewer, no validation |
| Judge rejection | 0.7 | Judge quality is high but not infallible |
| Manual (`attest learn`) | 0.5 | Default, unvalidated |

Confidence is set at creation and not mutated afterward. It serves as the initial sort tiebreaker before any outcome data accumulates.

**SourcePaths population:** When a learning is created with a `SourceTask`, `SourcePaths` is copied from that task's `Scope.OwnedPaths`. When created manually via CLI, `SourcePaths` may be specified with `--path` flags. All paths MUST be repo-root-relative, normalized with `filepath.Clean`, converted to forward-slash separators. CLI absolute paths are converted to repo-relative before storage.

#### Session handoff type

```go
type SessionHandoff struct {
    ID            string    `json:"id"`
    CreatedAt     time.Time `json:"created_at"`
    RunID         string    `json:"run_id,omitempty"`
    TaskID        string    `json:"task_id,omitempty"`
    Summary       string    `json:"summary"`
    NextSteps     []string  `json:"next_steps,omitempty"`
    OpenQuestions []string  `json:"open_questions,omitempty"`
    LearningIDs   []string  `json:"learning_ids,omitempty"`
}
```

#### Query options

```go
type QueryOpts struct {
    Tags             []string // match any tag (union with Paths and SearchText)
    Category         Category // exact category match
    Paths            []string // match learnings with overlapping SourcePaths (union with Tags)
    SearchText       string   // full-text search across Content, Summary, Tags (union with Tags/Paths)
    MinEffectiveness float64  // effectiveness threshold (0.0 = no filter; pipeline uses 0.3)
    Limit            int      // max results (0 = unlimited)
    SortBy           string   // "effectiveness" (default), "created_at"
}
```

Tag, path, and search text matching uses union semantics: a learning matches if it matches any tag OR any path OR the search text.

#### Store interface

```go
type Store struct {
    SharedDir  string           // committed path: .attest/learnings/ (redacted content)
    LocalDir   string           // local-only path: .git/attest/learnings/ (unredacted, optional)
    Scanner    *ContentScanner  // content scanner (nil = no scanning)
    Now        func() time.Time // clock injection for tests; defaults to time.Now
}

func NewStore(dir string) *Store                          // backward-compatible single-dir
func NewStoreWithLocalDir(sharedDir, localDir string) *Store // split mode with scanning

// Write operations
func (s *Store) Add(l *Learning) error
func (s *Store) RecordOutcome(ids []string, passed bool) error

// Query operations
func (s *Store) Query(opts QueryOpts) ([]Learning, error)
func (s *Store) Get(id string) (*Learning, error)

// Index operations
func (s *Store) RebuildIndex() error

// Session handoff
func (s *Store) WriteHandoff(h *SessionHandoff) error
func (s *Store) LatestHandoff() (*SessionHandoff, error)

// Maintenance
func (s *Store) Maintain(maxAge time.Duration) (*MaintainReport, error)
func (s *Store) GarbageCollect(maxAge time.Duration) (int, error)
func (s *Store) Repair() (kept, dropped int, err error)
```

#### Engine integration interface (`internal/state/types.go`)

The engine must not import `internal/learning` (same constraint as engine→ticket). Define a `LearningEnricher` interface in the state package:

```go
type LearningQueryOpts struct {
    Tags             []string
    Paths            []string
    SearchText       string
    MinEffectiveness float64
    Limit            int
}

type LearningEnricher interface {
    QueryLearnings(opts LearningQueryOpts) ([]LearningRef, error)
    RecordOutcome(ids []string, passed bool) error
}
```

The engine holds `LearningEnricher` (an interface), not `*learning.Store` (a concrete type). Same pattern as `ClaimableStore`.

#### Concurrency

All mutating Store operations (`Add`, `RecordOutcome`, `GarbageCollect`, `Maintain`) acquire an exclusive flock on `.attest/learnings/.lock`. Both `index.jsonl` and `tags.json` are written atomically while the lock is held. Same pattern as `internal/ticket/claim.go`.

#### Corruption Handling

If any non-empty `index.jsonl` line fails to decode, mutating operations MUST return `ErrCorruptLearningStore` and MUST NOT rewrite files. Read-only operations may surface partial results with a warning. Repair requires `attest learn repair`.

#### Effectiveness scoring

Effectiveness replaces the prior utility/decay/citation system. It is computed at query time:

```go
func (l *Learning) Effectiveness() float64 {
    if l.AttachCount == 0 {
        return l.Confidence // use initial confidence as proxy
    }
    return float64(l.SuccessCount) / float64(l.AttachCount)
}
```

Query results are sorted by effectiveness descending, then `created_at` descending, then ID ascending. The `MinEffectiveness` filter in `QueryOpts` serves as the quality gate (default 0.3).

#### Auto-expiry

During `Maintain`, learnings whose tags/paths haven't matched any query in 90 days (tracked via `LastAttachedAt`) are marked `Expired`. No background goroutine — maintenance runs only on explicit triggers (see §11.3).

#### Tag inverted index (`tags.json`)

```json
{
  "concurrency": ["lrn-a1b2c3d4", "lrn-e5f6g7h8"],
  "compiler": ["lrn-i9j0k1l2"],
  "flock": ["lrn-a1b2c3d4"]
}
```

Rebuilt from `index.jsonl` on every `Add()`. Keywords extracted from content (top 10 significant words) are also indexed.

### 3.2 Intent-First Ticket Fields

#### New Frontmatter fields (`internal/ticket/format.go`)

```go
Intent      string   `yaml:"intent,omitempty"`
Constraints []string `yaml:"constraints,omitempty"`
Warnings    []string `yaml:"warnings,omitempty"`
LearningIDs []string `yaml:"learning_ids,omitempty"`
```

#### New Task fields (`internal/state/types.go`)

```go
type LearningRef struct {
    ID       string  `json:"id"`
    Category string  `json:"category"`
    Summary  string  `json:"summary"`
    Maturity string  `json:"maturity,omitempty"` // kept for display compatibility
}

Intent          string        `json:"intent,omitempty"`
Constraints     []string      `json:"constraints,omitempty"`
Warnings        []string      `json:"warnings,omitempty"`
LearningIDs     []string      `json:"learning_ids,omitempty"`
LearningContext []LearningRef `json:"learning_context,omitempty"`
```

#### Compiler stays deterministic

The compiler remains pure — no learning store access. The engine enriches tasks *after* compilation:

```go
if e.LearningEnricher != nil {
    for i := range result.Tasks {
        e.enrichTaskWithLearnings(&result.Tasks[i])
    }
}
```

`enrichTaskWithLearnings` derives query tags from the task and queries by those tags + owned paths, attaching the top 5 learnings sorted by effectiveness descending. Anti-pattern summaries → `Warnings`. Codebase/tooling summaries → `Constraints`.

### 3.3 Session Handoff

#### Creation

```bash
attest learn handoff \
  --run run-1234 --task task-at-fr-001 \
  --summary "Implemented grouping logic, tests passing" \
  --next "Wire enrichment into Compile"
```

Writes to `.attest/learnings/handoffs/<timestamp>.json` and copies to `latest-handoff.json`.

#### Consumption

- `attest next <run-id>` and `attest status` display handoff only if < 24h old and matching run ID
- `attest context` includes handoff in the context bundle

### 3.4 Agent Context Assembly

```go
type ContextBundle struct {
    TaskID      string          `json:"task_id"`
    Learnings   []Learning      `json:"learnings"`
    Handoff     *SessionHandoff `json:"handoff,omitempty"`
    TokensUsed  int             `json:"tokens_used"`
    TokenBudget int             `json:"token_budget"`
}

func (s *Store) AssembleContext(taskID string, tags, paths []string, searchText string) (*ContextBundle, error)
```

Tag, path, and search text matches are unioned by learning ID, sorted by effectiveness, capped at 8 learnings and 2000 tokens. Read-only — does not modify the store. Outcome tracking happens later via `RecordOutcome` in `VerifyTask`.

### 3.5 CLI Commands

```
attest learn "content" --tag X --category pattern     # Add manually
attest learn query --tag X --category Y --limit 5     # Query
attest learn handoff --summary "..." --next "..."     # Session handoff
attest learn list                                     # List all active
attest learn gc                                       # Garbage collect
attest learn maintain                                 # Full maintenance
attest learn repair                                   # Fix corrupt store

attest context <run-id> <task-id>                     # Assemble + display
```

## 4. Design Decisions

### JSONL over individual files

JSONL gives: atomic-rewrite via temp-file + rename (crash-safe), sub-millisecond full scan for hundreds of entries, single file to back up, and trivial `grep` for debugging.

### Engine enrichment, not compiler enrichment

The compiler is deterministic and pure. Learning injection is a runtime concern — the engine enriches tasks *after* compilation. This preserves the architecture boundary.

### Conservative token budget (2000 tokens)

Better to inject 5 high-effectiveness learnings than 50 mediocre ones.

### No new dependencies

Uses stdlib `encoding/json` for JSONL, `os` for file I/O, existing `flock` for concurrent safety.

### Effectiveness over citation counting

Empirical analysis of 294 learnings across 5 projects showed citation count correlates with noise, not quality. The most-cited learnings (40-68 citations) were debug transcripts, not actionable knowledge. Verification outcome (did the task that used the learning pass?) is a stronger quality signal.

### No background maintenance

`MaintainIfStale` background goroutines caused test cleanup issues and added complexity for marginal benefit. Maintenance runs on two triggers: after run completion (engine call) and manual (`attest learn maintain`).

## 5. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Learning quality is low | Agents receive unhelpful context | Effectiveness scoring surfaces learnings that correlate with task success. Low-effectiveness learnings fall below query thresholds. |
| JSONL file grows unbounded | Slow queries | GC removes expired entries >90 days old. At 1KB/learning, 1K learnings = 1MB. |
| Tag taxonomy drift | Queries miss relevant learnings | Normalize tags (lowercase, strip whitespace). Keyword extraction catches partial matches. |
| Effectiveness cold-start | New learnings have no outcome data | Use `Confidence` as proxy until `AttachCount > 0`. Source-based confidence gives reasonable initial ranking. |
| Auto-expiry too aggressive | Useful learnings expired | 90-day inactivity threshold is conservative. Expired learnings are soft-deleted and can be restored. |

---

## 9. Phase 2: Learning Extraction & Injection

**Status:** Implemented
**Date:** 2026-03-21

### 9.1 The Closed Loop

```
Council findings ──→ learning store ──→ task enrichment (compile time)
Verifier failures ──→ learning store ──→ council reviewer context
Judge rejections ──→ learning store ──→ worker dispatch context
```

### 9.2 Extraction Points

#### 9.2.1 Council Review Findings → Learnings

**Trigger:** After council review completes. Rejected findings are skipped (handled by §9.2.2). Skipped during dry-run.

**Mapping:** Blocking findings → `anti_pattern`, non-blocking → `codebase`. Dedup by `finding:<id>` tag. Cap at 5 per extraction. Errors logged to stderr.

#### 9.2.2 Judge Rejections → Pattern Learnings

**Trigger:** After judge consolidation. Only extracts rejections with non-empty `DismissalRationale`.

**Mapping:** Category `pattern` (valid approach incorrectly questioned). Dedup by `rejected:<id>` tag.

#### 9.2.3 Verifier Failures → Anti-Pattern Learnings

**Trigger:** After `VerifyTask` returns `Pass == false`.

**Mapping:** Category derived from verifier finding category. Dedup by `verifier:<dedupe_key>` tag. `SourceTask` and `SourcePaths` copied from verified task.

### 9.3 Injection Points

#### 9.3.1 Task Enrichment at Compile Time (Implemented)

`enrichTaskWithLearnings` queries by task tags + owned paths, attaches top 5 by effectiveness. Anti-pattern summaries → `Warnings`. Codebase/tooling summaries → `Constraints`.

#### 9.3.2 Council Reviewer Context

Before running council reviews, query learnings matching the scope under review. Include as "Prior findings" in the reviewer prompt.

#### 9.3.3 Worker Dispatch Context (Partially Implemented)

`AssembleContext` produces a `ContextBundle`. The `attest context` command surfaces it. Full worker prompt integration deferred to dispatch phase.

### 9.4 Interface Boundaries

```
cmd/attest/     → imports learning, state, engine, ticket, councilflow
internal/engine → imports state, compiler, verifier (NOT learning, NOT councilflow)
internal/learning → imports state (for LearningRef, LearningQueryOpts)
```

Extraction lives in `cmd/attest/extract.go`, not in engine or councilflow.

---

## 10. Data Privacy: Split Stores, Content Scanning, and Optional Encryption

**Status:** Implemented (Layer 1 + Layer 2; Layer 3 encryption deferred)
**Date:** 2026-03-22

### 10.1 Problem

Learnings may contain secrets, local paths, internal hostnames, or business-sensitive details. Committing all learnings to git exposes this.

### 10.2 Three-Layer Protection

#### Layer 1: Split Stores (Local + Shared)

- **Local store** (`$(git rev-parse --git-common-dir)/attest/learnings/`): All learnings, unredacted. Shared across worktrees, never committed.
- **Shared store** (`.attest/learnings/`): Only learnings that pass content scan. Optionally encrypted.

#### Layer 2: Automated Content Scanning

Regex-based scan on `Add()` for secrets, tokens, local paths, IP addresses. Matched patterns redacted with `[REDACTED]`. Configurable via `.attest/scan-terms.txt`.

#### Layer 3: Optional Age Encryption

Passphrase-based (`ATTEST_LEARNING_PASSPHRASE`). Only `Content` field encrypted — metadata stays plaintext for query/display. Graceful degradation without passphrase.

---

## 11. Phase 3: Outcome-Based Learning Quality

**Status:** Implemented
**Date:** 2026-03-22

### 11.1 Rationale for Simplification

Empirical analysis of 294 learnings across 5 real projects revealed:

| Finding | Data | Implication |
|---------|------|-------------|
| Citation count tracks noise | Most-cited learnings (40-68 citations) were debug transcripts | Citation count is not a quality signal |
| Utility range compressed | All values between 0.47–0.79 | MinUtility threshold of 0.3 never filtered anything |
| Maturity nearly useless | 208/243 stayed provisional; 32 "established" were noisy entries | Promotion rules don't correlate with quality |
| Categories unused | 218/247 were "knowledge" | Category system needs enforcement, not more tiers |
| Cross-project contamination | 45 duplicates across 5 projects | Project-scoping matters more than scoring |

The prior design adopted citation tracking, maturity progression, utility decay, and maturity-weighted scoring. All of these produced machinery that didn't correlate with learning quality.

### 11.2 Outcome Tracking

The one quality signal we have: **did the task that used this learning pass verification?**

```go
// In Engine.VerifyTask, after determining pass/fail:
if e.LearningEnricher != nil && len(task.LearningIDs) > 0 {
    _ = e.LearningEnricher.RecordOutcome(task.LearningIDs, result.Pass)
}
```

`RecordOutcome` increments `AttachCount` for all IDs, and `SuccessCount` for IDs where `passed == true`. Single store write (batch operation).

Effectiveness = `SuccessCount / AttachCount`. A learning attached to 10 tasks where 9 passed has effectiveness 0.9. One where 2/5 passed has 0.4.

### 11.3 Maintenance

`Store.Maintain()` runs the following tasks:

```
attest learn maintain
  ├─ 1. Dedup: merge learnings with ≥2 shared tags and similar summaries
  ├─ 2. Contradiction scan: flag pattern vs anti_pattern with shared tags
  ├─ 3. Auto-expiry: mark learnings with no query match in 90 days as expired
  ├─ 4. Prevention refresh: compile high-effectiveness learnings to prevention checks
  ├─ 5. Index rebuild: regenerate tags.json
  └─ 6. GC: remove expired/superseded learnings older than 90 days
```

#### When it runs

| Trigger | How | Blocking? |
|---------|-----|-----------|
| **After run completion** | Engine calls `store.Maintain()` | Yes — runs inline, fast for <1K learnings |
| **Manual** | `attest learn maintain` | Yes — prints report |

No background goroutines. No lazy trigger from `Query()`.

### 11.4 Prevention Check Compilation

Learnings with effectiveness > 0.7 and `AttachCount >= 3` are compiled into prevention checks — markdown files injected into council reviewer prompts.

```markdown
---
id: lrn-abc123
source_learning: lrn-abc123
compiled_at: 2026-03-22T10:00:00Z
applicable_paths:
  - internal/engine
---
# Prevention: Concurrent state file access must use flock

- **Pattern:** State file read-modify-write without flock causes data loss
- **Ask:** Does the current plan/spec account for concurrent access?
- **Do:** Verify all state file mutations use `withLock` pattern
```

Injection points:

| Pipeline stage | Which checks | How injected |
|---|---|---|
| `attest tech-spec review` | `prevention/review/*.md` matching spec paths | Appended to reviewer prompt |
| `attest plan review` | `prevention/planning/*.md` matching plan paths | Appended to reviewer prompt |
| `attest context` | All matching by tags/paths | Included in ContextBundle |

### 11.5 What was removed (and why)

| Feature | Why removed |
|---------|-------------|
| `CitedCount` / `LastCitedAt` / `RecordCitation` | Citation count correlates with retrieval frequency, not quality. Replaced by outcome tracking. |
| Maturity progression (provisional → candidate → established) | 85% of learnings stayed provisional. The 13% that reached "established" were noisy. Replaced by effectiveness score. |
| Utility decay (lazy at query time) | Real utility range was 0.47–0.79 — decay never meaningfully filtered. Replaced by 90-day inactivity auto-expiry. |
| Maturity-weighted scoring | Maturity is removed. Effectiveness score is the single ranking signal. |
| `MaintainIfStale` background goroutine | Caused test cleanup issues. Maintenance on explicit triggers only. |
| MemRL feedback loop | Over-engineered for single-project scale. |

### 11.6 Implementation Roadmap

| Order | What | Est. lines |
|---|---|---|
| 1 | Replace `Utility`/`CitedCount`/`LastCitedAt`/`Maturity` with `AttachCount`/`SuccessCount`/`LastAttachedAt` on Learning struct | ~-40 |
| 2 | Replace `RecordCitation(s)` with `RecordOutcome` on Store + LearningEnricher interface | ~+20, -30 |
| 3 | Wire `RecordOutcome` into `Engine.VerifyTask` | +10 |
| 4 | Replace utility-based sorting with effectiveness-based sorting in `Query` | ~10 |
| 5 | Simplify `Maintain`: remove maturity transitions, add auto-expiry by `LastAttachedAt` | ~-50, +20 |
| 6 | Prevention check compilation (`Store.CompilePreventionChecks`) | +80 |
| 7 | Prevention loading in `cmdTechSpecReview` and `cmdPlan` | +30 |

**Estimated net change:** ~-50 lines (simplification).

### 11.7 The Simplified Loop

```
1. Run starts → compile tasks with learning enrichment
   ├─ Tasks get Warnings (anti_pattern) and Constraints (codebase/tooling)
   └─ LearningIDs attached to tasks (outcome tracked later at verify time)

2. Tech-spec review → council reviewers see prevention checks
   └─ High-effectiveness learnings compiled as "Prior findings"

3. Council findings → extracted as new learnings (auto)
4. Verification failures → extracted as learnings (auto)

5. Verification outcome → RecordOutcome(learningIDs, pass/fail)
   └─ SuccessCount incremented for passing tasks

6. Run completion → Maintain runs
   ├─ Dedup merges near-duplicate learnings
   ├─ Contradiction scan flags conflicting knowledge
   ├─ High-effectiveness learnings compiled to prevention checks
   ├─ Inactive learnings (90 days no match) auto-expired
   └─ Expired/superseded learnings garbage collected

7. Next run benefits from ALL prior learnings
   └─ Effectiveness score ensures quality learnings surface first
```

---

## Appendix: Pre-Mortem Findings (2026-03-20)

5 findings from inline pre-mortem. All resolved in this plan revision.

| ID | Severity | Finding | Resolution |
|----|----------|---------|------------|
| pm-001 | significant | `OwnedPaths` and `SourceOwnedPaths` duplicate fields on Learning struct — two council reviewers added overlapping fields | Consolidated into single `SourcePaths` field. Removed `OwnedPaths`. |
| pm-002 | significant | `OwnedPaths population` paragraph trapped inside code fence — implementer would miss field semantics | Moved outside code fence as standalone paragraph. |
| pm-003 | moderate | Engine→learning import violates architecture constraint (engine must not import concrete backends) | Added `LearningEnricher` interface to `state/types.go`. Engine holds the interface, not `*learning.Store`. Same pattern as `ClaimableStore`. |
| pm-004 | moderate | No `UpdatedAt` on Learning — makes debugging stale data harder | Accepted risk. Flock provides real protection. Can add later if debugging needs arise. |
| pm-005 | low | `Now func() time.Time` field missing from Store struct definition despite clock injection requirement | Added `Now` field to Store struct definition. |
