---
id: att-74rb
status: closed
deps: []
links: []
created: 2026-03-21T11:01:05Z
type: chore
priority: 3
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, validation, p3]
---
# Validate Maturity field values in Store.Add

No validation that Maturity field is one of the known values (provisional/candidate/established). Arbitrary strings accepted. Low risk — only internal callers set this field, and defaults handle empty. But inconsistent with Category which IS validated.

## Acceptance Criteria

1. Store.Add validates Maturity is empty or one of the 3 known values
2. Unknown maturity returns error
3. Test: Add with maturity='bogus' → error

