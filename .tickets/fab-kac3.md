---
id: fab-kac3
status: closed
deps: []
links: []
created: 2026-03-25T12:27:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, test]
---
# Add test for concurrent daemon Query calls

No test covers two goroutines calling Query simultaneously. While the daemon serves the sequential judge role (concurrent calls shouldn't happen in practice), the code should handle it correctly. The claudeProcess.mu serializes access, and Daemon.mu protects needsRestart. Add TestDaemon_ConcurrentQuery: start daemon, launch 3 goroutines each calling Query, verify all get valid responses (serialized through mutex), no races detected.

## Acceptance Criteria

go test -race -run TestDaemon_Concurrent ./internal/agentcli/... passes.


## Notes

**2026-03-25T13:13:37Z**

Pre-mortem note: Fixture echoes in order since claudeProcess.mu serializes. Test should use a timeout (e.g., 10s) as the real safety net against deadlocks, not just assertion on response content.
