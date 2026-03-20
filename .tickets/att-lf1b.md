---
id: att-lf1b
status: closed
deps: [att-rbg7]
links: []
created: 2026-03-19T15:25:50Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-jndm
tags: [claim-wave-2]
---
# Add claim_test.go — 15 claim lifecycle tests

New file internal/ticket/claim_test.go. 15 tests covering: claim success, already claimed, same-owner reclaim, expired reclaim, non-claimable status, release success/wrong-owner/unclaimed, renew success/wrong-owner, ReadClaimsForRun, ReclaimExpired, claim preserved through UpdateStatus, concurrent claim race (10 goroutines).

