---
id: att-zgrz
status: closed
deps: []
links: []
created: 2026-03-20T16:10:03Z
type: task
priority: 2
assignee: Ronny Unger
parent: att-2nve
tags: [memory, moderate]
---
# Align LearningEnricher interface with spec signature

Spec §3.2 defines LearningEnricher as: Query(opts LearningQueryOpts) ([]LearningRef, error) + RecordCitation(id string) error. Implementation has QueryByTagsAndPaths(tags, paths []string, limit int). Also LearningQueryOpts type from spec is not defined in state/types.go. Options: (A) align to spec — define LearningQueryOpts, rename method to Query; (B) update spec to match implementation. Either way, document the contract. The current divergence makes the spec unreliable as a reference.

## Acceptance Criteria

LearningEnricher interface matches either the spec or a deliberately updated spec; LearningQueryOpts defined if spec approach chosen; spec updated if implementation approach chosen

