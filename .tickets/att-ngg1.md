---
id: att-ngg1
status: closed
deps: [att-fyit]
links: []
created: 2026-03-24T09:21:16Z
type: task
priority: 1
assignee: Ronny Unger
tags: [rebrand, P1]
---
# Update README.md for fabrikk rebrand

## What's wrong
README.md title is "# attest", CLI examples use ./bin/attest, architecture
diagram shows cmd/attest/, spec links point to attest-*.md files.

## Risk
First thing anyone sees on the GitHub repo. Looks like a broken rename.

## Acceptance criteria
- Title is "# fabrikk"
- CLI examples use fabrikk (or fab)
- Architecture shows cmd/fabrikk/
- Spec links point to fabrikk-*.md
- grep 'attest' README.md returns only intentional refs (requirement IDs, attest_status)

