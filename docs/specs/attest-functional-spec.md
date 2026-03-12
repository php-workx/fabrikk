# attest Functional Specification

Version: 0.1
Status: Draft
Audience: Product, engineering, design, founder
Document type: Functional specification
Companion document: [attest-technical-spec.md](./attest-technical-spec.md)

---

## 1. Overview

### 1.1 Product summary

`attest` is a spec-driven autonomous run system for coding agents.

It takes one or more source spec files, normalizes them into an approved run artifact, compiles small isolated work items, executes those items through agent workers, and keeps reconciling the implementation against the approved spec until all requirements are verified or a true blocker is reached.

### 1.2 Product thesis

The core failure mode in current agent-driven delivery is not lack of planning. It is false closure.

Teams can write detailed functional specs, technical specs, and work breakdowns, then still end up with code that only partially satisfies the source requirements because:

- work items drift from the original spec
- implementers overclaim completion
- verification is either absent or too easy to fake
- users cannot safely step away and trust that the remaining loop will keep correcting itself

`attest` exists to make completion evidence-backed, traceable, and self-correcting.

### 1.3 Core promise

`attest` does not promise perfect implementation. It promises a stronger operational contract:

- every task must trace to stable requirement IDs
- every completion claim must be backed by verification artifacts
- every meaningful disagreement reopens or creates repair work
- a run does not finish because one agent says "done"

## 2. Goals

### 2.1 Primary goals

1. Turn approved spec files into a durable, traceable execution contract.
2. Support autonomous background runs that continue after the initiating agent session ends.
3. Keep users inside familiar agent CLI workflows instead of forcing a new front-end.
4. Prevent overlapping multi-agent work through task sizing, scoping, and lease-based claims.
5. Use multi-model review as a first-class quality gate, not a final afterthought.
6. Lower unattended failure rates by automatically opening repair work when verification fails.

### 2.2 Secondary goals

1. Make run state inspectable and resumable from a later session.
2. Use existing user subscriptions where possible instead of defaulting to paid APIs.
3. Provide enough structure for reproducibility without demanding rigid prose authoring on day one.
4. Keep the product small enough that an individual developer can run it locally.

## 3. Non-goals

v1 will not:

- replace Claude Code, Codex CLI, or Gemini CLI as the user's primary interface
- be a generic issue tracker
- require Beads, Linear, or another external tracker as the source of truth
- guarantee deterministic planning directly from arbitrary prose without a user approval checkpoint
- guarantee perfect correctness against every spec ambiguity
- act as a general workflow engine for non-coding tasks
- make all vendors feature-identical in v1

## 4. Personas

### 4.1 Primary persona: spec-first solo builder

- writes detailed functional and technical specs before implementation
- already works inside Claude Code or similar tools
- wants to approve the direction once, then walk away
- expects returned work to be materially correct against the spec

### 4.2 Secondary persona: technical lead running several agents

- needs multi-agent execution without accidental overlap
- cares about exact status, blockers, and requirement coverage
- wants an auditable path from source spec to completed code

### 4.3 Tertiary persona: quality-focused reviewer

- trusts model disagreement as a useful signal
- wants Codex and Gemini involved in review even when Claude is the main implementer
- cares more about correctness than minimal token cost

## 5. Product boundaries

### 5.1 Included in v1

- spec ingestion from one or more files
- clarification and normalization before execution
- explicit approval boundary before autonomous execution begins
- deterministic task compilation from the approved run artifact
- autonomous detached execution
- deterministic verification
- Codex and Gemini reviews
- Opus synthesis and final council decisions
- run reattachment and status inspection

### 5.2 Excluded from v1

- SaaS-hosted orchestration backend
- web dashboard
- org-wide policy administration
- direct code editing by Codex and Gemini as first-class implementers
- CrewAI as the core runtime
- full scheduling or queue management across many machines

## 6. Core concepts

### 6.1 Source specs

Source specs are human-authored input files. They may remain mostly free-form, but they must contain stable requirement identifiers for the parts that `attest` will track and verify.

