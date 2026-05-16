# Spec: claude-ipc — Persistent Claude CLI Backend

## Context

The current `claude` backend in `llmcli/claude.go` spawns a fresh subprocess per
`Stream()` call (100–500ms cold start per turn). The Claude CLI supports
`--input-format stream-json` which keeps the process alive and accepts multiple
turn requests on stdin — same output format as the per-call mode. The `clai`
project (`~/workspaces/clai/internal/claude/daemon.go`) proved this pattern
works: `claude --print --output-format stream-json --input-format stream-json
--verbose`.

A new `claude-ipc` backend reuses one long-lived Claude process per backend
instance, amortizing startup cost across all turns in a session. This mirrors the
`codex-appserver` (persistent JSON-RPC) and `omp-rpc` (persistent JSONL)
patterns already implemented.

---

## Process Command Line

```bash
claude -p "" --output-format stream-json --input-format stream-json --verbose
       [--model <model>] [--system-prompt <text>] [--no-session-persistence]
```

**Flag notes:**
- `-p ""` — empty prompt triggers non-interactive mode without an initial prompt.
  `--input-format stream-json` keeps the process alive after init.
- `--verbose` — **required** alongside `--output-format stream-json` for claude
  2.x; without it the process exits immediately after init.
- `--no-session-persistence` — prevents Claude from writing per-turn session JSON
  to `~/.claude/projects/*/`; reduces disk I/O for daemon use.
- `--replay-user-messages` — **NOT used**: re-emits user messages to stdout for
  acknowledgment, adding noise to parsing without benefit since we own the
  stdin encoder.
- System prompt, model, and working dir are baked in at process start and drive
  the routing key.

Working directory is set via `processSpec.Dir` (OS-level CWD for the
subprocess), **not** via `--add-dir` (`--add-dir` is a file-access allowlist
flag, not CWD).

---

## Architecture Overview

```text
ClaudeIPCBackend
  └── claudeIPCProc (one per backend, lazily started)
        ├── sup      *supervisor      owns the OS process (via startSupervisedWithStdin)
        ├── stdinW   io.WriteCloser   write end of stdin pipe (io.Pipe)
        ├── enc      *json.Encoder    JSONL encoder wrapping stdinW
        ├── sessionID string          captured from system/init frame at startup
        ├── cancel   context.CancelFunc  cancels procCtx (child of Background())
        └── turnMu   sync.Mutex       serializes concurrent turns
```

### Lifecycle Critical Invariant

`procCtx` MUST be derived from `context.Background()`, NOT from the per-turn
`Stream()` context. If derived from the stream ctx, the process is killed when
the first Stream call's timeout fires — the process would die after every turn,
defeating the entire purpose.

```go
procCtx, proc.cancel = context.WithCancel(context.Background())
```

`Close()` calls `proc.cancel()` to terminate the process.
Per-turn ctx is used ONLY inside the parsing goroutine, never for procCtx.

### Turn Serialization

`turnMu sync.Mutex` on `claudeIPCProc` serializes concurrent turns. Acquired
at Stream entry, released inside the parsing goroutine after it emits its
terminal event. OMP-RPC uses the same mutex pattern; it's simpler than a channel
semaphore since release always happens in the same goroutine chain.

### Cancellation Mid-Turn

If a Stream context is cancelled while a turn is in flight, the process stdout
has partially-consumed turn output — the protocol state is corrupted. Must call
`restartAfterProtocolError()` before releasing `turnMu`. Next `ensureProcess()`
starts a fresh process (new session). Document this cost in godoc.

---

## Startup Handshake

When the process starts, Claude emits the system/init frame **before receiving
any input**:

```json
{"type":"system","subtype":"init","session_id":"sid-abc...","model":"claude-opus-4-5"}
```

Function `waitForClaudeIPCReady(ctx context.Context, r *bufio.Reader) (sessionID string, err error)`:

- Reads lines via `internal.ReadBoundedLine(r, maxClaudeLineBytes)`
- Parses each as `claudeFrame`
- Returns when `frame.Type == "system" && frame.Subtype == "init"`; captures `frame.SessionID`
- Oversize lines: skip and continue
- Context cancel: return `"", ctx.Err()`
- EOF before init: return `"", io.ErrUnexpectedEOF`

**Startup probe timeout (stability guard):** Callers of `startClaudeIPCProcess`
wrap `ctx` with a hard deadline before passing it — default 10 seconds,
configurable via `FABRIKK_CLAUDE_IPC_INIT_TIMEOUT`. If the init frame does not
arrive within this window the process is torn down and the error is returned to
the `Stream()` caller with a clear message indicating the init probe failed
(distinct from a normal stream error). This makes CLI format regressions
immediately visible instead of hanging indefinitely.

