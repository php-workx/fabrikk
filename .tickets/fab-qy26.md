---
id: fab-qy26
status: closed
deps: []
links: []
created: 2026-03-25T17:00:27Z
type: chore
priority: 2
assignee: Ronny Unger
tags: [daemon, docs]
---
# Fix spec files table: IsDaemonRunning location

Spec §3 files-to-modify table row 4 says 'internal/agentcli/agentcli.go — Add IsDaemonRunning helper'. The function lives in daemon.go, not agentcli.go. The fab-c9gc spec update missed this row. Update the table to say daemon.go.

## Acceptance Criteria

Spec §3 files table matches reality. No mention of IsDaemonRunning in the agentcli.go row.

