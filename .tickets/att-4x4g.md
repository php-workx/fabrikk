---
id: att-4x4g
status: closed
deps: [att-rg4d]
links: []
created: 2026-03-21T10:02:16Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-40wv
tags: [memory, query]
---
# Add maturity-weighted scoring to Query

Update Query sort to weight utility by maturity: established=1.5x, candidate=1.2x, provisional=1.0x. Add maturityWeight helper. Test: established learning with utility 0.5 ranks above provisional with utility 0.5.

## Acceptance Criteria

TestMaturityWeightedQuery passes; established outranks provisional

