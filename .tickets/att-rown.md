---
id: att-rown
status: closed
deps: []
links: []
created: 2026-03-21T11:01:04Z
type: chore
priority: 2
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, cli, p3]
---
# Show Source field in attest context output

Spec §9.5: attest context should show learning source (council/verifier/rejection/manual) alongside content. Current output shows ID, category, utility, summary but not Source.

Fix: Add Source line to context display. Format: 'Source: council, run-123, finding sec-001'

## Acceptance Criteria

1. attest context output includes Source for each learning
2. Format matches spec §9.5 example
3. Manual learnings show 'Source: manual'

