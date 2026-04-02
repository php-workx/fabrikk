---
id: fab-6z0n
status: closed
deps: []
links: []
created: 2026-03-28T13:59:29Z
type: task
priority: 1
assignee: Ronny Unger
tags: [daemon]
---
# Fix context-watcher goroutine accumulation across restarts

Each Start() at daemon.go:177 spawns a goroutine that reads d.listener via pointer. After restart, old goroutine still lives and closes the NEW listener on ctx cancel. Fix: capture listener by value: ln := d.listener; go func() { <-ctx.Done(); _ = ln.Close() }(). Same for idle timeout goroutine.

## Acceptance Criteria

After N restarts + ctx cancel, only the current listener is closed.

