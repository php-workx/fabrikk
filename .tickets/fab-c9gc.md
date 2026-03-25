---
id: fab-c9gc
status: closed
deps: []
links: []
created: 2026-03-25T12:27:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, docs]
---
# Update daemon-orchestrator spec to match implementation

Several spec-vs-implementation divergences after pre-mortem and code review:
- §2 pseudocode says judgeConfig.InvokeFn → implementation uses CouncilConfig.JudgeInvokeFn → JudgeConfig.ConsolidateInvokeFn
- §4 QueryFunc calls Invoke directly → implementation calls InvokeFunc (global var, for test stub compatibility)
- §2 says PID at <run-dir>/daemon.pid → implementation puts PID at TMPDIR/fabrikk-<run-id>.pid
- §8 says 'Planned — not yet approved' → implementation is complete
- DaemonConfig in spec missing RunID field that implementation requires

## Acceptance Criteria

Spec matches implementation. No contradictions between docs/plans/daemon-orchestrator.md and internal/agentcli/daemon.go.

