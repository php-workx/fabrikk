---
id: att-a087
status: closed
deps: []
links: []
created: 2026-03-24T09:17:48Z
type: task
priority: 2
assignee: Ronny Unger
tags: [rebrand, config, P2]
---
# Fix stale symlink and config comments for fabrikk rebrand

## What's wrong

Two config-level references still use the old name:

1. .claude/skills/attest symlink: points to ../../skills/attest which no longer
   exists (skill directory is now skills/fabrikk/). Claude Code skill loading
   will fail silently.
2. .golangci.yml: two comments reference "attest" (line 3: "configuration for
   attest", line 91: "attest is a file-management tool"). Cosmetic but confusing
   when reading linter config.

## Risk

The stale symlink means the Claude Code skill (skills/fabrikk/SKILL.md) won't
load in sessions that use .claude/skills/. The golangci comments are cosmetic.

## Acceptance criteria

- .claude/skills/attest removed, .claude/skills/fabrikk symlink exists pointing
  to ../../skills/fabrikk
- .golangci.yml comments updated to reference "fabrikk"
- ls -la .claude/skills/ shows only fabrikk symlink

## Test cases

1. readlink .claude/skills/fabrikk returns ../../skills/fabrikk
2. ls .claude/skills/attest returns "No such file"
3. grep 'attest' .golangci.yml returns empty

