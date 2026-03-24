---
id: att-8cno
status: closed
deps: []
links: []
created: 2026-03-24T05:17:17Z
type: bug
priority: 0
assignee: Ronny Unger
tags: [bug, normalization, P1]
---
# Propagate --no-normalize flag to cmdTechSpecReviewFromFile

## What's wrong

The --no-normalize flag is parsed in cmdTechSpec (main.go:241) but not passed
through to cmdTechSpecReviewFromFile. Call sites at main.go:333 and main.go:366
hardcode `false` instead of forwarding f.noNormalize.

This means `attest tech-spec review --from <path> --no-normalize` still attempts
agent normalization — the most common usage path for the flag.

## Risk

Users who explicitly opt out of normalization still get their docs transformed.
The flag silently does nothing for the review-from-file shortcut, which is the
primary way external specs enter the pipeline.

## Acceptance criteria

- cmdTechSpecReviewFromFile receives and forwards the noNormalize flag
- `attest tech-spec review --from <path> --no-normalize` passes the document through unchanged
- `attest tech-spec review --from <path>` (without flag) still attempts agent normalization

## Test cases

1. Call `cmdTechSpec(ctx, ["review", "--from", "non-canonical.md", "--no-normalize"])` → verify agent InvokeFunc is never called, document preserved as-is
2. Call `cmdTechSpec(ctx, ["review", "--from", "non-canonical.md"])` → verify agent InvokeFunc IS called (normalization attempted)

