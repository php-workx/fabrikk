# Plan: Agent Memory System for attest

**Status:** Implemented
**Branch:** `feat/agent-memory`
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

**SourcePaths population:** When a learning is created with a `SourceTask`, `SourcePaths` is copied from that task's `Scope.OwnedPaths`. When created manually via CLI, `SourcePaths` may be specified with `--path` flags. Path-based queries (`QueryOpts.Paths`) match when any query path equals any learning `SourcePaths` entry (normalized exact path equality). All `SourcePaths` and query paths MUST be repo-root-relative, normalized with `filepath.Clean`, converted to forward-slash separators, and compared case-sensitively. CLI absolute paths are converted to repo-relative before storage.

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
// LearningQueryOpts is the subset of query options the engine needs.
type LearningQueryOpts struct {
    Tags       []string
    Paths      []string
    MinUtility float64
    Limit      int
}

// LearningEnricher enriches tasks with learnings from a learning store.
// Implemented by learning.Store. The engine type-asserts to this interface.
type LearningEnricher interface {
    QueryLearnings(opts LearningQueryOpts) ([]LearningRef, error)
    RecordCitation(id string) error
}
```

The engine holds `LearningEnricher` (an interface), not `*learning.Store` (a concrete type). `learning.Store` satisfies the interface. Same pattern as `ClaimableStore`.

#### Concurrency

All mutating Store operations (`Add`, `RecordCitation`, `GarbageCollect`) acquire an exclusive flock on `.attest/learnings/.lock` before reading or writing any store file. Both `index.jsonl` and `tags.json` are written atomically while the lock is held. Lock is released after all writes complete. Same pattern as `internal/ticket/claim.go`.

#### Corruption Handling

If any non-empty `index.jsonl` line fails to decode, mutating operations (`Add`, `RecordCitation`, `GarbageCollect`, `Maintain`, split-store promotion) MUST return `ErrCorruptLearningStore` and MUST NOT rewrite `index.jsonl` or `tags.json`. Read-only operations (`Query`, `Get`) may surface partial results with a warning logged to stderr. Repair requires an explicit `attest learn repair` command or manual intervention.

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

Rebuilt from `index.jsonl` on every `Add()`. Enables O(1) tag lookup for queries. Keywords extracted from content (top 10 significant words) are also indexed. Keyword extraction algorithm: lowercase all text, split on non-alphanumeric boundaries, drop tokens shorter than 3 characters and tokens in a fixed stop-word list, count exact-token frequency, break ties alphabetically, keep the top 10 tokens.

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

`enrichTaskWithLearnings` derives query tags from the task and queries by those tags + owned paths, attaching the top 5 learnings sorted by utility descending, then `created_at` descending, then ID ascending. **Tag derivation:** Tags are derived from (1) package names extracted from `Scope.OwnedPaths` (e.g., `internal/compiler` → tags `compiler`, `internal`), (2) requirement ID prefixes (e.g., `AT-FR-001` → tag `functional`, `AT-TS-002` → tag `testing`), and (3) the task's `Category` field if present. These derived tags are used for the `LearningQueryOpts.Tags` query — they are not persisted on the Task struct. It does not infer prose from `Content`. It sets `Warnings` from the `Summary` of matched `anti_pattern` learnings, sets `Constraints` from the `Summary` of matched `codebase` and `tooling` learnings, and leaves `Intent` unchanged. `LearningIDs` is populated with the IDs of all attached learnings. `LearningContext` is populated with `LearningRef` entries (ID, Category, Utility, Summary) for each attached learning, providing MarshalTicket with the structured data needed to render the `## Context from Learnings` body section.

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

- `attest next <run-id>` and `attest status` MUST omit handoff content older than 24 hours. When a handoff is shown, `attest status` also displays its age.
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

Method on `learning.Store` (not Engine — engine cannot import the learning package):

```go
func (s *Store) AssembleContext(taskID string, tags, paths []string) (*ContextBundle, error)
```

Two query strategies, deduped and capped at token budget:

1. **Tag match:** Learnings matching any of the task's tags (including content keywords)
2. **Path match:** Learnings whose `SourcePaths` overlap with the task's `Scope.OwnedPaths` (normalized exact path equality; matches when any path overlaps)

Merge tag and path matches by learning ID into one candidate set, sort by utility descending, then `created_at` descending, then ID ascending, apply the max-8 cap after sorting, and stop before adding any item that would exceed `TokenBudget`. Render outputs in that same order. Token budget: 2000 tokens (~500 words). Estimated as `len(content) / 4`.

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
| `internal/learning/index_test.go` | **NEW** — Keyword extraction and indexing tests | ~60 |
| `internal/engine/engine.go` | Add LearningEnricher field, enrichTaskWithLearnings | +60 |
| `cmd/attest/main.go` | Wire `learn` and `context` commands, LearningEnricher on engine | +15 |
| `cmd/attest/learn.go` | **NEW** — Learn sub-commands (add, query, handoff, list, gc) + context | ~200 |
| `cmd/attest/help.go` | Add "Learning" help group | +5 |
| `cmd/attest/tasks.go` | Display latest handoff in `next` + `showLatestHandoff` helper | +15 |
| `cmd/attest/main.go` | Show handoff in `status` command | +2 |

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
9. Manual: `attest next <run-id>` → shows latest handoff when age < 24h and omits it otherwise
10. Manual: `attest status` → shows handoff summary when age < 24h and omits it otherwise

---

## 9. Phase 2: Learning Extraction & Injection

**Status:** Implemented
**Date:** 2026-03-21

Phase 1 built the learning store, query engine, and task enrichment pipeline. Phase 2 closes the loop: learnings are automatically extracted from pipeline events and injected into future pipeline stages.

### 9.1 The Closed Loop

```
Council findings ──→ learning store ──→ task enrichment (compile time)
Verifier failures ──→ learning store ──→ council reviewer context
Judge rejections ──→ learning store ──→ worker dispatch context
Run completion   ──→ learning store ──→ future plan reviews
```