No stdin message needs to be sent to trigger the init frame — it arrives
automatically at process start.

---

## Per-Turn Wire Format

**Stdin (one JSON line per turn):**
```json
{"type":"user","message":{"role":"user","content":"<last user message only>"}}
```

Content is always `llmclient.LastUserMessage(input)`. The process holds full
conversation history — no need to replay prior turns. System prompt is fixed
at process start.

**Stdout (receive):** Same stream-json format as per-call claude — assistant
frames then result frame. **No init frame between turns** (only at process start).

---

## Parser Refactor: `parseClaudeStreamFromState`

**`parseClaudeStream` cannot be reused verbatim for turns 2+** because:
1. It creates a fresh `claudeParseState` with `startEmitted=false`, `assembledMsg=nil`
2. No system/init frame arrives between turns → EventStart is never emitted
3. This violates the stream protocol (EventStart must be first per turn)
4. `assembledMsg` stays nil → done event carries nil message

**Solution — split `parseClaudeStream` into two layers in `claude.go`:**

```go
// parseClaudeStream — unchanged external signature, used by per-call ClaudeBackend
func parseClaudeStream(ctx, r, out, te, fidelity) error {
    state := &claudeParseState{startFidelity: fidelity}
    return parseClaudeStreamFromState(ctx, r, out, te, state)
}

// parseClaudeStreamFromState — internal; accepts pre-initialized state
// (unexported; contains the existing loop body verbatim)
func parseClaudeStreamFromState(ctx, r, out, te, state *claudeParseState) error {
    // existing loop body — no changes
}
```

**Add `parseClaudeIPCTurn` in `claude_ipc.go`** (used for ALL IPC turns, including turn 1 — by the time we call it, the init frame has already been consumed by `waitForClaudeIPCReady`):

```go
func parseClaudeIPCTurn(
    ctx context.Context,
    r *bufio.Reader,
    out chan<- llmclient.Event,
    te *terminalEmitter,
    fidelity *llmclient.Fidelity,
    sessionID string,
) error {
    if !emit(ctx, out, startEvent(sessionID, fidelity)) {
        return ctx.Err()
    }
    state := &claudeParseState{
        startEmitted:  true,
        sessionID:     sessionID,
        startFidelity: fidelity,
        assembledMsg:  &llmclient.AssistantMessage{Role: "assistant"},
    }
    return parseClaudeStreamFromState(ctx, r, out, te, state)
}
```

`claudeParseState` is already in `claude.go` (same package) — `claude_ipc.go` can
reference it directly.

---

## Routing Key & Process Restart

```go
func claudeIPCRoutingKey(cfg llmclient.RequestConfig, input *llmclient.Context) string {
    systemPrompt := ""
    if input != nil {
        systemPrompt = input.SystemPrompt
    }
    h := fmt.Sprintf("%x", sha256.Sum256([]byte(systemPrompt)))[:8]
    ollama := ""
    if cfg.Ollama != nil {
        ollama = ollamaEffectiveBaseURL(*cfg.Ollama) + cfg.Ollama.Model
    }
    return cfg.Model + "\x00" + h + "\x00" + cfg.WorkingDirectory + "\x00" + ollama
}
```

`ensureProcess()` logic:
```text
b.mu.Lock()
if proc != nil && proc.alive() && routingKey == b.routingKey:
    b.mu.Unlock(); return proc, nil   // fast path: reuse
// teardown stale proc
old := b.proc; b.proc = nil; b.routingKey = ""
b.mu.Unlock()
if old != nil: old.cancel(), old.stdinW.Close(), io.Copy(Discard, old.sup.Stdout), old.sup.wait()
// start fresh
proc, err = startClaudeIPCProcess(...)
b.mu.Lock(); b.proc = proc; b.routingKey = routingKey; b.mu.Unlock()
```

`proc.alive()`:
```go
func (p *claudeIPCProc) alive() bool {
    select { case <-p.sup.done: return false; default: return true }
}
```

---

## Struct Definitions

