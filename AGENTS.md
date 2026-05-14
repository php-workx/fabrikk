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