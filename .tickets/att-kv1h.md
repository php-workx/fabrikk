---
id: att-kv1h
status: closed
deps: []
links: []
created: 2026-03-24T09:21:53Z
type: chore
priority: 3
assignee: Ronny Unger
tags: [rebrand, P3]
---
# Update agent-based-spec-normalization.md for fabrikk rebrand

## What's wrong
Active plan doc references old CLI name and paths:
- "attest tech-spec" commands (lines 8, 130, 244-255)
- "cmd/attest/main.go" file paths (lines 133-135, 204-205)

## Risk
Internal confusion when reading the plan to understand current state.

## Acceptance criteria
- CLI examples use "fabrikk"
- File paths use "cmd/fabrikk/"
- grep 'cmd/attest\|attest tech-spec' docs/plans/agent-based-spec-normalization.md returns empty

