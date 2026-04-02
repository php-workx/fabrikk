---
id: fab-mnm9
status: closed
deps: [fab-8468]
links: []
created: 2026-03-24T14:26:01Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Implement Daemon struct — socket listener, lifecycle, PID tracking

Add Daemon struct to daemon.go: DaemonConfig, NewDaemon, Start (socket listener, PID file), Stop (kill process, cleanup), SocketPath, handleDaemonConn. Socket at TMPDIR/fabrikk-<run-id>.sock with 0600 perms. Run dir 0700.

## Acceptance Criteria

Daemon can start, listen on Unix socket, handle connections, stop cleanly, remove socket and PID.

