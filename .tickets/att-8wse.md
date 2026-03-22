---
id: att-8wse
status: closed
deps: []
links: []
created: 2026-03-20T16:10:20Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-2nve
tags: [memory, moderate, error-handling]
---
# Surface swallowed errors in cmdContext and cmdLearnList

cmdContext (learn.go:309,318) silently discards errors from learnStore.Query and learnStore.LatestHandoff using blank identifiers. cmdLearnList (learn.go:261) silently discards LatestHandoff errors. These should either return the error or log a warning to stderr so users know when the learning store is unreadable. Fix: at minimum, print a warning to stderr; ideally return the error for Query failures since they indicate a broken store.

## Acceptance Criteria

cmdContext surfaces Query errors (return or stderr warning); cmdContext surfaces LatestHandoff errors; cmdLearnList surfaces LatestHandoff errors; no silent error swallowing in learning CLI commands

