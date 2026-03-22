---
id: att-tvwc
status: closed
deps: []
links: []
created: 2026-03-20T16:09:33Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-2nve
tags: [memory, significant]
---
# Implement ContextBundle type and Engine.AssembleContext method

Spec §3.4 defines a ContextBundle struct (TaskID, Learnings, Handoff, TokensUsed, TokenBudget) and Engine.AssembleContext(task) method. Neither exists. cmdContext in learn.go constructs an ad-hoc map[string]interface{} instead. Fix: define ContextBundle in internal/learning/types.go, implement AssembleContext on Engine (using LearningEnricher + handoff), refactor cmdContext to use it.

## Acceptance Criteria

ContextBundle type defined in learning/types.go; Engine.AssembleContext method exists and returns *ContextBundle; cmdContext delegates to AssembleContext; unit test for AssembleContext

