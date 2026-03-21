---
id: att-lom4
status: closed
deps: []
links: []
created: 2026-03-20T16:09:43Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-2nve
tags: [memory, significant]
---
# Implement keyword extraction from learning content for tag index

Spec §3.1: 'Keywords extracted from content (top 10 significant words) are also indexed' in tags.json. rebuildIndex only indexes l.Tags, not content keywords. Fix: implement keyword extraction (top 10 significant words by frequency, excluding stop words) in rebuildIndex. Consider creating internal/learning/index.go as spec §4 intended. Keywords should be indexed alongside explicit tags in the inverted index.

## Acceptance Criteria

rebuildIndex extracts keywords from Content field; keywords appear in tags.json alongside explicit tags; query by keyword matches relevant learnings; unit test covers keyword extraction

