# Council Review Pipeline Technical Specification

## 1. Technical context

- Language/runtime: Go (same binary as attest CLI)
- Packaging/deployment model: Built into `attest` binary, invoked via `attest tech-spec review`
- Primary dependencies: External CLI tools (claude, codex, gemini) for LLM invocation; no Go library dependencies beyond stdlib
- Storage/persistence: File-based artifacts under `.attest/runs/<run-id>/council/`
- Testing strategy: Unit tests for prompt construction, JSON parsing, consensus logic, annotation parsing, drift detection; integration tests require live CLI backends
- Deterministic test mode: All acceptance scenarios MUST have a fixture-backed execution path that does not require network or live LLM access. Fixture-backed tests use recorded backend transcripts or stub CLI binaries returning predetermined JSON. Live CLI backend runs are advisory/manual validation and do not replace deterministic CI coverage.
- Performance constraints: Wall-clock time bounded by slowest parallel reviewer (~2-3 min); judge consolidation adds ~2 min for conflict resolution
- Security constraints: Spec content is sent to external LLM backends via the user's authenticated CLI tools. Codebase context inclusion is optional and controlled by the caller.

## 2. Architecture

### 2.1 Runtime components

- **Persona generator** (`dynpersona.go`):
  - responsibility: Analyze spec content and generate dynamic review personas (language engineer + domain experts)
  - boundaries: Reads spec text, produces persona configs. Does not execute reviews.

- **Prompt builder** (`prompt.go`):
  - responsibility: Assemble review prompts from persona config + spec + prior findings + codebase context
  - boundaries: Pure function, no I/O. Produces prompt strings.

- **Runner** (`runner.go`):
  - responsibility: Execute reviewers in parallel via CLI backends, manage caching, collect results
  - boundaries: Owns CLI invocation, stdout/stderr capture, JSON extraction. Does not interpret findings.
  - I/O contract: Reviewer and judge prompts MUST be delivered via stdin. Backend stdout is reserved for the JSON response payload and MUST be captured separately from stderr. Implementations MUST drain stdout and stderr concurrently (e.g., via separate goroutines) to prevent pipe deadlocks. Stderr MUST be persisted as a sidecar artifact (`review-<persona-id>.stderr.log`) for debugging. Backend routing is via a static registry (`KnownBackends`). Adding a new LLM backend requires adding its CLI invocation config to the registry — no plugin mechanism exists.

- **Judge** (`judge.go`):
  - responsibility: Cross-validate findings, produce accept/reject decisions with edit snippets, resolve conflicts
  - boundaries: Parallel judgment (one Opus instance per reviewer), sequential edit application, conflict resolution pass. Judge uses Claude Opus exclusively — edit quality requires the strongest available model. Unlike reviewers, the judge backend is not configurable via `BackendFor()` to avoid edit quality variance.

- **Annotation parser** (`annotation.go`):
  - responsibility: Extract `@@` annotations from spec markdown, block approval on unresolved annotations
  - boundaries: Pure parsing, no LLM. Integrated into approval gate.

- **Engine adapter** (`engine/techspec.go`):
  - responsibility: Mediate between CLI commands and councilflow components. Owns run directory resolution, spec file I/O, and approval record persistence.
  - boundaries: CLI dispatch calls `eng.CouncilReviewTechnicalSpec()`, `eng.ReviewTechnicalSpec()`, and `eng.ApproveTechnicalSpec()`. Passes `CouncilConfig` to councilflow. Does not contain review logic.

- **Council orchestrator** (`council.go`):
  - responsibility: Coordinate the full pipeline: persona generation → parallel reviews → parallel judge → conflict resolution → spec update
  - boundaries: Owns round loop and config. Delegates to runner, judge, and persona generator.

- **Package layout**: The council subsystem SHOULD live under `internal/techspec/council`. Orchestration remains in package `council`; CLI backend adapters live behind a `Backend` interface; artifact persistence is isolated behind an `ArtifactStore` interface or concrete helper package. Public constructors return concrete structs; interfaces are accepted only at package boundaries for testability.

### 2.2 Data flow

1. Input enters through: `attest tech-spec review <run-id>` or `attest tech-spec review --from <path>`

