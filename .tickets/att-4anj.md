---
id: att-4anj
status: closed
deps: []
links: []
created: 2026-03-20T10:29:18Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [memory-wave-1]
---
# Implement internal/learning package — types, store, index

New package internal/learning/ with: types.go (Learning, SessionHandoff, QueryOpts, Category, ContextBundle, LearningRef), store.go (Store with JSONL atomic-rewrite, flock locking, Add/Query/Get/RecordCitation/GC), index.go (tag inverted index build/query), store_test.go (Add, Query, handoff, citation, decay, GC — use clock injection). See docs/plans/agent-memory-system.md section 3.1.

