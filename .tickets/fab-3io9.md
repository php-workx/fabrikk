---
id: fab-3io9
status: closed
deps: [fab-qbwf]
links: []
created: 2026-03-24T14:26:47Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add stale socket cleanup to Daemon.Start

In Daemon.Start, before binding: check if socket path exists, try net.Dial, if connection refused os.Remove the stale socket. Validate PID file: read PID, check os.FindProcess, remove stale PID. Add TestDaemon_StaleSocketCleanup.

## Acceptance Criteria

go test -race -run TestDaemon_StaleSocket ./internal/agentcli/... passes.