2. Processing path:
   - Structural review (pre-flight gate)
   - Dynamic persona generation (Claude analyzes spec)
   - Parallel reviewer execution (4 fixed + up to 3 dynamic personas across claude/codex/gemini). Each reviewer receives an optional codebase context string (relevant source code or structure summary) passed via `CouncilConfig.CodebaseContext` from the CLI caller. The engine adapter is responsible for populating this field; it is not auto-detected.
   - Parallel judge instances (one per reviewer, Opus model)
   - Edit application (string replacement, no LLM)
   - Conflict resolution (batched Opus passes for failed edits if aggregate prompt exceeds context limit)
   - Spec version write + structural review refresh
   - Round N+1 receives all `ReviewOutput` objects from round N (pre-judge, unfiltered) as prior findings context. This lets later-round reviewers independently validate or contradict earlier findings.
3. Output artifacts: Versioned spec (`technical-spec-v1.md`), individual review JSONs, consolidation JSON, rejection log, updated active `technical-spec.md`

### 2.3 Concurrency and process model

- execution model: Reviewers run as parallel goroutines, each spawning a CLI subprocess. Judge instances also run in parallel.
- lifecycle: Each CLI invocation is a one-shot process (no session persistence). Nudge pass is disabled pending session resume implementation.
- failure/restart behavior: Individual reviewer failures are non-fatal (logged, skipped). Judge failures per-reviewer are non-fatal. Persona generation failure is fatal. Review results are cached by spec hash — re-running resumes from where it left off.
- cancellation contract: All council stages MUST accept and propagate `context.Context` from the top-level CLI command. Each reviewer/judge subprocess MUST be started with a cancellable context (`exec.CommandContext`). On cancellation or deadline expiry, the runner MUST close subprocess stdin, wait for a configurable grace period (default 5s), then perform platform-appropriate process-tree termination (`SIGTERM` → `SIGKILL` on Unix) before returning. The orchestrator MUST wait for all spawned goroutines to exit before completing the command.
- error propagation rules: (1) Runner returns all successful reviews even if some reviewers fail, along with logged errors; (2) Orchestrator aborts the round if zero reviews succeed; (3) Judge processes whatever reviews it receives — partial review sets are valid input. Section-level cache invalidation (to avoid full re-runs on minor edits) is deferred to a future iteration (see open questions).

## 3. Canonical artifacts and schemas

- **Persona config** (`personas.json`):
  - path: `.attest/runs/<run-id>/council/round-N/personas.json`
  - producer: Persona generator + fixed persona definitions
  - consumer: Runner (for prompt construction), judge (for attribution)
  - schema: Array of `Persona` objects with `persona_id`, `display_name`, `type` ("fixed"|"dynamic"), `perspective`, `review_instructions`, `focus_sections[]`, `backend`, `model_preference`, `generated_by`, `rationale`

- **Review output** (`review-<persona-id>.json`):
  - path: `.attest/runs/<run-id>/council/round-N/review-<persona-id>.json`
  - producer: CLI backend (claude/codex/gemini)
  - consumer: Judge, council orchestrator
  - schema: `ReviewOutput` with `verdict` (PASS/WARN/FAIL), `confidence` (HIGH/MEDIUM/LOW), `key_insight`, `findings[]` (each with `finding_id`, `severity`, `category`, `section`, `location`, `description`, `recommendation`, `fix`, `why`, `ref`), `schema_version: 3`

- **Consolidation result** (`consolidation.json`):
  - path: `.attest/runs/<run-id>/council/round-N/consolidation.json`
  - producer: Judge
  - consumer: Council orchestrator, human review
  - schema: `ConsolidationResult` with `round`, `applied_count`, `rejected_count`, `failed_edits[]` (each with `finding_id`, `persona_id`, `original_find`, `error`), `drift_warnings[]` (each with `section`, `change_type`, `detail`), `rejection_log` (inline for programmatic access).
  - validation: Enum-valued fields across all schemas (`persona.type`, `persona.backend`, `review.verdict`, `review.confidence`, `finding.severity`) MUST be validated against a closed set of Go constants at parse time. Invalid enum values MUST produce `ErrInvalidReviewJSON`.

- **Rejection log** (`rejection-log.json`):
  - path: `.attest/runs/<run-id>/council/round-N/rejection-log.json`
  - producer: Judge (extracted from `ConsolidationResult.RejectionLog` as a convenience artifact for human review)
  - consumer: Human review
  - schema: `RejectionLog` with `round`, `rejections[]` (each with `finding_id`, `persona_id`, `severity`, `description`, `rejection_reason`, `dismissal_rationale`, `cross_reference`)
  - note: Same data as `rejection_log` in `consolidation.json`, written separately for human inspection. Both are authoritative — `consolidation.json` for programmatic access, `rejection-log.json` for review.

