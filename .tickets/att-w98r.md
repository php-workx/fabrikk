---
id: att-w98r
status: open
deps: [att-4anj, att-pshb]
links: []
created: 2026-03-20T10:29:18Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [memory-wave-3]
---
# Wire LearningStore into Engine — enrichTaskWithLearnings + AssembleContext

Add LearningStore field to Engine struct. Add enrichTaskWithLearnings method (post-compilation, queries by tags+paths, sets Warnings from anti_pattern, Constraints from codebase/tooling, populates LearningContext). Add AssembleContext method for dispatch-time context assembly. See docs/plans/agent-memory-system.md sections 3.2 and 3.4.

