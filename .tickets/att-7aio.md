---
id: att-7aio
status: closed
deps: []
links: []
created: 2026-03-19T15:25:17Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-jndm
tags: [claim-wave-1]
---
# Add claim fields to Frontmatter + UpdateFrontmatter preservation

Add ClaimedBy, ClaimBackend, ClaimExpires, ClaimHeartbeat to Frontmatter struct. Preserve in UpdateFrontmatter alongside Links/Assignee/ExternalRef/Extra. Add ErrAlreadyClaimed, ErrNotClaimed, ErrNotClaimOwner, ErrNotClaimable to errors.go. No behavioral change — foundation only.

