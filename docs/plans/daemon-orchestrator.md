# Plan: Daemon-Based Orchestrator Connection

**Status:** Planned
**Branch:** TBD
**Date:** 2026-03-24

---

## 1. Technical context

### Problem Statement

fabrikk invokes Claude CLI as one-shot subprocesses (`claude -p <prompt>`). Every invocation pays full startup cost: CLI initialization, hook loading, context assembly. For persona reviewers running in parallel this is fine — they're independent, ephemeral, and concurrent. But the **orchestrator/judge** role is different:

- It runs across the entire lifecycle of a run (spec review → plan → execution → verification)
- It processes outputs from every persona reviewer, every round
- It accumulates context about the run: what was approved, what was rejected, why
- It's invoked repeatedly and sequentially — each call re-discovers what the previous call already knew

A persistent daemon connection eliminates repeated startup cost and, more importantly, lets the orchestrator retain conversation context across invocations within a run.

### Prior Art: clai Daemon

The `clai` project (`~/workspaces/clai`) implements a Claude streaming daemon in `internal/claude/daemon.go`. Key design:

- **Unix socket** at `~/.cache/clai/daemon.sock` — lightweight IPC, no network overhead
- **Single Claude process** started with `--input-format stream-json --output-format stream-json` — keeps the process warm, sends queries as JSON over stdin, reads NDJSON responses from stdout
- **`QueryFast` pattern** — tries daemon first, falls back to one-shot CLI. Callers don't need to know which path is used.
- **Idle timeout** (2h default) — daemon shuts itself down when unused
- **Mutex-serialized queries** — one query at a time through the Claude process (sequential by design for the orchestrator role)

This is exactly the right pattern for fabrikk's judge/orchestrator: a warm process that accumulates context, queried sequentially, while reviewers run in parallel as separate processes.

### Design Decisions

#### Run-scoped daemon, not system-wide

Each `fabrikk run` gets its own daemon. This avoids cross-run context pollution and simplifies lifecycle management. The daemon dies when the run ends. No orphan process management needed beyond crash recovery.

#### Claude-only daemon (for now)

Only Claude CLI supports `--input-format stream-json`. Codex and Gemini backends remain one-shot. If other CLIs add streaming support, the daemon can be extended. This aligns with Claude being the primary judge/orchestrator backend.

#### Sequential queries through the daemon

The daemon serializes queries via mutex — one query at a time. This is correct for the judge/orchestrator role (it processes things sequentially). Parallel work uses one-shot processes. We do not try to multiplex concurrent queries through a single Claude conversation.

**Interaction with existing parallel judge fan-out:** The current `RunJudge` in `judge.go` fans out to parallel goroutines — one per reviewable review — each calling `InvokeFunc`. The daemon must NOT be wired into this parallel fan-out (it would serialize and regress performance). Instead, the daemon's `InvokeFn` is used only for consolidated judge queries (e.g., final synthesis across all reviews in a round). Per-reviewer judge goroutines continue using one-shot `agentcli.Invoke`.

#### Fallback to one-shot on daemon failure

If the daemon fails to start or crashes mid-run, all queries fall back to `agentcli.Invoke`. The run degrades gracefully — slower and without context accumulation, but still functional.

## 2. Architecture

### What stays one-shot

- **Persona reviewers** — spawned in parallel via `agentcli.Invoke`, torn down after each review. Different backends (Claude, Codex, Gemini), different models. Parallelism is the priority here.
- **Exploration agents** — dispatched for codebase exploration before planning. Ephemeral by nature.
- **Worker agents** — execute tasks in isolated worktrees. Fresh context per wave (Ralph pattern).

### What becomes persistent

- **The judge** — synthesizes reviewer findings, applies/rejects edits, decides if another round is needed. Currently in `councilflow/judge.go`. Receives a daemon-bound `InvokeFn` via `JudgeConfig`.
- **The orchestrator** — coordinates run phases (prepare → tech-spec → plan → execute → verify). Currently in `internal/engine/`. Receives a daemon-bound `InvokeFn` via struct field.

