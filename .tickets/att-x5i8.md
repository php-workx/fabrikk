---
id: att-x5i8
status: closed
deps: []
links: []
created: 2026-03-21T11:00:39Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, extraction, p2]
---
# Use DedupeKey for verifier learning dedup with confidence boosting

Spec §9.2.3: 'Deduplication by DedupeKey. If a learning with tag verifier:<dedupe_key> already exists, increment its confidence (capped at 1.0) instead of creating a duplicate.'

Implementation deduplicates by finding ID tag (verifier:<findingID>), not DedupeKey. No confidence boosting on repeat failures. Risk: repeated failures on the same issue create separate learnings instead of strengthening the existing one.

## Acceptance Criteria

1. extractLearningsFromVerifier uses DedupeKey (not FindingID) for dedup tag
2. When existing learning found with same DedupeKey, boost its Confidence by 0.1 (cap 1.0)
3. Do NOT create duplicate
4. Test: verify twice with same DedupeKey → single learning with boosted confidence
5. Test: verify with different DedupeKey → two separate learnings

