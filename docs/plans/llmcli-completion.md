# fabrikk llmcli Epic Completion Plan
## fab-yv57 follow-up tickets: implementation + test coverage

**Branch:** `feat/llmcli-backends`
**Goal:** All 29 linked follow-up tickets either have their missing tests written or their implementation gap closed. `go test ./llmcli/...` and `go test ./llmclient/...` pass after each wave.

---

## Summary

The epic `fab-yv57` has 12 closed child tickets (MVP done) and 29 open linked follow-ups. Of those 29, **~14 are already implemented in code** but lack the acceptance-criteria tests. The remaining **~15 have genuine implementation gaps** ranging from small (grace period constant) to large (rewriting the codex-appserver protocol layer).

The plan splits work into **6 waves** ordered by dependency depth. Subagents can run tickets within a wave in parallel. Each wave ends with a `go test` validation gate.

---

## Wave 1: Foundation (no blockers, all parallel)

### fab-sswr — Add structuredStream wrapper
**Type:** Implementation + tests  
**Files:** `llmcli/stream.go` (new), `llmcli/stream_test.go` (new)  
**What to build:** A shared `structuredStream` function that wraps any backend's parse loop. It must:
- Accept `parserFunc func(ctx, out chan<- Event, te *terminalEmitter) error`
- Emit exactly one `EventStart` (carrying caller's Fidelity) followed by parser events, then exactly one terminal (`EventDone` or `EventError`)
- Invoke `DefaultObserver.OnStreamStart`, `OnEventEmitted` per event, `OnStreamEnd` after terminal
- Support `onClose func()` callback for resource cleanup after terminal is emitted
- Guard against double-terminal via `terminalEmitter.once`
- Return `<-chan llmclient.Event`

**Critical note for implementer:** The ticket's stated motivation ("Observer hooks never invoked") is **incorrect** — every backend already calls `observeStreamStart` + `observeStream`. The real value is **code deduplication** (centralizing the goroutine+terminal-switch pattern). Do not delete `metrics.go` or `observeStream`; `structuredStream` is an *alternative* wrapper that backends may adopt in later waves.

**Tests:** `TestStructuredStream_EmitsStartAndDone`, `TestStructuredStream_EmitsError`, `TestStructuredStream_InvokesObserver`, `TestStructuredStream_EnforcesSingleTerminal`, `TestStructuredStream_CtxCancelStopsCancelled`.

### fab-eopt — Typed ErrUnsupportedOption
**Type:** Implementation + tests  
**Files:** `llmclient/options.go`, `llmclient/options_test.go`  
**What to build:** Keep existing `ErrUnsupportedOption = errors.New(...)` sentinel. Add:
```go
type UnsupportedOptionError struct {
    Backend string
    Options []OptionName
}
func (e *UnsupportedOptionError) Error() string { ... }
func (e *UnsupportedOptionError) Is(target error) bool { return target == ErrUnsupportedOption }
func (e *UnsupportedOptionError) Unwrap() error { return ErrUnsupportedOption }
func NewUnsupportedOptionError(backend string, names ...OptionName) *UnsupportedOptionError
```
**Tests:** `TestUnsupportedOptionError_Typed`, `TestUnsupportedOptionError_ErrorString`.

**Scope boundary:** Do NOT modify backend callers in this ticket. Only define the type.

### fab-grce — gracePeriod on processSpec
**Type:** Implementation + tests  
**Files:** `llmcli/subprocess.go`, `llmcli/subprocess_test.go`  
**What to change:**
- Rename `defaultGracePeriod` → `defaultPerCallGracePeriod` and set to `2 * time.Second`
- Add `GracePeriod time.Duration` to `processSpec`
- Add `gracePeriod time.Duration` to `supervisor`, initialized from spec (zero → default)
- `supervisor.terminate` passes `s.gracePeriod` instead of hardcoded constant
- If tests need to observe the grace arg, add a package-level `terminateProcessTreeFn` var that defaults to the real `terminateProcessTree`

**Tests:** `TestSupervisor_GracePeriod_DefaultTwoSeconds`, `TestSupervisor_GracePeriod_Override`.

**Scope boundary:** Do NOT change persistent backend call sites (codex_appserver, opencode_http, omp_rpc). They keep the 2s default.

### fab-smvr — semver comparison + ProbeWarnings
**Type:** Implementation + tests  
**Files:** `llmcli/detect.go`, `llmcli/detect_test.go`  
**What to build:**
- `compareSemver(a, b string) int` — strip suffixes after `-`/`+`, split on `.`, parse up to 3 numeric parts, return -1/0/1. Non-numeric → return 0 (unknown).
- `minVersions` map: `claude: "1.0.0"`, `codex: "0.1.0"`, `codex-appserver: "0.2.0"`, `opencode: "0.3.0"`, `omp: "14.0.0"`
- `versionWarning(name, version string) string` — returns `"<name> version <version> below minimum <min>"` when below, empty otherwise.
- Add `VersionWarning string` to `CliInfo`. Populate in `DetectAvailableContext`.

**Tests:** `TestCompareSemver`, `TestVersionWarning_BelowMinimum`, `TestVersionWarning_SuffixTolerance`, `TestVersionWarning_EmptyVersionNoWarning`.

**Scope boundary:** Do NOT wire `VersionWarning` into backend `Capabilities.ProbeWarnings` yet. This ticket delivers the infrastructure only.

### fab-ophc — opencode-serve server-process lifetime
**Type:** Implementation + tests  
**Files:** `llmcli/opencode_http.go`, `llmcli/opencode_http_test.go`  
**What to change:** `ensureServer` currently binds `exec.CommandContext(ctx, ...)` to the *first Stream's* `ctx`. When that caller cancels, the shared server dies.
- Add `serverCtx context.Context` and `serverCancel context.CancelFunc` to `OpenCodeHTTPBackend`, initialized lazily in `ensureServer` via `context.WithCancel(context.Background())`.
- Replace `exec.CommandContext(ctx, ...)` with `exec.CommandContext(b.serverCtx, ...)`.
- Replace the lifecycle goroutine's `<-ctx.Done()` with `<-b.serverCtx.Done()`.
- `Close()` calls `b.serverCancel()` alongside existing `s.terminate(nil)` + `s.wait()`, guarded by `closeOnce`.
- Per-turn operations (`waitReady`, `discoverSchema`, `openSSEStream`, `createSession`, `sendPromptAsync`) continue using the per-turn Stream ctx.

**Tests:** `TestOpenCodeHTTP_ServerSurvivesFirstStreamCancel`, `TestOpenCodeHTTP_CloseTerminatesServer`.

---

## Wave 2: Backend fixes + test coverage (parallel, no Wave 1 deps except where noted)

### fab-o1la — codex-exec Ollama wiring
**Type:** Tests only  
**Status:** Code already implements all requirements. `buildCodexExecArgs` (line 148-149) appends `"--oss"` when `cfg.Ollama != nil`. `codexExecStaticCapabilities` advertises `OllamaRouting: true` and `OptionOllama: OptionSupportFull`. `checkCodexExecRequiredOptions` accepts it.

**Missing:** `TestBuildCodexExecArgs_Ollama` in `codex_test.go`.

**What to write:** Test that `buildCodexExecArgs` with `cfg.Ollama.Model = "gpt-oss:120b"` returns args containing `"--oss"` and `"--model"` with the Ollama model. Also `TestCodexExecRegistry_StaticCapabilities_Ollama` and `TestCheckCodexExecRequiredOptions_Ollama`.

### fab-o2la — codex-appserver Ollama wiring
**Type:** Tests only  
**Status:** Code already implements all requirements. `ensureProcess` (line 374-376) builds env with `codexAppServerOllamaEnvOverrides(*cfg.Ollama)` when `cfg.Ollama != nil`. `codexAppServerStaticCapabilities` advertises it. `checkCodexAppServerRequiredOptions` accepts it.

**Missing:** `TestCodexAppServer_Ollama_InjectsEnv` in `codex_appserver_test.go`.

**What to write:** Use the in-process `procFactory` test hook to capture env passed to the process factory and assert `OPENAI_BASE_URL` is present when `cfg.Ollama` is set.

### fab-o3la — opencode-run Ollama wiring
**Type:** Tests only  
**Status:** Code already implements all requirements. `writeTempOpenCodeConfig` (line 159-160) merges `buildOpenCodeOllamaConfigJSON(*cfg.Ollama).Providers` into the config. `openCodeRunStaticCapabilities` advertises it.

**Missing:** `TestOpenCodeRun_Ollama_WritesProviderBlock` in `opencode_run_test.go`.

**What to write:** Call `writeTempOpenCodeConfig` with a non-nil Ollama cfg, parse the written JSON, assert it contains `providers.ollama` with correct `baseUrl`. Also `TestOpenCodeRunRegistry_StaticCapabilities_Ollama`.

### fab-o4la — omp (print) Ollama wiring
**Type:** Tests only  
**Status:** Code already implements all requirements. `omp.go::Stream` (line 65) calls `resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg))` which applies Ollama env overrides. `ompPrintStaticCapabilities` advertises it.

**Missing:** `TestOmpPrint_Ollama_InjectsEnv` in `omp_test.go`.

**What to write:** Extract the env construction into a testable helper (or mock `startSupervised` via a test hook) and assert env contains `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN=ollama`.

### fab-o5la — omp-rpc Ollama wiring
**Type:** Tests only  
**Status:** Code already implements all requirements. `startOmpRPCProcess` (line 239-240) calls `applyOmpOllamaEnv(env, *ollama)` when `ollama != nil`. `ompRPCStaticCapabilities` advertises it.

**Missing:** `TestOmpRPC_Ollama_InjectsEnv` in `omp_rpc_test.go`.

**What to write:** Verify the env passed at process-start time contains `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN=ollama`.

### fab-clfd — claude Fidelity.OptionResults
**Type:** Tests only  
**Status:** Code already implements all requirements. `claudeInitFidelity(cfg)` (line 485-502) conditionally populates `OptionModel`, `OptionSession`, `OptionOllama` only when those fields are non-empty on `cfg`.

**Missing:** `TestClaudeFidelity_OptionResultsReflectInput` in `claude_test.go`.

**What to write:** Subtests: zero options → nil/empty OptionResults; WithModel only → OptionModel:Applied; WithSession only → OptionSession:Applied; WithOllama only → OptionOllama:Applied; all three → all three Applied.

### fab-txfc — codex-exec start-event Fidelity
**Type:** Tests only  
**Status:** Code already implements all requirements. `codex.go::Stream` (line 114) passes `codexExecFidelity(cfg, streaming)` (line 233-247) to `streamTextProcess`, which carries accurate OptionResults.

**Missing:** `TestCodexExec_StartEventOptionResults` in `codex_test.go`.

**What to write:** Subtests covering no options, WithModel only, WithOllama only, WithSession with multi-message input (asserts Warning about message history lost).

### fab-txfo — opencode-run start-event Fidelity
**Type:** Tests only  
**Status:** Code already implements all requirements. `opencode_run.go::Stream` (line 88) passes `openCodeRunFidelity(cfg, streaming)` (line 229-243) to `streamTextProcess`.

**Missing:** `TestOpenCodeRun_StartEventOptionResults` in `opencode_run_test.go`.

**What to write:** Same pattern as fab-txfc.

### fab-cpcx — codex-appserver CodexProfile unsupported
**Type:** Tests only  
**Status:** Code already implements all requirements. `codexAppServerStaticCapabilities` (line 598-616) does NOT include `OptionCodexProfile`. `checkCodexAppServerRequiredOptions` (line 568) rejects it via `EnforceRequired`. `codexAppServerFidelity` (line 573-594) reports `OptionCodexProfile: OptionUnsupported` when `cfg.CodexProfile != ""`.

**Missing:** `TestCodexAppServer_CodexProfile_Unsupported` in `codex_appserver_test.go`.

**What to write:** Subtest for required-case (returns ErrUnsupportedOption) and best-effort case (Fidelity.OptionResults[OptionCodexProfile] == OptionUnsupported).

### fab-ceof — codex-appserver EOF without done
**Type:** Implementation + tests  
**Files:** `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`  
**What to change:** In `readTurnEvents`, the `io.EOF` branch (line 454-458) currently calls `te.done(..., StopEndTurn)`. Change it to:
- Construct `fmt.Errorf("llmcli codex-appserver: app-server closed stdout before emitting 'done': %w", err)`
- Call `b.restartAfterProtocolError(protoErr)` to tear down `b.proc`
- Call `te.error(ctx, protoErr)` instead of `te.done`

**Tests:** `TestCodexAppServer_EOFWithoutDone_IsError` — drive backend via `procFactory` hook with a pipe that closes writer after partial events. Assert final event is `EventError` and `b.proc == nil` after channel close.

### fab-opds — opencode-serve discoverSchema retries
**Type:** Implementation + tests  
**Files:** `llmcli/opencode_http.go`, `llmcli/opencode_http_test.go`  
**What to change:** `Stream` (line 170-175) calls `b.schemaDiscovered.CompareAndSwap(0,1)` unconditionally. Change to only set it when `b.discoverSchema(ctx)` returns nil:
```go
if b.schemaDiscovered.Load() == 0 {
    if err := b.discoverSchema(ctx); err == nil {
        b.schemaDiscovered.Store(1)
    }
}
```

**Tests:** `TestOpenCodeHTTP_DiscoverSchema_RetriesOnFailure` — httptest.Server returns 500 on first `/doc` call, 200 on second. Assert `/doc` handler invoked twice and `schemaDiscovered == 1` only after second.

### fab-ophn — opencode-serve refuse WithOllama
**Type:** Implementation + tests  
**Files:** `llmcli/opencode_http.go`, `llmcli/opencode_http_test.go`  
**What to change:**
- Add `checkOpenCodeHTTPRequiredOptions` (already exists at line 546, just delegates to `EnforceRequired`). No change needed for required case.
- In `Stream`, build `Fidelity` before opening SSE. When `cfg.Ollama != nil`, set `Fidelity.OptionResults[OptionOllama] = OptionUnsupported` and append warning: `"opencode-serve does not support per-request Ollama routing; use opencode-run for Ollama-backed calls"`.
- The `openCodeHTTPStaticCapabilities` already does NOT advertise `OptionOllama`. Add explicit `OptionOllama: OptionSupportNone` for clarity.
- `parseOpenCodeSSE` currently builds Fidelity inline (line 557). Refactor so `Stream` builds the Fidelity and passes it into the parser (or have the parser accept a pre-built Fidelity). The start event must carry the correct OptionResults.

**Tests:** `TestOpenCodeHTTP_OllamaReportsUnsupported` (best-effort case), `TestOpenCodeHTTP_OllamaRequiredFailsBeforeSpawn` (required case + assert zero HTTP Do calls).

### fab-clut — claude parse usage tokens
**Type:** Implementation + tests  
**Files:** `llmcli/claude.go`, `llmcli/claude_test.go`  
**What to change:**
- Add nested `Usage *claudeResultUsage` to `claudeFrame` struct (line 334-347)
- Define `type claudeResultUsage struct { InputTokens int; OutputTokens int; CacheReadInputTokens int; CacheCreationInputTokens int }`
- In `handleClaudeResultFrame` (around line 598-627), when `frame.Usage != nil`, populate `Usage.InputTokens`, `OutputTokens`, `CacheReadTokens`, `CacheWriteTokens` from the frame. When `frame.Usage == nil` but `frame.TotalCostUSD > 0`, preserve existing `&llmclient.Usage{}` sentinel.
- Propagate parsed Usage onto `assembledMsg.Usage`.

**Tests:** `TestClaudeResult_ParsesUsageTokens` — JSONL fixture with `"usage":{"input_tokens":42,"output_tokens":7}` → assert done event's `Usage.InputTokens == 42`, `Usage.OutputTokens == 7`.

### fab-ppps — probePipeStreaming real implementation
**Type:** Implementation + tests  
**Files:** `llmcli/textstream.go`, `llmcli/textstream_test.go` (new)  
**What to build:** Replace the `return false` stub (line 21-23) with:
- Spawn binary at `path` with provided `args` (or `--version` if nil)
- Capture stdout via `bufio.NewReader`, read in loop with 2-second budget
- Record timing of byte arrivals
- Return true if ≥2 reads had >0 bytes with ≥10ms gap, OR if first Read returned while subprocess still running
- Cache result in package-level `sync.Map` keyed by `path + "\x00" + strings.Join(args, "\x00")`
- Use `startSupervised` for subprocess lifecycle so ctx cancel terminates cleanly

**Tests:** Add fixture modes `slow_stream` and `buffered_then_exit` to the existing test binary (see `subprocess_test.go` TestMain). Tests: `TestProbePipeStreaming_DetectsIncremental`, `TestProbePipeStreaming_DetectsBuffered`, `TestProbePipeStreaming_CachesResult`.

**Scope boundary:** Do NOT change any backend caller's probe args (codex.go and opencode_run.go still pass `nil`). This ticket makes the probe *function* work.

---

## Wave 3: Codex app-server overhaul (depends on Wave 2 ceof)

### fab-caps — NDJSON + real JSON-RPC method names
**Type:** Implementation + tests  
**Files:** `llmcli/internal/ndjson.go` (new), `llmcli/internal/ndjson_test.go` (new), `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`

**What to build (3 distinct areas):**

**1. New `internal/ndjson.go`:**
- `ReadLine(r *bufio.Reader, maxBytes int) ([]byte, error)` — wraps `ReadBoundedLine` (reuse from `jsonl.go`), strips CRLF, returns JSON payload
- `WriteLine(w io.Writer, v any) error` — `json.Marshal(v) + "\n"`
- Reuse `ErrLineTooLong` from `jsonl.go`
- Tests in `internal/ndjson_test.go`: normal, oversize, EOF-partial, multi-line buffer

**2. Rewrite `codex_appserver.go` protocol layer:**
- Replace `internal.ReadFrame` / `internal.WriteRequest` with `internal.ReadLine` / `internal.WriteLine`
- Implement real handshake: `initialize` → read response → `initialized` notification → `thread/start` → await `thread/started` (capture `thread.id`) → `turn/start` with `{threadId, input:[{type:"text", text: prompt}]}`
- The `initialize`/`thread/start` phase is **per-process**, not per-turn
- Replace dispatch table in `dispatchCodexFrame` with real method names:
  - `thread/started` → emit start event with `SessionID = params.thread.id`
  - `turn/started` → no-op (start already emitted)
  - `item/agentMessage/delta` → `EventTextStart` (first delta for item), `EventTextDelta` with `params.delta`
  - `item/completed` → `EventTextEnd` with accumulated content
  - `turn/completed` → terminal `EventDone`
  - `error` → terminal `EventError`
- Preserve `restartAfterProtocolError` for any read/parse failure
- Update `codexRPCFrame` / `codexEventParams` types to match real `ClientRequest.ts` / `ServerNotification.ts` shapes

**3. Tests in `codex_appserver_test.go`:**
- `TestCodexAppServer_NDJSONFraming` — mock emits NDJSON (no Content-Length), asserts events arrive
- `TestCodexAppServer_Handshake` — asserts outbound sequence: `initialize` → `initialized` → `thread/start` → `turn/start` with thread id echoed
- `TestCodexAppServer_AgentMessageDelta` — text deltas map with correct contentIndex

**Note for implementer:** `ReadFrame` / `WriteRequest` in `internal/jsonrpc.go` must NOT be deleted — they remain available for future LSP transports. The backend's `maxCodexBodyBytes` becomes the NDJSON line cap.

---

## Wave 4: OMP-RPC overhaul (depends on Wave 2 o5la)

### fab-ompp — fix set_host_tools payload shape
**Type:** Implementation + tests  
**Files:** `llmcli/omp_rpc.go`, `llmcli/omp_rpc_test.go`  
**What to change:**
- Define local `ompHostToolSpec` struct (do NOT modify `llmclient.Tool`):
  ```go
  type ompHostToolSpec struct {
      Name        string         `json:"name"`
      Description string         `json:"description"`
      Parameters  map[string]any `json:"parameters"`
      Label       string         `json:"label,omitempty"`
      Hidden      bool           `json:"hidden,omitempty"`
  }
  ```
- Map each `llmclient.Tool` to this shape: copy `Name`/`Description`, translate `InputSchema` → `Parameters`. If `InputSchema` is nil, default to `{"type":"object"}`.
- Extend command envelope to carry `id`:
  ```go
  type ompRPCHostToolsCmd struct {
      ID    string            `json:"id"`
      Type  string            `json:"type"`
      Tools []ompHostToolSpec `json:"tools"`
  }
  ```
- Generate `id` from per-process monotonic counter + crypto/rand suffix. Store id on `ompRPCProc`.
- Add `ompResponseFrame` decode in `parseOmpLines` (or sibling) that recognizes `{type:"response", command, id, success, data}` and silently consumes success responses. Failure responses for set_host_tools should surface as Stream error returned before the turn starts.

**Tests:** `TestOmpRPC_SetHostTools_PayloadShape` — assert JSON has `tools[0].parameters` and no `inputSchema`. `TestOmpRPC_SetHostToolsResponse` — mock emits response frame matching outbound id, assert no error propagates.

### fab-omth — host_tool_call round-trip
**Type:** Implementation + tests  
**Files:** `llmclient/options.go`, `llmclient/options_test.go`, `llmcli/omp_rpc.go`, `llmcli/omp_rpc_test.go`

**What to build:**

**llmclient/options.go:**
- Define `type HostToolResponder func(ctx context.Context, call ToolCall) (result []ContentBlock, isError bool, err error)`
- Add `HostToolResponder` field to `RequestConfig`
- Add `WithHostToolResponder(fn HostToolResponder) Option`
- Tests: `TestWithHostToolResponder`, `TestWithHostToolResponder_NilSafe`

**llmcli/omp_rpc.go:**
- Define inbound `host_tool_call` frame type with fields: `id`, `toolCallId`, `toolName`, `arguments`
- Define outbound `ompHostToolResultCmd { Type, ID, Result, IsError }`
- Extend `parseOmpLines` / dispatch to recognize `host_tool_call`:
  - Emit `EventToolCallStart` + `EventToolCallEnd` on stream channel
  - If `cfg.HostToolResponder != nil`, invoke on goroutine (with Stream ctx), serialize result back to omp stdin via `proc.enc.Encode(...)` — hold send mutex
  - If `cfg.HostToolResponder == nil`, immediately send `host_tool_result{isError:true, result:{content:[{type:"text", text:"no host tool responder configured"}]}}`
- Thread `cfg` into `parseOmpRPCTurn` (store on `ompRPCProc` or pass as param)
- Handle `host_tool_cancel` if feasible (cancel responder sub-context)

**Tests:** `TestOmpRPC_HostToolRoundTrip`, `TestOmpRPC_HostToolNoResponder`, `TestOmpRPC_HostToolResultCorrelation` (two concurrent calls with distinct ids, assert no cross-wiring).

---

## Wave 5: structuredStream adoption (depends on Wave 1 sswr + respective backend prerequisites)

### fab-clss — claude adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/claude.go`, `llmcli/claude_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 2 (fab-clut)  
**What to change:** Replace the bespoke goroutine in `Stream` (lines ~160-210) with `structuredStream` call:
- Build `parserFunc` wrapping `parseClaudeStream` + post-parse drain/wait/error-classification (same pattern as ticket describes)
- Pass fidelity from `claudeInitFidelity(cfg)` (already done in Wave 2)
- Pass model from `cfg.Model`
- Delete the old goroutine + terminal-switch block

**Tests:** `TestClaudeStream_ObserverFires` — install spy observer, drive Stream, assert `OnStreamStart` once, `OnEventEmitted` N times, `OnStreamEnd` once.

### fab-caxs — codex-appserver adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 3 (fab-caps, fab-citr)  
**What to change:** Replace bespoke goroutine in `Stream` with `structuredStream`:
- `parserFunc` wraps the handshake + turn-read loop
- `onClose: func() { b.turnCh <- struct{}{} }` to release turn semaphore after terminal
- If `thread/started` semantics complicate start-event timing, add `suppressStart bool` parameter to `structuredStream` (decided at implementation time; keep decision local to this file)

**Tests:** `TestCodexAppServer_ObserverFires`.

### fab-omss — omp (print) adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/omp.go`, `llmcli/omp_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 2 (fab-o4la)  
**What to change:** Replace goroutine in `Stream` (lines ~73-107) with `structuredStream`:
- `parserFunc` wraps `parseOmpStream` + post-parse drain/wait
- Move start-event emission from `ompOnReady` into `Stream`; `ompOnReady` only captures session id
- Pass fidelity from `ompPrintFidelity(cfg)` (already done)

**Tests:** `TestOmpStream_ObserverFires`.

### fab-opss — opencode-serve adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/opencode_http.go`, `llmcli/opencode_http_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 2 (fab-ophn)  
**What to change:** Replace bespoke goroutine in `Stream` with `structuredStream`:
- Build `*llmclient.Fidelity` in `Stream` (using fab-ophn's construction)
- `parserFunc` wraps `parseOpenCodeSSE` + prompt-error goroutine join
- `onClose: func() { cancelSSE(); sseResp.Body.Close() }`

**Tests:** `TestOpenCodeHTTP_ObserverFires`.

---

## Wave 6: Nice-to-have (can run in parallel with Waves 2-5)

### fab-citr — codex-appserver ThreadItem mapping
**Type:** Implementation + tests  
**Files:** `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`  
**Prerequisites:** Wave 3 (fab-caps)  
**What to build:** After NDJSON + real handshake lands, extend dispatch table:
- Discriminated-union `item` struct for `item/started` / `item/completed` with variants:
  - `dynamicToolCall`: `{id, tool, arguments, status}`
  - `mcpToolCall`: `{id, server, tool, arguments, status}`
  - `commandExecution`: `{id, command, cwd, status}` → treat as tool call named `commandExecution`
  - `agentMessage`: `{id, text, phase}`
  - `reasoning`: `{id, summary, content}`
- `item/started` with tool variant → `EventToolCallStart`
- `item/completed` with tool variant → `EventToolCallEnd` (use per-turn map keyed by `item.id` to correlate)
- `item/reasoning/textDelta` → `EventThinkingStart` (first), `EventThinkingDelta`, `EventThinkingEnd` on `item/completed`
- `item/reasoning/summaryTextDelta` → also emit as `EventThinkingDelta`
- Set `codexAppServerStaticCapabilities.Thinking = true`
- Unrecognized variants → skip silently (forward-compat)

**Tests:** `TestCodexAppServer_ToolCallMapping`, `TestCodexAppServer_MCPToolCallMapping`, `TestCodexAppServer_ReasoningMapping`. Update registry test to assert `Thinking: true`.

---

## Dependency Graph

```text
Wave 1 (foundation, all parallel)
├── fab-sswr ──┬──→ fab-clss (needs sswr + clut)
│              ├──→ fab-caxs (needs sswr + caps + citr)
│              ├──→ fab-omss (needs sswr + o4la)
│              └──→ fab-opss (needs sswr + ophn)
├── fab-eopt ──→ fab-omth (needs eopt + ompp)
├── fab-grce
├── fab-smvr
└── fab-ophc ──→ fab-opds ──→ fab-ophn ──→ fab-opss

Wave 2 (parallel, some need Wave 1)
├── fab-o1la ──→ fab-txfc (needs txfp + o1la, but txfp already done)
├── fab-o2la
├── fab-o3la ──→ fab-txfo (needs txfp + o3la, but txfp already done)
├── fab-o4la ──→ fab-omss
├── fab-o5la ──→ fab-ompp ──→ fab-omth
├── fab-clfd ──→ fab-clut ──→ fab-clss
├── fab-ceof ──→ fab-caps ──→ fab-citr ──→ fab-caxs
├── fab-cpcx (tests only)
├── fab-opds
├── fab-ophn ──→ fab-opss
├── fab-ppps
└── fab-txfc, fab-txfo (tests only)

Wave 3
└── fab-caps

Wave 4
└── fab-ompp ──→ fab-omth

Wave 5 (adoption, all parallel after prereqs)
├── fab-clss
├── fab-caxs
├── fab-omss
└── fab-opss

Wave 6
└── fab-citr (can parallel with Wave 5 if caps is done)
```

---

## Assumptions and Defaults

- **Line numbers are approximate.** The codebase has shifted since tickets were written. Implementers must search by function name, not line number.
- **Tests-only tickets:** The code already satisfies all acceptance criteria. The subagent's job is to write the missing test function(s) named in the ticket.
- **structuredStream scope:** `fab-sswr` creates the wrapper but does NOT modify any existing backend. Adoption happens in Wave 5.
- **No new dependencies:** `fab-smvr` implements semver comparison inline; no `golang.org/x/mod/semver`.
- **Backward compatibility:** `streamTextProcess` already accepts `*Fidelity` (nil → synthesize defaults). Existing callers compile without change.
- **Codex app-server protocol:** `internal/jsonrpc.go` ReadFrame/WriteRequest are preserved; only the app-server backend switches to NDJSON.

---

## Validation Gates Per Wave

After each wave, run:
```bash
go test ./llmclient/...
go test ./llmcli/...
go test ./llmcli/... -run "TestRegistry_RegisteredNamesComplete|TestSelectBackendByName_AllRegisteredBackends|TestOptionSupportMatrix" -v
```

After Wave 5 (full adoption), additionally run:
```bash
! grep -R "io.ReadAll(stdout)\|cmd.Output()\|cmd.CombinedOutput()\|bufio.NewScanner" -n llmcli
```

After all waves, run the epic's final gate:
```bash
go test ./...
```
