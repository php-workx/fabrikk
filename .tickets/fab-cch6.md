---
id: fab-cch6
status: closed
deps: []
links: []
created: 2026-03-25T12:27:29Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, observability]
---
# Add logging to daemon restart events

Spec §2 Wave 3 item 6 says 'Log the crash and restart event.' The restart() method in daemon.go calls Stop() then Start() but logs nothing. The QueryFunc fallback logs to stderr, but the restart itself is invisible. Add fmt.Fprintf(os.Stderr) in restart() before Stop and after successful Start, including the reason (crash recovery).

## Acceptance Criteria

Restart events visible in stderr output. TestDaemon_CrashRecovery verifies log output.

