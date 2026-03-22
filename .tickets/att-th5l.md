---
id: att-th5l
status: closed
deps: [att-d2zu, att-pmuj]
links: []
created: 2026-03-21T10:02:16Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-40wv
tags: [memory, cli, maintenance]
---
# Add MaintainIfStale to Query + attest learn maintain CLI command

Add MaintainIfStale() called from Query() — spawns background goroutine if >24h since last maintain. Add lastMaintainedAt and maintaining atomic.Bool to Store. Add OnMaintain callback. Add cmdLearnMaintain to CLI.

## Acceptance Criteria

attest learn maintain prints report; Query triggers background maintain after 24h

