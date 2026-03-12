# Ticket / Beans Fit Analysis for `attest`

Date: 2026-03-12
Status: Research note
Scope: Evaluate `wedow/ticket` and `hmans/beans` for reusable task-management ideas inside `attest`.

## Summary

Both repositories contain useful ideas.

- `ticket` is the better source for a minimal, low-friction, file-backed task model with dependency-aware CLI workflows.
- `beans` is the better source for agent-oriented querying, optimistic concurrency, live subscriptions, archived project memory, and task-to-agent session modeling.

Best adoption recommendation:

- do **not** adopt either project wholesale as `attest`'s task system
- borrow `ticket`'s simplicity for the canonical task artifact shape and actionable status commands
- borrow `beans`' richer query/update patterns for agent-facing views, repair tracking, and future UI/status layers

## 1. `ticket`

Primary sources:

- GitHub repo: https://github.com/wedow/ticket
- README: https://raw.githubusercontent.com/wedow/ticket/master/README.md

Relevant capabilities:

- tickets are plain Markdown files with YAML frontmatter in `.tickets/`
- explicit dependency graph commands: `dep`, `dep tree`, `dep cycle`
- actionable workflow commands: `ready`, `blocked`, `start`, `close`, `reopen`
- parent lookup by walking upward to find `.tickets`
- plugin mechanism via `tk-<cmd>` or `ticket-<cmd>`
- JSON query output through `query`

Strong ideas to borrow:

- plain Markdown + frontmatter as a machine/human-readable task record
- first-class `ready` and `blocked` commands instead of generic list/filter UX
- dependency cycle detection before execution
- partial-ID ergonomics
- plugin-style extension points for repo-specific commands

What does not fit directly:

- the task model is intentionally minimal and does not include agent session state, review state, or richer optimistic concurrency controls
- status model is simpler than `attest` needs
- there is no built-in council/reviewer lifecycle

## 2. `beans`

Primary sources:

- GitHub repo: https://github.com/hmans/beans
- README: https://raw.githubusercontent.com/hmans/beans/main/README.md

Relevant capabilities:

- beans are Markdown files with frontmatter in `.beans`
- GraphQL query engine for low-token agent queries
- archived beans as project memory
- built-in TUI and roadmap generation
- worktree and agent session concepts
- GraphQL mutations with optimistic concurrency (`ifMatch`)
- subscription model with `INITIAL_SNAPSHOT` to avoid load/subscribe races

Strong ideas to borrow:

- agent-facing query layer that returns just the fields needed
- optimistic concurrency using content hash / ETag on task updates
- explicit `blocked_by` and `blocking` edges
- archived completed work as structured project memory
- race-free live status model using initial snapshot + change stream
- task-linked agent session metadata for status visibility

What does not fit directly:

- broader product scope than `attest` needs, including TUI/web/worktree/chat layers
- schema and runtime are much heavier than a v1 `attest` state engine
- agent session ownership is coupled to its own product shape, not `attest`'s council/review pipeline

## 3. Best ideas for `attest`

### Borrow from `ticket`

- canonical task files as Markdown with frontmatter
- `ready` / `blocked` views as first-class status surfaces
- dependency tree and cycle validation
- simple extension mechanism for future repo-specific task commands

### Borrow from `beans`

- content-hash-based optimistic concurrency for task edits
- explicit `blocking` / `blocked_by` relationships
- structured query/view layer for agents
- initial-snapshot event semantics for status streaming
- archived completed tasks as searchable memory

## 4. Recommendation

Use a hybrid approach:

- keep `attest`'s canonical run state in `.attest/`
- keep tasks structured for deterministic execution and council verification
- expose task-management views that feel more like `ticket`
- add agent-facing query/update semantics inspired by `beans`

If we reuse concrete ideas, the best immediate candidates are:

1. `ticket`-style `ready` / `blocked` / dependency ergonomics
2. `beans`-style optimistic concurrency and filtered query surface
3. `beans`-style archived task memory for later retrieval by planners/reviewers

## Bottom Line

We should not reinvent basic task ergonomics, but we also should not adopt another project's task system wholesale.

The best path is to build `attest`'s task model around its own spec-attestation workflow while borrowing:

- `ticket` for minimal task UX
- `beans` for agent-centric query and update mechanics