### 6.2 Run artifact

The run artifact is the approved normalized interpretation of the source specs. It is the execution contract for a specific run.

The run artifact contains:

- normalized requirements
- source references
- clarified assumptions
- dependencies
- risk classification
- initial task compilation inputs
- model-routing hints

### 6.3 Task graph

The task graph is the initial decomposition of the run artifact into small isolated work items.

The graph may evolve during execution when verifier or council findings create repair tasks, but the initial graph must compile deterministically from the approved run artifact.

### 6.4 Claim lease

A claim lease is the exclusive right of one worker to own a task for a bounded period of time. Claims prevent overlapping work on the same task.

### 6.5 Verification

Verification is the deterministic evidence check between implementation and closure.

It validates:

- linked requirement coverage
- required commands run
- expected outputs or artifacts
- test execution
- known gaps

### 6.6 Council

The council is the independent multi-model review stage. In v1 it includes:

- Claude Opus as synthesizer and Claude-side strategic reviewer
- Codex as independent code and test reviewer
- Gemini as independent spec and edge-case reviewer

## 7. High-level workflow

### 7.1 Planning and approval

1. User provides one or more source spec files.
2. `attest` analyzes the specs and extracts requirements, ambiguities, dependencies, and candidate task clusters.
3. `attest` asks clarification questions if needed.
4. `attest` produces a normalized run artifact.
5. User reviews and approves the artifact.

### 7.2 Autonomous execution

6. `attest` compiles the approved artifact into an initial task graph.
7. `attest` launches a detached run.
8. Workers claim tasks and implement them.
9. Deterministic verification runs.
10. Codex and Gemini review the result.
11. Claude Opus synthesizes findings.
12. If gaps remain, `attest` reopens or creates repair tasks automatically.
13. The loop repeats until requirements are verified or a true blocker is reached.

### 7.3 Reattachment

14. A later agent session can reattach to the run.
15. `attest` explains run status, blockers, open tasks, uncovered requirements, and reviewer findings.

## 8. Functional requirements

### 8.1 Spec intake and normalization

- **AT-FR-001**: The system must ingest one or more source spec files in a single run preparation flow.
- **AT-FR-002**: The system must require stable requirement IDs in the portions of the source specs that are tracked for execution.
- **AT-FR-003**: The system must extract requirements, assumptions, dependencies, ambiguities, and candidate task groupings into a normalized run artifact.
- **AT-FR-004**: The system must support a user approval boundary before autonomous execution begins.
- **AT-FR-005**: The system must treat the approved run artifact, not raw prose, as the source of truth for the run.

### 8.2 Task generation and isolation

- **AT-FR-006**: The system must compile an initial task graph deterministically from the approved run artifact.
- **AT-FR-007**: Each task must reference one or more requirement IDs from the approved run artifact.
- **AT-FR-008**: Each task must include enough scope metadata to reduce overlapping edits.
- **AT-FR-009**: The system must prevent multiple workers from owning the same task concurrently.
- **AT-FR-010**: The system must serialize, split, or otherwise prevent obviously overlapping task execution.

### 8.3 Autonomous detached execution

- **AT-FR-011**: The system must launch autonomous runs from within a familiar agent CLI workflow.
- **AT-FR-012**: The autonomous run must continue after the initiating agent session ends.
- **AT-FR-013**: The system must persist run state locally so a later session can inspect or resume it.
- **AT-FR-014**: The system must stop for a user decision only when a true blocker is reached.

### 8.4 Verification and repair

- **AT-FR-015**: A task must not close solely because the implementer reports completion.
- **AT-FR-016**: Each task must produce a structured completion report.
- **AT-FR-017**: Deterministic verification must check requirement coverage, evidence, and command execution.
- **AT-FR-018**: If deterministic verification fails, the task must reopen or produce repair work automatically.
- **AT-FR-019**: Repeated failures must escalate to a stronger reasoning path instead of looping indefinitely at the same level.