- **Versioned spec** (`technical-spec-vN.md`):
  - path: `.attest/runs/<run-id>/council/technical-spec-vN.md`
  - producer: Council orchestrator (after judge consolidation)
  - consumer: Next review round, human review, downstream pipeline

- **Approval record** (returned by engine adapter):
  - producer: `engine/techspec.go`: `ApproveTechnicalSpec()`
  - consumer: Downstream pipeline, audit trail
  - schema: `ArtifactApproval` with `approved_by`, `approved_at`, `spec_hash`, `spec_version`

- **Spec hash** (`spec-hash.txt`):
  - path: `.attest/runs/<run-id>/council/round-N/spec-hash.txt`
  - producer: Runner
  - consumer: Runner (cache validation)
  - schema: SHA-256 hex string

### 3.1 Schema validation requirements

- Each JSON artifact (`personas.json`, `review-*.json`, `consolidation.json`, `rejection-log.json`) MUST have a corresponding Go struct with JSON tags that serves as the executable schema definition.
- Artifact write functions MUST validate against these structs before writing. Go's `json.Marshal` serializes zero values by default — validation of required fields is the caller's responsibility (e.g., `ValidateReviewOutput` for reviews).
- Cross-artifact invariants: every `finding_id` in `rejection-log.json` MUST reference an existing finding from a `review-*.json`; `spec-hash.txt` MUST match the SHA-256 of the spec bytes that were reviewed; `review-*.json` MUST have `schema_version: 3`.
- Test fixtures MUST include golden JSON files for each artifact type to validate schema compliance in CI.
- Cache key: computed over spec content, persona definition, backend identifier, model selection, prompt schema version, and review schema version. All inputs are concatenated in a deterministic order before hashing.
  - atomicity: A review artifact is cache-eligible only after it is fully written to a temporary file and atomically renamed into place. The runner MUST prevent concurrent writes for the same run-id/round/persona cache key via file-based coordination (e.g., lock file or atomic rename).

## 4. Interfaces

### 4.1 CLI surface

- `attest tech-spec review <run-id>`:
  - inputs: Run ID with drafted tech spec
  - outputs: Structural review JSON to stdout, council findings summary, updated spec
  - errors: Structural review failure blocks council; persona generation failure stops round

- `attest tech-spec review --from <path>`:
  - inputs: Path to spec file (creates temporary run automatically)
  - outputs: Same as above
  - errors: File not found, spec parse failure

- Flags:
  - `--structural-only`: Skip council, run structural checks only
  - `--dry-run`: Generate prompts without executing reviewers
  - `--force`: Bypass review cache, re-run all reviewers
  - `--round N`: Limit to N rounds (default: 2)
  - `--concurrency N`: Limit concurrent CLI subprocesses (default: 4)

- `attest tech-spec approve <run-id>`:
  - inputs: Run ID with reviewed tech spec
  - outputs: Approval record
  - errors: Review not passing, hash mismatch, unresolved `@@` annotations

- Error taxonomy: Council components MUST return errors classifiable into the following categories: `ErrBackendUnavailable` (CLI not installed or not responding), `ErrSubprocessFailed` (non-zero exit), `ErrReviewTimeout` (context deadline exceeded), `ErrInvalidReviewJSON` (malformed or schema-violating output), `ErrEmptyReview` (no findings produced), `ErrPersonaGenerationFailed` (dynamic persona step failed), `ErrJudgeFailed` (judge could not produce a consolidation). Policy decisions (fatal vs. skipped vs. retryable) MUST be based on `errors.Is`/`errors.As` matching against these classes. `context.Canceled` and `context.DeadlineExceeded` MUST be preserved unwrapped for caller detection.

### 4.2 Internal contracts

- **CouncilConfig** (council.go): Carries CLI flags to the orchestrator.
  - fields: `Rounds` (int), `CodebaseContext` (string), `DryRun` (bool), `Force` (bool), `SkipJudge` (bool), `SkipDynPersonas` (bool)

- **RoundResult** (types.go): Return type from `Runner.RunRound()` carrying reviews and consensus to the orchestrator.
  - fields: `round` (int), `personas` ([]Persona), `reviews` ([]ReviewOutput), `consensus` (Verdict)

- **CLIBackend** (runner.go): Maps persona backend names to CLI invocation config.
  - fields: `Command` (string), `Args` ([]string), `PromptFlag` (string)
  - registry: `KnownBackends` static map keyed by backend name (claude, codex, gemini)

