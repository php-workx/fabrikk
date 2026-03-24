---
id: att-ks5s
status: closed
deps: []
links: []
created: 2026-03-24T05:17:45Z
type: chore
priority: 3
assignee: Ronny Unger
tags: [docs, observability, P3]
---
# Add --no-normalize to usage string and normalization success log

## What's wrong

Two minor gaps in user-facing documentation and observability:

1. **Usage string missing --no-normalize** (main.go:268): The usage/help text
   shows [--from <path>] [--council] [--force] [--round N] but doesn't mention
   --no-normalize. Users won't discover the flag from help output.

2. **No "normalization succeeded" log**: When the agent successfully normalizes a
   doc, the user sees "Document doesn't match canonical headings — attempting agent
   normalization ..." then silence until "Technical spec recorded: ...". There's no
   confirmation that normalization actually happened vs. fell back silently. A one-line
   "Agent normalization applied successfully" message would close the gap.

## Risk

Low. Users may not discover the flag; they may not know if normalization ran.
Neither causes data loss or incorrect behavior.

## Acceptance criteria

- Usage string at main.go:268 includes --no-normalize
- After successful agent normalization, a message like
  "Agent normalization applied (N canonical headings detected)" is printed
- After canonical fast-path (no normalization needed), no message is printed

## Test cases

1. Run `attest tech-spec --help` or trigger usage error → verify --no-normalize
   appears in output
2. Mock agent to return valid normalized spec → verify success message printed
   (check stdout capture in test)

