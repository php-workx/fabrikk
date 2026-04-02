---
id: fab-ih6o
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 1
assignee: Ronny Unger
tags: [daemon]
---
# Add cmd.Wait() in PID file write error path

daemon.go:136-139: Kill() called but cmd.Wait() missing. Compare with line 130 which has both. One line fix: add _ = d.process.cmd.Wait() after Kill() at line 138.

## Acceptance Criteria

No zombie process when PID file write fails.

