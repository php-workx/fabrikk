---
id: fab-d6kr
status: closed
deps: [fab-q95u]
links: []
created: 2026-03-24T14:27:00Z
type: task
priority: 2
assignee: Ronny Unger
tags: [daemon, test]
---
# Add context retention acceptance test

Add TestDaemon_ContextRetention: two sequential queries through same daemon where second query depends on info from first (e.g., 'remember X=42' then 'what is X?'). Must pass with daemon. After daemon restart, same second query must produce degraded answer. Proves context accumulation works.

## Acceptance Criteria

go test -race -run TestDaemon_Context ./internal/agentcli/... passes.

