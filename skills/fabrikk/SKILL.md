---
name: fabrikk
description: "Use when working with spec reviews, execution plans, task dispatch, learnings, or verification in this project. Also use when user says 'review spec', 'what's next', 'capture learning', or asks about run status."
argument-hint: "<review|plan|next|learn|status|context|maintain|verify> [args]"
allowed-tools: Bash, Read, Grep, Glob, Agent, Write, Edit
---

# fabrikk

**Execute the requested sub-command. Do not just describe what you would do.**

## Quick Reference

| Command | What it does |
|---|---|
| `/fabrikk review <path>` | Council review a spec |
| `/fabrikk plan <run-id>` | Draft + review execution plan |
| `/fabrikk next [run-id]` | Next task with context bundle |
| `/fabrikk learn "text" [flags]` | Capture a learning |
| `/fabrikk status [run-id]` | Unified dashboard |
| `/fabrikk context <run-id> <task>` | Assemble agent context |
| `/fabrikk maintain` | Knowledge store maintenance |
| `/fabrikk verify <run-id> <task>` | Deterministic verification |

## Sub-Commands

### review

```bash
fabrikk tech-spec review --from <spec-path> --skip-approval --mode mvp --round 1 [--force]
```

Default: MVP mode, 1 round. Use `--mode standard --round 2` for deeper review, `--mode production --round 3` for release-critical specs.

Learnings are auto-extracted from council findings and rejections. No manual extraction needed.

If PASS → suggest `/fabrikk plan <run-id>`. If WARN/FAIL → summarize blocking findings.

### plan

1. `fabrikk plan draft <run-id>`
2. `fabrikk plan review <run-id>` — display findings
3. If passes, ask user to approve: `fabrikk plan approve <run-id>`
4. After approval → `fabrikk approve <run-id>` compiles tasks

### next

1. If no run-id: `fabrikk status` to find active run
2. `fabrikk next <run-id>` — highest-priority ready task
3. `fabrikk context <run-id> <task-id>` — learnings + handoff
4. Suggest claiming or implementing

### learn

```bash
fabrikk learn "<content>" --tag <tags> --category <cat> [--path X] [--source-task X] [--source-run X]
```

Categories: `pattern`, `anti_pattern`, `tooling`, `codebase`, `process`. Default: `codebase`.

If `--tag` omitted, extract 2-3 key technical terms from content as tags.

### status

Combine three views into one dashboard:
1. `fabrikk status` — runs and their state
2. `tk ready` — ready tickets
3. `fabrikk learn list` — recent learnings + handoff

### context

```bash
fabrikk context <run-id> <task-id>
```

Shows matching learnings, token usage, handoff if < 24h old.

### maintain

Run `fabrikk learn maintain` for maintenance. Use `fabrikk learn list` to inspect current store state.

### verify

```bash
fabrikk verify <run-id> <task-id>
```

Learnings are auto-extracted from blocking findings on verification failure.

## Workflow Chains

- **Spec → tasks:** `review <path>` → `plan <run-id>` → `approve <run-id>` → `next <run-id>`
- **Session wrap-up:** `fabrikk learn handoff --summary "..." --next "..."` → `status`

## Common Mistakes

- Forgetting `--skip-approval` on review (blocks on interactive persona prompt)
- Learnings are auto-extracted from review/verify failures — no manual extraction needed
- Running `plan` before `tech-spec review` and approval
- Using `tk` for run-scoped tasks instead of `fabrikk next` (misses context bundle)
