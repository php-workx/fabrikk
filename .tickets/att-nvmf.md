---
id: att-nvmf
status: closed
deps: []
links: []
created: 2026-03-24T09:22:15Z
type: chore
priority: 3
assignee: Ronny Unger
tags: [rebrand, P3]
---
# Update stale path reference in ticket att-j63b

## What's wrong
.tickets/att-j63b.md line 25 references "cmd/attest/commands_test.go" which
is now cmd/fabrikk/commands_test.go.

## Risk
Minimal — closed ticket. But stale paths in tickets can confuse when
reviewing history.

## Acceptance criteria
- grep 'cmd/attest' .tickets/att-j63b.md returns empty

