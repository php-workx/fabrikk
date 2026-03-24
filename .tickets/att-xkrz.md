---
id: att-xkrz
status: closed
deps: []
links: []
created: 2026-03-24T09:21:39Z
type: task
priority: 2
assignee: Ronny Unger
tags: [rebrand, P2]
---
# Fix stale .claude/skills/attest symlink

## What's wrong
.claude/skills/attest symlink points to ../../skills/attest which no longer
exists. Skill directory is now skills/fabrikk/.

## Risk
Claude Code skill loading fails silently — the fabrikk skill won't be
available in sessions.

## Acceptance criteria
- .claude/skills/attest removed
- .claude/skills/fabrikk symlink exists pointing to ../../skills/fabrikk
- readlink .claude/skills/fabrikk returns ../../skills/fabrikk

