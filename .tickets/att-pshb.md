---
id: att-pshb
status: closed
deps: [att-4anj]
links: []
created: 2026-03-20T10:29:18Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [memory-wave-2]
---
# Add intent-first ticket fields — Intent, Constraints, Warnings, LearningIDs

Add Intent/Constraints/Warnings/LearningIDs/LearningContext to state.Task and Frontmatter. Add LearningRef type to state/types.go. Wire through TaskToFrontmatter/FrontmatterToTask. Update MarshalTicket to render '## Context from Learnings' section from LearningContext. See docs/plans/agent-memory-system.md section 3.2.

