---
id: fab-q294
status: closed
deps: []
links: []
created: 2026-03-24T14:26:23Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add ConsolidateInvokeFn to JudgeConfig

Add `ConsolidateInvokeFn agentcli.InvokeFn` field to `JudgeConfig` (judge.go:29). Add `consolidateInvoke` helper: returns `ConsolidateInvokeFn` if set, else `agentcli.InvokeFunc`.

**Only update `resolveConflicts` (line 212)** to use `consolidateInvoke`. This is a sequential, consolidated judge query — appropriate for daemon context.

**CRITICAL: Do NOT update `judgeOneReview` (line 274).** It runs in parallel goroutines (RunJudge lines 68-78). Wiring the daemon here would serialize all goroutines through the daemon mutex, destroying parallelism. `judgeOneReview` MUST stay on `agentcli.InvokeFunc`.

Pre-mortem finding: pm-20260324-001.

## Acceptance Criteria

- `go test -race ./internal/councilflow/...` passes
- `resolveConflicts` uses `ConsolidateInvokeFn` when set, `InvokeFunc` when nil
- `judgeOneReview` still uses `agentcli.InvokeFunc` directly (unchanged)

