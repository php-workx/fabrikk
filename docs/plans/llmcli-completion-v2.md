# fabrikk llmcli Epic Completion Plan — REVISED v2
## fab-yv57 follow-up tickets: implementation + test coverage

**Branch:** `feat/llmcli-backends`  
**Goal:** All 29 linked follow-up tickets closed. Every acceptance-criteria test passes. `go test ./...` passes after each wave.  
**Status of this plan:** Self-reviewed, findings addressed.

---

## Summary

The epic `fab-yv57` has 12 closed child tickets (MVP done) and **29 open linked follow-ups**. Of those 29:
- **9 are tests-only** — code already satisfies acceptance criteria; only the named tests are missing.
- **20 need implementation + tests** — from small fixes (grace period constant) to large rewrites (codex-appserver protocol layer).

Work is split into **7 waves** ordered by dependency depth. Subagents run tickets within a wave in parallel. Each wave ends with a tailored validation gate.

---

## Coding Conventions (apply to every ticket)

- **Line numbers are approximate.** Search by function / type name, never hardcode line numbers.
- **nolint annotations:** Every new helper taking `llmclient.RequestConfig` by value must include `//nolint:gocritic // RequestConfig value mirrors ...` on the function signature line. Follow existing patterns in the file.
- **Race safety:** After any change involving goroutines, channels, or `sync.Once`, run `go test ./llmcli/... -race -count=1`.
- **Forbidden patterns:** Never add `io.ReadAll(stdout)`, `cmd.Output()`, `cmd.CombinedOutput()`, or `bufio.NewScanner` to `llmcli/*.go` (existing `daemon/lockfile_test.go:473` is grandfathered).

---

## Wave 1: Foundation (no blockers, all parallel)

### fab-sswr — Add structuredStream wrapper
**Type:** Implementation + tests  
**Files:** `llmcli/stream.go` (new), `llmcli/stream_test.go` (new)  
**What to build:** A shared `structuredStream` function that centralizes the goroutine + terminal-switch pattern used by every backend.

**API:**
```go
// parserFunc reads from a backend-owned reader and emits events via out.
// It may also emit terminal events via te (done/error).  The wrapper
// guarantees exactly one terminal event on the channel.
type parserFunc func(ctx context.Context, out chan<- llmclient.Event, te *terminalEmitter) error

func structuredStream(
    ctx context.Context,
    backend string,
    sessionID string,
    model string,
    fidelity *llmclient.Fidelity,
    parseFn parserFunc,
    onClose func(),
    suppressStart bool,
) <-chan llmclient.Event
```

**Behavior:**
1. Create `ch := make(chan llmclient.Event, 16)`.
2. Create `te := newTerminalEmitter(ch)`.
3. Call `DefaultObserver.OnStreamStart(backend, model)`.
4. If `!suppressStart`, emit `EventStart` carrying `fidelity`.
5. Call `parseFn(ctx, ch, te)`.
   - When `suppressStart` is true, the parser is responsible for emitting the single `EventStart` (e.g., after `thread/started` provides the sessionID).
6. After `parseFn` returns, call `onClose()` if non-nil.
7. If `te.fired() == false`, emit a terminal based on `ctx.Err()` / `parseFn` error:
   - `ctx.Err() != nil` → `te.done(ctx, nil, nil, StopCancelled)`
   - `parseFn` returned non-nil → `te.error(ctx, parseErr)`
   - Otherwise → `te.done(ctx, nil, nil, StopEndTurn)`
8. Call `DefaultObserver.OnStreamEnd(backend, model, success, errType)`.

**Prerequisite change to terminalEmitter:**
Add a `fired() bool` method to `terminalEmitter` (in `llmcli/events.go`) that returns whether `once.Do` has been invoked. This is required so `structuredStream` can check whether the parser already emitted a terminal.

**Tests:** `TestStructuredStream_EmitsStartAndDone`, `TestStructuredStream_EmitsError`, `TestStructuredStream_InvokesObserver`, `TestStructuredStream_EnforcesSingleTerminal`, `TestStructuredStream_CtxCancelStopsCancelled`.

**Critical note:** The ticket's original motivation ("Observer hooks never invoked") is incorrect — every backend already calls `observeStreamStart` + `observeStream`. The real value of this wrapper is **code deduplication** plus ensuring Observer hooks have exactly one well-tested invocation site. In Wave 5, adoption tickets will **remove** the existing `observeStreamStart` / `observeStream` calls from each backend; `structuredStream` subsumes all observer logic.

