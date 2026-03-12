# attest

`attest` is a spec-driven autonomous run system for coding agents.

Its core promise is simple: implementation is not considered done because one agent says so. Work is only complete when it can be traced back to approved spec requirements, verified with evidence, and cleared by an independent council.

## Document Set

- `docs/specs/attest-functional-spec.md`
- `docs/specs/attest-technical-spec.md`
- `docs/plans/2026-03-12-attest-v1-implementation-plan.md`

## Product Summary

`attest` is designed for developers who already work inside agent CLIs such as Claude Code, Codex CLI, and Gemini CLI.

The intended workflow is:

1. Provide one or more spec files with stable requirement IDs.
2. Let `attest` analyze the spec and ask clarifying questions.
3. Review and approve the normalized run artifact.
4. Launch an autonomous run that decomposes work, dispatches agents, verifies output, opens repair tasks automatically, and keeps going until the implementation conforms to the approved spec or reaches a true blocker.
5. Reattach later from a normal agent CLI session and inspect exact status, blockers, and uncovered requirements.

## v1 Positioning

v1 is Claude-first for execution, with Codex CLI and Gemini CLI used as mandatory independent reviewers and council participants.

The product does not try to replace the user's preferred agent shell. The agent session remains the main control surface. `attest` provides the durable run state and orchestration logic underneath that workflow.
