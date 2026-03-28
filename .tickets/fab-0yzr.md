---
id: fab-0yzr
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 1
assignee: Ronny Unger
tags: [daemon, test]
---
# Fix TestDaemon_CrashRecovery — assert behavior not internal field

daemon_test.go:329 reads d.needsRestart without d.mu — race under -race flag. Fix: remove the field inspection. Instead assert on behavior: call d.Query() after crash and verify it triggers restart (succeeds with a fresh daemon).

## Acceptance Criteria

go test -race -run TestDaemon_Crash passes without race warnings.

