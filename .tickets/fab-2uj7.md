---
id: fab-2uj7
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 1
assignee: Ronny Unger
tags: [daemon, security]
---
# Revert socket permissions from Umask to Chmod

daemon.go:125 uses syscall.Umask(0o177) which is process-wide and not goroutine-safe. Revert to the simpler approach: net.Listen then os.Chmod(sockPath, 0o600). The brief permissions window on a user-private TMPDIR is less risky than mutating the process-wide umask.

## Acceptance Criteria

Socket file has 0600 perms. No syscall.Umask calls in daemon.go.

