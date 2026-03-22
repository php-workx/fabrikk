---
id: att-n3ad
status: closed
deps: [att-3fn2, att-x5i8]
links: []
created: 2026-03-21T11:01:04Z
type: chore
priority: 2
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, docs, p3]
---
# Update spec §3.1 and §11 to match implementation

Six documentation drift items between spec and implementation:
1. §3.1 Store struct shows only Dir+Now — missing OnMaintain, lastMaintainedAt, maintaining, mu
2. §3.1 GarbageCollect — now delegates to Maintain (different behavior)
3. §9.2.3 dedup by DedupeKey — impl uses finding tag (will be fixed by att-x5i8)
4. §9.4 extraction function signatures — impl takes wd string, not store pointer
5. §11.2.2 lists MaturityAntiPattern — not implemented (will be resolved by att-3fn2)
6. §11.3 Maintain signature — impl takes maxAge, returns pointer+error

## Acceptance Criteria

1. §3.1 Store struct updated with current fields
2. §3.1 GarbageCollect notes delegation to Maintain
3. §9.4 extraction signatures match implementation
4. §11.3 Maintain signature matches implementation
5. All code examples in spec compile-check against actual types

