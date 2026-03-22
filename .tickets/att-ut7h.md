---
id: att-ut7h
status: closed
deps: [att-tvwc, att-lvfq]
links: []
created: 2026-03-20T16:09:57Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-2nve
tags: [memory, moderate, testing]
---
# Add tests for enrichTaskWithLearnings, QueryByTagsAndPaths, MarshalTicket learnings, cmdContext, showLatestHandoff

Multiple integration paths are untested: (1) Engine.enrichTaskWithLearnings — no engine test exercises enrichment with a mock LearningEnricher. (2) Store.QueryByTagsAndPaths — only tested indirectly. (3) MarshalTicket with LearningContext — no test verifies the '## Context from Learnings' body section. (4) cmdContext — no CLI integration test. (5) showLatestHandoff in cmdNext — no test verifies handoff display in next output. (6) UpdateFrontmatter preserving learning fields — not exercised.

## Acceptance Criteria

Test for enrichTaskWithLearnings with mock enricher; test for QueryByTagsAndPaths directly; test for MarshalTicket rendering Context from Learnings section; integration test for cmdContext; test for showLatestHandoff in cmdNext; test for learning field round-trip through UpdateFrontmatter

