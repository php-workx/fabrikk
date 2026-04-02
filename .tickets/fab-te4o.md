---
id: fab-te4o
status: closed
deps: [fab-q294]
links: []
created: 2026-03-24T14:26:30Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add JudgeInvokeFn to CouncilConfig

Add `JudgeInvokeFn agentcli.InvokeFn` field to `CouncilConfig` (council.go:18). In `executeRound` (line 186), pass `cfg.JudgeInvokeFn` to `judgeCfg.ConsolidateInvokeFn`. Do NOT touch Runner, `runParallelReviews`, or reviewer paths.

## Acceptance Criteria

- `go test -race ./internal/councilflow/...` passes
- `JudgeInvokeFn` flows from `CouncilConfig` to `JudgeConfig.ConsolidateInvokeFn`

