---
id: fab-qbwf
status: closed
deps: [fab-qi7v]
links: []
created: 2026-03-24T14:26:15Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, test]
---
# Add daemon fixture test harness

Create test helper in daemon_test.go: a Go test binary that acts as fake Claude CLI. Reads stream-json from stdin, emits init/result frames on startup, echoes prompts as results. Can be signaled to terminate for crash tests. Add TestDaemon_StartStop, TestDaemon_Query, TestDaemon_QueryFunc_Fallback, TestDaemon_FilterEnv.

## Acceptance Criteria

go test -race ./internal/agentcli/... passes. No real Claude CLI invoked.