---

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

**Scope boundary:** Do NOT modify backend callers in this ticket. Only define the type and constructor.

---

### fab-grce — gracePeriod on processSpec
**Type:** Implementation + tests  
**Files:** `llmcli/subprocess.go`, `llmcli/subprocess_test.go`  
**What to change:**
- Rename `defaultGracePeriod` → `defaultPerCallGracePeriod` and set to `2 * time.Second`
- Add `GracePeriod time.Duration` to `processSpec`
- Add `gracePeriod time.Duration` to `supervisor`, initialized from spec (zero → default)
- `supervisor.terminate` passes `s.gracePeriod` instead of hardcoded constant
- Add a package-level `terminateProcessTreeFn` variable (defaults to real `terminateProcessTree`) so tests can observe the grace arg

**Tests:** `TestSupervisor_GracePeriod_DefaultTwoSeconds`, `TestSupervisor_GracePeriod_Override`.

**Scope boundary:** Do NOT change persistent backend call sites (codex_appserver, opencode_http, omp_rpc). They keep the 2s default until a follow-up.

---

### fab-smvr — semver comparison + ProbeWarnings
**Type:** Implementation + tests  
**Files:** `llmcli/detect.go`, `llmcli/detect_test.go`, `llmcli/registry.go` (wire-up)  
**What to build:**
- `compareSemver(a, b string) int` — strip suffixes after `-`/`+`, split on `.`, parse up to 3 numeric parts, return -1/0/1. Non-numeric → return 0 (unknown, no warning).
- `minVersions` map: `claude: "1.0.0"`, `codex: "0.1.0"`, `codex-appserver: "0.2.0"`, `opencode: "0.3.0"`, `omp: "14.0.0"`
- `versionWarning(name, version string) string` — returns `"<name> version <version> below minimum <min>"` when below, empty otherwise.
- Add `VersionWarning string` to `CliInfo`. Populate in `DetectAvailableContext`.
- **Wire into `Capabilities.ProbeWarnings`:** In each backend's factory registration (e.g. `claude_registry.go`, `codex_registry.go`), when constructing `Capabilities`, copy `info.VersionWarning` into `Caps.ProbeWarnings` if non-empty. This satisfies the ticket's acceptance criteria.

**Tests:** `TestCompareSemver`, `TestVersionWarning_BelowMinimum`, `TestVersionWarning_SuffixTolerance`, `TestVersionWarning_EmptyVersionNoWarning`, `TestDetectAvailable_EmitsVersionWarning`.

---

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

## Wave 2A: Tests-Only Batch (all parallel, no production code changes)

These tickets already have working implementation in `feat/llmcli-backends`. Subagents write only the missing acceptance-criteria tests.

### fab-txfp — textstream: streamTextProcess accepts a full *Fidelity
**Type:** Tests only  
**Files:** `llmcli/textstream_test.go` (new)  
**Status:** The `streamTextProcess` signature already accepts `*llmclient.Fidelity` (line 40-45). The nil fallback synthesizes `StreamingBufferedOnly` + `ToolControlNone`. Non-nil fidelity is emitted on the start event.

**What to write:** `TestStreamTextProcess_UsesCallerFidelity` — spawn a trivial subprocess, pass a custom `*Fidelity{Streaming: StreamingTextChunk, OptionResults: {OptionModel: OptionApplied}, Warnings: []string{"test"}}`, assert the start event carries that exact Fidelity.

---

### fab-o1la — codex-exec Ollama wiring
**Type:** Tests only  
**Files:** `llmcli/codex_test.go`  
**Status:** `buildCodexExecArgs` (line 148-149) appends `"--oss"` when `cfg.Ollama != nil`. `codexExecStaticCapabilities` advertises `OllamaRouting: true` and `OptionOllama: OptionSupportFull`. `checkCodexExecRequiredOptions` accepts it.

**What to write:** `TestBuildCodexExecArgs_Ollama` — assert args contain `--oss` and the Ollama model. Also `TestCodexExecRegistry_StaticCapabilities_Ollama` and `TestCheckCodexExecRequiredOptions_Ollama`.

---

