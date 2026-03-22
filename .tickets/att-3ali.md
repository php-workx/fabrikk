---
id: att-3ali
status: closed
deps: []
links: []
created: 2026-03-20T16:10:15Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-2nve
tags: [memory, moderate, bug]
---
# Fix Confidence/Utility zero-value override in Store.Add

Store.Add (store.go:48-53) checks 'l.Confidence == 0' and 'l.Utility == 0' to set defaults of 0.5. If a caller explicitly passes Confidence: 0.0 or Utility: 0.0, the values are silently overridden. Fix: use a separate 'defaults already set' check. Options: (A) check for zero-value time (CreatedAt.IsZero already used this pattern) alongside a boolean; (B) only set defaults when the entire Learning is freshly constructed (ID == empty); (C) document that 0.0 is reserved as 'use default'. Option B is simplest since ID=empty already indicates a new learning.

## Acceptance Criteria

Explicitly passing Confidence: 0.0 or Utility: 0.0 is preserved; default of 0.5 still applied when values are not set; test verifies both cases

