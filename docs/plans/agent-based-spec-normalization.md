# Fix: Replace mechanical spec normalization with agent-based transformation

**Date:** 2026-03-19
**Status:** Draft

## Context

When `attest tech-spec review --from <path>` receives a document that doesn't match the canonical template headings, `normalizeTechnicalSpec()` attempts to mechanically reformat it using keyword matching. This is lossy — it stripped a 260-line plan down to 9 lines because:

1. `hasLegacyTechnicalSpecHeadings()` matched on `"status: draft"` (from `**Status:** Draft`), triggering the rewrite path
2. `normalizedSectionContent()` looked for sections matching exact heading aliases — found almost none
3. The fallback `matchingLine()` grabbed at most one line per required section
4. Everything else was discarded

The mechanical approach can't understand document structure or intent. An agent can read any spec format and intelligently map content to the 8-section template, preserving all information.

## Approach

Replace `normalizeTechnicalSpec()` with an agent-based transformation when the document doesn't already have canonical headings. Keep the fast path (canonical headings present → pass through unchanged).

### Decision tree in `DraftTechnicalSpec`:

```
Document has canonical headings (## 1. Technical context, etc.)?
  YES → pass through unchanged (current fast path, no change)
  NO  → invoke agent to reformat into template
          → if agent unavailable or fails, pass through unchanged (safe fallback)
```

## Tasks

### 1. Extract `internal/agentcli/` package

Create `internal/agentcli/agentcli.go` by extracting from `internal/councilflow/runner.go`:

- `CLIBackend` struct (command, args, prompt flag)
- `KnownBackends` map (claude, codex, gemini with their configs)
- `ResolveCommand()` — PATH + fallback resolution
- `Invoke(ctx, backend, prompt, timeoutSec)` — subprocess execution with stdin fallback for large prompts

Then update `internal/councilflow/runner.go` to import from `agentcli` instead of owning this logic.

**Files:**
- `internal/agentcli/agentcli.go` (new)
- `internal/councilflow/runner.go` (refactor to use agentcli)

### 2. Add `normalizeWithAgent()` to engine

New function in `internal/engine/techspec.go`:

```go
func normalizeWithAgent(ctx context.Context, data []byte) ([]byte, error)
```

- Build prompt with the 8 canonical headings + descriptions + source document
- Instructions: preserve ALL content, map to sections, don't summarize or invent
- Invoke via `agentcli.Invoke()` with claude/opus, 120s timeout
- Strip markdown code fences from output if present
- Return reformatted spec bytes

### 3. Update `DraftTechnicalSpec` to use agent normalization

Replace the current `normalizeTechnicalSpec(data)` call with:

```go
if hasCanonicalTechnicalSpecHeadings(strings.ToLower(string(data))) {
    // Already canonical — pass through
} else if normalized, err := normalizeWithAgent(ctx, data); err == nil {
    data = normalized
}
// Agent failure → original data passes through unchanged
```

### 4. Remove mechanical normalization functions

Delete from `internal/engine/techspec.go`:
- `normalizeTechnicalSpec()` (line 274)
- `hasLegacyTechnicalSpecHeadings()` (line 317)
- `parseTechnicalSpecMarkdown()` (line 328)
- `normalizedSectionContent()` (line 365)
- `matchingLine()` (line 408)
- `stripSectionNumber()` (line 400)

Keep: `hasCanonicalTechnicalSpecHeadings()` (line 308) — still needed for fast-path check.

### 5. Update tests

- Remove tests for deleted mechanical normalization functions
- Add test: canonical-heading docs pass through unchanged
- Add test: agent failure falls back to original content (mock agent returning error)
- Integration test: `attest tech-spec review --from` with a free-form document produces a full spec

## Template content (hardcoded in prompt)

```
## 1. Technical context — Language, runtime, dependencies, constraints
## 2. Architecture — Components, data flow, concurrency model
## 3. Canonical artifacts and schemas — File paths, producers, consumers, schemas
## 4. Interfaces — CLI/API surface, state machines, transitions
## 5. Verification — Quality gates, acceptance scenarios, evidence requirements
## 6. Requirement traceability — Requirement IDs mapped to technical mechanisms
## 7. Open questions and risks — Unresolved decisions and their impact
## 8. Approval — Draft/review/approval status
```

## What stays the same

- `hasCanonicalTechnicalSpecHeadings()` — fast-path detection
- `technicalSpecRequirements` — structural validation in `ReviewTechnicalSpec`
- The structural review step — runs after normalization to verify agent output
- The council/spec review pipeline — unchanged
- Fallback — agent unavailable or fails → original document passes through

## Files modified

| File | Changes |
|---|---|
| `internal/agentcli/agentcli.go` | New: backend types, resolution, invocation |
| `internal/councilflow/runner.go` | Refactor: import agentcli, remove duplicated logic |
| `internal/engine/techspec.go` | Replace normalization, update DraftTechnicalSpec, delete dead code |
| `internal/engine/techspec_test.go` | Update tests for new normalization behavior |

## Verification

```bash
just pre-commit
attest tech-spec review --from docs/plans/councilflow-to-specreview-rename.md --mode mvp --skip-approval
# → verify technical spec contains all original content, not a 9-line stub
```
