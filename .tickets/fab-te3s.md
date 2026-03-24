---
id: fab-te3s
status: closed
deps: [fab-d6kr]
links: []
created: 2026-03-24T14:27:07Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add graceful shutdown with signal handling

In Daemon.Stop: propagate context cancellation to Claude process. Ensure socket+PID cleanup on all exit paths (defer, signal handler, panic recovery). Close listener when context is cancelled so Accept() unblocks. Add TestDaemon_GracefulShutdown.

## Acceptance Criteria

go test -race -run TestDaemon_Graceful ./internal/agentcli/... passes. No stale artifacts after any termination path.

