---
id: att-rg4d
status: closed
deps: []
links: []
created: 2026-03-21T10:02:16Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-40wv
tags: [memory, types]
---
# Add Source, SourceFinding, Maturity fields to Learning type

Add Source string (manual/council/verifier/rejection), SourceFinding string (finding ID), Maturity (provisional/candidate/established) to Learning struct in types.go. Set defaults in Store.Add when empty. All existing tests must pass.

## Acceptance Criteria

Fields exist on Learning; Add sets defaults; go test passes

