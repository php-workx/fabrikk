---
id: att-av4u
status: closed
deps: []
links: []
created: 2026-03-21T10:02:16Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-40wv
tags: [memory, extraction]
---
# Implement extraction functions (LearningFromFinding, LearningFromRejection, LearningFromVerifierFailure)

NEW file internal/learning/extract.go with: ExtractedFinding/ExtractedRejection structs, LearningFromFinding (council findings → learnings), LearningFromRejection (judge rejections → pattern learnings), LearningFromVerifierFailure (verifier failures → learnings). 6 tests in extract_test.go.

## Acceptance Criteria

6 extraction tests pass; functions map severity/category correctly

