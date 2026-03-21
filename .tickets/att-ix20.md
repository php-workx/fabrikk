---
id: att-ix20
status: closed
deps: []
links: []
created: 2026-03-20T16:10:37Z
type: chore
priority: 3
assignee: Ronny Unger
parent: att-2nve
tags: [memory, low, observability]
---
# Add engine event for successful learning enrichment

enrichTaskWithLearnings (engine.go:560-586) logs events only on citation failure (learning_citation_failed). No event is logged when enrichment succeeds — how many learnings were attached, which task was enriched. Add a learning_enrichment event after successful enrichment with detail showing task ID and count of attached learnings.

## Acceptance Criteria

Engine appends learning_enrichment event after enrichTaskWithLearnings succeeds; event includes task ID and learning count; test verifies event is appended