### fab-o2la — codex-appserver Ollama wiring
**Type:** Tests only  
**Files:** `llmcli/codex_appserver_test.go`  
**Status:** `ensureProcess` (line 372-374) builds env with `codexAppServerOllamaEnvOverrides(*cfg.Ollama)` when `cfg.Ollama != nil`. `codexAppServerStaticCapabilities` advertises it. `checkCodexAppServerRequiredOptions` accepts it.

**What to write:** `TestCodexAppServer_Ollama_InjectsEnv` — use the `procFactory` test hook to capture env and assert `OPENAI_BASE_URL` is present when `cfg.Ollama` is set. Also `TestCodexAppServerRegistry_StaticCapabilities_Ollama`, `TestCheckCodexAppServerRequiredOptions_Ollama`.

---

### fab-o3la — opencode-run Ollama wiring
**Type:** Tests only  
**Files:** `llmcli/opencode_run_test.go`  
**Status:** `writeTempOpenCodeConfig` (line 159-160) merges `buildOpenCodeOllamaConfigJSON(*cfg.Ollama).Providers` into the config. `openCodeRunStaticCapabilities` advertises it.

**What to write:** `TestOpenCodeRun_Ollama_WritesProviderBlock` — call `writeTempOpenCodeConfig` with Ollama cfg, parse written JSON, assert `providers.ollama` with correct `baseUrl`. Also `TestOpenCodeRunRegistry_StaticCapabilities_Ollama`, `TestCheckOpenCodeRunRequiredOptions_Ollama`.

---

### fab-o5la — omp-rpc Ollama wiring
**Type:** Tests only  
**Files:** `llmcli/omp_rpc_test.go`  
**Status:** `startOmpRPCProcess` (line 239-240) calls `applyOmpOllamaEnv(env, *ollama)` when `ollama != nil`. `ompRPCStaticCapabilities` advertises it.

**What to write:** `TestOmpRPC_Ollama_InjectsEnv` — verify env passed at process-start contains `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN=ollama`. Also `TestOmpRPCRegistry_StaticCapabilities_Ollama`, `TestCheckOmpRPCRequiredOptions_Ollama`.

---

### fab-clfd — claude Fidelity.OptionResults
**Type:** Tests only  
**Files:** `llmcli/claude_test.go`  
**Status:** `claudeInitFidelity(cfg)` (line 485-502) conditionally populates `OptionModel`, `OptionSession`, `OptionOllama` only when those fields are non-empty on `cfg`.

**What to write:** `TestClaudeFidelity_OptionResultsReflectInput` — subtests: zero options → nil/empty; WithModel only → OptionModel:Applied; WithSession only → OptionSession:Applied; WithOllama only → OptionOllama:Applied; all three → all three Applied.

---

### fab-txfc — codex-exec start-event Fidelity
**Type:** Tests only  
**Files:** `llmcli/codex_test.go`  
**Status:** `codex.go::Stream` (line 114) passes `codexExecFidelity(cfg, streaming)` (line 233-247) to `streamTextProcess`, carrying accurate OptionResults.

**What to write:** `TestCodexExec_StartEventOptionResults` — subtests: no options, WithModel only, WithOllama only, WithSession + multi-message input (assert Warning about history lost).

---

### fab-txfo — opencode-run start-event Fidelity
**Type:** Tests only  
**Files:** `llmcli/opencode_run_test.go`  
**Status:** `opencode_run.go::Stream` (line 88) passes `openCodeRunFidelity(cfg, streaming)` (line 229-243) to `streamTextProcess`.

**What to write:** `TestOpenCodeRun_StartEventOptionResults` — same pattern as fab-txfc.

---

### fab-cpcx — codex-appserver CodexProfile unsupported
**Type:** Tests only  
**Files:** `llmcli/codex_appserver_test.go`  
**Status:** `codexAppServerStaticCapabilities` does NOT include `OptionCodexProfile`. `checkCodexAppServerRequiredOptions` rejects it via `EnforceRequired`. `codexAppServerFidelity` (line 573-594) reports `OptionCodexProfile: OptionUnsupported` when `cfg.CodexProfile != ""`.

**What to write:** `TestCodexAppServer_CodexProfile_Unsupported` — subtest for required-case (returns ErrUnsupportedOption) and best-effort case (Fidelity.OptionResults[OptionCodexProfile] == OptionUnsupported).

---

## Wave 2B: Small Fixes (parallel, no cross-deps within this wave)

