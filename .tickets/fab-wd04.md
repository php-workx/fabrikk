---
id: fab-wd04
status: closed
deps: []
links: []
created: 2026-03-25T17:00:27Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, test]
---
# Add test for IsDaemonRunning

IsDaemonRunning (daemon.go:318) has no direct test. It's covered indirectly by TestDaemon_AlreadyRunning (via cleanupStaleSocket which does the same connect check), but an external reviewer could flag the gap. Add TestIsDaemonRunning: start daemon, verify IsDaemonRunning returns true; stop daemon, verify returns false.

## Acceptance Criteria

go test -race -run TestIsDaemonRunning ./internal/agentcli/... passes.

