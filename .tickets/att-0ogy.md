---
id: att-0ogy
status: closed
deps: [att-rbg7, att-drm1]
links: []
created: 2026-03-19T15:25:43Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [claim-wave-3]
---
# Add ClaimableStore interface + engine claim methods

Add ClaimableStore interface to state/types.go. Add ClaimAndDispatch, ReleaseTask, SweepExpiredClaims to engine. Engine type-asserts to ClaimableStore with RunDir fallback. Pass lease as parameter (no engine→ticket import). Add 2 engine tests.