### fab-ceof — codex-appserver EOF without done
**Type:** Implementation + tests  
**Files:** `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`  
**What to change:** In `readTurnEvents`, the `io.EOF` branch (line 454-458) currently calls `te.done(..., StopEndTurn)`. Change to:
- Construct `fmt.Errorf("llmcli codex-appserver: app-server closed stdout before emitting 'done': %w", err)`
- Call `b.restartAfterProtocolError(protoErr)` to tear down `b.proc`
- Call `te.error(ctx, protoErr)` instead of `te.done`

**Tests:** `TestCodexAppServer_EOFWithoutDone_IsError` — drive backend via `procFactory` hook with a pipe that closes writer after partial events. Assert final event is `EventError` and `b.proc == nil` after channel close.

---

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

---

### fab-clut — claude parse usage tokens
**Type:** Implementation + tests  
**Files:** `llmcli/claude.go`, `llmcli/claude_test.go`  
**What to change:**
- Add nested `Usage *claudeResultUsage` to `claudeFrame` struct (line 334-347)
- Define `type claudeResultUsage struct { InputTokens int; OutputTokens int; CacheReadInputTokens int; CacheCreationInputTokens int }`
- In `handleClaudeResultFrame` (around line 598-627), when `frame.Usage != nil`, populate `Usage.InputTokens`, `OutputTokens`, `CacheReadTokens`, `CacheWriteTokens` from the frame. When `frame.Usage == nil` but `frame.TotalCostUSD > 0`, preserve existing `&llmclient.Usage{}` sentinel.
- Propagate parsed Usage onto `assembledMsg.Usage`.

**Tests:** `TestClaudeResult_ParsesUsageTokens` — JSONL fixture with `"usage":{"input_tokens":42,"output_tokens":7}` → assert done event's `Usage.InputTokens == 42`, `Usage.OutputTokens == 7`.

---

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

### fab-o4la — omp (print) Ollama wiring
**Type:** Implementation + tests  
**Files:** `llmcli/omp.go`, `llmcli/omp_test.go`  
**What to change:** Current code uses `resolveProcessEnv(cfg, claudeOllamaEnvOverrides(cfg))` which satisfies the *behavioral* requirement but not the *structural* requirement the ticket tests for (`grep applyOmpOllamaEnv`).
- Replace the env construction with:
  ```go
  var env []string
  if cfg.Ollama != nil {
      env = applyOmpOllamaEnv(os.Environ(), *cfg.Ollama)
  } else {
      env = resolveProcessEnv(cfg, nil)
  }
  ```
  Note: `resolveProcessEnv` handles `cfg.EnvironmentSet` / `cfg.EnvironmentOverlay`; when Ollama is not set, use it. When Ollama IS set, `applyOmpOllamaEnv` already applies the base env + overrides, but we may need to ALSO apply `EnvironmentOverlay`. Review `resolveProcessEnv` to ensure no env features are lost.
- `ompPrintStaticCapabilities` already advertises `OllamaRouting: true` and `OptionOllama: OptionSupportFull`. No change needed there.
- `checkOmpPrintRequiredOptions` already delegates to `EnforceRequired`, which will accept `OptionOllama`. No change needed.

**Tests:** `TestOmpPrint_Ollama_InjectsEnv` — assert env contains `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN=ollama`. Also `TestOmpPrintRegistry_StaticCapabilities_Ollama`, `TestCheckOmpPrintRequiredOptions_Ollama`.

---

## Wave 2C: OpenCode Serve Chain (sequential, not parallel)

These three tickets touch the same file (`opencode_http.go`) in overlapping areas. Run in sequence.

### fab-ophn — opencode-serve refuse WithOllama
**Type:** Implementation + tests  
**Files:** `llmcli/opencode_http.go`, `llmcli/opencode_http_test.go`  
**What to change:**
- `checkOpenCodeHTTPRequiredOptions` already exists (line 546) and delegates to `EnforceRequired`. No change needed for required case.
- In `Stream`, build Fidelity **before** opening SSE. When `cfg.Ollama != nil`, set:
  - `Fidelity.OptionResults[OptionOllama] = OptionUnsupported`
  - `Fidelity.Warnings = append(Fidelity.Warnings, "opencode-serve does not support per-request Ollama routing; use opencode-run for Ollama-backed calls")`