Without Phase 2, the learning store is purely manual (`attest learn "..."`) — useful but passive. The goal is to make every pipeline stage both a producer and consumer of institutional knowledge.

### 9.2 Extraction Points (Where Learnings Are Produced)

#### 9.2.1 Council Review Findings → Learnings

**Trigger:** After `CouncilReviewTechnicalSpec` or execution plan council review completes.

**Source data:** `councilflow.CouncilResult` contains `Rounds[]`, each containing `Reviews[]` with `Findings[]`. Each `councilflow.Finding` has:

```go
type Finding struct {
    FindingID      string // "sec-001", "perf-001"
    Severity       string // critical, significant, minor
    Category       string // category label
    Section        string // spec section reference
    Description    string // what the problem is
    Recommendation string // concrete change to spec
}
```

**Mapping to learnings:**

| Finding field | Learning field | Rule |
|---|---|---|
| FindingID | — | Used for dedup (tag: `finding:<id>`) |
| Severity critical/significant | Category `anti_pattern` | Blocking finding = something to avoid |
| Severity minor | Category `codebase` | Non-blocking = contextual knowledge |
| Category | Tags | Lowercased, added as tag |
| Section | Tags | Lowercased, section name extracted as tag |
| Description | Content | The learning content |
| Recommendation | Content (appended) | "Recommendation: ..." appended to content |
| Description (first line) | Summary | Truncated to 100 chars |

**Confidence derivation:**

| Condition | Confidence |
|---|---|
| Finding accepted by judge (in `AppliedEdits`) | 0.8 |
| Finding present, no judge rejection | 0.6 |
| Finding from unanimous reviewer consensus | 0.7 |
| Default | 0.5 |

