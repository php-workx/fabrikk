---
id: fab-gu8m
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 0
assignee: Ronny Unger
tags: [daemon, race]
---
# Fix Stop() data race — add sync.Once or mutex for lifecycle fields

Stop() at daemon.go:189 reads d.listener and d.process without d.mu. The idle timeout goroutine (line 167) calls d.Stop() concurrently with Query() callers. Race detector would catch this. Fix: wrap Stop() body in sync.Once, or acquire d.mu and add a stopped bool to make it idempotent. Also nil out d.listener and d.process after cleanup.

## Acceptance Criteria

go test -race passes. Concurrent Stop()+Query() does not race.