- `openCodeHTTPStaticCapabilities` — add explicit `llmclient.OptionOllama: llmclient.OptionSupportNone` for clarity.
- `parseOpenCodeSSE` currently builds Fidelity inline (line 557). Refactor so `Stream` builds the Fidelity and passes it into the parser. The start event must carry the correct OptionResults.

**Tests:** `TestOpenCodeHTTP_OllamaReportsUnsupported` (best-effort case), `TestOpenCodeHTTP_OllamaRequiredFailsBeforeSpawn` (required case + assert zero HTTP Do calls).

**Note:** This ticket is **blocked by fab-opds** because both touch the `Stream` function's early setup code (around lines 145-180). fab-opds must land first.

---

## Wave 3: Codex App-Server Overhaul (depends on Wave 2B ceof)

### fab-caps — NDJSON + real JSON-RPC method names
**Type:** Implementation + tests  
**Files:** `llmcli/internal/ndjson.go` (new), `llmcli/internal/ndjson_test.go` (new), `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`

**This ticket is large. Break it into 3 sequential phases for safe implementation:**

**Phase A — `internal/ndjson.go`:**
- `ReadLine(r *bufio.Reader, maxBytes int) ([]byte, error)` — thin wrapper over `ReadBoundedLine`, strips trailing CRLF, returns JSON payload ready for `json.Unmarshal`
- `WriteLine(w io.Writer, v any) error` — `json.Marshal(v) + "\n"` with single write
- Reuse `ErrLineTooLong` from `jsonl.go`
- Tests: normal, oversize, EOF-partial, multi-line buffer
- Validate: `go test ./llmcli/internal/...`

**Phase B — `codex_appserver.go` protocol rewrite:**
- Replace `internal.ReadFrame` / `internal.WriteRequest` with `internal.ReadLine` / `internal.WriteLine`
- Implement real handshake: `initialize` → read response → `initialized` notification → `thread/start` → await `thread/started` (capture `thread.id`) → `turn/start` with `{threadId, input:[{type:"text", text: prompt}]}`
- The `initialize`/`thread/start` phase is **per-process**, not per-turn
- Replace dispatch table in `dispatchCodexFrame` with real method names:
  - `thread/started` → emit start event with `SessionID = params.thread.id`
  - `turn/started` → no-op
  - `item/agentMessage/delta` → `EventTextStart` (first delta for item), `EventTextDelta` with `params.delta`
  - `item/completed` → `EventTextEnd` with accumulated content
  - `turn/completed` → terminal `EventDone`
  - `error` → terminal `EventError`
- Preserve `restartAfterProtocolError` for any read/parse failure
- Update `codexRPCFrame` / `codexEventParams` types to match real `ClientRequest.ts` / `ServerNotification.ts` shapes
- `ReadFrame` / `WriteRequest` in `internal/jsonrpc.go` must NOT be deleted
- Validate: `go test ./llmcli/... -run TestCodexAppServer`



**Protocol Shape Reference** (verified against codex-cli 0.128.0 via `codex app-server generate-ts`):

Key notification shapes the dispatcher must handle:
- `thread/started` → `{ thread: { id: string, ... } }` — capture `thread.id` as sessionID
- `turn/started` → `{ threadId: string, turnId: string }` — no-op (start already emitted)
- `item/agentMessage/delta` → `{ threadId, turnId, itemId, delta: string }` — emit `EventTextDelta`
- `item/completed` → `{ item: ThreadItem, threadId, turnId }` — emit `EventTextEnd` (for agentMessage variant)
- `turn/completed` → `{ threadId, turn: Turn }` — terminal `EventDone`
- `error` → `{ message: string, code?: number }` — terminal `EventError`
- `item/reasoning/textDelta` → `{ threadId, turnId, itemId, delta, contentIndex: number }` — emit `EventThinkingDelta`

Key `ThreadItem` variants (for `item/started` + `item/completed`):
- `agentMessage`: `{ id, text, phase }`
- `dynamicToolCall`: `{ id, namespace, tool, arguments, status, contentItems, success }`
- `mcpToolCall`: `{ id, server, tool, status, arguments, result, error }`
- `commandExecution`: `{ id, command, cwd, status, aggregatedOutput, exitCode }`
- `reasoning`: `{ id, summary: string[], content: string[] }`

Handshake sequence (per-process, not per-turn):
1. Client sends `initialize` request → awaits response
2. Client sends `initialized` notification (no response)
3. Client sends `thread/start` request → awaits `thread/started` notification
4. Capture `thread.id` from `thread/started`
5. Client sends `turn/start` request with `{ threadId, input: [{ type: "text", text: prompt }] }`

