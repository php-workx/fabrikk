---
id: att-d44c
status: closed
deps: []
links: []
created: 2026-03-24T09:21:46Z
type: chore
priority: 3
assignee: Ronny Unger
tags: [rebrand, P3]
---
# Update .golangci.yml comments for fabrikk rebrand

## What's wrong
.golangci.yml has two comments referencing "attest":
- Line 3: "configuration for attest"
- Line 91: "attest is a file-management tool"

## Risk
Cosmetic. Confusing when reading linter config.

## Acceptance criteria
- grep 'attest' .golangci.yml returns empty

