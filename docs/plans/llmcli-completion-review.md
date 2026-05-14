# Self-Review: llmcli Epic Completion Plan
## Brutally honest assessment of docs/plans/llmcli-completion.md

**Reviewer:** Author (self-review)  
**Date:** 2026-05-04  
**Verdict:** The plan has structural soundness but contains **8 critical flaws** that would cause subagent failure, **6 high-severity gaps** that would cause confusion or partial failure, and **4 medium issues** that would cause friction. It requires revision before dispatch.

---

## CRITICAL FLAWS (Fix before dispatch)

### 1. fab-ophn (opencode-serve refuse WithOllama) is NOT "tests-only"
**Claim in plan:** "Code already implements all requirements."
**Reality:** `llmcli/opencode_http.go` has **zero references** to Ollama. `openCodeHTTPFidelity` does NOT report `OptionOllama: OptionUnsupported`. `parseOpenCodeSSE` builds Fidelity inline without any Ollama check. There is no warning appended. The `openCodeHTTPStaticCapabilities` does NOT explicitly list `OptionOllama: OptionSupportNone`.

**Impact:** A subagent told "tests-only" would search for existing code, find nothing, and be blocked.

**Fix:** Reclassify as **implementation + tests**. Add explicit implementation instructions for building Fidelity in `Stream` with Ollama checks, and modifying `openCodeHTTPStaticCapabilities`.

---

### 2. fab-o4la (omp print Ollama wiring) may NOT be fully implemented
**Claim in plan:** "Code already implements all requirements. `omp.go::Stream` calls `resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg))` which applies Ollama env overrides."
**Reality:** The ticket's acceptance criterion explicitly says: `grep -c 'applyOmpOllamaEnv' llmcli/omp.go returns at least 1`. Current code uses `claudeOllamaEnvOverrides(cfg)` (a map-based helper) going through `resolveProcessEnv`. It does NOT call `applyOmpOllamaEnv(os.Environ(), *cfg.Ollama)`.

**Impact:** The test `TestOmpPrint_Ollama_InjectsEnv` would need to verify the exact code path the ticket specifies. The current implementation satisfies the *behavioral* requirement but not the *structural* requirement the ticket tests for.

**Fix:** Reclassify as **implementation + tests**. Explicitly instruct: replace `resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg))` with a conditional call to `applyOmpOllamaEnv` when `cfg.Ollama != nil`.

---

### 3. fab-ceof and fab-cpcx placed in parallel Wave 2, but fab-ceof is BLOCKED by fab-cpcx
**Ticket deps:** `fab-ceof [blocked by fab-cpcx]`
**Plan placement:** Both in Wave 2 with no sequencing.

**Impact:** If subagents dispatch Wave 2 in parallel, the fab-ceof agent may be confused by why `codexAppServerFidelity` handles `OptionCodexProfile` differently than expected, or may write tests that break when fab-cpcx lands.

**Fix:** Either sequence fab-cpcx before fab-ceof (but fab-cpcx is tests-only so the code is already there — they can technically be parallel) OR add an explicit note: "fab-ceof agent should verify that `codexAppServerFidelity` already reports OptionCodexProfile as unsupported; if not, escalate."

---

### 4. fab-ophn and fab-opds placed in parallel Wave 2, but fab-ophn is BLOCKED by fab-opds
**Ticket deps:** `fab-ophn [blocked by fab-opds]`
**Plan placement:** Both in Wave 2 with no sequencing. The dependency graph shows `fab-ophc → fab-opds → fab-ophn → fab-opss` but the Wave 2 listing presents them as parallel items.

**Impact:** fab-ophn changes the `schemaDiscovered` handling which fab-opds also touches. Running in parallel creates merge-conflict risk.

**Fix:** Move fab-ophn to a post-Wave-2 slot or create a Wave 2.5 for the opencode-serve chain.

---

### 5. fab-txfp is completely MISSING from the plan
**Claim:** The plan covers "all 29 linked follow-up tickets."
**Reality:** The plan has 27 ticket sections. `fab-txfp` (textstream: streamTextProcess accepts a full *Fidelity) is referenced only as a prerequisite for fab-txfc and fab-txfo but never gets its own section.

**Impact:** This ticket has open acceptance criteria and no one is assigned to close it.

**Fix:** Add fab-txfp as a Wave 2 tests-only ticket. It already has the implementation (signature is `*llmclient.Fidelity`) but needs `TestStreamTextProcess_UsesCallerFidelity`.

---

