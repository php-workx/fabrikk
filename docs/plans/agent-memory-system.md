# Plan: Agent Memory System for attest

**Status:** Planned
**Branch:** `feat/track-work`
**Date:** 2026-03-20

---

## 1. Problem Statement

After implementing the ticket-based task store with claims, we identified three gaps in agent effectiveness:

1. **Learnings lost between sessions.** What an agent learns implementing Wave 3 (e.g., "use withLock for atomic read-check-write") isn't available when implementing Wave 4. Each session re-discovers the same patterns.

2. **New tasks don't benefit from past experience.** The compiler produces tasks with requirements, scopes, and dependencies — but no guidance on what approaches worked or failed for similar tasks in previous runs.

3. **Session continuity is expensive.** When a session ends mid-task, the next session burns context reconstructing where we left off by re-reading code, commits, and ticket states.

## 2. Prior Art Research

Six open-source memory frameworks were studied, plus AgentOps' knowledge flywheel:

| System | Key Pattern | Relevance to attest |
|--------|-------------|---------------------|
| **memU** ([GitHub](https://github.com/NevaMind-AI/memU)) | Memory as human-readable markdown files, file-system-inspired hierarchy | High — matches our .tickets/ approach. Memory Agent continuously consolidates files. 92% LoCoMo accuracy. |
| **Zep/Graphiti** ([GitHub](https://github.com/getzep/graphiti)) | Temporal knowledge graph with bi-temporal fact tracking (valid-from/valid-to) | Partial — temporal reasoning is useful but requires Neo4j/FalkorDB graph DB (too heavy). |
| **mem0** ([GitHub](https://github.com/mem0ai/mem0)) | Universal memory layer: user/session/agent tiers, vector semantic search | Low — Python-centric, requires vector DB + LLM for extraction. Opposite of our stdlib Go approach. |
| **Cognee** ([GitHub](https://github.com/topoteretes/cognee)) | Knowledge graph + vector search, local-first (SQLite + LanceDB + Kuzu) | Partial — local-first aligns but 30+ connectors and multimodal are way beyond scope. |
| **Letta** ([GitHub](https://github.com/letta-ai/letta)) | OS-inspired three-tier memory (core/recall/archival), agents self-edit memory blocks | Partial — self-editing concept is interesting but requires LLM compute for every memory decision. |
| **Memobase** ([GitHub](https://github.com/memodb-io/memobase)) | User-centric profile + event timeline, sub-100ms SQL queries, Go SDK | High — profile + timeline maps to our project learnings + task history. Simple, fast. |
| **AgentOps** (~/workspaces/agentops) | Tagged JSONL learnings with utility/confidence scoring, inverted index, CASS decay, handoff artifacts | High — most directly applicable. Tagged retrieval, maturity levels, session continuity. |

### What we adopt

- **memU:** Memory as readable files (already our pattern)
- **Memobase:** Profile + event timeline, SQL-fast retrieval
- **AgentOps:** Tagged JSONL with utility scoring, inverted index for topic search, session handoff artifacts
- **Letta:** Three-tier concept (core context / session / persistent) as a mental model

### What we explicitly skip

- Vector databases, graph databases, embedding pipelines
- LLM-assisted memory extraction (our extraction is deterministic or human-initiated)
- Semantic search (tag-based retrieval is sufficient for a single-project tool)
- External services or APIs

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
```

**Why JSONL:** AgentOps' `.agents/learnings/` approach produced 76+ individual markdown files with auto-generated names — hard to scan, query, or manage. A single `index.jsonl` gives atomic-rewrite via temp-file + rename (crash-safe), sub-millisecond scans for hundreds of entries, and a single file to back up.

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
    Confidence   float64  `json:"confidence"`             // 0-1, starts at 0.5
    Utility      float64  `json:"utility"`                // 0-1, EMA-smoothed
    CitedCount   int        `json:"cited_count"`           // times injected into context
    LastCitedAt  *time.Time `json:"last_cited_at,omitempty"` // set by RecordCitation
    SupersededBy string   `json:"superseded_by,omitempty"`
    Expired      bool     `json:"expired"`                // soft-delete
}
```

**SourcePaths population:** When a learning is created with a `SourceTask`, `SourcePaths` is copied from that task's `Scope.OwnedPaths`. When created manually via CLI, `SourcePaths` may be specified with `--path` flags. Path-based queries (`QueryOpts.Paths`) match when any query path equals any learning `SourcePaths` entry (normalized exact path equality).

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
    Tags          []string  // match any tag
    Category      Category  // exact category match
    Paths         []string  // match learnings with overlapping SourcePaths
    MinUtility    float64   // utility threshold (default 0.0)
    Limit         int       // max results (0 = unlimited)
    SortBy        string    // "utility" (default), "created_at"
}
```

#### Store interface

```go
type Store struct {
    Dir string         // .attest/learnings/
    Now func() time.Time // clock injection for tests; defaults to time.Now
}

func NewStore(dir string) *Store

// Write operations
func (s *Store) Add(l *Learning) error
func (s *Store) RecordCitation(id string) error

// Query operations
func (s *Store) Query(opts QueryOpts) ([]Learning, error)
func (s *Store) Get(id string) (*Learning, error)

// Index operations
func (s *Store) RebuildIndex() error

// Session handoff
func (s *Store) WriteHandoff(h *SessionHandoff) error
func (s *Store) LatestHandoff() (*SessionHandoff, error)

// Maintenance
func (s *Store) GarbageCollect(maxAge time.Duration) (int, error)
```

#### Engine integration interface (`internal/state/types.go`)

The engine must not import `internal/learning` (same constraint as engine→ticket). Define a `LearningEnricher` interface in the state package:

```go
// LearningEnricher enriches tasks with learnings from a learning store.
// Implemented by learning.Store. The engine type-asserts to this interface.
type LearningEnricher interface {
    Query(opts LearningQueryOpts) ([]LearningRef, error)
    RecordCitation(id string) error
}

// LearningQueryOpts is the subset of query options the engine needs.
type LearningQueryOpts struct {
    Tags       []string
    Paths      []string
    MinUtility float64
    Limit      int
}
```

The engine holds `LearningEnricher` (an interface), not `*learning.Store` (a concrete type). `learning.Store` satisfies the interface. Same pattern as `ClaimableStore`.

#### Concurrency

All mutating Store operations (`Add`, `RecordCitation`, `GarbageCollect`) acquire an exclusive flock on `.attest/learnings/.lock` before reading or writing any store file. Both `index.jsonl` and `tags.json` are written atomically while the lock is held. Lock is released after all writes complete. Same pattern as `internal/ticket/claim.go`.

#### Utility decay

Lazy — computed at query time, not a background daemon. Learnings whose `LastCitedAt` (or `CreatedAt` if never cited) is older than 30 days have utility decremented by 0.05 (floor 0.0). `RecordCitation` increments `CitedCount` and sets `LastCitedAt` to the current time. This keeps the store self-pruning: low-utility learnings naturally fall below query thresholds.

**Citation definition:** A citation occurs when a learning is included in `ContextBundle.Learnings` by `AssembleContext`. `RecordCitation` increments `CitedCount` and sets `LastCitedAt` to `Now()`. Decay compares `Now()` to `LastCitedAt`, or `CreatedAt` when never cited.

**Clock injection:** Time-dependent logic (utility decay, handoff staleness, garbage collection) uses an injected clock (`Now func() time.Time`) on the `Store` type, defaulting to `time.Now`. Tests must use a fixed clock when verifying decay, staleness, and GC thresholds.

#### Tag inverted index (`tags.json`)

```json
{
  "concurrency": ["lrn-a1b2c3d4", "lrn-e5f6g7h8"],
  "compiler": ["lrn-i9j0k1l2"],
  "flock": ["lrn-a1b2c3d4"]
}
```

Rebuilt from `index.jsonl` on every `Add()`. Enables O(1) tag lookup for queries. Keywords extracted from content (top 10 significant words) are also indexed.

### 3.2 Intent-First Ticket Fields

#### New Frontmatter fields (`internal/ticket/format.go`)

```go
// Learning context — injected by engine post-compilation, consumed by worker agents.
Intent      string   `yaml:"intent,omitempty"`       // Why this task exists
Constraints []string `yaml:"constraints,omitempty"`   // Known constraints from learnings
Warnings    []string `yaml:"warnings,omitempty"`      // Anti-patterns to avoid
LearningIDs []string `yaml:"learning_ids,omitempty"`  // Attached learning IDs
```

#### New Task fields (`internal/state/types.go`)

```go
// Add to state/types.go:
type LearningRef struct {
    ID       string  `json:"id"`
    Category string  `json:"category"`
    Utility  float64 `json:"utility"`
    Summary  string  `json:"summary"`
}

// Add to state.Task struct:
Intent          string        `json:"intent,omitempty"`
Constraints     []string      `json:"constraints,omitempty"`
Warnings        []string      `json:"warnings,omitempty"`
LearningIDs     []string      `json:"learning_ids,omitempty"`
LearningContext []LearningRef `json:"learning_context,omitempty"`
```

#### Compiler stays deterministic

The compiler remains pure — no learning store access. The engine enriches tasks *after* compilation:

```go
// In Engine.Compile, after compiler.CompileExecutionPlan:
if e.LearningEnricher != nil {
    for i := range result.Tasks {
        e.enrichTaskWithLearnings(&result.Tasks[i])
    }
}
```

`enrichTaskWithLearnings` queries by task tags + owned paths and attaches the top 5 learnings by utility. It does not infer prose from `Content`. It sets `Warnings` from the `Summary` of matched `anti_pattern` learnings, sets `Constraints` from the `Summary` of matched `codebase` and `tooling` learnings, and leaves `Intent` unchanged. `LearningIDs` is populated with the IDs of all attached learnings. `LearningContext` is populated with `LearningRef` entries (ID, Category, Utility, Summary) for each attached learning, providing MarshalTicket with the structured data needed to render the `## Context from Learnings` body section.

#### MarshalTicket body rendering

When `LearningContext` is non-empty, `MarshalTicket` renders a `## Context from Learnings` section in the markdown body from the structured `LearningRef` entries (ID, category, utility, summary). This makes learnings visible via `tk show` and provides agents with context without reading the learning store separately.

Example enriched ticket body:

```markdown
# Implement AT-FR-001 to AT-FR-003

## Scope
Owned paths: internal/compiler

## Acceptance Criteria
- test_pass
- quality_gate_pass

## Context from Learnings

- **lrn-a1b2c3d4** (pattern, utility=0.79): Requirement grouping by source
  line proximity reduces task count by ~40% without losing granularity.
- **lrn-e5f6g7h8** (anti_pattern, utility=0.65): Grouping by ID prefix alone
  ignores source proximity and produces poorly-scoped tasks.
```

### 3.3 Session Handoff

#### Creation

```bash
attest learn handoff \
  --run run-1234 --task task-at-fr-001 \
  --summary "Implemented grouping logic, tests passing" \
  --next "Wire enrichment into Compile" \
  --next "Add CLI command for learn query"
```

Writes to `.attest/learnings/handoffs/<timestamp>.json` and copies to `latest-handoff.json`.

#### Consumption

- `attest next <run-id>` displays the latest handoff alongside the next dispatchable task
- `attest status` shows handoff summary if < 24h old
- The `attest context` command includes the handoff in the context bundle

### 3.4 Agent Context Assembly

#### ContextBundle type

```go
type ContextBundle struct {
    TaskID      string          `json:"task_id"`
    Learnings   []Learning      `json:"learnings"`
    Handoff     *SessionHandoff `json:"handoff,omitempty"`
    TokensUsed  int             `json:"tokens_used"`
    TokenBudget int             `json:"token_budget"`
}
```

New method on Engine:

```go
func (e *Engine) AssembleContext(task *state.Task) (*learning.ContextBundle, error)
```

Three query strategies, deduped and capped at token budget:

1. **Tag match:** Learnings matching any of the task's tags
2. **Path match:** Learnings whose `SourcePaths` overlap with the task's `Scope.OwnedPaths` (normalized exact path equality; matches when any path overlaps)
3. **Explicit IDs:** Learnings referenced in `LearningIDs` frontmatter field

Token budget: 2000 tokens (~500 words). Max 8 learnings per task. Estimated as `len(content) / 4`.

### 3.5 CLI Commands

New help group:

```go
{
    Title: "Learning",
    Commands: []helpCommand{
        {"learn", "Add, query, or manage learnings"},
        {"context", "Assemble agent context for a task"},
    },
}
```

Sub-commands:

```
attest learn "content" --tag X --category pattern     # Add manually
attest learn query --tag X --category Y --limit 5     # Query
attest learn handoff --summary "..." --next "..."     # Session handoff
attest learn list                                     # List all active
attest learn gc                                       # Garbage collect
```

Standalone:

```
attest context <run-id> <task-id>                     # Assemble + display
```

## 4. Files to Modify

| File | Change | Est. lines |
|------|--------|-----------|
| `internal/learning/types.go` | **NEW** — Learning, SessionHandoff, QueryOpts, Category, ContextBundle | ~80 |
| `internal/learning/store.go` | **NEW** — Store with JSONL read/write, query, citation tracking | ~200 |
| `internal/learning/store_test.go` | **NEW** — Add, Query, handoff, citation, GC tests | ~250 |
| `internal/learning/index.go` | **NEW** — Tag inverted index build/query/keyword extraction | ~100 |
| `internal/state/types.go` | Add Intent, Constraints, Warnings, LearningIDs to Task struct | +4 |
| `internal/ticket/format.go` | Add 4 fields to Frontmatter, wire TaskToFrontmatter/FrontmatterToTask | +20 |
| `internal/state/types.go` | Add LearningEnricher + LearningQueryOpts interfaces | +12 |
| `internal/engine/engine.go` | Add LearningEnricher field, enrichTaskWithLearnings, AssembleContext | +60 |
| `cmd/attest/main.go` | Wire `learn` and `context` commands | +15 |
| `cmd/attest/learn.go` | **NEW** — Learn sub-commands (add, query, handoff, list, gc) | ~200 |
| `cmd/attest/help.go` | Add "Learning" help group | +5 |
| `cmd/attest/next.go` | Display latest handoff alongside next dispatchable task | +15 |
| `cmd/attest/status.go` | Show handoff summary when age < 24h | +10 |

**Estimated total:** ~930 new/modified lines.

## 5. Implementation Order

1. **Core store** (internal/learning/) — types.go, store.go, index.go, store_test.go. No deps on engine or ticket. Self-contained package.
2. **CLI commands** (cmd/attest/) — learn.go, main.go wiring, help.go update. Exercise the store via CLI.
3. **Ticket fields** (format.go + types.go) — Add Intent/Constraints/Warnings/LearningIDs fields, round-trip through frontmatter.
4. **Engine enrichment** — LearningStore field on Engine, enrichTaskWithLearnings post-compilation, wire in Compile.
5. **Session handoff** — WriteHandoff/LatestHandoff + display in `attest next` and `attest status`.
6. **Context assembly** — AssembleContext method + `attest context` command.

## 6. Design Decisions

### JSONL over individual files

AgentOps produces 76+ individual markdown files with auto-generated names. Scanning them requires reading every file. JSONL gives: atomic-rewrite via temp-file + rename (crash-safe), sub-millisecond full scan for hundreds of entries, single file to back up, and trivial `grep` for debugging. All mutations (Add, RecordCitation, GC, supersede) read the full file, apply changes in memory, and rewrite atomically.

### Engine enrichment, not compiler enrichment

The compiler is deterministic and pure (spec section 7.2). Learning injection is a runtime concern — the engine enriches tasks *after* compilation. This preserves the architecture boundary: compiler produces the task graph, engine adds operational context.

### Conservative token budget (2000 tokens)

Better to inject 5 high-utility learnings than 50 mediocre ones. Agents have limited context windows, and learnings should enhance (not dominate) the task description.

### No new dependencies

Uses stdlib `encoding/json` for JSONL, `os` for file I/O, existing `flock` for concurrent safety. No vector DB, no embedding model, no external service.

### Forward-compatible ticket fields

New YAML fields use `omitempty` and live alongside the existing `Extra` catch-all. Tickets without learnings are unchanged. The tk CLI passes unknown fields through.

## 7. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Learning quality is low (garbage in) | Agents receive unhelpful context, wasting tokens | Utility decay + manual `attest learn gc`. Low-utility learnings fall below query thresholds naturally. |
| JSONL file grows unbounded | Slow queries, large file | GarbageCollect removes expired entries >90 days old. At 1KB/learning, 10K learnings = 10MB — acceptable. |
| Tag taxonomy drift | Inconsistent tagging makes queries miss relevant learnings | Normalize tags (lowercase, strip whitespace). The inverted index catches partial matches via keyword extraction. |
| Enrichment slows compilation | Adding learning lookups to Compile path | Learning store scan is sub-millisecond for <10K entries. Enrichment is a single pass over tasks after compilation. |
| Session handoff becomes stale | Next session reads outdated handoff | Handoff displayed only if < 24h old. `attest status` shows age. |

## 8. Verification

1. `go build ./...` — compiles
2. `go test -race ./internal/learning/... ./internal/ticket/... ./internal/engine/... ./cmd/attest/...`
3. `golangci-lint run`
4. Manual: `attest learn "test learning" --tag test --category pattern` → verify in `.attest/learnings/index.jsonl`
5. Manual: `attest learn query --tag test` → returns the learning
6. Manual: `attest learn handoff --summary "test session"` → verify latest-handoff.json
7. Manual: compile a run with learnings → verify `## Context from Learnings` in ticket body
8. Manual: `attest context <run-id> <task-id>` → shows assembled context bundle
9. Manual: `attest next <run-id>` → shows latest handoff when present
10. Manual: `attest status` → shows handoff summary when age < 24h and omits it otherwise

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
