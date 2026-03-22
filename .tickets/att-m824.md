---
id: att-m824
status: closed
deps: []
links: []
created: 2026-03-21T11:00:39Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, enrichment, p2]
---
# Derive query tags from OwnedPaths and requirement IDs in enrichTaskWithLearnings

Spec §3.2: enrichTaskWithLearnings should derive tags from (1) package names from Scope.OwnedPaths (e.g., internal/compiler → tags compiler, internal), (2) requirement ID prefixes (AT-FR-001 → functional), (3) task Category. Instead it passes task.Tags directly — which are requirement-family tags like 'at-fr', not package names.

Risk: learning queries miss relevant learnings because query tags don't match learning tags. A learning tagged 'compiler' won't match a task whose Tags are ['at-fr'] even though OwnedPaths includes 'internal/compiler'.

## Acceptance Criteria

1. enrichTaskWithLearnings derives tags from OwnedPaths path segments
2. Derives tags from requirement ID prefixes (AT-FR→functional, AT-TS→testing, AT-NFR→non-functional)
3. Includes task Category if present
4. Does NOT persist derived tags on Task struct
5. Test: task with OwnedPaths=['internal/compiler'] queries with tags=['compiler','internal']
6. Test: task with RequirementIDs=['AT-FR-001'] queries with tags including 'functional'