**Phase C — integration tests:**
- `TestCodexAppServer_NDJSONFraming` — mock emits NDJSON (no Content-Length), asserts events arrive
- `TestCodexAppServer_Handshake` — asserts outbound sequence: `initialize` → `initialized` → `thread/start` → `turn/start` with thread id echoed
- `TestCodexAppServer_AgentMessageDelta` — text deltas map with correct contentIndex
- Validate: `go test ./llmcli/...`

---

## Wave 4: OMP-RPC Overhaul (depends on Wave 2A o5la)

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
- Add `ompResponseFrame` decode in `parseOmpLines` (or sibling helper) that recognizes `{type:"response", command, id, success, data}` frames. Silently consume success responses. Failure responses for set_host_tools should surface as Stream error returned before the turn starts.

**Tests:** `TestOmpRPC_SetHostTools_PayloadShape`, `TestOmpRPC_SetHostToolsResponse`.

---

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
- 

**Design note for host_tool_call handling:**
- Use a `sync.Mutex` on `proc.enc` for ALL stdin writes (set_host_tools, host_tool_result, prompt, abort). The mutex prevents concurrent host_tool_result frames from interleaving on the same line-based JSONL stream.
- Each responder goroutine gets its own child context: `responderCtx, cancel := context.WithCancel(ctx)`. Store the `cancel` func in a per-process map `pendingResponders map[string]context.CancelFunc` keyed by the tool call `id`.
- On `host_tool_cancel { id, targetId }`, look up the cancel func and invoke it. The responder goroutine should check `responderCtx.Err()` before writing to stdin.
- The main parser goroutine must NOT block waiting for responders. The responder goroutine writes to stdin independently and returns; the parser continues reading stdout.

Thread `cfg` into `parseOmpRPCTurn` (store on `ompRPCProc` or pass as param)
- Handle `host_tool_cancel` if feasible (cancel responder sub-context)

**Tests:** `TestOmpRPC_HostToolRoundTrip`, `TestOmpRPC_HostToolNoResponder`, `TestOmpRPC_HostToolResultCorrelation`.

---

## Wave 5: structuredStream Adoption (depends on Wave 1 sswr + respective backend prerequisites)

**Adoption strategy:** Each backend replaces its bespoke goroutine + terminal-switch block with `structuredStream`. Each adoption ticket **removes** the existing `observeStreamStart` and `observeStream` calls — `structuredStream` subsumes all observer logic. Run `go test ./llmcli/... -race -count=1` after each adoption.

### fab-clss — claude adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/claude.go`, `llmcli/claude_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 2B (fab-clut)  
**What to change:**
1. Remove `model, started := observeStreamStart(...)` and `return observeStream(...)` calls.
2. Build `parserFunc` that wraps `parseClaudeStream` + post-parse drain/wait:
   ```go
   parseFn := func(ctx context.Context, out chan<- llmclient.Event, te *terminalEmitter) error {
       parseErr := parseClaudeStream(ctx, s.Stdout, out, te, claudeInitFidelity(cfg))
       _, _ = io.Copy(io.Discard, s.Stdout)
       waitErr := s.wait()
       if parseErr != nil && !errors.Is(parseErr, context.Canceled) {
           return parseErr
       }
       if waitErr != nil {
           return fmt.Errorf("subprocess: %w; stderr: %s", waitErr, s.stderrTail())
       }
       return nil
   }
   ```
3. Return `structuredStream(streamCtx, "claude", "", cfg.Model, claudeInitFidelity(cfg), parseFn, cancelTimeout)`.

**Tests:** `TestClaudeStream_ObserverFires` — spy observer, assert `OnStreamStart` once, `OnEventEmitted` N times, `OnStreamEnd` once.

---

### fab-caxs — codex-appserver adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/codex_appserver.go`, `llmcli/codex_appserver_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 3 (fab-caps) + Wave 6 (fab-citr)  
**What to change:**
1. Remove `observeStreamStart` / `observeStream`.
2. `parserFunc` wraps the handshake + turn-read loop (`readTurnEvents`).
3. `onClose: func() { b.turnCh <- struct{}{} }` to release turn semaphore after terminal.
4. Pass `suppressStart: true` to `structuredStream`. The parser emits the single `EventStart` after `thread/started` provides the real `sessionID`. The parser builds the fidelity in the handshake completion callback and emits it via `emit(ctx, ch, startEvent(sessionID, fidelity))` before processing turn events.

