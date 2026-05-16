# llmcli daemon hosting

`llmcli/daemon` contains the reusable process-hosting pieces for long-lived
programs that wrap `llmcli`: lockfiles, lifecycle cancellation, bounded queues,
and circuit breakers.

## Canonical pattern

```go
lc := daemon.NewLifecycle(daemon.LifecycleConfig{
    ShutdownTimeout: 5 * time.Second,
})

lock, err := daemon.AcquireLockfile(daemon.DefaultLockfilePath("llmcli-bridge"))
if err != nil {
    return err
}
defer lock.Release()

backend, err := llmcli.SelectBackendChain(ctx, []string{"claude", "codex-exec"})
if err != nil {
    return err
}
defer backend.Close()

cb := daemon.NewCircuitBreaker(&daemon.CircuitBreakerConfig{
    FailureThreshold: 3,
    ResetInterval:    10 * time.Second,
})

return lc.Run(ctx, func(ctx context.Context) error {
    if !cb.Allow() {
        return nil
    }
    // Read requests, call backend.Stream or llmclient.Collect,
    // then cb.RecordSuccess or cb.RecordFailure.
    return nil
})
```

## Backend lifetime

Per-call backends spawn one subprocess per `Stream` call and are safe to call
concurrently on one backend value:

- `claude`
- `codex-exec`
- `opencode-run`
- `omp`

Persistent backends reuse a subprocess or local server. They are safe to call
from multiple goroutines, but requests are serialized internally where the
underlying protocol requires one in-flight turn:

- `codex-appserver`
- `opencode-serve`
- `omp-rpc`

Use one backend instance for normal long-lived servers. If a persistent backend
reports `ReadyUnknown`, emits a protocol `EventError`, or `Stream` returns a
process/protocol error, call `Close()` and construct/select a fresh backend.
Use a small pool of backend instances only when a persistent backend becomes a
throughput bottleneck.
