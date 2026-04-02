---
id: fab-c0zx
status: closed
deps: [fab-qi7v, fab-te4o]
links: []
created: 2026-03-24T14:26:39Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon]
---
# Wire daemon lifecycle in cmdTechSpecReview

In cmdTechSpecReview (main.go:412), after structural review passes: create Daemon with run-scoped DaemonConfig (SocketDir=os.TempDir(), RunID=filepath.Base(eng.RunDir.Root)), Start it, set cfg.JudgeInvokeFn=daemon.QueryFunc(), defer Stop(). Log warning and proceed without daemon on start failure.

## Acceptance Criteria

go build ./cmd/fabrikk/... compiles. Council review uses daemon when available, falls back gracefully.

