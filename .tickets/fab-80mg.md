---
id: fab-80mg
status: open
deps: []
links: []
created: 2026-03-25T12:27:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, engine]
---
# Wire daemon InvokeFn to engine orchestrator

Spec §2 says the orchestrator in internal/engine/ receives a daemon-bound InvokeFn via struct field. Currently only the council judge path (CouncilConfig.JudgeInvokeFn) is wired. Engine has no InvokeFn field. Today engine phase methods (Prepare, Approve, Compile, VerifyTask) don't call LLMs directly — but when plan review (spec-decomp §2.5) or verification phases add LLM calls, they'll need the daemon. Add an InvokeFn field to Engine struct and thread it through from the CLI command where the daemon is created.

## Acceptance Criteria

Engine struct has InvokeFn agentcli.InvokeFn field. startJudgeDaemon passes QueryFunc to engine. go build passes.


## Notes

**2026-03-25T13:13:21Z**

Pre-mortem note: Engine phase methods don't call LLMs today. This ticket is forward-looking — the field will be unused until plan review or verification phases add LLM calls. Consider deferring until then to avoid YAGNI. If implemented now, add a comment on the field explaining it's wired but not yet consumed.
