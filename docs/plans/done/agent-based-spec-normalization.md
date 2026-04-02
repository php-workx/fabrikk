# Fix: Replace mechanical spec normalization with agent-based transformation

**Date:** 2026-03-19 (draft), 2026-03-23 (revised), 2026-03-24 (implemented)
**Status:** Implemented

## Context

When `fabrikk tech-spec review --from <path>` receives a document that doesn't match the canonical template headings, `normalizeTechnicalSpec()` attempts to mechanically reformat it using keyword matching. This is lossy — it stripped a 260-line plan down to 9 lines because:

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
          → validate output (length check, heading check)
          → if agent unavailable, fails, or output is suspiciously short → pass through unchanged (safe fallback)
          → print warning: "Document doesn't match canonical headings — agent normalization applied"
```

## Tasks

### 0. Immediate safety fix (Phase 0)

**Ship first, before any other task.** Make the mechanical normalization a no-op for non-canonical docs to stop the content-loss bug immediately.

Change `normalizeTechnicalSpec()` to:

```go
func normalizeTechnicalSpec(data []byte) []byte {
    lower := strings.ToLower(string(data))
    if hasCanonicalTechnicalSpecHeadings(lower) {
        return data // already canonical
    }
    return data // pass through unchanged — don't attempt lossy rewrite
}
```

This is a one-line behavioral change. The mechanical rewrite path is dead code after this.

**Implementation note:** The linter (`golangci-lint`) flags unreachable code as errors, so the 152 lines of dead mechanical normalization functions were deleted in the same commit as Phase 0, absorbing Task 4.

**Files:**
- `internal/engine/techspec.go` (modify `normalizeTechnicalSpec`)

**Tests:**
- Existing `TestDraftTechnicalSpecNormalizesLegacyHeadings` will need updating — the test currently asserts canonical headings appear after normalization, but now the legacy doc passes through unchanged. Update the test to verify content preservation instead.

### 1. Extract `internal/agentcli/` package

Create `internal/agentcli/agentcli.go` by extracting from `internal/councilflow/runner.go`:

- `CLIBackend` struct (command, args, prompt flag)
- `KnownBackends` map (claude, codex, gemini with their configs)
- `ResolveCommand()` — PATH + fallback resolution
- `InvokeFn` type and `InvokeFunc` package-level var
- `Invoke(ctx, backend, prompt, timeoutSec)` — subprocess execution with stdin fallback for large prompts
- `ExtractFromCodeFence(s string) string` — exported version of `extractFromCodeFence` from `runner.go:431` (pm-002: currently unexported, needed by `normalizeWithAgent` in Task 2)

Then update `internal/councilflow/runner.go` to import from `agentcli` instead of owning this logic. This establishes CLI agent invocation as a shared capability for any package that needs it.

**Build discipline (pm-003):** This task touches 9 councilflow files. Run `go build ./internal/agentcli/ ./internal/councilflow/` after each file update — do not batch. Partial extraction leaves the repo unbuildable.

**Files:**
- `internal/agentcli/agentcli.go` (new)
- `internal/agentcli/agentcli_test.go` (new — test command resolution, stdin fallback threshold)
- `internal/councilflow/runner.go` (refactor to use agentcli)

### 2. Add `normalizeWithAgent()` to engine

New function in `internal/engine/techspec.go`:

```go
func normalizeWithAgent(ctx context.Context, data []byte) ([]byte, error)
```

**Prompt construction:**
- Include the 8 canonical headings with descriptions (from template section below)
- Include the full source document
- Critical instructions (must be prominent in the prompt):
  - "Preserve ALL content from the source document. Do not summarize, abbreviate, or omit any information."
  - "Map source content to the closest matching canonical section. If content doesn't clearly fit any section, place it under `## Additional Content` rather than dropping it."
  - "Do not invent content that is not in the source document."
  - "Preserve code blocks, tables, lists, and formatting from the source."
  - "If the source has sections beyond the 8 canonical ones, include them after the canonical sections."

