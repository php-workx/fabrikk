---
id: att-d2zu
status: closed
deps: [att-rg4d, att-4x4g]
links: []
created: 2026-03-21T10:02:16Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-40wv
tags: [memory, maintenance]
---
# Implement Store.Maintain with dedup, maturity transitions, staleness, GC

Add Maintain(maxAge) method: maturity transitions (provisional→candidate at 3+ citations/0.55 utility, candidate→established at 5+, demotion at <0.3), staleness penalty (uncited >30 days → utility -= 0.1), GC (expired >maxAge), index rebuild. atomic.Bool prevents concurrent runs. MaintainReport return type.

## Acceptance Criteria

TestMaintain_MaturityPromotion, TestMaintain_Demotion, TestMaintain_StalePenalty, TestMaintain_GC all pass

