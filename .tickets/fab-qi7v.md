---
id: fab-qi7v
status: closed
deps: [fab-mnm9]
links: []
created: 2026-03-24T14:26:08Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add Daemon.Query and Daemon.QueryFunc

Add Query method: connect to Unix socket, send DaemonRequest, read DaemonResponse. Add QueryFunc: returns InvokeFn closure that tries daemon, falls back to agentcli.Invoke. Add IsDaemonRunning to agentcli.go.

## Acceptance Criteria

QueryFunc returns working InvokeFn. Fallback works when daemon not running.