**Invocation:**
- Use `agentcli.Invoke()` with claude backend, 120s timeout
- Strip markdown code fences from output if present (model may wrap response in ```markdown ... ```)

**Output validation (drift check):**
- Output must be at least 50% of input byte length. If shorter, reject and fall back to original. This catches truncation from model errors or context overflow.
- Output must contain at least 4 of the 8 canonical headings. If fewer, reject and fall back. This catches garbled output.
- Log a warning if validation fails: "Agent normalization produced suspiciously short output (%d bytes vs %d input bytes) — using original document"

**Files:**
- `internal/engine/techspec.go` (add `normalizeWithAgent`)

### 3. Update `DraftTechnicalSpec` to use agent normalization

Replace the current `normalizeTechnicalSpec(data)` call with:

```go
if hasCanonicalTechnicalSpecHeadings(strings.ToLower(string(data))) {
    // Already canonical — pass through
} else {
    fmt.Println("Document doesn't match canonical headings — attempting agent normalization ...")
    if normalized, err := normalizeWithAgent(ctx, data); err == nil {
        data = normalized
    } else {
        fmt.Printf("Agent normalization unavailable (%v) — using original document\n", err)
    }
}
// Agent failure → original data passes through unchanged
```

Note: `DraftTechnicalSpec` currently ignores `ctx` (`_ = ctx`). The agent-based path needs `ctx` for timeout/cancellation — remove the `_ = ctx` line.

**`--no-normalize` flag:** Add a `--no-normalize` flag to `fabrikk tech-spec draft` that skips both the canonical-heading check and agent normalization, passing the document through as-is. This is useful when the user knows the document isn't a technical spec (e.g., a plan document, a design doc) and wants to skip the reformatting attempt entirely.

**All 3 `DraftTechnicalSpec` call sites must be updated (pm-001):**
1. `cmd/fabrikk/main.go` line 283: `eng.DraftTechnicalSpec(ctx, fromPath, noNormalize)` — in `cmdTechSpec` draft action
2. `cmd/fabrikk/main.go` line 317: `eng.DraftTechnicalSpec(ctx, fromPath, noNormalize)` — in `cmdTechSpecReviewFromFile` first call
3. `cmd/fabrikk/main.go` line 350: `eng.DraftTechnicalSpec(ctx, fromPath, noNormalize)` — in `cmdTechSpecReviewFromFile` second call

Missing any of these causes a compile error. All 3 must be updated in the same commit.

**Files:**
- `internal/engine/techspec.go` (update `DraftTechnicalSpec`)
- `cmd/fabrikk/main.go` (add `--no-normalize` flag parsing, update all 3 call sites)

### 4. Remove mechanical normalization functions — Absorbed into Phase 0

Linter required dead code deletion in the same commit as Phase 0. All 7 functions/types (152 lines) deleted alongside the no-op change. `hasCanonicalTechnicalSpecHeadings()` kept.

### 5. Update tests

**Remove:**
- `TestDraftTechnicalSpecNormalizesLegacyHeadings` — tests the deleted mechanical normalization

**Add:**
- **Canonical pass-through:** Document with all 8 canonical headings passes through byte-for-byte unchanged.
- **Agent fallback on error:** Mock `agentcli.InvokeFunc` to return an error → verify original document passes through unchanged, no content loss.
- **Agent fallback on short output:** Mock agent returning a suspiciously short response (< 50% of input) → verify original document passes through, warning logged.
- **Agent fallback on garbled output:** Mock agent returning text with fewer than 4 canonical headings → verify original document passes through.
- **Agent success:** Mock agent returning a well-formed normalized spec → verify output is used, contains canonical headings, preserves content.
- **`--no-normalize` flag:** Pass `--no-normalize` → verify document passes through unchanged regardless of headings.
- **Integration test:** `fabrikk tech-spec draft <run-id> --from <free-form-doc>` with a real document (no mock) → verify output contains all original content. (Only runs when CLI tools are available; skip in CI.)

