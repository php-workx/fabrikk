# AGENTS.md

`AGENTS.md` is the durable instruction file for this repo. Do not use it for
session memory or scratch notes.

## What This Repo Is

`fabrikk` is a spec-driven autonomous run system for coding agents. The key
product promise is not just “generate code,” but “finish work that traces back
to approved requirements, passes deterministic verification, and survives
independent review.”

## Hard Rules

- The approved run artifact is the execution contract. Do not quietly drift from
  it.
- The pipeline skeleton is coded in Go. Do not invent ad hoc stage skipping or
  alternate flows inside the instruction layer.
- `.tickets/` is the task backend. `.fabrikk/runs/<run-id>/` is durable run
  state. Do not casually hand-edit generated run artifacts unless the task is
  explicitly about migrations or debugging that format.
- Use the supported local gates (`just ...`) instead of one-off command mixes.
- This repo uses `tk` for ticket work. If you need task commands, start with
  `tk help`.

## Common Commands

```bash
just pre-commit
just pre-push
just check
just test
just test-race
just format
just build
```

Useful run flow:

```bash
./bin/fabrikk prepare --spec docs/specs/my-feature-spec.md
./bin/fabrikk review <run-id>
./bin/fabrikk artifact approve <run-id>
./bin/fabrikk status <run-id>
./bin/fabrikk next <run-id>
```

## Common Workflows

### 1. Pipeline or engine changes

1. Start from the current pipeline contract, not just the code shape.
2. Run at least `just pre-commit`; use `just pre-push` when changing execution,
   verification, or review flow.
3. If the change affects staged behavior, check `README.md` and
   `docs/HOW-IT-WORKS.md` for drift.

### 2. Ticket backend or run-state changes

1. Treat `.tickets/` and `.fabrikk/runs/` formats as product surfaces.
2. Preserve compatibility deliberately; do not change serialization casually.
3. Validate with the repo’s normal quality gates before stopping.

### 3. Spec-driven execution changes

1. Keep the approved artifact as the anchor.
2. Preserve deterministic checks where the design says “no LLM here.”
3. If the task changes execution semantics, update the relevant reference docs.

## Repo Facts

- Task persistence is file-backed via `.tickets/`.
- Run state is file-backed via `.fabrikk/runs/<run-id>/`.
- The compiler is deterministic.
- Council review and verifier stages are first-class, not optional cleanup.

## References

- `README.md` — current product framing and pipeline summary
- `docs/HOW-IT-WORKS.md` — detailed pipeline walkthrough


<claude-mem-context>
# Memory Context

# [fabrikk] recent context, 2026-04-27 7:02am GMT+2

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision 🚨security_alert 🔐security_note
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 50 obs (17,403t read) | 588,645t work | 97% savings

