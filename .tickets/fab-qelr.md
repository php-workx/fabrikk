---
id: fab-qelr
status: closed
deps: [fab-gu8m]
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 0
assignee: Ronny Unger
tags: [daemon, race]
---
# Fix TOCTOU on needsRestart — hold mutex during restart

Query() at daemon.go:229 releases d.mu before calling restart(). A concurrent Query() sees needsRestart=false and tries a dead socket. Fix: hold d.mu for the entire restart decision+execution, or add a restarting bool that causes concurrent callers to skip the daemon and go straight to one-shot fallback.

## Acceptance Criteria

go test -race passes. Concurrent Query() during restart falls back cleanly.