### 8.5 Multi-model review and council

- **AT-FR-020**: Codex review must run for task-level implementation validation in v1.
- **AT-FR-021**: Gemini review must run for task-level spec and edge-case validation in v1.
- **AT-FR-022**: Claude Opus must synthesize reviewer findings before closure.
- **AT-FR-023**: The system must support council checkpoints at least for spec review, task review, task verification, and final run acceptance.
- **AT-FR-024**: The system must treat unresolved blocking disagreement conservatively and keep the work open.

### 8.6 Model routing

- **AT-FR-025**: The system must choose Claude model tier based on task class, not one fixed model for every step.
- **AT-FR-026**: Planning, strategic review, and synthesis must default to Opus.
- **AT-FR-027**: Implementation and repair work must default to Sonnet.
- **AT-FR-028**: Lightweight scouting and information gathering must default to Haiku.
- **AT-FR-029**: The system must record routing and escalation decisions for later inspection.

### 8.7 Status, reattachment, and explainability

- **AT-FR-030**: A later agent session must be able to list active runs and inspect one without depending on the original session.
- **AT-FR-031**: The system must explain open blockers in plain language and link them to affected requirements and tasks.
- **AT-FR-032**: The system must show uncovered requirement IDs for any incomplete run.
- **AT-FR-033**: The system must show which reviewer or verifier blocked a task and why.

## 9. User journeys

### 9.1 Journey A: Start a new run

1. User writes or selects one or more spec files.
2. User asks the current agent session to prepare an `attest` run.
3. `attest` analyzes the specs and asks targeted clarification questions.
4. `attest` presents a normalized run artifact.
5. User approves the artifact.
6. `attest` compiles an initial task graph and launches the run.

### 9.2 Journey B: Detached completion

1. User leaves the terminal after run launch.
2. Workers continue implementation, verification, and repair loops in the background.
3. Codex and Gemini review task outputs.
4. Opus synthesizes findings and opens repair work when needed.
5. When the run converges, it reaches a verified terminal state.

### 9.3 Journey C: Reattach after a blocker

1. User opens a fresh agent session later.
2. User asks for run status.
3. `attest` reports:
   - current phase
   - open tasks
   - uncovered requirements
   - reviewer disagreements
   - true blockers needing user input

## 10. Non-functional requirements

- **AT-NFR-001**: v1 must work from local agent CLI workflows without requiring hosted orchestration.
- **AT-NFR-002**: Run state must survive process exit and session loss.
- **AT-NFR-003**: The user-facing control surface must remain understandable from conversational status output.
- **AT-NFR-004**: The system must favor correctness over cheapest-token routing when the two are in tension.
- **AT-NFR-005**: The system must still use lower-cost model tiers for low-risk scouting and routine implementation by default.
- **AT-NFR-006**: The system must be transparent about what is approved, verified, blocked, and assumed.

## 11. Acceptance scenarios

- **AT-AS-001**: Given the same approved run artifact, the system produces the same initial task graph.
- **AT-AS-002**: A run continues after the initiating session ends.
- **AT-AS-003**: A task does not close when only the implementer reports success.
- **AT-AS-004**: Verification failures create repair work automatically.
- **AT-AS-005**: Codex or Gemini can block closure even after deterministic verification passes.
- **AT-AS-006**: A later session can reattach and explain the exact reason the run is blocked.
- **AT-AS-007**: A run finishes only when every requirement in the approved artifact is verified or explicitly blocked.

## 12. Success criteria

`attest` is successful if users can:

- approve a run once and step away
- return later and find materially spec-conformant output
- understand exactly what remains open or blocked without re-reading the entire transcript
- rely on the system to keep pushing back against false completion claims

## 13. Assumptions

- v1 is Claude-first for execution.
- Codex and Gemini are mandatory secondary reviewers in v1.
- Source specs can evolve toward stable requirement IDs, but the product should not require fully rigid prose structures on day one.
- The user values trustworthy closure more than maximum task throughput.