**Tests:** `TestCodexAppServer_ObserverFires`.

---

### fab-omss — omp (print) adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/omp.go`, `llmcli/omp_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 2C (fab-o4la)  
**What to change:**
1. Remove `observeStreamStart` / `observeStream`.
2. `parserFunc` wraps `parseOmpStream` + post-parse drain/wait.
3. Move start-event emission from `ompOnReady` into `Stream` (build fidelity there); `ompOnReady` only captures session id.

**Tests:** `TestOmpStream_ObserverFires`.

---

### fab-opss — opencode-serve adopt structuredStream
**Type:** Implementation + tests  
**Files:** `llmcli/opencode_http.go`, `llmcli/opencode_http_test.go`  
**Prerequisites:** Wave 1 (fab-sswr) + Wave 2C (fab-ophn)  
**What to change:**
1. Remove `observeStreamStart` / `observeStream`.
2. Build `*llmclient.Fidelity` in `Stream` (using fab-ophn's construction).
3. `parserFunc` wraps `parseOpenCodeSSE` + prompt-error goroutine join.
4. `onClose: func() { cancelSSE(); sseResp.Body.Close() }`.

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

## Dependency Graph (Revised)

```text
Wave 1 (foundation, all parallel)
├── fab-sswr ──┬──→ Wave 5: fab-clss (needs sswr + clut)
│              ├──→ Wave 5: fab-caxs (needs sswr + caps + citr)
│              ├──→ Wave 5: fab-omss (needs sswr + o4la)
│              └──→ Wave 5: fab-opss (needs sswr + ophn)
├── fab-eopt ──→ Wave 4: fab-omth (needs eopt + ompp)
├── fab-grce
├── fab-smvr
└── fab-ophc ──→ Wave 2C: fab-opds ──→ fab-ophn ──→ Wave 5: fab-opss

Wave 2A (tests-only, all parallel)
├── fab-txfp
├── fab-o1la ──→ Wave 2B: fab-txfc (needs txfp + o1la, both done)
├── fab-o2la
├── fab-o3la ──→ Wave 2B: fab-txfo (needs txfp + o3la, both done)
├── fab-o5la ──→ Wave 4: fab-ompp ──→ fab-omth
├── fab-clfd ──→ Wave 2B: fab-clut ──→ Wave 5: fab-clss
└── fab-cpcx, fab-txfc, fab-txfo (tests only)

Wave 2B (small fixes, all parallel)
├── fab-ceof ──→ Wave 3: fab-caps ──→ Wave 6: fab-citr ──→ Wave 5: fab-caxs
├── fab-opds
├── fab-clut
├── fab-ppps
└── fab-o4la ──→ Wave 5: fab-omss

Wave 2C (opencode-serve chain, sequential)
├── fab-opds
└── fab-ophn ──→ Wave 5: fab-opss

Wave 3 (codex-appserver overhaul)
└── fab-caps (phases A → B → C)

Wave 4 (omp-rpc overhaul)
├── fab-ompp
└── fab-omth

Wave 5 (adoption, all parallel after prereqs)
├── fab-clss
├── fab-caxs
├── fab-omss
└── fab-opss

Wave 6 (nice-to-have)
└── fab-citr (can parallel with Wave 5 if caps is done)
```

---

## Validation Gates Per Wave

| Wave | Packages touched | Gate commands |
|------|------------------|---------------|
| 1 | `llmclient/...`, `llmcli/...`, `llmcli/internal/...` | `go test ./llmclient/... && go test ./llmcli/... && go test ./llmcli/internal/...` |
| 2A | `llmcli/...` | `go test ./llmcli/...` |
| 2B | `llmcli/...` | `go test ./llmcli/...` |
| 2C | `llmcli/...` | `go test ./llmcli/...` |
| 3 | `llmcli/...`, `llmcli/internal/...` | `go test ./llmcli/... && go test ./llmcli/internal/...` |
| 4 | `llmclient/...`, `llmcli/...` | `go test ./llmclient/... && go test ./llmcli/...` |
| 5 | `llmcli/...` | `go test ./llmcli/... -race -count=1 && ! grep -R "io.ReadAll(stdout)\|cmd.Output()\|cmd.CombinedOutput()\|bufio.NewScanner" -n llmcli` |
| 6 | `llmcli/...` | `go test ./llmcli/...` |

**Epic final gate:**
```bash
go test ./...
```

---

## Epic Closure Criteria

`fab-yv57` (Implement llmcli package) may be closed when:
1. All 29 linked follow-up tickets are closed.
2. Every acceptance-criteria test named in each ticket passes.
3. `go test ./...` passes with race detector (`-race -count=1`) on `llmcli/...`.
4. The forbidden-buffering grep gate passes:
   ```bash
   ! grep -R "io.ReadAll(stdout)\|cmd.Output()\|cmd.CombinedOutput()\|bufio.NewScanner" -n llmcli
   ```
5. The "Schema TBD" gaps in `docs/specs/llm-cli.md` are resolved OR documented in a follow-up ticket with explicit acceptance criteria.
6. Ollama routing is verified across all 5 backends by their respective tests (claude, codex-exec, codex-appserver, opencode-run, omp-print, omp-rpc).
7. Observer hooks fire correctly: at minimum, `TestStructuredStream_InvokesObserver` and per-backend `Test*ObserverFires` tests pass.

**Cross-cutting verification note:** `grep -rn "observeStream" llmcli/daemon/ llmcli/bridge/` returned 0 matches — these packages do not reference `observeStream` or `observeStreamStart`. The functions are safe to remove from backend paths (Wave 5 adoption) but must be kept in `metrics.go` as public APIs. Run `go build ./llmcli/...` after Wave 5 to confirm no compile errors.

---

## Appendix A: Test Fixture Patterns

| Backend | Fixture approach |
|---------|-----------------|
| Claude | `claudeJSONLReader()` helper generates JSONL lines; pipe to `parseClaudeStream` |
| omp print | `parseOmpEventsForTest()` drives `parseOmpStream` with string slices |
| omp rpc | `parseOmpRPCTurn()` with `bufio.Reader` over string slices; mock stdin via `io.Pipe()` |
| codex-appserver | `procFactory` hook in `NewCodexAppServerBackend` replaces real subprocess with in-memory pipe |
| opencode-serve | `httptest.Server` with custom handlers for `/doc`, `/event`, `/session`, `/session/:id/prompt_async` |
| codex-exec / opencode-run | `subprocess_test.go` TestMain fixture binary; new modes added as CLI flags |

**Adding a new fixture mode:** In `llmcli/subprocess_test.go`, find `TestMain`, add a new `case` in the switch. The fixture binary reads a mode flag and prints deterministic output.

---

## Appendix B: Ticket Inventory

| Ticket | Wave | Type | Status |
|--------|------|------|--------|
| fab-sswr | 1 | Impl + tests | Open |
| fab-eopt | 1 | Impl + tests | Open |
| fab-grce | 1 | Impl + tests | Open |
| fab-smvr | 1 | Impl + tests | Open |
| fab-ophc | 1 | Impl + tests | Open |
| fab-txfp | 2A | Tests only | **Added** |
| fab-o1la | 2A | Tests only | Open |
| fab-o2la | 2A | Tests only | Open |
| fab-o3la | 2A | Tests only | Open |
| fab-o5la | 2A | Tests only | Open |
| fab-clfd | 2A | Tests only | Open |
| fab-txfc | 2A | Tests only | Open |
| fab-txfo | 2A | Tests only | Open |
| fab-cpcx | 2A | Tests only | Open |
| fab-ceof | 2B | Impl + tests | Open |
| fab-opds | 2B / 2C | Impl + tests | Open |
| fab-clut | 2B | Impl + tests | Open |
| fab-ppps | 2B | Impl + tests | Open |
| fab-o4la | 2B | Impl + tests | **Reclassified** |
| fab-ophn | 2C | Impl + tests | **Reclassified** |
| fab-caps | 3 | Impl + tests | Open |
| fab-ompp | 4 | Impl + tests | Open |
| fab-omth | 4 | Impl + tests | Open |
| fab-clss | 5 | Impl + tests | Open |
| fab-caxs | 5 | Impl + tests | Open |
| fab-omss | 5 | Impl + tests | Open |
| fab-opss | 5 | Impl + tests | Open |
| fab-citr | 6 | Impl + tests | Open |

**Total:** 29 tickets covered. 9 tests-only, 20 impl + tests.
