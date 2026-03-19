# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

This project uses a CLI ticket system for task management. Run `tk help` when you need to use it.

## What is attest

A spec-driven autonomous run system for coding agents. Implementation is only complete when it traces back to approved spec requirements, passes deterministic verification, and clears independent multi-model council review. Written in Go with zero external dependencies — stdlib only, single binary.

## Commands

```bash
just setup          # Configure git hooks + install dev tools (first time)
just pre-commit     # Local checks + tests (~30s) — hooks run this automatically
just check          # Full quality gate: pre-commit + vuln scan (~45s)
just test           # All tests with race detector
just format         # Auto-fix formatting (gofumpt)
just build          # Compile to bin/attest with version info
just cover          # Tests with coverage → coverage.html

# Single test
go test -race -count=1 ./internal/compiler/...
go test -race -count=1 -run TestCompile ./internal/compiler/...
```

## Architecture

```
CLI (cmd/attest/)
  ├─ Engine (internal/engine/)      — Run lifecycle orchestration
  │   ├─ Compiler (internal/compiler/) — Spec → task graph (deterministic, no LLM)
  │   ├─ Councilflow (internal/councilflow/) — Multi-model review pipeline
  │   └─ Verifier (internal/verifier/) — Evidence-based verification
  └─ State (internal/state/)        — File-based types, RunDir I/O, atomic writes
```

**State lives on disk:** All run state is in `.attest/runs/<run-id>/` as JSON/JSONL files. `internal/state` provides `RunDir` for managing this directory and atomic file writes for concurrent safety.

**Compiler is deterministic:** Requirements are grouped by proximity, assigned to lanes, and given stable IDs/ETags. No LLM involvement — purely rule-based.

**Councilflow:** Parallel fan-out to persona reviewers (systems-architect, spec-conformance, agent-workflow, orchestrator) across multiple rounds. A Judge synthesizes findings with opinionated dismissal rationale. Review modes: MVP, standard, production.

**Verifier pipeline order:** Quality gate → evidence checks → scope check → requirement linkage. Each step can produce blocking findings.

## Key types (internal/state/types.go)

- `RunArtifact` — Approved normalized contract (requirements, clarifications, risk profile)
- `Task` — Work item with status, scope (owned/readonly/shared paths), dependencies, requirements
- `RunStatus` — Current state summary (active wave, task counts, blockers)
- `CompletionReport` / `VerifierResult` / `Finding` — Verification artifacts
- Requirement IDs follow: `AT-FR-***`, `AT-TS-***`, `AT-NFR-***`, `AT-AS-***`

## Code standards

- **Formatter:** gofumpt with `--extra` (stricter than gofmt)
- **Linter:** golangci-lint v2 with 25+ linters enabled. Config in `.golangci.yml`
- Complexity limits: gocognit max 30, cyclop max 22
- `//nolint` requires both specific linter name and explanation
- gosec exclusions (G301/G302/G304/G306) are intentional — attest is a file-management tool
- gocritic hugeParam threshold is 200 bytes (Task struct at 440 bytes is expected to be flagged if passed by value)

## Workflow

- Feature branches with PRs — no direct pushes to main
- Git hooks (`.githooks/`) run `just pre-commit` and `just check` automatically
- CI (`.github/workflows/ci.yml`) mirrors the same checks
