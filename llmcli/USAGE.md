# llmcli Usage

## Stdio Binary

`cmd/llmcli` builds the generic stdio JSON-lines binary for non-Go
consumers. It registers the built-in op-agnostic handlers before entering the
request loop, so Rust or other clients can spawn the binary directly instead
of importing Go code.

Request frames are one JSON object per line:

```json
{"request_id":"r1","op":"text","context":{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}
```

Built-in ops:

- `ready`: returns backend readiness as `{backend,state,detail}`.
- `text`: streams one backend turn, collects assistant text, and returns
  `{text,reason}`.
- `json`: streams one backend turn, parses collected text as JSON with one
  retry on parse failure, and returns `{raw,value}`.

Cancellation is a separate frame with the same request ID:

```json
{"request_id":"r1","cancel":true}
```

The reusable `llmcli/bridge` package remains available for teams that need a
custom binary with additional consumer-specific ops.

## Long-Lived Hosting

Long-lived consumers may keep one `llmclient.Backend` for the lifetime of the
process and call `Stream` for each request. Use `Ready(ctx)` at startup and
after failures to distinguish an unavailable binary, missing authentication,
and uncertain persistent-process health.

`Available()` is the legacy boolean shortcut. New server-style consumers should
prefer `Ready(ctx)` because it preserves the reason.

## Concurrent Stream Calls

Per-call backends spawn a new subprocess for each `Stream` call and are safe to
call concurrently on one backend value:

- `claude`
- `codex-exec`
- `opencode-run`
- `omp`

Persistent backends reuse one subprocess or server. They are safe to call from
multiple goroutines, but turns are serialized internally because the underlying
protocols use one stdin/stdout or HTTP session owner:

- `codex-appserver`
- `opencode-serve`
- `omp-rpc`

If a workload needs higher parallelism on persistent backends, create a small
pool of backend instances and route requests across the pool. Call `Close()` on
each instance during shutdown.

## Health and Restart Pattern

For a server process:

1. Select a backend with `SelectBackendChain(ctx, []string{...})`.
2. Call `Ready(ctx)` before accepting requests.
3. For each request, call `Stream(ctx, input, opts...)` and either consume the
   event channel directly or use `llmclient.Collect`.
4. If `Stream` returns an error, an `EventError` arrives, or `Ready(ctx)` stops
   returning `ReadyOK`, call `Close()` and select or construct a fresh backend.

Per-call backends normally do not need restart logic beyond retrying the failed
request. Persistent backends should be recreated after protocol corruption,
process exit, or repeated `ReadyUnknown` reports.
