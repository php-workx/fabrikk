---
id: att-fyit
status: closed
deps: []
links: []
created: 2026-03-24T09:21:25Z
type: task
priority: 1
assignee: Ronny Unger
tags: [rebrand, P1]
---
# Rename spec files from attest-* to fabrikk-*

## What's wrong
docs/specs/attest-functional-spec.md and docs/specs/attest-technical-spec.md
still use old filenames and titles. README links to these files will break
after README is updated.

## Risk
Broken links from README. Old product name in spec titles confuses readers.

## Note (pm-003)

Archive docs in docs/plans/done/ reference the old filenames. These are
frozen historical snapshots — do NOT update them. Broken references in
archives are acceptable.

## Acceptance criteria
- attest-functional-spec.md renamed to fabrikk-functional-spec.md, title updated
- attest-technical-spec.md renamed to fabrikk-technical-spec.md, title updated
- Product references within spec content updated to fabrikk
- ls docs/specs/attest-* returns empty
- docs/plans/done/ files left unchanged (historical archives)