**Invocation path separation:** Reviewer call sites (`Runner`, `dynpersona`) must continue calling `agentcli.Invoke` directly. Only judge and orchestrator receive the daemon-bound `InvokeFn` via config/struct fields. The package-global `agentcli.InvokeFunc` must not be mutated during a run.

### Daemon Lifecycle

```
fabrikk run <spec>
  │
  ├─ StartDaemon()              ← warm up Claude process, create socket
  │    └─ claude --print --model opus --input-format stream-json --output-format stream-json
  │
  ├─ Phase: Spec Review
  │    ├─ Persona reviewers      ← parallel one-shot processes (unchanged)
  │    └─ Judge consolidation    ← QueryViaDaemon() — warm, has context
  │
  ├─ Phase: Planning
  │    ├─ Exploration agent      ← one-shot process
  │    └─ Plan council review    ← parallel one-shot reviewers + daemon judge
  │
  ├─ Phase: Execution
  │    ├─ Worker agents          ← parallel one-shot processes in worktrees
  │    └─ Orchestrator decisions ← QueryViaDaemon() — wave transitions, blocker resolution
  │
  ├─ Phase: Verification
  │    └─ Verifier checks        ← QueryViaDaemon() — evidence evaluation
  │
  └─ StopDaemon()               ← kill Claude process, remove socket
```

The daemon lives for the duration of a single `fabrikk run`. It is **not** a system-wide service — each run gets its own daemon with its own socket.

### Socket and Process Management

```go
// DaemonConfig controls the persistent orchestrator connection.
type DaemonConfig struct {
    Backend     CLIBackend    // which CLI backend to use (default: Claude Opus)
    SocketDir   string        // directory for the Unix socket (default: os.TempDir())
    IdleTimeout time.Duration // auto-shutdown after inactivity (default: 30m)
}
```

**Socket location:** `$TMPDIR/fabrikk-<run-id>.sock` — uses a short temp-dir path to avoid Unix socket path length limits (~104 bytes). Scoped per run via run ID. Multiple concurrent runs get separate daemons. The run directory MUST be created with mode 0700 and the socket file MUST be created with mode 0600, restricting access to the owning user.

**PID tracking:** `<run-dir>/daemon.pid` — for cleanup on crash.

**Startup sequence:**
1. Resolve Claude CLI path (existing `ResolveCommand`)
2. Check for existing socket; if present, try connect — if connection refused, `os.Remove(sockPath)`
3. Start process with `--input-format stream-json --output-format stream-json`
4. Send init message, wait for `type: "system", subtype: "init"`
5. Wait for init response `type: "result"`
6. Create Unix socket listener
7. Ready for queries

### Run-Scoped Daemon Management

```go
// Daemon manages a persistent Claude process for a single run.
type Daemon struct {
    config   DaemonConfig
    process  *claudeProcess
    listener net.Listener
    sockPath string
    pidPath  string
}

// Start launches the Claude process and begins accepting connections.
func (d *Daemon) Start(ctx context.Context) error { ... }

// Stop kills the Claude process and removes the socket.
func (d *Daemon) Stop() error { ... }

// SocketPath returns the Unix socket path for client connections.
func (d *Daemon) SocketPath() string { ... }
```

The daemon lifecycle is managed by the CLI command that orchestrates a full run (e.g., `fabrikk run` in `cmd/fabrikk/`). **Note:** `Engine.Run()` does not exist today — the engine exposes separate phase methods (`Prepare`, `Approve`, `Compile`, `VerifyTask`) called individually by CLI commands. The following pseudocode shows the integration pattern at the CLI command level:

```go
// In the CLI command that orchestrates a full run (cmd/fabrikk/):
daemon := agentcli.NewDaemon(agentcli.DaemonConfig{
    Backend:   agentcli.KnownBackends[agentcli.BackendClaude],
    SocketDir: os.TempDir(),
})
if err := daemon.Start(ctx); err != nil {
    logger.Warn("daemon start failed, falling back to one-shot", "err", err)
} else {
    defer daemon.Stop()
    judgeConfig.InvokeFn = daemon.QueryFunc()
}

// ... call engine phase methods (Prepare, Compile, etc.) ...
```

### Context Accumulation

The daemon's Claude process retains conversation history across queries within a run. This means:

