---
id: att-rvke
status: closed
deps: []
links: []
created: 2026-03-24T09:58:18Z
type: task
priority: 2
assignee: Ronny Unger
tags: [rebrand, testing, P2]
---
# Update test fixtures from attest to fabrikk

## What's wrong

Test fixtures and mock data still use "attest" branding:

1. internal/engine/engine_test.go lines 377, 426: mock spec titled
   "# attest Technical Specification"
2. cmd/fabrikk/commands_test.go line 127: test fixture file path
   "specs/attest.md"

These are test inputs, not data format fields. They should reflect the
current product name.

## Acceptance criteria

- Mock spec titles use "# fabrikk Technical Specification"
- Test fixture paths use "fabrikk" not "attest"
- grep -rn '"# attest\|specs/attest' --include='*_test.go' returns empty
- All tests pass

