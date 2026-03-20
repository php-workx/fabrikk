---
id: att-7skh
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
# Add attest learn and attest context CLI commands

New file cmd/attest/learn.go with sub-commands: learn 'content' --tag --category (add), learn query --tag --category --limit (query), learn handoff --summary --next (handoff), learn list, learn gc. New attest context <run-id> <task-id> command. Wire into main.go switch. Add 'Learning' group to help.go. See docs/plans/agent-memory-system.md section 3.5.

