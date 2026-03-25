---
id: fab-cfhi
status: closed
deps: []
links: []
created: 2026-03-25T17:03:59Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Fix IsDaemonRunning: spec table, missing test, doc comment

Three findings on IsDaemonRunning from final audit:

1. Spec §3 files-to-modify table row 4 says 'internal/agentcli/agentcli.go — Add IsDaemonRunning helper'. Function lives in daemon.go. Update the table.

2. No direct test. Covered indirectly by TestDaemon_AlreadyRunning but an external reviewer would flag it. Add TestIsDaemonRunning: start daemon → true, stop → false.

3. Unused in production code. Add doc comment explaining intended use (CLI status command, health checks, multi-run coordination) so it's not flagged as dead code.

## Acceptance Criteria

Spec table updated. TestIsDaemonRunning passes. Doc comment references intended consumers.

