---
id: att-mj9y
status: closed
deps: [att-9mzd]
links: []
created: 2026-03-21T10:59:52Z
type: task
priority: 0
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, corruption, p1]
---
# Implement attest learn repair command

Spec §3.1 references 'attest learn repair' for corruption recovery. Maintain() error message says 'run attest learn repair to fix'. But the command doesn't exist — there's no 'repair' case in cmdLearn. Risk: users hit corruption, see the error message, but can't fix it without manual JSON editing.

Fix: Add 'repair' sub-command that reads index.jsonl, drops corrupt lines, rewrites. Print count of dropped/kept lines. Optionally back up the corrupt file first.

## Acceptance Criteria

1. 'attest learn repair' exists as CLI command
2. Reads index.jsonl, drops lines that fail JSON decode
3. Rewrites index.jsonl with only valid lines
4. Rebuilds tags.json
5. Backs up corrupt file as index.jsonl.corrupt.TIMESTAMP
6. Prints: 'Repaired: kept N learnings, dropped M corrupt lines'
7. Test: create store with valid+corrupt lines, run repair, verify valid lines preserved

