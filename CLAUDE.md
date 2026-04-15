# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

This project uses a CLI ticket system for task management. Run `tk help` when you need to use it.

## What is fabrikk

A spec-driven autonomous run system for coding agents. Implementation is only complete when it traces back to approved spec requirements, passes deterministic verification, and clears independent multi-model council review. Written in Go with minimal dependencies (4 direct: lipgloss, yaml.v3, gofrs/flock, charmbracelet/x/term), single binary.

## Commands

```bash
just setup          # Configure git hooks + install dev tools (first time)
just pre-commit     # Fast local checks + fresh non-race tests
just pre-push       # Pre-commit + race tests + vuln scan
just check          # Full quality gate (same as pre-push)
just test           # Fresh non-race tests
just test-race      # Fresh tests with race detector
just format         # Auto-fix formatting (gofumpt)
just build          # Compile to bin/fabrikk with version info (also creates bin/fab alias)
just cover          # Tests with coverage → coverage.html

# Single test
go test -race -count=1 ./internal/compiler/...
go test -race -count=1 -run TestCompile ./internal/compiler/...
```

## Architecture

```
CLI (cmd/fabrikk/)
  ├─ Engine (internal/engine/)         — Run lifecycle orchestration
  │   ├─ Compiler (internal/compiler/)   — Spec → task graph (deterministic, no LLM)
  │   ├─ AgentCLI (internal/agentcli/)   — Shared CLI backend invocation (claude, codex, gemini)
  │   ├─ Councilflow (internal/councilflow/) — Multi-model review pipeline
  │   └─ Verifier (internal/verifier/)   — Evidence-based verification
  ├─ State (internal/state/)           — Types, TaskStore interface, RunDir I/O, atomic writes
  └─ Ticket (internal/ticket/)         — File-based task backend (.tickets/ markdown+YAML)
```

**Task persistence via ticket.Store:** Tasks are stored as markdown files with YAML frontmatter in `.tickets/`. The `ticket.Store` implements `state.TaskStore` and `state.ClaimableStore` interfaces. This is the sole task backend — wired in `cmd/fabrikk/tasks.go`.

**Run state lives on disk:** Run artifacts (spec, plan, reviews) are in `.fabrikk/runs/<run-id>/` as JSON/JSONL files. `internal/state` provides `RunDir` for managing this directory and atomic file writes for concurrent safety.

**Claim system:** `ticket.Store` supports crash-safe exclusive claims via `gofrs/flock` file locking. Workers claim tasks with leases, heartbeat renewal, and atomic release with status transitions.

**Compiler is deterministic:** Requirements are grouped by proximity, assigned to lanes, and given stable IDs/ETags. No LLM involvement — purely rule-based.

**Councilflow:** Parallel fan-out to persona reviewers (systems-architect, spec-conformance, agent-workflow, orchestrator) across multiple rounds. A Judge synthesizes findings with opinionated dismissal rationale. Review modes: MVP, standard, production.

**Verifier pipeline order:** Quality gate → evidence checks → scope check → requirement linkage. Each step can produce blocking findings.

## Key types and interfaces (internal/state/types.go)

- `TaskStore` — Interface for task CRUD (ReadTasks, WriteTasks, ReadTask, WriteTask, UpdateStatus, CreateRun)
- `ClaimableStore` — Extends TaskStore with exclusive claim operations (ClaimTask, ReleaseClaim, RenewClaim)
- `RunArtifact` — Approved normalized contract (requirements, clarifications, risk profile)
- `Task` — Work item with status, scope (owned/readonly/shared paths), dependencies, requirements
- `Claim` — Exclusive worker lease on a task (owner, backend, lease expiry, heartbeat)
- `RunStatus` — Current state summary (active wave, task counts, blockers)
- `CompletionReport` / `VerifierResult` / `Finding` — Verification artifacts
- Requirement IDs follow: `AT-FR-***`, `AT-TS-***`, `AT-NFR-***`, `AT-AS-***`

## Code standards

- **Formatter:** gofumpt with `--extra` (stricter than gofmt)
- **Linter:** golangci-lint v2 with 25+ linters enabled. Config in `.golangci.yml`
- Complexity limits: gocognit max 30, cyclop max 22
- `//nolint` requires both specific linter name and explanation
- gosec exclusions (G301/G302/G304/G306) are intentional — fabrikk is a file-management tool
- gocritic hugeParam threshold is 200 bytes (Task struct at 440 bytes is expected to be flagged if passed by value)

## Ticket system (internal/ticket/)

The `.tickets/` directory holds all task state as markdown files with YAML frontmatter. Key files:

- **store.go** — `TaskStore` + `ClaimableStore` implementation; maps tickets to `state.Task`
- **format.go** — Marshals/unmarshals between `state.Task` and markdown+YAML; preserves body on frontmatter-only updates
- **claim.go** — Crash-safe exclusive claims with `gofrs/flock` file locking
- **deps.go** — Dependency parsing, cycle detection, topological sort
- **id.go** — `GenerateID` (collision-resistant {prefix}-{4char}), `ResolveID` (partial ID matching)

## Workflow

- Feature branches with PRs — no direct pushes to main
- Git hooks (`.githooks/`) run `just pre-commit` and `just check` automatically
- CI (`.github/workflows/ci.yml`) mirrors the same checks