**Mock cleanup (pm-004):** Every test that stubs `agentcli.InvokeFunc` MUST restore the original via `t.Cleanup()`:
```go
original := agentcli.InvokeFunc
agentcli.InvokeFunc = mockFn
t.Cleanup(func() { agentcli.InvokeFunc = original })
```
Without cleanup, parallel tests (`-race`) race on the shared package-level var.

**Files:**
- `internal/engine/engine_test.go` (update/add tests)
- `cmd/fabrikk/commands_test.go` (add `--no-normalize` test)

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

| File | Task | Changes |
|---|---|---|
| `internal/engine/techspec.go` | 0 | Make `normalizeTechnicalSpec` a no-op for non-canonical docs |
| `internal/agentcli/agentcli.go` | 1 | New: backend types, resolution, invocation |
| `internal/agentcli/agentcli_test.go` | 1 | New: command resolution tests |
| `internal/councilflow/runner.go` | 1 | Refactor: import agentcli, remove duplicated logic |
| `internal/engine/techspec.go` | 2, 3, 4 | Add `normalizeWithAgent`, update `DraftTechnicalSpec`, delete mechanical code |
| `internal/engine/engine_test.go` | 5 | Update/add normalization tests |
| `cmd/fabrikk/main.go` | 3 | Add `--no-normalize` flag |
| `cmd/fabrikk/commands_test.go` | 5 | Add `--no-normalize` test |

## Implementation order

```
Phase 0: Safety fix (Task 0) — ship immediately, one-line change
    ↓
Task 1: Extract agentcli package
    ↓
Task 2: normalizeWithAgent + output validation
    ↓
Task 3: Wire into DraftTechnicalSpec + --no-normalize flag
    ↓
Task 4: Delete mechanical normalization dead code
    ↓
Task 5: Update tests
```

Phase 0 can ship on its own branch/commit. Tasks 1-5 can be one branch.

## Pre-mortem findings (2026-03-23)

7 findings from council review. All resolved in this revision.

| ID | Severity | Finding | Resolution |
|----|----------|---------|------------|
| pm-001 | significant | `DraftTechnicalSpec` signature change breaks 3 callers | All 3 call sites in main.go explicitly listed in Task 3 |
| pm-002 | significant | `extractFromCodeFence` is unexported from councilflow | Extracted to `agentcli.ExtractFromCodeFence` in Task 1 |
| pm-003 | significant | Task 1 blast radius across 9 councilflow files | Added build-after-each-file discipline to Task 1 |
| pm-004 | moderate | `InvokeFunc` mock tests need cleanup to avoid race | All tests require `t.Cleanup()` pattern in Task 5 |
| pm-005 | moderate | Agent prompt quality only validated manually | Accepted; prompt verified via manual smoke test |
| pm-006 | moderate | Phase 0 test update assertions underspecified | Explicit assertions added to Task 0 |
| pm-007 | low | Unused imports after deletion | Added `goimports` step to Task 4 |

## Verification

```bash
# After Phase 0:
just pre-commit
fabrikk tech-spec draft <run-id> --from docs/plans/councilflow-to-specreview-rename.md
# → verify technical spec contains all original content, not a 9-line stub

# After Tasks 1-5:
just pre-commit
fabrikk tech-spec draft <run-id> --from docs/plans/councilflow-to-specreview-rename.md
# → verify technical spec has canonical headings AND all original content

fabrikk tech-spec draft <run-id> --from docs/plans/councilflow-to-specreview-rename.md --no-normalize
# → verify document passes through as-is

fabrikk tech-spec draft <run-id> --from docs/specs/attest-technical-spec.md
# → verify canonical doc passes through unchanged (fast path)
```