**Deduplication:** Before adding, query by tag `finding:<finding_id>`. If a learning with the same finding ID already exists, skip (don't re-add on repeated reviews). If the finding has evolved (different description), supersede the old learning.

**SourceRun** and **SourcePaths:** Set from the run ID and the files referenced in the spec section. If the finding references specific paths via `Section` or `Location`, extract those as `SourcePaths`. Otherwise, use the run-level file scope.

**What triggers extraction:** Extraction is triggered in `cmd/attest` after council review and verify commands complete. The engine and `councilflow` packages do not perform extraction directly (per §9.4 interface boundaries). The extraction function iterates the final round's reviews and the judge's consolidation, extracting findings that were NOT rejected.

#### 9.2.2 Judge Rejections → Pattern Learnings

**Trigger:** After judge consolidation, for each rejected finding.

**Source data:** `councilflow.ConsolidationResult.RejectionLog.Rejections[]`, each with:

```go
type Rejection struct {
    FindingID          string
    PersonaID          string
    Severity           string
    Description        string
    RejectionReason    string
    DismissalRationale string
    CrossReference     string
}
```

**Mapping:** A rejected finding is knowledge: "This was flagged as a problem but isn't, because X." This prevents future reviewers from raising the same false positive.

| Rejection field | Learning field | Rule |
|---|---|---|
| Description + RejectionReason | Content | "Reviewer flagged: {Description}. Judge rejected: {RejectionReason}" |
| — | Category | `pattern` (valid approach that was incorrectly questioned) |
| DismissalRationale | Content (appended) | Explains why the concern is invalid |
| FindingID | Tags | `rejected:<finding_id>` |
| PersonaID | Tags | Reviewer persona as tag |

**Confidence:** 0.7 (judge decisions are high quality, but rejections can be wrong).

**When NOT to extract:** Only extract rejections with a non-empty `DismissalRationale`. A bare rejection without reasoning is not useful knowledge.

#### 9.2.3 Verifier Failures → Anti-Pattern Learnings

**Trigger:** After `Engine.VerifyTask` returns a result with `Pass == false`.

**Source data:** `state.VerifierResult.BlockingFindings[]`, each with:

```go
type Finding struct {
    FindingID   string // unique ID
    Severity    string // critical, high, medium, low
    Category    string // quality_gate, missing_evidence, scope_violation, requirement_linkage
    Summary     string
    EvidenceRef string
    DedupeKey   string
}
```

**Mapping:**

| Verifier category | Learning category | Example |
|---|---|---|
| `quality_gate` | `tooling` | "Quality gate failed: gofumpt formatting" |
| `missing_evidence` | `process` | "Task completed without test_pass evidence" |
| `scope_violation` | `anti_pattern` | "Modified files outside owned paths" |
| `requirement_linkage` | `process` | "Task has no requirement IDs" |

**Confidence:** 0.9 (verifier findings are deterministic — high confidence).

**Tags:** Derived from the task's tags + the finding category. `SourceTask` and `SourcePaths` copied from the verified task.

**Deduplication:** By `DedupeKey`. If a learning with tag `verifier:<dedupe_key>` already exists, increment its confidence (capped at 1.0) instead of creating a duplicate. Repeated failures on the same issue should strengthen the learning, not clutter the store.

#### 9.2.4 Run Completion → Process Learnings

**Trigger:** When all tasks in a run reach `done` status (run state transitions to `completed`).

**This extraction is human-initiated, not automatic.** The engine logs a suggestion:

```
Run completed. Consider capturing learnings:
  attest learn "..." --source-run <run-id> --category process
```

**What to capture (guidance, not automation):**
- Wave count vs. expected: "Run needed 7 waves due to cascading scope violations in wave 3"
- Retry patterns: "Task X failed verification 3 times before passing — initial approach was wrong"
- File conflict resolutions: "Tasks A and B both touched config.go — serializing was the right call"
- Cross-task patterns: "All compiler tasks needed the same withLock pattern"

**Why not automatic:** Process learnings require judgment about what's significant. A run completing in 3 waves isn't inherently a learning — only the unexpected is worth capturing. Automation would produce noise.

### 9.3 Injection Points (Where Learnings Are Consumed)

#### 9.3.1 Task Enrichment at Compile Time (Already Implemented)

**When:** After `compiler.CompileExecutionPlan`, before `WriteTasks`.

**How:** `enrichTaskWithLearnings` queries by task tags + owned paths, attaches top 5 by utility. Anti-pattern summaries → `Warnings`. Codebase/tooling summaries → `Constraints`. `LearningContext` rendered in ticket body.

**No changes needed** — this works today.

#### 9.3.2 Council Reviewer Context (New)

**When:** Before running council reviews (tech-spec or execution plan).

**How:** Query the learning store for learnings matching the scope under review. Include them in the reviewer prompt as "Prior findings context."

**Query strategy:**
1. Extract file paths from the spec/plan being reviewed
2. Query by paths + tags derived from spec section headings
3. Cap at 5 learnings, utility >= 0.3

**Prompt injection format:**

```
## Prior Findings (from learning store)

The following issues have been identified in previous reviews of similar code:

- [lrn-abc123] (anti_pattern, utility=0.8): Concurrent state file access must
  use flock — previous implementation had race conditions.
- [lrn-def456] (pattern, utility=0.7): Reviewer flagged atomic writes as
  unnecessary, but judge confirmed they're required for crash safety.

Consider these when reviewing. Do not re-raise issues that have been resolved
unless you see evidence they have regressed.
```

**Why this matters:** Without this, every reviewer starts from zero. A reviewer might flag "no concurrent access protection" when flock was already added in a previous iteration. The prior findings context gives reviewers institutional memory.

**Architecture:** The CLI layer (`cmdTechSpecReview`) queries the learning store before invoking the council. This avoids adding a learning store dependency to the councilflow package. The learnings are passed as part of the spec content or as a preamble in the review packet.

#### 9.3.3 Worker Dispatch Context (Partially Implemented)

**When:** Before dispatching a task to a worker agent.

**How:** `AssembleContext` produces a `ContextBundle` with learnings + handoff. This should be included in the worker's prompt.

**Current state:** The `attest context <run-id> <task-id>` command produces the bundle, and `enrichTaskWithLearnings` puts learnings in the ticket body. But there's no automatic worker prompt template that includes the full context bundle.

**Future integration:** When the engine gains worker dispatch (Phase 4 detached mode), the worker prompt template should include the context bundle output. For now, agents using `attest next` can manually run `attest context` to get their context.

#### 9.3.4 Pre-Mortem / Plan Review Context (New)

**When:** Before reviewing an execution plan.

**How:** Same as council reviewer context (§9.3.2), but scoped to the plan's file paths and requirement IDs. Query learnings matching any of the plan's `ExecutionSlice.OwnedPaths` or `RequirementIDs`.

**Value:** "The last time we planned work on `internal/engine`, we underestimated the scope because we missed the coverage sync dependency." This prevents repeating planning mistakes.

### 9.4 Interface Boundaries

The learning store must not become a dependency of every package. Current boundaries:

```
cmd/attest/     → imports learning, state, engine, ticket, councilflow
internal/engine → imports state, compiler, verifier (NOT learning, NOT councilflow)
internal/councilflow → imports state (NOT learning, NOT engine)
internal/learning → imports state (for LearningRef, LearningQueryOpts)
internal/verifier → imports state (NOT learning)
```

**Extraction methods live in the CLI layer or a new `internal/extract` package**, not in the engine or councilflow. The CLI layer has access to all packages and can bridge between them.

**Option A: CLI-layer extraction (simpler)**

```go
// In cmd/attest/main.go or a new cmd/attest/extract.go
func extractLearningsFromCouncil(store *learning.Store, result *councilflow.CouncilResult, runID string) error
func extractLearningsFromVerifier(store *learning.Store, result *state.VerifierResult, task *state.Task) error
```

**Option B: Extract package (cleaner)**

```go
// internal/extract/council.go
func CouncilFindings(store LearningWriter, result *councilflow.CouncilResult, runID string) error

// internal/extract/verifier.go
func VerifierFindings(store LearningWriter, result *state.VerifierResult, task *state.Task) error
```

Where `LearningWriter` is a minimal interface:

```go
type LearningWriter interface {
    Add(l *learning.Learning) error
    Query(opts learning.QueryOpts) ([]learning.Learning, error) // for dedup
}
```

**Recommendation:** Option A for initial implementation (simpler, fewer packages). Refactor to Option B if extraction logic grows beyond ~100 lines per source.

### 9.5 New CLI Integration Points

#### Council review commands

After `attest tech-spec review` completes, if the council produced findings:

```
Council review complete: 3 findings (1 accepted, 1 rejected, 1 minor)
Extracted 2 learnings: lrn-abc123, lrn-def456
```

Extraction happens automatically — no extra command needed.

#### Verify command

After `attest verify` completes with failures:

```
Verification: FAIL
  [critical] quality_gate: gofumpt formatting errors
  [high] scope_violation: modified internal/state/types.go (not in owned paths)
Extracted 2 learnings: lrn-ghi789, lrn-jkl012
```

#### Context command enhancement

`attest context` should show learning source in addition to content:

```
Context for task-at-fr-001 (tokens: 450/2000):

Learnings (3):
  [lrn-abc123] (anti_pattern, utility=0.80): Concurrent state file access...
    Source: council review, run-1710234567, finding sec-001
  [lrn-def456] (pattern, utility=0.70): Reviewer flagged atomic writes...
    Source: judge rejection, run-1710234567, finding perf-002
  [lrn-ghi789] (tooling, utility=0.90): Quality gate failed on gofumpt...
    Source: verifier, task-at-fr-003
```

### 9.6 Confidence and Utility Dynamics

Learnings from different sources have different initial confidence:

| Source | Initial confidence | Rationale |
|---|---|---|
| Verifier failure | 0.9 | Deterministic, mechanically verified |
| Council finding (accepted by judge) | 0.8 | Multi-model consensus + judge validation |
| Council finding (not judged) | 0.6 | Single reviewer, no validation |
| Judge rejection | 0.7 | Judge quality is high but not infallible |
| Manual (`attest learn`) | 0.5 | Default, unvalidated |

**Utility starts at confidence value.** Decay and citation tracking then drive utility independently of confidence. A high-confidence learning that's never cited will decay; a medium-confidence learning that's cited frequently will maintain high utility.

**Confidence boosting:** When a verifier failure matches an existing learning (same `DedupeKey`), the existing learning's confidence is boosted by 0.1 (capped at 1.0). Repeated failures on the same issue confirm the learning is real.

### 9.7 Files to Modify (Phase 2)

| File | Change | Est. lines |
|------|--------|-----------|
| `cmd/attest/extract.go` | **NEW** — Council + verifier learning extraction functions | ~150 |
| `cmd/attest/main.go` | Call extraction after council review and verify commands | +10 |
| `cmd/attest/learn.go` | Enhanced context display (show learning source) | +15 |
| `internal/learning/types.go` | Add `Source` field to Learning (council/verifier/manual/rejection) | +5 |
| `internal/learning/store.go` | Add `QueryByDedupeTag` helper for dedup checks | +15 |

**Estimated total:** ~195 new/modified lines.

### 9.8 Implementation Order (Phase 2)

1. **Learning Source field** — Add `Source string` to Learning type (e.g., "council", "verifier", "rejection", "manual"). Backward compatible (omitempty).
2. **Verifier extraction** — Simplest source. After `VerifyTask` fails, extract findings. Add dedup by `DedupeKey`.
3. **Council extraction** — After council review, extract accepted findings + rejected findings with rationale.
4. **Council reviewer injection** — Query learnings before review, include in reviewer prompt.
5. **CLI integration** — Wire extraction into `cmdVerify` and `cmdTechSpecReview`. Enhance `cmdContext` display.

### 9.9 Risks and Mitigations (Phase 2)

| Risk | Impact | Mitigation |
|------|--------|------------|
| Extraction produces low-quality learnings | Token waste, misleading context | Start with high-confidence sources only (verifier, accepted council findings). Skip minor/informational findings initially. |
| Too many learnings from one review | Store bloat | Cap at 5 learnings per extraction event. Prefer higher-severity findings. |
| Duplicate learnings from repeated reviews | Store clutter | Dedup by finding ID tag. Boost confidence on repeat instead of duplicating. |
| Stale learnings from old reviews | Misleading context | Utility decay handles this naturally. Old uncited learnings fall below query thresholds. |
| Reviewer anchoring on prior findings | Reduced review independence | Include prior findings AFTER independent assessment prompt. Instruction: "Review independently first, then consider prior findings." |
| Extraction adds latency to review/verify commands | Slower CLI | Extraction is append-only with flock — sub-millisecond. No measurable impact. |

## 10. Data Privacy: Split Stores, Content Scanning, and Optional Encryption

**Status:** Planned
**Date:** 2026-03-21

### 10.1 Problem

Learnings contain knowledge extracted from reviews, verification failures, and implementation patterns. This knowledge may include:

- Secrets and tokens (API keys found during review, credentials in error output)
- Local filesystem paths (`/Users/jane/projects/acme-corp/`)
- Internal hostnames and IP addresses
- Project/company names that reveal client relationships
- Architecture details that are competitively sensitive
- Security vulnerabilities discovered during reviews

Committing all learnings to git exposes this to anyone with repo access. But git is the natural distribution mechanism — it's how agents in different worktrees, team members, and CI/CD pipelines share state.

Additionally, git worktrees (used for multi-agent parallel execution) each have independent working trees. A gitignored `.attest/learnings/` directory is isolated per worktree — agents can't share knowledge.

### 10.2 Three-Layer Protection

#### Layer 1: Split Stores (Local + Shared)

Two store locations, resolved automatically:

```
Local store (never committed, shared across worktrees):
  $(git rev-parse --git-common-dir)/attest/learnings/
  ├─ index.jsonl       # ALL learnings (including sensitive)
  └─ tags.json

Shared store (committed to git):
  .attest/learnings/
  ├─ index.jsonl       # Only learnings that passed content scan
  └─ tags.json         # (optionally age-encrypted)
```

**Why `.git/attest/`:** `git rev-parse --git-common-dir` returns the same path for all worktrees. Every agent, every worktree, every parallel worker sees the same local store. It's never committed — it's inside the git internal directory.

**When `Store.Add()` is called:**
- Manual: `attest learn "content" --tag X --category pattern`
- Phase 2 automatic extraction: after council review findings, verifier failures, judge rejections
- All paths go through `Store.Add()` — single entry point

**On `Store.Add()`:** Every learning is written to the local store first (unredacted, unencrypted). Then the scanner runs. Matched patterns are redacted. The redacted version (with optional content encryption) is written to the shared store. The caller doesn't choose — it's automatic.

**On `Store.Query()`:** Both stores are read and merged. Local-store learnings take precedence by ID (they have unredacted content). Shared-only learnings (from team members) are included with decryption if key is available.

#### Layer 2: Automated Content Scanning

Every learning is scanned on `Add()` before deciding whether to promote to the shared store. The scan is a Go function — fast regex matching, no external tools.

**Scan patterns:**

| Category | Patterns | Examples |
|---|---|---|
| API keys | `(?i)(api[_-]?key\|secret[_-]?key\|access[_-]?key)\s*[:=]\s*\S+` | `api_key=sk-abc123` |
| Passwords | `(?i)(password\|passwd\|pwd)\s*[:=]\s*\S+` | `password=hunter2` |
| Tokens | `(?i)(token\|bearer\|auth)\s*[:=]\s*\S+` | `token=ghp_xxxx` |
| AWS patterns | `AKIA[0-9A-Z]{16}`, `(?i)aws_secret` | AWS access key IDs |
| Private keys | `-----BEGIN (RSA\|EC\|OPENSSH) PRIVATE KEY-----` | PEM-encoded keys |
| URLs with creds | `https?://[^:]+:[^@]+@` | `https://user:pass@host` |
| Local paths | `/Users/\|/home/\|C:\\Users\\` | `/Users/jane/projects/` |
| IP addresses | `\b(?:10\|172\.(?:1[6-9]\|2\d\|3[01])\|192\.168)\.\d+\.\d+\b` | Private network IPs |
| Base64 blobs | `[A-Za-z0-9+/]{40,}={0,2}` (length > 40) | Long encoded tokens |
| Email addresses | `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z]{2,}\b` | `jane@acme-corp.com` |
| Custom terms | Configurable via `.attest/scan-terms.txt` (one per line) | Company names, project codenames |

**Custom terms file (`.attest/scan-terms.txt`):**

```
acme-corp
project-phoenix
internal.acme.com
```

One term per line. Case-insensitive matching. This lets teams block project-specific sensitive terms without modifying the scanner code.

**Scan result:**

```go
type ScanResult struct {
    Clean    bool     // true = safe to share
    Matches  []string // which patterns matched (for debugging, not stored)
}
```

Matched patterns are redacted with `[REDACTED]` before the learning is written to the shared store. This preserves the learning's insight while stripping sensitive details. No user prompt, no manual review.

```go
// "api_key=sk-abc123 caused auth failures" → "api_key=[REDACTED] caused auth failures"
// "/Users/jane/projects/acme/internal/engine" → "[REDACTED]/internal/engine"
// Redacted version promoted to shared store; original stays in local store
```

The scanner also runs on the `Summary` field (which is derived from content and could contain the same sensitive patterns).

**Configurable via `.attest/config.yaml`:**

```yaml
learning_scan:
  mode: redact   # "redact" (default) or "block" (keeps learning local-only, no shared copy)
  custom_terms_file: .attest/scan-terms.txt
```

#### Layer 3: Optional Age Encryption (Passphrase-Based)

When a passphrase is configured, shared-store learnings are encrypted before being written to the committed path. This protects against:

- Repository access by unauthorized parties (forks, leaked repos)
- Content that passed the scan but is still business-sensitive
- Defense in depth — scan catches obvious secrets, encryption catches everything else

**Configuration:** Single passphrase in `.env`:

```bash
# .env (gitignored)
ATTEST_LEARNING_PASSPHRASE=my-project-secret-2026
```

Same passphrase encrypts and decrypts — no key pairs, no recipients file, no identity management. Team members share the passphrase via their preferred secure channel (1Password, Slack DM, etc.) and add it to their local `.env`.

**Implementation:** Uses age's scrypt-based passphrase encryption (`age.NewScryptRecipient` / `age.NewScryptIdentity`). One passphrase, symmetric encrypt/decrypt:

```go
// Encrypt
recipient, _ := age.NewScryptRecipient(passphrase)
// Decrypt
identity, _ := age.NewScryptIdentity(passphrase)
```

**What gets encrypted:** Only the `Content` field. `ID`, `Tags`, `Category`, `Summary`, `Utility`, `Confidence`, `SourcePaths` remain plaintext. Summary is already scan-redacted and is needed for display and query ranking without the passphrase.

```json
{"id":"lrn-abc123","tags":["compiler","concurrency"],"category":"anti_pattern","utility":0.8,"summary":"Concurrent state file access must use flock","content":"ENC[age-scrypt:...]"}
```

**Graceful degradation without passphrase:**

| Operation | With passphrase | Without passphrase |
|---|---|---|
| `attest learn query --tag X` | Full results with content | Results with summary visible, content shows `[encrypted]` |
| `attest context` | Full context bundle | Learnings show summary only, no full content |
| Task enrichment | Warnings/Constraints from summaries | Works — summaries are plaintext |
| `attest learn list` | Full listing | IDs, tags, category, utility, summary all visible |

**No passphrase = still useful.** Summaries (scan-redacted, plaintext) drive task enrichment — Warnings and Constraints are derived from summaries, not full content. A team member without the passphrase gets the same enrichment behavior. The passphrase is only needed to read the full `Content` field.

**Dependency:** `filippo.io/age` — pure Go, well-maintained, modern encryption. One new dependency (total: 5 direct deps).

**Encryption is opt-in.** Without `ATTEST_LEARNING_PASSPHRASE`, shared-store learnings are committed in plaintext (but already scan-cleaned). This keeps the default experience simple.

### 10.3 Store Resolution Logic

```go
func NewStore(workDir string) *Store {
    // Local store: inside .git/ shared directory (all worktrees see this)
    gitCommonDir := exec("git rev-parse --git-common-dir")
    localDir := filepath.Join(gitCommonDir, "attest", "learnings")

    // Shared store: committed path in working tree (same path as Phase 1)
    sharedDir := filepath.Join(workDir, ".attest", "learnings")

    // Passphrase from .env (same passphrase encrypts and decrypts)
    passphrase := os.Getenv("ATTEST_LEARNING_PASSPHRASE")

    return &Store{
        LocalDir:   localDir,
        SharedDir:  sharedDir,
        Passphrase: passphrase,  // empty = no encryption
        Scanner:    NewContentScanner(workDir),
    }
}
```

**Write path (on every `Add()`):**
1. Write unredacted learning to local store (always)
2. Run content scanner on Content + Summary
3. Redact matched patterns (default) or block (configurable)
4. If age key configured: encrypt Content field of redacted version
5. Write redacted+encrypted version to shared store

**Read path:** Merge local + shared, dedup by ID. Local wins (has unredacted, unencrypted content). Shared-only learnings (from team members via git pull) are decrypted if key available.

### 10.4 Migration from Phase 1

Phase 1 uses a single `Dir` field (`Store.Dir = ".attest/learnings/"`). Migration:

1. If `.attest/learnings/index.jsonl` exists (Phase 1 location), it becomes the shared store (same path)
2. Copy all learnings to `.git/attest/learnings/` as the new local store (unredacted originals)
3. Run content scan on shared store entries, redact in place where needed
4. Log migration: "Migrated N learnings to split store (M redacted in shared store)"

The Phase 1 store path is the shared store path — no move needed. The local store is new.

### 10.5 Files to Modify

| File | Change | Est. lines |
|------|--------|-----------|
| `internal/learning/store.go` | Split store resolution (LocalDir + SharedDir), merged reads, split writes | +80 |
| `internal/learning/scanner.go` | **NEW** — Content scanning with regex patterns + custom terms | ~120 |
| `internal/learning/scanner_test.go` | **NEW** — Scan pattern tests | ~100 |
| `internal/learning/encrypt.go` | **NEW** — Age scrypt passphrase encrypt/decrypt for Content field | ~60 |
| `internal/learning/encrypt_test.go` | **NEW** — Encryption round-trip tests | ~60 |
| `go.mod` | Add `filippo.io/age` dependency (optional, behind build tag?) | +1 |

**Estimated total:** ~440 new/modified lines.

### 10.6 Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Scanner false positives (over-redaction) | Shared learning has `[REDACTED]` where it shouldn't | Original unredacted content always in local store. Switch to `block` mode if redaction is too aggressive. Custom terms file lets teams tune. |
| Scanner false negatives (misses a secret) | Redacted content still contains secret, committed to git | Defense in depth: encryption layer catches what scan misses. `.attest/scan-terms.txt` for project-specific terms. |
| Age key lost | Content field of shared learnings unreadable | Graceful degradation — summary, tags, category, utility all plaintext. Task enrichment works from summaries. Re-add learnings from local store (unencrypted). |
| `.git/attest/` path is unusual | Confusing for developers who inspect `.git/` | Document in README. The directory is clearly named. |
| Migration from Phase 1 changes store behavior | Existing users surprised | Migration is automatic and logged. Old path still works as fallback. |
| Content scan adds latency to `Add()` | Slower learning creation | Regex on < 10KB string — microseconds. No measurable impact. |

## 11. Phase 3: The Flywheel — From Passive Store to Active Prevention

**Status:** Implemented
**Date:** 2026-03-21

Phase 2 wires extraction and injection. Phase 3 closes the full feedback loop: learnings are cited, citations drive utility scoring, utility drives maturity progression, and mature learnings are compiled into prevention checks that run automatically.

### 11.1 Prior Art: AgentOps Knowledge Flywheel

AgentOps implements a 5-tier knowledge system with MemRL feedback (studied in `.agents/research/agentops-learnings.md` and `.agents/research/agentops-skill-deep-dive.md`). Key mechanisms:

**MemRL Feedback Loop:**
- Every learning retrieval is logged as a citation (`citations.jsonl`)
- Sessions provide reward/penalty feedback on whether cited learnings were helpful
- Utility updated via EMA: `utility_new = (1 - α) × utility_old + α × reward`
- Learning rate α is annealed: starts high (1.5), decays with citation count toward floor (0.05)
- Confidence decays exponentially when uncited: `confidence × exp(-weeks × 0.1)`

**Maturity Progression:**
```
provisional ──(utility ≥ 0.55, 3+ rewards)──→ candidate
candidate ──(utility ≥ 0.55, 5+ rewards, helpful > harmful)──→ established
any ──(utility ≤ 0.2, 3+ harmful)──→ anti-pattern
candidate ──(utility < 0.3)──→ provisional (demotion)
```

**Finding Compiler:** Raw findings from reviews are compiled into three output types:
- `.agents/findings/*.md` — structured finding artifacts with dedup keys
- `.agents/planning-rules/*.md` — injected as hard context during `/plan`
- `.agents/pre-mortem-checks/*.md` — injected as hard context during `/pre-mortem`

This compilation step is the bridge between "we found a problem" and "we prevent it next time."

**Session Hooks Automate Everything:**
- SessionEnd: `ao forge transcript` → `ao flywheel close-loop` → maturity transitions → finding compilation → prune stale knowledge
- SessionStart: inject relevant knowledge → consume handoffs → point to knowledge signpost

**The Flywheel Equation:**
```
dK/dt = I(t) - δ·K + σ·ρ·K

where:
  I(t) = injection rate (new learnings created)
  δ = decay rate (0.17/week)
  σ = retrieval effectiveness (what fraction of relevant learnings are surfaced)
  ρ = citation rate (citations per learning per week)

Escape Velocity: σ·ρ > δ → knowledge compounds
```

### 11.2 What attest Adopts (Simplified Go Implementation)

We adopt the core loop but implement it in Go without the full MemRL machinery.

#### 11.2.1 Citation Tracking

Every time a learning is included in a `ContextBundle` or injected into a council reviewer prompt, `RecordCitation` is called. This is already implemented in Phase 1.

**Enhancement:** Add a `Source` field to citation events to distinguish retrieval contexts:

| Citation source | When |
|---|---|
| `compile_enrichment` | Task enrichment during `Engine.Compile` |
| `context_assembly` | `AssembleContext` for worker dispatch |
| `council_context` | Council reviewer prior findings injection |

#### 11.2.2 Maturity Levels

Add a `Maturity` field to the Learning type:

```go
type Maturity string

const (
    MaturityProvisional Maturity = "provisional" // New, unvalidated
    MaturityCandidate   Maturity = "candidate"   // Cited 3+ times, utility ≥ 0.55
    MaturityEstablished Maturity = "established"  // Cited 5+ times, consistently helpful
    MaturityAntiPattern Maturity = "anti_pattern" // Demoted, harmful
)
```

**Promotion rules (simplified from AgentOps):**

| Transition | Condition |
|---|---|
| provisional → candidate | `CitedCount >= 3 AND Utility >= 0.55` |
| candidate → established | `CitedCount >= 5 AND Utility >= 0.55` |
| candidate → provisional | `Utility < 0.3` (demotion) |
| any → anti_pattern | `Utility <= 0.2` (after decay or explicit downvote) |

**Maturity affects query scoring:**

| Maturity | Weight |
|---|---|
| established | 1.5× |
| candidate | 1.2× |
| provisional | 1.0× |
| anti_pattern | 0.3× |

Maturity transitions are checked during `Maintain` (see §11.3).

#### 11.2.3 Finding Compilation

The most valuable AgentOps pattern. Learnings with high maturity (candidate or established) and category `anti_pattern` or `tooling` are compiled into prevention checks.

**Storage layout extension:**

```
.attest/learnings/
    index.jsonl
    tags.json
    handoffs/
    prevention/
        planning/          # Injected during execution plan review
            <learning-id>.md
        review/            # Injected during council tech-spec review
            <learning-id>.md
```

**Prevention check format:**

```markdown
---
id: lrn-abc123
source_learning: lrn-abc123
compiled_at: 2026-03-21T10:00:00Z
applicable_paths:
  - internal/engine
  - internal/state
---
# Prevention: Concurrent state file access must use flock

- **Pattern:** State file read-modify-write without flock causes data loss under concurrent workers
- **Ask:** Does the current plan/spec account for concurrent access to state files?
- **Do:** Verify all state file mutations use `withLock` pattern from `internal/ticket/claim.go`
```

**Compilation trigger:** After `GarbageCollect` runs maturity transitions, any learning promoted to `candidate` or `established` with category `anti_pattern` or `tooling` gets compiled into a prevention check. Demotion or expiry removes the check.

**Injection points:**

| Pipeline stage | Which prevention checks | How injected |
|---|---|---|
| `attest tech-spec review` | `prevention/review/*.md` matching spec paths | Appended to reviewer prompt as "Prior findings" |
| `attest plan review` | `prevention/planning/*.md` matching plan paths | Appended to reviewer prompt as "Planning rules" |
| `attest context` | All matching by tags/paths | Included in ContextBundle |

#### 11.2.4 Quality Gate for Injection

Not all learnings are worth injecting. Quality gate (applied during query):

```
maturity ∈ {provisional, candidate, established}  // not anti_pattern or expired
AND utility > 0.3
AND NOT superseded
AND NOT expired
```

Anti-pattern learnings are still queryable (for display, GC) but excluded from injection into task enrichment and worker context. They remain visible in `attest learn list` and `attest learn query`.

### 11.3 The Librarian: Automated Knowledge Maintenance

Without maintenance, the learning store degrades: duplicates pile up from repeated reviews, contradictory learnings coexist as the codebase evolves, stale learnings waste injection tokens, and prevention checks reference expired learnings. The librarian is a background maintenance process that keeps the knowledge base healthy.

#### Prior art

**memU** uses a dedicated "Memory Agent" that continuously consolidates files — a persistent librarian process. **AgentOps** distributes the same work across its session-end hook: `ao dedup --merge`, `ao contradict`, `ao maturity --expire --archive`, `ao maturity --evict --archive`, and `finding-compiler.sh` all run automatically after every session (~30 seconds total).

attest adopts the AgentOps approach but triggers maintenance differently (no sessions to hook into).

#### Maintenance tasks

`Store.Maintain()` runs the following tasks in order:

```
attest learn maintain
  ├─ 1. Dedup: merge learnings with overlapping content
  ├─ 2. Contradiction scan: flag conflicting learnings
  ├─ 3. Maturity transitions: promote/demote based on utility + citations
  ├─ 4. Prevention refresh: recompile prevention checks from mature learnings
  ├─ 5. Staleness scan: flag uncited learnings older than 30 days
  ├─ 6. Index rebuild: regenerate tags.json with current keywords
  └─ 7. GC: remove expired/superseded learnings older than 90 days
```

**Task details:**

| Task | Logic | Output |
|---|---|---|
| **Dedup** | For each pair of learnings with overlapping tags (≥2 shared tags) AND similar summaries (Levenshtein distance < 0.2 of shorter string length), merge: keep the one with higher utility, set `SupersededBy` on the other. | Count of merged learnings |
| **Contradiction scan** | Find learnings where one is `anti_pattern` and another is `pattern` with ≥2 shared tags. Flag as contradictions in maintenance output. Don't auto-resolve — contradictions may be intentional (approach X is bad in context A but good in context B). | List of contradictions for human review |
| **Maturity transitions** | Apply promotion/demotion rules from §11.2.2. Check every learning against thresholds. | Count of promotions/demotions |
| **Prevention refresh** | For each learning at `candidate` or `established` maturity with category `anti_pattern` or `tooling`: compile to prevention check. For demoted/expired learnings: remove their prevention checks. | Count of compiled/removed checks |
| **Staleness scan** | Flag learnings with `CitedCount == 0` and age > 30 days. These are candidates for future GC — not removed yet, but their utility is set to `max(utility - 0.1, 0)` to push them below injection thresholds. | Count of stale learnings |
| **Index rebuild** | Full rebuild of `tags.json` from current `index.jsonl`. Catches any drift from manual edits or interrupted writes. | — |
| **GC** | Remove learnings where `Expired == true` or `SupersededBy != ""` and age > 90 days. Remove orphaned prevention checks. | Count of removed learnings |

#### When it runs

Maintenance runs as a **non-blocking background process** — it never blocks the caller.

| Trigger | How | Blocking? |
|---|---|---|
| **After run completion** | Engine calls `go store.Maintain()` when run state transitions to `completed` | No — goroutine |
| **During Query()** | If `lastMaintainedAt` is > 24h ago, spawn `go store.Maintain()` before returning results | No — goroutine, current query uses pre-maintenance data |
| **Manual** | `attest learn maintain` — runs synchronously, prints report | Yes — user explicitly asked |

**Implementation:**

```go
// On Store:
type Store struct {
    // ... existing fields ...
    lastMaintainedAt time.Time   // in-memory, not persisted
    maintaining      atomic.Bool // prevents concurrent maintenance
}

func (s *Store) Maintain() MaintainReport {
    if !s.maintaining.CompareAndSwap(false, true) {
        return MaintainReport{Skipped: true} // already running
    }
    defer s.maintaining.Store(false)

    // ... run all tasks under flock ...
    s.lastMaintainedAt = s.now()
    return report
}

func (s *Store) MaintainIfStale() {
    if s.now().Sub(s.lastMaintainedAt) > 24*time.Hour {
        go s.Maintain() // non-blocking
    }
}

// Called from Query():
func (s *Store) Query(opts QueryOpts) ([]Learning, error) {
    s.MaintainIfStale()
    // ... existing query logic ...
}
```

**`atomic.Bool` prevents concurrent maintenance** — if two queries trigger `MaintainIfStale` simultaneously, only the first runs. The second returns immediately.

**Flock protects the store** — `Maintain()` acquires the exclusive flock for the entire maintenance run (same lock as `Add`, `RecordCitation`, etc.). This means `Add()` calls during maintenance will block briefly, but maintenance tasks are fast (sub-second for < 10K learnings).

#### Maintenance report

`attest learn maintain` prints a human-readable report:

```
Learning store maintenance:
  Dedup:          2 learnings merged (lrn-abc → lrn-def, lrn-ghi → lrn-jkl)
  Contradictions: 1 found (lrn-mno ↔ lrn-pqr, tags: [compiler, grouping])
  Maturity:       3 promoted (provisional→candidate), 1 demoted (candidate→provisional)
  Prevention:     2 checks compiled, 1 removed
  Stale:          4 learnings flagged (0 citations, >30 days)
  Index:          rebuilt (47 tags, 23 keywords)
  GC:             1 expired learning removed

Store health: 42 active learnings, 3 candidate, 1 established
Last maintained: 2026-03-21T10:00:00Z
```

Background runs (from Query/run completion) invoke an optional `OnMaintain func(MaintainReport)` callback on the `Store` struct if set. The CLI/engine layer sets this callback at construction time to log the report (e.g., via `AppendEvent` or structured logging). If the callback is nil, the report is silently discarded. This preserves the import boundary: `internal/learning` never imports `internal/state`'s event system.

### 11.4 What attest Skips

| AgentOps Feature | Why Skipped |
|---|---|
| MemRL with annealed learning rate | Over-engineered for our scale. Simple EMA with fixed α = 0.3 is sufficient. |
| 5-tier system (observation → core) | Too many tiers. 4 maturity levels cover the same ground. |
| Session transcript forging | attest is a CLI tool, not a conversational agent. No transcripts to mine. |
| `ao mine` (git/code pattern extraction) | Requires LLM inference. attest extractions are deterministic. |
| Global learnings (`~/.agents/learnings/`) | attest is project-scoped. Cross-project knowledge is out of scope. |
| Smart Connections (Obsidian API) | External dependency. Tag + keyword search is sufficient. |
| Constraint compilation with detector scripts | Over-engineered. Prevention checks are advisory (reviewer context), not mechanical detectors. |
| Session start/end hooks | Maintenance is triggered by run completion and lazy query checks, not session boundaries. |
| Flywheel equation metrics | Useful for monitoring at scale, not needed for a single-project tool. |

### 11.5 Implementation Roadmap

| Order | What | Depends on | Est. lines |
|---|---|---|---|
| 1 | `Maturity` field on Learning + transition logic | Phase 1 | +40 |
| 2 | Maturity-weighted scoring in `Query` | Step 1 | +15 |
| 3 | `Store.Maintain()` — dedup, contradictions, maturity transitions, staleness, GC | Steps 1-2 | +150 |
| 4 | `MaintainIfStale()` in `Query()` + goroutine trigger after run completion | Step 3 | +20 |
| 5 | `attest learn maintain` CLI command | Step 3 | +25 |
| 6 | Prevention check compilation (`Store.CompilePreventionChecks`) | Step 3 | +80 |
| 7 | Prevention loading in `cmdTechSpecReview` and `cmdPlan` | Step 6, Phase 2 | +30 |
| 8 | Citation source tracking (enhancement to `RecordCitation`) | Phase 1 | +10 |

**Estimated total:** ~370 lines.

### 11.6 The Full Loop (End State)

After Phases 1, 2, and 3 are complete:

```
1. Run starts → compile tasks with learning enrichment
   ├─ Tasks get Warnings (anti_pattern) and Constraints (codebase/tooling)
   ├─ Citations recorded for each injected learning
   └─ MaintainIfStale() triggered (non-blocking)

2. Tech-spec review → council reviewers see prior findings
   ├─ Prevention checks from mature learnings loaded as context
   └─ Reviewers can confirm/reject/refine prior findings

3. Council findings → extracted as new learnings
   ├─ Blocking findings → anti_pattern (confidence 0.8)
   ├─ Judge rejections → pattern (confidence 0.7)
   └─ Tags derived from affected paths + categories

4. Verification failures → extracted as learnings
   ├─ Deterministic findings → anti_pattern (confidence 0.9)
   └─ Dedup by DedupeKey, boost confidence on repeat

5. Learnings accumulate citations from steps 1-2
   ├─ Utility updated via EMA on each citation
   └─ Maturity progresses: provisional → candidate → established

6. Maintain runs (after run completion + lazy on query)
   ├─ Dedup merges near-duplicate learnings
   ├─ Contradiction scan flags conflicting knowledge
   ├─ Maturity transitions promote/demote based on utility
   ├─ Promoted learnings compiled to prevention checks
   ├─ Stale learnings pushed below injection thresholds
   ├─ Expired/superseded learnings garbage collected
   └─ Prevention checks for demoted learnings removed

7. Next run benefits from ALL prior learnings
   └─ The loop closes: findings → learnings → prevention → better reviews → fewer findings
```

**The key insight from AgentOps:** The flywheel doesn't just store knowledge — it measures which knowledge is actually useful (via citations) and promotes the valuable pieces to more prominent positions (via maturity). Without the librarian, the store becomes a graveyard of low-value observations. With it, knowledge compounds.

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