- **CouncilResult** (council.go): Final pipeline output returned to the engine adapter.
  - fields: `rounds` ([]RoundResult), `consolidations` ([]ConsolidationResult), `overall_verdict` (Verdict), `final_spec_path` (string)

### 4.3 Consensus rules

- Verdict values: PASS, WARN, FAIL
- Consensus computation: All PASS → PASS; any FAIL → FAIL; mixed → WARN
- Empty reviews → FAIL

## 5. Verification

- Quality gate: `just check` (vet, lint, test with race detector, build)
- Acceptance scenarios:
  - **CR-TS-001**: Council review with fixed personas produces structured findings for a known-deficient spec
  - **CR-TS-002**: Dynamic persona generation detects Go as the primary language and generates a Go engineer persona
  - **CR-TS-003**: Judge applies valid findings as surgical edits without dropping existing spec content
  - **CR-TS-004**: Judge rejects factually incorrect findings with documented reasons
  - **CR-TS-005**: Conflict resolution recovers edits that failed due to parallel modification
  - **CR-TS-006**: `@@` annotations block approval until resolved
  - **CR-TS-007**: Cached reviews are invalidated when spec content changes
  - **CR-TS-008**: Reviewer failure is non-fatal — remaining reviewers complete and findings are consolidated
  - **CR-TS-009**: Drift detection flags removed sections after judge edits
  - **CR-TS-010**: Two-round pipeline feeds round 1 findings as context to round 2 reviewers
  - **CR-TS-011**: `--dry-run` writes prompt files to the round directory and produces no `review-*.json` files
  - **CR-TS-012**: `--structural-only` runs structural checks, writes structural review JSON, and does not invoke council pipeline
  - **CR-TS-013**: `approve` with hash mismatch exits non-zero, writes no approval record, and reports the mismatch
  - **CR-TS-014**: `approve` with unresolved `@@` annotations exits non-zero and lists the blocking annotations
  - **CR-TS-015**: `--force` bypasses review cache and re-invokes all reviewers even when `spec-hash.txt` matches
  - **CR-TS-016**: Empty or unparseable spec input fails pre-flight validation with no council round created
  - **CR-TS-017**: Malformed reviewer JSON marks that reviewer as failed, records parse error, and remaining reviewers complete normally
  - **CR-TS-018**: Duplicate `persona_id` in persona config is rejected before runner execution
  - **CR-TS-019**: Escaped `@@` text (e.g., in code blocks) is not treated as an approval-blocking annotation
- Scenario oracle format: Each acceptance scenario requires a concrete Given/When/Then definition. Oracles for scenarios that reference nondeterministic behavior:
  - **CR-TS-001**: Given `fixtures/deficient-spec.md` (missing architecture section, no error handling), when `attest tech-spec review --from fixtures/deficient-spec.md` in deterministic mode, then `review-*.json` files contain findings with severity >= significant referencing the missing sections
  - **CR-TS-002**: Given `fixtures/go-service.md` (Go-specific spec), when running persona generation in deterministic mode, then `personas.json` contains exactly one dynamic persona with `type=language-engineer` and `perspective` containing `Go`
  - **CR-TS-003**: Given fixture spec with 10 top-level headings, when judge applies 3 valid findings, then output spec retains all 10 original headings plus any new content
  - **CR-TS-004**: Given fixture findings including one factually wrong claim, when judge processes findings, then `rejection-log.json` contains entry for that finding with non-empty `rejection_reason`
  - **CR-TS-005**: Given two findings that edit the same paragraph, when sequential application fails on the second, then conflict resolution pass recovers the second edit and `consolidation.json` shows `failed_edits` decremented
  - **CR-TS-009**: Given a spec with 8 sections, when judge edits remove content from section 3, then `consolidation.json` `drift_warnings` includes an entry referencing section 3
  - **CR-TS-010**: Given round 1 produces 5 findings, when round 2 reviewer prompts are constructed, then each prompt contains the 5 round-1 findings as context
- Required evidence: `just check` passes, end-to-end dogfood run against attest tech spec
- Run-level invariants (enforced in CI and end-to-end verification):
  - Every round MUST preserve all pre-existing top-level headings unless a finding explicitly authorizes removal and the judge accepts it
  - `technical-spec.md` MUST byte-match the latest `technical-spec-vN.md` after each round
  - Version numbers MUST increase monotonically by 1 per applied round
  - Artifact arrays (findings, rejections) MUST be sorted by `finding_id` before write for deterministic diffing
  - `spec-hash.txt` MUST be written before any reviewer invocation in that round
