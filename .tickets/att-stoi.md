---
id: att-stoi
status: closed
deps: []
links: []
created: 2026-03-24T09:17:37Z
type: task
priority: 1
assignee: Ronny Unger
tags: [rebrand, docs, P1]
---
# Rename spec files and update README/HOW-IT-WORKS for fabrikk rebrand

## What's wrong

User-facing documentation still references the old product name:

1. README.md: title "# attest", all ./bin/attest commands, cmd/attest/ architecture,
   links to docs/specs/attest-*.md
2. docs/specs/attest-functional-spec.md: filename + title + content references
3. docs/specs/attest-technical-spec.md: filename + title + content references
4. docs/HOW-IT-WORKS.md: title "# How attest Works" + product references

## Risk

Anyone visiting the GitHub repo sees "attest" everywhere despite the repo being
named "fabrikk". First impression is confusing — looks like a broken rename.

## Acceptance criteria

- README.md title is "# fabrikk", all CLI examples use fabrikk/fab, architecture
  shows cmd/fabrikk/, spec links point to fabrikk-*.md
- docs/specs/attest-functional-spec.md renamed to fabrikk-functional-spec.md,
  title and product references updated
- docs/specs/attest-technical-spec.md renamed to fabrikk-technical-spec.md,
  title and product references updated
- docs/HOW-IT-WORKS.md title is "# How fabrikk Works", product references updated
- grep -rn 'attest' README.md docs/HOW-IT-WORKS.md returns only intentional
  references (requirement IDs, attest_status field mentions)

## Test cases

1. Open README.md — no "attest" in title, CLI examples, or architecture diagram
2. Click spec links in README — they resolve to fabrikk-*.md files
3. grep -l 'attest-functional-spec\|attest-technical-spec' docs/specs/ returns empty

