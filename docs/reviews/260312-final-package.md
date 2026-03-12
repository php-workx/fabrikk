# Technical Spec Review Run

Date: 2026-03-12
Spec: `docs/specs/attest-technical-spec.md` v0.1
Functional spec: `docs/specs/attest-functional-spec.md` v0.1

---

## Round 1 Outputs

- Systems Architect: [`docs/reviews/260312-systems-architect.md`](./260312-systems-architect.md)
  - 4 critical, 8 high, 7 medium, 4 low findings
  - Key blockers: no process model, no concurrency safety, no state transitions, no git isolation
- Spec-Conformance: [`docs/reviews/260312-spec-conformance.md`](./260312-spec-conformance.md)
  - 3 critical, 5 high, 6 medium, 2 low findings
  - Key blockers: no run-level closure rule, verifier checks existence not content, deferral mechanism undefined
- Agent Workflow: [`docs/reviews/260312-agent-workflow.md`](./260312-agent-workflow.md)
  - 2 critical, 4 high, 5 medium, 3 low findings
  - Key blockers: no survivable process contract, no degraded-mode policy for mandatory reviewers

## Synthesis

- Full synthesis: [`docs/reviews/260312-synthesis.md`](./260312-synthesis.md)
- **Verdict: FAIL**
- Blocking findings (6):
  1. **B-01** [critical] Detached execution has no process model or liveness contract
  2. **B-02** [critical] File-based state has no concurrency safety or corruption recovery
  3. **B-03** [critical] Run and task state machines lack transition rules
  4. **B-04** [critical] Verifier validates reporting, not reality
  5. **B-05** [critical] No run-level closure rule; requirement coverage can be orphaned
  6. **B-06** [critical] External-backend failure and quorum policy are contradictory and undefined
- Required spec edits (before implementation planning):
  1. Add "Run engine lifecycle" section (process model, PID, liveness, reboot behavior)
  2. Add "State mutation model" section (single-writer, atomic writes, corruption recovery)
  3. Replace state lists with full transition tables (source, target, trigger, guard, terminal)
  4. Strengthen verifier to check independent evidence (exit codes, git diff, required_evidence field)
  5. Add "Run closure rule" section (requirement coverage, deferral mechanism, user approval)
  6. Resolve backend failure/quorum contradiction (pick unanimity or quorum, define retry/timeout/block, add preflight check)

## Follow-Up Round

- Needed: **no**
- Reason: All disagreements were resolved by the synthesis. The one open question (D-01: serial vs concurrent worker execution) requires a product decision from the spec author, not additional reviewer analysis. No reviewer disputed another reviewer's finding — they surfaced complementary issues from different lenses.

## Final Recommendation

- Ready for implementation planning: **no**
- Why: Six critical blocking findings must be resolved first. The spec is architecturally sound in its policy design (approval boundary, closure rule, multi-model review, fail-closed defaults), but systematically underspecified on execution mechanics. The gap pattern is consistent: the spec says *what should be true* but not *how it becomes true* when real processes run on real files.

## Action List for Spec Author

Priority order (each edit unlocks downstream decisions):

1. **Define the run engine process model** — this is the single biggest unresolved question. Everything else (liveness, resume, heartbeats, reattachment) depends on it.
2. **Write state transition tables** — run states and task states, with triggers and guards. Mark terminal states. This unlocks resume, stop, and dispatcher logic.
3. **Define the state mutation model** — single-writer rule, atomic writes, claim serialization. This unlocks the concurrency story.
4. **Resolve the quorum/unanimity contradiction** — pick one, define failure behavior, add preflight checks. This unlocks the backend failure story.
5. **Strengthen the verifier** — distinguish self-reported from independently checkable evidence. Define required_evidence per task. This unlocks meaningful verification.
6. **Add the run-level closure rule** — requirement coverage map, deferral artifact with user approval. This closes the most dangerous false-complete path.

Additionally, decide whether v1 workers execute serially or concurrently. If concurrent: add git worktree isolation (currently N-01/high). If serial: state that explicitly and enforce it in the dispatcher.

The 20 non-blocking findings (N-01 through N-20) can be addressed during implementation planning, though the high-severity ones (N-01 through N-08) should be resolved early.
