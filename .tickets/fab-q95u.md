---
id: fab-q95u
status: closed
deps: [fab-3io9]
links: []
created: 2026-03-24T14:26:54Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Add crash recovery to Daemon.Query

In Daemon.Query: if socket connection fails or DaemonResponse.Error indicates process death, (a) retry query once via one-shot Invoke, (b) mark daemon as needing restart, (c) before next query, start fresh daemon, remove stale socket+PID. Add TestDaemon_CrashRecovery using killable fixture process.

## Acceptance Criteria

go test -race -run TestDaemon_Crash ./internal/agentcli/... passes.

