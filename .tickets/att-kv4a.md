---
id: att-kv4a
status: closed
deps: []
links: []
created: 2026-03-20T16:09:48Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-2nve
tags: [memory, significant]
---
# Auto-populate SourcePaths from SourceTask's OwnedPaths

Spec §3.1: 'When a learning is created with a SourceTask, SourcePaths is copied from that task's Scope.OwnedPaths'. Store.Add does not look up the task. This requires Add to accept a TaskStore or the caller to resolve paths before calling Add. Preferred approach: have the CLI and engine callers resolve OwnedPaths before passing to Add (keeps Store independent of TaskStore). Document this contract.

## Acceptance Criteria

When a learning is added with --source-task, SourcePaths is populated from that task's OwnedPaths; unit test verifies SourcePaths population; works via both CLI and engine paths

