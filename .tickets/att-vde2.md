---
id: att-vde2
status: closed
deps: []
links: []
created: 2026-03-20T16:10:42Z
type: chore
priority: 3
assignee: Ronny Unger
parent: att-2nve
tags: [memory, low, error-handling]
---
# Log warning when corrupt JSONL lines are skipped in readAll

store.go readAll() (line 340) silently continues past corrupt JSONL lines with 'continue // skip corrupt lines'. Users have no way to know data was lost or corrupted. Fix: either log to stderr, count skipped lines and return a warning error (similar to state.ErrPartialRead pattern), or collect line numbers for reporting. Spec is silent on this but the ErrPartialRead pattern from state/ is a good precedent.

## Acceptance Criteria

Corrupt JSONL lines produce a visible warning (stderr or returned error); user can identify which lines were skipped; non-corrupt lines still load successfully

