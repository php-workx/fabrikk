---
id: att-3fn2
status: closed
deps: [att-m824]
links: []
created: 2026-03-21T11:00:39Z
type: task
priority: 1
assignee: Ronny Unger
parent: att-1ko9
tags: [memory, quality-gate, p2]
---
# Filter anti_pattern maturity learnings from injection

Spec §11.2.4: 'maturity ∈ {provisional, candidate, established} // not anti_pattern or expired AND utility > 0.3 AND NOT superseded AND NOT expired'. But §11.2.2 lists MaturityAntiPattern as a maturity level while the implementation only has 3 levels (no anti_pattern maturity).

Clarification: Category=anti_pattern learnings should still be injected as Warnings (§3.2 behavior). Only learnings whose Maturity has been demoted to a hypothetical 'demoted' state should be excluded. Since we only have 3 maturity levels, the quality gate simplifies to: utility > 0.3 (already applied via MinUtility in QueryLearnings) + not expired/superseded (already filtered in Query).

Fix: Remove MaturityAntiPattern from spec §11.2.2 since it's not implemented. The current Query filtering (expired + superseded + MinUtility=0.1) is close but MinUtility threshold should be 0.3 for injection paths. Update QueryLearnings to use MinUtility=0.3.

## Acceptance Criteria

1. QueryLearnings uses MinUtility=0.3 (not 0.1)
2. enrichTaskWithLearnings and AssembleContext respect the 0.3 threshold
3. Test: learning with utility=0.2 excluded from enrichment
4. Test: learning with utility=0.4 included
5. Spec §11.2.2 updated: remove MaturityAntiPattern, clarify quality gate

