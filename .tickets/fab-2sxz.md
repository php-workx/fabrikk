---
id: fab-2sxz
status: closed
deps: []
links: []
created: 2026-03-25T12:27:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, test]
---
# Add test for daemon-already-running detection

cleanupStaleSocket returns an error when a live daemon is already listening on the socket path. No test covers this path. Add TestDaemon_AlreadyRunning: start daemon A, then try to start daemon B with the same socket path. Verify B's Start() returns an error containing 'daemon already running'. Stop A, verify B can now start successfully.

## Acceptance Criteria

go test -race -run TestDaemon_AlreadyRunning ./internal/agentcli/... passes.