- Non-functional validation: Full pipeline completes in under 10 minutes for a 66KB spec

## 6. Requirement traceability

- `CR-FR-001` (fixed personas) → `personas.go`: `FixedPersonas()` returns 4 focused personas
- `CR-FR-002` (dynamic personas) → `dynpersona.go`: `GeneratePersonas()` produces up to 3 dynamic personas
- `CR-FR-003` (parallel reviews) → `runner.go`: `runParallelReviews()` launches goroutines per persona
- `CR-FR-004` (judge consolidation) → `judge.go`: `RunJudge()` with parallel judgment + conflict resolution
- `CR-FR-005` (spec versioning) → `council.go`: Writes `technical-spec-vN.md` per round
- `CR-FR-006` (drift detection) → `judge.go`: `DetectDrift()` flags removed sections and shrinkage
- `CR-FR-007` (rejection tracking) → `judge.go`: `RejectionLog` with per-finding reasons
- `CR-FR-008` (annotation support) → `annotation.go`: `ParseAnnotations()` + approval gate
- `CR-FR-009` (review caching) → `runner.go`: Spec hash validation, `--force` bypass
- `CR-FR-010` (multi-backend) → `runner.go`: `BackendFor()` routes personas to claude/codex/gemini
- `CR-FR-011` (security triage) → `judge.go`: Judge prompt distinguishes current vs future phase findings
- `CR-FR-012` (consensus) → `types.go`: `ComputeConsensus()` with PASS/WARN/FAIL rules

### 6.1 Verification matrix

| Requirement | Test level | Test file/package | Backend mode | Evidence artifact |
|---|---|---|---|---|
| CR-FR-001 (fixed personas) | unit | personas_test.go | pure | assertion on persona count and IDs |
| CR-FR-002 (dynamic personas) | integration | dynpersona_test.go | stubbed CLI | personas.json golden fixture |
| CR-FR-003 (parallel reviews) | integration | runner_test.go | stubbed CLI | review-*.json golden fixtures |
| CR-FR-004 (judge consolidation) | integration | judge_test.go | stubbed CLI | consolidation.json golden fixture |
| CR-FR-005 (spec versioning) | unit | council_test.go | pure | file existence and content assertions |
| CR-FR-006 (drift detection) | unit | judge_test.go | pure | drift_warnings assertions |
| CR-FR-007 (rejection tracking) | unit | judge_test.go | pure | rejection-log.json golden fixture |
| CR-FR-008 (annotation support) | unit | annotation_test.go | pure | annotation parse assertions |
| CR-FR-009 (review caching) | integration | runner_test.go | stubbed CLI | cache hit/miss assertions |
| CR-FR-010 (multi-backend) | unit | runner_test.go | pure | backend routing assertions |
| CR-FR-011 (security triage) | integration | judge_test.go | stubbed CLI | phase-tagged finding assertions |
| CR-FR-012 (consensus) | unit | types_test.go | pure | verdict computation assertions |

## 7. Open questions and risks

- Open question: Session resume for nudge pass
  - impact: Nudge is currently disabled; re-enabling requires persistent CLI sessions (Claude supports it, Codex supports it, Gemini does not)
  - decision needed by: Before enabling nudge in production

- Open question: Parallel edit conflicts
  - impact: ~20% of edits fail on first pass due to parallel judges modifying the same text; conflict resolution recovers most but not all
  - decision needed by: Acceptable for now; may need section-based edit locking for specs with highly interdependent sections

- Open question: Cache invalidation granularity
  - impact: Full spec hash caching forces complete re-runs on minor edits (~16+ LLM calls per round); section-level hashing would enable partial invalidation
  - decision needed by: Before exposing to frequent iterative editing workflows

- Open question: Cost visibility
  - impact: A full pipeline run invokes 7+ LLM calls for reviews + 7+ for judge + 1 for conflict resolution + 1 for persona generation = ~16+ calls per round
  - decision needed by: Before exposing to users who pay per token

- Risk: Dynamic persona quality depends on the judge model's breadth
  - mitigation: Fixed personas cover the critical review areas; dynamic personas add domain depth but are not load-bearing

- Risk: Large specs (>100KB) may exceed model context limits for some backends
  - mitigation: All prompts are delivered via stdin (see runner I/O contract in §2.1); judge instances run in parallel (one per reviewer) but each processes only that reviewer's findings

## 8. Approval

- Drafted by: Claude Opus 4.6
- Reviewed by: (pending council review)
- Approved by: (pending)
- Approved artifact hash: (pending)
