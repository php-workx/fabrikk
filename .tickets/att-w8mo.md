---
id: att-w8mo
status: closed
deps: []
links: []
created: 2026-03-24T09:18:02Z
type: chore
priority: 3
assignee: Ronny Unger
tags: [rebrand, docs, P3]
---
# Update active plan documents and tickets for fabrikk rebrand

## What's wrong

Active (non-archived) plan documents and tickets still reference old CLI name,
module paths, and directory names:

1. docs/plans/agent-based-spec-normalization.md: "attest tech-spec" commands,
   "cmd/attest/main.go" paths (lines 8, 130, 133-135, 204-205, 244-255)
2. docs/plans/councilflow-to-specreview-rename.md: old module path
   "github.com/runger/attest" (lines 120, 123, 126, etc.)
3. docs/plans/fabrikk-branding.md: rename checklist still shows unchecked
   items that are already done (lines 224-235)
4. .tickets/att-j63b.md: references "cmd/attest/commands_test.go"

## Risk

Low — these are internal planning documents. But they create confusion when
reading plans to understand current state or planning future work.

## Acceptance criteria

- docs/plans/agent-based-spec-normalization.md: CLI examples use "fabrikk",
  file paths use "cmd/fabrikk/"
- docs/plans/councilflow-to-specreview-rename.md: module paths updated to
  "github.com/php-workx/fabrikk"
- docs/plans/fabrikk-branding.md: completed checklist items marked [x]
- grep -rn 'cmd/attest' docs/plans/ returns empty (excluding done/ archives)
- grep -rn 'github.com/runger/attest' docs/plans/ returns empty (excluding done/)

## Test cases

1. grep -rn 'cmd/attest\|bin/attest' docs/plans/*.md returns empty
2. grep -rn 'runger/attest' docs/plans/*.md returns empty
3. fabrikk-branding.md checklist shows completed items as [x]