### 6. structuredStream adoption creates double Observer invocation
**Plan claim:** "Each backend adopts structuredStream in Wave 5."
**Reality:** Every existing backend already calls `observeStreamStart` + `observeStream`, which fire `OnStreamStart`, `OnSpawnDuration`, `OnEventEmitted`, and `OnStreamEnd`. If `structuredStream` ALSO fires these hooks, adoption would cause **double-counting** of all metrics.

**Impact:** Backends that adopt `structuredStream` must remove the `observeStream` wrapper. But the plan says "Do not delete `metrics.go` or `observeStream`." This is a direct contradiction.

**Fix:** Clarify the adoption strategy. Options:
- (A) `structuredStream` handles all observer hooks, and adoption tickets remove `observeStream` / `observeStreamStart` calls
- (B) `structuredStream` does NOT call observer hooks, and adoption tickets keep `observeStream` (but then what's the point?)
- (C) `structuredStream` accepts an optional `Observer` parameter; when nil, uses `DefaultObserver`; adoption tickets pass nil and keep existing `observeStream`

**Recommended:** Option A. Update the plan to explicitly say: "Adoption tickets must remove `observeStreamStart` and `observeStream` calls from `Stream`. The `structuredStream` wrapper subsumes all observer logic."

---

### 7. fab-caps scope is too large for a single subagent ticket
**Plan scope:** New `internal/ndjson.go`, rewrite handshake protocol, rewrite dispatch table, update frame types, 3 integration tests.

**Reality:** This is a protocol rewrite touching ~200 lines of core logic plus a new internal package. A subagent would struggle with the interplay between:
- NDJSON framing (new file)
- Handshake state machine (initialize → initialized → thread/start → turn/start)
- Dispatch table rewrite (replacing invented method names with real ones)
- Frame type updates

**Impact:** High probability of partial implementation or subtle bugs that only surface in integration.

**Fix:** Split fab-caps into 3 sequential sub-tickets:
- fab-caps-a: Add `internal/ndjson.go` with ReadLine/WriteLine + tests
- fab-caps-b: Rewrite `codex_appserver.go` handshake + dispatch (uses ndjson), keep text-only handling
- fab-caps-c: Add NDJSON + handshake integration tests

Or keep it as one ticket but add explicit sub-sections and acceptance criteria for each phase.

---

### 8. fab-smvr scope boundary contradicts the ticket's acceptance criteria
**Plan says:** "Do NOT wire VersionWarning into backend Capabilities.ProbeWarnings yet."
**Ticket acceptance criteria says:** "DetectAvailable attaches a ProbeWarnings entry of the form 'claude version 0.9.0 below minimum 1.0.0' when the detected version is older than the constraint, WITHOUT filtering the CLI out"

**Reality:** The ticket wants the warning to appear in `ProbeWarnings` (on `Capabilities` or `CliInfo`). The plan tells implementers not to wire it into `Capabilities.ProbeWarnings`.

**Impact:** Subagent implements per the plan, tests pass, but the ticket's acceptance criteria is not met because the warning doesn't surface to consumers.

**Fix:** Align with the ticket. The `CliInfo.VersionWarning` field should be wired into `Capabilities.ProbeWarnings` during backend factory construction. Or: if the plan truly wants to defer that wiring, the acceptance criteria must be renegotiated in the ticket.

---

## HIGH-SEVERITY GAPS

### 9. "Tests-only" classification is overstated and inconsistent
The plan claims 10 tickets are tests-only. Verified status:

| Ticket | Claim | Actual | Verdict |
|--------|-------|--------|---------|
| fab-o1la | Tests-only | `--oss` flag, caps, required check all exist | ✅ Correct |
| fab-o2la | Tests-only | Env injection, caps, required check all exist | ✅ Correct |
| fab-o3la | Tests-only | Config provider block, caps exist | ✅ Correct |
| fab-o4la | Tests-only | `applyOmpOllamaEnv` NOT called; uses different helper | ⚠️ **WRONG** |
| fab-o5la | Tests-only | `applyOmpOllamaEnv` IS called | ✅ Correct |
| fab-clfd | Tests-only | Conditional OptionResults exist | ✅ Correct |
| fab-txfc | Tests-only | Fidelity passed to streamTextProcess | ✅ Correct |
| fab-txfo | Tests-only | Fidelity passed to streamTextProcess | ✅ Correct |
| fab-cpcx | Tests-only | Unsupported reporting exists | ✅ Correct |
| fab-txfp | **MISSING** | Signature already `*Fidelity` | ❌ **MISSING** |

**Impact:** Subagents implementing fab-o4la as "tests-only" would write tests that grep for `applyOmpOllamaEnv` and fail.

**Fix:** Recategorize fab-o4la as implementation + tests. Add fab-txfp as tests-only.

---

### 10. Wave 2 mixes dependent tickets as parallel
The following tickets in Wave 2 have actual ticket-level dependencies but are listed as parallel:
- fab-ceof → depends on fab-cpcx
- fab-clut → depends on fab-clfd
- fab-ophn → depends on fab-opds
- fab-txfc → depends on fab-o1la + fab-txfp
- fab-txfo → depends on fab-o3la + fab-txfp

While tests-only dependencies don't create code conflicts, the Wave 2 narrative presents them as "all parallel." This is misleading.

**Fix:** Reorganize Wave 2 into two sub-waves:
- Wave 2A: Independent tickets (fab-o1la, o2la, o3la, o5la, clfd, cpcx, txfp, txfc, txfo)
- Wave 2B: Dependent tickets (fab-o4la, ceof, opds, clut, phn, ppps)

Or add explicit notes: "Tests-only tickets with dependencies on other tests-only tickets may be run in parallel because no production code changes."

---

### 11. The dependency graph is missing the fab-ophc → opds → ophn chain
The plan's ASCII graph shows:
```text
├── fab-opds
├── fab-ophn ──→ fab-opss
```
This implies fab-opds and fab-ophn are siblings. They are not.

**Fix:** Update the graph to show the linear chain correctly.

---

### 12. fab-omth should define `WithHostToolResponder`, not depend on fab-eopt for it
**Plan:** fab-eopt defines `UnsupportedOptionError` type. fab-omth depends on fab-eopt.
**Reality:** fab-omth needs `HostToolResponder` type and `WithHostToolResponder` option — neither of which are in fab-eopt's scope. The fab-omth ticket itself says these go in `llmclient/options.go`.

**Impact:** If fab-eopt is scoped to "type definition only" and fab-omth is scoped to "backend only," the `WithHostToolResponder` option falls through the cracks.

**Fix:** Either:
- Expand fab-eopt to include `WithHostToolResponder` (rename the ticket scope), OR
- Have fab-omth include the `llmclient/options.go` changes in its scope

**Recommended:** Add `WithHostToolResponder` to fab-omth's scope. It's a natural fit since the option only has meaning for omp-rpc's host_tool_call feature.

---

### 13. structuredStream `parserFunc` signature conflicts with existing parser patterns
**Plan signature:** `parserFunc func(ctx context.Context, out chan<- llmclient.Event, te *terminalEmitter) error`

**Existing patterns:**
- `parseClaudeStream(ctx, stdout, ch, te, fidelity)` — emits directly to `ch` (same channel as `te`)
- `parseOmpStream(ctx, stdout, ch, te, fidelity)` — same pattern
- `parseOpenCodeSSE(ctx, body, out, te)` — emits to `out` (same as `te`'s channel)

**Conflict:** The existing parsers emit directly to the channel that `te` also targets. The `structuredStream` wrapper would create its own internal channel and pass `out` to the parser. But `te` is constructed with the *outer* channel in existing code. If `structuredStream` passes a different `out` channel to the parser, `te` won't emit to the right place.

**Impact:** The `parserFunc` signature in the plan doesn't account for the fact that `te` must target the same channel that `out` writes to. Or: `structuredStream` must construct `te` internally and pass it to the parser, rather than letting the caller construct `te`.

**Fix:** Redesign the signature:
```go
type parserFunc func(ctx context.Context, out chan<- llmclient.Event) error
```
The `structuredStream` wrapper creates `te := newTerminalEmitter(ch)` internally. Parsers that need `te` (like `parseClaudeStream` which calls `te.done()` inside) receive it via a closure capture. The wrapper ignores `te` entirely and only looks at the parser's return error + ctx state.

Wait, but `parseClaudeStream` calls `te.done()` inside the parser. If `structuredStream` also tries to emit a terminal based on the parser's return, the `once` guard prevents double-fire. So the design is:
- `structuredStream` creates `ch` and `te`
- Calls `parseFn(ctx, ch)` — parser may call `te.done()` or `te.error()` internally
- After `parseFn` returns, `structuredStream` checks: did `te` already fire? If not, emit terminal based on error/ctx
- But `te` is unexported, so `structuredStream` can't check if it fired

**This is a fundamental design tension.** The existing parsers own terminal emission. `structuredStream` wants to own it. Both can't own it without coordination.

**Fix options:**
- Option A: Add a `fired() bool` method to `terminalEmitter` so `structuredStream` can check
- Option B: Parsers stop emitting terminals; `structuredStream` emits all terminals. Requires changing all 4 parsers.
- Option C: `structuredStream` ignores the parser's return value for terminal purposes and only uses it for `EventError` when `te` hasn't fired. Uses a try-send pattern.

**Recommended:** Option A (add `fired() bool` to `terminalEmitter`). Least invasive. Update the plan to include this.

---

### 14. No epic-closure criterion defined
The plan ends with `go test ./...` but doesn't say when `fab-yv57` itself closes.

**Missing criteria:**
- All 29 linked tickets are closed
- The "Schema TBD" gaps in `docs/specs/llm-cli.md` are resolved (or documented as still-open)
- The Observer hooks are verified to fire correctly (via `TestStructuredStream_InvokesObserver` or equivalent)
- Ollama routing works across all 5 backends (verified by tests)

**Fix:** Add an explicit "Epic Closure Criteria" section.

---

## MEDIUM ISSUES

### 15. Missing test fixture pattern guidance
The plan repeatedly says "use existing test fixture patterns" but doesn't describe what they are. New subagents won't know:
- `claude_test.go` uses `claudeJSONLReader()` to generate fixtures
- `omp_test.go` uses `parseOmpEventsForTest()`
- `codex_appserver_test.go` uses `procFactory` hooks
- `opencode_http_test.go` uses `httptest.Server`

**Fix:** Add a "Test Patterns Reference" appendix with 2-line descriptions of each backend's fixture approach.

---

### 16. Missing nolint/annotation guidance
The codebase uses `//nolint:gocritic` on many helpers that take `RequestConfig` by value. New code must follow this pattern. Subagents might not know.

**Fix:** Add a note: "All new helpers that accept `llmclient.RequestConfig` must include `//nolint:gocritic // RequestConfig value mirrors ...` on the same line as the function signature."

---

### 17. Race detector risk from Wave 5 adoption
`structuredStream` adoption changes goroutine lifetimes. The `llmcli` package already runs with race detection in CI. Any change to channel ownership or `sync.Once` usage is race-sensitive.

**Fix:** Add a note to each Wave 5 ticket: "Run `go test ./llmcli/... -race` after changes."

---

### 18. The wave validation gates are not tailored per wave
Every wave ends with the same commands, but:
- Wave 1 `fab-eopt` only touches `llmclient/...` — running `go test ./llmcli/...` is unnecessary
- Wave 3 `fab-caps` touches `llmcli/internal/...` — the gate should include that

**Fix:** Tailor each wave's gate to the packages actually touched.

---

## RECOMMENDATIONS FOR REVISION

1. **Reclassify fab-ophn and fab-o4la** as implementation + tests.
2. **Add fab-txfp** as a Wave 2 tests-only ticket.
3. **Restructure waves** to respect actual dependencies:
   - Wave 1: fab-sswr, fab-eopt, fab-grce, fab-smvr, fab-ophc
   - Wave 2A: Tests-only batch (all parallel)
   - Wave 2B: Small fixes (ceof, opds, clut, ppps, o4la)
   - Wave 2C: opencode-serve chain (ophn after opds)
   - Wave 3: fab-caps (split into 3 phases if possible)
   - Wave 4: fab-ompp → fab-omth
   - Wave 5: Adoption (clss, caxs, omss, opss)
   - Wave 6: fab-citr
4. **Resolve the structuredStream terminalEmitter design tension** by adding `fired() bool` to `terminalEmitter`.
5. **Clarify Observer strategy** — adoption removes `observeStream`, `structuredStream` subsumes it.
6. **Align fab-smvr scope** with the ticket's acceptance criteria (wire into ProbeWarnings).
7. **Add epic closure criteria** section.
8. **Add test patterns appendix** and nolint guidance.
9. **Add `-race` flag** to Wave 5 validation gates.

---

## VERDICT

The plan's dependency ordering and wave structure are approximately 70% correct. The critical issues (#1, #2, #5, #6, #7) would cause subagent failure or produce code that doesn't satisfy ticket acceptance criteria. Issues #3, #4, #8, #9, #10, #12, #13 would cause significant confusion and rework.

**Recommendation:** Revise the plan per the recommendations above, then re-review before dispatch.
