---
id: att-b9vk
status: closed
deps: [att-tvwc]
links: []
created: 2026-03-20T16:10:08Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-2nve
tags: [memory, moderate]
---
# Enforce token budget cap in context assembly

Spec §3.4: 'Token budget: 2000 tokens (~500 words). Max 8 learnings per task. Estimated as len(content)/4.' cmdContext uses limit=8 and min-utility=0.1 but does not enforce the 2000-token budget — estimateTokens is display-only. enrichTaskWithLearnings uses limit=5 (spec says top 5, so this is correct for enrichment). Fix: in AssembleContext (or cmdContext until AssembleContext exists), iteratively add learnings until the 2000-token budget is reached, then stop.

## Acceptance Criteria

Context assembly stops adding learnings when token budget (2000) is exceeded; test verifies budget enforcement with learnings that exceed budget

