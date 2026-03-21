---
id: att-3z5e
status: closed
deps: [att-lom4]
links: []
created: 2026-03-20T16:10:28Z
type: chore
priority: 3
assignee: Ronny Unger
parent: att-2nve
tags: [memory, low, structure]
---
# Extract tag index logic into internal/learning/index.go

Spec §4 lists internal/learning/index.go as a separate file (~100 lines) for tag inverted index build/query/keyword extraction. Currently the index logic is inlined in store.go (rebuildIndex method). This is a structural deviation. Moving to a separate file improves readability and aligns with the spec's file layout. Combine with att-lom4 (keyword extraction) if implementing together.

## Acceptance Criteria

internal/learning/index.go exists with index build/query logic; store.go delegates to it; all existing tests pass

