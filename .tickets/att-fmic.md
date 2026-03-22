---
id: att-fmic
status: closed
deps: []
links: []
created: 2026-03-21T11:00:39Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, maintenance, p2]
---
# Add dedup and contradiction scan to Store.Maintain

Spec §11.3 lists 7 maintenance tasks. Implementation has 4 (maturity transitions, staleness, GC, index rebuild). Missing:
1. Dedup: merge learnings with ≥2 shared tags AND similar summaries (Levenshtein < 0.2)
2. Contradiction scan: flag anti_pattern + pattern with ≥2 shared tags
(Prevention refresh is Phase 3 — not included here)

Risk: duplicate learnings accumulate from repeated reviews, wasting token budget. Contradictory learnings coexist without flagging.

## Acceptance Criteria

1. Maintain deduplicates: learnings with ≥2 shared tags and similar summaries → keep higher utility, SupersededBy on other
2. Maintain flags contradictions in MaintainReport (new Contradictions field)
3. Contradictions NOT auto-resolved (may be intentional)
4. Test: add two learnings with same tags+similar summary, maintain → one superseded
5. Test: add anti_pattern + pattern with shared tags, maintain → contradiction flagged
6. MaintainReport.Merged count populated

