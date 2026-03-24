---
id: att-fnlq
status: closed
deps: []
links: []
created: 2026-03-24T09:22:00Z
type: chore
priority: 3
assignee: Ronny Unger
tags: [rebrand, P3]
---
# Update councilflow-to-specreview-rename.md for fabrikk rebrand

## What's wrong
Plan doc contains old module paths "github.com/runger/attest" in code examples
(lines 120, 123, 126, 132, 135, 166, 254).

## Risk
Internal confusion. Anyone implementing this plan would use wrong import paths.

## Acceptance criteria
- Module paths updated to "github.com/php-workx/fabrikk"
- grep 'runger/attest' docs/plans/councilflow-to-specreview-rename.md returns empty

