---
id: att-lk0b
status: closed
deps: [att-i34x]
links: []
created: 2026-03-19T06:12:46Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [wave-3]
---
# Construct ticket.Store in cmdApprove, enable dual-write

In cmdApprove, create ticket.Store and set on engine. Compile writes both tasks.json and .tickets/. Add .tickets/*.lock to .gitignore.

