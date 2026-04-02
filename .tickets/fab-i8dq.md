---
id: fab-i8dq
status: closed
deps: []
links: []
created: 2026-03-25T12:27:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, test]
---
# Add test for Unix socket path length limit

macOS limits Unix socket paths to ~104 bytes. The fixture tests use short paths (os.MkdirTemp with short prefix), but no test verifies behavior when the computed socket path exceeds the limit. Add TestDaemon_SocketPathTooLong: create a DaemonConfig with a long SocketDir + RunID that produces a path >104 bytes, call Start(), verify it returns a clear error (not a cryptic syscall error).

## Acceptance Criteria

go test -race -run TestDaemon_SocketPath ./internal/agentcli/... passes.


## Notes

**2026-03-25T13:13:28Z**

Pre-mortem note: macOS limit is ~104 bytes, Linux is ~108. Test should use the actual platform error from net.Listen ("invalid argument") rather than hardcoding a byte count. Assert the error is returned cleanly, not that a specific length is rejected.