### Apr 25, 2026
1590 12:53p 🟣 llmcli: NewBackendByName tests added to select_test.go
1592 12:54p 🟣 llmclient/llmcli Execution-Control API: Full implementation diff summary
1593 " 🔵 Two test failures in llmcli after execution-control implementation
1594 " 🔴 textstream.go: EventDone with StopCancelled was swallowed when ctx already cancelled
1595 " 🔴 subprocess_test.go: macOS symlink cwd mismatch fixed with filepath.EvalSymlinks
1596 12:55p 🟣 llmclient/llmcli execution-control API: all tests pass
1597 " ⚖️ llmclient/llmcli Execution-Control API: Full Plan Scoped for Fresh-Context Implementation
1599 12:56p ⚖️ llmclient/llmcli Execution-Control API: Full Implementation Plan Scoped
1600 " 🟣 llmclient/llmcli Execution-Control API: All Tests Green
1601 12:57p 🔴 llmcli pre-commit blocked: three more gocritic hugeParam violations in claude.go
1602 " 🔵 gocritic hugeParam scope: 30 functions across 9 files accept RequestConfig by value
1603 " 🔴 Bulk nolint:gocritic annotations added across all llmcli backend files for RequestConfig by-value helpers
1604 " 🔴 Second wave of nolint:gocritic annotations applied to omp.go, omp_rpc.go, opencode_http.go, opencode_run.go, metrics.go
1608 12:58p 🔴 llmclient/llmcli tests green again after completing all gocritic nolint annotations
1609 " 🔴 One remaining gocritic hugeParam violation in opencode_run.go at line 135 — multi-line function signature
1610 " 🔴 writeTempOpenCodeConfig nolint placed on closing paren line of multi-line signature
1611 " 🔴 gocritic hugeParam on multi-line signatures requires doc-comment nolint before func, not inline on closing paren
1612 12:59p 🔴 just pre-commit passes: golangci-lint reports 0 issues after doc-comment nolint fix
1637 1:32p ⚖️ llmclient/llmcli Execution-Control API: Full Plan Scoped
1638 1:33p 🔵 Verk Runtime Adapter Architecture: Full Duplication Mapped Before Migration
1639 " 🔵 Verk go.mod Has No fabrikk Dependency Yet — Migration Has Not Started
1640 " 🔵 fabrikk llmcli/llmclient Has No Execution-Control Options Yet — Starting From Zero
1646 1:35p 🔵 fabrikk Execution-Control API Already Fully Implemented — Plan Is Already Done
1647 " 🔵 fabrikk llmcli Environment Resolution: 3-Layer Precedence with Deterministic Key Ordering
1648 " 🔵 fabrikk Codex Backend: WorkingDirectory Uses -C Flag; System Prompt Injected Into Prompt When CWD Set
1649 " 🔵 fabrikk llmcli subprocess.go: RawCapture Wired via stdoutReader/stderrWriter Helpers
1664 1:43p ✅ Verk llmcli Migration Plan Rewritten With 5 Integration Findings
1666 " 🔵 codex exec --json Flag Confirmed: Enables JSONL Event Stream Output
1667 " 🟣 fabrikk: WithCodexJSONL Option Added via TDD — Failing Test Written First
1668 1:46p 🟣 LLM CLI Execution-Control API: WithCodexJSONL TDD Setup
1669 " ⚖️ Verk Migration Plan Rewritten to Reflect Implementation Reality
1670 " 🔵 codex exec --json Flag Confirmed — Enables JSONL Telemetry for Verk Bridge
1671 1:47p 🟣 codex-exec JSONL Mode: WithCodexJSONL Option + streamCodexJSONLProcess Implemented
1672 " 🔴 gocritic rangeValCopy Lint Violation Fixed in codex_jsonl.go
1673 " ✅ Verk Migration Plan Task 3 Updated: Finding 3 Closed as Implemented
1675 1:48p 🟣 fabrikk LLM CLI Execution-Control API: Full Pre-Commit Gate Passed
1680 2:29p 🔵 fabrikk Repo Pre-Commit State: llmcli/llmclient Execution-Control Changes
1682 2:30p 🟣 fabrikk llmcli/llmclient Execution-Control API Committed
1683 " 🔵 fabrikk Pre-Commit Gate Passes Clean for Execution-Control Commit
1686 2:31p 🟣 fabrikk Execution-Control Commit Landed on feat/spec-normalization (f6ae11b)
1687 " ✅ fabrikk Verk Migration Plan Committed as Separate Docs Commit
1691 2:32p ✅ fabrikk Verk Migration Plan Commit Landed (dc92e71)
1692 " 🔵 fabrikk feat/spec-normalization: Two Remaining Uncommitted Files After Grouped Commits
1860 4:15p 🔵 LLM CLI Implementation Accidentally Committed to Spec Normalization Branch
1861 " ⚖️ feat/llmcli-backends Branch Created from feat/spec-normalization to Separate Concerns
1865 4:17p 🔵 fabrikk feat/spec-normalization Branch Commit Map: Engine vs llmcli Separation
1866 " 🔵 fabrikk Full Test Suite Passes on feat/llmcli-backends — Package Structure Confirmed
1868 4:19p 🔵 fabrikk Race Detector Tests and govulncheck Pass Clean on feat/llmcli-backends
1874 4:21p ✅ feat/spec-normalization Branch Retroactively Split — llmcli Commits Stripped via Force-Reset
1884 4:24p ✅ fabrikk Branch Split Complete and Verified — Final State Confirmed

Access 589k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>