- **Round 1 judge** sees reviewer findings and makes decisions
- **Round 2 judge** already knows what was accepted/rejected in round 1 — no need to re-send prior findings in the prompt
- **Plan review judge** has already seen the tech spec review — understands the spec's history
- **Verification judge** knows what the plan intended — can evaluate evidence against original intent

This is a significant quality improvement over the current approach where each judge invocation starts from scratch and must re-read all context from files.

### Env Filtering

Following clai's pattern, filter out `CLAUDECODE` env vars to prevent the daemon's Claude process from inheriting the parent Claude session's state:

```go
cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
```

### Implementation Order

#### Wave 1: Daemon core

1. **Stream types** — `stream.go` with `StreamMessage`, `StreamResponse`, `DaemonRequest`, `DaemonResponse`. Pure data types, no behavior.

2. **Daemon process** — `daemon.go` with `claudeProcess` (start, init handshake, query, shutdown), `Daemon` (socket listener, connection handler, lifecycle). Port from clai with adaptations: run-scoped socket path, configurable backend/model.

#### Wave 2: Integration

3. **Query client** — `Daemon.QueryFunc()` (closure returning `InvokeFn`), `Daemon.Query()`, `IsDaemonRunning` in `daemon.go`. Fallback to one-shot is built into the closure.

4. **Judge wiring** — Update `JudgeConfig` to accept an optional `InvokeFn`. When set, the judge uses the daemon connection. When nil, falls back to `agentcli.InvokeFunc` (current behavior). No breaking changes.

5. **Engine integration** — Start daemon at `Engine.Run`, stop on defer. Pass daemon's `QueryFunc` to judge config.

#### Wave 3: Hardening

6. **Crash recovery** — If the daemon process dies during query Q: (a) Q is retried once via one-shot `Invoke` and either succeeds or surfaces that error to the caller; (b) before the next daemon-eligible query, a fresh daemon is started; (c) old `<run-dir>/daemon.sock` and `<run-dir>/daemon.pid` are removed before the new daemon creates replacements; (d) conversation context from prior queries is lost — the run continues but subsequent judge queries no longer benefit from accumulated context. Log the crash and restart event.

7. **Graceful shutdown** — Context cancellation propagates to the Claude process. Socket cleanup on all exit paths (defer, signal handler, panic recovery).

## 3. Canonical artifacts and schemas

### Stream Types

```go
// StreamMessage is sent to Claude via stdin in stream-json format.
type StreamMessage struct {
    Type    string `json:"type"`
    Message struct {
        Role    string `json:"role"`
        Content string `json:"content"`
    } `json:"message"`
}

// StreamResponse is received from Claude via stdout in stream-json format.
type StreamResponse struct {
    Type    string `json:"type"`
    Subtype string `json:"subtype,omitempty"`
    Result  string `json:"result,omitempty"`
    Message struct {
        Content []struct {
            Type string `json:"type"`
            Text string `json:"text"`
        } `json:"content"`
    } `json:"message,omitempty"`
}

// DaemonRequest is sent by callers over the Unix socket.
type DaemonRequest struct {
    Prompt string `json:"prompt"`
}

// DaemonResponse is returned to callers over the Unix socket.
type DaemonResponse struct {
    Result string `json:"result,omitempty"`
    Error  string `json:"error,omitempty"`
}
```

### Files to Modify

| File | Change | Est. lines |
|------|--------|-----------|
| `internal/agentcli/daemon.go` | **NEW** — Daemon process management, socket listener, query client | ~250 |
| `internal/agentcli/daemon_test.go` | **NEW** — Daemon lifecycle tests using fixture process (start, query, context retention, stop, fallback, crash recovery). Tests must NOT invoke the real Claude CLI. A scripted fake process emits fixed `stream-json` init/result/error frames and can terminate on demand. | ~200 |
| `internal/agentcli/stream.go` | **NEW** — Stream-JSON types and parsing (StreamMessage, StreamResponse) | ~80 |
| `internal/agentcli/agentcli.go` | Add `IsDaemonRunning` helper | +10 |
| `cmd/fabrikk/` (run command) | Start/stop daemon at run boundaries, wire judge invoke function via config | +30 |
| `internal/councilflow/judge.go` | Accept `InvokeFn` parameter instead of using package-level `InvokeFunc` | ~10 (refactor) |