```go
type ClaudeIPCBackend struct {
    CliBackend
    mu         sync.Mutex
    closed     bool
    proc       *claudeIPCProc
    routingKey string
    // procFactory replaces startSupervisedWithStdin in tests.
    procFactory func(ctx context.Context, spec processSpecWithStdin) (*supervisor, error)
}

type claudeIPCProc struct {
    sup       *supervisor
    stdinW    io.WriteCloser
    enc       *json.Encoder
    sessionID string
    cancel    context.CancelFunc
    turnMu    sync.Mutex
}

type claudeIPCUserMessage struct {
    Type    string `json:"type"`
    Message struct {
        Role    string `json:"role"`
        Content string `json:"content"`
    } `json:"message"`
}
```

`processSpecWithStdin` and `startSupervisedWithStdin` are defined in
`llmcli/claude.go` — same package, no import needed.

---

## `Stream()` Goroutine Sketch

```go
go func() {
    defer cancelTimeout()
    defer te.close() // safety guard

    var restarted bool
    defer func() {
        if !restarted {
            proc.turnMu.Unlock()
        }
    }()

    // Send turn to stdin
    msg := claudeIPCUserMessage{Type: "user"}
    msg.Message.Role = "user"
    msg.Message.Content = llmclient.LastUserMessage(input)
    if err := proc.enc.Encode(msg); err != nil {
        proc.turnMu.Unlock()
        restarted = true
        b.restartAfterProtocolError()
        te.error(streamCtx, fmt.Errorf("claude-ipc: send: %w", err))
        return
    }

    fidelity := claudeIPCInitFidelity(cfg)
    parseErr := parseClaudeIPCTurn(streamCtx, proc.sup.Stdout, ch, te, fidelity, proc.sessionID)

    switch {
    case parseErr != nil &&
         !errors.Is(parseErr, context.Canceled) &&
         !errors.Is(parseErr, context.DeadlineExceeded):
        proc.turnMu.Unlock()
        restarted = true
        b.restartAfterProtocolError()
        te.error(ctx, fmt.Errorf("claude-ipc: parse: %w", parseErr))
    case streamCtx.Err() != nil:
        // Mid-turn cancel corrupts stdout state
        proc.turnMu.Unlock()
        restarted = true
        b.restartAfterProtocolError()
        te.done(streamCtx, nil, nil, llmclient.StopCancelled)
    default:
        // Normal completion — parseClaudeIPCTurn emitted done via te
        // turnMu released by defer
    }
}()
```

---

## Capabilities

```go
llmclient.Capabilities{
    Backend:       "claude-ipc",
    Streaming:     llmclient.StreamingStructured,
    ToolEvents:    true,
    MultiTurn:     true,
    Thinking:      true,
    Usage:         true,   // aligns with per-call; fab-clut adds token parsing
    OllamaRouting: true,   // env overrides at startup + routing key restart
    OptionSupport: map[llmclient.OptionName]llmclient.OptionSupport{
        llmclient.OptionModel:              llmclient.OptionSupportFull,
        llmclient.OptionSession:            llmclient.OptionSupportNone, // session is internal
        llmclient.OptionOllama:             llmclient.OptionSupportFull,
        llmclient.OptionWorkingDirectory:   llmclient.OptionSupportFull,
        llmclient.OptionEnvironment:        llmclient.OptionSupportFull,
        llmclient.OptionEnvironmentOverlay: llmclient.OptionSupportFull,
        llmclient.OptionTimeout:            llmclient.OptionSupportFull,
        llmclient.OptionJSONSchema:         llmclient.OptionSupportPartial,
        llmclient.OptionRawCapture:         llmclient.OptionSupportFull,
    },
}
```

`OptionSession: OptionSupportNone` — external session IDs cannot be injected;
the session is owned by the persistent process. The internal session ID IS
reported in EventStart for observability.

---

## Files

| File | Action | Notes |
|------|--------|-------|
| `llmcli/claude.go` | Edit | Extract `parseClaudeStreamFromState`; `parseClaudeStream` wraps it |
| `llmcli/claude_ipc.go` | New | All backend code + `parseClaudeIPCTurn` + `waitForClaudeIPCReady` |
| `llmcli/claude_ipc_registry.go` | New | `init()` registration |
| `llmcli/claude_ipc_test.go` | New | 13 test cases via procFactory |
| `llmcli/registry_test.go` | Edit | Add `"claude-ipc"`; count 7 → 8 |

---

## Test Cases

