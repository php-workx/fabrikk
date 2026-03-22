---
id: att-9rw1
status: closed
deps: []
links: []
created: 2026-03-21T10:59:52Z
type: task
priority: 0
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, paths, p1]
---
# Normalize SourcePaths to repo-root-relative with forward slashes

Spec §3.1: 'All SourcePaths and query paths MUST be repo-root-relative, normalized with filepath.Clean, converted to forward-slash separators, and compared case-sensitively. CLI absolute paths are converted to repo-relative before storage.'

Not implemented anywhere. Paths stored as-is from task.Scope.OwnedPaths or CLI --path flags. Risk: path matching fails silently when paths use different formats (absolute vs relative, OS separators).

Fix: Add normalizePath(path, repoRoot string) string helper in learning package. Call it:
1. In Store.Add() on each SourcePaths entry
2. In Store.Query() on each QueryOpts.Paths entry  
3. In cmdLearnAdd when processing --path flags
4. In enrichTaskWithLearnings when passing OwnedPaths

## Acceptance Criteria

1. filepath.Clean applied to all stored SourcePaths
2. Forward slashes used (filepath.ToSlash)
3. Absolute paths converted to repo-relative
4. Test: Add learning with '/Users/x/repo/internal/engine', query with 'internal/engine' → matches
5. Test: Add with 'internal\engine' (Windows sep), query with 'internal/engine' → matches
6. Test: Add with './internal/../internal/engine', query with 'internal/engine' → matches

