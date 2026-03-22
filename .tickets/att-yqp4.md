---
id: att-yqp4
status: closed
deps: []
links: []
created: 2026-03-21T11:01:04Z
type: chore
priority: 2
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, observability, p3]
---
# Log extraction count to run event stream

extractLearningsFromCouncil and extractLearningsFromVerifier print to stdout but don't log to the run event stream via AppendEvent. No observability for how many learnings were extracted from each review/verification.

## Acceptance Criteria

1. After extraction, append event with type 'learnings_extracted'
2. Detail includes count and source (council/verifier)
3. Test: verify event appears after extraction

