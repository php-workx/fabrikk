---
id: att-9mzd
status: closed
deps: []
links: []
created: 2026-03-21T10:59:52Z
type: task
priority: 0
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, corruption, p1]
---
# Check for corrupt JSONL lines before rewriting in Add and RecordCitation

Add() and RecordCitation() call readAll() which silently skips corrupt lines, then writeAll() which rewrites — permanently dropping unreadable entries. Spec §3.1 Corruption Handling says mutating ops MUST return ErrCorruptLearningStore and MUST NOT rewrite. Only Maintain() uses readAllWithCount() correctly. Risk: silent data loss on any store mutation when corruption exists.

Fix: Replace readAll() calls in Add/RecordCitation withLock closures with readAllWithCount(). If skipped > 0, return ErrCorruptLearningStore instead of proceeding. Keep readAll() for read-only paths (Query, Get) where partial results with warning are acceptable per spec.

## Acceptance Criteria

1. Add() returns ErrCorruptLearningStore when index.jsonl has corrupt lines
2. RecordCitation() returns ErrCorruptLearningStore when index.jsonl has corrupt lines
3. Neither rewrites index.jsonl or tags.json when corruption detected
4. Test: add valid learning, append corrupt line, call Add() → error, verify original+corrupt lines preserved
5. Test: same with RecordCitation()
6. Query/Get still work with partial results (existing behavior unchanged)