| Test | Verifies |
|------|---------|
| `TestClaudeIPC_SingleTurn` | init consumed by handshake; assistant+result → correct events |
| `TestClaudeIPC_MultiTurn_SameProc` | two turns reuse same proc; sessionID consistent |
| `TestClaudeIPC_RoutingKeyRestart_ModelChange` | model change → proc restarted |
| `TestClaudeIPC_RoutingKeyRestart_SystemPromptChange` | system prompt change → restart |
| `TestClaudeIPC_ProtocolError_EmitsErrorAndClearsProc` | stdout closed mid-turn → EventError + proc==nil |
| `TestClaudeIPC_MidTurnCancel_RestartsProc` | ctx cancel mid-turn → StopCancelled + proc restarted |
| `TestClaudeIPC_Close_TerminatesProcess` | Close() cancels procCtx, drains, waits; proc==nil |
| `TestClaudeIPC_Ready_AfterClose` | Ready() returns ReadyUnknown |
| `TestClaudeIPC_ConcurrentStream_Serialized` | two concurrent Stream() calls serialize cleanly |
| `TestClaudeIPCRegistry_StaticCapabilities` | MultiTurn+OllamaRouting==true, OptionSession==None |
| `TestClaudeIPC_WaitForReady_EOFBeforeInit` | ErrUnexpectedEOF when stdout closes before init |
| `TestClaudeIPC_WaitForReady_ContextCancelled` | ctx.Err() propagated from waitForClaudeIPCReady |
| `TestClaudeIPC_ParseIPCTurn_EmitsStartFirst` | EventStart always first; carries correct sessionID |

---

## Wave / Ticket Breakdown

### Wave A — Core (sequential; B and C depend on A)

**fab-clid-01: Refactor `parseClaudeStream`**
- Extract loop body into unexported `parseClaudeStreamFromState`
- `parseClaudeStream` becomes a one-line wrapper
- Zero behavior change; all existing Claude tests must still pass
- Gate: `go test ./llmcli/... -run TestClaude`

**fab-clid-02: Backend struct + process lifecycle**
- All structs, `NewClaudeIPCBackend`, `Capabilities`, `Ready`, `Close`
- `startClaudeIPCProcess`, `waitForClaudeIPCReady`, `ensureProcess`
- `restartAfterProtocolError`, `claudeIPCRoutingKey`, `buildClaudeIPCArgs`
- procFactory hook
- Gate: `go build ./llmcli/...`

**fab-clid-03: `Stream()` + turn protocol + registration**
- `Stream()`, `parseClaudeIPCTurn`, `claudeIPCInitFidelity`
- `checkClaudeIPCRequiredOptions`, `claudeIPCStaticCapabilities`
- `claude_ipc_registry.go` + update `registry_test.go`
- Gate: `go test ./llmcli/...`

**fab-clid-04: Tests**
- All 13 test cases
- Gate: `go test ./llmcli/... -run TestClaudeIPC -v -race`

### Wave B — Hardening (parallel, after Wave A)

**fab-clid-05: Per-turn timeout without killing process**
- Verify timeout (DeadlineExceeded) is treated same as cancellation (restartAfterProtocolError)
- Test: timeout mid-turn → StopCancelled or EventError; next turn can still run (after restart)

**fab-clid-06: Large system prompt**
- `--system-prompt` hits OS arg limits at ~256KB
- Probe whether Claude CLI supports `--system-prompt-file`; if yes, use it for prompts > 64KB
- If not supported: document limit in godoc

### Wave C — Optional

**fab-clid-07: Idle timeout**
- Track last turn completion; `FABRIKK_CLAUDE_IPC_IDLE` env (default 2h)
- Background goroutine; idle → restartAfterProtocolError equivalent to free resources

**fab-clid-08: Tool result injection**
- Requires `WithHostToolResponder` from `fab-omth`
- Deferred until tool result wire format confirmed from Claude CLI docs

---

## Conformance Constraints

- `waitForClaudeIPCReady` and `parseClaudeIPCTurn` must use `internal.ReadBoundedLine`
- No `bufio.NewScanner`, `io.ReadAll`, `cmd.Output()`, `cmd.CombinedOutput()` in `claude_ipc.go`
- `TestNoForbiddenBuffering` will catch violations
- All `RequestConfig` helper params: `//nolint:gocritic // RequestConfig is passed by value`

---

## Verification

```bash
# Wave A: no regressions to per-call claude
go test ./llmcli/... -run TestClaude -v

# Wave A complete
go test ./llmcli/... -run TestRegistry_RegisteredNamesComplete -v

# Wave A + B: race detector
go test ./llmcli/... -race -count=1

# Forbidden-pattern gate
! grep -R "bufio\.NewScanner\|io\.ReadAll\|cmd\.Output()\|cmd\.CombinedOutput()" llmcli/claude_ipc.go

# Final
go test ./...
```
