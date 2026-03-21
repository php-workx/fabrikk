---
name: attest
description: "Use when working with spec reviews, execution plans, task dispatch, learnings, or verification in this project. Also use when user says 'review spec', 'what's next', 'capture learning', or asks about run status."
argument-hint: "<review|plan|next|learn|status|context|maintain|verify> [args]"
allowed-tools: Bash, Read, Grep, Glob, Agent, Write, Edit
---

# attest

**Execute the requested sub-command. Do not just describe what you would do.**

## Quick Reference

| Command | What it does |
|---|---|
| `/attest review <path>` | Council review a spec |
| `/attest plan <run-id>` | Draft + review execution plan |
| `/attest next [run-id]` | Next task with context bundle |
| `/attest learn "text" [flags]` | Capture a learning |
| `/attest status [run-id]` | Unified dashboard |
| `/attest context <run-id> <task>` | Assemble agent context |
| `/attest maintain` | Knowledge store maintenance |
| `/attest verify <run-id> <task>` | Deterministic verification |

## Sub-Commands

### review

```bash
attest tech-spec review --from <spec-path> --skip-approval --mode mvp --round 1 [--force]
```

Default: MVP mode, 1 round. Use `--mode standard --round 2` for deeper review, `--mode production --round 3` for release-critical specs.

After review completes, extract up to 5 learnings from findings:
- Blocking findings → `attest learn "<desc>. Recommendation: <rec>" --tag <cat> --category anti_pattern --source-run <run-id>`
- Non-blocking insights → `--category codebase`

If PASS → suggest `/attest plan <run-id>`. If WARN/FAIL → summarize blocking findings.

### plan

1. `attest plan draft <run-id>`
2. `attest plan review <run-id>` — display findings
3. If passes, ask user to approve: `attest plan approve <run-id>`
4. After approval → `attest approve <run-id>` compiles tasks

### next

1. If no run-id: `attest status` to find active run
2. `attest next <run-id>` — highest-priority ready task
3. `attest context <run-id> <task-id>` — learnings + handoff
4. Suggest claiming or implementing

### learn

```bash
attest learn "<content>" --tag <tags> --category <cat> [--path X] [--source-task X] [--source-run X]
```

Categories: `pattern`, `anti_pattern`, `tooling`, `codebase`, `process`. Default: `codebase`.

If `--tag` omitted, extract 2-3 key technical terms from content as tags.

### status

Combine three views into one dashboard:
1. `attest status` — runs and their state
2. `tk ready` — ready tickets
3. `attest learn list` — recent learnings + handoff

### context

```bash
attest context <run-id> <task-id>
```

Shows matching learnings, token usage, handoff if < 24h old.

### maintain

Run `attest learn gc` + `attest learn list` to show store health. When `attest learn maintain` is implemented, use that instead.

### verify

```bash
attest verify <run-id> <task-id>
```

If FAIL: extract learnings from blocking findings → `--category anti_pattern --source-task <task-id>`.

## Workflow Chains

- **Spec → tasks:** `review <path>` → `plan <run-id>` → `approve <run-id>` → `next <run-id>`
- **Session wrap-up:** `attest learn handoff --summary "..." --next "..."` → `status`

## Common Mistakes

- Forgetting `--skip-approval` on review (blocks on interactive persona prompt)
- Not extracting learnings after review/verify failures — this is how the flywheel builds
- Running `plan` before `tech-spec review` and approval
- Using `tk` for run-scoped tasks instead of `attest next` (misses context bundle)
