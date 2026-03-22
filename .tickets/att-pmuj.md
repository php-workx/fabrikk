---
id: att-pmuj
status: closed
deps: [att-av4u]
links: []
created: 2026-03-21T10:02:16Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-40wv
tags: [memory, cli, extraction]
---
# Wire extraction into CLI (council review + verify commands)

After cmdTechSpecReview council completes: call extractLearningsFromCouncil. After cmdVerify with failures: call extractLearningsFromVerifier. Dedup by finding:ID tag. Cap at 5 learnings per extraction. Print extracted count.

## Acceptance Criteria

attest tech-spec review extracts learnings; attest verify extracts on failure

