---
id: att-rbg7
status: closed
deps: [att-7aio]
links: []
created: 2026-03-19T15:25:30Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [claim-wave-2]
---
# Implement claim.go — ClaimTask, ReleaseClaim, RenewClaim, ReadClaimsForRun, ReclaimExpired

New file internal/ticket/claim.go. All 5 Store methods + ClaimInfo struct + DefaultLeaseDuration + marshalFrontmatterAndBody helper. Each method uses withLock for atomic read-check-write. See docs/plans/ticket-claim-release.md section 5.3 for pseudocode.