**Estimated total:** ~560 new/modified lines.

### Run-Scoped Artifacts

- **Socket:** `$TMPDIR/fabrikk-<run-id>.sock` — Unix domain socket, short path to avoid sun_path limits
- **PID file:** `<run-dir>/daemon.pid` — process ID for crash cleanup

## 4. Interfaces

### Daemon Query Integration

The daemon exposes a `QueryFunc()` method that returns a closure capturing the socket path. This closure is passed to consumers (judge, orchestrator) via their config structs — no global mutation, no shared state:

```go
// QueryFunc returns a closure that queries the daemon, falling back to one-shot on failure.
func (d *Daemon) QueryFunc() InvokeFn {
    return func(ctx context.Context, backend *CLIBackend, prompt string, timeoutSec int) (string, error) {
        result, err := d.Query(ctx, prompt)
        if err == nil {
            return result, nil
        }
        // Daemon failed, fall back to one-shot
        return Invoke(ctx, backend, prompt, timeoutSec)
    }
}
```

The engine passes `daemon.QueryFunc()` to `JudgeConfig.InvokeFn` and orchestrator config at run start. Persona reviewers continue using `agentcli.Invoke` directly — they need parallel execution, not shared context. The package-level `InvokeFunc` global is not mutated.

### Daemon API

```go
func (d *Daemon) Start(ctx context.Context) error { ... }
func (d *Daemon) Stop() error { ... }
func (d *Daemon) SocketPath() string { ... }

// Query sends a prompt to the persistent Claude process and returns the response.
// Safe for sequential use; the daemon serializes calls internally with a mutex.
// Callers do not need to coordinate access.
func (d *Daemon) Query(ctx context.Context, prompt string) (string, error) { ... }

// QueryFunc returns an agentcli.InvokeFn bound to this daemon instance.
// The returned function tries the daemon first, falls back to one-shot Invoke on failure.
func (d *Daemon) QueryFunc() agentcli.InvokeFn { ... }
```

## 5. Verification

1. `go build ./...` — compiles
2. `go test -race ./internal/agentcli/...` — daemon lifecycle tests pass
3. `golangci-lint run`
4. Manual: `fabrikk run <spec>` — verify daemon starts, judge queries go through socket, daemon stops on completion
5. Manual: kill daemon process mid-run — verify auto-restart and fallback behavior
6. Manual: verify no stale socket/PID files after normal and abnormal termination
7. Manual: verify persona reviewers still run as parallel one-shot processes (no regression)
8. Automated: context retention test — two sequential queries through the same daemon where the second query depends on information established only in the first. Must pass. After daemon restart, the same second query must fail or produce a degraded answer unless prior context is resent.

## 6. Requirement traceability

- **spec-decomp-planning.md §2.3** — The exploration agent dispatch uses one-shot `agentcli.Invoke` (not daemon). The daemon is for the judge that reviews exploration results.
- **spec-decomp-planning.md §2.5** — Council review of execution plans: persona reviewers are one-shot, the judge uses the daemon.
- **councilflow** — The judge in `councilflow/judge.go` is the primary consumer of the daemon connection.
- **Future: multi-backend daemons** — When Codex/Gemini support streaming, each could get its own daemon for their respective judge roles.

## 7. Open questions and risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Claude process crashes mid-run | Context lost, queries fail | Auto-restart daemon, fall back to one-shot for that query. Log the event. |
| Context window fills up after many queries | Claude starts dropping earlier context | Monitor token usage in stream responses. If approaching limit, restart daemon (clearing context) and log a warning. Do not attempt context summarization in MVP. |
| Socket cleanup fails on crash | Stale socket blocks next run | Check for stale sockets at startup (try connect → fail → remove). PID file validation. |
| `stream-json` format changes in Claude CLI | Daemon breaks | Pin to known format. Version-check Claude CLI at startup. |
| Daemon adds startup latency to every run | Slower cold start | Claude init takes ~5-10s. Amortized across a run that takes minutes to hours. Net positive. |

## 8. Approval

**Status:** Planned — not yet approved for implementation.
